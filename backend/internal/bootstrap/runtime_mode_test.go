package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/runtimeinfo"
)

func TestLocalEntrypointsRejectPlatformBeforeMigration(t *testing.T) {
	for _, profile := range []Profile{ProfileDesktop, ProfileServer} {
		for _, removeDirectoryMarker := range []bool{false, true} {
			name := string(profile)
			if removeDirectoryMarker {
				name += "_missing_directory_marker"
			}
			t.Run(name, func(t *testing.T) {
				directory := t.TempDir()
				guard, err := database.PrepareRuntimeDirectory(directory, identity.ModePlatform)
				if err != nil {
					t.Fatal(err)
				}
				db, err := database.Open(database.Config{DataDir: directory})
				if err != nil {
					t.Fatal(err)
				}
				connection, err := db.DB()
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				if err := guard.CheckDatabase(db); err != nil {
					t.Fatal(err)
				}
				// 本地历史迁移会清理此表；拒绝必须发生在迁移之前。
				if err := db.Exec("CREATE TABLE billing_orders (id TEXT PRIMARY KEY)").Error; err != nil {
					t.Fatal(err)
				}
				if removeDirectoryMarker {
					if err := os.Remove(filepath.Join(directory, database.RuntimeModeFileName)); err != nil {
						t.Fatal(err)
					}
				}
				runtime, err := Open(t.Context(), Config{Profile: profile, DataDir: directory, AutoMigrate: true})
				if runtime != nil {
					_ = runtime.Close(t.Context())
				}
				if !errors.Is(err, database.ErrRuntimeModeMismatch) {
					t.Fatalf("本地入口未拒绝平台数据库：%v", err)
				}
				if !db.Migrator().HasTable("billing_orders") || db.Migrator().HasTable("local_schema_migrations") {
					t.Fatal("拒绝启动前执行了本地迁移")
				}
			})
		}
	}
}

func TestLocalEntrypointsRespectMaintenanceWorkspaceLock(t *testing.T) {
	for _, profile := range []Profile{ProfileDesktop, ProfileServer} {
		t.Run(string(profile), func(t *testing.T) {
			directory := t.TempDir()
			lock, err := runtimeinfo.LockWorkspace(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			started, err := Open(t.Context(), Config{Profile: profile, DataDir: directory, AutoMigrate: true})
			if started != nil {
				_ = started.Close(t.Context())
			}
			if err == nil || started != nil {
				t.Fatal("本地入口绕过维护锁打开工作区")
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 0 {
				t.Fatalf("未取得锁已创建数据库或模式标记：%v，%v", entries, err)
			}
		})
	}
}

func TestLocalServerHoldsWorkspaceLockUntilClose(t *testing.T) {
	directory := t.TempDir()
	started, err := Open(t.Context(), Config{Profile: ProfileServer, DataDir: directory, AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = started.Close(t.Context()) })
	if lock, err := runtimeinfo.LockWorkspace(directory); err == nil {
		_ = lock.Close()
		t.Fatal("本地服务运行时导入工具仍能取得停写锁")
	}
	if err := started.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	lock, err := runtimeinfo.LockWorkspace(directory)
	if err != nil {
		t.Fatalf("正常停止本地服务后未释放停写锁：%v", err)
	}
	_ = lock.Close()
}

func TestLocalServerRejectsExternalDatabaseBeforeOpening(t *testing.T) {
	for _, useURI := range []bool{false, true} {
		directory, external := t.TempDir(), t.TempDir()
		path := filepath.Join(external, "outside.db")
		dsn := path
		if useURI {
			dsn = "file:" + filepath.ToSlash(path) + "?_journal_mode=WAL"
		}
		started, err := Open(t.Context(), Config{Profile: ProfileServer, DataDir: directory, DatabaseURL: dsn, AutoMigrate: true})
		if started != nil {
			_ = started.Close(t.Context())
		}
		if err == nil {
			t.Fatal("本地服务接受了目录外数据库")
		}
		entries, err := os.ReadDir(external)
		if err != nil || len(entries) != 0 {
			t.Fatalf("拒绝目录外配置之前已创建文件：%v，%v", entries, err)
		}
	}
}
