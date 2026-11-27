package asset

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

type testObjectStorage struct {
	data                         map[string][]byte
	writeErr, readErr, deleteErr error
	fallbacks, writes            int
	cancel                       context.CancelFunc
}

func (f *testObjectStorage) Select(r *model.Resource) error {
	r.Provider, r.StorageSettingID, r.Endpoint, r.Bucket = "oss", "location", "https://oss-cn-shanghai.aliyuncs.com", "test-bucket"
	return nil
}
func (f *testObjectStorage) Write(ctx context.Context, r *model.Resource, path string) (string, error) {
	f.writes++
	if f.cancel != nil {
		f.cancel()
		return "", ctx.Err()
	}
	if f.writeErr != nil {
		return "", f.writeErr
	}
	body, err := os.ReadFile(path)
	if err == nil {
		f.data[r.ObjectKey] = body
	}
	return "etag", err
}
func (f *testObjectStorage) Open(_ context.Context, r *model.Resource, span string) (*ResourceStream, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	body, ok := f.data[r.ObjectKey]
	if !ok {
		return nil, os.ErrNotExist
	}
	start, end, valid := parseNormalizedByteRange(span, int64(len(body)))
	status := 200
	if span != "" {
		if !valid {
			return &ResourceStream{Resource: r, StatusCode: 416, Body: http.NoBody}, nil
		}
		body = body[start : end+1]
		status = 206
	}
	return &ResourceStream{Resource: r, Body: io.NopCloser(bytes.NewReader(body)), StatusCode: status, ContentLength: int64(len(body))}, nil
}
func (f *testObjectStorage) Exists(_ context.Context, r *model.Resource) (bool, error) {
	_, ok := f.data[r.ObjectKey]
	return ok, f.readErr
}
func (f *testObjectStorage) Delete(_ context.Context, r *model.Resource) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.data, r.ObjectKey)
	return nil
}
func (f *testObjectStorage) Sign(context.Context, *model.Resource, time.Duration) (string, error) {
	return "https://example.invalid/signed", nil
}
func (f *testObjectStorage) Fallback(*model.Resource, error) error { f.fallbacks++; return nil }
func objectDomain(t *testing.T) (*Service, *testObjectStorage) {
	svc, _, _ := newTestDomain(t)
	fake := &testObjectStorage{data: map[string][]byte{}}
	svc.ConfigureObjectStorage(fake)
	return svc, fake
}

func TestObjectStorageUploadMixedReadAndFallback(t *testing.T) {
	svc, remote := objectDomain(t)
	first, err := svc.UploadFile("owner", "file.txt", 7, "file", 0, 0, 0, bytes.NewReader([]byte("payload")), "upload-one")
	if err != nil || first.Provider != "oss" {
		t.Fatalf("OSS 保存失败: %+v %v", first, err)
	}
	if svc.blobs.Exists(first.ObjectKey) == nil {
		t.Fatal("OSS 成功后保留了永久本地副本")
	}
	again, err := svc.UploadFile("owner", "file.txt", 7, "file", 0, 0, 0, bytes.NewReader([]byte("payload")), "upload-one")
	if err != nil || again.ID != first.ID || remote.writes != 1 {
		t.Fatal("重复请求重复上传")
	}
	span, err := svc.OpenRangeContext(context.Background(), "owner", first.ID, "bytes=1-3")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(span.Body)
	span.Body.Close()
	if span.StatusCode != 206 || string(got) != "ayl" {
		t.Fatal("Range 内容错误")
	}
	if _, err := svc.OpenRange("other", first.ID, ""); err == nil {
		t.Fatal("跨用户访问成功")
	}
	path, release, err := svc.Materialize(context.Background(), "owner", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("临时媒体文件未释放")
	}
	remote.writeErr = errors.New("OSS 暂时不可用")
	second, err := svc.UploadFile("owner", "file.txt", 7, "file", 0, 0, 0, bytes.NewReader([]byte("payload")), "upload-two")
	if err != nil || second.Provider != "local" || second.StorageSettingID != "" || remote.fallbacks != 1 {
		t.Fatalf("回退失败: %+v %v", second, err)
	}
	if svc.blobs.Exists(second.ObjectKey) != nil {
		t.Fatal("回退文件缺失")
	}
	_, body, err := svc.Open("owner", first.ID)
	if err != nil {
		t.Fatal("全局回退影响了历史 OSS 文件", err)
	}
	body.Close()
}

func TestObjectStorageMetadataFailureRestartAndReadError(t *testing.T) {
	svc, remote := objectDomain(t)
	original := svc.repo
	svc.repo = &readySaveFailRepo{Repository: original, remaining: 1}
	key := "task:0"
	count := 0
	restore := func() (RecoveredArtifact, error) {
		count++
		return RecoveredArtifact{Kind: "image", FileName: "result.png", MimeType: "image/png", Size: 7, Width: 1, Height: 1, Body: bytes.NewReader([]byte("payload"))}, nil
	}
	first, err := svc.RecoverOwned("owner", key, restore)
	if err == nil || first.Status != model.ResourceStatusPending || svc.blobs.Exists(first.ObjectKey) != nil {
		t.Fatal("元数据失败未保留可恢复暂存")
	}
	// 新服务实例模拟进程重启，沿用原身份与磁盘暂存。
	restarted := NewService(Dependencies{Repository: original, Blobs: svc.blobs, Objects: remote, DataDir: svc.dataDir})
	second, err := restarted.RecoverOwned("owner", key, restore)
	if err != nil || second.ID != first.ID || count != 1 || second.Status != model.ResourceStatusReady {
		t.Fatalf("重启恢复错误: %+v %v count=%d", second, err, count)
	}
	remote.readErr = errors.New("OSS 鉴权失败")
	if _, err := restarted.RecoverOwned("owner", key, restore); err == nil || count != 1 {
		t.Fatal("OSS 访问异常被当作丢失并重新取回")
	}
	if _, err := restarted.OpenRange("owner", second.ID, ""); err == nil {
		t.Fatal("OSS 访问异常被伪装成功")
	}
}

func TestObjectStorageCancellationDoesNotFallback(t *testing.T) {
	svc, remote := objectDomain(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remote.cancel = cancel
	key := NormalizedUploadKey([]string{"cancel-upload"})
	result, _, err := svc.StoreContext(ctx, "owner", "file", "a.txt", "text/plain", 7, 0, 0, 0, bytes.NewReader([]byte("payload")), key)
	if !errors.Is(err, context.Canceled) || remote.fallbacks != 0 || result.Status == model.ResourceStatusReady {
		t.Fatalf("取消被当成成功回退: %+v %v", result, err)
	}
}

func TestObjectStorageChunkAndURLImport(t *testing.T) {
	svc, remote := objectDomain(t)
	sess, err := svc.StartChunkedUpload("owner", ChunkedUploadStart{FileName: "file.txt", Kind: "file", Size: 7, IdempotencyKey: "chunks"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.PutChunkedUpload("owner", sess.UploadID, 0, bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	result, err := svc.CompleteChunkedUploadContext(context.Background(), "owner", sess.UploadID)
	if err != nil || result.Provider != "oss" {
		t.Fatalf("分片上传失败: %+v %v", result, err)
	}
	again, err := svc.CompleteChunkedUpload("owner", sess.UploadID)
	if err != nil || result.ID != again.ID || remote.writes != 1 {
		t.Fatal("重复分片完成产生新资源")
	}
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("imported"))
	}))
	defer server.Close()
	imported, err := svc.ImportURL(context.Background(), "owner", server.URL+"/file.txt", "file", 1024, 0, 0, 0, "url-import")
	if err != nil || imported.Provider != "oss" || string(remote.data[imported.ObjectKey]) != "imported" {
		t.Fatalf("URL 导入失败: %+v %v", imported, err)
	}
}

func TestObjectStorageQuotaDiskFailureAndMaintenance(t *testing.T) {
	svc, repo, _ := newTestDomain(t)
	remote := &testObjectStorage{data: map[string][]byte{}}
	svc.ConfigureObjectStorage(remote)
	svc.quota = NewFileQuota(repo, func() (FileQuotaPolicy, error) {
		return FileQuotaPolicy{ResourceUploadMB: 1, GeneratedFileMB: 1, DailyUploadMB: 10, StoredFileGB: 1}, nil
	}, nil)
	first, err := svc.UploadFile("owner", "a.txt", 7, "file", 0, 0, 0, bytes.NewReader([]byte("payload")), "quota-a")
	if err != nil {
		t.Fatal(err)
	}
	remote.writeErr = errors.New("上游超时")
	for i := 0; i < 2; i++ {
		if _, err := svc.UploadFile("owner", "b.txt", 7, "file", 0, 0, 0, bytes.NewReader([]byte("payload")), "quota-b"); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := repo.UserStoredFileBytes("owner")
	if err != nil || stored != 14 {
		t.Fatalf("混合存储配额重复或漏计: %d %v", stored, err)
	}
	var daily model.UserDailyUploadUsage
	if err := repo.DB().First(&daily, "user_id = ?", "owner").Error; err != nil || daily.Bytes != 14 {
		t.Fatalf("回退重复计入日额度: %+v %v", daily, err)
	}
	// 模拟提交后进程退出，遗留暂存由维护循环释放。
	if err := svc.blobs.Write(first.ObjectKey, bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	remote.readErr = errors.New("远端暂时不可用")
	svc.releaseCommittedStages(context.Background())
	if svc.blobs.Exists(first.ObjectKey) != nil {
		t.Fatal("远端读取失败时误删可恢复暂存")
	}
	remote.readErr = nil
	delete(remote.data, first.ObjectKey)
	svc.releaseCommittedStages(context.Background())
	if svc.blobs.Exists(first.ObjectKey) != nil {
		t.Fatal("远端对象缺失时误删唯一暂存")
	}
	remote.data[first.ObjectKey] = []byte("payload")
	svc.releaseCommittedStages(context.Background())
	if svc.blobs.Exists(first.ObjectKey) == nil {
		t.Fatal("已提交 OSS 暂存未释放")
	}
	svc.blobs = &failNthWriteBlobStore{BlobStore: svc.blobs, failNth: 1}
	if _, err := svc.UploadFile("owner", "c.txt", 7, "file", 0, 0, 0, bytes.NewReader([]byte("payload")), "quota-c"); err == nil {
		t.Fatal("本地暂存写失败却报告成功")
	}
	if remote.fallbacks != 1 {
		t.Fatal("暂存失败不应进入 OSS 回退")
	}
}

func TestObjectStorageDeletionFailureRemainsRetryable(t *testing.T) {
	svc, db, _ := newDeletionDomain(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	remote := &testObjectStorage{data: map[string][]byte{}}
	svc.ConfigureObjectStorage(remote)
	r, _, err := svc.Store("owner", "file", "a.txt", "text/plain", 7, 0, 0, 0, bytes.NewReader([]byte("payload")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Asset{ID: "remote-asset", UserID: "owner", Title: "远端文件", PayloadJSON: `{"data":{"resourceId":"` + r.ID + `","storageKey":"resource:` + r.ID + `"}}`}).Error; err != nil {
		t.Fatal(err)
	}
	remote.deleteErr = errors.New("OSS 删除暂时失败")
	if err := svc.DeleteUserAssetWithResources("owner", "remote-asset"); err != nil {
		t.Fatal(err)
	}
	svc.DrainDeletionJobs(1)
	var job model.ResourceDeletionJob
	if err := db.First(&job, "resource_id = ?", r.ID).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != model.ResourceDeletionStatusPending || job.LastError == "" || job.StorageSettingID != r.StorageSettingID {
		t.Fatalf("失败删除未保留原位置: %+v", job)
	}
	if _, exists := remote.data[r.ObjectKey]; !exists {
		t.Fatal("错误删除了对象")
	}
	remote.deleteErr = nil
	if err := db.Model(&job).Update("next_attempt_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	svc.DrainDeletionJobs(1)
	if _, exists := remote.data[r.ObjectKey]; exists {
		t.Fatal("待重试对象未完成物理清理")
	}
}

func TestObjectStorageVideoDerivatives(t *testing.T) {
	installAssetFakeMediaTools(t, "ok")
	svc, remote := objectDomain(t)
	source, _, err := svc.Store("owner", "video", "source.mp4", "video/mp4", 64, 80, 60, 1000, bytes.NewReader(bytes.Repeat([]byte("v"), 64)), nil)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := svc.ExtractVideoFrames(context.Background(), "owner", source.ID, ExtractVideoFramesRequest{Preset: "first"}, "remote-first")
	if err != nil || len(frames.Frames) != 1 || frames.Frames[0].Resource.Provider != "oss" {
		t.Fatalf("远端截帧失败: %+v %v", frames, err)
	}
	separated, err := svc.SeparateVideoAudio(context.Background(), "owner", source.ID, "remote-audio")
	if err != nil || separated.Audio.Provider != "oss" || separated.MutedVideo.Provider != "oss" {
		t.Fatalf("远端音频分离失败: %+v %v", separated, err)
	}
	if len(remote.data) != 4 {
		t.Fatal("派生媒体保存数量错误")
	}
}
