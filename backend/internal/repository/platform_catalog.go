package repository

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

type PlatformCatalogQuery struct {
	Page, PageSize              int
	Keyword, Capability, Status string
}

type PlatformChannelSummary struct {
	model.ModelChannel
	TotalModels     int64
	AvailableModels int64
}

type PlatformModelSummary struct {
	model.ChannelModel
	ReadinessState string
}

// 平台目录、管理统计与筛选复用同一个 SQL 投影，避免已删除规格的旧价格重新发布模型。
func (r *Repository) platformModelProjection(protocols map[string][]string) *gorm.DB {
	protocolRules := []string{}
	args := []any{}
	for _, capability := range []string{"text", "image", "video", "audio"} {
		ids := protocols[capability]
		if len(ids) == 0 {
			continue
		}
		protocolRules = append(protocolRules, "(cm.capability = ? AND cm.protocol IN ?)")
		args = append(args, capability, ids)
	}
	protocolValid := "0 = 1"
	if len(protocolRules) > 0 {
		protocolValid = "(" + strings.Join(protocolRules, " OR ") + ")"
	}
	// 图片与视频的零提示词上限表示交由上游校验，不能因此排除已配置模型。
	configValid := `CASE WHEN cm.capability = 'audio' THEN 1 WHEN json_valid(cm.capability_config_json) THEN
		json_type(cm.capability_config_json, '$.' || cm.capability) = 'object'
		AND json_type(cm.capability_config_json, '$.' || cm.capability || '.references.promptMaxChars') = 'integer'
		AND json_extract(cm.capability_config_json, '$.' || cm.capability || '.references.promptMaxChars')
			BETWEEN CASE WHEN cm.capability = 'text' THEN 1 ELSE 0 END AND 1000000
		AND CASE WHEN cm.capability = 'video' THEN
			COALESCE(json_extract(cm.capability_config_json, '$.video.references.promptMaxUtf16Units'), 0) BETWEEN 0 AND 1000000
		ELSE 1 END ELSE 0 END`
	variantCount := `(SELECT COUNT(*) FROM channel_model_variants v WHERE v.channel_model_id = cm.id AND v.deleted_at IS NULL)`
	enabledCount := `(SELECT COUNT(*) FROM channel_model_variants v WHERE v.channel_model_id = cm.id AND v.deleted_at IS NULL AND v.enabled = 1)`
	publishedCount := `(SELECT COUNT(*) FROM channel_model_variants v JOIN model_credit_prices p ON p.channel_model_id = cm.id AND p.variant_id = v.id AND p.published = 1 WHERE v.channel_model_id = cm.id AND v.deleted_at IS NULL AND v.enabled = 1)`
	legacyPublished := `EXISTS (SELECT 1 FROM model_credit_prices p WHERE p.channel_model_id = cm.id AND p.variant_id = '' AND p.published = 1)`
	state := fmt.Sprintf(`CASE WHEN ch.enabled = 0 THEN 'channel_disabled' WHEN cm.enabled = 0 THEN 'model_disabled'
		WHEN NOT (%s) OR NOT COALESCE((%s), 0) OR (%s > 0 AND %s = 0) THEN 'incomplete'
		WHEN %s = 0 THEN CASE WHEN %s THEN 'available' ELSE 'unpublished' END
		WHEN %s = 0 THEN 'unpublished' WHEN %s < %s THEN 'partially_published' ELSE 'available' END`,
		protocolValid, configValid, variantCount, enabledCount, variantCount, legacyPublished, publishedCount, publishedCount, enabledCount)
	return r.db.Table("channel_models cm").Select("cm.*, "+state+" AS readiness_state", args...).
		Joins("JOIN model_channels ch ON ch.id = cm.channel_id AND ch.deleted_at IS NULL AND ch.scope = ?", model.ChannelScopeSystem).
		Where("cm.deleted_at IS NULL")
}

func (r *Repository) PlatformChannels(q PlatformCatalogQuery, protocols map[string][]string) ([]PlatformChannelSummary, int64, error) {
	query := r.db.Model(&model.ModelChannel{}).Where("scope = ?", model.ChannelScopeSystem)
	if q.Keyword != "" {
		pattern := "%" + strings.ToLower(q.Keyword) + "%"
		query = query.Where("lower(name) LIKE ? OR lower(public_alias) LIKE ? OR lower(base_url) LIKE ?", pattern, pattern, pattern)
	}
	if q.Status == "enabled" || q.Status == "disabled" {
		query = query.Where("enabled = ?", q.Status == "enabled")
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	channels := []model.ModelChannel{}
	if err := query.Order("created_at desc, id desc").Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize).Find(&channels).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]string, len(channels))
	for i := range channels {
		ids[i] = channels[i].ID
	}
	type counts struct {
		ChannelID                    string
		TotalModels, AvailableModels int64
	}
	stats := []counts{}
	if len(ids) > 0 {
		err := r.db.Table("(?) AS catalog", r.platformModelProjection(protocols)).Select("channel_id, COUNT(*) AS total_models, SUM(CASE WHEN readiness_state IN ('available', 'partially_published') THEN 1 ELSE 0 END) AS available_models").Where("channel_id IN ?", ids).Group("channel_id").Scan(&stats).Error
		if err != nil {
			return nil, 0, err
		}
	}
	byID := map[string]counts{}
	for _, item := range stats {
		byID[item.ChannelID] = item
	}
	result := make([]PlatformChannelSummary, len(channels))
	for i, ch := range channels {
		count := byID[ch.ID]
		result[i] = PlatformChannelSummary{ch, count.TotalModels, count.AvailableModels}
	}
	return result, total, nil
}

func (r *Repository) PlatformModels(channelID string, q PlatformCatalogQuery, protocols map[string][]string) ([]PlatformModelSummary, int64, error) {
	query := r.db.Table("(?) AS catalog", r.platformModelProjection(protocols)).Where("channel_id = ?", channelID)
	if q.Keyword != "" {
		pattern := "%" + strings.ToLower(q.Keyword) + "%"
		query = query.Where("lower(model_key) LIKE ? OR lower(provider_model_key) LIKE ? OR lower(display_name) LIKE ?", pattern, pattern, pattern)
	}
	if q.Capability != "" {
		query = query.Where("capability = ?", q.Capability)
	}
	if q.Status == "enabled" || q.Status == "disabled" {
		query = query.Where("enabled = ?", q.Status == "enabled")
	} else if q.Status != "" {
		query = query.Where("readiness_state = ?", q.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []PlatformModelSummary{}
	if q.PageSize > 0 {
		query = query.Limit(q.PageSize).Offset((q.Page - 1) * q.PageSize)
	}
	if err := query.Order("sort_order asc, created_at desc, id desc").Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	models := make([]model.ChannelModel, len(rows))
	for i := range rows {
		models[i] = rows[i].ChannelModel
	}
	if err := r.PopulateChannelModelVariants(models); err != nil {
		return nil, 0, err
	}
	for i := range rows {
		rows[i].ChannelModel = models[i]
	}
	return rows, total, nil
}

func (r *Repository) PlatformModel(id string, protocols map[string][]string) (*PlatformModelSummary, error) {
	var row PlatformModelSummary
	if err := r.db.Table("(?) AS catalog", r.platformModelProjection(protocols)).Where("id = ?", id).Take(&row).Error; err != nil {
		return nil, err
	}
	if err := r.PopulateChannelModelVariant(&row.ChannelModel); err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Repository) CreditPricesForModels(ids []string) ([]model.ModelCreditPrice, error) {
	rows := []model.ModelCreditPrice{}
	if len(ids) == 0 {
		return rows, nil
	}
	err := r.db.Where("channel_model_id IN ?", ids).Order("channel_model_id, variant_id").Find(&rows).Error
	return rows, err
}

func (r *Repository) PlatformCatalogTransaction(fn func(*Repository) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error { return fn(r.WithTx(tx)) })
}

func (r *Repository) SavePlatformChannel(channel *model.ModelChannel) error {
	return r.db.Save(channel).Error
}

// 单独启停仅更新状态，防止覆盖同时编辑的地址或凭据。
func (r *Repository) SetPlatformChannelEnabled(id string, enabled bool, audit model.PlatformAudit) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.ModelChannel{}).Where("id = ? AND scope = ?", id, model.ChannelScopeSystem).Updates(map[string]any{"enabled": enabled, "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return tx.Create(&audit).Error
	})
}
