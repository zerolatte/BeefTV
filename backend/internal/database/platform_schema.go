package database

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

// PreparePlatformDirectory 必须在打开数据库之前调用，拒绝接管旧的单人数据。
func PreparePlatformDirectory(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("平台数据目录不能为空")
	}
	marker := filepath.Join(dir, "platform-instance")
	if info, err := os.Stat(marker); err == nil {
		if info.IsDir() {
			return errors.New("平台目录标记无效")
		}
		data, readErr := os.ReadFile(marker)
		if readErr != nil {
			return readErr
		}
		if _, parseErr := uuid.Parse(string(data)); parseErr != nil {
			return errors.New("平台目录标记无效")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entries) > 0 {
		return errors.New("平台必须使用新的空数据目录，原本地数据不会自动迁移")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(kernel.NewID())
	return errors.Join(writeErr, file.Close())
}

// 平台采用单独的追加式迁移账本；既有本地迁移保持原样。
func MigratePlatformSchema(db *gorm.DB) error {
	platformStarted := db.Migrator().HasTable("platform_schema_migrations")
	if !platformStarted && db.Migrator().HasTable("local_schema_migrations") {
		return errors.New("已有单人数据库不能作为平台数据库，请使用新的空数据目录")
	}
	// 在共享迁移前记录平台初始化意图，意外退出后可以继续，且不接管单人库。
	if err := db.Exec("CREATE TABLE IF NOT EXISTS platform_schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)").Error; err != nil {
		return err
	}
	var version int64
	if err := db.Table("platform_schema_migrations").Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("平台数据库版本不匹配: %d", version)
	}
	if err := MigrateLocalSchema(db); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("CREATE TABLE IF NOT EXISTS platform_schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)").Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Table("platform_schema_migrations").Where("version = 1").Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		if err := tx.AutoMigrate(&model.PlatformAccount{}, &model.PlatformSession{}, &model.PlatformInstance{}, &model.PlatformAudit{}, &model.CreditAccount{}, &model.ModelCreditPrice{}, &model.CreditQuote{}, &model.CreditReservation{}, &model.CreditLedger{}); err != nil {
			return fmt.Errorf("初始化平台表: %w", err)
		}
		// 共享创作表保留迁移记录；平台路由仅使用已认证账号，不能使用虚拟工作区身份。
		if err := tx.Create(&model.PlatformInstance{ID: kernel.NewID()}).Error; err != nil {
			return err
		}
		return tx.Exec("INSERT INTO platform_schema_migrations(version, applied_at) VALUES (1, CURRENT_TIMESTAMP)").Error
	})
}

func RequirePlatformSchema(db *gorm.DB) error {
	if err := RequireLocalSchema(db); err != nil {
		return err
	}
	if !db.Migrator().HasTable("platform_schema_migrations") {
		return errors.New("平台数据库尚未初始化")
	}
	var version int64
	if err := db.Table("platform_schema_migrations").Select("COALESCE(MAX(version), 0)").Scan(&version).Error; err != nil {
		return err
	}
	if version != 1 {
		return fmt.Errorf("平台数据库版本不匹配: %d", version)
	}
	return nil
}
