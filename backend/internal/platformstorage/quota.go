//go:build platform

package platformstorage

import (
	"errors"
	"io"
	"log"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// QuotaAdapter 只将既有资源流程映射到持久账本；终结以ResourceRepository事务为准。
type QuotaAdapter struct{ storage *Service }

func (s *Service) Quota() *QuotaAdapter { return &QuotaAdapter{storage: s} }

func (q *QuotaAdapter) ensure(workspaceID string, size int64, key, purpose string) (string, error) {
	if err := validate(workspaceID, key, size); err != nil {
		return "", err
	}
	err := q.storage.transaction(func(tx *gorm.DB) error {
		held, err := reserve(tx, workspaceID, key, size, purpose, true)
		if err != nil {
			return err
		}
		if held.State == StateCommitted {
			return nil
		}
		return extend(tx, workspaceID, key, size)
	})
	return "platform", err
}

func (q *QuotaAdapter) ReserveUpload(w string, n int64, k string) (string, error) {
	return q.ensure(w, n, k, PurposeUpload)
}
func (q *QuotaAdapter) ReserveChunked(w string, n int64, k string) (string, error) {
	return q.ensure(w, n, k, PurposeUpload)
}
func (q *QuotaAdapter) ReserveRetry(w string, n int64, k string) (string, error) {
	return q.ensure(w, n, k, PurposeUpload)
}
func (q *QuotaAdapter) ReserveGenerated(w string, n int64, k string) (string, error) {
	return q.ensure(w, n, k, PurposeGenerated)
}
func (q *QuotaAdapter) ReserveGeneratedRetry(w string, n int64, k string) (string, error) {
	return q.ensure(w, n, k, PurposeGenerated)
}

func (q *QuotaAdapter) Release(w, _ string, _ int64, k string) {
	if err := q.storage.Release(w, k); err != nil {
		log.Printf("平台存储预留释放失败：%v", err)
	}
}
func (q *QuotaAdapter) ReleaseRetry(w, day string, n int64, k string) { q.Release(w, day, n, k) }

// Commit 不再另写账本，资源READY与空间结算已在同一事务完成。
func (q *QuotaAdapter) Commit(string, int64, string) {}

func (q *QuotaAdapter) PrepareResourceWrite(resource *model.Resource, body io.Reader) (io.Reader, func() error, error) {
	if resource == nil || body == nil {
		return nil, nil, asset.ResourceMissing()
	}
	key := resourceIdentity(resource)
	held, err := q.storage.Reservation(resource.UserID, key)
	if err != nil {
		return nil, nil, err
	}
	if held.State != StateHeld {
		if held.State == StateCommitted && held.ObjectIdentity == asset.StorageIdentity(resource) && held.CommittedBytes == resource.Size {
			reader := &reservedReader{source: body, reserved: held.CommittedBytes, committed: true}
			return reader, func() error {
				if reader.read != held.CommittedBytes {
					return conflict()
				}
				resource.Size = reader.read
				return nil
			}, nil
		}
		return nil, nil, conflict()
	}
	reader := &reservedReader{source: body, storage: q.storage, workspaceID: resource.UserID, key: key, reserved: held.ReservedBytes}
	return reader, func() error { resource.Size = reader.read; return nil }, nil
}

type reservedReader struct {
	source           io.Reader
	storage          *Service
	workspaceID, key string
	read, reserved   int64
	committed        bool
}

// 已发布文件的字节必须由崩溃前的持久预留覆盖；只恢复真实大小，不猜测或重复发放额度。
func (q *QuotaAdapter) ReconcileStoredResource(resource *model.Resource, size int64) error {
	held, err := q.storage.Reservation(resource.UserID, resourceIdentity(resource))
	if err != nil {
		return err
	}
	if held.State != StateHeld || size < 0 || size > held.ReservedBytes {
		return conflict()
	}
	resource.Size = size
	return nil
}

func (r *reservedReader) Read(buffer []byte) (int, error) {
	// 未知长度只暂存一个有界块，扩展成功后才允许FileStore将该块落盘。
	if len(buffer) > 64<<10 {
		buffer = buffer[:64<<10]
	}
	n, err := r.source.Read(buffer)
	if n <= 0 {
		if r.committed && err == io.EOF && r.read != r.reserved {
			return 0, conflict()
		}
		return n, err
	}
	target := r.read + int64(n)
	if r.committed && err == io.EOF && target != r.reserved {
		return 0, conflict()
	}
	if target > r.reserved {
		if r.committed {
			return 0, conflict()
		}
		if reserveErr := r.storage.Extend(r.workspaceID, r.key, target); reserveErr != nil {
			return 0, reserveErr
		}
		r.reserved = target
	}
	r.read = target
	return n, err
}

// EnsureUploadReservation 在解析 multipart 前预留声明大小，同键在途请求不得共享清理权。
func (s *Service) EnsureUploadReservation(workspaceID, key string, bytes int64) error {
	if err := validate(workspaceID, key, bytes); err != nil {
		return err
	}
	return s.transaction(func(tx *gorm.DB) error {
		var existing Reservation
		err := tx.Where("workspace_id = ? AND business_key = ?", workspaceID, key).First(&existing).Error
		if err == nil && (existing.RequestedBytes != bytes || existing.Purpose != PurposeUpload) {
			return conflict()
		}
		if err == nil && existing.State == StateHeld {
			return asset.UploadInProgress()
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		_, err = reserve(tx, workspaceID, key, bytes, PurposeUpload, true)
		return err
	})
}

func (s *Service) ReleaseUnstartedUpload(workspaceID, key string) error {
	var resource model.Resource
	err := s.db.Where("user_id = ? AND upload_key = ?", workspaceID, key).First(&resource).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return s.Release(workspaceID, key)
}

var _ asset.Quota = (*QuotaAdapter)(nil)
