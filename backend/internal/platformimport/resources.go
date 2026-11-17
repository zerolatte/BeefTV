//go:build platform

package platformimport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/platformstorage"
	"infinite-canvas/backend/internal/repository"
)

func validateResources(ctx context.Context, db *gorm.DB, directory string) error {
	var resources []model.Resource
	if err := db.WithContext(ctx).Find(&resources).Error; err != nil {
		return err
	}
	expected := map[string]bool{}
	ids := make([]string, 0, len(resources))
	owned := map[string]struct{}{}
	for _, resource := range resources {
		if resource.Status != model.ResourceStatusReady || resource.Size < 0 || resource.Size > platformauth.MaxPoints {
			return errors.New("仅支持已完成且字节数有效的本地资源")
		}
		if resource.Provider != "" && resource.Provider != "local" || resource.Endpoint != "" || resource.Bucket != "" || resource.StorageSettingID != "" {
			return errors.New("本阶段不支持外部对象存储或未知存储归属")
		}
		if err := validateObject(directory, "resources", resource.ObjectKey, resource.Size); err != nil {
			return err
		}
		expected["resources/"+resource.ObjectKey] = true
		if resource.PlaybackStatus == model.PlaybackStatusProcessing {
			return errors.New("存在进行中的播放转码，请在源工作区完成后导入")
		}
		if resource.PlaybackObjectKey != "" {
			return errors.New("本阶段不导入播放缓存副本，请先在源工作区清理播放缓存")
		}
		ids = append(ids, resource.ID)
		owned[resource.ID] = struct{}{}
	}
	for _, name := range []string{"resources", "playback"} {
		root := filepath.Join(directory, name)
		if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("资源树包含符号链接或目录联接")
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			if !expected[filepath.ToSlash(rel)] {
				return errors.New("资源树存在没有数据库归属的文件")
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if len(resources) == 0 {
		return nil
	}
	// 只接纳可以追溯到已保留业务对象的资源，避免孤立文件进入后续资源回收流程。
	snapshot, err := repository.New(db.WithContext(ctx)).ResourceReferenceSnapshot(resources[0].UserID, "", ids)
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	for _, direct := range snapshot.Direct {
		referenced[direct.ResourceID] = true
	}
	for _, document := range snapshot.Documents {
		for id := range assets.DocumentReferencedIDs(document.PrimaryJSON, owned) {
			referenced[id] = true
		}
		for id := range assets.DocumentReferencedIDs(document.SecondaryJSON, owned) {
			referenced[id] = true
		}
	}
	for _, id := range ids {
		if !referenced[id] {
			return errors.New("存在没有可审计业务引用的资源，请在源工作区确认保存或清理后导入")
		}
	}
	return nil
}

func validateObject(directory, tree, key string, size int64) error {
	if !fs.ValidPath(key) || strings.ContainsAny(key, "\\:\x00") {
		return errors.New("资源存储键不是受支持的安全相对路径")
	}
	root, err := os.OpenRoot(filepath.Join(directory, tree))
	if err != nil {
		return errors.New("资源目录缺失")
	}
	defer root.Close()
	file, err := root.Open(filepath.FromSlash(key))
	if err != nil {
		return errors.New("资源文件缺失或越出资源目录")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || size >= 0 && info.Size() != size {
		return errors.New("资源文件类型或字节数与数据库不一致")
	}
	return nil
}

func initializeStoredObjects(ctx context.Context, db *gorm.DB, accountID, workspaceID string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var resources []model.Resource
		if err := tx.Where("user_id = ?", workspaceID).Find(&resources).Error; err != nil {
			return err
		}
		objects := map[string]int64{}
		var used int64
		now := time.Now().UTC()
		for _, resource := range resources {
			identity := asset.StorageIdentity(&resource)
			if previous, ok := objects[identity]; ok {
				if previous != resource.Size {
					return errors.New("同一存储对象的字节数不一致")
				}
			} else {
				if resource.Size > platformauth.MaxPoints-used {
					return errors.New("导入存储总量超出整数范围")
				}
				used += resource.Size
				objects[identity] = resource.Size
				if err := tx.Create(&platformstorage.StoredObject{ID: uuid.NewString(), AccountID: accountID, WorkspaceID: workspaceID, ObjectIdentity: identity, Size: resource.Size, CreatedAt: now}).Error; err != nil {
					return err
				}
			}
			key := resource.ID
			if resource.UploadKey != nil && *resource.UploadKey != "" {
				key = *resource.UploadKey
			}
			if err := tx.Create(&platformstorage.Reservation{ID: uuid.NewString(), AccountID: accountID, WorkspaceID: workspaceID, BusinessKey: key, Purpose: platformstorage.PurposeManual, RequestedBytes: resource.Size, State: platformstorage.StateCommitted, ObjectIdentity: identity, CommittedBytes: resource.Size, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&platformauth.StorageAccount{}).Where("account_id = ?", accountID).Update("used_bytes", used).Error
	})
}

func validateFinal(ctx context.Context, db *gorm.DB, directory, workspace string) error {
	key, err := os.ReadFile(filepath.Join(directory, ".settings-key"))
	if err != nil || len(key) != 32 {
		return errors.New("目标主密钥缺失或损坏")
	}
	clear(key)
	var versions []int
	if err := db.WithContext(ctx).Model(&platformauth.SchemaMigration{}).Order("version").Pluck("version", &versions).Error; err != nil {
		return err
	}
	if len(versions) != platformauth.CurrentSchemaVersion {
		return errors.New("目标平台迁移账本不完整")
	}
	for index, version := range versions {
		if version != index+1 {
			return errors.New("目标平台迁移账本含未知版本")
		}
	}
	var marker modeMarker
	if err := readJSON(filepath.Join(directory, database.RuntimeModeFileName), &marker); err != nil {
		return err
	}
	if marker.Mode != identity.ModePlatform || marker.Version != 1 {
		return errors.New("导入目录尚未完成平台标记")
	}
	if err := validateMarker(ctx, db, marker); err != nil {
		return err
	}
	if err := databaseIntegrity(ctx, db); err != nil {
		return err
	}
	var bindings int64
	if err := db.WithContext(ctx).Table("platform_account_workspaces").Where("workspace_id = ?", workspace).Count(&bindings).Error; err != nil {
		return err
	}
	if bindings != 1 {
		return errors.New("导入账号绑定不完整")
	}
	if err := noRows(ctx, db, "SELECT COUNT(*) FROM platform_account_workspaces b LEFT JOIN platform_accounts a ON a.id=b.account_id LEFT JOIN platform_credentials c ON c.account_id=a.id LEFT JOIN platform_quota_accounts q ON q.account_id=a.id LEFT JOIN platform_storage_accounts s ON s.account_id=a.id WHERE a.id IS NULL OR c.account_id IS NULL OR q.account_id IS NULL OR s.account_id IS NULL", "目标账号或额度根不完整"); err != nil {
		return err
	}
	if err := noRows(ctx, db, "SELECT COUNT(*) FROM platform_storage_accounts s WHERE s.used_bytes <> (SELECT COALESCE(SUM(o.size),0) FROM platform_storage_objects o WHERE o.account_id=s.account_id)", "目标存储总量与对象清单不一致"); err != nil {
		return err
	}
	if err := validateResources(ctx, db, directory); err != nil {
		return err
	}
	if err := validateCipherInventory(ctx, db, directory, nil); err != nil {
		return fmt.Errorf("导入后密文审计失败：%w", err)
	}
	return nil
}
