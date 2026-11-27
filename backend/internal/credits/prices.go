package credits

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

type PriceRequest struct {
	ModelID     string `json:"modelId"`
	VariantID   string `json:"variantId"`
	UnitCredits int64  `json:"unitCredits"`
	Published   bool   `json:"published"`
}

func (r *PriceRequest) UnmarshalJSON(data []byte) error {
	var value struct {
		ModelID     string `json:"modelId"`
		VariantID   string `json:"variantId"`
		UnitCredits *int64 `json:"unitCredits"`
		Published   *bool  `json:"published"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if value.UnitCredits == nil || value.Published == nil {
		return kernel.BadAuthRequest("积分价格与发布状态必须明确填写")
	}
	*r = PriceRequest{ModelID: value.ModelID, VariantID: value.VariantID, UnitCredits: *value.UnitCredits, Published: *value.Published}
	return nil
}

// 批量价格先校验整组规格，再与审计一起提交；同值保存不失效已有报价。
func (s *Service) SetPrices(actor string, requests []PriceRequest) ([]model.ModelCreditPrice, error) {
	if _, err := s.identity.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if len(requests) == 0 || len(requests) > 100 {
		return nil, kernel.BadAuthRequest("一次请保存 1～100 个规格价格")
	}
	result := make([]model.ModelCreditPrice, 0, len(requests))
	err := s.repo.PlatformCatalogTransaction(func(repo *repository.Repository) error {
		seen := map[string]bool{}
		for i, req := range requests {
			if err := validatePrice(repo, req); err != nil {
				var appErr *kernel.AppError
				if !errors.As(err, &appErr) {
					return err
				}
				copy := *appErr
				copy.Message = fmt.Sprintf("第 %d 项价格：%s", i+1, appErr.Message)
				return &copy
			}
			key := req.ModelID + ":" + req.VariantID
			if seen[key] {
				return kernel.BadAuthRequest("同一模型规格不能重复提交价格")
			}
			seen[key] = true
		}
		for _, req := range requests {
			row, err := repo.CreditPrice(req.ModelID, req.VariantID)
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil && row.UnitCredits == req.UnitCredits && row.Published == req.Published {
				result = append(result, *row)
				continue
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				row = &model.ModelCreditPrice{ID: kernel.NewID(), ChannelModelID: req.ModelID, VariantID: req.VariantID, Version: 1}
			}
			row.UnitCredits, row.Published = req.UnitCredits, req.Published
			if err := repo.SaveCreditPrice(row, model.PlatformAudit{ID: kernel.NewID(), ActorID: actor, SubjectID: req.ModelID, Action: "model.price", Reason: "更新积分价格与发布状态"}); err != nil {
				return err
			}
			saved, err := repo.CreditPrice(req.ModelID, req.VariantID)
			if err != nil {
				return err
			}
			result = append(result, *saved)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validatePrice(repo *repository.Repository, req PriceRequest) error {
	if strings.TrimSpace(req.ModelID) == "" {
		return kernel.BadAuthRequest("模型标识不能为空")
	}
	if req.UnitCredits < 0 || req.UnitCredits > 1_000_000_000 {
		return kernel.BadAuthRequest("单价超出范围")
	}
	m, err := repo.ChannelModel(req.ModelID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return kernel.BadAuthRequest("模型不存在")
	}
	if err != nil {
		return err
	}
	if _, err := repo.AdminSystemChannel(m.ChannelID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return kernel.BadAuthRequest("模型所属系统渠道不存在")
		}
		return err
	}
	if err := repo.PopulateChannelModelVariant(m); err != nil {
		return err
	}
	valid := len(m.Variants) == 0 && req.VariantID == ""
	for _, variant := range m.Variants {
		if variant.ID == req.VariantID && variant.Enabled {
			valid = true
		}
	}
	if !valid {
		return kernel.BadAuthRequest("模型规格不存在或已停用")
	}
	return nil
}
