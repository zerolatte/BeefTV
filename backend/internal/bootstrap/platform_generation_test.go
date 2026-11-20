package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/repository"
)

func TestPlatformGenerationPersistsAndSettlesOnce(t *testing.T) {
	for _, mode := range []string{"text", "image"} {
		t.Run(mode, func(t *testing.T) { testPlatformGeneration(t, mode) })
	}
}

func testPlatformGeneration(t *testing.T, mode string) {
	t.Setenv("CANVAS_PUBLIC_ORIGIN", "http://localhost:3000")
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("生成请求方法错误: %s", r.Method)
			w.WriteHeader(405)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if mode == "image" {
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a9l8AAAAASUVORK5CYII="}]}`))
		} else {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"测试生成正文"},"finish_reason":"stop"}]}`))
		}
	}))
	defer upstream.Close()
	runtime, err := Open(context.Background(), Config{Profile: ProfileServer, DataDir: t.TempDir(), AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	repo := repository.NewPlatform(runtime.db)
	auth := identity.New(repo)
	admin, err := auth.InitializeAdmin("test-generator", "test-generator-password")
	if err != nil {
		t.Fatal(err)
	}
	client := &platformClient{runtime: runtime}
	client.call(t, "POST", "/auth/login", map[string]any{"username": admin.Username, "password": "test-generator-password"}, 200)
	enabled := true
	channel, err := runtime.service.CreateSystemChannel(&admin.User, modelcatalog.ChannelRequest{Name: "测试上游", BaseURL: upstream.URL, APIKey: "synthetic-test-key", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	protocol, modelKey := "chat-completion", "test-"+mode
	if mode == "image" {
		protocol = "openai-image"
	}
	m, err := runtime.service.SaveAdminChannelModel(&admin.User, channel.ID, "", modelcatalog.ChannelModelRequest{ModelKey: modelKey, DisplayName: "测试模型", Capability: mode, Protocol: protocol, Enabled: &enabled, CapabilityConfig: modelcatalog.DefaultModelCapabilityConfigForModel(protocol, modelKey)})
	if err != nil {
		t.Fatal(err)
	}
	client.call(t, "PUT", "/admin/prices", map[string]any{"modelId": m.ID, "variantId": m.Variants[0].ID, "unitCredits": 3, "published": true}, 200)
	client.call(t, "POST", "/admin/accounts/"+admin.ID+"/credits", map[string]any{"amount": 10, "operationId": "generation-grant", "reason": "集成测试"}, 200)
	request := map[string]any{"type": "canvas_" + mode, "operation": mode, "prompt": "生成测试内容", "model": modelKey, "input": map[string]any{"mode": mode, "prompt": "生成测试内容", "config": map[string]any{"channelId": channel.ID, "model": modelKey}, "metadata": map[string]any{"clientOperationId": "generation-integration"}}}
	quote := data(client.call(t, "POST", "/tasks/quote", request, 200))
	request["quoteId"] = quote["id"]
	created := data(client.call(t, "POST", "/tasks", request, 200))
	id := created["id"].(string)
	runtime.service.StartWorker()
	deadline := time.Now().Add(20 * time.Second)
	for {
		row, err := repo.TaskForUser(admin.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if row.Status == model.TaskStatusSucceeded && row.LeaseOwner == "" {
			break
		}
		if row.Status == model.TaskStatusFailed {
			t.Fatalf("生成失败: %s %s", row.ErrorCode, row.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("生成未完成: %s %s", row.Status, row.Stage)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := runtime.platformCredits.Reconcile(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.platformCredits.Reconcile(); err != nil {
		t.Fatal(err)
	}
	replay := data(client.call(t, "POST", "/tasks", request, 200))
	if replay["id"] != id {
		t.Fatal("重放创建了新任务")
	}
	balance, err := repo.CreditAccount(admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if balance.Available != 7 || balance.Frozen != 0 || calls.Load() != 1 {
		t.Fatalf("结算或上游调用次数错误: %+v calls=%d", balance, calls.Load())
	}
}
