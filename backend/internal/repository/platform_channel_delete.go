package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

var ErrPlatformChannelInUse = errors.New("platform channel has unfinished tasks or credits")

// 删除与任务准入均在 SQLite 写事务内完成；检查失败时连同写锁对应的更新一起回滚。
func (r *Repository) DeletePlatformChannel(id string, audit model.PlatformAudit) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		locked := tx.Model(&model.ModelChannel{}).Where("id = ? AND scope = ?", id, model.ChannelScopeSystem).
			Update("updated_at", now)
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		// 已退场的模型仍可能有任务与积分记录，因此占用检查也包含软删除模型。
		modelIDs := tx.Unscoped().Model(&model.ChannelModel{}).Select("id").Where("channel_id = ?", id)
		var active int64
		if err := tx.Model(&model.Task{}).Where("channel_model_id IN (?)", modelIDs).
			Where("status IN ? OR (lease_owner <> '' AND lease_expires_at > ?) OR provider_cancel_status = ?",
				[]model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}, now, model.ProviderCancelStatusRequested).
			Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrPlatformChannelInUse
		}
		// 平台失败任务在结果未确认前保留冻结，核对完成后才允许移除其上游凭据。
		if err := tx.Model(&model.CreditReservation{}).Where("model_id IN (?) AND state IN ?", modelIDs, []string{"reserved", "review"}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrPlatformChannelInUse
		}
		if err := tx.Model(&model.ChannelModelVariant{}).Where("channel_model_id IN (?)", modelIDs).
			Updates(map[string]any{"enabled": false, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Where("channel_model_id IN (?)", modelIDs).Delete(&model.ChannelModelVariant{}).Error; err != nil {
			return err
		}
		// 价格保留历史关联，只撤销发布并增加版本；报价和已受理任务的快照均不修改。
		if err := tx.Model(&model.ModelCreditPrice{}).Where("channel_model_id IN (?) AND published = ?", modelIDs, true).
			Updates(map[string]any{"published": false, "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
			return err
		}
		if err := r.WithTx(tx).DeleteSystemChannel(id); err != nil {
			return err
		}
		return tx.Create(&audit).Error
	})
}
