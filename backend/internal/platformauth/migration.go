//go:build platform

package platformauth

import (
	"fmt"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformstorage"

	"gorm.io/gorm"
)

const CurrentSchemaVersion = 10

// Migrate 使用独立版本序列；已发布的版本只读，后续变化追加迁移。
func Migrate(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(&SchemaMigration{}); err != nil {
			return err
		}
		var version int
		if err := tx.Model(&SchemaMigration{}).Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
			return err
		}
		if version > CurrentSchemaVersion {
			return fmt.Errorf("平台数据库版本 %d 高于当前程序支持的版本", version)
		}
		if version < 1 {
			if err := tx.AutoMigrate(&Account{}, &Credential{}, &AccountWorkspace{}, &Session{}, &SecurityEvent{}, &Audit{}, &BootstrapMarker{}, &QuotaAccount{}, &UsageLedger{}, &StorageAccount{}); err != nil {
				return err
			}
			if err := tx.Create(&SchemaMigration{Version: 1, AppliedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		if version < 2 {
			if err := tx.AutoMigrate(&platformstorage.Reservation{}, &platformstorage.StoredObject{}); err != nil {
				return err
			}
			if err := tx.Create(&SchemaMigration{Version: 2, AppliedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		if version < 3 {
			if err := tx.AutoMigrate(&model.PlatformModelGrant{}, &model.PlatformChannelState{}, &model.PlatformCredentialVersion{}, &model.PlatformModelPriceHead{}, &model.PlatformModelPrice{}); err != nil {
				return err
			}
			if err := tx.Create(&SchemaMigration{Version: 3, AppliedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		if version < 4 {
			if err := tx.AutoMigrate(&platformstorage.Reservation{}); err != nil {
				return err
			}
			if err := tx.Create(&SchemaMigration{Version: 4, AppliedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		if version < 5 {
			if err := tx.AutoMigrate(&model.PlatformExecutionIntent{}, &model.PlatformRequestReceipt{}, &model.PlatformPointAdjustment{}); err != nil {
				return err
			}
			if err := tx.Create(&SchemaMigration{Version: 5, AppliedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		if version < 6 {
			if err := tx.AutoMigrate(&platformstorage.UploadRequest{}); err != nil {
				return err
			}
			if err := tx.Create(&SchemaMigration{Version: 6, AppliedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		if version < 7 {
			if err := tx.AutoMigrate(&model.PlatformExecutionReview{}); err != nil {
				return err
			}
			if err := tx.Create(&SchemaMigration{Version: 7, AppliedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		if version < 8 {
			if err := tx.AutoMigrate(&platformstorage.AssetDeletion{}, &platformstorage.DeletionObject{}); err != nil {
				return err
			}
			if tx.Migrator().HasTable(&model.Asset{}) {
				if err := platformstorage.MigrateDeletionGuards(tx); err != nil {
					return err
				}
			}
			if err := tx.Create(&SchemaMigration{Version: 8, AppliedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		if version < 9 {
			if err := tx.AutoMigrate(&ImportReceipt{}); err != nil {
				return err
			}
			if err := tx.Create(&SchemaMigration{Version: 9, AppliedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		if version < 10 {
			if err := tx.AutoMigrate(&model.PlatformWorkflowRequest{}); err != nil {
				return err
			}
			if err := tx.Create(&SchemaMigration{Version: 10, AppliedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
