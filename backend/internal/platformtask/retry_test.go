//go:build platform

package platformtask_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/platformtask"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/taskruntime"
)

func TestTextRetryFreezesCurrentVersionsAndPreservesOriginal(t *testing.T) {
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if r.Header.Get("Authorization") != "Bearer new-key" {
			t.Errorf("重试未使用当前凭据：%q", r.Header.Get("Authorization"))
		}
		textResponse(w)
	}))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	ctx := context.Background()
	original := created(t, call(f.router, "POST", "/api/tasks", f.request("retry-current")))
	created(t, call(f.router, "POST", "/api/tasks/"+original.ID+"/cancel", nil))
	before, err := repository.New(f.db).TaskForUser(f.p.WorkspaceID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldIntent, err := f.quota.IntentForTask(ctx, f.p.WorkspaceID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := f.catalog.RotateCredential(ctx, "manager", f.channelID, admin.PlatformCredentialInput{BaseURL: upstream.URL, APIKey: "new-key"})
	if err != nil {
		t.Fatal(err)
	}
	price, err := f.catalog.PublishPrice(ctx, "manager", f.modelID, "text_generation", 20)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := f.catalog.LogicalModel(ctx, "manager", f.modelID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.catalog.PublishLogicalModel(ctx, "manager", f.modelID, admin.PlatformLogicalModelInput{
		Code: previous.Model.Code, Name: previous.Model.Name, Enabled: true, ExpectedRevisionID: previous.Revision.ID,
		CapabilitySpec: previous.CapabilitySpec, DefaultOptions: previous.DefaultOptions,
		Routes: []admin.PlatformLogicalRouteInput{{ChannelModelID: previous.Routes[0].ChannelModelID, Enabled: true, Weight: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]string{"clientOperationId": "retry-current"}
	retried := created(t, call(f.router, "POST", "/api/tasks/"+original.ID+"/retry", body))
	intent, err := f.quota.IntentForTask(ctx, f.p.WorkspaceID, retried.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.ID == original.ID || intent.ID == oldIntent.ID || intent.Points != 20 || intent.PriceVersionID != price.ID || intent.CredentialVersionID != credential.CredentialVersionID || intent.RevisionID != current.Revision.ID || intent.RevisionID == oldIntent.RevisionID {
		t.Fatalf("重试没有新建任务并冻结当前版本：old=%+v new=%+v", oldIntent, intent)
	}
	var metadata struct {
		Metadata struct {
			RetryOf string `json:"retryOf"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(retried.InputJSON), &metadata) != nil || metadata.Metadata.RetryOf != original.ID {
		t.Fatalf("重试未保留原任务关联：%s", retried.InputJSON)
	}
	replayed := created(t, call(f.router, "POST", "/api/tasks/"+original.ID+"/retry", body))
	if retried.ID != replayed.ID || f.balance(t).ReservedPoints != 20 {
		t.Fatal("重试网络重放重复建立预留")
	}
	if got := call(f.router, "GET", "/api/tasks/by-operation/retry-current", nil); got.Code != 409 || !strings.Contains(got.Body.String(), "idempotency_scope_required") {
		t.Fatalf("同键不同操作未要求明确作用域：%d %s", got.Code, got.Body.String())
	}
	for kind, id := range map[string]string{"create": original.ID, "retry": retried.ID} {
		if got := created(t, call(f.router, "GET", "/api/tasks/by-operation/retry-current?kind="+kind, nil)); got.ID != id || got.ClientOperationID == nil || *got.ClientOperationID != "retry-current" {
			t.Fatalf("操作定位错误：%s %+v", kind, got)
		}
	}
	if outcome := platformtask.ProcessOneForTest(ctx, f.service); outcome.Kind != taskruntime.KindCompleted || outcome.Err != nil {
		t.Fatalf("重试执行失败：%+v", outcome)
	}
	if balance := f.balance(t); posts.Load() != 1 || balance.SettledPoints != 20 || balance.ReservedPoints != 0 {
		t.Fatalf("重试调用或结算错误：POST=%d %+v", posts.Load(), balance)
	}
	after, err := repository.New(f.db).TaskForUser(f.p.WorkspaceID, original.ID)
	if err != nil || after.Status != before.Status || after.InputJSON != before.InputJSON || after.ResultJSON != before.ResultJSON || after.LogicalModelRevisionID != before.LogicalModelRevisionID {
		t.Fatalf("旧任务被重试覆盖：%+v %v", after, err)
	}
	other := created(t, call(f.router, "POST", "/api/tasks", f.request("second-original")))
	created(t, call(f.router, "POST", "/api/tasks/"+other.ID+"/cancel", nil))
	if got := call(f.router, "POST", "/api/tasks/"+other.ID+"/retry", body); got.Code != 409 || !strings.Contains(got.Body.String(), "idempotency_conflict") {
		t.Fatalf("同重试键改用其他原任务未冲突：%d %s", got.Code, got.Body.String())
	}
	if got := call(f.router, "POST", "/api/tasks/"+original.ID+"/retry", nil); got.Code != 400 || !strings.Contains(got.Body.String(), "idempotency_required") {
		t.Fatalf("重试缺键未拒绝：%d %s", got.Code, got.Body.String())
	}
	if _, err := f.catalog.ReplaceModelGrants(ctx, "manager", "creator", []string{}); err != nil {
		t.Fatal(err)
	}
	if got := call(f.router, "POST", "/api/tasks/"+original.ID+"/retry", map[string]string{"clientOperationId": "revoked-retry"}); got.Code != 403 {
		t.Fatalf("撤权后仍允许新重试：%d %s", got.Code, got.Body.String())
	}
	if got := created(t, call(f.router, "POST", "/api/tasks/"+original.ID+"/retry", body)); got.ID != retried.ID {
		t.Fatal("撤权影响既有请求收据取回")
	}
	if balance := f.balance(t); balance.ReservedPoints != 0 || balance.SettledPoints != 20 {
		t.Fatalf("拒绝或重放追加了扣点：%+v", balance)
	}
}

func TestTextRetryRejectsUnknownAndRequiresExistingResultRecovery(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("拒绝路径不应发送上游")
		textResponse(w)
	}))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	ctx := context.Background()
	row := created(t, call(f.router, "POST", "/api/tasks", f.request("unknown-original")))
	intent, err := f.quota.IntentForTask(ctx, f.p.WorkspaceID, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.quota.BeginSubmission(ctx, f.p.WorkspaceID, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Task{}).Where("id = ?", row.ID).Update("status", model.TaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	if got := call(f.router, "POST", "/api/tasks/"+row.ID+"/retry", map[string]string{"clientOperationId": "unknown-retry"}); got.Code != 409 || !strings.Contains(got.Body.String(), "submission_unknown") {
		t.Fatalf("未终结意图仍允许重试：%d %s", got.Code, got.Body.String())
	}
	if got := call(f.router, "POST", "/api/tasks/"+row.ID+"/retry", nil); got.Code != 400 || !strings.Contains(got.Body.String(), "idempotency_required") {
		t.Fatalf("未知原意图上的重试没有优先要求幂等标识：%d %s", got.Code, got.Body.String())
	}
	if err := f.quota.Observe(ctx, f.p.WorkspaceID, intent.ID, platformquota.FactSucceeded); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Task{}).Where("id = ?", row.ID).Update("result_json", `{"text":"已有答案"}`).Error; err != nil {
		t.Fatal(err)
	}
	if got := call(f.router, "POST", "/api/tasks/"+row.ID+"/retry", map[string]string{"clientOperationId": "cached-retry"}); got.Code != 409 || !strings.Contains(got.Body.String(), "recovery_required") {
		t.Fatalf("有成功缓存仍允许重试：%d %s", got.Code, got.Body.String())
	}
	if balance := f.balance(t); balance.ReservedPoints != 0 || balance.SettledPoints != 10 {
		t.Fatalf("拒绝重试改变原账本：%+v", balance)
	}
}
