//go:build platform

package platformboot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"infinite-canvas/backend/internal/bootstrap"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"

	"github.com/google/uuid"
)

const platformTestPassword = "platform-test-password-123!"

type platformFixture struct {
	runtime *Runtime
	server  *httptest.Server
	admin   *platformauth.Account
}

type platformBrowser struct {
	fixture *platformFixture
	client  *http.Client
	csrf    string
	account *platformauth.Account
}

type platformResponse struct {
	Code   int             `json:"code"`
	Data   json.RawMessage `json:"data"`
	Reason string          `json:"reason"`
	Msg    string          `json:"msg"`
}

func platformAccountInput(username string) platformauth.CreateAccountInput {
	points, storage := int64(0), int64(1<<30)
	return platformauth.CreateAccountInput{Username: username, DisplayName: "测试账号", Password: platformTestPassword, Role: platformauth.RoleUser, InitialPoints: &points, StorageLimitBytes: &storage}
}

func newPlatformFixture(t *testing.T) *platformFixture {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	runtime, err := Open(t.Context(), Config{DataDir: t.TempDir(), Origin: "https://" + server.Listener.Addr().String()})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	admin, err := runtime.Auth.Bootstrap(t.Context(), platformAccountInput("administrator"))
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = runtime.Handler
	server.StartTLS()
	t.Cleanup(server.Close)
	return &platformFixture{runtime: runtime, server: server, admin: admin}
}

func (f *platformFixture) browser(t *testing.T) *platformBrowser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &platformBrowser{fixture: f, client: &http.Client{Transport: f.server.Client().Transport, Jar: jar, Timeout: 15 * time.Second}}
}

func (b *platformBrowser) request(t *testing.T, method, path string, body any, expected int, headers map[string]string) platformResponse {
	t.Helper()
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, b.fixture.server.URL+"/api"+path, payload)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", b.fixture.server.URL)
	request.Header.Set("X-CSRF-Token", b.csrf)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := b.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result platformResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("%s %s 返回非JSON响应：%v", method, request.URL.Path, err)
	}
	if response.StatusCode != expected || (expected == 200 && result.Code != 0) {
		t.Fatalf("%s %s：status=%d code=%d reason=%s msg=%s，预期status=%d", method, request.URL.Path, response.StatusCode, result.Code, result.Reason, result.Msg, expected)
	}
	return result
}

func decodePlatformData[T any](t *testing.T, response platformResponse) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func (f *platformFixture) login(t *testing.T, username string, changePassword bool) *platformBrowser {
	t.Helper()
	browser := f.browser(t)
	csrf := decodePlatformData[struct{ CSRFToken string }](t, browser.request(t, "GET", "/auth/csrf", nil, 200, nil))
	browser.csrf = csrf.CSRFToken
	grant := decodePlatformData[platformauth.SessionGrant](t, browser.request(t, "POST", "/auth/login", map[string]any{"username": username, "password": platformTestPassword}, 200, nil))
	browser.csrf, browser.account = grant.CSRFToken, grant.Account
	if changePassword {
		if !grant.Account.MustChangePassword {
			t.Fatal("管理员开户后没有强制改密")
		}
		blocked := browser.request(t, "GET", "/projects", nil, 403, nil)
		if blocked.Reason != "password_change_required" {
			t.Fatalf("强制改密错误原因不匹配：%s", blocked.Reason)
		}
		grant = decodePlatformData[platformauth.SessionGrant](t, browser.request(t, "POST", "/auth/password", map[string]any{"oldPassword": platformTestPassword, "newPassword": username + "-changed-password-123!"}, 200, nil))
		browser.csrf, browser.account = grant.CSRFToken, grant.Account
		if browser.account.MustChangePassword {
			t.Fatal("改密成功后仍要求再次改密")
		}
	}
	return browser
}

func TestPlatformRuntimeBootstrapAndModeIsolation(t *testing.T) {
	fixture := newPlatformFixture(t)
	if fixture.admin.ID == fixture.admin.WorkspaceID || fixture.admin.WorkspaceID == "" {
		t.Fatal("账号与工作区没有独立绑定")
	}
	if _, err := fixture.runtime.Auth.Bootstrap(t.Context(), platformAccountInput("second-admin")); err == nil {
		t.Fatal("平台接受了重复管理员引导")
	}
	var accounts, markers int64
	if err := fixture.runtime.db.Model(&platformauth.Account{}).Count(&accounts).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.db.Model(&platformauth.BootstrapMarker{}).Count(&markers).Error; err != nil || accounts != 1 || markers != 1 {
		t.Fatalf("重复引导留下半成品：accounts=%d markers=%d err=%v", accounts, markers, err)
	}
	var dbs []struct{ Name, File string }
	if err := fixture.runtime.db.Raw("PRAGMA database_list").Scan(&dbs).Error; err != nil {
		t.Fatal(err)
	}
	var directory string
	for _, entry := range dbs {
		if entry.Name == "main" {
			directory = filepath.Dir(entry.File)
		}
	}
	if duplicate, err := Open(t.Context(), Config{DataDir: directory, Origin: fixture.server.URL}); err == nil {
		_ = duplicate.Close()
		t.Fatal("平台数据目录允许第二个运行实例")
	}
	if local, err := bootstrap.Open(t.Context(), bootstrap.Config{Profile: bootstrap.ProfileServer, DataDir: directory, AutoMigrate: true}); !errors.Is(err, database.ErrRuntimeModeMismatch) {
		if local != nil {
			_ = local.Close(t.Context())
		}
		t.Fatalf("本地入口接受平台目录：%v", err)
	}
	localDir := t.TempDir()
	guard, err := database.PrepareRuntimeDirectory(localDir, identity.ModeLocal)
	if err != nil {
		t.Fatal(err)
	}
	localDB, err := database.Open(database.Config{DataDir: localDir})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := localDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := guard.CheckDatabase(localDB); err != nil {
		t.Fatal(err)
	}
	if platform, err := Open(t.Context(), Config{DataDir: localDir, Origin: fixture.server.URL}); !errors.Is(err, database.ErrRuntimeModeMismatch) {
		if platform != nil {
			_ = platform.Close()
		}
		t.Fatalf("平台入口接受本地目录：%v", err)
	}
}

func TestPlatformHTTPRejectsAlternateCredentialsAndClosedRoutes(t *testing.T) {
	fixture := newPlatformFixture(t)
	browser := fixture.login(t, fixture.admin.Username, false)
	serverURL, err := url.Parse(fixture.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var sessionToken string
	for _, cookie := range browser.client.Jar.Cookies(serverURL) {
		if cookie.Name == "__Host-beeftv-session" {
			sessionToken = cookie.Value
		}
	}
	if sessionToken == "" {
		t.Fatal("登录未设置安全会话Cookie")
	}
	anonymous := fixture.browser(t)
	anonymous.request(t, "GET", "/projects?token="+url.QueryEscape(sessionToken)+"&userId="+fixture.admin.WorkspaceID, nil, 401, map[string]string{
		"Authorization": "Bearer " + sessionToken, "X-Desktop-Token": sessionToken, "X-Beeftv-Owner": sessionToken, "X-Beeftv-Client": "test-local-client",
	})
	for _, headers := range []map[string]string{{"X-CSRF-Token": ""}, {"Origin": "https://another.example"}} {
		result := browser.request(t, "POST", "/projects", map[string]any{"name": "不可创建"}, 403, headers)
		if result.Reason != "csrf_invalid" {
			t.Fatalf("CSRF失败原因不匹配：%s", result.Reason)
		}
	}
	for _, route := range []struct{ method, path string }{
		{"POST", "/ai/custom"}, {"POST", "/assistant/chat"}, {"GET", "/workspace/model-config"},
		{"POST", "/tasks/example/text-deltas"}, {"GET", "/ops"},
		{"POST", "/diagnostics/environment"}, {"PATCH", "/resources/storage-usage"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			browser.request(t, route.method, route.path, map[string]any{}, 404, nil)
		})
	}
	result := browser.request(t, "POST", "/tasks", map[string]any{}, 403, nil)
	if result.Reason != "generation_unavailable" {
		t.Fatalf("未验收的真实生成没有关闭：%s", result.Reason)
	}
}

func TestPlatformHTTPWorkspaceOwnershipAndRevision(t *testing.T) {
	fixture := newPlatformFixture(t)
	browsers := []*platformBrowser{fixture.login(t, fixture.admin.Username, false)}
	for _, username := range []string{"alice", "bravo"} {
		account, err := fixture.runtime.Auth.CreateAccount(t.Context(), fixture.admin.ID, platformAccountInput(username))
		if err != nil {
			t.Fatal(err)
		}
		browsers = append(browsers, fixture.login(t, account.Username, true))
	}
	type ownedObjects struct {
		project model.Project
		unit    model.ProjectUnit
		canvas  string
	}
	owned := make([]ownedObjects, len(browsers))
	for index, browser := range browsers {
		result := decodePlatformData[struct{ Project model.Project }](t, browser.request(t, "POST", "/projects", map[string]any{"name": "同名工作区项目", "userId": fixture.admin.WorkspaceID}, 200, nil))
		if result.Project.UserID != browser.account.WorkspaceID {
			t.Fatal("请求正文覆盖了会话工作区")
		}
		unit := decodePlatformData[struct{ Unit model.ProjectUnit }](t, browser.request(t, "POST", "/projects/"+result.Project.ID+"/units", map[string]any{"title": "第一章", "sourceText": "原始正文"}, 200, nil))
		canvasID := uuid.NewString()
		browser.request(t, "PUT", "/canvas-projects/"+canvasID, canvasRequest(canvasID, 0, "原始画布"), 200, nil)
		owned[index] = ownedObjects{project: result.Project, unit: unit.Unit, canvas: canvasID}
	}
	for actor, browser := range browsers {
		t.Run(fmt.Sprintf("账号%d的列表与版本", actor), func(t *testing.T) {
			projects := decodePlatformData[struct {
				Projects []struct{ Project model.Project }
			}](t, browser.request(t, "GET", "/projects", nil, 200, nil))
			if len(projects.Projects) != 1 || projects.Projects[0].Project.ID != owned[actor].project.ID {
				t.Fatal("项目列表泄漏其他工作区内容")
			}
			canvases := decodePlatformData[struct{ Projects []struct{ ID string } }](t, browser.request(t, "GET", "/canvas-projects", nil, 200, nil))
			if len(canvases.Projects) != 1 || canvases.Projects[0].ID != owned[actor].canvas {
				t.Fatal("画布列表泄漏其他工作区内容")
			}
			projectPath := "/projects/" + owned[actor].project.ID
			current := decodePlatformData[struct{ Project model.Project }](t, browser.request(t, "GET", projectPath+"/core", nil, 200, nil))
			browser.request(t, "PATCH", projectPath, map[string]any{"expectedRevision": current.Project.Revision, "name": "已更新项目"}, 200, nil)
			stale := browser.request(t, "PATCH", projectPath, map[string]any{"expectedRevision": current.Project.Revision, "name": "迟到覆盖"}, 409, nil)
			if stale.Reason != "revision_conflict" {
				t.Fatalf("项目版本冲突原因不匹配：%s", stale.Reason)
			}
			unitPath := projectPath + "/units/" + owned[actor].unit.ID
			snapshot := decodePlatformData[struct{ ProjectRevision int64 }](t, browser.request(t, "GET", unitPath, nil, 200, nil))
			browser.request(t, "PATCH", unitPath, map[string]any{"expectedRevision": snapshot.ProjectRevision, "sourceText": "最新正文"}, 200, nil)
			browser.request(t, "PATCH", unitPath, map[string]any{"expectedRevision": snapshot.ProjectRevision, "sourceText": "迟到正文"}, 409, nil)
			browser.request(t, "PUT", "/canvas-projects/"+owned[actor].canvas, canvasRequest(owned[actor].canvas, 1, "新画布标题"), 200, nil)
			browser.request(t, "PUT", "/canvas-projects/"+owned[actor].canvas, canvasRequest(owned[actor].canvas, 1, "迟到画布"), 409, nil)
			browser.request(t, "POST", projectPath+"/canvas-links", map[string]any{"canvasId": owned[actor].canvas, "unitId": owned[actor].unit.ID}, 200, nil)
		})
		for target, object := range owned {
			if actor == target {
				continue
			}
			projectPath := "/projects/" + object.project.ID
			for _, method := range []string{"GET", "PATCH", "DELETE"} {
				t.Run(fmt.Sprintf("账号%d对%d项目%s", actor, target, method), func(t *testing.T) {
					browser.request(t, method, projectPath, map[string]any{"expectedRevision": 1, "name": "越权修改"}, 404, nil)
				})
			}
			for _, method := range []string{"GET", "PUT", "DELETE"} {
				t.Run(fmt.Sprintf("账号%d对%d画布%s", actor, target, method), func(t *testing.T) {
					browser.request(t, method, "/canvas-projects/"+object.canvas, canvasRequest(object.canvas, 1, "越权修改"), 404, nil)
				})
			}
			for _, suffix := range []string{"/history", "/events"} {
				t.Run(fmt.Sprintf("账号%d对%d画布%s", actor, target, suffix), func(t *testing.T) {
					browser.request(t, "GET", "/canvas-projects/"+object.canvas+suffix, nil, 404, nil)
				})
			}
			t.Run(fmt.Sprintf("账号%d关联%d的画布或章节", actor, target), func(t *testing.T) {
				ownPath := "/projects/" + owned[actor].project.ID + "/canvas-links"
				browser.request(t, "POST", ownPath, map[string]any{"canvasId": object.canvas, "unitId": owned[actor].unit.ID}, 404, nil)
				browser.request(t, "POST", ownPath, map[string]any{"canvasId": owned[actor].canvas, "unitId": object.unit.ID}, 404, nil)
			})
		}
	}
	for index, browser := range browsers {
		browser.request(t, "DELETE", "/canvas-projects/"+owned[index].canvas, nil, 200, nil)
		browser.request(t, "GET", "/canvas-projects/"+owned[index].canvas, nil, 404, nil)
		browser.request(t, "DELETE", "/projects/"+owned[index].project.ID, nil, 200, nil)
		browser.request(t, "GET", "/projects/"+owned[index].project.ID, nil, 404, nil)
	}
}

func canvasRequest(id string, revision int64, title string) map[string]any {
	return map[string]any{"project": map[string]any{"id": id, "revision": revision, "title": title, "nodes": []any{}, "connections": []any{}}}
}
