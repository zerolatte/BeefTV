//go:build platform

package admin

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/repository"
)

func platformCatalogAccount(db *gorm.DB, accountID string, active bool) error {
	query := db.Where("id = ? AND status <> ?", accountID, platformauth.StatusDeleted)
	if active {
		query = query.Where("status = ? AND must_change_password = ?", platformauth.StatusActive, false)
	}
	var account platformauth.Account
	if err := query.Take(&account).Error; err != nil {
		return catalogNotFound(err, "账号")
	}
	return nil
}

func (s *PlatformCatalog) ModelGrants(ctx context.Context, actorID, accountID string) ([]string, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	db := s.repo.DB().WithContext(ctx)
	if err := platformCatalogAccount(db, accountID, false); err != nil {
		return nil, err
	}
	ids := []string{}
	err := db.Model(&model.PlatformModelGrant{}).Where("account_id = ?", accountID).Order("logical_model_id ASC").Pluck("logical_model_id", &ids).Error
	return ids, err
}

func (s *PlatformCatalog) ReplaceModelGrants(ctx context.Context, actorID, accountID string, ids []string) ([]string, error) {
	if ids == nil || len(ids) > 1000 {
		return nil, kernel.BadAuthRequest("必须提供模型授权数组，清空时使用空数组")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 36 || seen[id] {
			return nil, kernel.BadAuthRequest("模型授权标识为空、过长或重复")
		}
		seen[id] = true
	}
	err := s.transaction(ctx, actorID, func(repo *repository.Repository) error {
		if err := platformCatalogAccount(repo.DB(), accountID, false); err != nil {
			return err
		}
		if len(ids) > 0 {
			var count int64
			if err := repo.DB().Model(&model.LogicalModel{}).Where("id IN ? AND archived_at IS NULL", ids).Count(&count).Error; err != nil {
				return err
			}
			if count != int64(len(ids)) {
				return kernel.NotFound("模型不存在")
			}
		}
		if err := repo.DB().Where("account_id = ?", accountID).Delete(&model.PlatformModelGrant{}).Error; err != nil {
			return err
		}
		for _, id := range ids {
			if err := repo.DB().Create(&model.PlatformModelGrant{AccountID: accountID, LogicalModelID: id, CreatedBy: actorID, CreatedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return catalogAudit(repo.DB(), actorID, accountID, "model_grants_replaced", map[string]any{"logicalModelIds": ids})
	})
	return slices.Clone(ids), err
}

func (s *PlatformCatalog) ModelPrices(ctx context.Context, actorID, id string) ([]model.PlatformModelPrice, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	if _, err := s.repo.WithContext(ctx).LogicalModel(id); err != nil {
		return nil, catalogNotFound(err, "模型")
	}
	prices := []model.PlatformModelPrice{}
	err := s.repo.DB().WithContext(ctx).Where("logical_model_id = ?", id).Order("operation ASC, version DESC").Find(&prices).Error
	return prices, err
}

func (s *PlatformCatalog) PublishPrice(ctx context.Context, actorID, id, operation string, points int64) (*model.PlatformModelPrice, error) {
	operation = strings.TrimSpace(operation)
	if points <= 0 || points > platformauth.MaxPoints || operation == "" || len(operation) > 64 {
		return nil, kernel.BadAuthRequest("固定价格必须是范围内的正整数，并指定模型操作")
	}
	price := &model.PlatformModelPrice{ID: uuid.NewString(), LogicalModelID: id, Operation: operation, Points: points, CreatedBy: actorID, CreatedAt: time.Now().UTC()}
	err := s.transaction(ctx, actorID, func(repo *repository.Repository) error {
		item, err := repo.LogicalModel(id)
		if err != nil {
			return catalogNotFound(err, "模型")
		}
		revision, err := repo.LogicalModelRevision(item.ActiveRevisionID)
		if err != nil {
			return err
		}
		spec, err := modelcatalog.DecodeCapabilitySpec(revision.CapabilitySpecJSON)
		if err != nil {
			return err
		}
		if !slices.Contains(spec.Operations, operation) {
			return kernel.BadAuthRequest("价格操作必须属于已发布的模型能力")
		}
		head := model.PlatformModelPriceHead{LogicalModelID: id, Operation: operation}
		if err := repo.DB().Clauses(clause.OnConflict{DoNothing: true}).Create(&head).Error; err != nil {
			return err
		}
		updated := repo.DB().Model(&model.PlatformModelPriceHead{}).Where("logical_model_id = ? AND operation = ? AND version_sequence < ?", id, operation, platformauth.MaxPoints).UpdateColumn("version_sequence", gorm.Expr("version_sequence + 1"))
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return errors.New("模型价格版本不可分配")
		}
		if err := repo.DB().Where("logical_model_id = ? AND operation = ?", id, operation).Take(&head).Error; err != nil {
			return err
		}
		price.Version = head.VersionSequence
		if err := repo.DB().Create(price).Error; err != nil {
			return err
		}
		if err := repo.DB().Model(&model.PlatformModelPriceHead{}).Where("logical_model_id = ? AND operation = ?", id, operation).Update("price_version_id", price.ID).Error; err != nil {
			return err
		}
		return catalogAudit(repo.DB(), actorID, "", "model_price_published", map[string]any{"logicalModelId": id, "operation": operation, "priceVersionId": price.ID, "version": price.Version, "points": points})
	})
	return price, err
}
