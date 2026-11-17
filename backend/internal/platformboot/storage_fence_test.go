//go:build platform

package platformboot

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/runtimeinfo"
)

func TestPlatformRejectsExternalDatabaseLinkBeforeOpening(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "missing_target"
		if existing {
			name = "existing_target"
		}
		t.Run(name, func(t *testing.T) {
			directory, external := t.TempDir(), t.TempDir()
			target := filepath.Join(external, "outside.db")
			var original []byte
			if existing {
				db, err := database.Open(database.Config{DSN: target + "?_journal_mode=DELETE"})
				if err != nil {
					t.Fatal(err)
				}
				connection, err := db.DB()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = connection.Close() })
				if err := db.Exec("CREATE TABLE external_data (id INTEGER PRIMARY KEY)").Error; err != nil {
					t.Fatal(err)
				}
				if err := connection.Close(); err != nil {
					t.Fatal(err)
				}
				original, err = os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, filepath.Join(directory, "open_ai_canvas.db")); err != nil {
				t.Skipf("当前系统不支持创建符号链接：%v", err)
			}
			started, err := Open(t.Context(), Config{DataDir: directory, Origin: "https://platform.test"})
			if started != nil {
				_ = started.Close()
			}
			if err == nil || started != nil {
				t.Fatal("平台接受了指向目录外的数据库链接")
			}
			entries, err := os.ReadDir(external)
			if err != nil {
				t.Fatal(err)
			}
			if existing {
				body, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(body, original) || len(entries) != 1 {
					t.Fatalf("启动拒绝前修改了外部文件：%v", err)
				}
			} else if len(entries) != 0 {
				t.Fatalf("启动拒绝前创建了外部文件：%v", entries)
			}
			lock, err := runtimeinfo.LockWorkspace(directory)
			if err != nil {
				t.Fatalf("启动拒绝后未释放工作区锁：%v", err)
			}
			_ = lock.Close()
		})
	}
}

func TestPlatformRejectsHardLinkedDatabaseAndReopensAfterUnlink(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	cfg := Config{DataDir: first, Origin: "https://platform.test"}
	started, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := started.Close(); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(first, "open_ai_canvas.db")
	alias := filepath.Join(second, "open_ai_canvas.db")
	if err := os.Link(source, alias); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{first, second} {
		candidate, err := Open(t.Context(), Config{DataDir: directory, Origin: cfg.Origin})
		if candidate != nil {
			_ = candidate.Close()
		}
		if err == nil || candidate != nil {
			t.Fatal("不同目录锁允许打开同一个硬链接数据库")
		}
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("拒绝硬链接前修改了数据库：%v", err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("解除硬链接后无法重新打开原平台：%v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}
