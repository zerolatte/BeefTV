package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/kernel"
)

func TestStorageProbeDeletePermissionThroughSDK(t *testing.T) {
	svc, repo, _ := storageFixture(t)
	client, upstream := sdkFixture(t)
	upstream.failDelete = true
	svc.options.Client = func(OSSDraft) ObjectClient { return client }
	_, err := svc.Probe(context.Background(), "admin", ProbeRequest{OSS: testDraft()})
	assertProbeCleanupFailure(t, err, "storage_delete_permission", "oss:DeleteObject")
	if len(upstream.objects) != 1 {
		t.Fatal("删除拒绝后测试对象状态不真实")
	}
	jobs, err := repo.StorageCleanupSettings(cleanupPrefix, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal("删除拒绝后未保留清理任务")
	}
	upstream.failDelete = false
	svc.cleanupPending(context.Background())
	jobs, err = repo.StorageCleanupSettings(cleanupPrefix, 10)
	if err != nil || len(jobs) != 0 || len(upstream.objects) != 0 {
		t.Fatal("恢复删除权限后清理失败")
	}
}

type cleanupRecordFailure struct{ Repository }

func (cleanupRecordFailure) DeleteSystemSetting(string) error {
	return errors.New("sensitive-key internal-address")
}

func TestStorageProbeLocalCleanupRecordFailure(t *testing.T) {
	svc, repo, upstream := storageFixture(t)
	svc.repo = cleanupRecordFailure{Repository: repo}
	_, err := svc.Probe(context.Background(), "admin", ProbeRequest{OSS: testDraft()})
	assertProbeCleanupFailure(t, err, "storage_cleanup_record", "OSS 文件已删除，但本地清理记录更新失败")
	if strings.Contains(err.Error(), "权限") || len(upstream.data) != 0 {
		t.Fatal("本地记录失败被误报为远端删除权限问题")
	}
	svc.repo = repo
	svc.cleanupPending(context.Background())
	jobs, err := repo.StorageCleanupSettings(cleanupPrefix, 10)
	if err != nil || len(jobs) != 0 {
		t.Fatal("本地记录恢复后未完成清理")
	}
}

func TestStorageProbeCleanupRetainsTimeoutAndCancellation(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled} {
		svc, _, upstream := storageFixture(t)
		upstream.failDelete = cause
		_, err := svc.Probe(context.Background(), "admin", ProbeRequest{OSS: testDraft()})
		if errors.Is(cause, context.Canceled) {
			if !errors.Is(err, context.Canceled) {
				t.Fatal("删除时取消丢失取消状态")
			}
		} else {
			assertProbeCleanupFailure(t, err, "storage_timeout", "超时")
		}
	}
}

func assertProbeCleanupFailure(t *testing.T, err error, reason, message string) {
	t.Helper()
	var failure *kernel.AppError
	if !errors.As(err, &failure) || string(failure.Reason) != reason || !strings.Contains(failure.Message, message) {
		t.Fatalf("探测错误分类不正确: %v", err)
	}
	if strings.Contains(err.Error(), "sensitive-key") || strings.Contains(err.Error(), "internal-address") || strings.Contains(err.Error(), "private") {
		t.Fatal("探测错误泄漏上游正文")
	}
}
