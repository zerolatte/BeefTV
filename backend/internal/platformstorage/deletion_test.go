//go:build platform

package platformstorage_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformstorage"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

type failingDeleteStore struct {
	asset.BlobStore
	fail    bool
	failKey string
}

func (s *failingDeleteStore) Delete(key string) error {
	if s.fail || key == s.failKey {
		return errors.New("模拟物理删除失败")
	}
	return s.BlobStore.Delete(key)
}

func TestAssetDeletionRetainsMetadataUntilFilesRemoved(t *testing.T) {
	f := newFixture(t, 100)
	if err := database.MigrateLocalSchema(f.db); err != nil {
		t.Fatal(err)
	}
	store := &failingDeleteStore{BlobStore: asset.NewFileStore(f.root), fail: true}
	resources := asset.NewService(asset.Dependencies{Repository: f.repo, Blobs: store, Quota: f.storage.Quota(), LocalStorage: true, DataDir: f.root})
	session, err := resources.StartChunkedUpload("workspace-alice", asset.ChunkedUploadStart{FileName: "asset.bin", Kind: "file", Size: 7, IdempotencyKey: "delete-asset"})
	if err != nil {
		t.Fatal(err)
	}
	if err := resources.PutChunkedUpload("workspace-alice", session.UploadID, 0, bytes.NewBufferString("payload")); err != nil {
		t.Fatal(err)
	}
	resource, err := resources.CompleteChunkedUpload("workspace-alice", session.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	item := model.Asset{ID: "asset-delete", UserID: "workspace-alice", Title: "素材", PayloadJSON: fmt.Sprintf(`{"data":{"storageKey":"resource:%s"}}`, resource.ID)}
	if err := f.db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	if err := resources.DeleteUserAssetWithResources("workspace-alice", item.ID); err == nil {
		t.Fatal("物理删除失败未阻止素材删除")
	}
	if err := f.db.First(&model.Asset{}, "id = ?", item.ID).Error; err != nil {
		t.Fatal("素材记录被提前删除", err)
	}
	if err := f.db.First(&model.Resource{}, "id = ?", resource.ID).Error; err != nil {
		t.Fatal("资源记录被提前删除", err)
	}
	usage(t, f, 7, 0)
	store.fail = false
	if err := f.db.Model(&model.ResourceDeletionJob{}).Where("1 = 1").Update("next_attempt_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if err := resources.DeleteUserAssetWithResources("workspace-alice", item.ID); err != nil {
		t.Fatal(err)
	}
	usage(t, f, 0, 0)
	if store.Exists(resource.ObjectKey) == nil {
		t.Fatal("物理对象未删除")
	}
	if _, err := resources.CompleteChunkedUpload("workspace-alice", session.UploadID); err == nil {
		t.Fatal("已删除资源仍从上传会话缓存成功返回")
	}
	_, err = resources.StartChunkedUpload("workspace-alice", asset.ChunkedUploadStart{FileName: "asset.bin", Kind: "file", Size: 7, IdempotencyKey: "delete-asset"})
	reason(t, err, "upload_result_deleted")
	usage(t, f, 0, 0)
	var count int64
	if err := f.db.Model(&model.Asset{}).Where("id = ?", item.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("素材记录未删除", count, err)
	}
}

func deletionFixture(t *testing.T) (*storageFixture, *failingDeleteStore, *asset.Service, []model.Resource, model.Asset) {
	t.Helper()
	f := newFixture(t, 100)
	if err := database.MigrateLocalSchema(f.db); err != nil {
		t.Fatal(err)
	}
	if err := platformstorage.MigrateDeletionGuards(f.db); err != nil {
		t.Fatal(err)
	}
	store := &failingDeleteStore{BlobStore: asset.NewFileStore(f.root)}
	svc := asset.NewService(asset.Dependencies{Repository: f.repo, Blobs: store, Quota: f.storage.Quota(), LocalStorage: true, DataDir: f.root})
	values := make([]model.Resource, 0, 2)
	for _, key := range []string{"a-object", "z-object"} {
		if _, err := f.storage.Reserve("workspace-alice", key, 7); err != nil {
			t.Fatal(err)
		}
		resource := model.Resource{ID: key, UserID: "workspace-alice", Provider: "local", ObjectKey: key, UploadKey: &key, Size: 7, Status: model.ResourceStatusPending}
		if err := f.repo.CreateResource(&resource); err != nil {
			t.Fatal(err)
		}
		if err := store.Write(key, bytes.NewBufferString("payload")); err != nil {
			t.Fatal(err)
		}
		resource.Status = model.ResourceStatusReady
		if err := f.repo.SaveResource(&resource); err != nil {
			t.Fatal(err)
		}
		values = append(values, resource)
	}
	item := model.Asset{ID: "two-object-asset", UserID: "workspace-alice", Title: "多文件素材", PayloadJSON: `{"data":{"storageKey":"resource:a-object"},"history":[{"storageKey":"resource:z-object"}]}`}
	if err := f.db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	return f, store, svc, values, item
}

func assertDeletionUnavailable(t *testing.T, f *storageFixture, item model.Asset, resources []model.Resource) {
	t.Helper()
	repo := repository.New(f.db)
	if _, err := repo.AssetForUser(item.UserID, item.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("删除中的素材仍可读取：%v", err)
	}
	if err := repo.UpsertAsset(&item); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("旧素材请求复活了删除对象：%v", err)
	}
	for _, resource := range resources {
		if _, err := f.resources.Resource(resource.UserID, resource.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("删除资源仍可读取：%v", err)
		}
		if err := f.repo.SaveResource(&resource); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("旧资源结果复活了删除对象：%v", err)
		}
	}
	values, err := f.resources.Resources(item.UserID, 100)
	if err != nil || len(values) != 0 {
		t.Fatalf("删除资源仍可列出：%+v %v", values, err)
	}
	valuesAsset, total, err := repo.UserAssetsPage(item.UserID, 1, 20, repository.UserAssetPageFilter{})
	if err != nil || total != 0 || len(valuesAsset) != 0 {
		t.Fatalf("删除素材仍被列表统计：%+v %d %v", valuesAsset, total, err)
	}
}

func retryDeletionAfterRestart(t *testing.T, f *storageFixture) {
	t.Helper()
	if err := f.db.Model(&model.ResourceDeletionJob{}).Where("1 = 1").Updates(map[string]any{"lease_expires_at": time.Now().Add(-time.Hour), "next_attempt_at": time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	f.newResources().DrainDeletionJobs(32)
}

func TestAssetDeletionPartialFailureResumesFromDurableObjects(t *testing.T) {
	f, store, svc, values, item := deletionFixture(t)
	store.failKey = values[1].ObjectKey
	reason(t, svc.DeleteUserAssetWithResources(item.UserID, item.ID), "asset_deletion_pending")
	if store.Exists(values[0].ObjectKey) == nil || store.Exists(values[1].ObjectKey) != nil {
		t.Fatal("未形成前一对象已删、后一对象待重试的故障场景")
	}
	assertDeletionUnavailable(t, f, item, values)
	usage(t, f, 14, 0)
	var stored model.Asset
	if err := f.db.First(&stored, "id = ?", item.ID).Error; err != nil || stored.Status != model.AssetVersionStatusDeleting {
		t.Fatalf("删除进度未保留原始元数据：%+v %v", stored, err)
	}
	retryDeletionAfterRestart(t, f)
	usage(t, f, 0, 0)
	if err := f.newResources().DeleteUserAssetWithResources(item.UserID, item.ID); err != nil {
		t.Fatalf("完成后的重复删除不幂等：%v", err)
	}
	if err := repository.New(f.db).UpsertAsset(&item); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("完成删除后旧 ID 被复用：%v", err)
	}
}

func TestAssetDeletionCommitFailureAfterPhysicalRemovalResumes(t *testing.T) {
	f, store, svc, values, item := deletionFixture(t)
	if err := f.db.Exec(`CREATE TRIGGER reject_asset_delete BEFORE DELETE ON assets BEGIN SELECT RAISE(ABORT, 'injected finalization failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	reason(t, svc.DeleteUserAssetWithResources(item.UserID, item.ID), "asset_deletion_pending")
	for _, value := range values {
		if store.Exists(value.ObjectKey) == nil {
			t.Fatal("没有执行到物理文件已删除的故障窗口")
		}
	}
	assertDeletionUnavailable(t, f, item, values)
	usage(t, f, 14, 0)
	if err := f.db.Exec("DROP TRIGGER reject_asset_delete").Error; err != nil {
		t.Fatal(err)
	}
	retryDeletionAfterRestart(t, f)
	usage(t, f, 0, 0)
	exists, complete, err := f.repo.AssetDeletionState(item.UserID, item.ID)
	if err != nil || !exists || !complete {
		t.Fatalf("删除恢复未终结：%v %v %v", exists, complete, err)
	}
}

func TestOverlappingAssetDeletionsCleanSharedObjectExactlyOnce(t *testing.T) {
	f, store, _, values, item := deletionFixture(t)
	alias := values[0]
	alias.ID = "shared-alias"
	key := "alias-upload"
	alias.UploadKey = &key
	alias.Status = model.ResourceStatusPending
	if _, err := f.storage.Reserve(alias.UserID, key, alias.Size); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CreateResource(&alias); err != nil {
		t.Fatal(err)
	}
	alias.Status = model.ResourceStatusReady
	if err := f.repo.SaveResource(&alias); err != nil {
		t.Fatal(err)
	}
	second := model.Asset{ID: "second-asset", UserID: item.UserID, PayloadJSON: `{"data":{"storageKey":"resource:shared-alias"}}`}
	if err := f.db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	// 两个意图均在文件 worker 执行前提交，模拟并发请求交错。
	if err := f.repo.PrepareAssetDeletion(item.UserID, item.ID, []string{values[0].ID, values[1].ID}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.PrepareAssetDeletion(second.UserID, second.ID, []string{alias.ID}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.db.Model(&model.ResourceDeletionJob{}).Where("object_key = ?", alias.ObjectKey).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("共享对象未形成唯一清理任务：%d %v", count, err)
	}
	usage(t, f, 14, 0)
	f.newResources().DrainDeletionJobs(32)
	for _, value := range values {
		if err := store.Exists(value.ObjectKey); err == nil {
			t.Fatalf("共享对象删除遗漏：%s", value.ObjectKey)
		}
	}
	usage(t, f, 0, 0)
	for _, id := range []string{item.ID, second.ID} {
		if _, complete, err := f.repo.AssetDeletionState(item.UserID, id); err != nil || !complete {
			t.Fatalf("删除意图没有完成：%s %v", id, err)
		}
	}
}

func TestDeletionAccountingDistinguishesStorageProvider(t *testing.T) {
	f, _, svc, values, item := deletionFixture(t)
	external := values[0]
	external.ID, external.Provider, external.UploadKey = "external-resource", "external", nil
	if err := f.db.Create(&external).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteUserAssetWithResources(item.UserID, item.ID); err != nil {
		t.Fatal(err)
	}
	usage(t, f, 0, 0)
	if err := f.db.First(&model.Resource{}, "id = ?", external.ID).Error; err != nil {
		t.Fatal("不同供应商的对象被删除", err)
	}
}

func TestPlatformDeletionWorkerPreservesUnmanagedOrphans(t *testing.T) {
	f := newFixture(t, 100)
	if err := database.MigrateLocalSchema(f.db); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	for _, status := range []model.ResourceStatus{model.ResourceStatusReady, model.ResourceStatusPending, model.ResourceStatusFailed} {
		resource := model.Resource{ID: string(status), UserID: "workspace-alice", Provider: "local", ObjectKey: string(status), Status: status, Size: 7, CreatedAt: old, UpdatedAt: old}
		if err := f.db.Create(&resource).Error; err != nil {
			t.Fatal(err)
		}
		if err := asset.NewFileStore(f.root).Write(resource.ObjectKey, bytes.NewBufferString("payload")); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.resources.StartDeletionWorker(ctx, func(run func(context.Context)) bool { run(ctx); return true })
	var count int64
	if err := f.db.Model(&model.Resource{}).Count(&count).Error; err != nil || count != 3 {
		t.Fatalf("平台 worker 提前移除了孤儿元数据：%d %v", count, err)
	}
	if err := f.db.Model(&model.ResourceDeletionJob{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("平台 worker 自动创建了未受控删除任务：%d %v", count, err)
	}
}

func TestClientCannotSetInternalDeletingState(t *testing.T) {
	if _, err := asset.AssetFromJSON("workspace-alice", json.RawMessage(`{"id":"manual-state","kind":"text","title":"文本","coverUrl":"","tags":[],"data":{"content":"内容"}}`)); err != nil {
		t.Fatal("合法素材应能解析", err)
	}
	_, err := asset.AssetFromJSON("workspace-alice", json.RawMessage(`{"id":"manual-state","kind":"text","title":"文本","coverUrl":"","tags":[],"status":"deleting","data":{"content":"内容"}}`))
	if err == nil {
		t.Fatal("客户端能伪造内部删除状态")
	}
}

func TestAssetDeletionRetainsCompletedTaskReferences(t *testing.T) {
	f := newFixture(t, 100)
	if err := database.MigrateLocalSchema(f.db); err != nil {
		t.Fatal(err)
	}
	resource := model.Resource{ID: "resource-task", UserID: "workspace-alice", Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "users/alice/task.bin", Size: 7}
	item := model.Asset{ID: "asset-task", UserID: "workspace-alice", Title: "结果素材", PayloadJSON: `{"data":{"storageKey":"resource:resource-task"}}`}
	task := model.Task{ID: "completed-task", UserID: "workspace-alice", Status: model.TaskStatusSucceeded, ResultJSON: `{"resourceId":"resource-task"}`}
	for _, row := range []any{&resource, &item, &task} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.resources.DeleteUserAssetWithResources("workspace-alice", item.ID); err == nil {
		t.Fatal("已完成任务引用未阻止删除")
	}
	if err := f.db.First(&model.Asset{}, "id = ?", item.ID).Error; err != nil {
		t.Fatal("引用中的素材被删除", err)
	}
}
