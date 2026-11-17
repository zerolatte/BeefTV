//go:build platform

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/platformauth"
)

const testPlatformOrigin = "https://platform.test"
const testPlatformPassword = "platform-password-123!"
const testPlatformNewPassword = "changed-platform-password-456!"

func TestPlatformOriginAndDevelopmentCookieConfiguration(t *testing.T) {
	for _, origin := range []string{"http://platform.test", "http://localhost", "https://platform.test/path", "https://user:password@platform.test", "https://platform.test?key=value", "https://platform.test#fragment"} {
		if _, err := newPlatformAuthentication(nil, origin, false); err == nil {
			t.Fatalf("接受了不安全来源配置: %s", origin)
		}
	}
	for _, origin := range []string{"http://localhost", "http://127.0.0.1:8080", "http://[::1]:8080"} {
		auth, err := newPlatformAuthentication(nil, origin, true)
		if err != nil {
			t.Fatal(err)
		}
		if auth.secure || strings.HasPrefix(auth.cookieName(), "__Host-") {
			t.Fatal("HTTP开发模式使用了无法设置的Secure Cookie")
		}
	}
	if _, err := newPlatformAuthentication(nil, "http://192.168.1.1", true); err == nil {
		t.Fatal("开发例外不能放宽到局域网HTTP")
	}
}

type platformAuthTestEnvironment struct {
	db      *gorm.DB
	service *platformauth.Service
	admin   *platformauth.Account
	auth    *platformAuthentication
	router  *gin.Engine
}

func newPlatformAuthTestEnvironment(t *testing.T) *platformAuthTestEnvironment {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared&_foreign_keys=on"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ConfigurePool(db); err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := database.MigrateLocalSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := platformauth.Migrate(db); err != nil {
		t.Fatal(err)
	}
	service := platformauth.NewService(db)
	points, storage := int64(0), int64(1024)
	admin, err := service.Bootstrap(context.Background(), platformauth.CreateAccountInput{Username: "manager", DisplayName: "管理员", Password: testPlatformPassword, InitialPoints: &points, StorageLimitBytes: &storage})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := newPlatformAuthentication(service, testPlatformOrigin, false)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	public := router.Group("/api")
	protected := public.Group("")
	protected.Use(auth.middleware())
	auth.register(public, protected)
	protected.GET("/private", func(c *gin.Context) {
		principal, err := CurrentPrincipal(c)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, principal)
	})
	protected.POST("/private", func(c *gin.Context) { ok(c, true) })
	protected.GET("/events", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.WriteString("event: ready\ndata: {}\n\n")
		c.Writer.Flush()
		<-c.Request.Context().Done()
	})
	return &platformAuthTestEnvironment{db: db, service: service, admin: admin, auth: auth, router: router}
}

func platformAuthRequest(t *testing.T, router http.Handler, method, path string, body any, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func platformAuthResponse(t *testing.T, recorder *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("期望 HTTP %d，实际 %d: %s", status, recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func csrfForPlatformTest(t *testing.T, env *platformAuthTestEnvironment) (string, []*http.Cookie) {
	t.Helper()
	response := platformAuthRequest(t, env.router, http.MethodGet, "/api/auth/csrf", nil, nil, nil)
	body := platformAuthResponse(t, response, 200)
	return body["data"].(map[string]any)["csrfToken"].(string), response.Result().Cookies()
}

func loginForPlatformTest(t *testing.T, env *platformAuthTestEnvironment, username, password string) (map[string]any, *http.Cookie) {
	t.Helper()
	csrf, cookies := csrfForPlatformTest(t, env)
	response := platformAuthRequest(t, env.router, http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": password}, cookies, map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": csrf})
	body := platformAuthResponse(t, response, 200)["data"].(map[string]any)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == env.auth.cookieName() {
			if strings.Contains(response.Body.String(), cookie.Value) {
				t.Fatal("会话原始令牌泄露到 JSON 响应")
			}
			return body, cookie
		}
	}
	t.Fatal("登录缺少会话 Cookie")
	return nil, nil
}

func TestPlatformAuthenticationHTTPBoundary(t *testing.T) {
	env := newPlatformAuthTestEnvironment(t)
	loginBody := map[string]string{"username": "manager", "password": testPlatformPassword}
	response := platformAuthRequest(t, env.router, http.MethodPost, "/api/auth/login", loginBody, nil, map[string]string{"Origin": testPlatformOrigin})
	if platformAuthResponse(t, response, 403)["reason"] != "csrf_invalid" {
		t.Fatal("缺少登录CSRF未被拒绝")
	}
	for _, headers := range []map[string]string{{"Origin": "https://attacker.test"}, {"Sec-Fetch-Site": "cross-site"}} {
		response = platformAuthRequest(t, env.router, http.MethodGet, "/api/auth/csrf", nil, nil, headers)
		platformAuthResponse(t, response, 403)
	}
	csrf, cookies := csrfForPlatformTest(t, env)
	if len(cookies) != 1 {
		t.Fatal("登录前Cookie数量错误")
	}
	assertPlatformCookie(t, cookies[0])
	response = platformAuthRequest(t, env.router, http.MethodPost, "/api/auth/login", loginBody, cookies, map[string]string{"Origin": "https://attacker.test", "X-CSRF-Token": csrf})
	platformAuthResponse(t, response, 403)
	response = platformAuthRequest(t, env.router, http.MethodPost, "/api/auth/login", loginBody, cookies, map[string]string{"Origin": testPlatformOrigin})
	platformAuthResponse(t, response, 403)
	var failureMessage any
	for _, candidate := range []map[string]string{{"username": "unknown", "password": testPlatformPassword}, {"username": "manager", "password": testPlatformNewPassword}} {
		response = platformAuthRequest(t, env.router, http.MethodPost, "/api/auth/login", candidate, cookies, map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": csrf})
		body := platformAuthResponse(t, response, 401)
		if failureMessage != nil && failureMessage != body["msg"] {
			t.Fatal("登录响应泄露账号状态")
		}
		failureMessage = body["msg"]
	}
	grant, sessionCookie := loginForPlatformTest(t, env, "manager", testPlatformPassword)
	assertPlatformCookie(t, sessionCookie)
	response = platformAuthRequest(t, env.router, http.MethodGet, "/api/private", nil, []*http.Cookie{{Name: env.auth.cookieName(), Value: strings.Repeat("a", 43)}}, nil)
	platformAuthResponse(t, response, 401)
	response = platformAuthRequest(t, env.router, http.MethodGet, "/api/private", nil, nil, map[string]string{"Authorization": "Bearer local-owner-token", "X-Canvas-Owner-Token": "local-owner-token", "X-Canvas-Client-Token": "local-client-token"})
	platformAuthResponse(t, response, 401)
	response = platformAuthRequest(t, env.router, http.MethodGet, "/api/private", nil, []*http.Cookie{sessionCookie}, nil)
	principal := platformAuthResponse(t, response, 200)["data"].(map[string]any)
	if principal["accountId"] != env.admin.ID || principal["workspaceId"] != env.admin.WorkspaceID {
		t.Fatal("账号与工作区身份被混用")
	}
	for _, headers := range []map[string]string{nil, {"Origin": "https://attacker.test", "X-CSRF-Token": grant["csrfToken"].(string)}, {"Origin": testPlatformOrigin, "X-CSRF-Token": strings.Repeat("b", 43)}} {
		response = platformAuthRequest(t, env.router, http.MethodPost, "/api/private", nil, []*http.Cookie{sessionCookie}, headers)
		platformAuthResponse(t, response, 403)
	}
	response = platformAuthRequest(t, env.router, http.MethodPost, "/api/private", nil, []*http.Cookie{sessionCookie}, map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": grant["csrfToken"].(string)})
	platformAuthResponse(t, response, 200)
	response = platformAuthRequest(t, env.router, http.MethodGet, "/api/auth/csrf", nil, []*http.Cookie{sessionCookie}, nil)
	if platformAuthResponse(t, response, 200)["data"].(map[string]any)["csrfToken"] != grant["csrfToken"] {
		t.Fatal("刷新页面改变了现有会话CSRF")
	}
}

func assertPlatformCookie(t *testing.T, cookie *http.Cookie) {
	t.Helper()
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" || !strings.HasPrefix(cookie.Name, "__Host-") {
		t.Fatalf("平台Cookie属性不符合合同: %s", cookie.Name)
	}
}

func TestPlatformSessionBindingAndReauthentication(t *testing.T) {
	env := newPlatformAuthTestEnvironment(t)
	grant, cookie := loginForPlatformTest(t, env, "manager", testPlatformPassword)
	response := platformAuthRequest(t, env.router, http.MethodGet, "/api/private", nil, []*http.Cookie{cookie}, map[string]string{"X-Platform-Session": "previous-session"})
	if platformAuthResponse(t, response, 401)["reason"] != "session_mismatch" {
		t.Fatal("旧标签页读取了新Cookie所属会话")
	}
	response = platformAuthRequest(t, env.router, http.MethodGet, "/api/private", nil, []*http.Cookie{cookie}, map[string]string{"X-Platform-Session": grant["sessionId"].(string)})
	platformAuthResponse(t, response, 200)
	if response.Header().Get("X-Platform-Session") != grant["sessionId"] {
		t.Fatal("响应未标记已验证会话")
	}
	response = platformAuthRequest(t, env.router, http.MethodGet, "/api/auth/csrf?purpose=login", nil, []*http.Cookie{cookie}, nil)
	csrf := platformAuthResponse(t, response, 200)["data"].(map[string]any)["csrfToken"].(string)
	if csrf == grant["csrfToken"] || len(response.Result().Cookies()) != 1 {
		t.Fatal("已有Cookie时无法引导原账号重新认证")
	}
	cookies := append(response.Result().Cookies(), cookie)
	response = platformAuthRequest(t, env.router, http.MethodPost, "/api/auth/login", map[string]string{"username": "manager", "password": testPlatformPassword}, cookies, map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": csrf})
	platformAuthResponse(t, response, 200)
}

func TestPlatformRequiresPasswordChangeBeforeBusinessAccess(t *testing.T) {
	env := newPlatformAuthTestEnvironment(t)
	points, storage := int64(0), int64(1024)
	_, err := env.service.CreateAccount(context.Background(), env.admin.ID, platformauth.CreateAccountInput{Username: "alice", DisplayName: "测试用户", Password: testPlatformPassword, Role: platformauth.RoleUser, InitialPoints: &points, StorageLimitBytes: &storage})
	if err != nil {
		t.Fatal(err)
	}
	grant, cookie := loginForPlatformTest(t, env, "alice", testPlatformPassword)
	response := platformAuthRequest(t, env.router, http.MethodGet, "/api/private", nil, []*http.Cookie{cookie}, nil)
	if platformAuthResponse(t, response, 403)["reason"] != "password_change_required" {
		t.Fatal("初始密码可以直接访问业务")
	}
	response = platformAuthRequest(t, env.router, http.MethodGet, "/api/auth/me", nil, []*http.Cookie{cookie}, nil)
	platformAuthResponse(t, response, 200)
	headers := map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": grant["csrfToken"].(string)}
	response = platformAuthRequest(t, env.router, http.MethodPost, "/api/auth/password", map[string]string{"oldPassword": "incorrect-password", "newPassword": testPlatformNewPassword}, []*http.Cookie{cookie}, headers)
	platformAuthResponse(t, response, 401)
	response = platformAuthRequest(t, env.router, http.MethodPost, "/api/auth/password", map[string]string{"oldPassword": testPlatformPassword, "newPassword": testPlatformNewPassword}, []*http.Cookie{cookie}, headers)
	body := platformAuthResponse(t, response, 200)["data"].(map[string]any)
	if body["account"].(map[string]any)["mustChangePassword"] != false || body["csrfToken"] == grant["csrfToken"] {
		t.Fatal("改密没有更新账号和会话")
	}
	var nextCookie *http.Cookie
	for _, item := range response.Result().Cookies() {
		if item.Name == env.auth.cookieName() {
			nextCookie = item
		}
	}
	if nextCookie == nil {
		t.Fatal("改密缺少新会话")
	}
	response = platformAuthRequest(t, env.router, http.MethodGet, "/api/private", nil, []*http.Cookie{cookie}, nil)
	platformAuthResponse(t, response, 401)
	response = platformAuthRequest(t, env.router, http.MethodGet, "/api/private", nil, []*http.Cookie{nextCookie}, nil)
	platformAuthResponse(t, response, 200)
}

func TestPlatformPasswordResetTerminatesActiveStream(t *testing.T) {
	env := newPlatformAuthTestEnvironment(t)
	_, cookie := loginForPlatformTest(t, env, "manager", testPlatformPassword)
	server := httptest.NewServer(env.router)
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(cookie)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("事件流状态码错误: %d", response.StatusCode)
	}
	completed := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, response.Body); completed <- err }()
	if err := env.service.ResetPassword(context.Background(), env.admin.ID, env.admin.ID, testPlatformNewPassword); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("重置密码后事件流在 %s 内中断", time.Since(started))
	case <-time.After(5 * time.Second):
		t.Fatal("撤销后的事件流未在 5 秒内中断")
	}
	recorded := platformAuthRequest(t, env.router, http.MethodGet, "/api/auth/me", nil, []*http.Cookie{cookie}, nil)
	platformAuthResponse(t, recorded, 401)
}
