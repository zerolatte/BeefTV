package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

type testAdmin struct{}

func (testAdmin) RequireAdmin(id string) (*model.PlatformAccount, error) {
	if id != "admin" {
		return nil, kernel.Forbidden("需要管理员权限")
	}
	return &model.PlatformAccount{User: model.User{ID: id}}, nil
}

type fakeObjects struct {
	mu                           sync.Mutex
	data                         map[string][]byte
	failPut, failGet, failDelete error
	corrupt                      bool
	block                        bool
	seen                         chan struct{}
	aborted                      []string
}

func (f *fakeObjects) Put(ctx context.Context, key string, body io.Reader) error {
	if f.seen != nil {
		select {
		case f.seen <- struct{}{}:
		default:
		}
	}
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPut != nil {
		return f.failPut
	}
	data, err := io.ReadAll(body)
	if err == nil {
		f.data[key] = data
	}
	return err
}
func (f *fakeObjects) PutFile(ctx context.Context, key, path, mime string, record func(string) error) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return "test-etag", f.Put(ctx, key, file)
}
func (f *fakeObjects) Get(_ context.Context, key, span string) (*assets.ResourceStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failGet != nil {
		return nil, f.failGet
	}
	data, ok := f.data[key]
	if !ok {
		return nil, kernel.NotFound("文件不存在")
	}
	if f.corrupt {
		data = []byte("corrupt")
	}
	return &assets.ResourceStream{Body: io.NopCloser(bytes.NewReader(data)), StatusCode: 200, ContentLength: int64(len(data))}, nil
}
func (f *fakeObjects) Exists(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.data[key]
	return ok, f.failGet
}
func (f *fakeObjects) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDelete != nil {
		return f.failDelete
	}
	delete(f.data, key)
	return nil
}
func (f *fakeObjects) Abort(_ context.Context, key, id string) error {
	f.aborted = append(f.aborted, id)
	return nil
}
func (f *fakeObjects) Sign(context.Context, string, time.Duration) (string, error) {
	return "https://example.invalid/temporary", nil
}

func storageFixture(t *testing.T) (*Service, *repository.Repository, *fakeObjects) {
	t.Helper()
	dir := t.TempDir()
	db, err := gorm.Open(sqlite.Open(filepath.Join(dir, "storage.db")+"?_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.SystemSetting{}, &model.PlatformAudit{}, &model.Resource{}); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	objects := &fakeObjects{data: map[string][]byte{}}
	svc := New(repo, testAdmin{}, Options{Secrets: localcrypto.TaskCodec{DataDir: dir}, Client: func(OSSDraft) ObjectClient { return objects }})
	return svc, repo, objects
}
func testDraft() *OSSDraft {
	return &OSSDraft{Endpoint: "https://oss-cn-shanghai.aliyuncs.com", Region: "cn-shanghai", Bucket: "test-private", AccessKeyID: "test-access-id", AccessKeySecret: "test-access-secret"}
}

func TestStorageConfigSecurityVersionAndHistory(t *testing.T) {
	svc, repo, objects := storageFixture(t)
	ctx := context.Background()
	if _, err := svc.Get("user"); err == nil {
		t.Fatal("普通用户读到了管理配置")
	}
	if _, err := svc.Probe(ctx, "user", ProbeRequest{OSS: testDraft()}); err == nil {
		t.Fatal("普通用户执行了探测")
	}
	if _, err := svc.Save(ctx, "user", SaveRequest{Provider: "local"}); err == nil {
		t.Fatal("普通用户修改了配置")
	}
	initial, err := svc.Get("admin")
	if err != nil || initial.Provider != "local" || initial.Version != 0 {
		t.Fatalf("默认配置错误: %+v %v", initial, err)
	}
	if _, err := svc.Probe(ctx, "admin", ProbeRequest{OSS: testDraft()}); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := svc.readPolicy(); p.Version != 0 || len(objects.data) != 0 {
		t.Fatal("探测提前保存或未删除对象")
	}
	view, err := svc.Save(ctx, "admin", SaveRequest{Provider: "oss", OSS: testDraft()})
	if err != nil || view.Version != 1 || !view.OSS.HasAccessKeyID || !view.OSS.HasAccessKeySecret {
		t.Fatalf("保存错误: %+v %v", view, err)
	}
	encoded, _ := json.Marshal(view)
	if bytes.Contains(encoded, []byte("test-access")) {
		t.Fatal("公开配置泄露密钥")
	}
	row, err := repo.SystemSetting(locationPrefix + locationID(*testDraft()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.ValueJSON, "test-access") || strings.Count(row.ValueJSON, localcrypto.Prefix) != 2 {
		t.Fatal("密钥未全部加密")
	}
	resource := &model.Resource{ID: "old", UserID: "owner", ObjectKey: "old-object"}
	if err := svc.Select(resource); err != nil {
		t.Fatal(err)
	}
	draft := testDraft()
	draft.AccessKeyID, draft.AccessKeySecret = "", ""
	if _, err := svc.Save(ctx, "admin", SaveRequest{Provider: "oss", ExpectedVersion: 1, OSS: draft}); err != nil {
		t.Fatal(err)
	}
	draft.AccessKeyID = "rotated-id"
	if _, err := svc.Save(ctx, "admin", SaveRequest{Provider: "oss", ExpectedVersion: 2, OSS: draft}); err == nil {
		t.Fatal("更换 ID 未要求 Secret")
	}
	draft.AccessKeySecret = "rotated-secret"
	if _, err := svc.Save(ctx, "admin", SaveRequest{Provider: "oss", ExpectedVersion: 2, OSS: draft}); err != nil {
		t.Fatal(err)
	}
	_, credentials, err := svc.objectClient(resource)
	if err != nil || credentials.AccessKeyID != "rotated-id" {
		t.Fatal("同位置旧文件未使用更新后的凭据")
	}
	draft.Bucket = "another-private"
	if _, err := svc.Save(ctx, "admin", SaveRequest{Provider: "oss", ExpectedVersion: 3, OSS: draft}); err != nil {
		t.Fatal(err)
	}
	_, old, err := svc.objectClient(resource)
	if err != nil || old.Bucket != "test-private" {
		t.Fatal("更换桶破坏历史位置")
	}
	if _, err := svc.Save(ctx, "admin", SaveRequest{Provider: "local", ExpectedVersion: 3}); err == nil {
		t.Fatal("旧版本覆盖成功")
	}
	objects.failPut = errors.New("offline")
	if _, err := svc.Save(ctx, "admin", SaveRequest{Provider: "local", ExpectedVersion: 4}); err != nil {
		t.Fatal("离线时不能切换本地", err)
	}
}

func TestStorageProbeFailuresCancelAndCleanup(t *testing.T) {
	for _, scenario := range []string{"put", "get", "delete", "integrity", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, objects := storageFixture(t)
			switch scenario {
			case "put":
				objects.failPut = errors.New("secret https://internal")
			case "get":
				objects.failGet = errors.New("secret")
			case "delete":
				objects.failDelete = errors.New("secret")
			case "integrity":
				objects.corrupt = true
			case "cancel":
				objects.block = true
				objects.seen = make(chan struct{}, 1)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancel" {
				go func() { <-objects.seen; cancel() }()
			}
			_, err := svc.Probe(ctx, "admin", ProbeRequest{OSS: testDraft()})
			if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "internal") {
				t.Fatalf("探测错误不真实或泄密: %v", err)
			}
			if scenario == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("取消状态丢失")
			}
			p, _, _ := svc.readPolicy()
			if p.Version != 0 {
				t.Fatal("探测写入了策略")
			}
			rows, _ := repo.StorageCleanupSettings(cleanupPrefix, 10)
			if (scenario == "delete" || scenario == "cancel") && len(rows) != 1 {
				t.Fatal("缺少持久清理任务")
			}
			objects.failDelete, objects.failPut, objects.failGet, objects.block = nil, nil, nil, false
			svc.cleanupPending(context.Background())
			rows, _ = repo.StorageCleanupSettings(cleanupPrefix, 10)
			if len(rows) != 0 || len(objects.data) != 0 {
				t.Fatal("恢复后未清理测试对象")
			}
		})
	}
}

func TestStorageAtomicAuditAndConcurrentSave(t *testing.T) {
	svc, repo, _ := storageFixture(t)
	ctx := context.Background()
	repo.DB().Callback().Create().Before("gorm:create").Register("fail-storage-audit", func(db *gorm.DB) {
		if db.Statement.Table == "platform_audits" {
			db.AddError(errors.New("审计写入失败"))
		}
	})
	if _, err := svc.Save(ctx, "admin", SaveRequest{Provider: "oss", OSS: testDraft()}); err == nil {
		t.Fatal("审计失败却报告成功")
	}
	p, _, _ := svc.readPolicy()
	if p.Version != 0 {
		t.Fatal("事务未回滚策略")
	}
	var count int64
	repo.DB().Model(&model.SystemSetting{}).Where("key LIKE ?", locationPrefix+"%").Count(&count)
	if count != 0 {
		t.Fatal("事务未回滚凭据")
	}
	repo.DB().Callback().Create().Remove("fail-storage-audit")
	var wg sync.WaitGroup
	errorsOut := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Save(ctx, "admin", SaveRequest{Provider: "local"})
			errorsOut <- err
		}()
	}
	wg.Wait()
	close(errorsOut)
	success := 0
	for err := range errorsOut {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("并发保存成功次数=%d", success)
	}
}

func TestStorageRejectsNonOfficialEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://oss-cn-shanghai.aliyuncs.com", "https://127.0.0.1", "https://oss-cn-shanghai.aliyuncs.com.evil.invalid", "https://oss-cn-shanghai.aliyuncs.com/path", "https://oss-cn-beijing.aliyuncs.com"} {
		d := testDraft()
		d.Endpoint = endpoint
		if validateDraft(*d) == nil {
			t.Fatal("接受非官方或跨区域地址")
		}
	}
}
