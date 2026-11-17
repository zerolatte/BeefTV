//go:build platform

package platformworkflow

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

func (w *Workspace) reserveRetry(runID, key string) error {
	return w.s.repo.DB().WithContext(w.ctx).Transaction(func(tx *gorm.DB) error {
		row := model.PlatformWorkflowRequest{ID: kernel.NewID(), WorkspaceID: w.id, Kind: "retry", RequestKey: key, RunID: runID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		var saved model.PlatformWorkflowRequest
		if err := tx.Where("workspace_id = ? AND kind = ? AND request_key = ?", w.id, "retry", key).Take(&saved).Error; err != nil {
			return err
		}
		if saved.RunID != runID {
			return &kernel.AppError{Status: 409, Code: 409, Reason: "idempotency_conflict", Message: "本次重试标识已用于另一个工作流运行"}
		}
		var count int64
		if err := tx.Model(&model.CanvasWorkflowRun{}).Where("id = ? AND user_id = ?", runID, w.id).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return errors.New("工作流重试对象已失效")
		}
		return nil
	})
}
