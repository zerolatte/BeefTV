//go:build platform

package platformbackup

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

func TestRestoreFencesWorkflowContinuationAndPreservesHistory(t *testing.T) {
	source, db := fixture(t)
	const original = `{"steps":[{"nodeId":"done","status":"succeeded","text":"已保存文本"},{"nodeId":"next","status":"pending"}],"outputs":[{"nodeId":"done","text":"已保存文本"}],"retryRequests":{"original-retry":{"applied":true}},"largeNumber":9007199254740993}`
	for _, status := range []string{"queued", "running", "paused", "succeeded", "failed", "cancelled"} {
		row := model.CanvasWorkflowRun{ID: status, UserID: "workspace", ClientOperationID: status, Status: status, StateJSON: original}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	backup, target := filepath.Join(t.TempDir(), "snapshot"), filepath.Join(t.TempDir(), "restored")
	if _, err := Create(t.Context(), source, backup); err != nil {
		t.Fatal(err)
	}
	if err := Restore(t.Context(), backup, target); err != nil {
		t.Fatal(err)
	}
	copyDB, closeDB, err := openDatabase(filepath.Join(target, databaseName), true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	var expected map[string]json.RawMessage
	if err := json.Unmarshal([]byte(original), &expected); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"queued", "running", "paused", "succeeded", "failed", "cancelled"} {
		var row model.CanvasWorkflowRun
		if err := copyDB.First(&row, "id = ?", status).Error; err != nil {
			t.Fatal(err)
		}
		active := status == "queued" || status == "running" || status == "paused"
		if !active {
			if row.Status != status || row.StateJSON != original {
				t.Fatal("恢复修改了终态工作流历史")
			}
			continue
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal([]byte(row.StateJSON), &got); err != nil {
			t.Fatal(err)
		}
		var reason string
		if err := json.Unmarshal(got["reason"], &reason); err != nil || reason != string(kernel.ReasonBackupRecoveryRequired) || row.Status != "failed" {
			t.Fatal("恢复工作流仍可继续准入", err)
		}
		for key, value := range expected {
			if string(value) != string(got[key]) {
				t.Fatalf("恢复丢失步骤、结果、重试收据或数字精度：%s", key)
			}
		}
		var sourceRow model.CanvasWorkflowRun
		if err := db.First(&sourceRow, "id = ?", status).Error; err != nil || sourceRow.Status != status || sourceRow.StateJSON != original {
			t.Fatal("恢复改写了源运行", err)
		}
	}
}
