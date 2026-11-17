//go:build platform

package handler

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
	"infinite-canvas/backend/internal/identity"
)

func TestPlatformOpenAPIUsesAuthenticatedMountedRouteContract(t *testing.T) {
	env := newPlatformAuthTestEnvironment(t)
	public := env.router.Group("/api")
	public.GET("/runtime", func(c *gin.Context) { ok(c, gin.H{"mode": "platform"}) })
	public.GET("/healthz", func(c *gin.Context) { ok(c, gin.H{"status": "ok"}) })
	env.router.GET("/metrics", func(c *gin.Context) { c.Status(http.StatusOK) })
	protected := public.Group("", env.auth.middleware())
	registerPlatformAdminRoutes(protected, env.service)
	registerTaskTextReadRoutes(protected, nil, &taskTextReadFixture{})
	protected.GET("/resources/:id/file", func(c *gin.Context) { c.Data(http.StatusOK, "application/octet-stream", []byte("文件")) })
	if err := RegisterPlatformOpenAPIRoutes(protected, env.router, env.auth.cookieName()); err != nil {
		t.Fatal(err)
	}
	for _, headers := range []map[string]string{nil, {"Authorization": "Bearer local-token"}, {"X-Canvas-Owner-Token": "local-token"}} {
		response := platformAuthRequest(t, env.router, http.MethodGet, "/api/openapi.yaml", nil, nil, headers)
		platformAuthResponse(t, response, http.StatusUnauthorized)
	}
	grant, cookie := loginForPlatformTest(t, env, "manager", testPlatformPassword)
	response := platformAuthRequest(t, env.router, http.MethodGet, "/api/openapi.yaml", nil, []*http.Cookie{cookie}, nil)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/yaml") || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("文档响应错误: HTTP %d，头=%v，正文=%s", response.Code, response.Header(), response.Body.String())
	}
	var document map[string]any
	if err := yaml.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document["openapi"] != "3.0.3" || document["x-beeftv-generation-enabled"] != false {
		t.Fatal("平台版本或生成关闭合同错误", document)
	}
	for index, localOnly := range []string{"/assistant/", "/ai/custom", "/agent-clients", "/plugins", "X-Canvas-Owner-Token", "local-token", cookie.Value} {
		if strings.Contains(response.Body.String(), localOnly) {
			t.Fatalf("平台文档混入本地能力或凭据，第 %d 项", index+1)
		}
	}
	paths := document["paths"].(map[string]any)
	assertPlatformOpenAPIRouteSet(t, env.router.Routes(), paths)
	security := document["security"].([]any)
	if len(security) != 1 || security[0].(map[string]any)["PlatformSession"] == nil {
		t.Fatal("默认操作没有要求平台会话")
	}
	schemes := document["components"].(map[string]any)["securitySchemes"].(map[string]any)
	session := schemes["PlatformSession"].(map[string]any)
	if len(schemes) != 1 || session["in"] != "cookie" || session["name"] != env.auth.cookieName() {
		t.Fatal("认证方案不是实际平台 Cookie", schemes)
	}
	for _, path := range []string{"/runtime", "/healthz", "/auth/csrf", "/auth/login"} {
		method := "get"
		if path == "/auth/login" {
			method = "post"
		}
		operation := paths[path].(map[string]any)[method].(map[string]any)
		if anonymous, ok := operation["security"].([]any); !ok || len(anonymous) != 0 {
			t.Fatalf("匿名入口错误要求会话: %s", path)
		}
	}
	for _, path := range []string{"/openapi.yaml", "/auth/me", "/admin/users"} {
		operation := paths[path].(map[string]any)["get"].(map[string]any)
		if _, exists := operation["security"]; exists {
			t.Fatalf("受保护入口覆盖了默认会话要求: %s", path)
		}
	}
	for _, item := range []struct{ path, method string }{{"/auth/login", "post"}, {"/admin/users/{id}", "patch"}, {"/admin/users/{id}/storage-quota", "put"}, {"/admin/users/{id}", "delete"}} {
		operation := paths[item.path].(map[string]any)[item.method].(map[string]any)
		for _, name := range []string{"Origin", "X-CSRF-Token"} {
			parameter := platformOpenAPITestParameter(t, operation, "header", name)
			if parameter["required"] != true {
				t.Fatalf("修改请求没有强制 %s: %s %s", name, item.method, item.path)
			}
		}
	}
	admin := paths["/admin/users/{id}"].(map[string]any)["patch"].(map[string]any)
	if admin["x-required-role"] != "admin" || platformOpenAPITestParameter(t, admin, "path", "id")["required"] != true {
		t.Fatal("管理员或路径参数约束缺失")
	}
	stream := paths["/tasks/{id}/text-events"].(map[string]any)["get"].(map[string]any)
	platformOpenAPITestParameter(t, stream, "header", "Last-Event-ID")
	platformOpenAPITestParameter(t, stream, "query", "after")
	streamContent := stream["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)
	if streamContent["text/event-stream"] == nil {
		t.Fatal("文本事件错误描述成 JSON")
	}
	file := paths["/resources/{id}/file"].(map[string]any)["get"].(map[string]any)
	platformOpenAPITestParameter(t, file, "header", "Range")
	if file["responses"].(map[string]any)["206"] == nil {
		t.Fatal("文件 Range 合同缺失")
	}
	if err := env.service.Logout(context.Background(), grant["sessionId"].(string)); err != nil {
		t.Fatal(err)
	}
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodGet, "/api/openapi.yaml", nil, []*http.Cookie{cookie}, nil), http.StatusUnauthorized)
}

func assertPlatformOpenAPIRouteSet(t *testing.T, routes gin.RoutesInfo, paths map[string]any) {
	t.Helper()
	parameter := regexp.MustCompile(`[:*]([A-Za-z_][A-Za-z0-9_]*)`)
	expected := map[string]bool{}
	for _, route := range routes {
		if strings.HasPrefix(route.Path, "/api/") {
			path := parameter.ReplaceAllString(strings.TrimPrefix(route.Path, "/api"), "{$1}")
			expected[strings.ToLower(route.Method)+" "+path] = true
		}
	}
	for path, raw := range paths {
		for method := range raw.(map[string]any) {
			key := method + " " + path
			if !expected[key] {
				t.Fatalf("文档宣称未注册的接口: %s", key)
			}
			delete(expected, key)
		}
	}
	if len(expected) > 0 {
		t.Fatal("文档遗漏实际接口", expected)
	}
}

func platformOpenAPITestParameter(t *testing.T, operation map[string]any, location, name string) map[string]any {
	t.Helper()
	parameters, _ := operation["parameters"].([]any)
	for _, raw := range parameters {
		parameter := raw.(map[string]any)
		if parameter["in"] == location && parameter["name"] == name {
			return parameter
		}
	}
	t.Fatalf("缺少参数 %s %s", location, name)
	return nil
}

func TestPlatformOpenAPIDeniesUnprotectedOrLocalIdentity(t *testing.T) {
	router := gin.New()
	if err := RegisterPlatformOpenAPIRoutes(router.Group("/api"), router, "beeftv-dev-session"); err != nil {
		t.Fatal(err)
	}
	response := platformAuthRequest(t, router, http.MethodGet, "/api/openapi.yaml", nil, nil, nil)
	platformAuthResponse(t, response, http.StatusUnauthorized)
	localRouter := gin.New()
	local := localRouter.Group("/api", func(c *gin.Context) {
		if err := SetPrincipal(c, identity.Principal{Mode: identity.ModeLocal, WorkspaceID: "local", Role: identity.RoleAdmin, Status: identity.StatusActive}); err != nil {
			t.Fatal(err)
		}
	})
	if err := RegisterPlatformOpenAPIRoutes(local, localRouter, "beeftv-dev-session"); err != nil {
		t.Fatal(err)
	}
	response = platformAuthRequest(t, localRouter, http.MethodGet, "/api/openapi.yaml", nil, nil, nil)
	platformAuthResponse(t, response, http.StatusUnauthorized)
	if err := RegisterPlatformOpenAPIRoutes(nil, router, "session"); err == nil {
		t.Fatal("接受了缺失的受保护路由组")
	}
	if err := RegisterPlatformOpenAPIRoutes(router.Group("/other"), router, "session"); err == nil {
		t.Fatal("接受了平台 API 以外的路由组")
	}
}
