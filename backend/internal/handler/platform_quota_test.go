//go:build platform

package handler

import (
	"context"
	"net/http"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/platformquota"
)

func TestPlatformQuotaHTTPAuthorizationReplayAndBounds(t *testing.T) {
	env := newPlatformAuthTestEnvironment(t)
	protected := env.router.Group("/api")
	protected.Use(env.auth.middleware())
	registerPlatformQuotaRoutes(protected, env.service, platformquota.New(env.db))
	points, bytes := int64(100), int64(1024)
	user, err := env.service.CreateAccount(context.Background(), env.admin.ID, platformauth.CreateAccountInput{Username: "creator", DisplayName: "创作者", Password: testPlatformPassword, Role: platformauth.RoleUser, InitialPoints: &points, StorageLimitBytes: &bytes})
	if err != nil {
		t.Fatal(err)
	}
	grant, cookie := loginForPlatformTest(t, env, "manager", testPlatformPassword)
	headers := map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": grant["csrfToken"].(string)}
	cookies := []*http.Cookie{cookie}
	path := "/api/admin/users/" + user.ID + "/point-adjustments"
	input := map[string]any{"clientOperationId": "http-adjustment", "points": 25, "reason": "测试追加额度"}
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, path, input, nil, nil), 401)
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, path, input, cookies, nil), 403)
	first := platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, path, input, cookies, headers), 200)
	again := platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, path, input, cookies, headers), 200)
	firstID := first["data"].(map[string]any)["adjustment"].(map[string]any)["id"]
	againID := again["data"].(map[string]any)["adjustment"].(map[string]any)["id"]
	if firstID != againID {
		t.Fatal("HTTP 重试重复发放")
	}
	input["points"] = 26
	response := platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, path, input, cookies, headers), 409)
	if response["reason"] != "idempotency_conflict" {
		t.Fatal(response)
	}
	input["clientOperationId"] = "http-reduction"
	input["points"] = -126
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, path, input, cookies, headers), 409)
	input["unexpected"] = "not allowed"
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, path, input, cookies, headers), 400)
	var saved platformauth.QuotaAccount
	env.db.First(&saved, "account_id = ?", user.ID)
	if saved.GrantedPoints != 125 {
		t.Fatal("余额错误")
	}
	intent := model.PlatformExecutionIntent{ID: "usage-intent", AccountID: user.ID, WorkspaceID: user.WorkspaceID, TaskID: "task-id", LogicalModelID: "model-id", RevisionID: "private-revision", RouteID: "private-route", ChannelID: "private-channel", ChannelModelID: "private-channel-model", CredentialVersionID: "private-credential", PriceVersionID: "price-id", Operation: "text_generation", Points: 10, State: "reserved"}
	if err := env.db.Create(&intent).Error; err != nil {
		t.Fatal(err)
	}
	usage := platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodGet, "/api/admin/usage?accountId="+user.ID, nil, cookies, headers), 200)["data"].(map[string]any)
	items := usage["items"].([]any)
	if len(items) != 1 {
		t.Fatal("用量未按账号读取")
	}
	for _, field := range []string{"workspaceId", "revisionId", "routeId", "channelId", "channelModelId", "credentialVersionId", "prompt", "inputJson"} {
		if _, exists := items[0].(map[string]any)[field]; exists {
			t.Fatalf("用量泄露字段 %s", field)
		}
	}
	// 管理中间件与领域层都拒绝普通账号，即使对方知道管理员接口。
	if err := env.db.Model(&platformauth.Account{}).Where("id = ?", user.ID).Update("must_change_password", false).Error; err != nil {
		t.Fatal(err)
	}
	userGrant, userCookie := loginForPlatformTest(t, env, "creator", testPlatformPassword)
	userHeaders := map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": userGrant["csrfToken"].(string)}
	delete(input, "unexpected")
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, path, input, []*http.Cookie{userCookie}, userHeaders), 403)
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodGet, "/api/admin/usage", nil, []*http.Cookie{userCookie}, userHeaders), 403)
}
