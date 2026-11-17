//go:build platform

package platformquota

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
)

func TestConcurrentAdmissionCannotOverspend(t *testing.T) {
	f := setup(t, 70)
	var wg sync.WaitGroup
	out := make(chan error, 20)
	durations := make(chan time.Duration, 20)
	started := time.Now()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			started := time.Now()
			_, err := f.service.Admit(context.Background(), f.request(fmt.Sprintf("operation-%02d", index)), persistTask)
			durations <- time.Since(started)
			out <- err
		}(i)
	}
	wg.Wait()
	totalTime := time.Since(started)
	close(out)
	close(durations)
	measured := make([]time.Duration, 0, 20)
	for duration := range durations {
		measured = append(measured, duration)
	}
	slices.Sort(measured)
	t.Logf("20个并发准入：总耗时=%s，单请求P50=%s，P95=%s，最大=%s", totalTime, measured[9], measured[18], measured[19])
	success := 0
	for err := range out {
		if err == nil {
			success++
		} else {
			requireReason(t, err, "quota_exceeded")
		}
	}
	if success != 7 {
		t.Fatalf("预期只允许7个，实际%d", success)
	}
	balance := f.balance(t)
	if balance.ReservedPoints != 70 || balance.SettledPoints != 0 {
		t.Fatalf("错误余额：%+v", balance)
	}
	for _, table := range []string{"tasks", "platform_execution_intents", "platform_request_receipts"} {
		var n int64
		if err := f.db.Table(table).Count(&n).Error; err != nil || n != 7 {
			t.Fatalf("%s 数量=%d err=%v", table, n, err)
		}
	}
}

func TestAdmissionRollbackAndIdempotency(t *testing.T) {
	f := setup(t, 100)
	input := f.request("operation-replay")
	_, err := f.service.Admit(context.Background(), input, func(tx *gorm.DB, s *modelcatalog.PlatformAdmissionSelection, id string) (*model.Task, error) {
		if _, err := persistTask(tx, s, id); err != nil {
			return nil, err
		}
		return nil, errors.New("任务提交后故障")
	})
	if err == nil {
		t.Fatal("故障未传出")
	}
	if f.balance(t).ReservedPoints != 0 {
		t.Fatal("失败仍保留预留")
	}
	var count int64
	f.db.Model(&model.Task{}).Count(&count)
	if count != 0 {
		t.Fatal("任务没有回滚")
	}
	first, err := f.service.Admit(context.Background(), input, persistTask)
	if err != nil {
		t.Fatal(err)
	}
	// 重放读取原收据，不受价格变化影响，也不重新执行持久化回调。
	if _, err := f.catalog.PublishPrice(context.Background(), "manager", f.modelID, "text_generation", 20); err != nil {
		t.Fatal(err)
	}
	again, err := New(f.db).Admit(context.Background(), input, func(*gorm.DB, *modelcatalog.PlatformAdmissionSelection, string) (*model.Task, error) {
		t.Fatal("重放执行了持久化")
		return nil, nil
	})
	if err != nil || !again.Replayed || again.Task.ID != first.Task.ID || again.Intent.Points != 10 {
		t.Fatalf("重放错误：%+v %v", again, err)
	}
	input.RequestDigest = Digest([]byte("changed"))
	_, err = f.service.Admit(context.Background(), input, persistTask)
	requireReason(t, err, "idempotency_conflict")
	input.RequestKey = ""
	_, err = f.service.Admit(context.Background(), input, persistTask)
	requireReason(t, err, "idempotency_required")
	if f.balance(t).ReservedPoints != 10 {
		t.Fatal("重放改变了预留")
	}
}

func TestConcurrentIdenticalRequestReturnsOneTask(t *testing.T) {
	f := setup(t, 100)
	input := f.request("same-operation")
	var wg sync.WaitGroup
	results := make(chan *Admitted, 20)
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := f.service.Admit(context.Background(), input, persistTask)
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]bool{}
	for result := range results {
		ids[result.Task.ID] = true
	}
	if len(ids) != 1 || f.balance(t).ReservedPoints != 10 {
		t.Fatal("同键并发重复创建")
	}
}
