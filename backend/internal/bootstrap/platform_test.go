package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/repository"
)

type platformClient struct {
	userID  string
	runtime *Runtime
	cookies []*http.Cookie
	csrf    string
}

func (client *platformClient) call(t *testing.T, method, path string, body any, want int) map[string]any {
	t.Helper()
	encoded, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "http://localhost:3000/api"+path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("X-CSRF-Token", client.csrf)
	req.Header.Set("X-Platform-User", client.userID)
	for _, cookie := range client.cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	client.runtime.Handler().ServeHTTP(recorder, req)
	if recorder.Code != want {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, recorder.Code, want, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "beeftv_csrf" {
			client.csrf = cookie.Value
		}
		found := false
		for i, old := range client.cookies {
			if old.Name == cookie.Name {
				client.cookies[i] = cookie
				found = true
			}
		}
		if !found {
			client.cookies = append(client.cookies, cookie)
		}
	}
	var result map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if path == "/auth/login" && want == 200 {
		client.userID = data(result)["user"].(map[string]any)["id"].(string)
	}
	return result
}
func data(result map[string]any) map[string]any { return result["data"].(map[string]any) }

func TestPlatformAccountsIsolationAndCredits(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_ORIGIN", "http://localhost:3000")
	runtime, err := Open(context.Background(), Config{Profile: ProfileServer, DataDir: t.TempDir(), AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	repo := repository.New(runtime.db)
	auth := identity.New(repo)
	admin, err := auth.InitializeAdmin("test-admin", "example-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	anonymous := &platformClient{runtime: runtime}
	anonymous.call(t, "GET", "/workspace/bootstrap", nil, 401)
	anonymous.call(t, "GET", "/resources/foreign/file", nil, 401)
	manager := &platformClient{runtime: runtime}
	manager.call(t, "POST", "/auth/login", map[string]any{"username": "test-admin", "password": "example-admin-password"}, 200)
	created := data(manager.call(t, "POST", "/admin/accounts", map[string]any{"username": "creator-one", "displayName": "测试创作者"}, 200))
	userID := created["user"].(map[string]any)["id"].(string)
	temporary := created["temporaryPassword"].(string)
	user := &platformClient{runtime: runtime}
	user.call(t, "POST", "/auth/login", map[string]any{"username": "creator-one", "password": temporary}, 200)
	user.call(t, "GET", "/workspace/bootstrap", nil, 403)
	user.call(t, "POST", "/auth/password", map[string]any{"oldPassword": temporary, "newPassword": "example-creator-password"}, 200)
	user.call(t, "GET", "/auth/session", nil, 401)
	user.call(t, "POST", "/auth/login", map[string]any{"username": "CREATOR-ONE", "password": "example-creator-password"}, 200)
	user.call(t, "GET", "/workspace/bootstrap", nil, 200)
	user.call(t, "GET", "/admin/accounts", nil, 403)
	user.userID = admin.ID
	user.call(t, "POST", "/tasks", map[string]any{}, 401)
	user.userID = userID
	csrf := user.csrf
	user.csrf = "invalid"
	user.call(t, "POST", "/tasks", map[string]any{}, 403)
	user.csrf = csrf
	for _, path := range []string{"/ai/custom", "/assistant/sessions", "/ops/clients", "/workspace/model-config"} {
		user.call(t, "PUT", path, map[string]any{}, 404)
	}
	user.call(t, "POST", "/tasks/missing/retry", map[string]any{}, 403)
	manager.call(t, "POST", "/admin/accounts/"+userID+"/credits", map[string]any{"amount": 20, "operationId": "grant-test-credit", "reason": "测试发放"}, 200)
	manager.call(t, "POST", "/admin/accounts/"+userID+"/credits", map[string]any{"amount": 20, "operationId": "grant-test-credit", "reason": "测试发放"}, 200)
	account := data(user.call(t, "GET", "/credits", nil, 200))
	if account["available"] != float64(20) {
		t.Fatal("重复发放积分")
	}
	enabled := true
	channel, err := runtime.service.CreateSystemChannel(&admin.User, modelcatalog.ChannelRequest{Name: "测试模型服务", BaseURL: "https://example.com", APIKey: "unit-test-secret", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	m, err := runtime.service.SaveAdminChannelModel(&admin.User, channel.ID, "", modelcatalog.ChannelModelRequest{ModelKey: "test-text", DisplayName: "测试文本", Capability: "text", Protocol: string(model.ChannelInterfaceChatCompletion), Enabled: &enabled, CapabilityConfig: modelcatalog.DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "test-text")})
	if err != nil {
		t.Fatal(err)
	}
	manager.call(t, "PUT", "/admin/prices", map[string]any{"modelId": m.ID, "variantId": m.Variants[0].ID, "unitCredits": 4, "published": true}, 200)
	catalog := user.call(t, "GET", "/workspace/model-config", nil, 200)
	encoded, _ := json.Marshal(catalog)
	if strings.Contains(string(encoded), "example.com") || strings.Contains(string(encoded), "unit-test-secret") {
		t.Fatal("用户模型目录泄露渠道配置")
	}
	request := map[string]any{"type": "canvas_text", "operation": "text", "prompt": "测试", "model": "test-text", "input": map[string]any{"mode": "text", "prompt": "测试", "config": map[string]any{"channelId": channel.ID, "model": "test-text"}, "metadata": map[string]any{"clientOperationId": "operation-test-001"}}}
	quote := data(user.call(t, "POST", "/tasks/quote", request, 200))
	request["quoteId"] = quote["id"]
	first := data(user.call(t, "POST", "/tasks", request, 200))
	second := data(user.call(t, "POST", "/tasks", request, 200))
	if first["id"] != second["id"] {
		t.Fatal("重复提交创建了新任务")
	}
	account = data(user.call(t, "GET", "/credits", nil, 200))
	if account["available"] != float64(16) || account["frozen"] != float64(4) {
		t.Fatalf("冻结余额错误: %v", account)
	}
	manager.call(t, "GET", "/tasks/"+first["id"].(string), nil, 404)
	manager.call(t, "PATCH", "/admin/accounts/"+userID, map[string]any{"displayName": "测试创作者", "status": "disabled"}, 200)
	user.call(t, "GET", "/credits", nil, 401)
	if err := runtime.platformCredits.Reconcile(); err != nil {
		t.Fatal(err)
	}
	balance, err := repo.CreditAccount(userID)
	if err != nil {
		t.Fatal(err)
	}
	if balance.Available != 20 || balance.Frozen != 0 {
		t.Fatalf("未执行任务停用退款错误: %+v", balance)
	}
}
