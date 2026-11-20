package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/repository"
)

func TestPlatformPrivateResourcesCanvasAndSessionRevocation(t *testing.T) {
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
	auth := identity.New(repository.NewPlatform(runtime.db))
	admin, err := auth.InitializeAdmin("isolation-admin", "isolation-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	newClient := func(name string) *platformClient {
		account, temporary, err := auth.CreateAccount(admin.ID, name, "隔离测试用户")
		if err != nil {
			t.Fatal(err)
		}
		if err := auth.ChangePassword(account.ID, temporary, "isolation-user-password"); err != nil {
			t.Fatal(err)
		}
		client := &platformClient{runtime: runtime}
		client.call(t, "POST", "/auth/login", map[string]any{"username": name, "password": "isolation-user-password"}, 200)
		return client
	}
	owner, other := newClient("isolation-one"), newClient("isolation-two")
	manager := &platformClient{runtime: runtime}
	manager.call(t, "POST", "/auth/login", map[string]any{"username": admin.Username, "password": "isolation-admin-password"}, 200)
	canvasID := kernel.NewID()
	canvas := map[string]any{"id": canvasID, "name": "私有画布", "revision": 0, "nodes": []any{}, "connections": []any{}}
	owner.call(t, "PUT", "/canvas-projects/"+canvasID, map[string]any{"project": canvas}, 200)
	owner.call(t, "GET", "/canvas-projects/"+canvasID, nil, 200)
	for _, client := range []*platformClient{other, manager} {
		client.call(t, "GET", "/canvas-projects/"+canvasID, nil, 404)
		client.call(t, "DELETE", "/canvas-projects/"+canvasID, nil, 404)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "private.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("仅所属账号可读"))
	_ = writer.WriteField("kind", "file")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/resources", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-CSRF-Token", owner.csrf)
	req.Header.Set("X-Platform-User", owner.userID)
	for _, cookie := range owner.cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("上传失败: %d %s", response.Code, response.Body.String())
	}
	var uploaded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	resourceID := data(uploaded)["resource"].(map[string]any)["id"].(string)
	for _, client := range []*platformClient{other, manager} {
		client.call(t, "GET", "/resources/"+resourceID+"/file", nil, 404)
	}
	req = httptest.NewRequest("GET", "/api/resources/"+resourceID+"/file", nil)
	for _, cookie := range owner.cookies {
		req.AddCookie(cookie)
	}
	response = httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, req)
	if response.Code != 200 || response.Result().Header.Get("Cache-Control") != "no-store" || response.Body.String() != "仅所属账号可读" {
		t.Fatalf("私有文件或缓存策略错误: %d %v", response.Code, response.Result().Header)
	}
	server := httptest.NewServer(runtime.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err = http.NewRequestWithContext(ctx, "GET", server.URL+"/api/canvas-projects/"+canvasID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range owner.cookies {
		req.AddCookie(cookie)
	}
	stream, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != 200 {
		t.Fatalf("事件流状态错误: %d", stream.StatusCode)
	}
	owner.call(t, "POST", "/auth/logout", map[string]any{}, 200)
	if _, err := io.ReadAll(stream.Body); err != nil {
		t.Fatalf("撤销会话后事件流未及时关闭: %v", err)
	}
	owner.call(t, "GET", "/canvas-projects/"+canvasID, nil, 401)
}
