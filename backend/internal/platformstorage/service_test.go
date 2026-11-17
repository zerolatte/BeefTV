//go:build platform

package platformstorage_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/platformstorage"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type storageFixture struct {
	db        *gorm.DB
	storage   *platformstorage.Service
	repo      *platformstorage.ResourceRepository
	resources *asset.Service
	root      string
}

func newFixture(t *testing.T, limit int64) *storageFixture {
	t.Helper()
	root := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(root, "storage.db")+"?_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&platformauth.Account{}, &platformauth.AccountWorkspace{}, &platformauth.StorageAccount{}, &platformstorage.Reservation{}, &platformstorage.StoredObject{}, &platformstorage.AssetDeletion{}, &platformstorage.DeletionObject{}, &model.Resource{}, &model.ResourceDeletionJob{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alice", "bob"} {
		if err := db.Create(&platformauth.Account{ID: name, Username: name, DisplayName: name, Role: platformauth.RoleUser, Status: platformauth.StatusActive}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&platformauth.AccountWorkspace{AccountID: name, WorkspaceID: "workspace-" + name}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&platformauth.StorageAccount{AccountID: name, LimitBytes: limit}).Error; err != nil {
			t.Fatal(err)
		}
	}
	storage := platformstorage.New(db)
	f := &storageFixture{db: db, storage: storage, repo: storage.ResourceRepository(repository.New(db)), root: root}
	f.resources = f.newResources()
	return f
}

func (f *storageFixture) newResources() *asset.Service {
	return asset.NewService(asset.Dependencies{Repository: f.repo, Blobs: asset.NewFileStore(f.root), Quota: f.storage.Quota(), DataDir: f.root, LocalStorage: true, ResumeChunkedUploads: true})
}

func reason(t *testing.T, err error, expected string) {
	t.Helper()
	var value *kernel.AppError
	if !errors.As(err, &value) || string(value.Reason) != expected {
		t.Fatalf("期望 %s，实际 %v", expected, err)
	}
}

func usage(t *testing.T, f *storageFixture, used, reserved int64) {
	t.Helper()
	value, err := f.storage.Usage("workspace-alice")
	if err != nil || value.UsedBytes != used || value.ReservedBytes != reserved {
		t.Fatalf("用量期望 %d/%d，实际 %+v %v", used, reserved, value, err)
	}
}

func TestConcurrentReservationCannotExceedLimit(t *testing.T) {
	f := newFixture(t, 100)
	var successes atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			_, err := f.storage.Reserve("workspace-alice", fmt.Sprintf("upload-%d", i), 10)
			if err == nil {
				successes.Add(1)
			} else {
				reason(t, err, "storage_quota_exceeded")
			}
		})
	}
	wg.Wait()
	if successes.Load() != 10 {
		t.Fatalf("成功预留数 %d", successes.Load())
	}
	usage(t, f, 0, 100)
}

func TestReplaySettlementAndObjectDeduplication(t *testing.T) {
	f := newFixture(t, 100)
	for _, key := range []string{"one", "two"} {
		if _, err := f.storage.Reserve("workspace-alice", key, 20); err != nil {
			t.Fatal(err)
		}
		if _, err := f.storage.Reserve("workspace-alice", key, 20); err != nil {
			t.Fatal(err)
		}
		if err := f.storage.Commit("workspace-alice", key, "local-object", 20); err != nil {
			t.Fatal(err)
		}
		if err := f.storage.Commit("workspace-alice", key, "local-object", 20); err != nil {
			t.Fatal(err)
		}
		if err := f.storage.Release("workspace-alice", key); err != nil {
			t.Fatal(err)
		}
	}
	usage(t, f, 20, 0)
	_, err := f.storage.Reserve("workspace-alice", "one", 21)
	reason(t, err, "idempotency_conflict")
	reason(t, f.storage.Commit("workspace-alice", "one", "different-object", 20), "idempotency_conflict")
}

func TestLimitReductionKeepsExistingHold(t *testing.T) {
	f := newFixture(t, 100)
	if _, err := f.storage.Reserve("workspace-alice", "first", 80); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&platformauth.StorageAccount{}).Where("account_id = ?", "alice").Update("limit_bytes", 10).Error; err != nil {
		t.Fatal(err)
	}
	_, err := f.storage.Reserve("workspace-alice", "second", 1)
	reason(t, err, "storage_quota_exceeded")
	reason(t, f.storage.Extend("workspace-alice", "first", 81), "storage_quota_exceeded")
	if err := f.storage.Commit("workspace-alice", "first", "object", 80); err != nil {
		t.Fatal(err)
	}
	usage(t, f, 80, 0)
}

func TestReadyAndSettlementRollbackTogether(t *testing.T) {
	f := newFixture(t, 100)
	key := "ready-atomic"
	if _, err := f.storage.Quota().ReserveUpload("workspace-alice", 7, key); err != nil {
		t.Fatal(err)
	}
	r := &model.Resource{ID: "r", UserID: "workspace-alice", UploadKey: &key, Provider: "local", ObjectKey: "file", Size: 7, Status: model.ResourceStatusPending}
	if err := f.repo.CreateResource(r); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("CREATE TRIGGER reject_ready BEFORE UPDATE ON resources WHEN NEW.status = 'ready' BEGIN SELECT RAISE(ABORT,'injected'); END").Error; err != nil {
		t.Fatal(err)
	}
	r.Status = model.ResourceStatusReady
	if err := f.repo.SaveResource(r); err == nil {
		t.Fatal("应注入失败")
	}
	usage(t, f, 0, 7)
	var persisted model.Resource
	if err := f.db.First(&persisted, "id = ?", r.ID).Error; err != nil || persisted.Status != model.ResourceStatusPending {
		t.Fatal("状态未回滚", err)
	}
	r.Status = model.ResourceStatusFailed
	if err := f.repo.SaveResource(r); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.SaveResource(r); err != nil {
		t.Fatal(err)
	}
	usage(t, f, 0, 0)
}

func TestUnknownStreamQuotaFailureRemovesTemporaryBytes(t *testing.T) {
	f := newFixture(t, 10)
	r, err := f.resources.StoreGenerated("workspace-alice", "file", "output.bin", "application/octet-stream", 0, 0, 0, 0, bytes.NewReader(bytes.Repeat([]byte{1}, 11)))
	reason(t, err, "storage_quota_exceeded")
	if r == nil || r.Status != model.ResourceStatusFailed {
		t.Fatalf("应保留失败资源用于恢复: %+v", r)
	}
	usage(t, f, 0, 0)
	if err := filepath.WalkDir(filepath.Join(f.root, "resources"), func(_ string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return errors.New("超限后残留文件")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestChunkSessionRestartAndOwnerIsolation(t *testing.T) {
	f := newFixture(t, 100)
	request := asset.ChunkedUploadStart{FileName: "payload.bin", Kind: "file", Size: 7, IdempotencyKey: "durable-upload"}
	session, err := f.resources.StartChunkedUpload("workspace-alice", request)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.resources.PutChunkedUpload("workspace-bob", session.UploadID, 0, bytes.NewReader([]byte("payload"))); err == nil {
		t.Fatal("跨工作区分片写入未拒绝")
	}
	if err := f.resources.PutChunkedUpload("workspace-alice", session.UploadID, 0, bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	restarted := f.newResources()
	resumed, err := restarted.StartChunkedUpload("workspace-alice", request)
	if err != nil || resumed.UploadID != session.UploadID || len(resumed.Received) != 1 {
		t.Fatalf("分片未恢复: %+v %v", resumed, err)
	}
	resource, err := restarted.CompleteChunkedUpload("workspace-alice", resumed.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	usage(t, f, 7, 0)
	stream, err := restarted.OpenRange("workspace-alice", resource.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	data, err := io.ReadAll(stream.Body)
	if err != nil || string(data) != "payload" {
		t.Fatal("上传结果不同", err)
	}
	if _, err := restarted.OpenRange("workspace-bob", resource.ID, "bytes=0-2"); err == nil {
		t.Fatal("跨工作区 Range 未拒绝")
	}
}

func TestUploadRecoveryKeepsGeneratedReservations(t *testing.T) {
	f := newFixture(t, 100)
	if _, err := f.storage.Quota().ReserveGenerated("workspace-alice", 20, "generation"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.storage.Quota().ReserveUpload("workspace-alice", 10, "orphan"); err != nil {
		t.Fatal(err)
	}
	_ = f.newResources()
	usage(t, f, 0, 20)
	held, err := f.storage.Reservation("workspace-alice", "generation")
	if err != nil || held.State != platformstorage.StateHeld {
		t.Fatal("未知生成预留被释放", err)
	}
}

func TestDeletionCompletionReleasesOnce(t *testing.T) {
	f := newFixture(t, 100)
	if _, err := f.storage.Reserve("workspace-alice", "first", 20); err != nil {
		t.Fatal(err)
	}
	identity := asset.StorageIdentity(&model.Resource{Provider: "local", ObjectKey: "file"})
	if err := f.storage.Commit("workspace-alice", "first", identity, 20); err != nil {
		t.Fatal(err)
	}
	job := model.ResourceDeletionJob{ID: "deletion", UserID: "workspace-alice", Provider: "local", ObjectKey: "file", LeaseOwner: "worker", Status: model.ResourceDeletionStatusProcessing}
	if err := f.db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteResourceDeletionJob(job.ID, "foreign-worker"); err != nil {
		t.Fatal(err)
	}
	usage(t, f, 20, 0)
	if err := f.repo.CompleteResourceDeletionJob(job.ID, "worker"); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.CompleteResourceDeletionJob(job.ID, "worker"); err != nil {
		t.Fatal(err)
	}
	usage(t, f, 0, 0)
}

func TestMultipartConcurrentIdentityCannotShareRelease(t *testing.T) {
	f := newFixture(t, 100)
	if err := f.storage.EnsureUploadReservation("workspace-alice", "upload", 10); err != nil {
		t.Fatal(err)
	}
	if err := f.storage.EnsureUploadReservation("workspace-alice", "upload", 10); err == nil {
		t.Fatal("同键并发请求不能共享预留的释放权")
	}
	usage(t, f, 0, 10)
	if err := f.storage.ReleaseUnstartedUpload("workspace-alice", "upload"); err != nil {
		t.Fatal(err)
	}
	usage(t, f, 0, 0)
	if err := f.storage.EnsureUploadReservation("workspace-alice", "upload", 11); err == nil {
		t.Fatal("释放后的同键不能更改请求大小")
	}
}

func TestRecoverMissingReadyBytesDoesNotChargeStorageAgain(t *testing.T) {
	f := newFixture(t, 7)
	restore := func() (asset.RecoveredArtifact, error) {
		return asset.RecoveredArtifact{Kind: "file", FileName: "out.bin", MimeType: "application/octet-stream", Size: 7, Body: bytes.NewReader([]byte("payload"))}, nil
	}
	resource, err := f.resources.RecoverOwned("workspace-alice", "task:result", restore)
	if err != nil {
		t.Fatal(err)
	}
	if err := asset.NewFileStore(f.root).Delete(resource.ObjectKey); err != nil {
		t.Fatal(err)
	}
	resource, err = f.resources.RecoverOwned("workspace-alice", "task:result", restore)
	if err != nil || resource.Status != model.ResourceStatusReady {
		t.Fatalf("恢复失败 %+v %v", resource, err)
	}
	usage(t, f, 7, 0)
}
