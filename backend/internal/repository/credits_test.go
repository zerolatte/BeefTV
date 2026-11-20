package repository

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
)

func creditRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "credits.db")+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ConfigurePool(db); err != nil {
		t.Fatal(err)
	}
	if err := database.MigratePlatformSchema(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	for _, row := range []any{
		&model.PlatformAccount{User: model.User{ID: "user", Role: model.UserRoleUser, Status: model.UserStatusActive}, WorkspaceID: "workspace", LoginName: "user", AuthVersion: 1},
		&model.CreditAccount{UserID: "user", Available: 10},
		&model.ModelChannel{ID: "channel", Scope: model.ChannelScopeSystem, Enabled: true},
		&model.ChannelModel{ID: "model", ChannelID: "channel", ModelKey: "example", Enabled: true},
		&model.ModelCreditPrice{ID: "price", ChannelModelID: "model", VariantID: "", UnitCredits: 4, Version: 1, Published: true},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return NewPlatform(db)
}

func admitCredit(repo *Repository, id, operation string) error {
	q := &model.CreditQuote{ID: "quote-" + id, UserID: "user", ModelID: "model", PriceID: "price", PriceVersion: 1, RequestHash: "hash", UnitCredits: 4, Units: 1, TotalCredits: 4, ExpiresAt: time.Now().Add(time.Minute)}
	m, err := repo.ChannelModel("model")
	if err != nil {
		return err
	}
	ch, err := repo.SystemChannel("channel")
	if err != nil {
		return err
	}
	q.ModelUpdatedAt, q.ChannelUpdatedAt = m.UpdatedAt, ch.UpdatedAt
	if err := repo.CreateCreditQuote(q); err != nil {
		return err
	}
	task := &model.Task{ID: id, UserID: "user", Type: "canvas_text", Status: model.TaskStatusQueued, ClientOperationID: &operation, ClientOperationHash: "hash", InputJSON: "{}"}
	return repo.AdmitCreditTask(task, 100, q.ID, "hash")
}

func TestCreditConcurrentAdmissionCannotOverspend(t *testing.T) {
	repo := creditRepository(t)
	var success atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if admitCredit(repo, fmt.Sprint(i), fmt.Sprint("op-", i)) == nil {
				success.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if success.Load() != 2 {
		t.Fatalf("成功任务数量=%d，期望 2", success.Load())
	}
	balance, err := repo.CreditAccount("user")
	if err != nil {
		t.Fatal(err)
	}
	if balance.Available != 2 || balance.Frozen != 8 {
		t.Fatalf("并发余额错误: %+v", balance)
	}
}

func TestCreditReplayAndSettlementAreIdempotent(t *testing.T) {
	repo := creditRepository(t)
	if err := admitCredit(repo, "task-one", "same-operation"); err != nil {
		t.Fatal(err)
	}
	err := admitCredit(repo, "task-two", "same-operation")
	var replay *ClientOperationReplay
	if !errors.As(err, &replay) || replay.Task.ID != "task-one" {
		t.Fatalf("未回读原任务: %v", err)
	}
	rows, _, err := repo.CreditUsagePage("user", "", 0, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("重复预占: %+v %v", rows, err)
	}
	var wg sync.WaitGroup
	if err := repo.db.Model(&model.Task{}).Where("id = ?", "task-one").Update("status", model.TaskStatusCancelled).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := repo.SettleCredits(rows[0].ID, 0, "", "确认无产出"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	balance, _ := repo.CreditAccount("user")
	if balance.Available != 10 || balance.Frozen != 0 {
		t.Fatalf("重复退款: %+v", balance)
	}
	ledger, _, err := repo.CreditLedgerPage("user", 0, 100)
	if err != nil || len(ledger) != 3 {
		t.Fatalf("流水应仅有冻结/结算/退款: %+v %v", ledger, err)
	}
}

func TestCreditAdmissionRollbackAndQuoteGuards(t *testing.T) {
	repo := creditRepository(t)
	q := &model.CreditQuote{ID: "quote", UserID: "user", ModelID: "model", PriceID: "price", PriceVersion: 1, RequestHash: "hash", UnitCredits: 4, Units: 1, TotalCredits: 4, ExpiresAt: time.Now().Add(time.Minute)}
	m, _ := repo.ChannelModel("model")
	ch, _ := repo.SystemChannel("channel")
	q.ModelUpdatedAt, q.ChannelUpdatedAt = m.UpdatedAt, ch.UpdatedAt
	if err := repo.CreateCreditQuote(q); err != nil {
		t.Fatal(err)
	}
	operation := "guard-operation"
	row := &model.Task{ID: "task", UserID: "user", Type: "canvas_text", Status: model.TaskStatusQueued, ClientOperationID: &operation, ClientOperationHash: "hash", InputJSON: "{}"}
	if err := repo.AdmitCreditTask(row, 100, q.ID, "forged"); err == nil {
		t.Fatal("接受了篡改报价")
	}
	if err := repo.AdmitCreditTask(row, 100, q.ID, "hash", func(*Repository) error { return errors.New("测试存储失败") }); err == nil {
		t.Fatal("没有返回存储失败")
	}
	balance, _ := repo.CreditAccount("user")
	if balance.Available != 10 || balance.Frozen != 0 {
		t.Fatalf("事务失败仍冻结余额: %+v", balance)
	}
	if err := repo.db.Model(&model.ModelCreditPrice{}).Where("id = ?", "price").Update("version", 2).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.AdmitCreditTask(row, 100, q.ID, "hash"); err == nil {
		t.Fatal("接受了过时价格")
	}
}
