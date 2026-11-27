package credits

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platform"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/resource"
	"infinite-canvas/backend/internal/task"
)

// Service 统一模型发布、报价、生成准入与积分账本策略。
type Service struct {
	repo             *repository.Repository
	identity         *identity.Service
	tasks            *task.Service
	policy           func() (platform.RuntimePolicySetting, error)
	catalogProtocols func() map[string][]string
}

func New(repo *repository.Repository, identities *identity.Service, tasks *task.Service, policy func() (platform.RuntimePolicySetting, error)) *Service {
	return &Service{repo: repo, identity: identities, tasks: tasks, policy: policy}
}

func (s *Service) ConfigureCatalogProtocols(resolve func() map[string][]string) {
	s.catalogProtocols = resolve
}

func (s *Service) active(id string) error {
	account, err := s.identity.RequireActive(id)
	if err != nil {
		return err
	}
	if account.MustChangePassword {
		return kernel.Forbidden("请先修改临时密码")
	}
	return nil
}

func (s *Service) Authorize(id string) error { return s.active(id) }

func (s *Service) Validate(userID string, req task.CreateRequest) (task.CreateRequest, error) {
	if err := s.active(userID); err != nil {
		return req, err
	}
	if !req.PrepareOnly && req.QuoteID == "" {
		return req, kernel.BadAuthRequest("请先确认积分报价")
	}
	switch req.Type {
	case "canvas_text", "canvas_image", "canvas_video", "canvas_audio":
	default:
		return req, kernel.Forbidden("此任务类型未在平台开放")
	}
	if req.LogicalModelID != "" {
		return req, kernel.BadAuthRequest("请选择平台发布的模型")
	}
	input, err := task.NormalizeInput(req.Input)
	if err != nil {
		return req, err
	}
	// 仅接受创作参数，不能提交执行器、脚本、工具调用或前端伪造的文本结果。
	allowed := map[string]bool{"mode": true, "prompt": true, "config": true, "capabilityOptions": true, "referenceImages": true, "referenceVideos": true, "referenceAudios": true, "mask": true, "metadata": true, "textOptions": true, "textHistory": true, "retryOf": true}
	for key := range input {
		if !allowed[key] {
			return req, kernel.BadAuthRequest("不支持的生成参数：" + key)
		}
	}
	config, ok := input["config"].(map[string]any)
	if !ok {
		return req, kernel.BadAuthRequest("缺少平台模型配置")
	}
	if fmt.Sprint(input["mode"]) != strings.TrimPrefix(req.Type, "canvas_") {
		return req, kernel.BadAuthRequest("任务类型与生成模式不一致")
	}
	for _, key := range []string{"apiKey", "secretKey", "baseUrl", "headers", "credentialRef", "customBody", "extraBody", "endpoint", "url"} {
		if value, exists := config[key]; exists && value != nil && value != "" {
			return req, kernel.Forbidden("平台不接受自带模型凭据或地址")
		}
		delete(config, key)
	}
	if err := validateCreativeInput(input, config); err != nil {
		return req, err
	}
	if _, err := task.ClientOperationID(input); err != nil {
		return req, err
	}
	operationID, _ := task.ClientOperationID(input)
	if operationID == "" {
		return req, kernel.BadAuthRequest("缺少生成提交标识")
	}
	// 使用现有目录算法从数据库重建协议和规格，客户端不能指定上游模型。
	resolved, err := modelcatalog.ResolveSystemChannelModelSelection(input, req.Type, req.Operation, modelcatalog.TaskSelectLookup{SystemChannel: s.repo.SystemChannel, ChannelModelByKey: s.repo.ChannelModelByKey})
	if err != nil {
		return req, err
	}
	req.Input = resolved
	config = resolved["config"].(map[string]any)
	m, err := s.repo.ChannelModelByKey(fmt.Sprint(config["channelId"]), fmt.Sprint(config["model"]))
	if err != nil {
		return req, err
	}
	price, err := s.repo.CreditPrice(m.ID, stringValue(config["variantId"]))
	if err != nil || !price.Published {
		return req, kernel.Forbidden("此模型规格尚未发布积分价格")
	}
	if _, err := unitsFor(m.Capability, config); err != nil {
		return req, err
	}
	return req, nil
}

func stringValue(v any) string { value, _ := v.(string); return value }

func unitsFor(capability string, config map[string]any) (int64, error) {
	units := int64(1)
	if raw := config["count"]; raw != nil && fmt.Sprint(raw) != "" {
		parsed, err := strconv.ParseInt(fmt.Sprint(raw), 10, 64)
		if err != nil || parsed < 1 || parsed > 15 {
			return 0, kernel.BadAuthRequest("生成数量必须是 1～15 的整数")
		}
		units = parsed
	}
	if capability != "image" && units != 1 {
		return 0, kernel.BadAuthRequest("此模型每个任务只能生成一个结果")
	}
	if capability == "image" {
		return imageUnits(config, units)
	}
	return units, nil
}

func (s *Service) Quote(userID string, req task.CreateRequest) (*model.CreditQuote, error) {
	req.PrepareOnly = true
	prepared, err := s.tasks.CreateTask(userID, req)
	if err != nil {
		return nil, err
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(prepared.InputJSON), &input); err != nil {
		return nil, err
	}
	config, ok := input["config"].(map[string]any)
	if !ok {
		return nil, kernel.BadAuthRequest("模型配置无效")
	}
	m, err := s.repo.ChannelModelByKey(stringValue(config["channelId"]), stringValue(config["model"]))
	if err != nil {
		return nil, err
	}
	price, err := s.repo.CreditPrice(m.ID, stringValue(config["variantId"]))
	if err != nil || !price.Published {
		return nil, kernel.Forbidden("此模型规格尚未发布")
	}
	units, err := unitsFor(m.Capability, config)
	if err != nil {
		return nil, err
	}
	hash, err := task.ClientOperationHash(req)
	if err != nil {
		return nil, err
	}
	quote := &model.CreditQuote{ID: kernel.NewID(), UserID: userID, RequestHash: hash, PriceID: price.ID, PriceVersion: price.Version, ModelID: m.ID, UnitCredits: price.UnitCredits, Units: units, TotalCredits: units * price.UnitCredits, ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
	channel, err := s.repo.SystemChannel(m.ChannelID)
	if err != nil {
		return nil, err
	}
	quote.ModelUpdatedAt, quote.ChannelUpdatedAt = m.UpdatedAt, channel.UpdatedAt
	return quote, s.repo.CreateCreditQuote(quote)
}

func (s *Service) CreateAdmitted(row *model.Task, limit int, req task.CreateRequest) error {
	hash, err := task.ClientOperationHash(req)
	if err != nil {
		return err
	}
	policy, err := s.policy()
	if err != nil {
		return err
	}
	return s.repo.AdmitCreditTask(row, limit, req.QuoteID, hash, func(repo *repository.Repository) error {
		usage, err := repo.UserStorageUsage(row.UserID)
		if err != nil {
			return err
		}
		return resource.ValidateTaskStorageQuota(usage, int64(len(row.InputJSON)+len(row.Prompt)), policy.Resource)
	})
}

func (s *Service) Account(userID string) (*model.CreditAccount, error) {
	if err := s.active(userID); err != nil {
		return nil, err
	}
	return s.repo.CreditAccount(userID)
}

func (s *Service) Adjust(actor, userID, operationID, reason string, amount int64) error {
	if _, err := s.identity.RequireAdmin(actor); err != nil {
		return err
	}
	if amount == 0 || amount < -1_000_000_000_000 || amount > 1_000_000_000_000 || len(operationID) < 8 || len(operationID) > 80 || len(strings.TrimSpace(reason)) == 0 || len(reason) > 500 {
		return kernel.BadAuthRequest("积分数量、操作标识或调整原因无效")
	}
	return s.repo.AdjustCredits(actor, userID, operationID, strings.TrimSpace(reason), amount)
}

func (s *Service) SetPrice(actor string, price model.ModelCreditPrice) error {
	_, err := s.SetPrices(actor, []PriceRequest{{ModelID: price.ChannelModelID, VariantID: price.VariantID, UnitCredits: price.UnitCredits, Published: price.Published}})
	return err
}

func (s *Service) Ledger(userID string, page, limit int) ([]model.CreditLedger, int64, error) {
	if err := s.active(userID); err != nil {
		return nil, 0, err
	}
	if page < 1 || limit < 1 || limit > 100 {
		return nil, 0, kernel.BadAuthRequest("分页参数无效")
	}
	return s.repo.CreditLedgerPage(userID, (page-1)*limit, limit)
}

func (s *Service) Usage(actor, userID, state string, page, limit int) ([]model.CreditReservation, int64, error) {
	if _, err := s.identity.RequireAdmin(actor); err != nil {
		return nil, 0, err
	}
	if page < 1 || limit < 1 || limit > 100 {
		return nil, 0, kernel.BadAuthRequest("分页参数无效")
	}
	return s.repo.CreditUsagePage(userID, state, (page-1)*limit, limit)
}

func (s *Service) Prices(actor string) ([]model.ModelCreditPrice, error) {
	if _, err := s.identity.RequireAdmin(actor); err != nil {
		return nil, err
	}
	return s.repo.CreditPrices()
}
