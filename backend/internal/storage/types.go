// Package storage 管理平台文件存储策略、位置凭据及 OSS 驱动。
package storage

import (
	"context"
	"io"
	"time"

	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const policyKey = "resource-storage.policy"
const locationPrefix = "resource-storage.location."

type OSSDraft struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyId"`
	AccessKeySecret string `json:"accessKeySecret"`
}

type SaveRequest struct {
	Provider        string    `json:"provider"`
	ExpectedVersion int64     `json:"expectedVersion"`
	OSS             *OSSDraft `json:"oss,omitempty"`
}

type ProbeRequest struct {
	OSS *OSSDraft `json:"oss,omitempty"`
}
type ProbeResult struct {
	ElapsedMS int64  `json:"elapsedMs"`
	Message   string `json:"message"`
}
type PublicOSS struct {
	Endpoint           string `json:"endpoint"`
	Region             string `json:"region"`
	Bucket             string `json:"bucket"`
	HasAccessKeyID     bool   `json:"hasAccessKeyId"`
	HasAccessKeySecret bool   `json:"hasAccessKeySecret"`
}
type View struct {
	Provider        string                       `json:"provider"`
	Version         int64                        `json:"version"`
	OSS             *PublicOSS                   `json:"oss"`
	Statistics      []repository.StorageUsage    `json:"statistics"`
	RecentFallbacks []repository.StorageFallback `json:"recentFallbacks"`
}

type policy struct {
	Provider   string `json:"provider"`
	Version    int64  `json:"version"`
	LocationID string `json:"locationId"`
}

// location 中两个凭据字段只能保存 localcrypto 密文。
type location struct {
	OSSDraft
	ID string `json:"id"`
}

type Repository interface {
	SystemSetting(string) (*model.SystemSetting, error)
	SaveSystemSetting(*model.SystemSetting) error
	DeleteSystemSetting(string) error
	ReplaceStorageSettings(context.Context, string, *model.SystemSetting, *model.SystemSetting, model.PlatformAudit) error
	StorageStatistics() ([]repository.StorageUsage, error)
	StorageFallbacks(int) ([]repository.StorageFallback, error)
	RecordPlatformAudit(model.PlatformAudit) error
	StorageCleanupSettings(string, int) ([]model.SystemSetting, error)
}
type Admin interface {
	RequireAdmin(string) (*model.PlatformAccount, error)
}
type Secrets interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}

// ObjectClient 使业务测试可以注入假 OSS，生产始终使用官方 SDK。
type ObjectClient interface {
	PutFile(context.Context, string, string, string, func(string) error) (string, error)
	Put(context.Context, string, io.Reader) error
	Get(context.Context, string, string) (*assets.ResourceStream, error)
	Exists(context.Context, string) (bool, error)
	Delete(context.Context, string) error
	Abort(context.Context, string, string) error
	Sign(context.Context, string, time.Duration) (string, error)
}

type Options struct {
	Secrets Secrets
	Client  func(OSSDraft) ObjectClient
}
type Service struct {
	repo    Repository
	admin   Admin
	options Options
}

func New(repo Repository, admin Admin, options Options) *Service {
	if options.Client == nil {
		options.Client = newOSSClient
	}
	return &Service{repo: repo, admin: admin, options: options}
}
