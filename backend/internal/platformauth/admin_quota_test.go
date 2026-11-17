//go:build platform

package platformauth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"infinite-canvas/backend/internal/kernel"
)

func requireQuotaStatus(t *testing.T, err error, status int) {
	t.Helper()
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) || appErr.Status != status {
		t.Fatalf("期望状态 %d，实际 %v", status, err)
	}
}

func TestAdminQuotaReadAndStorageLimitPreserveUsage(t *testing.T) {
	s := testService(t)
	admin := bootstrapForTest(t, s)
	ctx := context.Background()
	user, err := s.CreateAccount(ctx, admin.ID, testCreateInput("quota_user"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&QuotaAccount{}).Where("account_id = ?", user.ID).Updates(map[string]any{"settled_points": 20, "reserved_points": 30}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&StorageAccount{}).Where("account_id = ?", user.ID).Updates(map[string]any{"used_bytes": 800, "reserved_bytes": 100}).Error; err != nil {
		t.Fatal(err)
	}
	quota, err := s.AccountQuota(ctx, admin.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if quota.Points.Available != 50 || quota.Storage.UsedBytes != 800 || quota.Storage.ReservedBytes != 100 {
		t.Fatalf("额度汇总错误: %+v", quota)
	}
	quota, err = s.UpdateStorageLimit(ctx, admin.ID, user.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if quota.Storage.LimitBytes != 0 || quota.Storage.UsedBytes != 800 || quota.Storage.ReservedBytes != 100 || quota.Points.Available != 50 {
		t.Fatalf("调低上限修改了已用或预留: %+v", quota)
	}
	logs, total, err := s.AuditLogs(ctx, admin.ID, user.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(logs) != 2 || logs[0].Action != "storage_limit_updated" {
		t.Fatalf("缺少用户范围的追加审计: %+v", logs)
	}
	var difference map[string]map[string]int64
	if err := json.Unmarshal([]byte(logs[0].Difference), &difference); err != nil {
		t.Fatal(err)
	}
	if difference["storageLimitBytes"]["from"] != 1024*1024 || difference["storageLimitBytes"]["to"] != 0 {
		t.Fatal("存储审计差异错误")
	}
	for _, action := range []func() error{
		func() error { _, err := s.AccountQuota(ctx, user.ID, admin.ID); return err },
		func() error { _, err := s.UpdateStorageLimit(ctx, user.ID, user.ID, 100); return err },
		func() error { _, _, err := s.AuditLogs(ctx, user.ID, "", 0, 10); return err },
	} {
		requireQuotaStatus(t, action(), 403)
	}
	for _, limit := range []int64{-1, MaxPoints + 1} {
		_, err := s.UpdateStorageLimit(ctx, admin.ID, user.ID, limit)
		requireQuotaStatus(t, err, 400)
	}
	_, err = s.AccountQuota(ctx, admin.ID, "missing")
	requireQuotaStatus(t, err, 404)
}

func TestStorageQuotaAuditFailureRollsBackAndDeletionRetainsLedger(t *testing.T) {
	s := testService(t)
	admin := bootstrapForTest(t, s)
	ctx := context.Background()
	user, err := s.CreateAccount(ctx, admin.ID, testCreateInput("audit_user"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Exec("CREATE TRIGGER reject_storage_audit BEFORE INSERT ON platform_audits WHEN NEW.action = 'storage_limit_updated' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateStorageLimit(ctx, admin.ID, user.ID, 1); err == nil {
		t.Fatal("审计失败不得提交存储额度")
	}
	quota, err := s.AccountQuota(ctx, admin.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if quota.Storage.LimitBytes != 1024*1024 {
		t.Fatal("审计失败未回滚上限")
	}
	deleted := StatusDeleted
	if _, err := s.UpdateAccount(ctx, admin.ID, user.ID, UpdateAccountInput{Status: &deleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AccountQuota(ctx, admin.ID, user.ID); err != nil {
		t.Fatal("注销账号的历史额度仍须可审计", err)
	}
	_, err = s.UpdateStorageLimit(ctx, admin.ID, user.ID, 100)
	requireQuotaStatus(t, err, 404)
	var ledgerCount int64
	if err := s.db.Model(&UsageLedger{}).Where("account_id = ?", user.ID).Count(&ledgerCount).Error; err != nil {
		t.Fatal(err)
	}
	if ledgerCount != 1 {
		t.Fatal("注销删除了初始发放流水")
	}
}

func TestQuotaReadRejectsCorruptAccountingAndInvalidAuditPagination(t *testing.T) {
	s := testService(t)
	admin := bootstrapForTest(t, s)
	ctx := context.Background()
	if err := s.db.Model(&QuotaAccount{}).Where("account_id = ?", admin.ID).Update("reserved_points", 101).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.AccountQuota(ctx, admin.ID, admin.ID); err == nil {
		t.Fatal("不平衡账户不能返回伪造可用余额")
	}
	for _, page := range [][2]int{{-1, 10}, {10000001, 10}, {0, 0}, {0, 101}} {
		_, _, err := s.AuditLogs(ctx, admin.ID, "", page[0], page[1])
		requireQuotaStatus(t, err, 400)
	}
}

func TestPendingPasswordChangeDoesNotCountAsAnEffectiveAdministrator(t *testing.T) {
	s := testService(t)
	admin := bootstrapForTest(t, s)
	ctx := context.Background()
	input := testCreateInput("pending_admin")
	input.Role = RoleAdmin
	pending, err := s.CreateAccount(ctx, admin.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	role := RoleUser
	_, err = s.UpdateAccount(ctx, admin.ID, admin.ID, UpdateAccountInput{Role: &role})
	requireReason(t, err, "last_admin")
	if _, err := s.UpdateAccount(ctx, admin.ID, pending.ID, UpdateAccountInput{Role: &role}); err != nil {
		t.Fatal("未完成改密的备用管理员可以撤回角色", err)
	}
}
