package credits

import (
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// Catalog 仅投影用户选型所需数据，绝不返回渠道地址、密钥、请求头与上游 SKU。
func (s *Service) Catalog(userID string) (map[string]any, error) {
	if err := s.active(userID); err != nil {
		return nil, err
	}
	channels, err := s.repo.SystemChannels(false)
	if err != nil {
		return nil, err
	}
	protocols := map[string][]string{}
	if s.catalogProtocols != nil {
		protocols = s.catalogProtocols()
	}
	items := []map[string]any{}
	for _, channel := range channels {
		models, _, err := s.repo.PlatformModels(channel.ID, repository.PlatformCatalogQuery{}, protocols)
		if err != nil {
			return nil, err
		}
		keys := []string{}
		profiles := []map[string]any{}
		for _, m := range models {
			if m.ReadinessState != "available" && m.ReadinessState != "partially_published" {
				continue
			}
			var capability map[string]any
			raw := m.CapabilityConfigJSON
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &capability); err != nil {
					return nil, err
				}
			}
			keys = append(keys, m.ModelKey)
			profiles = append(profiles, map[string]any{"model": m.ModelKey, "displayName": m.DisplayName, "capability": m.Capability, "protocol": m.Protocol, "capabilityConfig": capability})
		}
		if len(keys) > 0 {
			items = append(items, map[string]any{"id": channel.ID, "name": channel.PublicName(), "scope": model.ChannelScopeSystem, "enabled": true, "baseUrl": "", "apiKey": "", "apiFormat": channel.APIFormat, "models": keys, "modelProfiles": profiles})
		}
	}
	return map[string]any{"channels": items}, nil
}
