//go:build platform

package platformbackup

import (
	"os"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/database"
)

func TestBackupOpenDatabaseRejectsHardLinks(t *testing.T) {
	for _, immutable := range []bool{false, true} {
		name := "create"
		if immutable {
			name = "verify"
		}
		t.Run(name, func(t *testing.T) {
			path := backupDatabaseFileFixture(t)
			readOriginal := func() {
				t.Helper()
				db, closeDB, err := openDatabase(path, immutable)
				if err != nil {
					t.Fatal(err)
				}
				defer closeDB()
				var value string
				if err := db.Raw("SELECT value FROM file_probe WHERE id = 1").Scan(&value).Error; err != nil || value != "原始数据" {
					t.Fatalf("原始数据读取失败或被修改：%q，%v", value, err)
				}
			}
			readOriginal()
			alias := filepath.Join(t.TempDir(), "alias.db")
			if err := os.Link(path, alias); err != nil {
				t.Fatalf("创建硬链接失败，未完成行为验证：%v", err)
			}
			for _, linkedPath := range []string{path, alias} {
				db, closeDB, err := openDatabase(linkedPath, immutable)
				if closeDB != nil {
					closeDB()
				}
				if err == nil || db != nil || closeDB != nil {
					t.Fatalf("硬链接数据库未在打开前拒绝：%s，%v", linkedPath, err)
				}
			}
			if err := os.Remove(alias); err != nil {
				t.Fatal(err)
			}
			readOriginal()
		})
	}
}

func TestBackupOpenDatabaseRejectsMissingFileAndDirectory(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{filepath.Join(root, "missing.db"), root} {
		for _, immutable := range []bool{false, true} {
			db, closeDB, err := openDatabase(path, immutable)
			if closeDB != nil {
				closeDB()
			}
			if err == nil || db != nil || closeDB != nil {
				t.Fatalf("缺失文件或目录被作为备份数据库打开：%s，%v", path, err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "missing.db")); !os.IsNotExist(err) {
		t.Fatalf("检查缺失数据库时创建了文件：%v", err)
	}
}

func backupDatabaseFileFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "original.db")
	db, err := database.Open(database.Config{DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("CREATE TABLE file_probe (id INTEGER PRIMARY KEY, value TEXT NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO file_probe (id, value) VALUES (1, ?)", "原始数据").Error; err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
