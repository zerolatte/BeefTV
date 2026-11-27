package asset

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

// ConfigureObjectStorage 仅在组合根启动路由与工作线程前调用。
func (s *Service) ConfigureObjectStorage(store ObjectStorage) { s.objects = store }

func (s *Service) selectStorage(r *model.Resource) error {
	if s.objects == nil {
		r.Provider = "local"
		return nil
	}
	return s.objects.Select(r)
}

type cancelReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r cancelReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (s *Service) writeObject(ctx context.Context, r *model.Resource, name string, body io.Reader) (string, error) {
	if r == nil {
		return "", ResourceMissing()
	}
	if s == nil || s.blobs == nil {
		return "", errors.New("本地资源暂存不可用")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(r.ObjectKey) == "" {
		r.ObjectKey = ObjectKey(r.UserID, r.Kind, name, r.MimeType, time.Now())
	}
	if err := s.blobs.Write(r.ObjectKey, cancelReader{ctx: ctx, reader: body}); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.publishStage(ctx, r)
}

func (s *Service) publishStage(ctx context.Context, r *model.Resource) (string, error) {
	if r.Provider == "local" || r.Provider == "" {
		r.Provider = "local"
		return "", nil
	}
	if r.Provider != "oss" || s.objects == nil {
		return "", ResourceNotLocal()
	}
	file, err := s.blobs.Open(r.ObjectKey)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() != r.Size {
		return "", kernel.BadAuthRequest("暂存文件大小与资源记录不一致，请重新上传")
	}
	etag, err := s.objects.Write(ctx, r, file.Name())
	if err == nil {
		return etag, nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return "", err
	}
	// 本地原文件已通过崩溃安全写入；仅切换实际位置，不创建第二份资源或配额记录。
	if auditErr := s.objects.Fallback(r, err); auditErr != nil {
		return "", kernel.NewAppError(503, "无法记录存储回退，文件暂存已保留，请重试")
	}
	r.Provider, r.Endpoint, r.Bucket, r.StorageSettingID, r.ETag = "local", "", "", "", ""
	return "", nil
}

func (s *Service) releaseStage(r *model.Resource) {
	if s != nil && s.blobs != nil && r != nil && r.Provider == "oss" && r.Status == model.ResourceStatusReady {
		// 删除失败不影响已提交的远端资源；后续读取/重试仍可再次释放。
		_ = s.blobs.Delete(r.ObjectKey)
	}
}

// objectAvailable 严格区分不存在与访问失败；后者不能触发结果重新取回。
func (s *Service) objectAvailable(ctx context.Context, r *model.Resource) (bool, error) {
	if r == nil || s.blobs == nil || r.ObjectKey == "" {
		return false, nil
	}
	if r.Provider == "oss" {
		if s.objects == nil {
			return false, ResourceNotLocal()
		}
		if r.Status != model.ResourceStatusReady && s.blobs.Exists(r.ObjectKey) == nil {
			return true, nil
		}
		return s.objects.Exists(ctx, r)
	}
	err := s.blobs.Exists(r.ObjectKey)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// resumeStored 对未完成的远端上传重放暂存；没有暂存时仅在远端确实存在后提交元数据。
func (s *Service) resumeStored(ctx context.Context, r *model.Resource) error {
	if r.Provider != "oss" {
		return nil
	}
	if s.blobs.Exists(r.ObjectKey) == nil {
		etag, err := s.publishStage(ctx, r)
		if err != nil {
			return err
		}
		r.ETag = etag
		return nil
	}
	present, err := s.objectAvailable(ctx, r)
	if err != nil {
		return err
	}
	if !present {
		return ResourceFileMissing()
	}
	return nil
}

// Materialize 校验归属并提供媒体工具可使用的路径；远端临时文件由返回的释放函数清理。
func (s *Service) Materialize(ctx context.Context, userID, id string) (string, func(), error) {
	r, err := s.ownedResource(userID, id)
	if err != nil {
		return "", nil, err
	}
	if r.Status != model.ResourceStatusReady {
		return "", nil, ResourceNotReady()
	}
	return s.materialize(ctx, r)
}

func (s *Service) materialize(ctx context.Context, r *model.Resource) (string, func(), error) {
	if r.Provider == "local" || (r.Status != model.ResourceStatusReady && s.blobs.Exists(r.ObjectKey) == nil) {
		file, err := s.blobs.Open(r.ObjectKey)
		if err != nil {
			return "", nil, err
		}
		return file.Name(), func() { _ = file.Close() }, nil
	}
	if r.Provider != "oss" || s.objects == nil {
		return "", nil, ResourceNotLocal()
	}
	stream, err := s.objects.Open(ctx, r, "")
	if err != nil {
		return "", nil, err
	}
	defer stream.Body.Close()
	dir := filepath.Join(s.dataDir, "tmp", "resource-media")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", nil, err
	}
	file, err := os.CreateTemp(dir, "media-*"+FileExtension("", r.MimeType, r.Kind))
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = file.Close(); _ = os.Remove(file.Name()) }
	written, err := io.Copy(file, cancelReader{ctx: ctx, reader: stream.Body})
	if err == nil && written != r.Size {
		err = kernel.NewAppError(502, "OSS 文件不完整，请重试")
	}
	if err == nil {
		err = file.Close()
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return file.Name(), cleanup, nil
}

func (s *Service) SignedReference(ctx context.Context, userID, id string, ttl time.Duration) (string, error) {
	r, err := s.ownedResource(userID, id)
	if err != nil {
		return "", err
	}
	if r.Status != model.ResourceStatusReady {
		return "", ResourceNotReady()
	}
	if r.Provider != "oss" || s.objects == nil {
		return "", fmt.Errorf("资源没有远端签名地址")
	}
	return s.objects.Sign(ctx, r, ttl)
}
