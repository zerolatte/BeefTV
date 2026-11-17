//go:build platform

package platformbackup

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/database"
)

// 恢复旧快照时撤销旧会话，并隔离快照中无法证明未提交的任务。
func fenceRestoredRuntime(ctx context.Context, dataDir string, platformVersion int64) error {
	if platformVersion == 0 {
		return nil
	}
	db, err := database.Open(database.Config{DataDir: dataDir})
	if err != nil {
		return err
	}
	sql, err := db.DB()
	if err != nil {
		return err
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Exec("UPDATE platform_sessions SET revoked_at=? WHERE revoked_at IS NULL", now).Error; err != nil {
			return err
		}
		if err := fenceRestoredWorkflows(tx, now); err != nil {
			return err
		}
		if platformVersion < 5 {
			return nil
		}
		// 快照之后原进程可能已经调用上游，恢复出的 reserved 不能证明从未发送。
		// 保留全部预留并禁止自动重投，等待原目录及上游事实核对。
		if err := tx.Exec(`UPDATE tasks SET status='failed', stage='备份恢复后待核对',
			error='备份之后的执行事实需要核对，不能自动重新提交', error_code='submission_unknown',
			lease_owner='', lease_expires_at=NULL, updated_at=?
			WHERE status IN ('queued','running') AND id IN
			(SELECT task_id FROM platform_execution_intents WHERE state IN ('reserved','submitting','accepted','unknown'))`, now).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE platform_execution_intents SET state='unknown',
			submitted_at=COALESCE(submitted_at,?), updated_at=?
			WHERE state IN ('reserved','submitting','accepted','unknown')`, now, now).Error
	})
	return errors.Join(err, sql.Close())
}
