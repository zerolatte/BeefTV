//go:build platform

package platformauth

import (
	"context"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestImportAccountRollsBackAllRootsAndRetainsWorkspace(t *testing.T) {
	s := testService(t)
	if err := s.db.Create(&model.Workspace{ID: "source-workspace", Name: "原工作区"}).Error; err != nil {
		t.Fatal(err)
	}
	input := ImportAccountInput{CreateAccountInput: testCreateInput("imported"), WorkspaceID: "source-workspace", SourceFingerprint: strings.Repeat("a", 64)}
	if err := s.db.Exec("CREATE TRIGGER reject_import_ledger BEFORE INSERT ON platform_usage_ledgers BEGIN SELECT RAISE(ABORT,'injected'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportAccount(context.Background(), input); err == nil {
		t.Fatal("故障没有触发事务回滚")
	}
	for _, table := range []string{"platform_accounts", "platform_credentials", "platform_account_workspaces", "platform_bootstrap_markers", "platform_quota_accounts", "platform_storage_accounts", "platform_usage_ledgers", "platform_import_receipts"} {
		var count int64
		if err := s.db.Table(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s 回滚不完整: %d %v", table, count, err)
		}
	}
	if err := s.db.Exec("DROP TRIGGER reject_import_ledger").Error; err != nil {
		t.Fatal(err)
	}
	account, err := s.ImportAccount(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if account.WorkspaceID != input.WorkspaceID || account.Role != RoleAdmin {
		t.Fatalf("导入账号绑定错误: %+v", account)
	}
	if _, err := s.ImportAccount(context.Background(), input); err == nil {
		t.Fatal("允许重复导入开户")
	}
	var count int64
	if err := s.db.Model(&model.Workspace{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("导入创建了额外工作区", count, err)
	}
}
