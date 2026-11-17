package runtimeinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/desktopstorage"
)

func TestWindowsWorkspaceLockExcludesOtherUserProfiles(t *testing.T) {
	directory, firstHome, secondHome := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", firstHome)
	first, err := LockWorkspace(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if !strings.HasPrefix(first.Name(), filepath.Join(firstHome, ".beeftv", "runtime")+string(os.PathSeparator)) {
		t.Fatalf("主要锁路径改变：%s", first.Name())
	}
	t.Setenv("USERPROFILE", secondHome)
	if second, err := LockWorkspace(directory); err == nil {
		_ = second.Close()
		t.Fatal("不同系统用户绕过工作区物理锁")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := LockWorkspace(directory)
	if err != nil {
		t.Fatalf("原用户退出后第二用户未能取得锁，或失败分支遗留句柄：%v", err)
	}
	if second.Name() == first.Name() {
		t.Fatal("测试没有使用两个独立用户锁路径")
	}
	if physical, err := desktopstorage.Lock(directory); err == nil {
		_ = physical.Close()
		t.Fatal("工作区未持有物理锁")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("重复关闭锁失败：%v", err)
	}
	physical, err := desktopstorage.Lock(directory)
	if err != nil {
		t.Fatalf("关闭后未释放物理锁：%v", err)
	}
	_ = physical.Close()
}

func TestWindowsWorkspaceLockRequiresPhysicalLockAndReleasesUserLockOnFailure(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERPROFILE", t.TempDir())
	physicalPath := filepath.Join(filepath.Dir(directory), ".workspace.beeftv-lock")
	if err := os.Mkdir(physicalPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if lock, err := LockWorkspace(directory); err == nil {
		_ = lock.Close()
		t.Fatal("无法创建物理锁时退回了单用户锁")
	} else if !strings.Contains(err.Error(), "物理锁") {
		t.Fatalf("没有明确说明物理锁失败：%v", err)
	}
	if err := os.Remove(physicalPath); err != nil {
		t.Fatal(err)
	}
	lock, err := LockWorkspace(directory)
	if err != nil {
		t.Fatalf("物理锁失败后用户锁未释放：%v", err)
	}
	_ = lock.Close()
}
