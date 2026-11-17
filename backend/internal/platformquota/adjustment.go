//go:build platform

package platformquota

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
)

type AdjustmentInput struct {
	ClientOperationID string `json:"clientOperationId"`
	Points            int64  `json:"points"`
	Reason            string `json:"reason"`
}

func (s *Service) Adjust(ctx context.Context, actorID, accountID string, input AdjustmentInput) (*model.PlatformPointAdjustment, error) {
	if err := validateRequestKey(input.ClientOperationID); err != nil {
		return nil, err
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Points == 0 || input.Points < -platformauth.MaxPoints || input.Points > platformauth.MaxPoints || input.Reason == "" || !utf8.ValidString(input.Reason) || utf8.RuneCountInString(input.Reason) > 500 {
		return nil, kernel.BadAuthRequest("请填写范围内的非零整数点数及 1—500 字的调整原因")
	}
	var result *model.PlatformPointAdjustment
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		if err := requireAdmin(tx, actorID); err != nil {
			return err
		}
		var existing model.PlatformPointAdjustment
		err := tx.Where("actor_account_id = ? AND request_key = ?", actorID, input.ClientOperationID).Take(&existing).Error
		if err == nil {
			if existing.AccountID != accountID || existing.Points != input.Points || existing.Reason != input.Reason {
				return failure(409, "idempotency_conflict", "同一操作标识对应了不同调整")
			}
			result = &existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var account platformauth.Account
		if err := tx.Where("id = ? AND status <> ?", accountID, platformauth.StatusDeleted).Take(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return kernel.NotFound("账号不存在")
			}
			return err
		}
		query := tx.Model(&platformauth.QuotaAccount{}).Where("account_id = ? AND settled_points <= granted_points AND reserved_points <= granted_points - settled_points", accountID)
		if input.Points > 0 {
			query = query.Where("granted_points <= ?", platformauth.MaxPoints-input.Points)
		} else {
			query = query.Where("granted_points - settled_points - reserved_points >= ?", -input.Points)
		}
		updated := query.Updates(map[string]any{"granted_points": gorm.Expr("granted_points + ?", input.Points), "updated_at": s.now()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return failure(409, "quota_adjustment_conflict", "调整不能突破已结算与预留下限或整数上限")
		}
		result = &model.PlatformPointAdjustment{ID: kernel.NewID(), ActorAccountID: actorID, AccountID: accountID, RequestKey: input.ClientOperationID, Points: input.Points, Reason: input.Reason, CreatedAt: s.now()}
		if err := tx.Create(result).Error; err != nil {
			return err
		}
		if err := ledger(tx, accountID, "adjustment:"+result.ID, "adjustment", input.Points, input.Reason, s.now()); err != nil {
			return err
		}
		difference, err := json.Marshal(map[string]any{"points": input.Points, "reason": input.Reason, "adjustmentId": result.ID})
		if err != nil {
			return err
		}
		return tx.Create(&platformauth.Audit{ID: kernel.NewID(), ActorAccountID: actorID, TargetAccountID: accountID, Action: "points_adjusted", Difference: string(difference), CreatedAt: s.now()}).Error
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) Usage(ctx context.Context, actorID, accountID string, offset, limit int) ([]model.PlatformExecutionIntent, int64, error) {
	if offset < 0 || offset > 10000000 || limit < 1 || limit > 100 {
		return nil, 0, kernel.BadAuthRequest("分页参数无效")
	}
	rows := []model.PlatformExecutionIntent{}
	var total int64
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		if err := requireAdmin(tx, actorID); err != nil {
			return err
		}
		query := tx.Model(&model.PlatformExecutionIntent{})
		if accountID != "" {
			query = query.Where("account_id = ?", accountID)
		}
		if err := query.Count(&total).Error; err != nil {
			return err
		}
		if err := query.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
			return err
		}
		for index := range rows {
			row := &rows[index]
			if !terminal(row.State) && row.SubmittedAt != nil && !row.SubmittedAt.After(s.now().Add(-24*time.Hour)) {
				row.ReviewRequired = true
			}
		}
		return nil
	})
	return rows, total, err
}
