//go:build platform

package platformboot

import (
	"os"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/platformbackup"
)

func TestPlatformUpgradeCreatesCompleteBackupBeforeMigration(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "platform")
	cfg := Config{DataDir: dataDir, Origin: "https://platform.example.test"}
	runtime, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.MigrationBackup != "" {
		t.Fatal("空库不应产生迁移备份")
	}
	if err := runtime.db.Exec("DELETE FROM platform_schema_migrations WHERE version=?", platformauth.CurrentSchemaVersion).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"platform_asset_deletion_insert_guard", "platform_asset_deletion_update_guard"} {
		if err := runtime.db.Exec("DROP TRIGGER " + name).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dataDir, "resources"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "resources", "original"), []byte("resource"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	manifest, err := platformbackup.Verify(t.Context(), runtime.MigrationBackup)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PlatformVersion != platformauth.CurrentSchemaVersion-1 {
		t.Fatal("快照不在迁移之前")
	}
	var version int
	if err := runtime.db.Model(&platformauth.SchemaMigration{}).Select("MAX(version)").Scan(&version).Error; err != nil || version != platformauth.CurrentSchemaVersion {
		t.Fatal("迁移未完成", err)
	}
	value, err := os.ReadFile(filepath.Join(runtime.MigrationBackup, "data", "resources", "original"))
	if err != nil || string(value) != "resource" {
		t.Fatal("迁移备份不含资源", err)
	}
}

func TestPlatformMissingMasterKeyCannotBeReplaced(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "platform")
	cfg := Config{DataDir: dataDir, Origin: "https://platform.example.test"}
	runtime, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dataDir, ".settings-key")); err != nil {
		t.Fatal(err)
	}
	if unexpected, err := Open(t.Context(), cfg); err == nil {
		unexpected.Close()
		t.Fatal("为已有平台生成了替代主密钥")
	}
	if _, err := os.Stat(filepath.Join(dataDir, ".settings-key")); !os.IsNotExist(err) {
		t.Fatal("缺失密钥被覆盖", err)
	}
	db, err := database.Open(database.Config{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	var version int
	if err := db.Model(&platformauth.SchemaMigration{}).Select("MAX(version)").Scan(&version).Error; err != nil || version != platformauth.CurrentSchemaVersion {
		t.Fatal("失败启动修改了迁移版本", err)
	}
}
