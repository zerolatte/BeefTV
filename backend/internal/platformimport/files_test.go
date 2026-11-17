//go:build platform

package platformimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
)

func TestImportRejectsUnmergedJournalAndMissingKey(t *testing.T) {
	for _, file := range []string{databaseName + "-wal", databaseName + "-journal", ".settings-key"} {
		t.Run(file, func(t *testing.T) {
			f := newSource(t)
			f.close(t)
			if file == ".settings-key" {
				if err := os.Remove(filepath.Join(f.source, file)); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := writeFile(filepath.Join(f.source, file), []byte("unmerged")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Import(context.Background(), f.input()); err == nil {
				t.Fatal("未拒绝缺失密钥或未合并日志")
			}
			if _, err := os.Lstat(f.destination); !os.IsNotExist(err) {
				t.Fatal("不完整来源生成目标")
			}
		})
	}
}

func TestImportRejectsLinkedRootAndResource(t *testing.T) {
	f := newSource(t)
	addResource(t, f)
	f.close(t)
	alias := filepath.Join(filepath.Dir(f.source), "alias")
	if err := os.Symlink(f.source, alias); err != nil {
		t.Skip("当前执行环境不允许创建符号链接", err)
	}
	input := f.input()
	input.Source = alias
	if _, err := Import(context.Background(), input); err == nil || !strings.Contains(err.Error(), "根目录") {
		t.Fatal("源根链接被接受", err)
	}
	if err := os.Remove(filepath.Join(f.source, "resources", "sample.png")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(f.source), "outside.png")
	if err := writeFile(outside, []byte("image")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.source, "resources", "sample.png")); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(context.Background(), f.input()); err == nil {
		t.Fatal("越界资源链接被接受")
	}
}

func TestImportRejectsDifferentSourceForCompletedDestination(t *testing.T) {
	f := newSource(t)
	f.close(t)
	if _, err := Import(context.Background(), f.input()); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(f.source, ".storage-v2-migrated"), []byte("changed")); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(context.Background(), f.input()); err == nil || !strings.Contains(err.Error(), "不是此来源") {
		t.Fatal("不同指纹被当作重复导入", err)
	}
}

func TestPublishedStageRemainsFencedUntilCommit(t *testing.T) {
	parent := t.TempDir()
	stage := filepath.Join(parent, "stage")
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(stage, database.PlatformRestoreIncompleteFileName), []byte("incomplete")); err != nil {
		t.Fatal(err)
	}
	if err := renameNewDirectory(stage, target); err != nil {
		t.Fatal(err)
	}
	if _, err := database.PrepareRuntimeDirectory(target, identity.ModePlatform); err == nil {
		t.Fatal("交付故障后的半成品可以启动")
	}
}
