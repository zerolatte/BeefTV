//go:build platform

package platformquota

import (
	"context"
	"testing"

	"infinite-canvas/backend/internal/platformauth"
)

func TestAdjustmentAuthorizationBoundsAndReplay(t *testing.T) {
	f := setup(t, 100)
	input := AdjustmentInput{ClientOperationID: "adjust-operation", Points: 20, Reason: "额外发放"}
	if _, err := f.service.Adjust(context.Background(), "creator", "creator", input); err == nil {
		t.Fatal("普通账号可调额")
	}
	first, err := f.service.Adjust(context.Background(), "manager", "creator", input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.service.Adjust(context.Background(), "manager", "creator", input)
	if err != nil || first.ID != again.ID || f.balance(t).GrantedPoints != 120 {
		t.Fatal("调额重放重复发放")
	}
	input.Points = 30
	_, err = f.service.Adjust(context.Background(), "manager", "creator", input)
	requireReason(t, err, "idempotency_conflict")
	if _, err := f.service.Admit(context.Background(), f.request("held-operation"), persistTask); err != nil {
		t.Fatal(err)
	}
	input = AdjustmentInput{ClientOperationID: "reduce-operation", Points: -111, Reason: "扣减剩余额度"}
	_, err = f.service.Adjust(context.Background(), "manager", "creator", input)
	requireReason(t, err, "quota_adjustment_conflict")
	input.Points = -110
	if _, err := f.service.Adjust(context.Background(), "manager", "creator", input); err != nil {
		t.Fatal(err)
	}
	if balance := f.balance(t); balance.GrantedPoints != 10 || balance.ReservedPoints != 10 {
		t.Fatal("调减未保留在途占用")
	}
	input = AdjustmentInput{ClientOperationID: "overflow-operation", Points: platformauth.MaxPoints, Reason: "越界"}
	_, err = f.service.Adjust(context.Background(), "manager", "creator", input)
	requireReason(t, err, "quota_adjustment_conflict")
}

func TestAdjustmentAuditFailureRollsBack(t *testing.T) {
	f := setup(t, 100)
	if err := f.db.Exec("CREATE TRIGGER reject_quota_audit BEFORE INSERT ON platform_audits WHEN NEW.action = 'points_adjusted' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Adjust(context.Background(), "manager", "creator", AdjustmentInput{ClientOperationID: "rollback-adjustment", Points: 10, Reason: "审计故障"})
	if err == nil || f.balance(t).GrantedPoints != 100 {
		t.Fatal("审计失败没有回滚")
	}
	var count int64
	f.db.Table("platform_point_adjustments").Count(&count)
	if count != 0 {
		t.Fatal("失败仍留下收据")
	}
}
