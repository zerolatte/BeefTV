//go:build platform

package platformtask_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	cw "infinite-canvas/backend/internal/canvasworkflow"
	"infinite-canvas/backend/internal/handler"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/platformtask"
	"infinite-canvas/backend/internal/platformworkflow"
	"infinite-canvas/backend/internal/repository"
)

func workflowService(t *testing.T, f fixture) *platformworkflow.Service {
	t.Helper()
	s, err := platformworkflow.New(repository.New(f.db), f.service.WorkflowTasks(), modelcatalog.NewPlatformCatalogReader(repository.New(f.db)), platformworkflow.Cipher{
		Encrypt: func(v string) (string, error) { return "sealed:" + v, nil },
		Decrypt: func(v string) (string, error) {
			if !strings.HasPrefix(v, "sealed:") {
				return "", errors.New("配方不可解密")
			}
			return strings.TrimPrefix(v, "sealed:"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func workflowScope(t *testing.T, s *platformworkflow.Service, p identity.Principal) *platformworkflow.Workspace {
	t.Helper()
	w, err := s.Workspace(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func workflowDefinition(modelID string, two bool) cw.Definition {
	d := cw.Definition{Version: 1, PresentationVersion: 1, Nodes: []cw.Node{
		{ID: "input", Title: "输入", Role: "input", MediaType: "text", Text: "原文"},
		{ID: "first", Title: "第一步", Role: "operation", MediaType: "text", OperationID: "generate.text", OperationVersion: 1, Config: map[string]any{"modelOption": modelID, "prompt": "整理：@[node:input]"}},
	}, Edges: []cw.Edge{{From: "input", To: "first", FromPort: "output", ToPort: "prompt"}}, OutputNodeIDs: []string{"first"}}
	if two {
		d.Nodes = append(d.Nodes, cw.Node{ID: "second", Title: "第二步", Role: "operation", MediaType: "text", OperationID: "generate.text", OperationVersion: 1, Config: map[string]any{"modelOption": modelID, "prompt": "续写：@[node:first]"}})
		d.Edges = append(d.Edges, cw.Edge{From: "first", To: "second", FromPort: "output", ToPort: "prompt"})
		d.OutputNodeIDs = append(d.OutputNodeIDs, "second")
	}
	return d
}

func workflowCreate(t *testing.T, w *platformworkflow.Workspace, modelID string, two bool) cw.Template {
	t.Helper()
	v, err := w.Create(cw.CreateRequest{Name: "文本工作流", Definition: workflowDefinition(modelID, two)})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func workflowStart(t *testing.T, w *platformworkflow.Workspace, id, key string) cw.Run {
	t.Helper()
	v, err := w.Start(id, cw.RunRequest{ClientOperationID: key, TemplateRevision: 1, Inputs: map[string]cw.Value{"input": {Text: "新输入"}}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func workflowLoad(t *testing.T, w *platformworkflow.Workspace, id string) cw.Run {
	t.Helper()
	v, err := w.Run(id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func assertWorkflowReason(t *testing.T, err error, status int, reason string) {
	t.Helper()
	var app *kernel.AppError
	if !errors.As(err, &app) || app.Status != status || reason != "" && string(app.Reason) != reason {
		t.Fatalf("错误不匹配: %v", err)
	}
}

func TestPlatformWorkflowPausesAndResumesWithoutRechargingSuccess(t *testing.T) {
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { posts.Add(1); textResponse(w) }))
	defer upstream.Close()
	f := setup(t, upstream.URL, 10)
	s := workflowService(t, f)
	w := workflowScope(t, s, f.p)
	template := workflowCreate(t, w, f.modelID, true)
	run := workflowStart(t, w, template.ID, "run-once")
	replay := workflowStart(t, w, template.ID, "run-once")
	if run.ID != replay.ID {
		t.Fatal("运行重复创建")
	}
	_, err := w.Start(template.ID, cw.RunRequest{ClientOperationID: "run-once", Inputs: map[string]cw.Value{"input": {Text: "不同输入"}}})
	assertWorkflowReason(t, err, 409, "idempotency_conflict")
	s.Advance(t.Context())
	if result := platformtask.ProcessOneForTest(t.Context(), f.service); result.Err != nil {
		t.Fatal(result.Err)
	}
	s.Advance(t.Context())
	paused := workflowLoad(t, w, run.ID)
	if paused.Status != "paused" || paused.Reason != "quota_exceeded" || paused.Steps[1].Status != "succeeded" || posts.Load() != 1 {
		t.Fatalf("暂停结果错误: %+v, calls=%d", paused, posts.Load())
	}
	if _, err := f.quota.Adjust(t.Context(), "manager", "creator", platformquota.AdjustmentInput{ClientOperationID: "add-points-once", Points: 10, Reason: "继续工作流"}); err != nil {
		t.Fatal(err)
	}
	s = workflowService(t, f)
	w = workflowScope(t, s, f.p)
	s.Advance(t.Context())
	if workflowLoad(t, w, run.ID).Status != "paused" {
		t.Fatal("充值或重启自动恢复了工作流")
	}
	if _, err = w.Retry(run.ID, "continue-once"); err != nil {
		t.Fatal(err)
	}
	s.Advance(t.Context())
	if result := platformtask.ProcessOneForTest(t.Context(), f.service); result.Err != nil {
		t.Fatal(result.Err)
	}
	s.Advance(t.Context())
	finished := workflowLoad(t, w, run.ID)
	if finished.Status != "succeeded" || len(finished.Outputs) != 2 || posts.Load() != 2 || f.balance(t).SettledPoints != 20 || finished.Steps[1].TaskID != paused.Steps[1].TaskID {
		t.Fatalf("恢复重复生成或结算: %+v, calls=%d", finished, posts.Load())
	}
	if _, err = w.Retry(run.ID, "continue-once"); err != nil {
		t.Fatal("旧请求没有重放", err)
	}
	if _, err = f.catalog.ReplaceModelGrants(t.Context(), "manager", "creator", []string{}); err != nil {
		t.Fatal(err)
	}
	revoked := workflowStart(t, w, template.ID, "run-after-revocation")
	s.Advance(t.Context())
	if result := workflowLoad(t, w, revoked.ID); result.Status != "failed" || posts.Load() != 2 || f.balance(t).SettledPoints != 20 {
		t.Fatal("工作流新步骤没有重新检查模型授权", result)
	}
}

func TestPlatformWorkflowRetrySurvivesCheckpointFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { textResponse(w) }))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	s := workflowService(t, f)
	w := workflowScope(t, s, f.p)
	template := workflowCreate(t, w, f.modelID, false)
	run := workflowStart(t, w, template.ID, "retry-source")
	s.Advance(t.Context())
	started := workflowLoad(t, w, run.ID)
	originalID := started.Steps[1].TaskID
	if _, err := f.service.Cancel(t.Context(), f.p, originalID); err != nil {
		t.Fatal(err)
	}
	s.Advance(t.Context())
	if err := f.db.Exec("CREATE TRIGGER fail_workflow_checkpoint BEFORE UPDATE ON canvas_workflow_runs WHEN NEW.status = 'queued' AND OLD.status = 'failed' BEGIN SELECT RAISE(ABORT, 'checkpoint unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := w.Retry(run.ID, "retry-once"); err == nil {
		t.Fatal("未注入终态写入失败")
	}
	if f.balance(t).ReservedPoints != 10 {
		t.Fatal("失败重试没有保留任务预留")
	}
	if err := f.db.Exec("DROP TRIGGER fail_workflow_checkpoint").Error; err != nil {
		t.Fatal(err)
	}
	s = workflowService(t, f)
	w = workflowScope(t, s, f.p)
	replayed, err := w.Retry(run.ID, "retry-once")
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.db.Model(&model.Task{}).Where("user_id = ?", f.p.WorkspaceID).Count(&count).Error; err != nil || count != 2 {
		t.Fatal("重启重复创建任务", count, err)
	}
	if replayed.Steps[1].TaskID == originalID {
		t.Fatal("未保留新的执行意图")
	}
	if result := platformtask.ProcessOneForTest(t.Context(), f.service); result.Err != nil {
		t.Fatal(result.Err)
	}
	s.Advance(t.Context())
	if result := workflowLoad(t, w, run.ID); result.Status != "succeeded" {
		t.Fatalf("重试未完成 %+v", result)
	}
	if _, err = w.Retry(run.ID, "retry-once"); err != nil {
		t.Fatal(err)
	}
	other := workflowStart(t, w, template.ID, "other-run")
	_, err = w.Retry(other.ID, "retry-once")
	assertWorkflowReason(t, err, 409, "idempotency_conflict")
	encoded, _ := json.Marshal(workflowLoad(t, w, run.ID))
	if strings.Contains(string(encoded), "retryRequests") {
		t.Fatal("公开运行响应暴露了内部重试收据")
	}
}

func workflowRouter(service *platformworkflow.Service, p identity.Principal) *gin.Engine {
	r := gin.New()
	g := r.Group("/api")
	g.Use(func(c *gin.Context) { _ = handler.SetPrincipal(c, p); c.Next() })
	handler.RegisterPlatformWorkflowRoutes(g, service)
	return r
}

func TestPlatformWorkflowOwnershipAndClosedProductionGate(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { textResponse(w) }))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	s := workflowService(t, f)
	w := workflowScope(t, s, f.p)
	template := workflowCreate(t, w, f.modelID, false)
	run := workflowStart(t, w, template.ID, "owner-run")
	for _, account := range []string{"other", "manager"} {
		router := workflowRouter(s, principal(account))
		if response := call(router, "GET", "/api/canvas-workflows", nil); response.Code != 200 || strings.Contains(response.Body.String(), template.ID) {
			t.Fatal("列表泄露其他工作区模板", response.Body.String())
		}
		for _, request := range []struct {
			method, path string
			body         any
		}{
			{"GET", "/canvas-workflows/" + template.ID, nil},
			{"GET", "/canvas-workflows/" + template.ID + "/editor", nil},
			{"PUT", "/canvas-workflows/" + template.ID, cw.UpdateRequest{Name: "修改", ExpectedRevision: 1, Definition: workflowDefinition(f.modelID, false)}},
			{"DELETE", "/canvas-workflows/" + template.ID, nil},
			{"GET", "/canvas-workflows/" + template.ID + "/runs", nil},
			{"POST", "/canvas-workflows/" + template.ID + "/runs", cw.RunRequest{ClientOperationID: "foreign-operation"}},
			{"GET", "/canvas-workflow-runs/" + run.ID, nil},
			{"POST", "/canvas-workflow-runs/" + run.ID + "/retry", map[string]string{"clientOperationId": "foreign-operation"}},
			{"POST", "/canvas-workflow-runs/" + run.ID + "/cancel", nil},
			{"POST", "/canvas-workflow-runs/" + run.ID + "/select-image", map[string]string{"nodeId": "x", "resourceId": "x"}},
		} {
			if response := call(router, request.method, "/api"+request.path, request.body); response.Code != 404 {
				t.Fatalf("跨工作区未返回404: %s %s: %d %s", request.method, request.path, response.Code, response.Body.String())
			}
		}
	}
	bad := workflowDefinition(f.modelID, false)
	bad.Nodes[1].Config["baseUrl"] = "https://untrusted.test"
	if _, err := w.Create(cw.CreateRequest{Name: "禁止配置", Definition: bad}); err == nil {
		t.Fatal("接受了上游覆盖")
	}
	bad = workflowDefinition(f.modelID, false)
	bad.Nodes[1].SourceTaskID = "foreign-source-task"
	if err := f.db.Create(&model.Task{ID: "foreign-source-task", UserID: "workspace-other", Type: "canvas_text", LogicalModelID: f.modelID, Status: model.TaskStatusSucceeded}).Error; err != nil {
		t.Fatal(err)
	}
	_, err := w.Create(cw.CreateRequest{Name: "越权关联", Definition: bad})
	assertWorkflowReason(t, err, 404, "")
	bad = workflowDefinition(f.modelID, false)
	bad.Nodes[1].Config["modelOption"] = "not-authorized"
	_, err = w.Create(cw.CreateRequest{Name: "未授权模型", Definition: bad})
	assertWorkflowReason(t, err, 403, "model_not_authorized")
	production, err := platformtask.New(repository.New(f.db), f.quota, f.catalog)
	if err != nil {
		t.Fatal(err)
	}
	f.service = production
	closed := workflowScope(t, workflowService(t, f), f.p)
	_, err = closed.Start(template.ID, cw.RunRequest{ClientOperationID: "production"})
	assertWorkflowReason(t, err, 403, "generation_unavailable")
	_, err = closed.Retry(run.ID, "production-retry")
	assertWorkflowReason(t, err, 403, "generation_unavailable")
	quarantined := workflowLoad(t, w, run.ID)
	quarantined.Status, quarantined.Reason = "failed", string(kernel.ReasonBackupRecoveryRequired)
	state, _ := json.Marshal(quarantined)
	if err := f.db.Model(&model.CanvasWorkflowRun{}).Where("id = ?", run.ID).Updates(map[string]any{"status": "failed", "state_json": string(state)}).Error; err != nil {
		t.Fatal(err)
	}
	_, err = closed.Retry(run.ID, "restored-retry")
	assertWorkflowReason(t, err, 409, string(kernel.ReasonBackupRecoveryRequired))
	if err := f.db.Model(&platformauth.Account{}).Where("id = ?", f.p.AccountID).Update("status", platformauth.StatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	_, err = s.Workspace(context.Background(), f.p)
	assertWorkflowReason(t, err, 401, "")
}

func TestPlatformWorkflowRetryQuotaPauseKeepsOriginalBatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { textResponse(w) }))
	defer upstream.Close()
	f := setup(t, upstream.URL, 10)
	s := workflowService(t, f)
	w := workflowScope(t, s, f.p)
	template := workflowCreate(t, w, f.modelID, false)
	run := workflowStart(t, w, template.ID, "quota-retry-source")
	s.Advance(t.Context())
	before := workflowLoad(t, w, run.ID)
	if _, err := f.service.Cancel(t.Context(), f.p, before.Steps[1].TaskID); err != nil {
		t.Fatal(err)
	}
	s.Advance(t.Context())
	if _, err := f.quota.Adjust(t.Context(), "manager", "creator", platformquota.AdjustmentInput{ClientOperationID: "remove-points", Points: -10, Reason: "测试调减"}); err != nil {
		t.Fatal(err)
	}
	_, err := w.Retry(run.ID, "quota-retry-once")
	assertWorkflowReason(t, err, 403, "quota_exceeded")
	s.Advance(t.Context())
	if state := workflowLoad(t, w, run.ID); state.Status != "paused" || state.Reason != "quota_exceeded" {
		t.Fatalf("失败重试未保持额度暂停 %+v", state)
	}
	if _, err := f.quota.Adjust(t.Context(), "manager", "creator", platformquota.AdjustmentInput{ClientOperationID: "restore-points", Points: 10, Reason: "测试追加"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Retry(run.ID, "quota-retry-once"); err != nil {
		t.Fatal(err)
	}
	if result := platformtask.ProcessOneForTest(t.Context(), f.service); result.Err != nil {
		t.Fatal(result.Err)
	}
	s.Advance(t.Context())
	if state := workflowLoad(t, w, run.ID); state.Status != "succeeded" {
		t.Fatalf("原批次没有完成 %+v", state)
	}
}

func TestPlatformWorkflowLegacyDefinitionKeepsPromptAfterSourceCleanup(t *testing.T) {
	const expectedPrompt = "请整理：新输入。保留结尾"
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		found := false
		for _, message := range body.Messages {
			found = found || message.Role == "user" && message.Content == expectedPrompt
		}
		if !found {
			t.Errorf("提示词指令丢失或替换错误: %+v", body.Messages)
		}
		textResponse(w)
	}))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	s := workflowService(t, f)
	w := workflowScope(t, s, f.p)
	source := model.Task{ID: "legacy-source", UserID: f.p.WorkspaceID, Type: "canvas_text", LogicalModelID: f.modelID, Status: model.TaskStatusSucceeded, Prompt: "请整理：原文。保留结尾"}
	if err := f.db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	definition := cw.Definition{Version: 1, Nodes: []cw.Node{
		{ID: "input", Title: "输入", Role: "input", MediaType: "text", Text: "原文"},
		{ID: "output", Title: "输出", Role: "generation", MediaType: "text", SourceTaskID: source.ID},
	}, Edges: []cw.Edge{{From: "input", To: "output"}}, OutputNodeIDs: []string{"output"}}
	template, err := w.Create(cw.CreateRequest{Name: "画布保存的流程", Definition: definition})
	if err != nil {
		t.Fatal(err)
	}
	if template.Definition.Nodes[1].Role != "operation" || template.Definition.Nodes[1].OperationID != "generate.text" || template.Definition.Edges[0].ToPort != "prompt" {
		t.Fatalf("保存的定义没有规范化: %+v", template.Definition)
	}
	if err := f.db.Delete(&source).Error; err != nil {
		t.Fatal(err)
	}
	template, err = w.Update(template.ID, cw.UpdateRequest{Name: "来源清理后编辑", ExpectedRevision: template.Revision, Definition: definition})
	if err != nil {
		t.Fatal("模板未保留来源配方", err)
	}
	run, err := w.Start(template.ID, cw.RunRequest{ClientOperationID: "legacy-run", TemplateRevision: template.Revision, Inputs: map[string]cw.Value{"input": {Text: "新输入"}}})
	if err != nil {
		t.Fatal(err)
	}
	s.Advance(t.Context())
	if result := platformtask.ProcessOneForTest(t.Context(), f.service); result.Err != nil {
		t.Fatal(result.Err)
	}
	s.Advance(t.Context())
	if result := workflowLoad(t, w, run.ID); result.Status != "succeeded" || posts.Load() != 1 || f.balance(t).SettledPoints != 10 {
		t.Fatalf("旧定义没有完成一次执行: %+v, posts=%d", result, posts.Load())
	}
	definition.Edges[0].From = "missing-input"
	_, err = w.Prepare(definition, template.ID, template.Revision)
	assertWorkflowReason(t, err, 400, "")
}

func TestPlatformWorkflowCancelFindsRetryBeforeCheckpoint(t *testing.T) {
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posts.Add(1)
		textResponse(w)
	}))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	s := workflowService(t, f)
	w := workflowScope(t, s, f.p)
	template := workflowCreate(t, w, f.modelID, false)
	run := workflowStart(t, w, template.ID, "cancel-retry-source")
	s.Advance(t.Context())
	originalID := workflowLoad(t, w, run.ID).Steps[1].TaskID
	if _, err := f.service.Cancel(t.Context(), f.p, originalID); err != nil {
		t.Fatal(err)
	}
	s.Advance(t.Context())
	if err := f.db.Exec("CREATE TRIGGER fail_cancel_retry_checkpoint BEFORE UPDATE ON canvas_workflow_runs WHEN NEW.status = 'queued' AND OLD.status = 'failed' BEGIN SELECT RAISE(ABORT, 'checkpoint unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := w.Retry(run.ID, "cancel-retry-once"); err == nil || f.balance(t).ReservedPoints != 10 {
		t.Fatal("未进入重试已准入但运行 checkpoint 失败窗口", err)
	}
	if err := f.db.Exec("DROP TRIGGER fail_cancel_retry_checkpoint").Error; err != nil {
		t.Fatal(err)
	}
	s = workflowService(t, f)
	w = workflowScope(t, s, f.p)
	cancelled, err := w.Cancel(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" || cancelled.Steps[1].TaskID == originalID || cancelled.Steps[1].Status != "cancelled" || f.balance(t).ReservedPoints != 0 {
		t.Fatalf("取消未找回重试任务或释放预留: %+v", cancelled)
	}
	if result := platformtask.ProcessOneForTest(t.Context(), f.service); result.Err != nil {
		t.Fatal(result.Err)
	}
	if posts.Load() != 0 || f.balance(t).SettledPoints != 0 {
		t.Fatal("已取消的重试仍提交上游或扣费")
	}
	if _, err := w.Retry(run.ID, "cancel-retry-once"); err == nil {
		t.Fatal("取消后重放未完成请求重新开始了工作流")
	}
}
