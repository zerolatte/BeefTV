//go:build platform

package platformstorage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
)

// UploadRequest 在分片目录清理后仍保留请求摘要，阻止同键更换文件或元数据。
type UploadRequest struct {
	ID          string `gorm:"primaryKey;size:36"`
	WorkspaceID string `gorm:"uniqueIndex:platform_upload_request_key;not null"`
	RequestKey  string `gorm:"uniqueIndex:platform_upload_request_key;not null"`
	Digest      string `gorm:"not null;size:64"`
	CreatedAt   time.Time
}

func (UploadRequest) TableName() string { return "platform_upload_requests" }

func (s *Service) BindUploadRequest(workspaceID string, req asset.ChunkedUploadStart) error {
	if err := asset.ValidateUploadDigest(req.ContentDigest); err != nil {
		return err
	}
	key := asset.NormalizedUploadKey([]string{req.IdempotencyKey})
	if key == nil {
		return kernel.BadAuthRequest("上传操作标识不能为空")
	}
	if err := validate(workspaceID, *key, req.Size); err != nil {
		return err
	}
	req.IdempotencyKey = ""
	req.FileName = strings.TrimSpace(req.FileName)
	req.Kind = asset.NormalizeKind(req.Kind, "")
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	return s.transaction(func(tx *gorm.DB) error {
		if _, err := accountForWorkspace(tx, workspaceID, true); err != nil {
			return err
		}
		var current UploadRequest
		err := tx.Where("workspace_id = ? AND request_key = ?", workspaceID, *key).First(&current).Error
		if err == nil {
			if current.Digest != digest {
				return conflict()
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&UploadRequest{ID: kernel.NewID(), WorkspaceID: workspaceID, RequestKey: *key, Digest: digest}).Error
	})
}
