package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

// Select 在创建资源记录前冻结位置，切换全局策略不会改变该资源。
func (s *Service) Select(resource *model.Resource) error {
	p, _, err := s.readPolicy()
	if err != nil {
		return err
	}
	resource.Provider = p.Provider
	if p.Provider == "local" {
		return nil
	}
	loc, err := s.readLocation(p.LocationID)
	if err != nil {
		return err
	}
	resource.StorageSettingID, resource.Endpoint, resource.Bucket = loc.ID, loc.Endpoint, loc.Bucket
	return nil
}

func (s *Service) objectClient(r *model.Resource) (ObjectClient, OSSDraft, error) {
	if r == nil || r.Provider != "oss" || r.StorageSettingID == "" {
		return nil, OSSDraft{}, storageError("configuration", "资源存储位置无效")
	}
	loc, err := s.readLocation(r.StorageSettingID)
	if err != nil {
		return nil, OSSDraft{}, err
	}
	if loc.Endpoint != r.Endpoint || loc.Bucket != r.Bucket {
		return nil, OSSDraft{}, storageError("configuration", "资源与存储位置不一致")
	}
	draft, err := s.plain(loc)
	if err != nil {
		return nil, draft, err
	}
	if err := validateDraft(draft); err != nil {
		return nil, draft, storageError("configuration", "原存储位置配置无效")
	}
	return s.options.Client(draft), draft, nil
}

func (s *Service) Write(ctx context.Context, r *model.Resource, path string) (string, error) {
	client, draft, err := s.objectClient(r)
	if err != nil {
		return "", err
	}
	unlock, err := lockObject(ctx, r.StorageSettingID, r.ObjectKey)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := s.clearPrevious(ctx, r.StorageSettingID, r.ObjectKey, client); err != nil {
		return "", err
	}
	// 先登记补偿任务；成功后删除。进程在远端成功、元数据提交前退出仍可恢复暂存。
	job, err := s.registerCleanup(draft, r.StorageSettingID, r.ObjectKey, "")
	if err != nil {
		return "", err
	}
	etag, err := client.PutFile(ctx, r.ObjectKey, path, r.MimeType, func(id string) error { job.UploadID = id; return s.saveJob(job) })
	if err == nil {
		if err := s.repo.DeleteSystemSetting(job.settingKey()); err != nil {
			return "", storageError("cleanup", "无法确认 OSS 上传补偿记录，请重试")
		}
		return etag, nil
	}
	var upload *oss.UploadError
	if errors.As(err, &upload) && upload.UploadId != "" {
		job.UploadID = upload.UploadId
		if saveErr := s.saveJob(job); saveErr != nil {
			return "", storageError("cleanup", "无法保存分片清理记录，请重试")
		}
	}
	// 使用独立短预算清理取消或失败的分片，失败仍保留持久任务。
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.cleanJob(cleanupCtx, job, client)
	return "", safeError(err)
}

func (s *Service) Open(ctx context.Context, r *model.Resource, span string) (*assets.ResourceStream, error) {
	client, _, err := s.objectClient(r)
	if err != nil {
		return nil, err
	}
	stream, err := client.Get(ctx, r.ObjectKey, span)
	if err != nil {
		return nil, safeError(err)
	}
	stream.Resource = r
	if stream.StatusCode == 416 {
		stream.ContentRange = fmt.Sprintf("bytes */%d", r.Size)
	}
	return stream, nil
}
func (s *Service) Exists(ctx context.Context, r *model.Resource) (bool, error) {
	client, _, err := s.objectClient(r)
	if err != nil {
		return false, err
	}
	present, err := client.Exists(ctx, r.ObjectKey)
	return present, safeError(err)
}
func (s *Service) Delete(ctx context.Context, r *model.Resource) error {
	client, _, err := s.objectClient(r)
	if err != nil {
		return err
	}
	unlock, err := lockObject(ctx, r.StorageSettingID, r.ObjectKey)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.clearPrevious(ctx, r.StorageSettingID, r.ObjectKey, client); err != nil {
		return err
	}
	return safeError(client.Delete(ctx, r.ObjectKey))
}
func (s *Service) Sign(ctx context.Context, r *model.Resource, ttl time.Duration) (string, error) {
	client, _, err := s.objectClient(r)
	if err != nil {
		return "", err
	}
	if ttl <= 0 || ttl > 15*time.Minute {
		ttl = 5 * time.Minute
	}
	return client.Sign(ctx, r.ObjectKey, ttl)
}
func (s *Service) Fallback(r *model.Resource, cause error) error {
	err := safeError(cause)
	return s.repo.RecordPlatformAudit(model.PlatformAudit{ID: kernel.NewID(), ActorID: r.UserID, SubjectID: r.ID, Action: "storage.fallback", Reason: err.Error()})
}
