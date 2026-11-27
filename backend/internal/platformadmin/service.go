package platformadmin

import (
	"errors"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/plugins"
	"infinite-canvas/backend/internal/protocol"
	"infinite-canvas/backend/internal/repository"
)

type SecretCodec interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}
type PluginCatalog interface {
	ProviderCatalog(string, string, bool) []plugins.ProviderCatalogItem
	Registry() *protocol.Registry
	LookupWorkflow(string, protocol.Capability, string) (protocol.ManifestWorkflow, bool)
}
type Options struct {
	Secrets SecretCodec
	Plugins PluginCatalog
}
type Service struct {
	auth    *identity.Service
	repo    *repository.Repository
	options Options
}

func New(auth *identity.Service, repo *repository.Repository, options Options) *Service {
	return &Service{auth: auth, repo: repo, options: options}
}

func pageValues(page, size, fallback int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = fallback
	}
	return page, size
}

func catalogError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return kernel.NotFound("渠道或模型不存在，请刷新后重试")
	}
	return err
}

func (s *Service) Channels(actor string, q admin.ListQuery) (*ChannelPage, error) {
	if _, err := s.auth.RequireAdmin(actor); err != nil {
		return nil, err
	}
	page, size := pageValues(q.Page, q.Limit, 10)
	if q.Status != "" && q.Status != "enabled" && q.Status != "disabled" {
		return nil, kernel.BadAuthRequest("渠道状态无效")
	}
	rows, total, err := s.repo.PlatformChannels(repository.PlatformCatalogQuery{Page: page, PageSize: size, Keyword: strings.TrimSpace(q.Keyword), Status: q.Status}, s.protocolIDs())
	if err != nil {
		return nil, err
	}
	result := &ChannelPage{Channels: []ChannelView{}, Total: total, Page: page, PageSize: size}
	for _, row := range rows {
		result.Channels = append(result.Channels, channelView(row))
	}
	return result, nil
}

func channelView(row repository.PlatformChannelSummary) ChannelView {
	public := modelcatalog.PublicChannel(row.ModelChannel, true, nil)
	// 列表不携带请求头值；编辑未提供 headers 时由服务器保留原配置。
	public.Headers, public.Models, public.ModelProfiles = nil, nil, nil
	headers, _ := modelcatalog.ParseChannelHeadersJSON(row.HeadersJSON)
	return ChannelView{PublicModelChannel: public, TotalModels: row.TotalModels, AvailableModels: row.AvailableModels, HasHeaders: len(headers) > 0}
}

func (s *Service) channel(id string) (*model.ModelChannel, error) {
	if strings.TrimSpace(id) == "" {
		return nil, kernel.NotFound("渠道不存在")
	}
	row, err := s.repo.AdminSystemChannel(id)
	return row, catalogError(err)
}

func (s *Service) Models(actor, channelID string, q ModelQuery) (*ModelPage, error) {
	if _, err := s.auth.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if _, err := s.channel(channelID); err != nil {
		return nil, err
	}
	if q.Capability != "" && modelcatalog.NormalizeCapability(q.Capability) == "" {
		return nil, kernel.BadAuthRequest("模型能力类型无效")
	}
	if q.Status != "" && q.Status != "enabled" && q.Status != "disabled" && readinessReasons[q.Status] == "" {
		return nil, kernel.BadAuthRequest("模型状态无效")
	}
	page, size := pageValues(q.Page, q.PageSize, 20)
	rows, total, err := s.repo.PlatformModels(channelID, repository.PlatformCatalogQuery{Page: page, PageSize: size, Keyword: strings.TrimSpace(q.Keyword), Capability: q.Capability, Status: q.Status}, s.protocolIDs())
	if err != nil {
		return nil, err
	}
	items, err := modelViews(s.repo, rows)
	if err != nil {
		return nil, err
	}
	return &ModelPage{Items: items, Total: total, Page: page, PageSize: size}, nil
}

var readinessReasons = map[string]string{
	"available": "全部启用规格已发布，可供用户生成", "partially_published": "部分启用规格已发布，可使用已发布规格",
	"channel_disabled": "渠道已停用", "model_disabled": "模型已停用", "incomplete": "协议、能力或启用规格尚未配置完整", "unpublished": "尚未发布有效规格的积分价格",
}

func modelViews(repo *repository.Repository, rows []repository.PlatformModelSummary) ([]ModelView, error) {
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	prices, err := repo.CreditPricesForModels(ids)
	if err != nil {
		return nil, err
	}
	priceMap := map[string]*model.ModelCreditPrice{}
	for i := range prices {
		priceMap[prices[i].ChannelModelID+":"+prices[i].VariantID] = &prices[i]
	}
	result := make([]ModelView, 0, len(rows))
	for _, row := range rows {
		config, err := modelcatalog.DecodeModelCapabilityConfig(row.CapabilityConfigJSON)
		if err != nil {
			return nil, kernel.NewAppError(500, "模型能力配置损坏，请修复后重试")
		}
		if config != nil {
			row.CapabilityConfig = map[string]any{"version": config.Version}
			if config.Text != nil {
				row.CapabilityConfig["text"] = config.Text
			}
			if config.Image != nil {
				row.CapabilityConfig["image"] = config.Image
			}
			if config.Video != nil {
				row.CapabilityConfig["video"] = config.Video
			}
		}
		variants := make([]VariantView, len(row.Variants))
		for i, variant := range row.Variants {
			selector := variant.Selector
			if selector == nil {
				selector = map[string]string{}
			}
			variants[i] = VariantView{ChannelModelVariant: variant, Selector: selector, Price: priceMap[row.ID+":"+variant.ID]}
		}
		result = append(result, ModelView{ChannelModel: row.ChannelModel, Variants: variants, Price: priceMap[row.ID+":"], Readiness: Readiness{row.ReadinessState, readinessReasons[row.ReadinessState]}})
	}
	return result, nil
}

func audit(actor, subject, action string) model.PlatformAudit {
	return model.PlatformAudit{ID: kernel.NewID(), ActorID: actor, SubjectID: subject, Action: action, Reason: "管理员更新平台模型配置"}
}
