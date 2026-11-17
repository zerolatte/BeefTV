//go:build platform

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/protocol"
	"infinite-canvas/backend/internal/repository"
)

type PlatformCatalogDependencies struct {
	EncryptSecret func(string) (string, error)
	DecryptSecret func(string) (string, error)
	Protocols     modelcatalog.ProtocolLookup
	Invalidate    func(context.Context)
}

type PlatformProtocolView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Capability string `json:"capability"`
}

func (s *PlatformCatalog) Protocols(ctx context.Context, actorID string) ([]PlatformProtocolView, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	items := []PlatformProtocolView{}
	for _, metadata := range protocol.Builtins().List(protocol.SurfaceAdminSystemChannel, "", false) {
		resolved, ok := s.deps.Protocols(metadata.ID)
		if ok && resolved.Enabled && resolved.UnavailableReason == "" {
			items = append(items, PlatformProtocolView{ID: resolved.ID, Name: metadata.Name, Capability: resolved.PrimaryCapability})
		}
	}
	return items, nil
}

type PlatformCatalog struct {
	repo *repository.Repository
	deps PlatformCatalogDependencies
}

func NewPlatformCatalog(repo *repository.Repository, deps PlatformCatalogDependencies) (*PlatformCatalog, error) {
	if repo == nil || repo.DB() == nil || deps.EncryptSecret == nil || deps.DecryptSecret == nil {
		return nil, errors.New("平台模型管理装配不完整")
	}
	if deps.Protocols == nil {
		deps.Protocols = modelcatalog.LookupFromRegistry(protocol.Builtins())
	}
	return &PlatformCatalog{repo: repo, deps: deps}, nil
}

func (s *PlatformCatalog) transaction(ctx context.Context, actorID string, fn func(*repository.Repository) error) error {
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := platformauth.RequireAdminInTransaction(tx, actorID); err != nil {
			return err
		}
		return fn(s.repo.WithTx(tx))
	})
	if err == nil && s.deps.Invalidate != nil {
		s.deps.Invalidate(ctx)
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) || (err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")) {
		return &kernel.AppError{Status: 409, Code: 409, Reason: "model_configuration_conflict", Message: "模型配置已存在或已被修改"}
	}
	return err
}

func (s *PlatformCatalog) requireAdmin(ctx context.Context, actorID string) error {
	return platformauth.RequireAdminInTransaction(s.repo.DB().WithContext(ctx), actorID)
}

func catalogAudit(db *gorm.DB, actorID, targetAccountID, action string, difference map[string]any) error {
	encoded, err := json.Marshal(difference)
	if err != nil {
		return err
	}
	return db.Create(&platformauth.Audit{ID: uuid.NewString(), ActorAccountID: actorID, TargetAccountID: targetAccountID, Action: action, Difference: string(encoded), CreatedAt: time.Now().UTC()}).Error
}

func catalogNotFound(err error, label string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return kernel.NotFound(label + "不存在")
	}
	return err
}

func validCatalogPage(page, size int) error {
	if page < 1 || page > 100000 || size < 1 || size > 100 {
		return kernel.BadAuthRequest("分页参数无效")
	}
	return nil
}
