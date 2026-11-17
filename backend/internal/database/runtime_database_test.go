package database

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"infinite-canvas/backend/internal/identity"
)

func TestRuntimeDatabaseAddressMatchesOpenedFile(t *testing.T) {
	for _, format := range []string{"plain", "opaque", "uri"} {
		t.Run(format, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "workspace space")
			guard := prepareRuntimeModeDirectory(t, directory, identity.ModeLocal)
			path := filepath.Join(directory, "custom.db")
			dsn := path
			if format == "opaque" {
				dsn = "file:" + filepath.ToSlash(path)
			}
			if format == "uri" {
				uriPath := filepath.ToSlash(path)
				if runtime.GOOS == "windows" {
					uriPath = "/" + uriPath
				}
				dsn = (&url.URL{Scheme: "file", Path: uriPath}).String()
			}
			dsn += "?_busy_timeout=5000&_foreign_keys=on"
			if err := guard.ValidateDatabaseDSN(dsn); err != nil {
				t.Fatal(err)
			}
			db, err := Open(Config{DataDir: directory, DSN: dsn})
			if err != nil {
				t.Fatal(err)
			}
			connection, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			if err := guard.CheckDatabase(db); err != nil {
				t.Fatalf("实际 SQLite 文件与预检归属不一致：%v", err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("SQLite 未写入预期文件：%v", err)
			}
		})
	}
}

func TestRuntimeDatabaseRejectsExternalAndNestedFiles(t *testing.T) {
	for _, mode := range []identity.Mode{identity.ModeLocal, identity.ModePlatform} {
		t.Run(string(mode), func(t *testing.T) {
			directory, external := t.TempDir(), t.TempDir()
			guard := prepareRuntimeModeDirectory(t, directory, mode)
			for _, path := range []string{filepath.Join(external, "external.db"), filepath.Join(directory, "nested", "nested.db")} {
				for _, dsn := range []string{path, "file:" + filepath.ToSlash(path) + "?mode=rwc"} {
					if err := guard.ValidateDatabaseDSN(dsn); err == nil {
						t.Fatalf("预检接受了不同工作区锁保护的数据库：%s", dsn)
					}
				}
			}
			externalDB := openRuntimeModeDatabase(t, external)
			if err := guard.CheckDatabase(externalDB); err == nil {
				t.Fatal("实际数据库校验接受了目录外文件")
			}
			if externalDB.Migrator().HasTable("runtime_mode_markers") {
				t.Fatal("归属校验失败仍改写了外部数据库")
			}
			if err := guard.ValidateDatabaseDSN("file:memory?mode=memory&mode=rwc"); err == nil {
				t.Fatal("接受了有歧义的数据库存储模式")
			}
		})
	}
}
