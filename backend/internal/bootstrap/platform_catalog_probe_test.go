package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformadmin"
)

func TestPlatformCatalogProbeIsReadOnlyAndUsesSavedCredentials(t *testing.T) {
	f := newCatalogFixture(t)
	type observedRequest struct{ method, path, authorization, geminiKey, customHeader, query string }
	var requests []observedRequest
	var mutex sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mutex.Lock()
		requests = append(requests, observedRequest{request.Method, request.URL.Path, request.Header.Get("Authorization"), request.Header.Get("x-goog-api-key"), request.Header.Get("X-Catalog-Test"), request.URL.RawQuery})
		mutex.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(request.URL.Path, "/v1beta/models") {
			_, _ = w.Write([]byte(`{"models":[{"name":"models/discovered-image","displayName":"上游图片"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"already-added"},{"id":"already-disabled"},{"id":"discovered-neutral","display_name":"上游模型"}]}`))
	}))
	t.Cleanup(upstream.Close)
	const savedKey = "catalog-saved-synthetic-key"
	const overrideKey = "catalog-override-synthetic-key"
	disabled := false
	disabledModel := catalogModel("already-disabled", "audio", "openai-audio")
	disabledModel.Enabled = &disabled
	request := platformadmin.ChannelRequest{ChannelRequest: modelcatalog.ChannelRequest{Name: "探测验收", BaseURL: upstream.URL, APIKey: savedKey,
		Headers: []modelcatalog.ChannelHeader{{Name: "X-Catalog-Test", Value: "saved-header"}}},
		InitialModels: []platformadmin.ModelRequest{catalogModel("already-added", "text", "chat-completion", catalogVariant(nil, catalogAmount(7))), disabledModel}}
	channel := catalogData[platformadmin.ChannelView](t, f.admin.call(t, "POST", "/admin/channels", request, 200))
	var stored model.ModelChannel
	if err := f.runtime.db.First(&stored, "id = ?", channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.APIKey == savedKey || stored.APIKey == "" {
		t.Fatal("渠道凭据未加密保存")
	}
	counts := catalogRows(t, f.runtime)
	modelsBefore := f.models(t, channel.ID, "")
	probes := []platformadmin.ProbeRequest{
		{BaseURL: upstream.URL, APIKey: "catalog-draft-synthetic-key"},
		{ChannelID: channel.ID},
		{ChannelID: channel.ID, APIKey: overrideKey, Headers: []modelcatalog.ChannelHeader{{Name: "X-Catalog-Test", Value: "draft-header"}}},
		{ChannelID: channel.ID, CatalogFormat: "gemini"},
		{ChannelID: channel.ID},
	}
	for i, probe := range probes {
		response := f.admin.call(t, "POST", "/admin/channels/probe", probe, 200)
		result := catalogData[platformadmin.ProbeResult](t, response)
		want := 3
		if probe.CatalogFormat == "gemini" {
			want = 1
		}
		if len(result.Models) != want || result.DurationMs < 0 {
			t.Fatalf("第 %d 次探测目录错误: %+v", i, result)
		}
		if probe.ChannelID != "" {
			existing := map[string]bool{}
			ids, ok := data(response)["existingModelIds"].([]any)
			if !ok {
				t.Fatalf("已有渠道探测缺少已添加模型标识: %+v", response)
			}
			for _, id := range ids {
				existing[id.(string)] = true
			}
			if !existing["already-added"] || !existing["already-disabled"] || existing["discovered-neutral"] {
				t.Fatalf("已添加模型标记遗漏停用模型或误标新模型: %+v", existing)
			}
		}
	}
	mutex.Lock()
	observed := append([]observedRequest(nil), requests...)
	mutex.Unlock()
	if len(observed) != len(probes) {
		t.Fatalf("探测发送了额外请求: %+v", observed)
	}
	for _, request := range observed {
		if request.method != "GET" || !strings.HasSuffix(request.path, "/models") || request.query != "" {
			t.Fatalf("探测不是只读目录请求或将凭据放进 URL: %+v", request)
		}
	}
	if observed[0].authorization != "Bearer catalog-draft-synthetic-key" || observed[1].authorization != "Bearer "+savedKey || observed[1].customHeader != "saved-header" || observed[2].authorization != "Bearer "+overrideKey || observed[2].customHeader != "draft-header" || observed[3].geminiKey != savedKey || observed[3].authorization != "" || observed[4].authorization != "Bearer "+savedKey {
		t.Fatalf("草稿覆盖或已存凭据复用错误: %+v", observed)
	}
	var after model.ModelChannel
	if err := f.runtime.db.First(&after, "id = ?", channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, after) || !reflect.DeepEqual(modelsBefore, f.models(t, channel.ID, "")) || !reflect.DeepEqual(counts, catalogRows(t, f.runtime)) {
		t.Fatal("重复获取目录修改了已存连接、模型、价格、审计或生成账本")
	}
	f.admin.call(t, "POST", "/admin/channels/probe", map[string]any{"channelId": "missing-channel"}, 404)
}

func TestPlatformCatalogProbeDistinguishesEmptyAndFailures(t *testing.T) {
	f := newCatalogFixture(t)
	before := catalogRows(t, f.runtime)
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		reason     string
	}{
		{"空目录", 200, `{"data":[]}`, 200, ""},
		{"未授权", 401, `{"error":"catalog-private-test-key https://internal.invalid"}`, 502, "catalog_auth_failed"},
		{"权限不足", 403, `{"error":"catalog-private-test-key https://internal.invalid"}`, 502, "catalog_auth_failed"},
		{"不存在目录", 404, `{"error":"catalog-private-test-key https://internal.invalid"}`, 502, "catalog_unsupported"},
		{"不支持方法", 405, `{"error":"catalog-private-test-key https://internal.invalid"}`, 502, "catalog_unsupported"},
		{"限流", 429, `{"error":"catalog-private-test-key https://internal.invalid"}`, 502, "catalog_rate_limited"},
		{"异常JSON", 200, `<html>catalog-private-test-key https://internal.invalid</html>`, 502, "catalog_response_invalid"},
		{"错误结构", 200, `{"unexpected":[]}`, 502, "catalog_response_invalid"},
		{"上游故障", 500, `{"error":"catalog-private-test-key https://internal.invalid"}`, 502, "catalog_upstream_failed"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method != "GET" || !strings.HasSuffix(request.URL.Path, "/models") {
					t.Errorf("收到生成或非目录请求: %s %s", request.Method, request.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			t.Cleanup(upstream.Close)
			result := f.admin.call(t, "POST", "/admin/channels/probe", platformadmin.ProbeRequest{BaseURL: upstream.URL, APIKey: "catalog-private-test-key"}, test.wantStatus)
			if test.wantStatus == 200 {
				probe := catalogData[platformadmin.ProbeResult](t, result)
				if len(probe.Models) != 0 {
					t.Fatal("合法空目录不应伪造模型")
				}
			} else if result["reason"] != test.reason {
				t.Fatalf("探测错误没有清楚分类: 期望=%s 实际=%+v", test.reason, result)
			}
			encoded, _ := json.Marshal(result)
			for _, sensitive := range []string{"catalog-private-test-key", "internal.invalid", upstream.URL} {
				if strings.Contains(string(encoded), sensitive) {
					t.Fatalf("探测响应泄露上游敏感信息: %s", encoded)
				}
			}
		})
	}
	if !reflect.DeepEqual(before, catalogRows(t, f.runtime)) {
		t.Fatal("探测空目录或失败时产生了持久化副作用")
	}
}

func TestPlatformCatalogProbeDeadlineAndCancellation(t *testing.T) {
	f := newCatalogFixture(t)
	before := catalogRows(t, f.runtime)
	for _, cancelManually := range []bool{false, true} {
		name := "超时"
		if cancelManually {
			name = "主动取消"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			upstreamCanceled := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				close(started)
				select {
				case <-request.Context().Done():
					close(upstreamCanceled)
				case <-time.After(3 * time.Second):
					w.WriteHeader(http.StatusGatewayTimeout)
				}
			}))
			t.Cleanup(upstream.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			type response struct {
				status int
				body   map[string]any
				err    error
			}
			completed := make(chan response, 1)
			go func() {
				status, body, err := catalogContextCall(f.admin, ctx, "POST", "/admin/channels/probe", platformadmin.ProbeRequest{BaseURL: upstream.URL, APIKey: "catalog-cancel-synthetic-key"})
				completed <- response{status, body, err}
			}()
			select {
			case <-started:
				if cancelManually {
					cancel()
				}
			case <-time.After(2 * time.Second):
				t.Fatal("探测没有发出目录请求")
			}
			select {
			case result := <-completed:
				if result.err != nil {
					t.Fatal(result.err)
				}
				if result.status == 200 {
					t.Fatal("超时或取消被报告为连接成功")
				}
				if !cancelManually && (result.status != 504 || result.body["reason"] != "catalog_timeout") {
					t.Fatalf("超时错误分类不正确: %+v", result)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("取消没有及时停止探测")
			}
			select {
			case <-upstreamCanceled:
			case <-time.After(time.Second):
				t.Fatal("请求取消没有传递给上游")
			}
		})
	}
	if !reflect.DeepEqual(before, catalogRows(t, f.runtime)) {
		t.Fatal("取消或超时产生了模型、任务、冻结积分或审计记录")
	}
}
