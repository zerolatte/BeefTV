//go:build platform

package platformstorage

import (
	"encoding/json"
	"errors"
	"time"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

// AssetDeletion 保留请求与资源集合，物理清理完成前不删除原始元数据。
type AssetDeletion struct {
	ID              string `gorm:"primaryKey;size:36"`
	WorkspaceID     string `gorm:"uniqueIndex:platform_asset_deletion;not null"`
	AssetID         string `gorm:"uniqueIndex:platform_asset_deletion;not null"`
	ResourceIDsJSON string `gorm:"not null"`
	Complete        bool   `gorm:"not null;default:false"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (AssetDeletion) TableName() string { return "platform_asset_deletions" }

// DeletionObject 与现有 outbox 一一对应；完成证据和账本终结同事务提交。
type DeletionObject struct {
	JobID      string `gorm:"primaryKey;size:36"`
	DeletionID string `gorm:"index;not null"`
	Complete   bool   `gorm:"not null;default:false"`
}

func (DeletionObject) TableName() string { return "platform_deletion_objects" }

func (r *ResourceRepository) AssetDeletionState(workspaceID, assetID string) (bool, bool, error) {
	var intent AssetDeletion
	err := r.storage.db.Where("workspace_id = ? AND asset_id = ?", workspaceID, assetID).First(&intent).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, false, nil
	}
	return err == nil, intent.Complete, err
}

func (r *ResourceRepository) PrepareAssetDeletion(workspaceID, assetID string, resourceIDs []string, expectedStatus ...string) error {
	return r.storage.transaction(func(tx *gorm.DB) error {
		var existing AssetDeletion
		if err := tx.Where("workspace_id = ? AND asset_id = ?", workspaceID, assetID).First(&existing).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return repository.New(tx).PrepareAssetDeletion(workspaceID, assetID, resourceIDs, func(tx *gorm.DB) error {
			if _, err := accountForWorkspace(tx, workspaceID, true); err != nil {
				return err
			}
			resources, err := repository.New(tx).ResourcesForUserIDs(workspaceID, resourceIDs)
			if err != nil {
				return err
			}
			if len(resources) != len(resourceIDs) {
				return repository.ErrResourceCleanupSetChanged
			}
			physical := make(map[string]*model.Resource)
			for i := range resources {
				resource := &resources[i]
				if resource.Provider != "" && resource.Provider != "local" {
					return asset.ResourceNotLocal()
				}
				// 另一个已持久化删除意图的别名不会保留物理对象；后进入的意图承担清理。
				count, err := repository.New(tx.Where("status IS NULL OR status <> ?", model.ResourceStatusDeleted)).ResourceStorageReferenceCount(resource, resourceIDs)
				if err != nil {
					return err
				}
				if count == 0 {
					physical[asset.StorageIdentity(resource)] = resource
				}
			}
			encoded, err := json.Marshal(resourceIDs)
			if err != nil {
				return err
			}
			intent := AssetDeletion{ID: kernel.NewID(), WorkspaceID: workspaceID, AssetID: assetID, ResourceIDsJSON: string(encoded)}
			if err := tx.Create(&intent).Error; err != nil {
				return err
			}
			jobs := asset.DeletionJobs(workspaceID, physical)
			for _, job := range jobs {
				if err := tx.Create(&job).Error; err != nil {
					return err
				}
				if err := tx.Create(&DeletionObject{JobID: job.ID, DeletionID: intent.ID}).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&model.Asset{}).Where("id = ? AND user_id = ?", assetID, workspaceID).Update("status", model.AssetVersionStatusDeleting).Error; err != nil {
				return err
			}
			if len(resourceIDs) > 0 {
				if err := tx.Model(&model.Resource{}).Where("id IN ? AND user_id = ?", resourceIDs, workspaceID).Update("status", model.ResourceStatusDeleted).Error; err != nil {
					return err
				}
			}
			if len(jobs) == 0 {
				return finishAssetDeletion(tx, &intent)
			}
			return nil
		}, expectedStatus...)
	})
}

func finishAssetDeletion(tx *gorm.DB, intent *AssetDeletion) error {
	if intent.Complete {
		return nil
	}
	var pending int64
	if err := tx.Model(&DeletionObject{}).Where("deletion_id = ? AND complete = ?", intent.ID, false).Count(&pending).Error; err != nil || pending > 0 {
		return err
	}
	var resourceIDs []string
	if err := json.Unmarshal([]byte(intent.ResourceIDsJSON), &resourceIDs); err != nil {
		return err
	}
	return repository.New(tx).DeleteAssetAndResourcesAfterGuard(intent.WorkspaceID, intent.AssetID, resourceIDs, nil, func(tx *gorm.DB) error {
		var resources []model.Resource
		if len(resourceIDs) > 0 {
			if err := tx.Where("user_id = ? AND id IN ?", intent.WorkspaceID, resourceIDs).Find(&resources).Error; err != nil {
				return err
			}
		}
		for _, resource := range resources {
			remaining, err := repository.New(tx.Where("user_id = ?", intent.WorkspaceID)).ResourceStorageReferenceCount(&resource, resourceIDs)
			if err != nil {
				return err
			}
			if remaining == 0 {
				if err := removeObject(tx, intent.WorkspaceID, asset.StorageIdentity(&resource)); err != nil {
					return err
				}
			}
		}
		return tx.Model(intent).Update("complete", true).Error
	}, string(model.AssetVersionStatusDeleting))
}

func completeAssetDeletionObject(tx *gorm.DB, jobID string) (bool, error) {
	var object DeletionObject
	if err := tx.First(&object, "job_id = ?", jobID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := tx.Model(&object).Update("complete", true).Error; err != nil {
		return true, err
	}
	var intent AssetDeletion
	if err := tx.First(&intent, "id = ?", object.DeletionID).Error; err != nil {
		return true, err
	}
	return true, finishAssetDeletion(tx, &intent)
}

var _ asset.DurableDeletion = (*ResourceRepository)(nil)
