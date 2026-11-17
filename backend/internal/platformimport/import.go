//go:build platform

// Package platformimport 只在隔离副本上接纳经过完整审计的本地工作区。
package platformimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/runtimeinfo"
)

const databaseName = "open_ai_canvas.db"

type Input struct {
	Source      string
	Destination string
	Account     platformauth.CreateAccountInput
}

type Result struct {
	AccountID   string `json:"accountId"`
	WorkspaceID string `json:"workspaceId"`
	Fingerprint string `json:"fingerprint"`
	Repeated    bool   `json:"repeated"`
}

type modeMarker struct {
	ID          int           `json:"-"`
	Version     int           `json:"version"`
	Mode        identity.Mode `json:"mode"`
	DirectoryID string        `json:"directoryId"`
}

// Import 在锁定的源目录只读取证；所有迁移、脱敏与开户均发生在不可启动的副本。
func Import(ctx context.Context, input Input) (_ *Result, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source, destination, err := importPaths(input.Source, input.Destination)
	if err != nil {
		return nil, err
	}
	lock, err := runtimeinfo.LockWorkspace(source)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	db, closeSource, err := openReadOnly(filepath.Join(source, databaseName))
	if err != nil {
		return nil, err
	}
	defer closeSource()
	workspace, err := inspectSource(ctx, db, source)
	if err != nil {
		return nil, err
	}
	files, fingerprint, err := sourceInventory(ctx, source)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return repeatedResult(ctx, destination, fingerprint)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".platform-import-")
	if err != nil {
		return nil, err
	}
	// stage 是本调用在目标父目录独占创建的路径，源目录与目标已经验证互不包含。
	defer func() { _ = os.RemoveAll(stage) }()
	if err := writeFile(filepath.Join(stage, database.PlatformRestoreIncompleteFileName), []byte("import in progress\n")); err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Exec("VACUUM main INTO ?", filepath.Join(stage, databaseName)).Error; err != nil {
		return nil, fmt.Errorf("创建导入一致快照失败：%w", err)
	}
	if err := copyBusinessFiles(ctx, source, stage, files); err != nil {
		return nil, err
	}
	_, after, err := sourceInventory(ctx, source)
	if err != nil || after != fingerprint {
		return nil, errors.New("导入期间源文件发生变化，目标未交付")
	}
	target, err := database.Open(database.Config{DataDir: stage})
	if err != nil {
		return nil, err
	}
	sqlDB, err := target.DB()
	if err != nil {
		return nil, err
	}
	defer sqlDB.Close()
	if err := database.ConfigurePool(target); err != nil {
		return nil, err
	}
	// 清理 SQL 可见值之外的旧记录空隙；交付前再次重建，避免明文凭据残留在空闲页。
	if err := target.Exec("PRAGMA secure_delete=ON").Error; err != nil {
		return nil, err
	}
	if _, err := localcrypto.Key(stage); err != nil {
		return nil, err
	}
	if err := database.MigrateLocalSchema(target.WithContext(ctx)); err != nil {
		return nil, err
	}
	if err := scrubBusinessData(ctx, target, source, stage); err != nil {
		return nil, err
	}
	if err := target.WithContext(ctx).Exec("VACUUM").Error; err != nil {
		return nil, fmt.Errorf("重建净化数据库失败：%w", err)
	}
	if err := platformauth.Migrate(target.WithContext(ctx)); err != nil {
		return nil, err
	}
	account, err := platformauth.NewService(target).ImportAccount(ctx, platformauth.ImportAccountInput{CreateAccountInput: input.Account, WorkspaceID: workspace, SourceFingerprint: fingerprint})
	if err != nil {
		return nil, err
	}
	if err := initializeStoredObjects(ctx, target, account.ID, workspace); err != nil {
		return nil, err
	}
	marker := modeMarker{ID: 1, Version: 1, Mode: identity.ModePlatform, DirectoryID: uuid.NewString()}
	if err := target.WithContext(ctx).Table("runtime_mode_markers").Where("id = 1").Updates(map[string]any{"mode": marker.Mode, "directory_id": marker.DirectoryID}).Error; err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(marker)
	if err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(stage, database.RuntimeModeFileName), encoded); err != nil {
		return nil, err
	}
	if err := validateFinal(ctx, target, stage, workspace); err != nil {
		return nil, err
	}
	if err := target.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return nil, err
	}
	if err := sqlDB.Close(); err != nil {
		return nil, err
	}
	if err := publishStage(stage, destination); err != nil {
		return nil, err
	}
	return &Result{AccountID: account.ID, WorkspaceID: workspace, Fingerprint: fingerprint}, nil
}

// 带启动阻断标记原子发布，且不得替换其他进程刚创建的空目录。
func publishStage(stage, destination string) error {
	if info, err := os.Lstat(filepath.Join(stage, database.PlatformRestoreIncompleteFileName)); err != nil || !info.Mode().IsRegular() {
		return errors.New("导入暂存目录缺失启动阻断标记")
	}
	if err := renameNewDirectory(stage, destination); err != nil {
		return fmt.Errorf("无法原子交付到全新目标目录：%w", err)
	}
	return os.Remove(filepath.Join(destination, database.PlatformRestoreIncompleteFileName))
}

func repeatedResult(ctx context.Context, destination, fingerprint string) (*Result, error) {
	guard, err := database.PrepareRuntimeDirectory(destination, identity.ModePlatform)
	if err != nil {
		return nil, err
	}
	lock, err := runtimeinfo.LockWorkspace(guard.CanonicalPath())
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	db, closeDB, err := openReadOnly(filepath.Join(destination, databaseName))
	if err != nil {
		return nil, err
	}
	defer closeDB()
	var receipt platformauth.ImportReceipt
	if err := db.WithContext(ctx).Where("source_fingerprint = ?", fingerprint).Take(&receipt).Error; err != nil {
		return nil, errors.New("目标已存在且不是此来源的已完成导入")
	}
	if err := validateFinal(ctx, db, destination, receipt.SourceWorkspaceID); err != nil {
		return nil, err
	}
	return &Result{AccountID: receipt.AccountID, WorkspaceID: receipt.SourceWorkspaceID, Fingerprint: fingerprint, Repeated: true}, nil
}
