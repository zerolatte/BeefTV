//go:build platform

package platformbackup

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
)

type modeMarker struct {
	ID          int           `json:"-"`
	Version     int           `json:"version"`
	Mode        identity.Mode `json:"mode"`
	DirectoryID string        `json:"directoryId"`
}

func inspect(ctx context.Context, db *gorm.DB, source string) (*Manifest, error) {
	if _, err := os.Lstat(filepath.Join(source, database.PlatformRestoreIncompleteFileName)); err == nil {
		return nil, errors.New("平台恢复尚未完成，不能备份或验证该目录")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var marker modeMarker
	if err := readJSON(filepath.Join(source, database.RuntimeModeFileName), &marker); err != nil {
		return nil, err
	}
	if marker.Mode != identity.ModePlatform || marker.Version != 1 {
		return nil, errors.New("只支持已标记的平台数据；本地工作区必须经过独立导入流程")
	}
	if _, err := uuid.Parse(marker.DirectoryID); err != nil {
		return nil, errors.New("平台目录标记无效")
	}
	var rows []modeMarker
	if err := db.WithContext(ctx).Table("runtime_mode_markers").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != 1 || rows[0].ID != 1 || rows[0].DirectoryID != marker.DirectoryID || rows[0].Mode != marker.Mode || rows[0].Version != marker.Version {
		return nil, errors.New("平台数据库与目录标记不一致")
	}
	key, err := os.ReadFile(filepath.Join(source, ".settings-key"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("平台主密钥缺失或长度无效")
	}
	clear(key)
	var integrity []string
	if err := db.WithContext(ctx).Raw("PRAGMA integrity_check").Scan(&integrity).Error; err != nil {
		return nil, err
	}
	if len(integrity) != 1 || integrity[0] != "ok" {
		return nil, errors.New("数据库完整性检查未通过")
	}
	local, platform, err := schemaVersions(db.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	if err := validateRestorableData(ctx, db, source); err != nil {
		return nil, err
	}
	return &Manifest{Version: 1, DirectoryID: marker.DirectoryID, LocalVersion: local, PlatformVersion: platform, CreatedAt: time.Now().UTC()}, nil
}

func schemaVersions(db *gorm.DB) (local, platform int64, err error) {
	for _, item := range []struct {
		table string
		value *int64
	}{{"local_schema_migrations", &local}, {"platform_schema_migrations", &platform}} {
		var count int64
		if err = db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", item.table).Scan(&count).Error; err != nil {
			return
		}
		if count != 0 {
			err = db.Table(item.table).Select("COALESCE(MAX(version),0)").Scan(item.value).Error
			if err != nil {
				return
			}
		}
	}
	return
}

func openDatabase(path string, immutable bool) (*gorm.DB, func(), error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("备份数据库缺失或不是普通文件")
	}
	if err := database.ValidateDatabaseFile(path); err != nil {
		return nil, nil, err
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := url.URL{Scheme: "file", Path: path}
	query := url.Values{"mode": {"ro"}, "_busy_timeout": {"5000"}}
	if immutable {
		query.Set("immutable", "1")
	}
	uri.RawQuery = query.Encode()
	db, err := database.Open(database.Config{DSN: uri.String()})
	if err != nil {
		return nil, nil, err
	}
	sql, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	closeDB := func() { _ = sql.Close() }
	if err := database.ConfigurePool(db); err != nil {
		closeDB()
		return nil, nil, err
	}
	return db, closeDB, nil
}
