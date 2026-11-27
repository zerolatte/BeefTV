package platformadmin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/plugins"
	"infinite-canvas/backend/internal/protocol"
)

type ChannelRequest struct {
	modelcatalog.ChannelRequest
	InitialModels []ModelRequest `json:"initialModels"`
}

// 组合保存只编辑单价；发布必须通过独立价格操作明确确认。
type PriceRequest struct {
	UnitCredits int64 `json:"unitCredits"`
}

func (r *PriceRequest) UnmarshalJSON(data []byte) error {
	var value struct {
		UnitCredits *int64 `json:"unitCredits"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if value.UnitCredits == nil {
		return fmt.Errorf("请填写规格积分价格，零积分需要明确填写 0")
	}
	r.UnitCredits = *value.UnitCredits
	return nil
}

type VariantRequest struct {
	modelcatalog.ChannelModelVariantRequest
	Price *PriceRequest `json:"price,omitempty"`
}

type ModelRequest struct {
	ModelKey         string                              `json:"modelKey"`
	ProviderModelKey string                              `json:"providerModelKey"`
	DisplayName      string                              `json:"displayName"`
	Icon             string                              `json:"icon"`
	Capability       string                              `json:"capability"`
	Protocol         string                              `json:"protocol"`
	Enabled          *bool                               `json:"enabled"`
	CapabilityConfig *modelcatalog.ModelCapabilityConfig `json:"capabilityConfig"`
	Variants         []VariantRequest                    `json:"variants"`
}

func (r *ModelRequest) UnmarshalJSON(data []byte) error {
	type plain ModelRequest
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("模型配置包含不支持的字段或字段类型无效：%w", err)
	}
	*r = ModelRequest(value)
	return nil
}

func (r ModelRequest) contract() modelcatalog.ChannelModelRequest {
	variants := make([]modelcatalog.ChannelModelVariantRequest, len(r.Variants))
	for i := range r.Variants {
		variants[i] = r.Variants[i].ChannelModelVariantRequest
	}
	return modelcatalog.ChannelModelRequest{ModelKey: r.ModelKey, ProviderModelKey: r.ProviderModelKey, DisplayName: r.DisplayName, Icon: r.Icon, Capability: r.Capability, Protocol: r.Protocol, Enabled: r.Enabled, CapabilityConfig: r.CapabilityConfig, Variants: variants}
}

type ChannelView struct {
	modelcatalog.PublicModelChannel
	TotalModels     int64 `json:"totalModels"`
	AvailableModels int64 `json:"availableModels"`
	HasHeaders      bool  `json:"hasHeaders"`
}

type ChannelPage struct {
	Channels []ChannelView `json:"channels"`
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"pageSize"`
}

type Readiness struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type VariantView struct {
	model.ChannelModelVariant
	Selector map[string]string       `json:"selector"`
	Price    *model.ModelCreditPrice `json:"price"`
}

type ModelView struct {
	model.ChannelModel
	Variants  []VariantView           `json:"variants"`
	Price     *model.ModelCreditPrice `json:"price"`
	Readiness Readiness               `json:"readiness"`
}

type ModelPage struct {
	Items    []ModelView `json:"items"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
}

type ModelQuery struct {
	Page, PageSize              int
	Keyword, Capability, Status string
}

type ProbeRequest struct {
	ChannelID     string                       `json:"channelId"`
	BaseURL       string                       `json:"baseUrl"`
	APIKey        string                       `json:"apiKey"`
	SecretKey     string                       `json:"secretKey"`
	Headers       []modelcatalog.ChannelHeader `json:"headers"`
	CatalogFormat string                       `json:"catalogFormat"`
}

type ProbeResult struct {
	Models           []modelcatalog.ChannelModelCatalogItem `json:"models"`
	ExistingModelIDs []string                               `json:"existingModelIds"`
	DurationMs       int64                                  `json:"durationMs"`
}

type ProtocolView struct {
	plugins.ProviderCatalogItem
	Parameters    []protocol.Parameter `json:"parameters,omitempty"`
	Description   string               `json:"description,omitempty"`
	Documentation string               `json:"documentation,omitempty"`
}
