//go:build platform

package platformbackup

import (
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/platformquota"
)

func TestRestoreFencesTasksSubmittedAfterSnapshot(t *testing.T) {
	source, db := fixture(t)
	row := model.Task{ID: "restore-task", UserID: "restore-workspace", Type: "canvas_text", Status: model.TaskStatusQueued}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	intent := model.PlatformExecutionIntent{ID: "restore-intent", AccountID: "restore-account", WorkspaceID: row.UserID, TaskID: row.ID, Points: 10, State: platformquota.StateReserved}
	if err := db.Create(&intent).Error; err != nil {
		t.Fatal(err)
	}
	account := platformauth.QuotaAccount{AccountID: intent.AccountID, GrantedPoints: 20, ReservedPoints: 10}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot")
	if _, err := Create(t.Context(), source, backup); err != nil {
		t.Fatal(err)
	}
	// 模拟快照之后源实例已经发送并完成，恢复库不能因旧 reserved 再次提交。
	if err := db.Model(&intent).Update("state", platformquota.StateSettled).Error; err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored")
	if err := Restore(t.Context(), backup, restored); err != nil {
		t.Fatal(err)
	}
	copyDB, closeDB, err := openDatabase(filepath.Join(restored, databaseName), true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	var copied model.PlatformExecutionIntent
	if err := copyDB.First(&copied, "id=?", intent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if copied.State != platformquota.StateUnknown || copied.SubmittedAt == nil {
		t.Fatal("恢复旧预留未建立提交围栏")
	}
	if err := copyDB.First(&row, "id=?", row.ID).Error; err != nil || row.Status != model.TaskStatusFailed {
		t.Fatal("恢复任务仍可被自动领取", err)
	}
	if err := copyDB.First(&account, "account_id=?", account.AccountID).Error; err != nil || account.ReservedPoints != 10 || account.SettledPoints != 0 {
		t.Fatal("恢复擅自释放或结算点数", err)
	}
}
