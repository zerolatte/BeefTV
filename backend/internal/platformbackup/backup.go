//go:build platform

// Package platformbackup 负责停写状态下的平台整目录备份与隔离恢复。
package platformbackup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/runtimeinfo"
)

const databaseName = "open_ai_canvas.db"
const manifestName = "manifest.json"

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	Version         int       `json:"version"`
	DirectoryID     string    `json:"directoryId"`
	LocalVersion    int64     `json:"localVersion"`
	PlatformVersion int64     `json:"platformVersion"`
	CreatedAt       time.Time `json:"createdAt"`
	Files           []File    `json:"files"`
}

// Create 只读打开源数据库，持有工作区独占锁直到数据库与资源复制完成。
func Create(ctx context.Context, source, destination string) (*Manifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, err := existingDirectory(source)
	if err != nil {
		return nil, err
	}
	lock, err := runtimeinfo.LockWorkspace(source)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	db, closeDB, err := openDatabase(filepath.Join(source, databaseName), false)
	if err != nil {
		return nil, err
	}
	defer closeDB()
	return snapshot(ctx, db, source, destination)
}

// BeforeMigration 只能由已持有工作区锁、尚未启动任何写入服务的组合根调用。
// 空库不备份；有版本变化时必须成功保存完整快照才允许继续迁移。
func BeforeMigration(ctx context.Context, db *gorm.DB, source string, expectedPlatform int64) (string, error) {
	local, platform, err := schemaVersions(db.WithContext(ctx))
	if err != nil {
		return "", err
	}
	if local > database.CurrentSchemaVersion || platform > expectedPlatform {
		return "", errors.New("数据库版本高于当前程序，拒绝迁移")
	}
	if local == 0 && platform == 0 || local == database.CurrentSchemaVersion && platform == expectedPlatform {
		return "", nil
	}
	source, err = existingDirectory(source)
	if err != nil {
		return "", err
	}
	parent := filepath.Join(filepath.Dir(source), filepath.Base(source)+".backups")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("创建迁移备份目录失败：%w", err)
	}
	destination := filepath.Join(parent, time.Now().UTC().Format("20060102T150405Z")+"-"+uuid.NewString())
	if _, err := snapshot(ctx, db, source, destination); err != nil {
		return "", fmt.Errorf("迁移前备份失败，未执行迁移：%w", err)
	}
	return destination, nil
}

func snapshot(ctx context.Context, db *gorm.DB, source, destination string) (_ *Manifest, resultErr error) {
	manifest, err := inspect(ctx, db, source)
	if err != nil {
		return nil, err
	}
	destination, err = newDestination(source, destination)
	if err != nil {
		return nil, err
	}
	// destination 由本次调用独占创建，且已验证与源目录互不包含。
	defer func() {
		if resultErr != nil {
			_ = os.RemoveAll(destination)
		}
	}()
	data := filepath.Join(destination, "data")
	if err := os.Mkdir(data, 0o700); err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Exec("VACUUM main INTO ?", filepath.Join(data, databaseName)).Error; err != nil {
		return nil, fmt.Errorf("创建数据库一致快照失败：%w", err)
	}
	if err := os.Chmod(filepath.Join(data, databaseName), 0o600); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(data, databaseName), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if err := errors.Join(file.Sync(), file.Close()); err != nil {
		return nil, err
	}
	if err := copyTree(ctx, source, data, true); err != nil {
		return nil, err
	}
	manifest.Files, err = inventory(ctx, data)
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	// 完成标记最后落盘；中断留下的无清单目录不能被恢复。
	if err := writeSynced(filepath.Join(destination, manifestName), encoded); err != nil {
		return nil, err
	}
	if _, err := Verify(ctx, destination); err != nil {
		return nil, err
	}
	return manifest, nil
}

// Verify 检查文件集合、摘要、数据库完整性、主密钥以及目录和数据库的双端标记。
func Verify(ctx context.Context, backup string) (*Manifest, error) {
	backup, err := existingDirectory(backup)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := readJSON(filepath.Join(backup, manifestName), &manifest); err != nil {
		return nil, fmt.Errorf("备份缺失完成清单或清单损坏：%w", err)
	}
	if manifest.Version != 1 || manifest.CreatedAt.IsZero() || len(manifest.Files) == 0 {
		return nil, errors.New("备份格式或完成状态无效")
	}
	data := filepath.Join(backup, "data")
	files, err := inventory(ctx, data)
	if err != nil {
		return nil, err
	}
	if !sameFiles(files, manifest.Files) {
		return nil, errors.New("备份文件集合、大小或内容摘要不一致")
	}
	db, closeDB, err := openDatabase(filepath.Join(data, databaseName), true)
	if err != nil {
		return nil, err
	}
	defer closeDB()
	actual, err := inspect(ctx, db, data)
	if err != nil {
		return nil, err
	}
	if actual.DirectoryID != manifest.DirectoryID || actual.LocalVersion != manifest.LocalVersion || actual.PlatformVersion != manifest.PlatformVersion {
		return nil, errors.New("备份清单与数据库标记或迁移版本不一致")
	}
	return &manifest, nil
}

// Restore 只恢复到全新目录，不迁移、不启动服务，也不覆盖现有工作区。
func Restore(ctx context.Context, backup, destination string) (resultErr error) {
	manifest, err := Verify(ctx, backup)
	if err != nil {
		return err
	}
	backup, err = existingDirectory(backup)
	if err != nil {
		return err
	}
	destination, err = newDestinationPath(backup, destination)
	if err != nil {
		return err
	}
	lock, err := runtimeinfo.LockWorkspace(destination)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := os.Mkdir(destination, 0o700); err != nil {
		return fmt.Errorf("目标目录必须尚不存在：%w", err)
	}
	// 只清理本次持锁创建的目录；此 defer 必须先于锁释放执行。
	defer func() {
		if resultErr != nil {
			_ = os.RemoveAll(destination)
		}
	}()
	marker := filepath.Join(destination, database.PlatformRestoreIncompleteFileName)
	if err := writeSynced(marker, []byte("restore in progress\n")); err != nil {
		return err
	}
	if err := copyTree(ctx, filepath.Join(backup, "data"), destination, false); err != nil {
		return err
	}
	files, err := inventory(ctx, destination)
	if err != nil {
		return err
	}
	files = withoutRestoreMarker(files)
	if !sameFiles(files, manifest.Files) {
		return errors.New("恢复复制期间备份内容发生变化，目标未交付")
	}
	if err := fenceRestoredRuntime(ctx, destination, manifest.PlatformVersion); err != nil {
		return err
	}
	return os.Remove(marker)
}

func withoutRestoreMarker(files []File) []File {
	marker := filepath.ToSlash(database.PlatformRestoreIncompleteFileName)
	filtered := files[:0]
	for _, file := range files {
		if file.Path != marker {
			filtered = append(filtered, file)
		}
	}
	return filtered
}
