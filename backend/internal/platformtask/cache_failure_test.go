//go:build platform

package platformtask_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/platformtask"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/task"
)

func TestTextSuccessfulResultCacheFailureSettlesWithoutClaimingRecoverability(t *testing.T) {
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { posts.Add(1); textResponse(w) }))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	row := created(t, call(f.router, "POST", "/api/tasks", f.request("cache-failure")))
	if err := f.db.Exec(`CREATE TRIGGER fail_result_cache BEFORE UPDATE ON tasks WHEN NEW.result_json <> '' AND NEW.result_json <> OLD.result_json BEGIN SELECT RAISE(FAIL, '模拟完整结果缓存失败'); END`).Error; err != nil {
		t.Fatal(err)
	}
	_ = platformtask.ProcessOneForTest(context.Background(), f.service)
	saved, err := repository.New(f.db).TaskForUser(f.p.WorkspaceID, row.ID)
	if err != nil || saved.Status != model.TaskStatusFailed || saved.ResultJSON != "" {
		t.Fatalf("完整缓存故障没有生效：%+v %v", saved, err)
	}
	if balance := f.balance(t); balance.ReservedPoints != 0 || balance.SettledPoints != 10 || posts.Load() != 1 {
		t.Fatalf("上游成功未按事实结算：POST=%d %+v", posts.Load(), balance)
	}
	if got := call(f.router, "GET", "/api/tasks/"+row.ID+"/text-deltas", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "模拟答案") {
		t.Fatalf("测试未保存草稿，不能证明草稿不会伪装完整结果：%d %s", got.Code, got.Body.String())
	}
	if err := f.db.Exec("DROP TRIGGER fail_result_cache").Error; err != nil {
		t.Fatal(err)
	}
	restarted, err := platformtask.New(repository.New(f.db), platformquota.New(f.db), f.catalog)
	if err != nil {
		t.Fatal(err)
	}
	platformtask.EnableLoopbackForTest(t, restarted, upstream.URL)
	view, err := restarted.Get(f.p, row.ID)
	if err != nil || view.Outcome == nil || view.Outcome.Phase != "failed" || view.Outcome.Regenerate != "confirm" || view.Outcome.Reason != task.ReasonChargedResultUnrecoverable {
		t.Fatalf("未明确投影已收费但完整结果不可恢复：%+v %v", view, err)
	}
	list, err := restarted.TasksWithOptions(f.p.WorkspaceID, task.ListOptions{Limit: 10})
	if err != nil || len(list) != 1 || list[0].Outcome == nil || *list[0].Outcome != *view.Outcome || list[0].FailureDiagnostics != nil {
		t.Fatalf("项目列表与详情投影不一致：%+v %v", list, err)
	}
	if _, err := restarted.Recover(context.Background(), f.p, row.ID); err == nil {
		t.Fatal("完整结果缺失时误把草稿当作可恢复成功结果")
	}
	if got := call(router(restarted, f.p), "POST", "/api/tasks/"+row.ID+"/recover", nil); got.Code != 409 || !strings.Contains(got.Body.String(), task.ReasonChargedResultUnrecoverable) {
		t.Fatalf("恢复接口没有明确说明已结算结果缺失：%d %s", got.Code, got.Body.String())
	}
	_ = platformtask.ProcessOneForTest(context.Background(), restarted)
	if balance := f.balance(t); balance.ReservedPoints != 0 || balance.SettledPoints != 10 || posts.Load() != 1 {
		t.Fatalf("无法恢复时重复提交或收费：POST=%d %+v", posts.Load(), balance)
	}
	closed, err := platformtask.New(repository.New(f.db), f.quota, f.catalog)
	if err != nil {
		t.Fatal(err)
	}
	view, err = closed.Get(f.p, row.ID)
	if err != nil || view.Outcome == nil || view.Outcome.Regenerate != "blocked" || view.Outcome.Reason != task.ReasonChargedResultUnrecoverable {
		t.Fatalf("正式关闭模式仍展示重生成可用：%+v %v", view, err)
	}
}
