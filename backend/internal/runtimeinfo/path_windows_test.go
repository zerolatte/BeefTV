package runtimeinfo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsCanonicalDescriptorIgnoresAppDataShadows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	dataDir := filepath.Join(home, "AppData", "Roaming", "BeefTV")
	shadowDir := filepath.Join(home, "AppData", "Local", "Packages", "fixture", "LocalCache", "Roaming", "BeefTV")
	t.Setenv("APPDATA", filepath.Dir(shadowDir))
	t.Setenv("BEEFTV_DATA_DIR", dataDir)
	for _, dir := range []string{dataDir, shadowDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, pid := range []int{os.Getpid(), deadPID(t)} {
		legacy, err := json.Marshal(Info{BaseURL: "http://127.0.0.1:59999/api", PID: pid, Version: "old-shadow"})
		if err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{dataDir, shadowDir} {
			if err := os.WriteFile(filepath.Join(dir, FileName), legacy, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := Write(dataDir, "http://127.0.0.1:53211/api", "canonical"); err != nil {
			t.Fatal(err)
		}
		path := testPath(t, dataDir)
		if !strings.HasPrefix(path, filepath.Join(home, ".beeftv", "runtime")+string(os.PathSeparator)) {
			t.Fatalf("descriptor remained inside AppData: %s", path)
		}
		info, found := Discover("")
		if !found || info.Version != "canonical" || info.BaseURL != "http://127.0.0.1:53211/api" {
			t.Fatalf("shadow PID %d displaced canonical runtime: %+v, found=%v", pid, info, found)
		}
		// Restart replaces the one canonical file, without changing discovery source.
		if err := Write(dataDir, "http://127.0.0.1:53212/api", "restarted"); err != nil {
			t.Fatal(err)
		}
		info, found = Discover("")
		if !found || info.Version != "restarted" {
			t.Fatalf("canonical replacement not discovered: %+v", info)
		}
		if err := Remove(dataDir); err != nil {
			t.Fatal(err)
		}
		if _, found := Discover(""); found {
			t.Fatalf("missing canonical descriptor fell back to legacy/shadow PID %d", pid)
		}
		for _, dir := range []string{dataDir, shadowDir} {
			if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
				t.Fatalf("legacy shadow was modified: %v", err)
			}
		}
	}
}

func TestWindowsCanonicalWorkspaceIdentity(t *testing.T) {
	dataDir, otherDir := t.TempDir(), t.TempDir()
	path := testPath(t, dataDir)
	for _, equivalent := range []string{strings.ToUpper(dataDir), filepath.Join(dataDir, "child", ".."), strings.ReplaceAll(dataDir, `\`, "/")} {
		got, err := Path(equivalent)
		if err != nil || got != path {
			t.Fatalf("Windows normalization differs for %q: %q, %v", equivalent, got, err)
		}
	}
	if path == testPath(t, otherDir) {
		t.Fatal("distinct workspaces share a descriptor")
	}
	if err := Write(dataDir, "http://127.0.0.1:53211/api", "first"); err != nil {
		t.Fatal(err)
	}
	if err := Write(otherDir, "http://127.0.0.1:53212/api", "second"); err != nil {
		t.Fatal(err)
	}
	info, found := Discover(otherDir)
	if !found || info.Version != "second" {
		t.Fatalf("workspace isolation failed: %+v", info)
	}
	wrong, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	otherPath := testPath(t, otherDir)
	if err := os.WriteFile(otherPath, wrong, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found := Discover(otherDir); found {
		t.Fatal("descriptor from another workspace accepted")
	}
	if err := Remove(otherDir); err == nil {
		t.Fatal("wrong-workspace descriptor must not be removed even with own PID")
	}
	if _, err := os.Stat(otherPath); err != nil {
		t.Fatal("wrong-workspace descriptor was removed")
	}
}

func TestWindowsCanonicalPathFailsClosedWithoutAbsoluteHome(t *testing.T) {
	for _, home := range []string{"", "relative-home"} {
		t.Setenv("USERPROFILE", home)
		dataDir := t.TempDir()
		if path, err := Path(dataDir); err == nil || path != "" {
			t.Fatalf("invalid home %q produced path %q, %v", home, path, err)
		}
		if err := Write(dataDir, "http://127.0.0.1:53211/api", "fixture"); err == nil {
			t.Fatal("Write must fail with no canonical root")
		}
		if _, err := Load(dataDir); err == nil {
			t.Fatal("Load must fail with no canonical root")
		}
		if _, found := Discover(dataDir); found {
			t.Fatal("Discover must fail with no canonical root")
		}
		if err := Remove(dataDir); err == nil {
			t.Fatal("Remove must fail with no canonical root")
		}
	}
}

func TestWindowsWorkspaceAliasesShareLockAndDiscovery(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "long-workspace-directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := windows.UTF16PtrFromString(canonical)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	n, err := windows.GetShortPathName(wide, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || int(n) >= len(buffer) {
		t.Fatalf("读取目录短路径失败：%v", err)
	}
	short := windows.UTF16ToString(buffer[:n])
	lock, err := LockWorkspace(short)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	for _, alias := range []string{canonical, directory, strings.ToUpper(canonical)} {
		if duplicate, err := LockWorkspace(alias); err == nil {
			_ = duplicate.Close()
			t.Fatalf("目录别名绕过已有实例锁：%s", alias)
		}
	}
	if err := Write(short, "http://127.0.0.1:53211/api", "alias-fixture"); err != nil {
		t.Fatal(err)
	}
	defer Remove(short)
	if info, found := Discover(canonical); !found || info.Version != "alias-fixture" {
		t.Fatalf("长路径无法发现短路径写入的运行时：%+v", info)
	}
	if testPath(t, filepath.Join(short, "future")) != testPath(t, filepath.Join(canonical, "future")) {
		t.Fatal("尚未创建的子目录通过父目录别名产生不同身份")
	}
}
