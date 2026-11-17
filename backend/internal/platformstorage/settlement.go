//go:build platform

package platformstorage

import (
	"errors"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
)

func (s *Service) Commit(workspaceID, key, objectIdentity string, bytes int64) error {
	if err := validate(workspaceID, key, bytes); err != nil {
		return err
	}
	if strings.TrimSpace(objectIdentity) == "" {
		return kernel.BadAuthRequest("存储对象标识缺失")
	}
	return s.transaction(func(tx *gorm.DB) error { return commit(tx, workspaceID, key, objectIdentity, bytes) })
}

func commit(tx *gorm.DB, workspaceID, key, objectIdentity string, bytes int64) error {
	var held Reservation
	if err := tx.Where("workspace_id = ? AND business_key = ?", workspaceID, key).First(&held).Error; err != nil {
		return err
	}
	if held.State == StateCommitted {
		if held.ObjectIdentity != objectIdentity || held.CommittedBytes != bytes {
			return conflict()
		}
		return nil
	}
	if held.State != StateHeld || bytes > held.ReservedBytes {
		return conflict()
	}
	var stored StoredObject
	err := tx.Where("workspace_id = ? AND object_identity = ?", workspaceID, objectIdentity).First(&stored).Error
	added := int64(0)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		added = bytes
		stored = StoredObject{ID: kernel.NewID(), AccountID: held.AccountID, WorkspaceID: workspaceID, ObjectIdentity: objectIdentity, Size: bytes}
		if err := tx.Create(&stored).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if stored.Size != bytes {
		return conflict()
	}
	result := tx.Table("platform_storage_accounts").Where("account_id = ? AND reserved_bytes >= ?", held.AccountID, held.ReservedBytes).
		Updates(map[string]any{"used_bytes": gorm.Expr("used_bytes + ?", added), "reserved_bytes": gorm.Expr("reserved_bytes - ?", held.ReservedBytes), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("存储预留账户不一致")
	}
	return tx.Model(&held).Updates(map[string]any{"state": StateCommitted, "object_identity": objectIdentity, "committed_bytes": bytes, "updated_at": time.Now().UTC()}).Error
}

func (s *Service) Release(workspaceID, key string) error {
	if err := validate(workspaceID, key, 0); err != nil {
		return err
	}
	return s.transaction(func(tx *gorm.DB) error { return release(tx, workspaceID, key) })
}

func release(tx *gorm.DB, workspaceID, key string) error {
	var held Reservation
	err := tx.Where("workspace_id = ? AND business_key = ?", workspaceID, key).First(&held).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if held.State != StateHeld {
		return nil
	}
	result := tx.Table("platform_storage_accounts").Where("account_id = ? AND reserved_bytes >= ?", held.AccountID, held.ReservedBytes).
		Updates(map[string]any{"reserved_bytes": gorm.Expr("reserved_bytes - ?", held.ReservedBytes), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("存储预留账户不一致")
	}
	return tx.Model(&held).Updates(map[string]any{"state": StateReleased, "updated_at": time.Now().UTC()}).Error
}

// RemoveObject 仅在物理对象删除成功且不再被资源引用后调用。
func (s *Service) RemoveObject(workspaceID, objectIdentity string) error {
	if err := validate(workspaceID, objectIdentity, 0); err != nil {
		return err
	}
	return s.transaction(func(tx *gorm.DB) error { return removeObject(tx, workspaceID, objectIdentity) })
}

func removeObject(tx *gorm.DB, workspaceID, objectIdentity string) error {
	var stored StoredObject
	err := tx.Where("workspace_id = ? AND object_identity = ?", workspaceID, objectIdentity).First(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	result := tx.Table("platform_storage_accounts").Where("account_id = ? AND used_bytes >= ?", stored.AccountID, stored.Size).
		Updates(map[string]any{"used_bytes": gorm.Expr("used_bytes - ?", stored.Size), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("存储用量账户不一致")
	}
	return tx.Delete(&stored).Error
}
