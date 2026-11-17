//go:build platform

package platformimport

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
)

type sourceFixture struct {
	source, destination, workspace string
	db                             *gorm.DB
}

func newSource(t *testing.T) *sourceFixture {
	t.Helper()
	parent := t.TempDir()
	f := &sourceFixture{source: filepath.Join(parent, "local"), destination: filepath.Join(parent, "platform")}
	guard, err := database.PrepareRuntimeDirectory(f.source, identity.ModeLocal)
	if err != nil {
		t.Fatal(err)
	}
	f.db, err = database.Open(database.Config{DataDir: f.source})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ConfigurePool(f.db); err != nil {
		t.Fatal(err)
	}
	if err := guard.CheckDatabase(f.db); err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateLocalSchema(f.db); err != nil {
		t.Fatal(err)
	}
	var workspace model.Workspace
	if err := f.db.Take(&workspace).Error; err != nil {
		t.Fatal(err)
	}
	f.workspace = workspace.ID
	if _, err := localcrypto.Key(f.source); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.close(t) })
	return f
}

func (f *sourceFixture) close(t *testing.T) {
	t.Helper()
	if f.db == nil {
		return
	}
	connection, err := f.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	f.db = nil
}

func (f *sourceFixture) input() Input {
	points, storage := int64(25), int64(4096)
	return Input{Source: f.source, Destination: f.destination, Account: platformauth.CreateAccountInput{Username: "imported", DisplayName: "导入管理员", Password: "test-import-password", InitialPoints: &points, StorageLimitBytes: &storage}}
}

func insert(t *testing.T, db *gorm.DB, row any) {
	t.Helper()
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
}

func encrypt(t *testing.T, dir, value string) string {
	t.Helper()
	value, err := localcrypto.Encrypt(dir, value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func encode(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func addResource(t *testing.T, f *sourceFixture) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(f.source, "resources"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(f.source, "resources", "sample.png"), []byte("image")); err != nil {
		t.Fatal(err)
	}
	insert(t, f.db, &model.Resource{ID: "resource-1", UserID: f.workspace, Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "sample.png", Size: 5, Kind: "image"})
	insert(t, f.db, &model.Project{ID: "project-1", UserID: f.workspace, Name: "保留项目", CoverResourceID: "resource-1"})
}

func fingerprint(t *testing.T, dir string) string {
	t.Helper()
	_, result, err := sourceInventory(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestImportPreservesIdentityAndReencryptsWithoutSourceChanges(t *testing.T) {
	f := newSource(t)
	addResource(t, f)
	key := encrypt(t, f.source, "source-api-secret")
	input := encode(t, map[string]any{"apiKey": key, "prompt": "原提示词", "metadata": map[string]any{"count": json.Number("9007199254740993")}})
	insert(t, f.db, &model.Task{ID: "task-1", UserID: f.workspace, ProjectID: "project-1", Type: "canvas_text", Status: model.TaskStatusSucceeded, InputJSON: input, ResultJSON: `{"text":"已完成文本"}`, WorkflowSourceCipher: encrypt(t, f.source, input)})
	insert(t, f.db, &model.Result{ID: "result-1", UserID: f.workspace, TaskID: "task-1", Kind: "text", Payload: `{"text":"已完成文本"}`})
	insert(t, f.db, &model.ModelChannel{ID: "channel-1", UserID: f.workspace, Scope: "user", Enabled: true, Name: "本地渠道", APIKey: key, BaseURL: "https://example.invalid", HeadersJSON: `[{"key":"Authorization","value":"Bearer header-only-secret"}]`})
	removedLog := strings.Repeat("long-raw-request-secret-to-erase-", 1024)
	if err := f.db.Exec("INSERT INTO api_call_logs(id,user_id,request_body,response_body) VALUES('old-log',?,?,?)", f.workspace, removedLog, removedLog).Error; err != nil {
		t.Fatal(err)
	}
	recipe := encrypt(t, f.source, encode(t, map[string]any{"request": map[string]any{"input": map[string]any{"apiKey": "workflow-recipe-secret", "prompt": "模板文本"}}}))
	insert(t, f.db, &model.CanvasWorkflow{ID: "workflow-1", UserID: f.workspace, Name: "历史模板", DefinitionJSON: `{"version":1}`, RecipeCipher: recipe})
	insert(t, f.db, &model.CanvasWorkflowRevision{TemplateID: "workflow-1", Revision: 1, UserID: f.workspace, Name: "历史模板", DefinitionJSON: `{"version":1}`, RecipeCipher: recipe})
	f.close(t)
	before := fingerprint(t, f.source)
	sourceKey, _ := os.ReadFile(filepath.Join(f.source, ".settings-key"))
	result, err := Import(context.Background(), f.input())
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkspaceID != f.workspace || result.Repeated || result.Fingerprint != before {
		t.Fatalf("导入身份不符: %+v", result)
	}
	if fingerprint(t, f.source) != before {
		t.Fatal("导入改变了源业务文件")
	}
	targetKey, err := os.ReadFile(filepath.Join(f.destination, ".settings-key"))
	if err != nil || bytes.Equal(targetKey, sourceKey) {
		t.Fatal("目标未创建独立主密钥", err)
	}
	db, closeDB, err := openReadOnly(filepath.Join(f.destination, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	var task model.Task
	if err := db.Where("id = ?", "task-1").Take(&task).Error; err != nil {
		t.Fatal(err)
	}
	plain, err := localcrypto.Decrypt(f.destination, task.WorkflowSourceCipher)
	if err != nil || strings.Contains(plain, "apiKey") || !strings.Contains(plain, "9007199254740993") {
		t.Fatalf("配方未正确清理和重加密: %s %v", plain, err)
	}
	if _, err := localcrypto.Decrypt(f.source, task.WorkflowSourceCipher); err == nil {
		t.Fatal("目标密文仍由源密钥解密")
	}
	var channel model.ModelChannel
	if err := db.Where("id = ?", "channel-1").Take(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if channel.Enabled || channel.APIKey != "" || channel.BaseURL != "" || channel.HeadersJSON != "" {
		t.Fatal("旧渠道仍可执行")
	}
	var storage platformauth.StorageAccount
	if err := db.Where("account_id = ?", result.AccountID).Take(&storage).Error; err != nil {
		t.Fatal(err)
	}
	if storage.UsedBytes != 5 || storage.ReservedBytes != 0 {
		t.Fatalf("资源计量错误: %+v", storage)
	}
	var workflow model.CanvasWorkflow
	if err := db.Where("id = ?", "workflow-1").Take(&workflow).Error; err != nil {
		t.Fatal(err)
	}
	workflowPlain, err := localcrypto.Decrypt(f.destination, workflow.RecipeCipher)
	if err != nil || strings.Contains(workflowPlain, "workflow-recipe-secret") || !strings.Contains(workflowPlain, "模板文本") {
		t.Fatal("工作流配方未安全重加密", err)
	}
	closeDB()
	targetBytes, err := os.ReadFile(filepath.Join(f.destination, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"long-raw-request-secret-to-erase-", "header-only-secret", "workflow-recipe-secret", "source-api-secret"} {
		if bytes.Contains(targetBytes, []byte(secret)) {
			t.Fatal("目标数据库物理页仍含被清理凭据")
		}
	}
	repeated, err := Import(context.Background(), f.input())
	if err != nil || !repeated.Repeated || repeated.AccountID != result.AccountID {
		t.Fatalf("重复导入失败: %+v %v", repeated, err)
	}
	db, closeDB, err = openReadOnly(filepath.Join(f.destination, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	for _, table := range []string{"platform_accounts", "platform_usage_ledgers", "platform_import_receipts", "results", "projects"} {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("%s 重复或丢失记录: %d %v", table, count, err)
		}
	}
}

func TestImportCountsSharedStorageObjectOnce(t *testing.T) {
	f := newSource(t)
	addResource(t, f)
	insert(t, f.db, &model.Resource{ID: "resource-2", UserID: f.workspace, Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "sample.png", Size: 5, Kind: "image"})
	insert(t, f.db, &model.Project{ID: "project-2", UserID: f.workspace, Name: "第二项目", CoverResourceID: "resource-2"})
	f.close(t)
	input := f.input()
	*input.Account.StorageLimitBytes = 0
	result, err := Import(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	db, closeDB, err := openReadOnly(filepath.Join(f.destination, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	var storage platformauth.StorageAccount
	if err := db.Where("account_id = ?", result.AccountID).Take(&storage).Error; err != nil {
		t.Fatal(err)
	}
	if storage.UsedBytes != 5 || storage.LimitBytes != 0 {
		t.Fatalf("相同对象重复计费或隐含调整上限: %+v", storage)
	}
	for table, want := range map[string]int64{"platform_storage_objects": 1, "platform_storage_reservations": 2} {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil || count != want {
			t.Fatalf("%s = %d，期望%d: %v", table, count, want, err)
		}
	}
}
