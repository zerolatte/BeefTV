package platformadmin

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/repository"
)

const maxModelBatch = 100

func (s *Service) DeleteChannel(actor, id string) error {
	if _, err := s.auth.RequireAdmin(actor); err != nil {
		return err
	}
	entry := audit(actor, id, "channel.delete")
	entry.Reason = "管理员删除渠道及关联模型配置"
	err := s.repo.DeletePlatformChannel(id, entry)
	if errors.Is(err, repository.ErrPlatformChannelInUse) {
		return &kernel.AppError{Status: 409, Code: 409, Reason: "channel_in_use", Message: "渠道仍有未完成任务或待核对积分，请等待任务结束，并在用量管理完成积分核对后重试"}
	}
	return catalogError(err)
}

func enabledOnly(req ChannelRequest) bool {
	r := req.ChannelRequest
	return r.Enabled != nil && r.Name == "" && r.BaseURL == "" && r.APIKey == "" && r.SecretKey == "" && r.PublicAlias == nil && r.SortOrder == nil && r.Headers == nil && r.Models == nil && r.ConcurrencyLimit == nil && r.UseGlobalConcurrency == nil && req.InitialModels == nil
}

func (s *Service) SaveChannel(actor, id string, req ChannelRequest) (*ChannelView, error) {
	if _, err := s.auth.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if len(req.InitialModels) > maxModelBatch {
		return nil, kernel.BadAuthRequest("一次最多添加 100 个模型")
	}
	if id != "" && len(req.InitialModels) > 0 {
		return nil, kernel.BadAuthRequest("已有渠道请通过批量模型接口添加模型")
	}
	if id != "" && enabledOnly(req) {
		if err := s.repo.SetPlatformChannelEnabled(id, *req.Enabled, audit(actor, id, "channel.status")); err != nil {
			return nil, catalogError(err)
		}
		return s.savedChannel(id)
	}
	if s.options.Secrets == nil {
		return nil, kernel.NewAppError(500, "渠道密钥存储尚未就绪")
	}
	createdID := id
	err := s.repo.PlatformCatalogTransaction(func(repo *repository.Repository) error {
		row := model.ModelChannel{ID: id, UserID: actor, Scope: model.ChannelScopeSystem, Enabled: true}
		action := "channel.create"
		if id == "" {
			row.ID = kernel.NewID()
		} else {
			existing, err := repo.AdminSystemChannel(id)
			if err != nil {
				return catalogError(err)
			}
			row = *existing
			if err := admin.DecryptSystemChannelSecrets(s.options.Secrets.Decrypt, &row); err != nil {
				return err
			}
			req.ChannelRequest = modelcatalog.MergeChannelRequest(req.ChannelRequest, row)
			action = "channel.update"
		}
		next, err := modelcatalog.ApplyChannelRequest(req.ChannelRequest, row)
		if err != nil {
			return err
		}
		prepared := make([]preparedModel, 0, len(req.InitialModels))
		seen := map[string]bool{}
		for index, input := range req.InitialModels {
			item, err := s.prepareModel(repo, &next, "", input)
			if err != nil {
				return modelInputError(index, input.ModelKey, err)
			}
			key := strings.ToLower(item.row.ModelKey)
			if seen[key] {
				return kernel.BadAuthRequest("渠道内模型标识不能重复：" + item.row.ModelKey)
			}
			seen[key] = true
			prepared = append(prepared, item)
		}
		if err := admin.EncryptSystemChannelSecrets(s.options.Secrets.Encrypt, &next); err != nil {
			return err
		}
		if err := repo.SavePlatformChannel(&next); err != nil {
			return err
		}
		for _, item := range prepared {
			if err := savePreparedModel(repo, actor, item); err != nil {
				return err
			}
		}
		if len(req.Models) > 0 && len(prepared) == 0 {
			if err := modelcatalog.SyncInitialChannelModelsInRepo(repo, &next, req.Models); err != nil {
				return err
			}
		}
		if len(prepared) > 0 {
			if err := repo.SyncChannelModelNames(next.ID, time.Now().UTC()); err != nil {
				return err
			}
		}
		if err := repo.RecordPlatformAudit(audit(actor, next.ID, action)); err != nil {
			return err
		}
		createdID = next.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.savedChannel(createdID)
}

func (s *Service) savedChannel(id string) (*ChannelView, error) {
	row, err := s.channel(id)
	if err != nil {
		return nil, err
	}
	_, total, err := s.repo.PlatformModels(id, repository.PlatformCatalogQuery{Page: 1, PageSize: 1}, s.protocolIDs())
	if err != nil {
		return nil, err
	}
	_, available, err := s.repo.PlatformModels(id, repository.PlatformCatalogQuery{Page: 1, PageSize: 1, Status: "available"}, s.protocolIDs())
	if err != nil {
		return nil, err
	}
	_, partial, err := s.repo.PlatformModels(id, repository.PlatformCatalogQuery{Page: 1, PageSize: 1, Status: "partially_published"}, s.protocolIDs())
	if err != nil {
		return nil, err
	}
	view := channelView(repository.PlatformChannelSummary{ModelChannel: *row, TotalModels: total, AvailableModels: available + partial})
	return &view, nil
}

type preparedModel struct {
	row      *model.ChannelModel
	variants []model.ChannelModelVariant
	prices   map[string]*PriceRequest
}

func (s *Service) prepareModel(repo *repository.Repository, channel *model.ModelChannel, id string, input ModelRequest) (preparedModel, error) {
	result := preparedModel{prices: map[string]*PriceRequest{}}
	var existing *model.ChannelModel
	if id != "" {
		var err error
		existing, err = repo.ChannelModelByID(channel.ID, id)
		if err != nil {
			return result, catalogError(err)
		}
	}
	req := input.contract()
	capability := modelcatalog.NormalizeCapability(req.Capability)
	meta, err := s.protocolMeta(req.Protocol, capability, existing != nil && string(existing.Protocol) == req.Protocol)
	if err != nil {
		return result, err
	}
	lookup := func(string) (modelcatalog.ProtocolMeta, bool) {
		return modelcatalog.ProtocolMeta{ID: meta.ID, Enabled: true, PrimaryCapability: capability}, true
	}
	key, providerKey, capability, protocol, err := modelcatalog.NormalizeChannelModelContract(lookup, channel, req)
	if err != nil {
		return result, err
	}
	if len([]rune(key)) > 120 || len([]rune(providerKey)) > 120 || len([]rune(req.DisplayName)) > 160 || len(req.Variants) > 100 {
		return result, kernel.BadAuthRequest("模型标识、名称或规格数量超出限制")
	}
	conflict, err := repo.ChannelModelByKeyIncludingDisabled(channel.ID, key)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	if conflict != nil && conflict.ID != id {
		return result, kernel.BadAuthRequest("该渠道已存在模型 " + key)
	}
	row, variants, err := modelcatalog.PrepareChannelModelSave(channel.ID, id, req, key, providerKey, capability, protocol, existing, func(string) (string, error) { return kernel.NewID(), nil })
	if err != nil {
		return result, err
	}
	for i, variant := range input.Variants {
		if variant.Price == nil {
			continue
		}
		if variant.Price.UnitCredits < 0 || variant.Price.UnitCredits > 1_000_000_000 {
			return result, kernel.BadAuthRequest("单价超出范围")
		}
		result.prices[variants[i].SelectorKey] = variant.Price
	}
	result.row, result.variants = row, variants
	return result, nil
}

func savePreparedModel(repo *repository.Repository, actor string, item preparedModel) error {
	if err := repo.SaveChannelModelWithVariants(item.row, item.variants); err != nil {
		return err
	}
	// 仓储按 selector 复用真实规格 ID，价格必须在该映射完成后写入。
	for _, variant := range item.variants {
		input := item.prices[variant.SelectorKey]
		if input == nil {
			continue
		}
		price, err := repo.CreditPrice(item.row.ID, variant.ID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && price.UnitCredits == input.UnitCredits {
			continue
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			price = &model.ModelCreditPrice{ID: kernel.NewID(), ChannelModelID: item.row.ID, VariantID: variant.ID, Version: 1, Published: false}
		}
		price.UnitCredits = input.UnitCredits
		if err := repo.SaveCreditPrice(price, audit(actor, item.row.ID, "model.price.edit")); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) SaveModel(actor, channelID, id string, input ModelRequest) (*ModelView, error) {
	if id != "" && modelEnabledOnly(input) {
		return s.setModelEnabled(actor, channelID, id, *input.Enabled)
	}
	items, err := s.saveModels(actor, channelID, id, []ModelRequest{input})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

func modelEnabledOnly(input ModelRequest) bool {
	return input.Enabled != nil && input.ModelKey == "" && input.ProviderModelKey == "" && input.DisplayName == "" && input.Icon == "" && input.Capability == "" && input.Protocol == "" && input.CapabilityConfig == nil && input.Variants == nil
}

func (s *Service) setModelEnabled(actor, channelID, id string, enabled bool) (*ModelView, error) {
	if _, err := s.auth.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if err := s.repo.SetPlatformModelEnabled(channelID, id, enabled, audit(actor, id, "model.status")); err != nil {
		return nil, catalogError(err)
	}
	row, err := s.repo.PlatformModel(id, s.protocolIDs())
	if err != nil {
		return nil, err
	}
	items, err := modelViews(s.repo, []repository.PlatformModelSummary{*row})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

func (s *Service) SaveModels(actor, channelID string, inputs []ModelRequest) ([]ModelView, error) {
	return s.saveModels(actor, channelID, "", inputs)
}

func (s *Service) saveModels(actor, channelID, id string, inputs []ModelRequest) ([]ModelView, error) {
	if _, err := s.auth.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if len(inputs) == 0 || len(inputs) > maxModelBatch {
		return nil, kernel.BadAuthRequest("一次请提交 1～100 个模型")
	}
	if s.options.Secrets == nil {
		return nil, kernel.NewAppError(500, "渠道密钥存储尚未就绪")
	}
	ids := []string{}
	err := s.repo.PlatformCatalogTransaction(func(repo *repository.Repository) error {
		channel, err := repo.AdminSystemChannel(channelID)
		if err != nil {
			return catalogError(err)
		}
		if err := admin.DecryptSystemChannelSecrets(s.options.Secrets.Decrypt, channel); err != nil {
			return err
		}
		prepared := make([]preparedModel, 0, len(inputs))
		seen := map[string]bool{}
		for index, input := range inputs {
			item, err := s.prepareModel(repo, channel, id, input)
			if err != nil {
				return modelInputError(index, input.ModelKey, err)
			}
			key := strings.ToLower(item.row.ModelKey)
			if seen[key] {
				return kernel.BadAuthRequest("渠道内模型标识不能重复：" + item.row.ModelKey)
			}
			seen[key] = true
			prepared = append(prepared, item)
		}
		for _, item := range prepared {
			if err := savePreparedModel(repo, actor, item); err != nil {
				return err
			}
			if err := repo.RecordPlatformAudit(audit(actor, item.row.ID, "model.save")); err != nil {
				return err
			}
			ids = append(ids, item.row.ID)
		}
		return repo.SyncChannelModelNames(channelID, time.Now().UTC())
	})
	if err != nil {
		return nil, err
	}
	rows := make([]repository.PlatformModelSummary, 0, len(ids))
	for _, id := range ids {
		row, err := s.repo.PlatformModel(id, s.protocolIDs())
		if err != nil {
			return nil, err
		}
		rows = append(rows, *row)
	}
	return modelViews(s.repo, rows)
}

func modelInputError(index int, key string, err error) error {
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) {
		return err
	}
	copy := *appErr
	copy.Message = fmt.Sprintf("第 %d 个模型（%s）：%s", index+1, strings.TrimSpace(key), appErr.Message)
	copy.Details = map[string]any{"modelIndex": index, "modelKey": key}
	return &copy
}
