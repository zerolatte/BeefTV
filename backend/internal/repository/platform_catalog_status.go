package repository

import (
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

// 目录去重包含停用模型，只读取指定渠道的标识，避免携带完整模型配置。
func (r *Repository) PlatformChannelModelKeys(channelID string) ([]string, error) {
	keys := []string{}
	err := r.db.Model(&model.ChannelModel{}).Where("channel_id = ?", channelID).Order("model_key asc").Pluck("model_key", &keys).Error
	return keys, err
}

// 启停只更新状态，未完成配置的草稿也可停用，审计与渠道摘要保持同一事务。
func (r *Repository) SetPlatformModelEnabled(channelID, id string, enabled bool, audit model.PlatformAudit) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := r.lockSystemChannelForModelMutation(tx, channelID); err != nil {
			return err
		}
		now := time.Now().UTC()
		result := tx.Model(&model.ChannelModel{}).Where("id = ? AND channel_id = ?", id, channelID).Updates(map[string]any{"enabled": enabled, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if err := refreshChannelModelNames(tx, channelID, now); err != nil {
			return err
		}
		return tx.Create(&audit).Error
	})
}
