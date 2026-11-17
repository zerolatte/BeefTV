//go:build platform

package platformbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/runtimeinfo"
)

func fixture(t *testing.T) (string, *gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "platform")
	guard, err := database.PrepareRuntimeDirectory(path, identity.ModePlatform)
	if err != nil {
		t.Fatal(err)
	}
	path = guard.CanonicalPath()
	db, err := database.Open(database.Config{DataDir: path})
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sql.Close() })
	for _, err := range []error{database.ConfigurePool(db), guard.CheckDatabase(db), database.MigrateLocalSchema(db), platformauth.Migrate(db)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := localcrypto.Key(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "resources"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "resources", "video.bin"), []byte("original-media"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE backup_probe (id INTEGER PRIMARY KEY, value TEXT NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO backup_probe VALUES (1, 'before')").Error; err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestBackupRestoresDatabaseResourcesAndKey(t *testing.T) {
	source, db := fixture(t)
	now := time.Now().UTC()
	session := platformauth.Session{ID: "snapshot-session", AccountID: "snapshot-account", TokenHash: "snapshot-token", CSRFHash: "snapshot-csrf", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot")
	manifest, err := Create(t.Context(), source, backup)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.LocalVersion != database.CurrentSchemaVersion || manifest.PlatformVersion != platformauth.CurrentSchemaVersion {
		t.Fatalf("备份版本错误：%+v", manifest)
	}
	if err := db.Exec("UPDATE backup_probe SET value='after'").Error; err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "resources", "video.bin"), []byte("changed-media"), 0o600); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := Restore(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(restored, database.PlatformRestoreIncompleteFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("成功恢复后仍保留未完成标记", err)
	}
	copyDB, closeDB, err := openDatabase(filepath.Join(restored, databaseName), true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	var value string
	if err := copyDB.Raw("SELECT value FROM backup_probe WHERE id=1").Scan(&value).Error; err != nil || value != "before" {
		t.Fatalf("未恢复一致数据库：%s %v", value, err)
	}
	var restoredSession platformauth.Session
	if err := copyDB.First(&restoredSession, "id=?", session.ID).Error; err != nil || restoredSession.RevokedAt == nil {
		t.Fatal("恢复后旧会话仍然有效", err)
	}
	if err := db.First(&session, "id=?", session.ID).Error; err != nil || session.RevokedAt != nil {
		t.Fatal("恢复修改了源库会话", err)
	}
	media, err := os.ReadFile(filepath.Join(restored, "resources", "video.bin"))
	if err != nil || string(media) != "original-media" {
		t.Fatal("资源未恢复", err)
	}
	key, err := os.ReadFile(filepath.Join(source, ".settings-key"))
	if err != nil {
		t.Fatal(err)
	}
	copyKey, err := os.ReadFile(filepath.Join(restored, ".settings-key"))
	if err != nil || !bytes.Equal(key, copyKey) {
		t.Fatal("主密钥未随数据库恢复", err)
	}
	localGuard, err := database.PrepareRuntimeDirectory(restored, identity.ModeLocal)
	if err == nil || localGuard != nil {
		t.Fatal("恢复后的平台库不能交给local模式")
	}
}

func TestBackupRejectsIncompleteRestoreDirectory(t *testing.T) {
	source, _ := fixture(t)
	marker := filepath.Join(source, database.PlatformRestoreIncompleteFileName)
	if err := os.WriteFile(marker, []byte("restore in progress\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(t.Context(), source, filepath.Join(t.TempDir(), "snapshot")); err == nil {
		t.Fatal("未完成恢复目录不能创建备份")
	}
}

func TestBackupRefusesLiveWorkspaceAndExistingDestination(t *testing.T) {
	source, _ := fixture(t)
	lock, err := runtimeinfo.LockWorkspace(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "backup")
	if _, err := Create(t.Context(), source, destination); err == nil {
		t.Fatal("活动工作区被备份")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("锁失败写了目标目录")
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(t.Context(), source, destination); err == nil {
		t.Fatal("覆盖了已有目录")
	}
	if value, err := os.ReadFile(filepath.Join(destination, "keep")); err != nil || string(value) != "keep" {
		t.Fatal("原目标文件被修改", err)
	}
	if _, err := Create(t.Context(), source, filepath.Join(source, "nested")); err == nil {
		t.Fatal("允许在源内创建备份")
	}
}

func TestBackupRejectsDamageAndDoesNotDeliverPartialRestore(t *testing.T) {
	source, _ := fixture(t)
	for _, damage := range []string{"file", "missing", "extra", "manifest", "key", "marker"} {
		t.Run(damage, func(t *testing.T) {
			backup := filepath.Join(t.TempDir(), "snapshot")
			if _, err := Create(t.Context(), source, backup); err != nil {
				t.Fatal(err)
			}
			data := filepath.Join(backup, "data")
			switch damage {
			case "file":
				_ = os.WriteFile(filepath.Join(data, "resources", "video.bin"), []byte("corrupt"), 0o600)
			case "missing":
				_ = os.Remove(filepath.Join(data, "resources", "video.bin"))
			case "extra":
				_ = os.WriteFile(filepath.Join(data, "unexpected"), []byte("extra"), 0o600)
			case "manifest":
				_ = os.Remove(filepath.Join(backup, manifestName))
			case "key":
				_ = os.WriteFile(filepath.Join(data, ".settings-key"), []byte("short"), 0o600)
			case "marker":
				_ = os.WriteFile(filepath.Join(data, database.RuntimeModeFileName), []byte(`{"version":1,"mode":"local"}`), 0o600)
			}
			if _, err := Verify(t.Context(), backup); err == nil {
				t.Fatal("损坏备份通过校验")
			}
			destination := filepath.Join(t.TempDir(), "restore")
			if err := Restore(t.Context(), backup, destination); err == nil {
				t.Fatal("损坏备份被恢复")
			}
			if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("损坏备份留下已交付目录")
			}
		})
	}
}

func TestBackupCancellationAndModeMismatchLeaveSourceUntouched(t *testing.T) {
	source, _ := fixture(t)
	before, err := inventory(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Create(ctx, source, filepath.Join(t.TempDir(), "cancelled")); err == nil {
		t.Fatal("取消后仍然创建备份")
	}
	after, err := inventory(t.Context(), source)
	if err != nil || !sameFiles(before, after) {
		t.Fatal("取消备份修改了源文件", err)
	}
	var marker modeMarker
	if err := readJSON(filepath.Join(source, database.RuntimeModeFileName), &marker); err != nil {
		t.Fatal(err)
	}
	marker.Mode = identity.ModeLocal
	value, _ := json.Marshal(marker)
	if err := os.WriteFile(filepath.Join(source, database.RuntimeModeFileName), value, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(t.Context(), source, filepath.Join(t.TempDir(), "badmode")); err == nil {
		t.Fatal("混合模式被备份")
	}
}

func TestBeforeMigrationBacksUpOnlyBeforeVersionChange(t *testing.T) {
	source, db := fixture(t)
	path, err := BeforeMigration(t.Context(), db, source, platformauth.CurrentSchemaVersion)
	if err != nil || path != "" {
		t.Fatal("当前版本不应重复备份", path, err)
	}
	if err := db.Exec("DELETE FROM platform_schema_migrations WHERE version=?", platformauth.CurrentSchemaVersion).Error; err != nil {
		t.Fatal(err)
	}
	path, err = BeforeMigration(t.Context(), db, source, platformauth.CurrentSchemaVersion)
	if err != nil || path == "" {
		t.Fatal("升级前未备份", err)
	}
	manifest, err := Verify(t.Context(), path)
	if err != nil || manifest.PlatformVersion != platformauth.CurrentSchemaVersion-1 {
		t.Fatal("备份不是迁移前版本", err)
	}
	if err := os.Remove(filepath.Join(source, ".settings-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := BeforeMigration(t.Context(), db, source, platformauth.CurrentSchemaVersion); err == nil {
		t.Fatal("缺主密钥仍允许迁移")
	}
}
