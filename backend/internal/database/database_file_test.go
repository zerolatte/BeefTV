package database

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/identity"
)

func TestValidateDatabaseFileAllowsMissingWithoutCreating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if err := ValidateDatabaseFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("预检不应创建数据库文件：%v", err)
	}
}

func TestValidateDatabaseFileReadsRegularFileWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.db")
	content := []byte("database validation probe")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDatabaseFile(path); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, content) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("校验修改了只读文件：%v", err)
	}
}

func TestValidateDatabaseFileRejectsDirectory(t *testing.T) {
	if err := ValidateDatabaseFile(t.TempDir()); err == nil {
		t.Fatal("目录不能作为数据库主文件")
	}
}

func TestValidateDatabaseFileRejectsHardLinksAndAllowsUnlinkedFile(t *testing.T) {
	directory := t.TempDir()
	path, alias := filepath.Join(directory, "database.db"), filepath.Join(directory, "alias.db")
	if err := os.WriteFile(path, []byte("database validation probe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, alias); err != nil {
		t.Fatalf("创建真实硬链接失败：%v", err)
	}
	for _, name := range []string{path, alias} {
		if err := ValidateDatabaseFile(name); err == nil {
			t.Fatal("具有多个目录项的数据库未被拒绝")
		}
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDatabaseFile(path); err != nil {
		t.Fatalf("解除硬链接后普通文件仍被拒绝：%v", err)
	}
}

func TestRuntimeDatabaseDSNRejectsHardLinks(t *testing.T) {
	for _, mode := range []identity.Mode{identity.ModeLocal, identity.ModePlatform} {
		t.Run(string(mode), func(t *testing.T) {
			directory := t.TempDir()
			guard := prepareRuntimeModeDirectory(t, directory, mode)
			path := filepath.Join(directory, "open_ai_canvas.db")
			alias := filepath.Join(t.TempDir(), "alias.db")
			if err := os.WriteFile(path, []byte("database validation probe"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(path, alias); err != nil {
				t.Fatal(err)
			}
			if err := guard.ValidateDatabaseDSN(""); err == nil {
				t.Fatal("默认数据库预检接受了跨目录硬链接")
			}
			if err := guard.ValidateDatabaseDSN(path); err == nil {
				t.Fatal("显式数据库预检接受了跨目录硬链接")
			}
			if err := os.Remove(alias); err != nil {
				t.Fatal(err)
			}
			if err := guard.ValidateDatabaseDSN(""); err != nil {
				t.Fatalf("解除硬链接后预检仍失败：%v", err)
			}
		})
	}
}
