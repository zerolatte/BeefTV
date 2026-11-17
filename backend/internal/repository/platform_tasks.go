//go:build platform

package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

// ClaimNextPlatformTask 不领取导入的本地历史任务；平台执行必须有同域意图及永久账号绑定。
func (r *Repository) ClaimNextPlatformTask(owner string, leaseDuration time.Duration) (*model.Task, error) {
	return r.claimNextTask(owner, leaseDuration, func(query *gorm.DB) *gorm.DB {
		return query.Where(`EXISTS (
			SELECT 1 FROM platform_execution_intents AS intent
			JOIN platform_account_workspaces AS binding
				ON binding.account_id = intent.account_id AND binding.workspace_id = intent.workspace_id
			WHERE intent.task_id = tasks.id AND intent.workspace_id = tasks.user_id
		)`)
	})
}

// CachePlatformTextResult 在成功事实结算前持久化结果，以便交付失败时免费恢复。
func (r *Repository) CachePlatformTextResult(ctx context.Context, row *model.Task, result string) error {
	updated := r.db.WithContext(ctx).Model(&model.Task{}).Where("id = ? AND user_id = ? AND status = ? AND lease_owner = ?", row.ID, row.UserID, model.TaskStatusRunning, row.LeaseOwner).Update("result_json", result)
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrTaskStateConflict
	}
	return nil
}

func (r *Repository) ResumePlatformTextDelivery(ctx context.Context, workspace, id string) error {
	updated := r.db.WithContext(ctx).Model(&model.Task{}).Where("id = ? AND user_id = ? AND type = ? AND status IN ? AND result_json <> ''", id, workspace, "canvas_text", []model.TaskStatus{model.TaskStatusFailed, model.TaskStatusCancelled}).Updates(map[string]any{"status": model.TaskStatusQueued, "stage": "等待恢复结果交付", "lease_owner": "", "lease_expires_at": nil, "completed_at": nil, "error": "", "error_code": ""})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrTaskStateConflict
	}
	return nil
}

func (r *Repository) AppendPlatformTaskLog(workspace, taskID, level, message string) error {
	if _, err := r.TaskForUser(workspace, taskID); err != nil {
		return err
	}
	return r.db.Create(&model.TaskLog{ID: kernel.NewID(), UserID: workspace, TaskID: taskID, Level: level, Message: message, CreatedAt: time.Now()}).Error
}

func (r *Repository) PlatformTaskIntents(workspace string, ids []string) ([]model.PlatformExecutionIntent, error) {
	var rows []model.PlatformExecutionIntent
	if len(ids) == 0 {
		return rows, nil
	}
	err := r.db.Where("workspace_id = ? AND task_id IN ?", workspace, ids).Find(&rows).Error
	return rows, err
}

func (r *Repository) PlatformTaskOutputResults(workspace string, ids []string) ([]model.Result, error) {
	var rows []model.Result
	if len(ids) == 0 {
		return rows, nil
	}
	err := r.db.Where("user_id = ? AND task_id IN ? AND kind = ?", workspace, ids, generationOutputResultKind).Order("task_id ASC, id ASC").Find(&rows).Error
	return rows, err
}
