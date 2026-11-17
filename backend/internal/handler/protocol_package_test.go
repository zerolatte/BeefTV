package handler

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/protocol"
)

// 测试按需构建真实清单，避免依赖工作区是否已经生成发布插件包。
func buildTestProtocolPackages(t *testing.T, ids ...string) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, id := range ids {
		manifest, err := os.ReadFile(filepath.Join(root, "plugin-packages", id, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		var archive bytes.Buffer
		writer := zip.NewWriter(&archive)
		entry, err := writer.Create("manifest.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(manifest); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+protocol.PluginPackageExtension), archive.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
