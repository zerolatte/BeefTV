//go:build platform

package platformbackup

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

// 快照之后原运行可能已提交后续步骤，不能将旧排队状态作为可继续的依据。
func fenceRestoredWorkflows(tx *gorm.DB, now time.Time) error {
	var rows []model.CanvasWorkflowRun
	if err := tx.Where("status IN ?", []string{"queued", "running", "paused"}).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var state map[string]json.RawMessage
		if err := json.Unmarshal([]byte(row.StateJSON), &state); err != nil || state == nil {
			return errors.New("工作流运行状态损坏，不能安全恢复备份")
		}
		for field, value := range map[string]string{
			"status": "failed",
			"reason": string(kernel.ReasonBackupRecoveryRequired),
			"error":  "备份之后的工作流执行事实需要核对，已停止自动继续",
		} {
			state[field], _ = json.Marshal(value)
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if err := tx.Model(&model.CanvasWorkflowRun{}).Where("id = ? AND user_id = ?", row.ID, row.UserID).
			Updates(map[string]any{"status": "failed", "state_json": string(encoded), "updated_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}
