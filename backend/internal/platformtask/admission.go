//go:build platform

package platformtask

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/task"
)

const requestKind = "task:create:canvas_text"
const retryRequestKind = "task:retry:canvas_text"
const workflowRequestKind = "workflow:step:canvas_text"
const workflowRetryRequestKind = "workflow:retry:canvas_text"

func internalKey(kind, key string) string {
	if kind == workflowRequestKind || kind == workflowRetryRequestKind {
		return key
	}
	return platformquota.Digest([]byte(kind + "\x00" + key))
}

func (s *Service) Create(ctx context.Context, p identity.Principal, req task.CreateRequest) (*model.Task, error) {
	return s.admit(ctx, p, req, requestKind, "")
}

func (s *Service) admit(ctx context.Context, p identity.Principal, req task.CreateRequest, kind, retryOf string) (*model.Task, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	return s.admitAccount(ctx, p.AccountID, p.WorkspaceID, req, kind, retryOf)
}

// admitAccount 供会话入口和持久工作流共用；账号绑定在额度事务内再次核验。
func (s *Service) admitAccount(ctx context.Context, accountID, workspaceID string, req task.CreateRequest, kind, retryOf string) (*model.Task, error) {
	if s.testUpstream == "" {
		return nil, unavailable()
	}
	input, err := validateCreate(req)
	if err != nil {
		return nil, err
	}
	req.Input = input
	key, err := task.ClientOperationID(input)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, &kernel.AppError{Status: 400, Code: 400, Reason: "idempotency_required", Message: "必须提供 clientOperationId"}
	}
	canonical, err := json.Marshal(struct {
		Request task.CreateRequest
		RetryOf string
	}{req, retryOf})
	if err != nil {
		return nil, kernel.BadAuthRequest("任务输入无法序列化")
	}
	ports := admissionPorts{}
	persist := &admissionPersistence{service: s, ctx: ctx, accountID: accountID, workspaceID: workspaceID, request: req, key: key, kind: kind, retryOf: retryOf, digest: platformquota.Digest(canonical)}
	core := task.NewService(admissionStore{task.NewStore(s.repo)}, task.Dependencies{
		Catalog: ports, Secrets: ports, Media: ports, Projects: ports, Policy: ports,
		Runtime: ports, TextReplay: s.replay, Persist: persist, Present: presenter{service: s}, Logs: s,
	})
	return core.CreateTask(workspaceID, req)
}

func validateCreate(req task.CreateRequest) (map[string]any, error) {
	if req.Type != "canvas_text" || req.Operation != "text_generation" || strings.TrimSpace(req.LogicalModelID) == "" {
		return nil, kernel.BadAuthRequest("当前平台执行器仅支持已授权逻辑模型的文本生成")
	}
	if req.Provider != "" || req.Model != "" || req.AdmissionID != "" || req.PrepareOnly {
		return nil, kernel.BadAuthRequest("平台任务不得覆盖服务端模型配置")
	}
	input, err := task.NormalizeInput(req.Input)
	if err != nil {
		return nil, err
	}
	for key := range input {
		switch key {
		case "mode", "prompt", "textHistory", "textOptions", "metadata", "config":
		default:
			return nil, kernel.BadAuthRequest("当前文本任务不支持输入字段：" + key)
		}
	}
	if mode, exists := input["mode"]; exists && mode != "text" {
		return nil, kernel.BadAuthRequest("文本任务必须使用 text 模式")
	}
	input["mode"] = "text"
	if prompt, exists := input["prompt"]; exists && prompt != req.Prompt {
		return nil, kernel.BadAuthRequest("文本提示词不一致")
	}
	input["prompt"] = req.Prompt
	if config, exists := input["config"]; exists {
		values, ok := config.(map[string]any)
		if !ok || len(values) != 0 {
			return nil, kernel.BadAuthRequest("平台任务不能提交上游 URL、Header、Key 或协议配置")
		}
	}
	metadata, ok := input["metadata"].(map[string]any)
	if !ok && input["metadata"] != nil {
		return nil, kernel.BadAuthRequest("任务 metadata 格式无效")
	}
	for key := range metadata {
		if key != "clientOperationId" {
			return nil, kernel.BadAuthRequest("当前文本任务不支持元数据字段：" + key)
		}
	}
	if options, exists := input["textOptions"]; exists {
		values, ok := options.(map[string]any)
		if !ok {
			return nil, kernel.BadAuthRequest("文本选项无效")
		}
		for key, value := range values {
			if key != "stream" && key != "thinking" {
				return nil, kernel.BadAuthRequest("文本选项无效")
			}
			if _, ok := value.(bool); !ok {
				return nil, kernel.BadAuthRequest("文本选项必须为布尔值")
			}
		}
	}
	if history, exists := input["textHistory"]; exists {
		rows, ok := history.([]any)
		if !ok || len(rows) > 100 {
			return nil, kernel.BadAuthRequest("文本历史格式或数量无效")
		}
		for _, item := range rows {
			row, ok := item.(map[string]any)
			if !ok || len(row) != 2 {
				return nil, kernel.BadAuthRequest("文本历史格式无效")
			}
			role, _ := row["role"].(string)
			_, contentOK := row["content"].(string)
			if !contentOK || (role != "user" && role != "assistant" && role != "system") {
				return nil, kernel.BadAuthRequest("文本历史格式无效")
			}
		}
	}
	return input, nil
}

type admissionPersistence struct {
	service                    *Service
	ctx                        context.Context
	accountID, workspaceID     string
	request                    task.CreateRequest
	key, kind, retryOf, digest string
}

func (p *admissionPersistence) CreateAdmitted(row *model.Task, limit int) error {
	admitted, err := p.service.quota.Admit(p.ctx, platformquota.Admission{
		AccountID: p.accountID, WorkspaceID: p.workspaceID,
		RequestKey: p.key, RequestKind: p.kind, RequestDigest: p.digest,
		LogicalModelID: p.request.LogicalModelID,
		ModelIntent:    modelcatalog.ModelRequestIntentFromTaskInput(p.request.Input, p.request.Type, p.request.Operation),
	}, func(tx *gorm.DB, selection *modelcatalog.PlatformAdmissionSelection, _ string) (*model.Task, error) {
		routed := selection.Routed
		row.LogicalModelID, row.LogicalModelRevisionID = routed.LogicalModel.ID, routed.Revision.ID
		row.RouteID, row.ChannelModelID, row.RouteRun = routed.Route.ID, routed.ChannelModel.ID, 1
		row.Provider, row.Model = "managed", routed.LogicalModel.Code
		key := internalKey(p.kind, p.key)
		row.ClientOperationID = &key
		frozen := modelcatalog.ApplyRoutedProviderSelection(p.request.Input, &routed)
		profile, err := modelcatalog.NormalizedChannelModelCapability(&routed.ChannelModel)
		if err != nil {
			return nil, err
		}
		config := frozen["config"].(map[string]any)
		upstreamModel := routed.ChannelModel.ProviderModelKey
		if routed.Variant != nil && routed.Variant.ProviderModelKey != "" {
			upstreamModel = routed.Variant.ProviderModelKey
		}
		if upstreamModel == "" {
			upstreamModel = routed.ChannelModel.ModelKey
		}
		config["model"], config["providerModelKey"] = upstreamModel, upstreamModel
		config["apiFormat"], config["interfaceType"] = string(routed.ChannelModel.Protocol), string(routed.ChannelModel.Protocol)
		config["capabilityConfig"] = profile
		if p.retryOf != "" {
			metadata := frozen["metadata"].(map[string]any)
			metadata["retryOf"] = p.retryOf
		}
		encoded, err := json.Marshal(frozen)
		if err != nil {
			return nil, err
		}
		row.InputJSON = string(encoded)
		if err := repository.New(tx).CreateTaskWithActiveLimit(row, limit); err != nil {
			if errors.Is(err, repository.ErrTaskScopeNotActive) {
				return nil, kernel.NotFound("项目或画布不存在")
			}
			return nil, err
		}
		return row, nil
	})
	if err != nil {
		return err
	}
	*row = *admitted.Task
	return nil
}

// 平台请求收据是唯一幂等权威；活跃数及关联归属均在准入事务内复核。
type admissionStore struct{ task.Store }

func (admissionStore) TaskByClientOperation(string, string) (*model.Task, error) { return nil, nil }
func (admissionStore) ActiveTaskCount(string) (int64, error)                     { return 0, nil }

// 凭据与媒体没有客户端入口；能力、任务数和项目归属统一由准入事务校验。
type admissionPorts struct{}

func (admissionPorts) Select(_ string, req task.SelectRequest) (task.SelectResult, error) {
	return task.SelectResult{Input: req.Input}, nil
}
func (admissionPorts) PrepareRetry(*model.Task, map[string]any) error              { return unavailable() }
func (admissionPorts) RequireCustomChannels(map[string]any) error                  { return nil }
func (admissionPorts) ValidateCapability(map[string]any) error                     { return nil }
func (admissionPorts) HasExecutableVideoConfig(map[string]any) bool                { return false }
func (admissionPorts) ResolveManaged(input map[string]any) (map[string]any, error) { return input, nil }
func (admissionPorts) Protect(map[string]any) error                                { return nil }
func (admissionPorts) DecryptInputJSON(raw string) (string, error)                 { return raw, nil }
func (admissionPorts) ContainsInlineData(map[string]any) bool                      { return false }
func (admissionPorts) ValidateTransport(string, map[string]any) error              { return nil }
func (admissionPorts) EnsureActive(string, string) error                           { return nil }
func (admissionPorts) ActiveTaskLimit() (int, error)                               { return 100, nil }
func (admissionPorts) IsDraining() bool                                            { return false }
func (admissionPorts) StopLocalWait(string)                                        {}
func (admissionPorts) Dispatch(func()) bool                                        { return false }
