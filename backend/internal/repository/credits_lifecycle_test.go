package repository

import (
	"infinite-canvas/backend/internal/model"
	"testing"
	"time"
)

func TestCreditRecoveryAndSettlementExcludeEachOther(t *testing.T) {
	repo := creditRepository(t)
	if err := admitCredit(repo, "recover-task", "recover-operation"); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Model(&model.Task{}).Where("id = ?", "recover-task").Update("status", model.TaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	rows, _, err := repo.CreditUsagePage("user", "", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ClaimFailedTaskProviderRecovery("recover-task", "user", "manual-recovery:test", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := repo.SettleCredits(rows[0].ID, 0, "", "确认无产出"); err == nil {
		t.Fatal("结果取回期间允许结算")
	}
	if err := repo.ReleaseTaskProviderRecovery("recover-task", "manual-recovery:test"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SettleCredits(rows[0].ID, 0, "", "确认无产出"); err != nil {
		t.Fatal(err)
	}
	if err := repo.ClaimFailedTaskProviderRecovery("recover-task", "user", "manual-recovery:again", time.Minute); err == nil {
		t.Fatal("已退款任务重新取得上游恢复租约")
	}
	if err := repo.SettleCredits(rows[0].ID, 1, "", "不同结论"); err == nil {
		t.Fatal("接受冲突结算")
	}
}

func TestCreditWorkerCannotClaimDisabledAccount(t *testing.T) {
	repo := creditRepository(t)
	if err := admitCredit(repo, "disabled-task", "disabled-operation"); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Model(&model.PlatformAccount{}).Where("id = ?", "user").Update("status", model.UserStatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimNextTask("worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed != nil {
		t.Fatal("领取了停用账号的任务")
	}
	if err := repo.db.Model(&model.Task{}).Where("id = ?", "disabled-task").Updates(map[string]any{"status": model.TaskStatusRunning, "provider_request_id": "accepted-request"}).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.ClaimNextTask("worker", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("停用后不能查询已提交任务: %v", err)
	}
}

func TestCreditCompletedLeaseIsReleasedByItsOwner(t *testing.T) {
	repo := creditRepository(t)
	if err := admitCredit(repo, "completed-task", "completed-operation"); err != nil {
		t.Fatal(err)
	}
	row, err := repo.ClaimNextTask("worker-current", time.Minute)
	if err != nil || row == nil {
		t.Fatalf("领取失败: %v", err)
	}
	row.Status = model.TaskStatusSucceeded
	if err := repo.SaveTaskCompletion(row, model.TaskStatusRunning, nil); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.Task(row.ID)
	if err != nil || completed.LeaseOwner != "" || completed.LeaseExpiresAt != nil {
		t.Fatalf("结果入库未释放租约: %v", err)
	}
	if err := repo.db.Model(&model.Task{}).Where("id = ?", row.ID).Updates(map[string]any{"lease_owner": "worker-current", "lease_expires_at": time.Now().Add(time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.ReleaseTaskLease(row.ID, "worker-old"); err != nil {
		t.Fatal(err)
	}
	current, err := repo.Task(row.ID)
	if err != nil || current.LeaseOwner != "worker-current" {
		t.Fatalf("旧执行者清除了新租约: %v", err)
	}
	if err := repo.ReleaseTaskLease(row.ID, "worker-current"); err != nil {
		t.Fatal(err)
	}
	current, err = repo.Task(row.ID)
	if err != nil || current.LeaseOwner != "" || current.LeaseExpiresAt != nil {
		t.Fatalf("成功任务租约未清理: %v", err)
	}
}
