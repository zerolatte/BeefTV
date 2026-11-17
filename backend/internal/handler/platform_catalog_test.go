//go:build platform

package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/repository"
)

func newPlatformCatalogHTTP(t *testing.T) *platformAuthTestEnvironment {
	t.Helper()
	env := newPlatformAuthTestEnvironment(t)
	directory := t.TempDir()
	repo := repository.New(env.db)
	catalog, err := admin.NewPlatformCatalog(repo, admin.PlatformCatalogDependencies{EncryptSecret: func(value string) (string, error) { return localcrypto.Encrypt(directory, value) }, DecryptSecret: func(value string) (string, error) { return localcrypto.Decrypt(directory, value) }})
	if err != nil {
		t.Fatal(err)
	}
	protected := env.router.Group("/api")
	protected.Use(env.auth.middleware())
	registerPlatformAdminRoutes(protected, env.service)
	registerPlatformCatalogAdminRoutes(protected, env.service, catalog)
	reader := modelcatalog.NewPlatformCatalogReader(repo)
	protected.GET("/models/catalog", func(c *gin.Context) {
		principal, _ := CurrentPrincipal(c)
		catalog, err := reader.Read(c.Request.Context(), principal.AccountID, principal.WorkspaceID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, catalog)
	})
	return env
}

func TestPlatformCatalogHTTPManagementAndAuthorization(t *testing.T) {
	env := newPlatformCatalogHTTP(t)
	grant, cookie := loginForPlatformTest(t, env, "manager", testPlatformPassword)
	headers := map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": grant["csrfToken"].(string)}
	cookies := []*http.Cookie{cookie}
	request := func(method, path string, body any, status int) map[string]any {
		response := platformAuthRequest(t, env.router, method, path, body, cookies, headers)
		for _, secret := range []string{"first-test-key", "second-test-key", "secret-header-value"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatal("管理 HTTP 泄露密钥")
			}
		}
		return platformAuthResponse(t, response, status)["data"].(map[string]any)
	}
	channelInput := map[string]any{"name": "模拟上游", "credentials": map[string]any{"baseUrl": "https://127.0.0.1:9443/v1", "apiKey": "first-test-key", "headers": []any{map[string]any{"name": "X-Test", "value": "secret-header-value"}}}}
	protocols := request(http.MethodGet, "/api/admin/model-protocols", nil, 200)["protocols"].([]any)
	if len(protocols) == 0 {
		t.Fatal("协议目录为空")
	}
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodGet, "/api/admin/channels", nil, nil, nil), 401)
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, "/api/admin/channels", channelInput, cookies, nil), 403)
	channel := request(http.MethodPost, "/api/admin/channels", channelInput, 200)["channel"].(map[string]any)
	channelID := channel["id"].(string)
	if channel["credentialVersion"] != float64(1) || channel["hasApiKey"] != true {
		t.Fatal("凭据摘要合同错误")
	}
	unknown := platformAuthRequest(t, env.router, http.MethodPatch, "/api/admin/channels/"+channelID, map[string]any{"apiKey": "forbidden"}, cookies, headers)
	platformAuthResponse(t, unknown, 400)
	rotated := request(http.MethodPost, "/api/admin/channels/"+channelID+"/credentials", map[string]any{"baseUrl": "https://127.0.0.1:9443/v2", "apiKey": "second-test-key"}, 200)["channel"].(map[string]any)
	if rotated["credentialVersion"] != float64(2) {
		t.Fatal("轮换没有产生新版本")
	}
	upstream := request(http.MethodPost, "/api/admin/channels/"+channelID+"/models", modelcatalog.ChannelModelRequest{ModelKey: "mock-text", DisplayName: "模拟文本", Capability: "text", Protocol: "chat-completion", CapabilityConfig: modelcatalog.DefaultModelCapabilityConfig("chat-completion")}, 200)["model"].(map[string]any)
	logical := request(http.MethodPost, "/api/admin/models", map[string]any{"code": "mock-platform-model", "name": "平台文本", "enabled": true, "capabilitySpec": map[string]any{"version": 1, "capability": "text", "operations": []string{"text_generation"}}, "defaultOptions": map[string]any{}, "routes": []any{map[string]any{"channelModelId": upstream["id"], "enabled": true, "weight": 1}}}, 200)
	modelID := logical["model"].(map[string]any)["id"].(string)
	request(http.MethodPost, "/api/admin/models/"+modelID+"/prices", map[string]any{"operation": "text_generation", "points": 9}, 200)
	points, storage := int64(0), int64(1000)
	user, err := env.service.CreateAccount(context.Background(), env.admin.ID, platformauth.CreateAccountInput{Username: "creator", DisplayName: "创作者", Role: platformauth.RoleUser, Password: testPlatformPassword, InitialPoints: &points, StorageLimitBytes: &storage})
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPut, "/api/admin/users/"+user.ID+"/model-grants", map[string]any{"logicalModelIds": []string{modelID}}, 200)
	initialGrant, err := env.service.Login(context.Background(), "creator", testPlatformPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.service.ChangePassword(context.Background(), initialGrant.SessionID, testPlatformPassword, testPlatformNewPassword); err != nil {
		t.Fatal(err)
	}
	userGrant, userCookie := loginForPlatformTest(t, env, "creator", testPlatformNewPassword)
	userHeaders := map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": userGrant["csrfToken"].(string)}
	for _, path := range []string{"/api/admin/model-protocols", "/api/admin/channels", "/api/admin/models", "/api/admin/users/" + user.ID + "/model-grants", "/api/admin/models/" + modelID + "/prices"} {
		platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodGet, path, nil, []*http.Cookie{userCookie}, userHeaders), 403)
	}
	response := platformAuthRequest(t, env.router, http.MethodGet, "/api/models/catalog", nil, []*http.Cookie{userCookie}, nil)
	data := platformAuthResponse(t, response, 200)["data"].(map[string]any)
	if len(data["models"].([]any)) != 1 || len(data["channels"].([]any)) != 0 {
		t.Fatal("授权目录合同错误")
	}
	if strings.Contains(response.Body.String(), "127.0.0.1") || strings.Contains(response.Body.String(), "Key") || strings.Contains(response.Body.String(), channelID) {
		t.Fatal("用户目录泄露渠道配置")
	}
	request(http.MethodPut, "/api/admin/users/"+user.ID+"/model-grants", map[string]any{"logicalModelIds": []string{}}, 200)
	data = platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodGet, "/api/models/catalog", nil, []*http.Cookie{userCookie}, nil), 200)["data"].(map[string]any)
	if len(data["models"].([]any)) != 0 {
		t.Fatal("撤权后目录没有及时清空")
	}
}
