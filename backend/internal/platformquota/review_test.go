//go:build platform

package platformquota

import (
	"context"
	"testing"
	"time"

	"infinite-canvas/backend/internal/platformauth"
)

func TestManualReviewPreservesHistoryAndReconciles(t *testing.T) {
	f := setup(t, 100)
	ctx := context.Background()
	admitted, err := f.service.Admit(ctx, f.request("review-operation"), persistTask)
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := f.service.BeginSubmission(ctx, "workspace-creator", admitted.Intent.ID); err != nil || !allowed {
		t.Fatal(err)
	}
	input := ReviewInput{ClientOperationID: "manual-review-operation", ExpectedState: StateSubmitting, Fact: FactFailedConsumed, Reason: "模拟上游已确认完全失败且消耗"}
	_, err = f.service.Review(ctx, "manager", admitted.Intent.ID, input)
	requireReason(t, err, "review_not_due")
	f.service.now = func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }
	first, err := f.service.Review(ctx, "manager", admitted.Intent.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.service.Review(ctx, "manager", admitted.Intent.ID, input)
	if err != nil || first.ID != again.ID {
		t.Fatal("人工核对重放失败")
	}
	input.ClientOperationID = "another-review-operation"
	_, err = f.service.Review(ctx, "manager", admitted.Intent.ID, input)
	requireReason(t, err, "settlement_conflict")
	result, err := f.service.Reconcile(ctx, "manager", "creator")
	if err != nil || !result.Consistent || result.Granted != "100" || result.Settled != "0" || result.Reserved != "0" || result.LedgerEntries != 4 {
		t.Fatalf("失败补偿账本不平 %+v %v", result, err)
	}
	if err := f.db.Model(&platformauth.QuotaAccount{}).Where("account_id = ?", "creator").Update("granted_points", 101).Error; err != nil {
		t.Fatal(err)
	}
	result, err = f.service.Reconcile(ctx, "manager", "creator")
	if err != nil || result.Consistent {
		t.Fatal("未报告计数差异")
	}
	if f.balance(t).GrantedPoints != 101 {
		t.Fatal("核对不应自动掩盖差异")
	}
}

func TestManualReviewAuditFailureRollsBackSettlement(t *testing.T) {
	f := setup(t, 100)
	ctx := context.Background()
	admitted, err := f.service.Admit(ctx, f.request("review-rollback"), persistTask)
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := f.service.BeginSubmission(ctx, "workspace-creator", admitted.Intent.ID); err != nil || !allowed {
		t.Fatal(err)
	}
	f.service.now = func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }
	if err := f.db.Exec("CREATE TRIGGER reject_review_audit BEFORE INSERT ON platform_audits WHEN NEW.action = 'execution_reviewed' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Review(ctx, "manager", admitted.Intent.ID, ReviewInput{ClientOperationID: "review-audit-failure", ExpectedState: StateSubmitting, Fact: FactSucceeded, Reason: "模拟成功证据"})
	if err == nil {
		t.Fatal("审计故障未传出")
	}
	if balance := f.balance(t); balance.ReservedPoints != 10 || balance.SettledPoints != 0 {
		t.Fatal("人工核对未全部回滚")
	}
	result, err := f.service.Reconcile(ctx, "manager", "creator")
	if err != nil || !result.Consistent {
		t.Fatalf("故障后账本不平 %+v %v", result, err)
	}
}
