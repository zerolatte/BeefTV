package desktopstorage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func createLocationTestLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("当前 Windows 环境不允许创建符号链接：%v", err)
		}
		t.Fatal(err)
	}
}

func TestCanonicalDirectoryRejectsDanglingFileAndDirectoryLinks(t *testing.T) {
	for _, directoryTarget := range []bool{false, true} {
		name := "file"
		if directoryTarget {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target, link := filepath.Join(root, "target"), filepath.Join(root, "link")
			if directoryTarget {
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(target, []byte("test"), 0o600); err != nil {
				t.Fatal(err)
			}
			createLocationTestLink(t, target, link)
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			paths := []string{link}
			if directoryTarget {
				paths = append(paths, filepath.Join(link, "future", "open_ai_canvas.db"))
			}
			for _, path := range paths {
				if resolved, err := CanonicalDirectory(path); err == nil || resolved != "" {
					t.Fatalf("悬空链接被当作未创建路径：%q，%q，%v", path, resolved, err)
				}
			}
			if _, err := os.Lstat(link); err != nil {
				t.Fatalf("校验修改了链接：%v", err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("校验创建了链接目标：%v", err)
			}
		})
	}
}

func TestCanonicalDirectoryPreservesMissingPathsUnderExistingParents(t *testing.T) {
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"plain", "linked"} {
		t.Run(prefix, func(t *testing.T) {
			parent := root
			if prefix == "linked" {
				parent = filepath.Join(t.TempDir(), "alias")
				createLocationTestLink(t, root, parent)
			}
			path := filepath.Join(parent, "future", "nested")
			resolved, err := CanonicalDirectory(path)
			if err != nil || resolved != filepath.Join(canonical, "future", "nested") {
				t.Fatalf("普通未创建路径解析失败：%q，%v", resolved, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("路径规范化不应创建目录：%v", err)
			}
		})
	}
}
