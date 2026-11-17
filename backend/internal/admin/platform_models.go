//go:build platform

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/repository"
)

func (s *PlatformCatalog) ChannelModels(ctx context.Context, actorID, channelID string) ([]model.ChannelModel, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	repo := s.repo.WithContext(ctx)
	if _, err := repo.AdminSystemChannel(channelID); err != nil {
		return nil, catalogNotFound(err, "渠道")
	}
	items, err := repo.ChannelModels(channelID, true)
	if items == nil {
		items = []model.ChannelModel{}
	}
	return modelcatalog.HydrateChannelModelCapabilityConfig(items), err
}

func (s *PlatformCatalog) SaveChannelModel(ctx context.Context, actorID, channelID, id string, input modelcatalog.ChannelModelRequest) (*model.ChannelModel, error) {
	var result *model.ChannelModel
	err := s.transaction(ctx, actorID, func(repo *repository.Repository) error {
		channel, err := repo.AdminSystemChannel(channelID)
		if err != nil {
			return catalogNotFound(err, "渠道")
		}
		view, err := platformChannelView(repo, *channel)
		if err != nil {
			return err
		}
		// 合同只需要确认凭据种类存在，不把明文带入模型记录或响应。
		if view.HasAPIKey {
			channel.APIKey = "configured"
		}
		if view.HasSecretKey {
			channel.SecretKey = "configured"
		}
		key, providerKey, capability, protocol, err := modelcatalog.NormalizeChannelModelContract(s.deps.Protocols, channel, input)
		if err != nil {
			return err
		}
		if len(key) > 120 || len(providerKey) > 120 || utf8.RuneCountInString(input.DisplayName) > 160 {
			return kernel.BadAuthRequest("模型标识或名称过长")
		}
		var existing *model.ChannelModel
		if id != "" {
			existing, err = repo.ChannelModelByID(channelID, id)
			if err != nil {
				return catalogNotFound(err, "渠道模型")
			}
			var references int64
			if err := repo.DB().Model(&model.LogicalModelRoute{}).Where("channel_model_id = ?", id).Count(&references).Error; err != nil {
				return err
			}
			if references > 0 {
				return &kernel.AppError{Status: 409, Code: 409, Reason: "model_version_in_use", Message: "该渠道模型已被发布版本引用，请创建新的渠道模型并发布模型版本"}
			}
		}
		item, variants, err := modelcatalog.PrepareChannelModelSave(channelID, id, input, key, providerKey, capability, protocol, existing, func(string) (string, error) { return uuid.NewString(), nil })
		if err != nil {
			return err
		}
		if err := repo.SaveChannelModelWithVariants(item, variants); err != nil {
			return err
		}
		if err := repo.SyncChannelModelNames(channelID, time.Now().UTC()); err != nil {
			return err
		}
		item.Variants = variants
		if err := catalogAudit(repo.DB(), actorID, "", "channel_model_saved", map[string]any{"channelId": channelID, "channelModelId": item.ID, "modelKey": item.ModelKey, "capability": item.Capability, "protocol": item.Protocol, "enabled": item.Enabled}); err != nil {
			return err
		}
		items := modelcatalog.HydrateChannelModelCapabilityConfig([]model.ChannelModel{*item})
		result = &items[0]
		return nil
	})
	return result, err
}

type PlatformLogicalRouteInput struct {
	ChannelModelID string `json:"channelModelId"`
	Enabled        bool   `json:"enabled"`
	Priority       int    `json:"priority"`
	Weight         int    `json:"weight"`
}

type PlatformLogicalModelInput struct {
	Code               string                      `json:"code"`
	Name               string                      `json:"name"`
	Icon               string                      `json:"icon"`
	Description        string                      `json:"description"`
	Enabled            bool                        `json:"enabled"`
	SortOrder          int                         `json:"sortOrder"`
	ExpectedRevisionID string                      `json:"expectedRevisionId"`
	CapabilitySpec     modelcatalog.CapabilitySpec `json:"capabilitySpec"`
	DefaultOptions     map[string]any              `json:"defaultOptions"`
	Routes             []PlatformLogicalRouteInput `json:"routes"`
}

type PlatformLogicalModelView struct {
	Model          model.LogicalModel          `json:"model"`
	Revision       *model.LogicalModelRevision `json:"revision"`
	CapabilitySpec modelcatalog.CapabilitySpec `json:"capabilitySpec"`
	DefaultOptions map[string]any              `json:"defaultOptions"`
	Routes         []model.LogicalModelRoute   `json:"routes"`
}

var platformModelCode = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,79}$`)

func (s *PlatformCatalog) LogicalModels(ctx context.Context, actorID string, page, size int) ([]model.LogicalModel, int64, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, 0, err
	}
	if err := validCatalogPage(page, size); err != nil {
		return nil, 0, err
	}
	query := s.repo.DB().WithContext(ctx).Model(&model.LogicalModel{}).Where("archived_at IS NULL")
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := []model.LogicalModel{}
	err := query.Order("sort_order ASC, created_at ASC, id ASC").Offset((page - 1) * size).Limit(size).Find(&items).Error
	return items, total, err
}

func (s *PlatformCatalog) LogicalModel(ctx context.Context, actorID, id string) (*PlatformLogicalModelView, error) {
	if err := s.requireAdmin(ctx, actorID); err != nil {
		return nil, err
	}
	return platformLogicalModelView(s.repo.WithContext(ctx), id)
}

func platformLogicalModelView(repo *repository.Repository, id string) (*PlatformLogicalModelView, error) {
	graph, err := repo.LogicalModelGraph(id, true)
	if err != nil {
		return nil, catalogNotFound(err, "模型")
	}
	if graph.Revision == nil {
		return nil, errors.New("模型缺少已发布版本")
	}
	spec, err := modelcatalog.DecodeCapabilitySpec(graph.Revision.CapabilitySpecJSON)
	if err != nil {
		return nil, err
	}
	defaults, err := modelcatalog.DecodeLogicalDefaults(graph.Revision.DefaultOptionsJSON, spec)
	if err != nil {
		return nil, err
	}
	routes := graph.Routes
	if routes == nil {
		routes = []model.LogicalModelRoute{}
	}
	return &PlatformLogicalModelView{Model: graph.Model, Revision: graph.Revision, CapabilitySpec: spec, DefaultOptions: defaults, Routes: routes}, nil
}

func (s *PlatformCatalog) PublishLogicalModel(ctx context.Context, actorID, id string, input PlatformLogicalModelInput) (*PlatformLogicalModelView, error) {
	input.Code, input.Name = strings.ToLower(strings.TrimSpace(input.Code)), strings.TrimSpace(input.Name)
	if !platformModelCode.MatchString(input.Code) || utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 120 || utf8.RuneCountInString(input.Description) > 500 || len(input.Icon) > 80 {
		return nil, kernel.BadAuthRequest("模型标识、名称或描述无效")
	}
	if err := modelcatalog.ValidateChannelSortOrder(input.SortOrder); err != nil {
		return nil, err
	}
	if len(input.Routes) < 1 || len(input.Routes) > 100 {
		return nil, kernel.BadAuthRequest("模型必须配置 1—100 条渠道路由")
	}
	spec, err := modelcatalog.NormalizeCapabilitySpec(input.CapabilitySpec)
	if err != nil {
		return nil, err
	}
	if len(spec.Operations) == 0 {
		return nil, kernel.BadAuthRequest("平台模型必须明确声明可计价的操作")
	}
	defaults, err := modelcatalog.NormalizeLogicalDefaults(spec, input.DefaultOptions)
	if err != nil {
		return nil, err
	}
	var result *PlatformLogicalModelView
	err = s.transaction(ctx, actorID, func(repo *repository.Repository) error {
		now := time.Now().UTC()
		item := model.LogicalModel{ID: uuid.NewString(), CreatedAt: now}
		creating := id == ""
		if !creating {
			previous, err := repo.LogicalModel(id)
			if err != nil {
				return catalogNotFound(err, "模型")
			}
			if input.ExpectedRevisionID == "" {
				return &kernel.AppError{Status: 400, Code: 400, Reason: "expected_revision_required", Message: "必须提交预期模型版本"}
			}
			if previous.ActiveRevisionID != input.ExpectedRevisionID {
				return &kernel.AppError{Status: 409, Code: 409, Reason: "revision_conflict", Message: "模型已更新，请刷新后再发布"}
			}
			item = *previous
			if item.Code != input.Code {
				return kernel.BadAuthRequest("稳定模型标识不能修改")
			}
		}
		routes, err := preparePlatformLogicalRoutes(repo, spec, input.Routes, now)
		if err != nil {
			return err
		}
		item.Code, item.Name, item.Icon, item.Description = input.Code, input.Name, input.Icon, input.Description
		item.Capability, item.Enabled, item.SortOrder, item.UpdatedAt = spec.Capability, input.Enabled, input.SortOrder, now
		specJSON, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		defaultsJSON, err := json.Marshal(defaults)
		if err != nil {
			return err
		}
		revision := &model.LogicalModelRevision{ID: uuid.NewString(), LogicalModelID: item.ID, CapabilitySpecJSON: string(specJSON), DefaultOptionsJSON: string(defaultsJSON), CreatedBy: actorID, CreatedAt: now}
		if err := repo.SaveLogicalModelBundle(&item, revision, routes, creating); err != nil {
			return err
		}
		if err := catalogAudit(repo.DB(), actorID, "", "logical_model_published", map[string]any{"logicalModelId": item.ID, "revisionId": revision.ID, "version": revision.Version, "enabled": item.Enabled}); err != nil {
			return err
		}
		result, err = platformLogicalModelView(repo, item.ID)
		return err
	})
	return result, err
}

func preparePlatformLogicalRoutes(repo *repository.Repository, spec modelcatalog.CapabilitySpec, input []PlatformLogicalRouteInput, now time.Time) ([]model.LogicalModelRoute, error) {
	routes := make([]model.LogicalModelRoute, 0, len(input))
	capabilities := make([]modelcatalog.CapabilitySpec, 0, len(input))
	seen := map[string]bool{}
	for _, route := range input {
		if route.ChannelModelID == "" || seen[route.ChannelModelID] || route.Priority < -100000 || route.Priority > 100000 || route.Weight < 1 || route.Weight > 100000 {
			return nil, kernel.BadAuthRequest("渠道路由标识、优先级或权重无效")
		}
		seen[route.ChannelModelID] = true
		channelModel, err := repo.ChannelModel(route.ChannelModelID)
		if err != nil {
			return nil, catalogNotFound(err, "渠道模型")
		}
		if _, err := repo.AdminSystemChannel(channelModel.ChannelID); err != nil {
			return nil, catalogNotFound(err, "平台渠道")
		}
		if _, err := platformChannelView(repo, model.ModelChannel{ID: channelModel.ChannelID}); err != nil {
			return nil, err
		}
		capability, err := modelcatalog.ChannelModelCapabilitySpec(*channelModel)
		if err != nil {
			return nil, err
		}
		if capability.Capability != spec.Capability {
			return nil, kernel.BadAuthRequest("路由模型能力与逻辑模型不一致")
		}
		if route.Enabled {
			capabilities = append(capabilities, capability)
		}
		routes = append(routes, model.LogicalModelRoute{ID: uuid.NewString(), ChannelModelID: route.ChannelModelID, Enabled: route.Enabled, Priority: route.Priority, Weight: route.Weight, CreatedAt: now, UpdatedAt: now})
	}
	if len(capabilities) == 0 {
		return nil, kernel.BadAuthRequest("至少启用一条模型路由")
	}
	if err := modelcatalog.ValidateProductSpecWithinRoutes(spec, capabilities); err != nil {
		return nil, err
	}
	return routes, nil
}
