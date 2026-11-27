package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformadmin"
	"infinite-canvas/backend/internal/repository"
)

type catalogFixture struct {
	runtime *Runtime
	auth    *identity.Service
	admin   *platformClient
}

func newCatalogFixture(t *testing.T) *catalogFixture {
	t.Helper()
	t.Setenv("CANVAS_PUBLIC_ORIGIN", "http://localhost:3000")
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
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
	const password = "catalog-behavior-test-password"
	if _, err := auth.InitializeAdmin("catalog-admin", password); err != nil {
		t.Fatal(err)
	}
	return &catalogFixture{runtime: runtime, auth: auth, admin: catalogSession(t, runtime, auth, "catalog-admin", password)}
}

// 会话由真实身份服务签发，后续全部请求经过平台鉴权、权限与 CSRF 中间件。
func catalogSession(t *testing.T, runtime *Runtime, auth *identity.Service, username, password string) *platformClient {
	t.Helper()
	login, err := auth.Login(username, password)
	if err != nil {
		t.Fatal(err)
	}
	return &platformClient{runtime: runtime, userID: login.Account.ID, csrf: login.CSRFToken,
		cookies: []*http.Cookie{{Name: "beeftv_session", Value: login.Token}, {Name: "beeftv_csrf", Value: login.CSRFToken}}}
}

func (f *catalogFixture) user(t *testing.T) *platformClient {
	t.Helper()
	account, temporary, err := f.auth.CreateAccount(f.admin.userID, "catalog-user", "目录测试用户")
	if err != nil {
		t.Fatal(err)
	}
	const password = "catalog-user-test-password"
	if err := f.auth.ChangePassword(account.ID, temporary, password); err != nil {
		t.Fatal(err)
	}
	return catalogSession(t, f.runtime, f.auth, "catalog-user", password)
}

func catalogData[T any](t *testing.T, result map[string]any) T {
	t.Helper()
	encoded, err := json.Marshal(result["data"])
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("解析业务响应失败: %v；响应=%s", err, encoded)
	}
	return value
}

func catalogModel(key, capability, protocol string, variants ...platformadmin.VariantRequest) platformadmin.ModelRequest {
	enabled := true
	return platformadmin.ModelRequest{ModelKey: key, ProviderModelKey: key, DisplayName: key, Capability: capability,
		Protocol: protocol, Enabled: &enabled, CapabilityConfig: modelcatalog.DefaultModelCapabilityConfigForModel(protocol, key), Variants: variants}
}

func catalogVariant(selector map[string]string, amount *int64) platformadmin.VariantRequest {
	enabled := true
	value := platformadmin.VariantRequest{ChannelModelVariantRequest: modelcatalog.ChannelModelVariantRequest{Selector: selector, Enabled: &enabled}}
	if amount != nil {
		value.Price = &platformadmin.PriceRequest{UnitCredits: *amount}
	}
	return value
}

func catalogAmount(value int64) *int64 { return &value }

func (f *catalogFixture) createChannel(t *testing.T, name string, models ...platformadmin.ModelRequest) platformadmin.ChannelView {
	t.Helper()
	request := platformadmin.ChannelRequest{ChannelRequest: modelcatalog.ChannelRequest{Name: name, BaseURL: "https://catalog.example.invalid", APIKey: "catalog-synthetic-key"}, InitialModels: models}
	return catalogData[platformadmin.ChannelView](t, f.admin.call(t, "POST", "/admin/channels", request, 200))
}

func (f *catalogFixture) models(t *testing.T, channelID, query string) platformadmin.ModelPage {
	t.Helper()
	return catalogData[platformadmin.ModelPage](t, f.admin.call(t, "GET", "/admin/channels/"+channelID+"/models"+query, nil, 200))
}

func catalogFindModel(t *testing.T, page platformadmin.ModelPage, key string) platformadmin.ModelView {
	t.Helper()
	for _, item := range page.Items {
		if item.ModelKey == key {
			return item
		}
	}
	t.Fatalf("模型目录中缺少 %q", key)
	return platformadmin.ModelView{}
}

func catalogFindVariant(t *testing.T, item platformadmin.ModelView, key, value string) platformadmin.VariantView {
	t.Helper()
	for _, variant := range item.Variants {
		if variant.Selector[key] == value {
			return variant
		}
	}
	t.Fatalf("模型 %q 缺少规格 %s=%s", item.ModelKey, key, value)
	return platformadmin.VariantView{}
}

func catalogRows(t *testing.T, runtime *Runtime) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	for _, item := range []any{&model.ModelChannel{}, &model.ChannelModel{}, &model.ChannelModelVariant{}, &model.ModelCreditPrice{}, &model.PlatformAudit{}, &model.Task{}, &model.CreditReservation{}} {
		query := runtime.db.Model(item)
		var count int64
		if err := query.Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		counts[query.Statement.Table] = count
	}
	return counts
}

func catalogContextCall(client *platformClient, ctx context.Context, method, path string, body any) (int, map[string]any, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	request := httptest.NewRequest(method, "http://localhost:3000/api"+path, bytes.NewReader(encoded)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://localhost:3000")
	request.Header.Set("X-CSRF-Token", client.csrf)
	request.Header.Set("X-Platform-User", client.userID)
	for _, cookie := range client.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	client.runtime.Handler().ServeHTTP(recorder, request)
	var result map[string]any
	err = json.Unmarshal(recorder.Body.Bytes(), &result)
	return recorder.Code, result, err
}

func catalogUserModels(t *testing.T, client *platformClient) map[string]bool {
	t.Helper()
	result := data(client.call(t, "GET", "/workspace/model-config", nil, 200))
	config := result["config"].(map[string]any)
	keys := map[string]bool{}
	for _, raw := range config["channels"].([]any) {
		channel := raw.(map[string]any)
		for _, key := range channel["models"].([]any) {
			keys[key.(string)] = true
		}
	}
	return keys
}
