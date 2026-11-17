//go:build platform

package platformquota

import (
	"context"
	"sync"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
)

func TestSettlementIsOnceAndFailureCompensates(t *testing.T) {
	for _, fact := range []ExecutionFact{FactSucceeded, FactFailedConsumed, FactFailedNoCost} {
		t.Run(string(fact), func(t *testing.T) {
			f := setup(t, 100)
			admitted, err := f.service.Admit(context.Background(), f.request("settle-operation"), persistTask)
			if err != nil {
				t.Fatal(err)
			}
			allowed, err := f.service.BeginSubmission(context.Background(), "workspace-creator", admitted.Intent.ID)
			if err != nil || !allowed {
				t.Fatalf("不能提交 %v", err)
			}
			var wg sync.WaitGroup
			errs := make(chan error, 20)
			for range 20 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs <- f.service.Observe(context.Background(), "workspace-creator", admitted.Intent.ID, fact)
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			balance := f.balance(t)
			expected := int64(0)
			if fact == FactSucceeded {
				expected = 10
			}
			if balance.ReservedPoints != 0 || balance.SettledPoints != expected || balance.GrantedPoints != 100 {
				t.Fatalf("结算错误 %+v", balance)
			}
			var entries []platformauth.UsageLedger
			if err := f.db.Where("account_id = ?", "creator").Find(&entries).Error; err != nil {
				t.Fatal(err)
			}
			expectedCount := 3
			if fact == FactFailedConsumed {
				expectedCount = 4
			}
			if len(entries) != expectedCount {
				t.Fatalf("重复流水 %d", len(entries))
			}
			// 与既有终局冲突不能改写历史或再次扣减。
			other := FactSucceeded
			if fact == FactSucceeded {
				other = FactFailedConsumed
			}
			requireReason(t, f.service.Observe(context.Background(), "workspace-creator", admitted.Intent.ID, other), "settlement_conflict")
		})
	}
}

func TestUnknownRetainedOnRestartAndOverdue(t *testing.T) {
	f := setup(t, 100)
	admitted, err := f.service.Admit(context.Background(), f.request("unknown-operation"), persistTask)
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := f.service.BeginSubmission(context.Background(), "workspace-creator", admitted.Intent.ID)
	if err != nil || !allowed {
		t.Fatal(err)
	}
	restarted := New(f.db)
	allowed, err = restarted.BeginSubmission(context.Background(), "workspace-creator", admitted.Intent.ID)
	if err != nil || allowed {
		t.Fatal("重启重复提交")
	}
	if err := restarted.Observe(context.Background(), "workspace-creator", admitted.Intent.ID, FactUnknown); err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }
	n, err := restarted.MarkOverdue(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("未转人工 %d %v", n, err)
	}
	if err := restarted.CancelUnsubmitted(context.Background(), "workspace-creator", admitted.Intent.ID); err != nil {
		t.Fatal(err)
	}
	if f.balance(t).ReservedPoints != 10 {
		t.Fatal("未知状态被释放")
	}
	if err := restarted.Observe(context.Background(), "workspace-creator", admitted.Intent.ID, FactSucceeded); err != nil {
		t.Fatal(err)
	}
	var intent model.PlatformExecutionIntent
	f.db.First(&intent, "id = ?", admitted.Intent.ID)
	if intent.ReviewRequired || intent.State != StateSettled {
		t.Fatal("迟到成功未终结")
	}
}

func TestRevocationBeforeAndAfterSubmission(t *testing.T) {
	for _, submitted := range []bool{false, true} {
		t.Run(map[bool]string{false: "提交前", true: "受理后"}[submitted], func(t *testing.T) {
			f := setup(t, 100)
			admitted, err := f.service.Admit(context.Background(), f.request("revoke-operation"), persistTask)
			if err != nil {
				t.Fatal(err)
			}
			if submitted {
				allowed, err := f.service.BeginSubmission(context.Background(), "workspace-creator", admitted.Intent.ID)
				if err != nil || !allowed {
					t.Fatal(err)
				}
			}
			if _, err := f.catalog.ReplaceModelGrants(context.Background(), "manager", "creator", []string{}); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&platformauth.Account{}).Where("id = ?", "creator").Update("status", platformauth.StatusDisabled).Error; err != nil {
				t.Fatal(err)
			}
			if submitted {
				if err := f.service.Observe(context.Background(), "workspace-creator", admitted.Intent.ID, FactSucceeded); err != nil {
					t.Fatal(err)
				}
			} else {
				allowed, err := f.service.BeginSubmission(context.Background(), "workspace-creator", admitted.Intent.ID)
				if err != nil || allowed {
					t.Fatal("撤权后仍然提交")
				}
			}
			balance := f.balance(t)
			expected := int64(0)
			if submitted {
				expected = 10
			}
			if balance.ReservedPoints != 0 || balance.SettledPoints != expected {
				t.Fatalf("撤权错误 %+v", balance)
			}
			requireReason(t, f.service.Observe(context.Background(), "workspace-manager", admitted.Intent.ID, FactSucceeded), "not_found")
		})
	}
}
