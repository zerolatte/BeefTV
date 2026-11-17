//go:build platform

package platformtask

import (
	"context"
	"encoding/json"
	"strings"

	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/task"
)

// Retry 产生独立任务及意图；旧任务的收费、错误和成功结果始终保留。
func (s *Service) Retry(ctx context.Context, p identity.Principal, id, key string) (*model.Task, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	return s.retryAccount(ctx, p.AccountID, p.WorkspaceID, id, key, retryRequestKind)
}

func (s *Service) retryAccount(ctx context.Context, accountID, workspaceID, id, key, kind string) (*model.Task, error) {
	if _, err := task.ClientOperationID(map[string]any{"metadata": map[string]any{"clientOperationId": key}}); err != nil {
		return nil, err
	}
	if strings.TrimSpace(key) == "" {
		return nil, &kernel.AppError{Status: 400, Code: 400, Reason: "idempotency_required", Message: "必须提供 clientOperationId"}
	}
	previous, err := s.repo.TaskForUser(workspaceID, id)
	if err != nil {
		return nil, err
	}
	if s.testUpstream == "" {
		return nil, unavailable()
	}
	if previous.Type != "canvas_text" || (previous.Status != model.TaskStatusFailed && previous.Status != model.TaskStatusCancelled) {
		return nil, &kernel.AppError{Status: 409, Code: 409, Reason: "retry_not_allowed", Message: "只有已结束的失败或取消文本任务可重试"}
	}
	intent, err := s.quota.IntentForTask(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	switch intent.State {
	case platformquota.StateReleased, platformquota.StateCompensated, platformquota.StateSettled:
	default:
		return nil, &kernel.AppError{Status: 409, Code: 409, Reason: "submission_unknown", Message: "原执行意图尚未终结，请先核对原任务"}
	}
	if previous.ResultJSON != "" {
		return nil, &kernel.AppError{Status: 409, Code: 409, Reason: "recovery_required", Message: "原任务已有成功结果，请恢复交付"}
	}
	var previousInput map[string]any
	if err := json.Unmarshal([]byte(previous.InputJSON), &previousInput); err != nil {
		return nil, kernel.BadAuthRequest("原任务输入无法恢复")
	}
	input := map[string]any{"mode": "text", "metadata": map[string]any{"clientOperationId": key}}
	for _, field := range []string{"textHistory", "textOptions"} {
		if value, ok := previousInput[field]; ok {
			input[field] = value
		}
	}
	return s.admitAccount(ctx, accountID, workspaceID, task.CreateRequest{ProjectID: previous.ProjectID, Type: previous.Type, Operation: previous.Operation, Prompt: previous.Prompt, LogicalModelID: previous.LogicalModelID, Input: input}, kind, previous.ID)
}
