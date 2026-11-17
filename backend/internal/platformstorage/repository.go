//go:build platform

package platformstorage

import (
	"errors"
	"strings"
	"time"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

type ResourceRepository struct {
	asset.Repository
	storage *Service
}

func (s *Service) ResourceRepository(repo *repository.Repository) *ResourceRepository {
	return &ResourceRepository{Repository: asset.NewRepository(repo), storage: s}
}

func resourceIdentity(resource *model.Resource) string {
	if resource.UploadKey != nil && strings.TrimSpace(*resource.UploadKey) != "" {
		return strings.TrimSpace(*resource.UploadKey)
	}
	return resource.ID
}

func (r *ResourceRepository) CreateResource(resource *model.Resource) error {
	return r.storage.transaction(func(tx *gorm.DB) error {
		if resource == nil || resource.UserID == "" || resource.ID == "" {
			return asset.ResourceMissing()
		}
		var held Reservation
		if err := tx.Where("workspace_id = ? AND business_key = ? AND state = ?", resource.UserID, resourceIdentity(resource), StateHeld).First(&held).Error; err != nil {
			return err
		}
		if resource.Size > held.ReservedBytes {
			return quotaExceeded()
		}
		return tx.Create(resource).Error
	})
}

func (r *ResourceRepository) SaveResource(resource *model.Resource) error {
	return r.SaveResources([]*model.Resource{resource})
}

func (r *ResourceRepository) SaveResources(resources []*model.Resource) error {
	return r.storage.transaction(func(tx *gorm.DB) error {
		for _, resource := range resources {
			if resource == nil || resource.ID == "" || resource.UserID == "" {
				return asset.ResourceMissing()
			}
			var persisted model.Resource
			if err := tx.Where("id = ? AND user_id = ?", resource.ID, resource.UserID).First(&persisted).Error; err != nil {
				return err
			}
			if persisted.Status == model.ResourceStatusDeleted {
				return gorm.ErrRecordNotFound
			}
			if resourceIdentity(resource) != resourceIdentity(&persisted) {
				return conflict()
			}
			switch resource.Status {
			case model.ResourceStatusReady:
				if err := commit(tx, resource.UserID, resourceIdentity(resource), asset.StorageIdentity(resource), resource.Size); err != nil {
					return err
				}
			case model.ResourceStatusFailed:
				if err := release(tx, resource.UserID, resourceIdentity(resource)); err != nil {
					return err
				}
			}
			if err := tx.Save(resource).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func reservationWitness(held Reservation) model.UserUploadReservation {
	return model.UserUploadReservation{UserID: held.WorkspaceID, Identity: held.BusinessKey, Size: held.ReservedBytes, Day: "platform", DurableOwnership: true}
}

func (r *ResourceRepository) UploadReservation(workspaceID, key string) (*model.UserUploadReservation, error) {
	held, err := r.storage.Reservation(workspaceID, key)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if held.State != StateHeld {
		return nil, nil
	}
	value := reservationWitness(*held)
	return &value, nil
}

func (r *ResourceRepository) ListUploadReservations() ([]model.UserUploadReservation, error) {
	var values []Reservation
	if err := r.storage.db.Where("state = ? AND purpose = ?", StateHeld, PurposeUpload).Find(&values).Error; err != nil {
		return nil, err
	}
	result := make([]model.UserUploadReservation, 0, len(values))
	for _, value := range values {
		result = append(result, reservationWitness(value))
	}
	return result, nil
}

func (r *ResourceRepository) ClearUploadReservation(workspaceID, key string) error {
	resource, err := r.ResourceByUploadKey(workspaceID, key)
	if err != nil {
		return err
	}
	if resource.Status != model.ResourceStatusReady {
		return conflict()
	}
	return r.storage.Commit(workspaceID, key, asset.StorageIdentity(resource), resource.Size)
}

// 平台孤儿回收尚未接入持久删除意图，不能使用本地提前移除元数据的自动清理路径。
func (r *ResourceRepository) ResourceCleanupCandidates(time.Time, time.Time, int) ([]model.Resource, error) {
	return nil, nil
}

func (r *ResourceRepository) ReleaseIdentifiedDailyUpload(workspaceID, _ string, key string, _ int64) error {
	return r.storage.Release(workspaceID, key)
}

// CompleteResourceDeletionJob 由既有删除 worker 在物理删除成功后调用。
// 完成 outbox 和释放用量必须同一事务，重启重复执行不会多减。
func (r *ResourceRepository) CompleteResourceDeletionJob(id, owner string) error {
	return r.storage.transaction(func(tx *gorm.DB) error {
		var job model.ResourceDeletionJob
		err := tx.Where("id = ? AND lease_owner = ?", id, owner).First(&job).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if handled, err := completeAssetDeletionObject(tx, id); handled || err != nil {
			if err != nil {
				return err
			}
			return tx.Delete(&job).Error
		}
		resource := &model.Resource{UserID: job.UserID, Provider: job.Provider, Endpoint: job.Endpoint, Bucket: job.Bucket, ObjectKey: job.ObjectKey}
		count, err := repository.New(tx).ResourceStorageReferenceCount(resource, nil)
		if err != nil {
			return err
		}
		if count > 0 {
			return &kernel.AppError{Status: 409, Code: 409, Reason: "resource_referenced", Message: "存储对象仍被资源引用"}
		}
		if err := removeObject(tx, job.UserID, asset.StorageIdentity(resource)); err != nil {
			return err
		}
		return tx.Delete(&job).Error
	})
}

var _ asset.Repository = (*ResourceRepository)(nil)
