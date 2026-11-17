//go:build platform

package platformquota

import (
	"context"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
)

type ExecutionFact string

const (
	FactAccepted       ExecutionFact = "accepted"
	FactUnknown        ExecutionFact = "unknown"
	FactSucceeded      ExecutionFact = "succeeded"
	FactNotAccepted    ExecutionFact = "not_accepted"
	FactFailedNoCost   ExecutionFact = "failed_no_cost"
	FactFailedConsumed ExecutionFact = "failed_consumed"
)

func terminal(state string) bool {
	return state == StateSettled || state == StateReleased || state == StateCompensated
}

// BeginSubmission 在外部调用前持久化提交边界。重启遇到 submitting 只能查询，不能再提交。
func (s *Service) BeginSubmission(ctx context.Context, workspaceID, intentID string) (bool, error) {
	allowed := false
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		allowed = false
		intent, err := loadIntent(tx, workspaceID, intentID)
		if err != nil {
			return err
		}
		if intent.State != StateReserved {
			return nil
		}
		var authorized int64
		err = tx.Table("platform_accounts AS a").
			Joins("JOIN platform_account_workspaces AS w ON w.account_id = a.id").
			Joins("JOIN platform_model_grants AS g ON g.account_id = a.id AND g.logical_model_id = ?", intent.LogicalModelID).
			Joins("JOIN logical_models AS m ON m.id = g.logical_model_id AND m.enabled = ? AND m.archived_at IS NULL", true).
			Joins("JOIN model_channels AS c ON c.id = ? AND c.enabled = ? AND c.deleted_at IS NULL", intent.ChannelID, true).
			Where("a.id = ? AND w.workspace_id = ? AND a.status = ? AND a.must_change_password = ?", intent.AccountID, intent.WorkspaceID, platformauth.StatusActive, false).Count(&authorized).Error
		if err != nil {
			return err
		}
		if authorized != 1 {
			return s.finalize(tx, intent, StateReleased, "提交前授权失效")
		}
		now := s.now()
		updated := tx.Model(intent).Where("state = ?", StateReserved).Updates(map[string]any{"state": StateSubmitting, "submitted_at": now, "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		allowed = updated.RowsAffected == 1
		return nil
	})
	return allowed, err
}

// CancelUnsubmitted 只释放尚未越过提交边界的预留；取消请求本身不证明上游未受理。
func (s *Service) CancelUnsubmitted(ctx context.Context, workspaceID, intentID string) error {
	return s.transaction(ctx, func(tx *gorm.DB) error {
		intent, err := loadIntent(tx, workspaceID, intentID)
		if err != nil {
			return err
		}
		if intent.State == StateReserved {
			return s.finalize(tx, intent, StateReleased, "提交前取消")
		}
		return nil
	})
}

// Observe 只接收服务端执行器确认的事实；HTTP 不提供用户填写结算事实的入口。
func (s *Service) Observe(ctx context.Context, workspaceID, intentID string, fact ExecutionFact) error {
	state := ""
	switch fact {
	case FactAccepted:
		state = StateAccepted
	case FactUnknown:
		state = StateUnknown
	case FactSucceeded:
		state = StateSettled
	case FactNotAccepted, FactFailedNoCost:
		state = StateReleased
	case FactFailedConsumed:
		state = StateCompensated
	default:
		return kernel.BadAuthRequest("未知的执行事实")
	}
	return s.transaction(ctx, func(tx *gorm.DB) error {
		intent, err := loadIntent(tx, workspaceID, intentID)
		if err != nil {
			return err
		}
		if terminal(intent.State) {
			if !terminal(state) || intent.State == state {
				return nil
			}
			return failure(409, "settlement_conflict", "预留已经终结，差异需要人工追加更正")
		}
		if intent.State == StateReserved && fact != FactNotAccepted {
			return failure(409, "execution_not_submitted", "执行尚未进入提交边界")
		}
		if terminal(state) {
			return s.finalize(tx, intent, state, string(fact))
		}
		// 已确认受理不能因一次查询超时降级为受理未知。
		if intent.State == StateAccepted && state == StateUnknown {
			return nil
		}
		return tx.Model(intent).Updates(map[string]any{"state": state, "updated_at": s.now()}).Error
	})
}

func (s *Service) finalize(tx *gorm.DB, intent *model.PlatformExecutionIntent, state, reason string) error {
	now := s.now()
	updates := map[string]any{"reserved_points": gorm.Expr("reserved_points - ?", intent.Points), "updated_at": now}
	if state == StateSettled {
		updates["settled_points"] = gorm.Expr("settled_points + ?", intent.Points)
	}
	// settled_points 为净结算；失败补偿的两条原始流水在同一事务追加，净额为零。
	result := tx.Model(&platformauth.QuotaAccount{}).Where("account_id = ? AND reserved_points >= ? AND settled_points <= granted_points AND reserved_points <= granted_points - settled_points", intent.AccountID, intent.Points).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return failure(409, "quota_inconsistent", "点数账户与预留不一致")
	}
	kind := "release"
	if state == StateSettled || state == StateCompensated {
		kind = "settle"
	}
	if err := ledger(tx, intent.AccountID, "intent:"+intent.ID+":"+kind, kind, intent.Points, reason, now); err != nil {
		return err
	}
	if state == StateCompensated {
		if err := ledger(tx, intent.AccountID, "intent:"+intent.ID+":compensate", "compensate", intent.Points, "完全失败由平台承担", now); err != nil {
			return err
		}
	}
	return tx.Model(intent).Updates(map[string]any{"state": state, "finalized_at": now, "review_required": false, "updated_at": now}).Error
}

// MarkOverdue 不释放点数，只把超过 24 小时仍没有终局的执行纳入人工核对。
func (s *Service) MarkOverdue(ctx context.Context) (int64, error) {
	result := s.db.WithContext(ctx).Model(&model.PlatformExecutionIntent{}).
		Where("state IN ? AND submitted_at <= ? AND review_required = ?", []string{StateSubmitting, StateAccepted, StateUnknown}, s.now().Add(-24*time.Hour), false).
		Updates(map[string]any{"review_required": true, "updated_at": s.now()})
	return result.RowsAffected, result.Error
}
