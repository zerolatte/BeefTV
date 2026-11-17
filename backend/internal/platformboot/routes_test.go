//go:build platform

package platformboot

import (
	"io"
	"net/http"
	"testing"

	"github.com/goccy/go-yaml"
	cw "infinite-canvas/backend/internal/canvasworkflow"
)

func TestPlatformBootPublishesOnlyMountedContract(t *testing.T) {
	fixture := newPlatformFixture(t)
	fixture.browser(t).request(t, "GET", "/openapi.yaml", nil, 401, nil)
	browser := fixture.login(t, fixture.admin.Username, false)
	response, err := browser.client.Get(fixture.server.URL + "/api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("平台接口规范不可读或允许持久缓存：%d", response.StatusCode)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ path, method string }{
		{"/openapi.yaml", "get"},
		{"/settings/prompt-templates", "get"},
		{"/canvas-workflows", "post"},
		{"/canvas-workflows/{id}/runs", "post"},
		{"/canvas-workflow-runs/{id}/retry", "post"},
		{"/tasks/{id}/text-events", "get"},
	} {
		if document.Paths[entry.path][entry.method] == nil {
			t.Fatalf("启动后规范缺少已装配合同：%s %s", entry.method, entry.path)
		}
	}
	for _, path := range []string{"/ai/custom", "/workspace/model-config", "/diagnostics/environment", "/assistant/chat"} {
		if document.Paths[path] != nil {
			t.Fatalf("平台规范暴露关闭的本地扩展入口：%s", path)
		}
	}
	operations := decodePlatformData[struct {
		Operations []cw.Operation `json:"operations"`
	}](t, browser.request(t, "GET", "/canvas-workflow-operations", nil, 200, nil))
	if len(operations.Operations) != 1 || operations.Operations[0].ID != "generate.text" || operations.Operations[0].UnavailableReason == "" {
		t.Fatalf("平台操作目录没有限制文本范围与关闭状态：%+v", operations.Operations)
	}
	runs := decodePlatformData[struct {
		Workflows []cw.Template `json:"workflows"`
	}](t, browser.request(t, "GET", "/canvas-workflows", nil, 200, nil))
	if len(runs.Workflows) != 0 {
		t.Fatal("新平台账号出现非本人工作流")
	}
	features := decodePlatformData[struct {
		Generation bool `json:"generation"`
	}](t, browser.request(t, "GET", "/features", nil, 200, nil))
	if features.Generation {
		t.Fatal("生产组合根意外开放生成")
	}
}
