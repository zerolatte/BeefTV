//go:build platform

package platformbackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/runtimeinfo"
)

func TestRestoreLocksMissingDestinationBeforeCreatingIt(t *testing.T) {
	source, _ := fixture(t)
	backup := filepath.Join(t.TempDir(), "snapshot")
	if _, err := Create(t.Context(), source, backup); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restore")
	lock, err := runtimeinfo.LockWorkspace(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := Restore(t.Context(), backup, destination); err == nil {
		t.Fatal("活跃目标锁未阻止恢复")
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("锁冲突仍然创建了目标", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Restore(t.Context(), backup, destination); err != nil {
		t.Fatal("释放目标锁后无法恢复", err)
	}
}

func TestRestoreNeverDeletesExistingDestination(t *testing.T) {
	source, _ := fixture(t)
	backup := filepath.Join(t.TempDir(), "snapshot")
	if _, err := Create(t.Context(), source, backup); err != nil {
		t.Fatal(err)
	}
	for _, active := range []bool{false, true} {
		destination := t.TempDir()
		if err := os.WriteFile(filepath.Join(destination, "keep"), []byte("原有数据"), 0600); err != nil {
			t.Fatal(err)
		}
		var lock *runtimeinfo.WorkspaceLock
		if active {
			var err error
			lock, err = runtimeinfo.LockWorkspace(destination)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := Restore(t.Context(), backup, destination); err == nil {
			t.Fatal("恢复接受了已存在目标")
		}
		if lock != nil {
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
		}
		value, err := os.ReadFile(filepath.Join(destination, "keep"))
		if err != nil || string(value) != "原有数据" {
			t.Fatal("恢复失败清理了原有数据", err)
		}
	}
}

type cancelDuringRestore struct {
	context.Context
	destination            string
	observed, lockBypassed bool
}

func (c *cancelDuringRestore) Err() error {
	if _, err := os.Stat(filepath.Join(c.destination, database.PlatformRestoreIncompleteFileName)); err != nil {
		return c.Context.Err()
	}
	if !c.observed {
		c.observed = true
		if lock, err := runtimeinfo.LockWorkspace(c.destination); err == nil {
			c.lockBypassed = true
			_ = lock.Close()
		}
	}
	return context.Canceled
}

func TestRestoreCancellationKeepsDestinationLockedThroughWorkAndRemovesOnlyItsFiles(t *testing.T) {
	source, _ := fixture(t)
	backup := filepath.Join(t.TempDir(), "snapshot")
	if _, err := Create(t.Context(), source, backup); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restore")
	ctx := &cancelDuringRestore{Context: t.Context(), destination: destination}
	if err := Restore(ctx, backup, destination); !errors.Is(err, context.Canceled) {
		t.Fatal("恢复期间取消未生效", err)
	}
	if !ctx.observed || ctx.lockBypassed {
		t.Fatal("恢复期间未持续持有目标锁")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("取消后留下半成品目标", err)
	}
	lock, err := runtimeinfo.LockWorkspace(destination)
	if err != nil {
		t.Fatal("清理完成后未释放目标锁", err)
	}
	_ = lock.Close()
	if _, err := Verify(t.Context(), backup); err != nil {
		t.Fatal("取消恢复损坏备份", err)
	}
}

func TestRestoreTargetParentAliasSharesLock(t *testing.T) {
	source, _ := fixture(t)
	backup := filepath.Join(t.TempDir(), "snapshot")
	if _, err := Create(t.Context(), source, backup); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	parent := filepath.Join(root, "actual")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Skip("当前环境不允许目录符号链接", err)
	}
	destination := filepath.Join(parent, "restored")
	lock, err := runtimeinfo.LockWorkspace(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := Restore(t.Context(), backup, filepath.Join(alias, "restored")); err == nil {
		t.Fatal("父目录别名绕过目标锁")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("别名锁冲突仍创建目标", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Restore(t.Context(), backup, filepath.Join(alias, "restored")); err != nil {
		t.Fatal("别名规范化后无法恢复", err)
	}
}
