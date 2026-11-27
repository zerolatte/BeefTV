package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

const cleanupPrefix = "resource-storage.cleanup."

type cleanupJob struct {
	ID          string   `json:"id"`
	LocationID  string   `json:"locationId"`
	ObjectKey   string   `json:"objectKey"`
	UploadID    string   `json:"uploadId,omitempty"`
	Credentials location `json:"credentials"`
}

func (j cleanupJob) settingKey() string { return cleanupPrefix + j.ID }

var objectLocks [64]chan struct{}
var objectLocksOnce sync.Once

func cleanupID(locationID, key string) string {
	sum := sha256.Sum256([]byte(locationID + "\x00" + key))
	return hex.EncodeToString(sum[:20])
}
func lockObject(ctx context.Context, locationID, key string) (func(), error) {
	objectLocksOnce.Do(func() {
		for i := range objectLocks {
			objectLocks[i] = make(chan struct{}, 1)
		}
	})
	sum := sha256.Sum256([]byte(locationID + "\x00" + key))
	lock := objectLocks[int(sum[0])%len(objectLocks)]
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) registerCleanup(d OSSDraft, id, key, uploadID string) (cleanupJob, error) {
	job := cleanupJob{ID: cleanupID(id, key), LocationID: id, ObjectKey: key, UploadID: uploadID}
	// 未保存草稿只为清理任务保留加密凭据，不会修改当前配置和版本。
	if id == "" {
		var err error
		d.AccessKeyID, err = s.options.Secrets.Encrypt(d.AccessKeyID)
		if err == nil {
			d.AccessKeySecret, err = s.options.Secrets.Encrypt(d.AccessKeySecret)
		}
		if err != nil {
			return job, storageError("cleanup", "无法创建安全的测试清理任务")
		}
		job.Credentials.OSSDraft = d
	}
	return job, s.saveJob(job)
}
func (s *Service) saveJob(job cleanupJob) error {
	data, _ := json.Marshal(job)
	return s.repo.SaveSystemSetting(&model.SystemSetting{Key: job.settingKey(), ValueJSON: string(data)})
}
func (s *Service) clearPrevious(ctx context.Context, id, key string, client ObjectClient) error {
	row, err := s.repo.SystemSetting(cleanupPrefix + cleanupID(id, key))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var job cleanupJob
	if json.Unmarshal([]byte(row.ValueJSON), &job) != nil {
		return storageError("cleanup", "待清理记录损坏，请联系管理员检查")
	}
	return s.cleanJob(ctx, job, client)
}
func (s *Service) cleanJob(ctx context.Context, job cleanupJob, client ObjectClient) error {
	if job.UploadID != "" {
		if err := client.Abort(ctx, job.ObjectKey, job.UploadID); err != nil {
			return safeError(err)
		}
	}
	if err := client.Delete(ctx, job.ObjectKey); err != nil {
		return safeError(err)
	}
	if err := s.repo.DeleteSystemSetting(job.settingKey()); err != nil {
		return storageError("cleanup_record", "OSS 文件已删除，但本地清理记录更新失败；请稍后重试")
	}
	return nil
}

// Run 定期重试测试对象、失败上传及已知分片的清理，退出时遵循平台上下文。
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		s.cleanupPending(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) cleanupPending(ctx context.Context) {
	rows, err := s.repo.StorageCleanupSettings(cleanupPrefix, 50)
	if err != nil {
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		var job cleanupJob
		if json.Unmarshal([]byte(row.ValueJSON), &job) != nil {
			continue
		}
		unlock, err := lockObject(ctx, job.LocationID, job.ObjectKey)
		if err != nil {
			return
		}
		// 获取锁后重读，避免把刚刚上传成功并删除补偿记录的对象清除。
		current, err := s.repo.SystemSetting(row.Key)
		if err != nil || current.ValueJSON != row.ValueJSON {
			unlock()
			continue
		}
		loc := &job.Credentials
		if job.LocationID != "" {
			loc, err = s.readLocation(job.LocationID)
		}
		if err == nil {
			draft, decryptErr := s.plain(loc)
			if decryptErr == nil {
				budget, cancel := context.WithTimeout(ctx, 25*time.Second)
				if cleanupErr := s.cleanJob(budget, job, s.options.Client(draft)); cleanupErr != nil {
					_ = s.saveJob(job)
				}
				cancel()
			}
		}
		unlock()
	}
}
