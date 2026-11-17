//go:build platform

package platformtask_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/task"
)

func TestTextTaskProjectionUsesSameIntentFactsForListAndDetail(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { textResponse(w) }))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	ctx := context.Background()
	row := created(t, call(f.router, "POST", "/api/tasks", f.request("projection-cases")))
	assertOutcome := func(phase, regenerate, reason string) {
		t.Helper()
		view, err := f.service.Get(f.p, row.ID)
		want := task.Outcome{Phase: phase, Regenerate: regenerate, Reason: reason}
		if err != nil || view.Outcome == nil || *view.Outcome != want {
			t.Fatalf("详情意图投影错误：got=%+v want=%+v err=%v", view, want, err)
		}
		list, err := f.service.TasksWithOptions(f.p.WorkspaceID, task.ListOptions{Limit: 10})
		if err != nil || len(list) != 1 || list[0].Outcome == nil || *list[0].Outcome != want {
			t.Fatalf("列表意图投影错误：%+v %v", list, err)
		}
	}
	assertOutcome("running", "blocked", "task_running")
	if _, err := f.service.Cancel(ctx, f.p, row.ID); err != nil {
		t.Fatal(err)
	}
	assertOutcome("failed", "allowed", "task_failed")
	intent, err := f.quota.IntentForTask(ctx, f.p.WorkspaceID, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.PlatformExecutionIntent{}).Where("id = ?", intent.ID).Update("state", platformquota.StateUnknown).Error; err != nil {
		t.Fatal(err)
	}
	assertOutcome("failed", "blocked", "submission_unknown")
	if err := f.db.Model(&model.Task{}).Where("id = ?", row.ID).Update("result_json", `{"text":"持久化完整答案"}`).Error; err != nil {
		t.Fatal(err)
	}
	assertOutcome("retrievable", "blocked", "delivery_pending")
	duplicate := *intent
	duplicate.ID = "duplicate-intent"
	if err := f.db.Create(&duplicate).Error; err != nil {
		t.Fatal(err)
	}
	assertOutcome("failed", "blocked", "evidence_unavailable")
}

func TestPlatformHistoricalTaskProjectionKeepsOwnedDurableOutputs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { textResponse(w) }))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	row := model.Task{ID: "historical-task", UserID: f.p.WorkspaceID, Type: "canvas_image", Status: model.TaskStatusSucceeded}
	if err := f.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	output, _ := json.Marshal(task.CanonicalOutput{OutputIndex: 0, MediaType: "image", MaterializedAssetID: "owned-asset", ResourceID: "owned-resource"})
	foreign, _ := json.Marshal(task.CanonicalOutput{OutputIndex: 1, MediaType: "image", MaterializedAssetID: "foreign-asset", ResourceID: "foreign-resource"})
	for _, result := range []model.Result{
		{ID: "owned-result", UserID: f.p.WorkspaceID, TaskID: row.ID, Kind: task.ResultKindGenerationOutput, Payload: string(output)},
		{ID: "foreign-result", UserID: "workspace-other", TaskID: row.ID, Kind: task.ResultKindGenerationOutput, Payload: string(foreign)},
	} {
		if err := f.db.Create(&result).Error; err != nil {
			t.Fatal(err)
		}
	}
	view, err := f.service.Get(f.p, row.ID)
	if err != nil || len(view.Outputs) != 1 || view.Outputs[0].ResourceID != "owned-resource" || view.ResultState != task.ResultStateReady || view.Outcome == nil || view.Outcome.Regenerate != "blocked" {
		t.Fatalf("历史任务没有保留自有交付输出或错误开放重生成：%+v %v", view, err)
	}
	list, err := f.service.TasksWithOptions(f.p.WorkspaceID, task.ListOptions{Limit: 10})
	if err != nil || len(list) != 1 || len(list[0].Outputs) != 1 || list[0].Outputs[0].ResourceID != "owned-resource" || list[0].ResultState != task.ResultStateReady {
		t.Fatalf("历史任务列表未使用同一交付投影：%+v %v", list, err)
	}
}

func TestPlatformHistoricalTaskCannotAdvertiseRecoveryFromLocalEvidence(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("历史任务不应发起上游调用")
		textResponse(w)
	}))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	row := model.Task{ID: "local-delivery-failure", UserID: f.p.WorkspaceID, Type: "canvas_text", Status: model.TaskStatusFailed, ResultJSON: `{"text":"本地历史结果"}`}
	if err := f.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	want := task.Outcome{Phase: "failed", Regenerate: "blocked", Reason: "platform_task_not_admitted"}
	view, err := f.service.Get(f.p, row.ID)
	if err != nil || view.Outcome == nil || *view.Outcome != want {
		t.Fatalf("历史详情误报可取回：%+v %v", view, err)
	}
	list, err := f.service.TasksWithOptions(f.p.WorkspaceID, task.ListOptions{Limit: 10})
	if err != nil || len(list) != 1 || list[0].Outcome == nil || *list[0].Outcome != want {
		t.Fatalf("历史列表误报可取回：%+v %v", list, err)
	}
	if _, err := f.service.Recover(t.Context(), f.p, row.ID); err == nil {
		t.Fatal("本地任务缺少平台意图却可恢复")
	}
	var stored model.Task
	if err := f.db.First(&stored, "id = ?", row.ID).Error; err != nil || stored.Status != row.Status || stored.ResultJSON != row.ResultJSON {
		t.Fatal("读取或恢复拒绝修改了历史任务", err)
	}
	if balance := f.balance(t); balance.ReservedPoints != 0 || balance.SettledPoints != 0 {
		t.Fatal("历史任务进入平台账本")
	}
}
