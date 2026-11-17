//go:build platform

package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"infinite-canvas/backend/internal/platformauth"
)

func preparePlatformAdminHTTP(t *testing.T) (*platformAuthTestEnvironment, *http.Cookie, map[string]string) {
	t.Helper()
	env := newPlatformAuthTestEnvironment(t)
	protected := env.router.Group("/api")
	protected.Use(env.auth.middleware())
	registerPlatformAdminRoutes(protected, env.service)
	grant, cookie := loginForPlatformTest(t, env, "manager", testPlatformPassword)
	return env, cookie, map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": grant["csrfToken"].(string)}
}

func platformUserInput(username string) map[string]any {
	return map[string]any{"username": username, "displayName": "测试用户", "password": testPlatformPassword, "role": "user", "initialPoints": 0, "storageLimitBytes": 1024}
}

func TestPlatformAdminHTTPUserLifecycleAndAudit(t *testing.T) {
	env, cookie, headers := preparePlatformAdminHTTP(t)
	cookies := []*http.Cookie{cookie}
	request := func(method, path string, body any, status int) map[string]any {
		response := platformAuthRequest(t, env.router, method, path, body, cookies, headers)
		if strings.Contains(response.Body.String(), testPlatformPassword) || strings.Contains(response.Body.String(), "passwordHash") || strings.Contains(response.Body.String(), "authVersion") {
			t.Fatal("管理响应泄露凭据或内部认证字段")
		}
		return platformAuthResponse(t, response, status)
	}
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodGet, "/api/admin/users", nil, nil, nil), 401)
	platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, "/api/admin/users", platformUserInput("alice"), cookies, nil), 403)
	created := request(http.MethodPost, "/api/admin/users", platformUserInput(" Alice "), 200)["data"].(map[string]any)["user"].(map[string]any)
	userID := created["id"].(string)
	if created["username"] != "alice" || created["workspaceId"] == userID || created["mustChangePassword"] != true {
		t.Fatal("开户身份或强制改密合同错误")
	}
	list := request(http.MethodGet, "/api/admin/users?page=1&pageSize=1", nil, 200)["data"].(map[string]any)
	if list["total"] != float64(2) || len(list["users"].([]any)) != 1 {
		t.Fatal("管理分页错误")
	}
	request(http.MethodGet, "/api/admin/users?pageSize=101", nil, 400)
	emptyPage := request(http.MethodGet, "/api/admin/users?page=100&pageSize=20", nil, 200)["data"].(map[string]any)
	if users, valid := emptyPage["users"].([]any); !valid || len(users) != 0 {
		t.Fatal("空分页必须返回数组")
	}
	request(http.MethodPatch, "/api/admin/users/"+userID, map[string]any{"displayName": "新显示名"}, 200)
	quota := request(http.MethodGet, "/api/admin/users/"+userID+"/quota", nil, 200)["data"].(map[string]any)
	if quota["points"].(map[string]any)["available"] != float64(0) {
		t.Fatal("显式零初始点数丢失")
	}
	quota = request(http.MethodPut, "/api/admin/users/"+userID+"/storage-quota", map[string]any{"limitBytes": 0}, 200)["data"].(map[string]any)
	if quota["storage"].(map[string]any)["limitBytes"] != float64(0) {
		t.Fatal("存储上限未更新")
	}
	grant, err := env.service.Login(context.Background(), "alice", testPlatformPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "/api/admin/users/"+userID+"/reset-password", map[string]any{"password": testPlatformNewPassword}, 200)
	if _, err := env.service.Authenticate(context.Background(), grant.Token); err == nil {
		t.Fatal("密码重置未撤销旧会话")
	}
	resetGrant, err := env.service.Login(context.Background(), "alice", testPlatformNewPassword, "127.0.0.1")
	if err != nil || !resetGrant.Account.MustChangePassword {
		t.Fatal("重置密码未要求改密", err)
	}
	request(http.MethodPatch, "/api/admin/users/"+userID, map[string]any{"status": "disabled"}, 200)
	if _, err := env.service.Authenticate(context.Background(), resetGrant.Token); err == nil {
		t.Fatal("禁用未撤销旧会话")
	}
	request(http.MethodDelete, "/api/admin/users/"+userID, nil, 200)
	request(http.MethodPost, "/api/admin/users", platformUserInput("alice"), 409)
	request(http.MethodPatch, "/api/admin/users/"+userID, map[string]any{"status": "active"}, 404)
	request(http.MethodGet, "/api/admin/users/"+userID+"/quota", nil, 200)
	request(http.MethodPut, "/api/admin/users/"+userID+"/storage-quota", map[string]any{"limitBytes": 1}, 404)
	logs := request(http.MethodGet, "/api/admin/audit-logs?accountId="+userID+"&pageSize=100", nil, 200)["data"].(map[string]any)
	if logs["total"].(float64) < 6 {
		t.Fatal("用户生命周期缺少追加审计")
	}
	for _, raw := range logs["logs"].([]any) {
		if raw.(map[string]any)["targetAccountId"] != userID {
			t.Fatal("账号审计筛选串入其他用户")
		}
	}
	request(http.MethodDelete, "/api/admin/users/"+env.admin.ID, nil, 403)
	request(http.MethodPatch, "/api/admin/users/"+env.admin.ID, map[string]any{"role": "user"}, 409)
}

func TestPlatformAdminHTTPRejectsUnknownAndMissingFields(t *testing.T) {
	env, cookie, headers := preparePlatformAdminHTTP(t)
	cookies := []*http.Cookie{cookie}
	for index, invalid := range []map[string]any{
		{"username": "missing", "displayName": "测试", "password": testPlatformPassword, "role": "user", "storageLimitBytes": 1},
		{"username": "missing", "displayName": "测试", "password": testPlatformPassword, "role": "user", "initialPoints": 0},
		{"username": "unknown", "displayName": "测试", "password": testPlatformPassword, "role": "user", "initialPoints": 0, "storageLimitBytes": 1, "workspaceId": "forged"},
		{"username": "fraction", "displayName": "测试", "password": testPlatformPassword, "role": "user", "initialPoints": 1.5, "storageLimitBytes": 1},
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPost, "/api/admin/users", invalid, cookies, headers), 400)
		})
	}
	for _, body := range []any{nil, map[string]any{}, map[string]any{"username": "renamed"}, map[string]any{"initialPoints": 50}, map[string]any{"status": "deleted"}} {
		platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPatch, "/api/admin/users/"+env.admin.ID, body, cookies, headers), 400)
	}
	for _, body := range []any{map[string]any{}, map[string]any{"limitBytes": nil}, map[string]any{"limitBytes": -1}, map[string]any{"limitBytes": 1.5}, map[string]any{"limitBytes": 1, "usedBytes": 0}} {
		platformAuthResponse(t, platformAuthRequest(t, env.router, http.MethodPut, "/api/admin/users/"+env.admin.ID+"/storage-quota", body, cookies, headers), 400)
	}
	response := platformAuthRequest(t, env.router, http.MethodGet, "/api/admin/users", nil, cookies, headers)
	if platformAuthResponse(t, response, 200)["data"].(map[string]any)["total"] != float64(1) {
		t.Fatal("无效开户产生了半成品账号")
	}
}

func TestPlatformAdminHTTPRejectsOrdinaryUsers(t *testing.T) {
	env, _, _ := preparePlatformAdminHTTP(t)
	points, storage := int64(0), int64(1024)
	user, err := env.service.CreateAccount(context.Background(), env.admin.ID, platformauth.CreateAccountInput{Username: "ordinary", DisplayName: "普通用户", Password: testPlatformPassword, Role: "user", InitialPoints: &points, StorageLimitBytes: &storage})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := env.service.Login(context.Background(), user.Username, testPlatformPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	grant, err = env.service.ChangePassword(context.Background(), grant.SessionID, testPlatformPassword, testPlatformNewPassword)
	if err != nil {
		t.Fatal(err)
	}
	cookies := []*http.Cookie{{Name: env.auth.cookieName(), Value: grant.Token}}
	headers := map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": grant.CSRFToken}
	for _, route := range [][2]string{{"GET", "/users"}, {"POST", "/users"}, {"PATCH", "/users/" + env.admin.ID}, {"POST", "/users/" + env.admin.ID + "/reset-password"}, {"DELETE", "/users/" + env.admin.ID}, {"GET", "/users/" + env.admin.ID + "/quota"}, {"PUT", "/users/" + user.ID + "/storage-quota"}, {"GET", "/audit-logs"}} {
		platformAuthResponse(t, platformAuthRequest(t, env.router, route[0], "/api/admin"+route[1], map[string]any{}, cookies, headers), 403)
	}
}

func TestPlatformAdminHTTPConcurrentUsernameConflict(t *testing.T) {
	env, cookie, headers := preparePlatformAdminHTTP(t)
	var wait sync.WaitGroup
	statuses := make(chan int, 2)
	for _, name := range []string{"Concurrent", " concurrent "} {
		wait.Add(1)
		go func(username string) {
			defer wait.Done()
			statuses <- platformAuthRequest(t, env.router, http.MethodPost, "/api/admin/users", platformUserInput(username), []*http.Cookie{cookie}, headers).Code
		}(name)
	}
	wait.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("并发同名开户结果错误: %v", counts)
	}
}
