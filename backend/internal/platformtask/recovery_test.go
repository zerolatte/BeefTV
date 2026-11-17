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
	"infinite-canvas/backend/internal/taskruntime"
)

func TestTextResultSurvivesSettlementAndCompletionFailures(t *testing.T) {
	for _, stage := range []string{"settlement", "completion"} {
		t.Run(stage, func(t *testing.T) {
			var posts atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { posts.Add(1); textResponse(w) }))
			defer upstream.Close()
			f := setup(t, upstream.URL, 100)
			row := created(t, call(f.router, "POST", "/api/tasks", f.request("failure-"+stage)))
			trigger := `CREATE TRIGGER fault BEFORE UPDATE ON platform_quota_accounts WHEN NEW.settled_points > OLD.settled_points BEGIN SELECT RAISE(FAIL, '模拟结算写入失败'); END`
			if stage == "completion" {
				trigger = `CREATE TRIGGER fault BEFORE UPDATE ON tasks WHEN NEW.status = 'succeeded' BEGIN SELECT RAISE(FAIL, '模拟终态保存失败'); END`
			}
			if err := f.db.Exec(trigger).Error; err != nil {
				t.Fatal(err)
			}
			outcome := platformtask.ProcessOneForTest(context.Background(), f.service)
			if outcome.Err == nil {
				t.Fatalf("故障没有命中：%+v", outcome)
			}
			saved, err := repository.New(f.db).TaskForUser(f.p.WorkspaceID, row.ID)
			if err != nil || !strings.Contains(saved.ResultJSON, "模拟答案") {
				t.Fatalf("上游成功结果未先保存：%+v %v", saved, err)
			}
			if err := f.db.Exec("DROP TRIGGER fault").Error; err != nil {
				t.Fatal(err)
			}
			restarted, err := platformtask.New(repository.New(f.db), platformquota.New(f.db), f.catalog)
			if err != nil {
				t.Fatal(err)
			}
			platformtask.EnableLoopbackForTest(t, restarted, upstream.URL)
			if _, err := restarted.Recover(context.Background(), f.p, row.ID); err != nil {
				t.Fatal(err)
			}
			outcome = platformtask.ProcessOneForTest(context.Background(), restarted)
			if outcome.Kind != taskruntime.KindCompleted || outcome.Err != nil {
				t.Fatalf("原任务恢复失败：%+v", outcome)
			}
			if balance := f.balance(t); posts.Load() != 1 || balance.ReservedPoints != 0 || balance.SettledPoints != 10 {
				t.Fatalf("恢复重复调用或扣点：POST=%d %+v", posts.Load(), balance)
			}
		})
	}
}

func TestUnknownSubmissionNeverPostsAgainAfterRestart(t *testing.T) {
	for _, stage := range []string{"before-send", "connection-lost"} {
		t.Run(stage, func(t *testing.T) {
			var posts atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				posts.Add(1)
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
			}))
			defer upstream.Close()
			f := setup(t, upstream.URL, 100)
			row := created(t, call(f.router, "POST", "/api/tasks", f.request("unknown-"+stage)))
			intent, err := f.quota.IntentForTask(context.Background(), f.p.WorkspaceID, row.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "before-send" {
				allowed, err := f.quota.BeginSubmission(context.Background(), f.p.WorkspaceID, intent.ID)
				if err != nil || !allowed {
					t.Fatalf("无法模拟提交围栏后崩溃：%t %v", allowed, err)
				}
			} else {
				outcome := platformtask.ProcessOneForTest(context.Background(), f.service)
				if outcome.Kind != taskruntime.KindUncertain {
					t.Fatalf("丢失响应没有标记未知：%+v", outcome)
				}
				// 模拟崩溃时任务仍在队列，持久化意图必须独立阻止再次发送。
				if err := f.db.Model(&model.Task{}).Where("id = ?", row.ID).Updates(map[string]any{"status": model.TaskStatusQueued, "lease_owner": "", "lease_expires_at": nil}).Error; err != nil {
					t.Fatal(err)
				}
			}
			restarted, err := platformtask.New(repository.New(f.db), platformquota.New(f.db), f.catalog)
			if err != nil {
				t.Fatal(err)
			}
			platformtask.EnableLoopbackForTest(t, restarted, upstream.URL)
			outcome := platformtask.ProcessOneForTest(context.Background(), restarted)
			if outcome.Kind != taskruntime.KindUncertain {
				t.Fatalf("重启没有保持未知：%+v", outcome)
			}
			if _, err := restarted.Recover(context.Background(), f.p, row.ID); err == nil {
				t.Fatal("没有结果仍允许恢复为新请求")
			}
			want := int32(0)
			if stage == "connection-lost" {
				want = 1
			}
			if balance := f.balance(t); posts.Load() != want || balance.ReservedPoints != 10 || balance.SettledPoints != 0 {
				t.Fatalf("未知结果被重发或释放：POST=%d %+v", posts.Load(), balance)
			}
		})
	}
}

func TestTextAdmissionRollsBackWhenTaskInsertFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("任务未入库不应调用上游")
		textResponse(w)
	}))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	if err := f.db.Exec(`CREATE TRIGGER fail_task BEFORE INSERT ON tasks BEGIN SELECT RAISE(FAIL, '模拟任务写入失败'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Create(context.Background(), f.p, f.request("rollback-task")); err == nil {
		t.Fatal("任务写入故障没有返回")
	}
	var intents, receipts int64
	f.db.Model(&model.PlatformExecutionIntent{}).Count(&intents)
	f.db.Model(&model.PlatformRequestReceipt{}).Count(&receipts)
	if balance := f.balance(t); balance.ReservedPoints != 0 || intents != 0 || receipts != 0 {
		t.Fatalf("任务失败留下预留或收据：%+v intents=%d receipts=%d", balance, intents, receipts)
	}
}

func TestRevokedAcceptedTextCanSettleAndDeliver(t *testing.T) {
	var revoke func() error
	var revoked atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := revoke(); err != nil {
			t.Errorf("模拟受理后撤权失败：%v", err)
		} else {
			revoked.Store(true)
		}
		textResponse(w)
	}))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	revoke = func() error {
		_, err := f.catalog.ReplaceModelGrants(context.Background(), "manager", "creator", []string{})
		return err
	}
	created(t, call(f.router, "POST", "/api/tasks", f.request("accepted-revoke")))
	outcome := platformtask.ProcessOneForTest(context.Background(), f.service)
	if outcome.Kind != taskruntime.KindCompleted || outcome.Err != nil || !revoked.Load() {
		t.Fatalf("已受理任务因撤权无法交付：%+v", outcome)
	}
	if balance := f.balance(t); balance.SettledPoints != 10 || balance.ReservedPoints != 0 {
		t.Fatalf("已受理结果没有结算：%+v", balance)
	}
}
