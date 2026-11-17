//go:build platform

package platformboot

import (
	"errors"
	"os"
	"path/filepath"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/localcrypto"
)

func prepareMasterKey(db *gorm.DB, dataDir string) error {
	path := filepath.Join(dataDir, ".settings-key")
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() != 32 {
			return errors.New("平台主密钥损坏，拒绝启动")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var tables int64
	if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name != 'runtime_mode_markers'").Scan(&tables).Error; err != nil {
		return err
	}
	if tables != 0 {
		return errors.New("已有平台数据库缺失主密钥，请恢复匹配备份，不能创建替代密钥")
	}
	_, err = localcrypto.Key(dataDir)
	return err
}
