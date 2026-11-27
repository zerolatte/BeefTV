package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/model"
)

var ErrStorageVersion = errors.New("存储设置版本冲突")

type StorageUsage struct {
	Provider string `json:"provider"`
	Count    int64  `json:"count"`
	Bytes    int64  `json:"bytes"`
}
type StorageFallback struct {
	ResourceID string    `json:"resourceId" gorm:"column:subject_id"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ReplaceStorageSettings 将策略版本、位置凭据和审计作为一次原子写入。
func (r *Repository) ReplaceStorageSettings(ctx context.Context, expected string, policy, location *model.SystemSetting, audit model.PlatformAudit) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var result *gorm.DB
		if expected == "" {
			result = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(policy)
		} else {
			result = tx.Model(&model.SystemSetting{}).Where("key = ? AND value_json = ?", policy.Key, expected).
				Updates(map[string]any{"value_json": policy.ValueJSON, "updated_by": policy.UpdatedBy, "updated_at": time.Now()})
		}
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrStorageVersion
		}
		if location != nil {
			if err := tx.Save(location).Error; err != nil {
				return err
			}
		}
		return tx.Create(&audit).Error
	})
}

func (r *Repository) StorageStatistics() ([]StorageUsage, error) {
	rows := []StorageUsage{}
	err := r.db.Model(&model.Resource{}).Select("provider, COUNT(*) AS count, COALESCE(SUM(size), 0) AS bytes").
		Where("status = ?", model.ResourceStatusReady).Group("provider").Scan(&rows).Error
	return rows, err
}
func (r *Repository) StorageFallbacks(limit int) ([]StorageFallback, error) {
	rows := []StorageFallback{}
	err := r.db.Model(&model.PlatformAudit{}).Select("subject_id, reason, created_at").
		Where("action = ?", "storage.fallback").Order("created_at DESC").Limit(limit).Scan(&rows).Error
	return rows, err
}
func (r *Repository) StorageCleanupSettings(prefix string, limit int) ([]model.SystemSetting, error) {
	rows := []model.SystemSetting{}
	err := r.db.Where("key LIKE ?", prefix+"%").Order("updated_at ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *Repository) ReadyRemoteResourcePage(ctx context.Context, after string, limit int) ([]model.Resource, error) {
	rows := []model.Resource{}
	err := r.db.WithContext(ctx).Where("provider = ? AND status = ? AND id > ?", "oss", model.ResourceStatusReady, after).Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}
