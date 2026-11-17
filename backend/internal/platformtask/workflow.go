//go:build platform

package platformtask

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/task"
)

// WorkflowTasks 只供持久工作流调用；后台身份由不可重分配的工作区绑定解析，不伪造会话。
type WorkflowTasks struct{ service *Service }

func (s *Service) WorkflowTasks() *WorkflowTasks { return &WorkflowTasks{service: s} }

func (w *WorkflowTasks) Available() bool { return w.service.testUpstream != "" }

func (w *WorkflowTasks) account(ctx context.Context, workspaceID string) (string, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return "", kernel.Unauthorized("缺少工作区身份")
	}
	var account platformauth.Account
	err := w.service.repo.DB().WithContext(ctx).Model(&platformauth.Account{}).
		Select("platform_accounts.*").
		Joins("JOIN platform_account_workspaces ON platform_account_workspaces.account_id = platform_accounts.id").
		Where("platform_account_workspaces.workspace_id = ? AND platform_accounts.status = ? AND platform_accounts.must_change_password = ?", workspaceID, platformauth.StatusActive, false).
		Take(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", kernel.Unauthorized("平台账号或工作区绑定已失效")
	}
	return account.ID, err
}

func (w *WorkflowTasks) Create(ctx context.Context, workspaceID string, req task.CreateRequest) (*model.Task, error) {
	accountID, err := w.account(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	key, err := task.ClientOperationID(req.Input)
	if err != nil || !strings.HasPrefix(key, "canvas-workflow:") {
		return nil, kernel.BadAuthRequest("工作流步骤标识无效")
	}
	return w.service.admitAccount(ctx, accountID, workspaceID, req, workflowRequestKind, "")
}

func (w *WorkflowTasks) Retry(ctx context.Context, workspaceID, taskID, key string) (*model.Task, error) {
	accountID, err := w.account(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return w.service.retryAccount(ctx, accountID, workspaceID, taskID, key, workflowRetryRequestKind)
}

func (w *WorkflowTasks) Cancel(ctx context.Context, workspaceID, taskID string) error {
	if _, err := w.account(ctx, workspaceID); err != nil {
		return err
	}
	_, err := w.service.cancelOwned(ctx, workspaceID, taskID)
	return err
}
