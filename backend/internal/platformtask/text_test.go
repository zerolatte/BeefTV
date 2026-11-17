//go:build platform

package platformtask_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/platformtask"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/taskruntime"
)

func textResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"模拟答案\"}}]}\n\ndata: [DONE]\n\n")
}

func TestHTTPTextTaskUsesWorkerAndChargesOnce(t *testing.T) {
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer old-key" {
			t.Errorf("上游身份或方法错误：%s %q", r.Method, r.Header.Get("Authorization"))
		}
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "upstream-text" || !body.Stream {
			t.Errorf("未使用冻结模型或服务端流式文本：%+v", body)
		}
		textResponse(w)
	}))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	request := f.request("operation-http-1")
	first := created(t, call(f.router, "POST", "/api/tasks", request))
	replay := created(t, call(f.router, "POST", "/api/tasks", request))
	if first.ID != replay.ID || f.balance(t).ReservedPoints != 10 {
		t.Fatal("幂等请求产生多份任务或预留")
	}
	result := platformtask.ProcessOneForTest(context.Background(), f.service)
	if result.Kind != taskruntime.KindCompleted || result.Err != nil {
		t.Fatalf("执行失败：%+v", result)
	}
	if balance := f.balance(t); balance.SettledPoints != 10 || balance.ReservedPoints != 0 || posts.Load() != 1 {
		t.Fatalf("结算异常：%+v POST=%d", balance, posts.Load())
	}
	for _, path := range []string{"/api/tasks/" + first.ID, "/api/tasks/" + first.ID + "/text-deltas", "/api/tasks/" + first.ID + "/text-events"} {
		response := call(f.router, "GET", path, nil)
		if response.Code != 200 || !strings.Contains(response.Body.String(), "模拟答案") || strings.Contains(response.Body.String(), "old-key") || strings.Contains(response.Body.String(), upstream.URL) {
			t.Fatalf("读取结果错误或凭据泄漏：%s %d %s", path, response.Code, response.Body.String())
		}
	}
	if got := call(f.router, "POST", "/api/tasks/"+first.ID+"/text-deltas", map[string]any{"content": "伪造"}); got.Code != 404 {
		t.Fatalf("平台客户端仍能写文本：%d", got.Code)
	}
	for _, suffix := range []string{"", "/logs", "/text-deltas", "/text-events"} {
		if got := call(router(f.service, principal("other")), "GET", "/api/tasks/"+first.ID+suffix, nil); got.Code != 404 {
			t.Fatalf("跨工作区读取未404：%s %d %s", suffix, got.Code, got.Body.String())
		}
	}
	request.Prompt = "不同问题"
	conflict := call(f.router, "POST", "/api/tasks", request)
	if conflict.Code != 409 || !strings.Contains(conflict.Body.String(), "idempotency_conflict") {
		t.Fatalf("同键异参未冲突：%d %s", conflict.Code, conflict.Body.String())
	}
	intent, err := f.quota.IntentForTask(context.Background(), f.p.WorkspaceID, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.quota.Observe(context.Background(), f.p.WorkspaceID, intent.ID, platformquota.FactSucceeded); err != nil {
		t.Fatal(err)
	}
	if balance := f.balance(t); balance.SettledPoints != 10 {
		t.Fatalf("重复成功通知重复扣点：%+v", balance)
	}
}

func TestTextTaskConcurrencyCannotOverspend(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { textResponse(w) }))
	defer upstream.Close()
	f := setup(t, upstream.URL, 50)
	var accepted atomic.Int32
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			_, err := f.service.Create(context.Background(), f.p, f.request(fmt.Sprintf("parallel-%02d", i)))
			if err == nil {
				accepted.Add(1)
			}
		}(i)
	}
	group.Wait()
	var tasks, intents int64
	if err := f.db.Model(&model.Task{}).Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.PlatformExecutionIntent{}).Count(&intents).Error; err != nil {
		t.Fatal(err)
	}
	if balance := f.balance(t); accepted.Load() != 5 || tasks != 5 || intents != 5 || balance.ReservedPoints != 50 {
		t.Fatalf("并发超发或事务撕裂：成功=%d tasks=%d intents=%d balance=%+v", accepted.Load(), tasks, intents, balance)
	}
}

func TestTextTaskFreezesCredentialAndRevokesBeforeDispatch(t *testing.T) {
	var keys []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Authorization"))
		textResponse(w)
	}))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	created(t, call(f.router, "POST", "/api/tasks", f.request("credential-old")))
	if _, err := f.catalog.RotateCredential(context.Background(), "manager", f.channelID, admin.PlatformCredentialInput{BaseURL: upstream.URL, APIKey: "new-key"}); err != nil {
		t.Fatal(err)
	}
	created(t, call(f.router, "POST", "/api/tasks", f.request("credential-new")))
	for i := 0; i < 2; i++ {
		if outcome := platformtask.ProcessOneForTest(context.Background(), f.service); outcome.Err != nil {
			t.Fatalf("执行错误：%+v", outcome)
		}
	}
	if len(keys) != 2 || keys[0] != "Bearer old-key" || keys[1] != "Bearer new-key" {
		t.Fatalf("在途任务凭据被覆盖：%v", keys)
	}
	created(t, call(f.router, "POST", "/api/tasks", f.request("revoked-queued")))
	if _, err := f.catalog.ReplaceModelGrants(context.Background(), "manager", "creator", []string{}); err != nil {
		t.Fatal(err)
	}
	_ = platformtask.ProcessOneForTest(context.Background(), f.service)
	if balance := f.balance(t); len(keys) != 2 || balance.ReservedPoints != 0 || balance.SettledPoints != 20 {
		t.Fatalf("撤权任务被提交或未释放：%v %+v", keys, balance)
	}
}

func TestTextTaskRetainsFrozenChannelModelAfterAdminEdit(t *testing.T) {
	var models []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		models = append(models, body.Model)
		textResponse(w)
	}))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	created(t, call(f.router, "POST", "/api/tasks", f.request("model-before-edit")))
	enabled := true
	updated, err := f.catalog.SaveChannelModel(context.Background(), "manager", f.channelID, "", modelcatalog.ChannelModelRequest{
		ModelKey: "mock-text-v2", ProviderModelKey: "updated-upstream-text", DisplayName: "新版模拟文本模型", Capability: "text", Protocol: string(model.ChannelInterfaceChatCompletion), Enabled: &enabled,
		CapabilityConfig: modelcatalog.DefaultModelCapabilityConfig("chat-completion"),
	})
	if err != nil {
		t.Fatal(err)
	}
	previous, err := f.catalog.LogicalModel(context.Background(), "manager", f.modelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.catalog.PublishLogicalModel(context.Background(), "manager", f.modelID, admin.PlatformLogicalModelInput{
		Code: previous.Model.Code, Name: previous.Model.Name, Enabled: true, ExpectedRevisionID: previous.Revision.ID,
		CapabilitySpec: previous.CapabilitySpec, DefaultOptions: previous.DefaultOptions,
		Routes: []admin.PlatformLogicalRouteInput{{ChannelModelID: updated.ID, Enabled: true, Weight: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	created(t, call(f.router, "POST", "/api/tasks", f.request("model-after-edit")))
	for i := 0; i < 2; i++ {
		if outcome := platformtask.ProcessOneForTest(context.Background(), f.service); outcome.Kind != taskruntime.KindCompleted || outcome.Err != nil {
			t.Fatalf("冻结模型执行失败：%+v", outcome)
		}
	}
	if len(models) != 2 || models[0] != "upstream-text" || models[1] != "updated-upstream-text" {
		t.Fatalf("在途任务模型配置被管理员后续编辑覆盖：%v", models)
	}
}

func TestProductionTaskServiceRemainsClosedAndRejectsOverrides(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("拒绝路径不应联网"); textResponse(w) }))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	closed, err := platformtask.New(repository.New(f.db), f.quota, f.catalog)
	if err != nil {
		t.Fatal(err)
	}
	if response := call(router(closed, f.p), "POST", "/api/tasks", f.request("production-closed")); response.Code != 403 || !strings.Contains(response.Body.String(), "generation_unavailable") {
		t.Fatalf("正式构造器没有关闭生成：%d %s", response.Code, response.Body.String())
	}
	for _, key := range []string{"baseUrl", "apiKey", "headers", "interfaceType", "channelId"} {
		request := f.request("override-" + key)
		request.Input["config"] = map[string]any{key: "forbidden"}
		if response := call(f.router, "POST", "/api/tasks", request); response.Code != 400 {
			t.Fatalf("覆盖字段未拒绝：%s %d", key, response.Code)
		}
	}
	request := f.request("")
	if response := call(f.router, "POST", "/api/tasks", request); response.Code != 400 || !strings.Contains(response.Body.String(), "idempotency_required") {
		t.Fatalf("缺少幂等标识未拒绝：%d %s", response.Code, response.Body.String())
	}
}
