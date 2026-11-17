package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"infinite-canvas/backend/internal/identity"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const RuntimeModeFileName = ".runtime-mode.json"
const PlatformRestoreIncompleteFileName = ".platform-restore-incomplete"

var ErrRuntimeModeMismatch = errors.New("数据目录与数据库运行模式不一致")

type runtimeModeMarker struct {
	ID          int           `json:"-"`
	Version     int           `json:"version"`
	Mode        identity.Mode `json:"mode"`
	DirectoryID string        `json:"directoryId"`
}

func (runtimeModeMarker) TableName() string { return "runtime_mode_markers" }

type RuntimeDirectory struct {
	// InstanceID 在双端标记校验成功后设置，供平台缓存隔离使用。
	InstanceID string
	path       string
	mode       identity.Mode
	marker     *runtimeModeMarker
	entries    []os.DirEntry
}

// CanonicalPath 用于实例锁和后续文件操作，避免目录别名绕过同一工作区锁。
func (d *RuntimeDirectory) CanonicalPath() string { return d.path }

// PrepareRuntimeDirectory 只检查目录，不认领未标记数据；数据库检查必须先于任何迁移。
func PrepareRuntimeDirectory(dataDir string, mode identity.Mode) (*RuntimeDirectory, error) {
	if mode != identity.ModeLocal && mode != identity.ModePlatform {
		return nil, errors.New("运行模式无效")
	}
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("后端数据目录不能为空")
	}
	path, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(path, PlatformRestoreIncompleteFileName)); err == nil {
		return nil, errors.New("平台恢复尚未完成，拒绝启动")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	marker, err := readRuntimeModeMarker(filepath.Join(path, RuntimeModeFileName))
	if err != nil {
		return nil, err
	}
	if marker != nil && marker.Mode != mode {
		return nil, fmt.Errorf("%w：目录属于 %s，当前为 %s", ErrRuntimeModeMismatch, marker.Mode, mode)
	}
	return &RuntimeDirectory{path: path, mode: mode, marker: marker, entries: entries}, nil
}

// CheckDatabase 用双端标记绑定目录和数据库。先提交数据库标记，再写目录标记；
// 中途退出只能由同模式恢复，绝不把已有未标记本地库自动升级为平台库。
func (d *RuntimeDirectory) CheckDatabase(db *gorm.DB) error {
	if d == nil || db == nil {
		return errors.New("运行目录或数据库未初始化")
	}
	databasePath, err := runtimeDatabasePath(db)
	if err != nil {
		return err
	}
	if err := d.validateDatabasePath(databasePath); err != nil {
		return err
	}
	var marker runtimeModeMarker
	err = db.Transaction(func(tx *gorm.DB) error {
		var tableNames []string
		if err := tx.Raw("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'").Scan(&tableNames).Error; err != nil {
			return err
		}
		hasMarker := false
		for _, name := range tableNames {
			if name == (runtimeModeMarker{}).TableName() {
				hasMarker = true
			}
			if d.mode == identity.ModeLocal && strings.HasPrefix(name, "platform_") {
				return fmt.Errorf("%w：本地程序不能打开平台业务数据库", ErrRuntimeModeMismatch)
			}
		}
		if hasMarker {
			var rows []runtimeModeMarker
			if err := tx.Find(&rows).Error; err != nil {
				return fmt.Errorf("读取数据库运行标记失败：%w", err)
			}
			if len(rows) != 1 || rows[0].ID != 1 || validateRuntimeModeMarker(rows[0]) != nil {
				return errors.New("数据库运行标记损坏，拒绝启动")
			}
			marker = rows[0]
			if marker.Mode != d.mode {
				return fmt.Errorf("%w：数据库属于 %s，当前为 %s", ErrRuntimeModeMismatch, marker.Mode, d.mode)
			}
			if d.marker != nil && (d.marker.DirectoryID != marker.DirectoryID || d.marker.Version != marker.Version) {
				return fmt.Errorf("%w：目录与数据库不是同一份数据", ErrRuntimeModeMismatch)
			}
			return nil
		}
		if d.marker != nil {
			return fmt.Errorf("%w：目录已有标记但数据库标记缺失", ErrRuntimeModeMismatch)
		}
		if d.mode == identity.ModePlatform {
			if len(tableNames) != 0 {
				return errors.New("平台不能直接打开未标记的已有数据库，请使用隔离导入流程")
			}
			if err := d.requireEmptyPlatformDirectory(databasePath); err != nil {
				return err
			}
		}
		marker = runtimeModeMarker{ID: 1, Version: 1, Mode: d.mode, DirectoryID: uuid.NewString()}
		if err := tx.Exec("CREATE TABLE runtime_mode_markers (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL, mode TEXT NOT NULL CHECK (mode IN ('local', 'platform')), directory_id TEXT NOT NULL)").Error; err != nil {
			return err
		}
		return tx.Create(&marker).Error
	})
	if err != nil {
		return err
	}
	// 内存数据库只供本地测试使用，不能给持久目录留下一个无法复用的临时绑定。
	if databasePath == "" {
		return nil
	}
	if err := writeRuntimeModeMarker(filepath.Join(d.path, RuntimeModeFileName), marker); err != nil {
		return err
	}
	d.marker = &marker
	d.InstanceID = marker.DirectoryID
	return nil
}

func runtimeDatabasePath(db *gorm.DB) (string, error) {
	var databases []struct{ Name, File string }
	if err := db.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
		return "", err
	}
	for _, database := range databases {
		if database.Name != "main" || database.File == "" {
			continue
		}
		path, err := filepath.Abs(database.File)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(path)
	}
	return "", nil
}

func (d *RuntimeDirectory) requireEmptyPlatformDirectory(databasePath string) error {
	for _, entry := range d.entries {
		path := filepath.Join(d.path, entry.Name())
		if !entry.IsDir() && (path == databasePath || path == databasePath+"-wal" || path == databasePath+"-shm" || path == databasePath+"-journal") {
			continue
		}
		return errors.New("首次平台初始化必须使用空数据目录，已有资源或主密钥请通过隔离导入流程处理")
	}
	return nil
}

func validateRuntimeModeMarker(marker runtimeModeMarker) error {
	if marker.Version != 1 || (marker.Mode != identity.ModeLocal && marker.Mode != identity.ModePlatform) {
		return errors.New("运行模式标记版本或模式无效")
	}
	if _, err := uuid.Parse(marker.DirectoryID); err != nil {
		return errors.New("运行模式目录标识无效")
	}
	return nil
}

func readRuntimeModeMarker(path string) (*runtimeModeMarker, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("数据目录运行标记不是有效的小型普通文件")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var marker runtimeModeMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return nil, fmt.Errorf("数据目录运行标记损坏：%w", err)
	}
	if err := validateRuntimeModeMarker(marker); err != nil {
		return nil, err
	}
	return &marker, nil
}

func writeRuntimeModeMarker(path string, marker runtimeModeMarker) error {
	raw, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := readRuntimeModeMarker(path)
		if readErr != nil {
			return readErr
		}
		if existing == nil || existing.Mode != marker.Mode || existing.DirectoryID != marker.DirectoryID || existing.Version != marker.Version {
			return fmt.Errorf("%w：运行标记在启动过程中发生变化", ErrRuntimeModeMismatch)
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(raw, '\n'))
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}
