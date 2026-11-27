package platformadmin

import (
	"strings"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/protocol"
)

func (s *Service) protocolViews(capability string) []ProtocolView {
	result := []ProtocolView{}
	if s.options.Plugins == nil || s.options.Plugins.Registry() == nil {
		return result
	}
	registry := s.options.Plugins.Registry()
	for _, item := range s.options.Plugins.ProviderCatalog(string(protocol.SurfaceAdminSystemChannel), capability, false) {
		adapter, ok := registry.Resolve(item.ID)
		if !ok || item.Retired {
			continue
		}
		meta := adapter.Metadata()
		if !meta.Enabled || meta.UnavailableReason != "" {
			continue
		}
		result = append(result, ProtocolView{item, meta.Parameters, meta.Description, meta.Documentation})
	}
	return result
}

func (s *Service) Protocols(actor, capability string) ([]ProtocolView, error) {
	if _, err := s.auth.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if capability != "" && modelcatalog.NormalizeCapability(capability) == "" {
		return nil, kernel.BadAuthRequest("模型能力类型无效")
	}
	return s.protocolViews(capability), nil
}

func (s *Service) protocolIDs() map[string][]string { return ProtocolIDs(s.options.Plugins) }

// 目录的可执行协议集合由实时注册表给出，供管理列表与用户目录共用。
func ProtocolIDs(plugins PluginCatalog) map[string][]string {
	ids := map[string][]string{}
	if plugins == nil || plugins.Registry() == nil {
		return ids
	}
	for _, meta := range plugins.Registry().List(protocol.SurfaceAdminSystemChannel, "", false) {
		for _, capability := range meta.Categories {
			ids[string(capability)] = append(ids[string(capability)], protocol.ResolvableIDs(meta)...)
		}
	}
	return ids
}

func (s *Service) protocolMeta(id, capability string, existing bool) (protocol.Metadata, error) {
	if s.options.Plugins == nil || s.options.Plugins.Registry() == nil {
		return protocol.Metadata{}, kernel.BadAuthRequest("模型协议目录尚未就绪")
	}
	adapter, ok := s.options.Plugins.Registry().Resolve(strings.TrimSpace(id))
	if !ok {
		return protocol.Metadata{}, kernel.BadAuthRequest("请选择已安装的模型请求协议")
	}
	meta := adapter.Metadata()
	if !meta.Enabled || meta.UnavailableReason != "" {
		return meta, kernel.BadAuthRequest("所选模型协议不可用")
	}
	matchedScope, matchedCapability := false, false
	for _, scope := range meta.Scopes {
		matchedScope = matchedScope || scope == protocol.SurfaceAdminSystemChannel
	}
	for _, category := range meta.Categories {
		matchedCapability = matchedCapability || string(category) == capability
	}
	if !matchedScope || !matchedCapability {
		return meta, kernel.BadAuthRequest("模型能力与请求协议不匹配，或协议不支持系统渠道")
	}
	if !existing {
		selectable := false
		for _, item := range s.protocolViews(capability) {
			selectable = selectable || item.ID == meta.ID
		}
		if !selectable {
			return meta, kernel.BadAuthRequest("该模型协议已退役，请选择当前可用协议")
		}
	}
	return meta, nil
}

func (s *Service) Capability(actor string, draft modelcatalog.ModelCapabilityDraft) (*modelcatalog.ModelCapabilityConfig, error) {
	if _, err := s.auth.RequireAdmin(actor); err != nil {
		return nil, err
	}
	capability := modelcatalog.NormalizeCapability(draft.Capability)
	meta, err := s.protocolMeta(draft.Protocol, capability, false)
	if err != nil {
		return nil, err
	}
	if capability == "audio" {
		return nil, nil
	}
	draft.Protocol, draft.Capability = meta.ID, capability
	return modelcatalog.NewCapabilityNormalizer(s.options.Plugins.LookupWorkflow).NormalizeModelCapabilityDraft(draft)
}
