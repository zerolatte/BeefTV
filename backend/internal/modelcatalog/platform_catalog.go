//go:build platform

package modelcatalog

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/repository"
)

type PlatformCatalogModel struct {
	PublicLogicalModel
	PointPrices map[string]int64 `json:"pointPrices"`
}

type PlatformCatalogResponse struct {
	Source   CatalogSource          `json:"source"`
	Models   []PlatformCatalogModel `json:"models"`
	Channels []PublicChannelCatalog `json:"channels"`
}

type PlatformCatalogReader struct{ repo *repository.Repository }

func NewPlatformCatalogReader(repo *repository.Repository) *PlatformCatalogReader {
	return &PlatformCatalogReader{repo: repo}
}

func requirePlatformCatalogAccount(db *gorm.DB, accountID, workspaceID string) error {
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(workspaceID) == "" {
		return kernel.Unauthorized("缺少有效的平台账号和工作区")
	}
	var count int64
	err := db.Model(&platformauth.Account{}).
		Joins("JOIN platform_account_workspaces ON platform_account_workspaces.account_id = platform_accounts.id").
		Where("platform_accounts.id = ? AND platform_accounts.status = ? AND platform_accounts.must_change_password = ? AND platform_account_workspaces.workspace_id = ?", accountID, platformauth.StatusActive, false, workspaceID).Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return kernel.Unauthorized("平台账号或工作区绑定已失效")
	}
	return nil
}

// 目录不缓存账号授权，撤权后的下一次读取立即生效。
func (s *PlatformCatalogReader) Read(ctx context.Context, accountID, workspaceID string) (*PlatformCatalogResponse, error) {
	response := &PlatformCatalogResponse{Source: CatalogSourceFrontend, Models: []PlatformCatalogModel{}, Channels: []PublicChannelCatalog{}}
	err := s.repo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requirePlatformCatalogAccount(tx, accountID, workspaceID); err != nil {
			return err
		}
		items := []model.LogicalModel{}
		if err := tx.Model(&model.LogicalModel{}).Joins("JOIN platform_model_grants ON platform_model_grants.logical_model_id = logical_models.id").Where("platform_model_grants.account_id = ? AND logical_models.enabled = ? AND logical_models.archived_at IS NULL", accountID, true).Order("logical_models.sort_order ASC, logical_models.created_at ASC, logical_models.id ASC").Find(&items).Error; err != nil {
			return err
		}
		snapshot, err := platformRouteSnapshot(s.repo.WithTx(tx), items)
		if err != nil {
			return err
		}
		pricesByModel, err := platformCurrentPrices(tx, snapshot.Ordered)
		if err != nil {
			return err
		}
		for _, id := range snapshot.Ordered {
			cached := snapshot.Models[id]
			prices := pricesByModel[id]
			if prices == nil {
				prices = map[string]int64{}
			}
			available := LogicalModelCapabilityCovered(cached.ProductSpec, AvailableCachedRouteSpecs(cached.Routes)) && len(cached.ProductSpec.Operations) > 0
			for _, operation := range cached.ProductSpec.Operations {
				if prices[operation] <= 0 {
					available = false
				}
			}
			response.Models = append(response.Models, PlatformCatalogModel{PublicLogicalModel: ProjectPublicLogicalModel(cached, available), PointPrices: prices})
		}
		return nil
	})
	return response, err
}

func platformRouteSnapshot(repo *repository.Repository, items []model.LogicalModel) (*RouteCatalog, error) {
	graphs, err := repo.LogicalModelGraphs(items, false)
	if err != nil {
		return nil, err
	}
	converted := make(map[string]LogicalModelGraph, len(graphs))
	channelIDs := []string{}
	for id, graph := range graphs {
		converted[id] = CatalogGraphFromRepo(graph)
		for _, channelModel := range graph.ChannelModels {
			channelIDs = append(channelIDs, channelModel.ChannelID)
		}
	}
	enabled := make(map[string]bool, len(channelIDs))
	if len(channelIDs) > 0 {
		var readyIDs []string
		if err := repo.DB().Model(&model.PlatformChannelState{}).
			Joins("JOIN model_channels ON model_channels.id = platform_channel_states.channel_id AND model_channels.scope = ? AND model_channels.enabled = ? AND model_channels.deleted_at IS NULL", model.ChannelScopeSystem, true).
			Joins("JOIN platform_credential_versions ON platform_credential_versions.id = platform_channel_states.current_credential_id AND platform_credential_versions.channel_id = platform_channel_states.channel_id").
			Where("platform_channel_states.channel_id IN ?", channelIDs).Pluck("platform_channel_states.channel_id", &readyIDs).Error; err != nil {
			return nil, err
		}
		for _, id := range readyIDs {
			enabled[id] = true
		}
	}
	return BuildRouteCatalog(time.Now().UTC(), 0, items, converted, enabled), nil
}

func platformCurrentPrices(db *gorm.DB, modelIDs []string) (map[string]map[string]int64, error) {
	result := make(map[string]map[string]int64, len(modelIDs))
	if len(modelIDs) == 0 {
		return result, nil
	}
	var prices []model.PlatformModelPrice
	err := db.Model(&model.PlatformModelPrice{}).Joins("JOIN platform_model_price_heads ON platform_model_price_heads.price_version_id = platform_model_prices.id AND platform_model_price_heads.logical_model_id = platform_model_prices.logical_model_id AND platform_model_price_heads.operation = platform_model_prices.operation").Where("platform_model_prices.logical_model_id IN ?", modelIDs).Find(&prices).Error
	if err != nil {
		return nil, err
	}
	for _, price := range prices {
		if price.Points > 0 && price.Points <= platformauth.MaxPoints {
			if result[price.LogicalModelID] == nil {
				result[price.LogicalModelID] = map[string]int64{}
			}
			result[price.LogicalModelID][price.Operation] = price.Points
		}
	}
	return result, nil
}

// PlatformAdmissionSelection 只含冻结配置与引用，不含明文凭据。
type PlatformAdmissionSelection struct {
	Routed              RoutedModel
	CredentialVersionID string
	Price               model.PlatformModelPrice
}

// ResolvePlatformAdmission 必须在调用方创建任务、预留点数的同一事务中执行。
func ResolvePlatformAdmission(tx *gorm.DB, accountID, workspaceID, modelID string, intent ModelRequestIntent) (*PlatformAdmissionSelection, error) {
	if tx == nil || tx.Statement == nil {
		return nil, errors.New("平台准入必须绑定数据库事务")
	}
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, errors.New("平台准入必须绑定数据库事务")
	}
	if err := requirePlatformCatalogAccount(tx, accountID, workspaceID); err != nil {
		return nil, err
	}
	var grant model.PlatformModelGrant
	if err := tx.Where("account_id = ? AND logical_model_id = ?", accountID, modelID).Take(&grant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &kernel.AppError{Status: 403, Code: 403, Reason: "model_not_authorized", Message: "当前账号未获该模型授权"}
		}
		return nil, err
	}
	var item model.LogicalModel
	if err := tx.Where("id = ? AND enabled = ? AND archived_at IS NULL", modelID, true).Take(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, kernel.BadAuthRequest("模型已停用或不存在")
		}
		return nil, err
	}
	snapshot, err := platformRouteSnapshot(repository.New(tx), []model.LogicalModel{item})
	if err != nil {
		return nil, err
	}
	cached, ok := snapshot.Models[item.ID]
	if !ok || !slices.Contains(cached.ProductSpec.Operations, intent.Operation) {
		return nil, kernel.BadAuthRequest("模型未发布有效的操作能力")
	}
	intent.Options = mergeIntentDefaults(intent.Options, cached.Defaults)
	if match := MatchCapability(cached.ProductSpec, intent); !match.Matched {
		return nil, kernel.BadAuthRequest("模型不支持当前请求：" + strings.Join(match.Reasons, "；"))
	}
	router := NewRouter(nil, nil, nil, 0, 0)
	eligible := router.EligibleLogicalRoutes(cached.Routes, intent, nil)
	if len(eligible) == 0 {
		return nil, kernel.BadAuthRequest("没有可用的模型路由")
	}
	selected := WeightedRoute(eligible)
	var state model.PlatformChannelState
	if err := tx.Where("channel_id = ?", selected.ChannelModel.ChannelID).Take(&state).Error; err != nil {
		return nil, err
	}
	var price model.PlatformModelPrice
	err = tx.Model(&model.PlatformModelPrice{}).Joins("JOIN platform_model_price_heads ON platform_model_price_heads.price_version_id = platform_model_prices.id AND platform_model_price_heads.logical_model_id = platform_model_prices.logical_model_id AND platform_model_price_heads.operation = platform_model_prices.operation").Where("platform_model_prices.logical_model_id = ? AND platform_model_prices.operation = ?", modelID, intent.Operation).Take(&price).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && (price.Points <= 0 || price.Points > platformauth.MaxPoints)) {
		return nil, &kernel.AppError{Status: 403, Code: 403, Reason: "model_price_unavailable", Message: "模型操作尚未发布有效价格"}
	}
	if err != nil {
		return nil, err
	}
	return &PlatformAdmissionSelection{Routed: RoutedModel{LogicalModel: item, Revision: cached.Revision, Route: selected.Route, ChannelModel: selected.ChannelModel, Variant: channelModelVariantForIntent(selected.ChannelModel, intent), Defaults: cached.Defaults}, CredentialVersionID: state.CurrentCredentialID, Price: price}, nil
}
