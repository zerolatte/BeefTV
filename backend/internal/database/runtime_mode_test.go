package database

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/identity"

	"gorm.io/gorm"
)

func openRuntimeModeDatabase(t *testing.T, directory string) *gorm.DB {
	t.Helper()
	db, err := Open(Config{DataDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePool(db); err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return db
}

func prepareRuntimeModeDirectory(t *testing.T, directory string, mode identity.Mode) *RuntimeDirectory {
	t.Helper()
	guard, err := PrepareRuntimeDirectory(directory, mode)
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

func TestRuntimeModeInitializesEmptyPlatformAndReopens(t *testing.T) {
	directory := t.TempDir()
	guard := prepareRuntimeModeDirectory(t, directory, identity.ModePlatform)
	if _, err := os.Stat(filepath.Join(directory, RuntimeModeFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("准备目录不应提前认领数据：%v", err)
	}
	db := openRuntimeModeDatabase(t, directory)
	if err := guard.CheckDatabase(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE platform_accounts (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	reopened := prepareRuntimeModeDirectory(t, directory, identity.ModePlatform)
	if err := reopened.CheckDatabase(db); err != nil {
		t.Fatalf("同模式重启失败：%v", err)
	}
	if guard.InstanceID == "" || guard.InstanceID != reopened.InstanceID {
		t.Fatal("平台实例标识必须在重启后保持不变")
	}
	if _, err := PrepareRuntimeDirectory(directory, identity.ModeLocal); !errors.Is(err, ErrRuntimeModeMismatch) {
		t.Fatalf("本地程序未在打开数据库前拒绝平台目录：%v", err)
	}
}

func TestRuntimeModeRejectsIncompletePlatformRestore(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, PlatformRestoreIncompleteFileName)
	if err := os.WriteFile(marker, []byte("restore in progress\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareRuntimeDirectory(directory, identity.ModePlatform); err == nil {
		t.Fatal("恢复未完成的目录不能启动")
	}
	if _, err := PrepareRuntimeDirectory(directory, identity.ModeLocal); err == nil {
		t.Fatal("恢复未完成的目录不能切换到本地模式启动")
	}
}

func TestRuntimeModeAdoptsLegacyOnlyAsLocal(t *testing.T) {
	for _, mode := range []identity.Mode{identity.ModeLocal, identity.ModePlatform} {
		t.Run(string(mode), func(t *testing.T) {
			directory := t.TempDir()
			db := openRuntimeModeDatabase(t, directory)
			if err := db.Exec("CREATE TABLE projects (id TEXT PRIMARY KEY)").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("INSERT INTO projects (id) VALUES ('legacy-project')").Error; err != nil {
				t.Fatal(err)
			}
			guard := prepareRuntimeModeDirectory(t, directory, mode)
			err := guard.CheckDatabase(db)
			if mode == identity.ModeLocal && err != nil {
				t.Fatalf("历史本地数据库无法升级：%v", err)
			}
			if mode == identity.ModePlatform {
				if err == nil || db.Migrator().HasTable(&runtimeModeMarker{}) {
					t.Fatalf("平台错误认领已有库：%v", err)
				}
				if _, statErr := os.Stat(filepath.Join(directory, RuntimeModeFileName)); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("拒绝启动后不应留下认领标记：%v", statErr)
				}
			}
			var count int64
			if err := db.Table("projects").Where("id = ?", "legacy-project").Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("启动检查修改了业务数据：count=%d err=%v", count, err)
			}
		})
	}
}

func TestRuntimeModeRemovedDirectoryMarkerCannotChangeMode(t *testing.T) {
	for _, mode := range []identity.Mode{identity.ModeLocal, identity.ModePlatform} {
		t.Run(string(mode), func(t *testing.T) {
			directory := t.TempDir()
			guard := prepareRuntimeModeDirectory(t, directory, mode)
			db := openRuntimeModeDatabase(t, directory)
			if err := guard.CheckDatabase(db); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(directory, RuntimeModeFileName)); err != nil {
				t.Fatal(err)
			}
			other := identity.ModeLocal
			if mode == identity.ModeLocal {
				other = identity.ModePlatform
			}
			wrong := prepareRuntimeModeDirectory(t, directory, other)
			if err := wrong.CheckDatabase(db); !errors.Is(err, ErrRuntimeModeMismatch) {
				t.Fatalf("删除目录标记后跨模式打开成功：%v", err)
			}
			recovery := prepareRuntimeModeDirectory(t, directory, mode)
			if err := recovery.CheckDatabase(db); err != nil {
				t.Fatalf("原模式不能恢复未完成的目录标记写入：%v", err)
			}
		})
	}
}

func TestRuntimeModeMissingDatabaseMarkerFailsClosed(t *testing.T) {
	directory := t.TempDir()
	guard := prepareRuntimeModeDirectory(t, directory, identity.ModePlatform)
	db := openRuntimeModeDatabase(t, directory)
	if err := guard.CheckDatabase(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&runtimeModeMarker{}); err != nil {
		t.Fatal(err)
	}
	reopened := prepareRuntimeModeDirectory(t, directory, identity.ModePlatform)
	if err := reopened.CheckDatabase(db); !errors.Is(err, ErrRuntimeModeMismatch) {
		t.Fatalf("丢失数据库标记后仍可启动：%v", err)
	}
	if _, err := PrepareRuntimeDirectory(directory, identity.ModeLocal); !errors.Is(err, ErrRuntimeModeMismatch) {
		t.Fatalf("删除数据库标记后跨模式启动成功：%v", err)
	}
}

func TestRuntimeModeRejectsSwappedDatabaseWithinSameMode(t *testing.T) {
	firstDir, secondDir := t.TempDir(), t.TempDir()
	first := prepareRuntimeModeDirectory(t, firstDir, identity.ModeLocal)
	second := prepareRuntimeModeDirectory(t, secondDir, identity.ModeLocal)
	firstDB, secondDB := openRuntimeModeDatabase(t, firstDir), openRuntimeModeDatabase(t, secondDir)
	if err := first.CheckDatabase(firstDB); err != nil {
		t.Fatal(err)
	}
	if err := second.CheckDatabase(secondDB); err != nil {
		t.Fatal(err)
	}
	// 把另一目录的数据库副本放入受锁目录，继续验证目录身份而非路径拒绝。
	replacementPath := filepath.Join(firstDir, "replacement.db")
	if err := secondDB.Exec("VACUUM INTO ?", replacementPath).Error; err != nil {
		t.Fatal(err)
	}
	replacement, err := Open(Config{DSN: replacementPath})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := replacement.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := first.CheckDatabase(replacement); !errors.Is(err, ErrRuntimeModeMismatch) {
		t.Fatalf("同模式替换数据库未被识别：%v", err)
	}
}

func TestRuntimeModeRejectsUnmarkedPlatformTablesAsLocal(t *testing.T) {
	directory := t.TempDir()
	db := openRuntimeModeDatabase(t, directory)
	if err := db.Exec("CREATE TABLE platform_accounts (id TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	guard := prepareRuntimeModeDirectory(t, directory, identity.ModeLocal)
	if err := guard.CheckDatabase(db); !errors.Is(err, ErrRuntimeModeMismatch) {
		t.Fatalf("丢失双端标记的平台库被误认成本地库：%v", err)
	}
}

func TestRuntimeModeRejectsUnmarkedResourcesAndExternalDatabase(t *testing.T) {
	t.Run("旧主密钥", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, ".settings-key"), []byte("test-only-placeholder"), 0o600); err != nil {
			t.Fatal(err)
		}
		guard := prepareRuntimeModeDirectory(t, directory, identity.ModePlatform)
		db := openRuntimeModeDatabase(t, directory)
		if err := guard.CheckDatabase(db); err == nil {
			t.Fatal("平台初始化接受了已有主密钥")
		}
	})
	t.Run("目录外数据库", func(t *testing.T) {
		guard := prepareRuntimeModeDirectory(t, t.TempDir(), identity.ModePlatform)
		db := openRuntimeModeDatabase(t, t.TempDir())
		if err := guard.CheckDatabase(db); err == nil {
			t.Fatal("平台初始化接受了目录外数据库")
		}
	})
	t.Run("目录标记损坏", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, RuntimeModeFileName), []byte("invalid"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := PrepareRuntimeDirectory(directory, identity.ModeLocal); err == nil {
			t.Fatal("损坏的目录标记被忽略")
		}
	})
}

func TestRuntimeModeRejectsMemoryPlatformButAcceptsLocal(t *testing.T) {
	db, err := Open(Config{DSN: "file:runtime-mode-memory?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	platform := prepareRuntimeModeDirectory(t, t.TempDir(), identity.ModePlatform)
	if err := platform.CheckDatabase(db); err == nil {
		t.Fatal("平台接受了临时内存数据库")
	}
	localDir := t.TempDir()
	local := prepareRuntimeModeDirectory(t, localDir, identity.ModeLocal)
	if err := local.CheckDatabase(db); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(localDir, RuntimeModeFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("本地临时数据库不应持久绑定目录：%v", err)
	}
}
