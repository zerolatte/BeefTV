//go:build platform

package repository_test

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/repository"
)

func TestPlatformTaskClaimRequiresMatchingIntentAndWorkspaceBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = connection.Close() })
	if err := db.AutoMigrate(&model.Task{}, &model.PlatformExecutionIntent{}, &platformauth.AccountWorkspace{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&platformauth.AccountWorkspace{AccountID: "account", WorkspaceID: "workspace"}).Error; err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"legacy", "foreign-intent", "unbound-intent", "admitted"} {
		row := model.Task{ID: id, UserID: "workspace", Type: "canvas_text", Status: model.TaskStatusQueued, CreatedAt: time.Now().Add(time.Duration(index-10) * time.Minute)}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if id == "legacy" {
			continue
		}
		intent := model.PlatformExecutionIntent{ID: "intent-" + id, TaskID: id, AccountID: "account", WorkspaceID: "workspace", Points: 10, State: "reserved"}
		if id == "foreign-intent" {
			intent.WorkspaceID = "other-workspace"
		}
		if id == "unbound-intent" {
			intent.AccountID = "other-account"
		}
		if err := db.Create(&intent).Error; err != nil {
			t.Fatal(err)
		}
	}
	repo := repository.New(db)
	claimed, err := repo.ClaimNextPlatformTask("platform-worker", time.Minute)
	if err != nil || claimed == nil || claimed.ID != "admitted" || claimed.Attempts != 1 {
		t.Fatalf("平台没有领取唯一有效任务：%+v %v", claimed, err)
	}
	if next, err := repo.ClaimNextPlatformTask("another-worker", time.Minute); err != nil || next != nil {
		t.Fatalf("平台领取了历史任务、错误归属或有效租约任务：%+v %v", next, err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", claimed.ID).Update("lease_expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repo.ClaimNextPlatformTask("replacement-worker", time.Minute)
	if err != nil || reclaimed == nil || reclaimed.ID != claimed.ID || reclaimed.Attempts != 2 {
		t.Fatalf("平台不能恢复过期租约：%+v %v", reclaimed, err)
	}
	for _, id := range []string{"legacy", "foreign-intent", "unbound-intent"} {
		row, err := repo.Task(id)
		if err != nil || row.Status != model.TaskStatusQueued || row.Attempts != 0 || row.LeaseOwner != "" {
			t.Fatalf("排除任务被修改：%s %+v %v", id, row, err)
		}
	}
	local, err := repo.ClaimNextTask("local-worker", time.Minute)
	if err != nil || local == nil || local.ID != "legacy" {
		t.Fatalf("本地领取合同受到平台范围影响：%+v %v", local, err)
	}
}
