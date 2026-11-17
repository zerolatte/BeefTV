//go:build platform

package platformquota

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformauth"
)

type Admission struct {
	AccountID, WorkspaceID                 string
	RequestKey, RequestKind, RequestDigest string
	LogicalModelID                         string
	ModelIntent                            modelcatalog.ModelRequestIntent
}

type Admitted struct {
	Task     *model.Task
	Intent   *model.PlatformExecutionIntent
	Replayed bool
}

// PersistAdmitted 必须只用传入事务写入既有任务，不能联网或启动调度。
type PersistAdmitted func(tx *gorm.DB, selection *modelcatalog.PlatformAdmissionSelection, intentID string) (*model.Task, error)

func (s *Service) Admit(ctx context.Context, input Admission, persist PersistAdmitted) (*Admitted, error) {
	if err := validateRequestKey(input.RequestKey); err != nil {
		return nil, err
	}
	digest, err := hex.DecodeString(input.RequestDigest)
	if err != nil || len(digest) != 32 || strings.TrimSpace(input.RequestKind) == "" || len(input.RequestKind) > 64 || persist == nil {
		return nil, kernel.BadAuthRequest("执行请求摘要、类型或持久化端口无效")
	}
	input.RequestDigest = hex.EncodeToString(digest)
	var result *Admitted
	err = s.transaction(ctx, func(tx *gorm.DB) error {
		if err := requireAccount(tx, input.AccountID, input.WorkspaceID, true); err != nil {
			return err
		}
		var receipt model.PlatformRequestReceipt
		err := tx.Where("workspace_id = ? AND kind = ? AND request_key = ?", input.WorkspaceID, input.RequestKind, input.RequestKey).Take(&receipt).Error
		if err == nil {
			if receipt.RequestDigest != input.RequestDigest {
				return failure(409, "idempotency_conflict", "同一操作标识对应了不同请求")
			}
			intent, err := loadIntent(tx, input.WorkspaceID, receipt.IntentID)
			if err != nil {
				return err
			}
			var saved model.Task
			if err := tx.Where("id = ? AND user_id = ?", intent.TaskID, input.WorkspaceID).Take(&saved).Error; err != nil {
				return err
			}
			result = &Admitted{Task: &saved, Intent: intent, Replayed: true}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		selection, err := modelcatalog.ResolvePlatformAdmission(tx, input.AccountID, input.WorkspaceID, input.LogicalModelID, input.ModelIntent)
		if err != nil {
			return err
		}
		price := selection.Price.Points
		updated := tx.Model(&platformauth.QuotaAccount{}).Where("account_id = ? AND settled_points <= granted_points AND reserved_points <= granted_points - settled_points AND ? <= granted_points - settled_points - reserved_points", input.AccountID, price).Updates(map[string]any{"reserved_points": gorm.Expr("reserved_points + ?", price), "updated_at": s.now()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return failure(403, "quota_exceeded", "AI 点数不足")
		}
		intent := &model.PlatformExecutionIntent{ID: kernel.NewID(), AccountID: input.AccountID, WorkspaceID: input.WorkspaceID, LogicalModelID: input.LogicalModelID, RevisionID: selection.Routed.Revision.ID, RouteID: selection.Routed.Route.ID, ChannelID: selection.Routed.ChannelModel.ChannelID, ChannelModelID: selection.Routed.ChannelModel.ID, CredentialVersionID: selection.CredentialVersionID, PriceVersionID: selection.Price.ID, Operation: input.ModelIntent.Operation, Points: price, State: StateReserved, CreatedAt: s.now()}
		created, err := persist(tx, selection, intent.ID)
		if err != nil {
			return err
		}
		if created == nil || created.ID == "" || created.UserID != input.WorkspaceID {
			return errors.New("任务持久化未返回绑定工作区的任务")
		}
		var saved model.Task
		if err := tx.Where("id = ? AND user_id = ?", created.ID, input.WorkspaceID).Take(&saved).Error; err != nil {
			return err
		}
		if saved.LogicalModelID != intent.LogicalModelID || saved.LogicalModelRevisionID != intent.RevisionID || saved.RouteID != intent.RouteID || saved.ChannelModelID != intent.ChannelModelID || saved.Operation != intent.Operation {
			return errors.New("任务未使用准入冻结的模型与路由")
		}
		intent.TaskID = saved.ID
		if err := tx.Create(intent).Error; err != nil {
			return err
		}
		if err := ledger(tx, input.AccountID, "intent:"+intent.ID+":reserve", "reserve", price, "执行准入预留", s.now()); err != nil {
			return err
		}
		if err := tx.Create(&model.PlatformRequestReceipt{ID: kernel.NewID(), WorkspaceID: input.WorkspaceID, Kind: input.RequestKind, RequestKey: input.RequestKey, RequestDigest: input.RequestDigest, IntentID: intent.ID}).Error; err != nil {
			return err
		}
		result = &Admitted{Task: &saved, Intent: intent}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
