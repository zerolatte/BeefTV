//go:build platform

package platformbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

func validateRestorableData(ctx context.Context, db *gorm.DB, source string) error {
	if err := validateCredentialKey(ctx, db, source); err != nil {
		return err
	}
	return validateReadyResources(ctx, db, source)
}

func validateCredentialKey(ctx context.Context, db *gorm.DB, source string) error {
	if !db.Migrator().HasTable(&model.PlatformCredentialVersion{}) {
		return nil
	}
	rows, err := db.WithContext(ctx).Model(&model.PlatformCredentialVersion{}).Select("ciphertext").Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var ciphertext string
		if err := rows.Scan(&ciphertext); err != nil {
			return err
		}
		if ciphertext == "" {
			return errors.New("平台渠道凭据密文缺失，不能确认备份可恢复")
		}
		if _, err := localcrypto.Decrypt(source, ciphertext); err != nil {
			return errors.New("平台渠道凭据无法由当前主密钥解密，不能确认备份可恢复")
		}
	}
	return rows.Err()
}

func validateReadyResources(ctx context.Context, db *gorm.DB, source string) error {
	if !db.Migrator().HasTable(&model.Resource{}) {
		return nil
	}
	rows, err := db.WithContext(ctx).Model(&model.Resource{}).
		Select("provider", "object_key", "size").Where("status = ?", model.ResourceStatusReady).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	store := asset.NewFileStore(source)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var resource model.Resource
		if err := rows.Scan(&resource.Provider, &resource.ObjectKey, &resource.Size); err != nil {
			return err
		}
		provider := strings.ToLower(strings.TrimSpace(resource.Provider))
		if provider != "" && provider != "local" {
			return errors.New("平台完整备份暂不支持外部存储资源")
		}
		root, err := os.Lstat(filepath.Join(source, "resources"))
		if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
			return errors.New("就绪资源目录缺失或不属于当前数据目录")
		}
		if err := validateStoredResource(store, &resource); err != nil {
			return err
		}
	}
	return rows.Err()
}

func validateStoredResource(store *asset.FileStore, resource *model.Resource) error {
	file, err := store.Open(resource.ObjectKey)
	if err != nil {
		return errors.New("就绪资源文件缺失、不可读或越出存储目录，不能创建完整备份")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || resource.Size < 0 || info.Size() != resource.Size {
		return errors.New("就绪资源文件类型或字节数与数据库不一致，不能创建完整备份")
	}
	return nil
}
