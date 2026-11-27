package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/storage"
)

func TestPlatformStoragePermissionsVersionAndCSRF(t *testing.T) {
	f := newCatalogFixture(t)
	value := catalogData[storage.View](t, f.admin.call(t, "GET", "/admin/storage", nil, 200))
	if value.Provider != "local" || value.Version != 0 || value.OSS != nil {
		t.Fatalf("平台默认设置错误: %+v", value)
	}
	user := f.user(t)
	user.call(t, "GET", "/admin/storage", nil, 403)
	user.call(t, "PUT", "/admin/storage", storage.SaveRequest{Provider: "local"}, 403)
	user.call(t, "POST", "/admin/storage/probe", storage.ProbeRequest{}, 403)
	value = catalogData[storage.View](t, f.admin.call(t, "PUT", "/admin/storage", storage.SaveRequest{Provider: "local"}, 200))
	if value.Version != 1 {
		t.Fatal("配置版本未推进")
	}
	f.admin.call(t, "PUT", "/admin/storage", storage.SaveRequest{Provider: "local"}, 409)
	request := httptest.NewRequest("PUT", "http://localhost:3000/api/admin/storage", strings.NewReader(`{"provider":"local","expectedVersion":1}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:3000")
	request.Header.Set("X-Platform-User", f.admin.userID)
	for _, cookie := range f.admin.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	f.runtime.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("无 CSRF 写入未拒绝: %d", recorder.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	status, _, err := catalogContextCall(f.admin, ctx, "PUT", "/admin/storage", storage.SaveRequest{Provider: "local", ExpectedVersion: 1})
	if err != nil || status == 200 {
		t.Fatalf("取消保存错误: %d %v", status, err)
	}
	final := f.admin.call(t, "GET", "/admin/storage", nil, 200)
	data, _ := json.Marshal(final)
	if strings.Contains(string(data), "accessKeySecret\"") || strings.Contains(string(data), "accessKeyId\"") {
		t.Fatal("管理接口包含凭据字段")
	}
}
