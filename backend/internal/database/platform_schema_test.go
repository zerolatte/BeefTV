package database

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformDirectoryNeverTakesOverLocalData(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old-project.db")
	if err := os.WriteFile(old, []byte("old-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PreparePlatformDirectory(dir); err == nil {
		t.Fatal("接管了已有目录")
	}
	content, err := os.ReadFile(old)
	if err != nil || string(content) != "old-data" {
		t.Fatal("修改了原本地数据")
	}
	fresh := t.TempDir()
	if err := PreparePlatformDirectory(fresh); err != nil {
		t.Fatal(err)
	}
	if err := PreparePlatformDirectory(fresh); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fresh, "platform-instance"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PreparePlatformDirectory(fresh); err == nil {
		t.Fatal("接受了无效平台标记")
	}
}

func TestPlatformMigrationResumesAfterSharedSchemaAndRejectsLocalDatabase(t *testing.T) {
	for _, platformStarted := range []bool{false, true} {
		t.Run(map[bool]string{false: "拒绝单人库", true: "恢复中断初始化"}[platformStarted], func(t *testing.T) {
			db, err := Open(Config{Driver: "sqlite", DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
			if platformStarted {
				if err := db.Exec("CREATE TABLE platform_schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)").Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := MigrateLocalSchema(db); err != nil {
				t.Fatal(err)
			}
			err = MigratePlatformSchema(db)
			if !platformStarted {
				if err == nil {
					t.Fatal("接管了单人数据库")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := MigratePlatformSchema(db); err != nil {
				t.Fatal(err)
			}
			if err := RequirePlatformSchema(db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
