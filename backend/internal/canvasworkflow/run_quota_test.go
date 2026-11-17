package canvasworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// 夹具只替换原子准入端口，实际状态推进及重启恢复使用生产调度器。
type quotaRunHost struct {
	RunHost
	db       *gorm.DB
	balance  int
	calls    map[string][]string
	retries  int
	rejected error
}

func (h *quotaRunHost) Generate(user, operation, _ string, n Node, _ map[string]Node, _ map[string]*Step) (*model.Task, error) {
	h.calls[n.ID] = append(h.calls[n.ID], operation)
	if h.rejected != nil {
		return nil, h.rejected
	}
	if h.balance == 0 {
		return nil, fmt.Errorf("准入拒绝：%w", kernel.QuotaExceeded("AI 点数不足"))
	}
	task := &model.Task{ID: "task-" + n.ID, UserID: user, Type: "canvas_text",
		Status: model.TaskStatusSucceeded, ClientOperationID: &operation, ResultJSON: `{"text":"已保存结果"}`}
	if err := h.db.Create(task).Error; err != nil {
		return nil, err
	}
	h.balance--
	return task, nil
}

func (h *quotaRunHost) ReferenceIDs(string) (map[string]bool, error)     { return map[string]bool{}, nil }
func (h *quotaRunHost) Draining() bool                                   { return false }
func (h *quotaRunHost) Outputs(*model.Task) ([]TaskOutput, []TaskOutput) { return nil, nil }
func (h *quotaRunHost) FailureMessage(message, _, _ string) string       { return message }
func (h *quotaRunHost) RetryStep(_ model.CanvasWorkflowRun, _ string, task *model.Task) (*model.Task, error) {
	h.retries++
	return task, nil
}
func (h *quotaRunHost) CancelTask(_ context.Context, user, id string) error {
	return h.db.Model(&model.Task{}).Where("user_id = ? AND id = ?", user, id).Update("status", model.TaskStatusCancelled).Error
}

func quotaRunFixture(t *testing.T, balance int, parallel bool) (*Runs, *quotaRunHost) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "workflow.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err = db.AutoMigrate(&model.CanvasWorkflowRun{}, &model.Task{}); err != nil {
		t.Fatal(err)
	}
	nodes := []Node{}
	steps := []Step{}
	for _, id := range []string{"first", "second", "third"} {
		nodes = append(nodes, Node{ID: id, Title: id, Role: "generation", MediaType: "text"})
		steps = append(steps, Step{NodeID: id, Title: id, Status: "pending"})
	}
	definition := Definition{Version: 1, Nodes: nodes, OutputNodeIDs: []string{"first", "second", "third"}}
	if !parallel {
		definition.Edges = []Edge{{From: "first", To: "second"}, {From: "second", To: "third"}}
	}
	definitionJSON, _ := json.Marshal(definition)
	state, _ := json.Marshal(Run{Status: "queued", Steps: steps, Outputs: []Output{}})
	row := model.CanvasWorkflowRun{ID: "run", UserID: "owner", TemplateID: "template",
		ClientOperationID: "confirmed-run", Status: "queued", DefinitionJSON: string(definitionJSON), StateJSON: string(state)}
	if err = db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	host := &quotaRunHost{db: db, balance: balance, calls: map[string][]string{}}
	loop := false
	return NewRuns(repository.New(db), nil, host, &sync.Mutex{}, &loop), host
}

func loadQuotaRun(t *testing.T, runs *Runs) Run {
	t.Helper()
	run, err := runs.Get("owner", "run")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestQuotaPauseRetainsResultsAndResumesOnlyPendingSteps(t *testing.T) {
	runs, host := quotaRunFixture(t, 1, false)
	runs.AdvanceAll(context.Background())
	paused := loadQuotaRun(t, runs)
	if paused.Status != "paused" || paused.Reason != "quota_exceeded" || paused.Steps[0].Status != "succeeded" ||
		paused.Steps[1].Status != "pending" || paused.Steps[1].TaskID != "" || len(paused.Outputs) != 1 || len(host.calls["third"]) != 0 {
		t.Fatalf("暂停状态或已保存结果错误：%+v", paused)
	}
	loop := false
	runs = NewRuns(repository.New(host.db), nil, host, &sync.Mutex{}, &loop)
	host.balance = 2
	runs.AdvanceAll(context.Background())
	if loadQuotaRun(t, runs).Status != "paused" || len(host.calls["second"]) != 1 {
		t.Fatal("重启或追加点数不能自动恢复准入")
	}
	if unchanged := loadQuotaRun(t, runs); !unchanged.UpdatedAt.Equal(paused.UpdatedAt) {
		t.Fatal("无变化的暂停轮询不应改写运行记录")
	}
	if _, err := runs.Retry("other", "run"); err == nil {
		t.Fatal("其他工作区能够继续运行")
	}
	resumed, err := runs.Retry("owner", "run")
	if err != nil || resumed.Status != "queued" || resumed.Reason != "" || len(resumed.Outputs) != 1 {
		t.Fatalf("继续运行失败：%+v %v", resumed, err)
	}
	runs.AdvanceAll(context.Background())
	finished := loadQuotaRun(t, runs)
	if finished.Status != "succeeded" || len(finished.Outputs) != 3 || host.balance != 0 || host.retries != 0 {
		t.Fatalf("恢复结果错误：%+v，余额=%d，重试=%d", finished, host.balance, host.retries)
	}
	if len(host.calls["first"]) != 1 || len(host.calls["second"]) != 2 || len(host.calls["third"]) != 1 ||
		host.calls["second"][0] != host.calls["second"][1] {
		t.Fatalf("步骤重新执行或标识不稳定：%+v", host.calls)
	}
	runs.AdvanceAll(context.Background())
	var count int64
	host.db.Model(&model.Task{}).Count(&count)
	if count != 3 {
		t.Fatalf("恢复创建了重复任务：%d", count)
	}
}

func TestPausedRunCollectsInFlightResultsWithoutNewAdmission(t *testing.T) {
	runs, host := quotaRunFixture(t, 0, true)
	operation := StepOperation("run", "second")
	task := model.Task{ID: "accepted-second", UserID: "owner", Type: "canvas_text",
		Status: model.TaskStatusRunning, ClientOperationID: &operation}
	if err := host.db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	runs.AdvanceAll(context.Background())
	paused := loadQuotaRun(t, runs)
	if paused.Status != "paused" || paused.Steps[1].TaskID != task.ID || paused.Steps[1].Status != "running" || len(host.calls["third"]) != 0 {
		t.Fatalf("暂停后没有对齐已准入任务：%+v", paused)
	}
	if err := host.db.Model(&task).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"在途结果"}`}).Error; err != nil {
		t.Fatal(err)
	}
	runs.AdvanceAll(context.Background())
	paused = loadQuotaRun(t, runs)
	if paused.Status != "paused" || paused.Steps[1].Text != "在途结果" || len(paused.Outputs) != 1 || len(host.calls["first"]) != 1 {
		t.Fatalf("暂停期间未交付在途结果或重复准入：%+v", paused)
	}
	host.balance = 2
	if _, err := runs.Retry("owner", "run"); err != nil {
		t.Fatal(err)
	}
	runs.AdvanceAll(context.Background())
	if run := loadQuotaRun(t, runs); run.Status != "succeeded" || run.Steps[1].TaskID != task.ID || len(host.calls["second"]) != 0 {
		t.Fatalf("继续运行重建了在途任务：%+v", run)
	}
}

func TestCancelPausedRunKeepsSuccessfulStepsAndStopsPendingAdmission(t *testing.T) {
	runs, host := quotaRunFixture(t, 1, false)
	runs.AdvanceAll(context.Background())
	cancelled, err := runs.Cancel(context.Background(), "owner", "run")
	if err != nil || cancelled.Status != "cancelled" || cancelled.Reason != "" ||
		cancelled.Steps[0].Status != "succeeded" || cancelled.Steps[1].Status != "cancelled" || len(cancelled.Outputs) != 1 {
		t.Fatalf("暂停取消错误：%+v %v", cancelled, err)
	}
	host.balance = 10
	runs.AdvanceAll(context.Background())
	if _, err = runs.Retry("owner", "run"); err == nil {
		t.Fatal("取消运行可以继续")
	}
	if len(host.calls["second"]) != 1 || len(host.calls["third"]) != 0 {
		t.Fatal("取消后仍发起新任务")
	}
}

func TestPausedRunReconcilesReceiptAfterPauseCheckpointCrash(t *testing.T) {
	runs, host := quotaRunFixture(t, 0, true)
	run := loadQuotaRun(t, runs)
	run.Status, run.Reason, run.Error = "paused", "quota_exceeded", "AI 点数不足"
	state, _ := json.Marshal(run)
	if err := host.db.Model(&model.CanvasWorkflowRun{}).Where("id = ?", run.ID).
		Updates(map[string]any{"status": "paused", "state_json": string(state)}).Error; err != nil {
		t.Fatal(err)
	}
	operation := StepOperation("run", "third")
	task := model.Task{ID: "receipt-before-crash", UserID: "owner", Type: "canvas_text",
		Status: model.TaskStatusSucceeded, ClientOperationID: &operation, ResultJSON: `{"text":"重启恢复结果"}`}
	if err := host.db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	runs.AdvanceAll(context.Background())
	reconciled := loadQuotaRun(t, runs)
	if reconciled.Status != "paused" || reconciled.Steps[2].TaskID != task.ID ||
		reconciled.Steps[2].Text != "重启恢复结果" || len(reconciled.Outputs) != 1 || len(host.calls) != 0 {
		t.Fatalf("暂停重启漏掉已准入任务或错误提交：%+v", reconciled)
	}
}

func TestWorkflowPausesOnlyOnStructuredAdmissionQuotaError(t *testing.T) {
	for _, rejected := range []error{errors.New("quota_exceeded"), kernel.Forbidden("模型未授权")} {
		t.Run(rejected.Error(), func(t *testing.T) {
			runs, host := quotaRunFixture(t, 1, false)
			host.rejected = rejected
			runs.AdvanceAll(context.Background())
			run := loadQuotaRun(t, runs)
			if run.Status != "failed" || run.Reason != "" || run.Steps[0].Status != "failed" {
				t.Fatalf("非额度错误被当成暂停：%+v", run)
			}
		})
	}
}

func TestContinuePausedRunRechecksAdmissionAuthorization(t *testing.T) {
	runs, host := quotaRunFixture(t, 1, false)
	runs.AdvanceAll(context.Background())
	host.balance = 10
	host.rejected = kernel.Forbidden("模型授权已撤销")
	if _, err := runs.Retry("owner", "run"); err != nil {
		t.Fatal(err)
	}
	runs.AdvanceAll(context.Background())
	run := loadQuotaRun(t, runs)
	if run.Status != "failed" || run.Reason != "" || run.Steps[0].Status != "succeeded" ||
		len(run.Outputs) != 1 || len(host.calls["first"]) != 1 || len(host.calls["third"]) != 0 || host.balance != 10 {
		t.Fatalf("继续未重新准入或覆盖成功结果：%+v", run)
	}
}
