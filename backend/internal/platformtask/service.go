//go:build platform

// Package platformtask 把平台准入账本接到既有任务和文本执行器。
package platformtask

import (
	"context"
	"errors"
	"strings"
	"time"

	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/task"
	"infinite-canvas/backend/internal/taskruntime"
	"infinite-canvas/backend/internal/textreplay"
)

type Service struct {
	repo    *repository.Repository
	quota   *platformquota.Service
	catalog *admin.PlatformCatalog
	replay  *textreplay.Service
	reads   *task.Service
	runtime *taskruntime.Runtime
	// 仅测试包可设置模拟上游；正式构造器没有生成开关。
	testUpstream string
}

func New(repo *repository.Repository, quota *platformquota.Service, catalog *admin.PlatformCatalog) (*Service, error) {
	if repo == nil || repo.DB() == nil || quota == nil || catalog == nil {
		return nil, errors.New("平台任务装配不完整")
	}
	s := &Service{repo: repo, quota: quota, catalog: catalog}
	s.replay = textreplay.New(textreplay.NewStore(repo), textreplay.Dependencies{Logger: s})
	s.reads = task.NewService(task.NewStore(repo), task.Dependencies{Present: presenter{service: s}})
	s.runtime = taskruntime.New(taskruntime.Deps{Repository: leaseRepository{repo}, Executor: s})
	return s, nil
}

func requirePrincipal(p identity.Principal) error {
	if p.Mode != identity.ModePlatform || p.Validate() != nil {
		return kernel.Unauthorized("需要有效的平台会话")
	}
	return nil
}

func unavailable() error {
	return &kernel.AppError{Status: 403, Code: 403, Reason: "generation_unavailable", Message: "平台生成尚未开放"}
}

func (s *Service) Get(p identity.Principal, id string) (*model.Task, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	return s.reads.Get(p.WorkspaceID, id)
}

func (s *Service) List(p identity.Principal, options task.ListOptions) ([]task.Summary, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	return s.TasksWithOptions(p.WorkspaceID, options)
}

// TasksWithOptions 供项目读取领域使用已验证的工作区，复用同一份平台脱敏与状态投影。
func (s *Service) TasksWithOptions(workspaceID string, options task.ListOptions) ([]task.Summary, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, kernel.Unauthorized("缺少工作区身份")
	}
	return s.reads.TasksWithOptions(workspaceID, options)
}

func (s *Service) Logs(p identity.Principal, id string) ([]model.TaskLog, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	if _, err := s.repo.TaskForUser(p.WorkspaceID, id); err != nil {
		return nil, err
	}
	return s.reads.Logs(p.WorkspaceID, id)
}

func (s *Service) FindOperation(p identity.Principal, key string, scope ...string) (*model.Task, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	if _, err := task.ClientOperationID(map[string]any{"metadata": map[string]any{"clientOperationId": key}}); err != nil {
		return nil, err
	}
	kinds := []string{requestKind, retryRequestKind, workflowRequestKind, workflowRetryRequestKind}
	if len(scope) > 0 && scope[0] != "" {
		switch scope[0] {
		case "create":
			kinds = []string{requestKind}
		case "retry":
			kinds = []string{retryRequestKind}
		case "workflow-step":
			kinds = []string{workflowRequestKind}
		case "workflow-retry":
			kinds = []string{workflowRetryRequestKind}
		default:
			return nil, kernel.BadAuthRequest("任务操作类型无效")
		}
	}
	var row *model.Task
	for _, kind := range kinds {
		candidate, err := task.NewStore(s.repo).TaskByClientOperation(p.WorkspaceID, internalKey(kind, key))
		if err != nil {
			return nil, err
		}
		if candidate != nil {
			if row != nil {
				return nil, &kernel.AppError{Status: 409, Code: 409, Reason: "idempotency_scope_required", Message: "该标识同时用于多个操作，请指定操作类型"}
			}
			row = candidate
		}
	}
	if row == nil {
		return nil, kernel.NotFound("任务不存在")
	}
	return presenter{service: s}.Task(*row), nil
}

func (s *Service) TextReplay() *textreplay.Service { return s.replay }

// Recover 只重新交付已有成功结果，未知结果不得伪装成新生成。
func (s *Service) Recover(ctx context.Context, p identity.Principal, id string) (*model.Task, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	row, err := s.repo.TaskForUser(p.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	if row.Status == model.TaskStatusSucceeded {
		return presenter{service: s}.Task(*row), nil
	}
	if row.Type != "canvas_text" {
		return nil, &kernel.AppError{Status: 409, Code: 409, Reason: "platform_task_not_admitted", Message: "当前任务没有可恢复的平台文本执行"}
	}
	intent, err := s.quota.IntentForTask(ctx, p.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	if row.ResultJSON == "" && intent.State == platformquota.StateSettled {
		return nil, &kernel.AppError{Status: 409, Code: 409, Reason: kernel.ErrorReason(task.ReasonChargedResultUnrecoverable), Message: "上游已成功并结算，但完整结果未保存，无法恢复交付"}
	}
	if row.ResultJSON == "" {
		return nil, &kernel.AppError{Status: 409, Code: 409, Reason: "submission_unknown", Message: "没有可恢复的成功结果，请核对原任务"}
	}
	if intent.State == platformquota.StateReleased || intent.State == platformquota.StateCompensated || intent.State == platformquota.StateReserved {
		return nil, &kernel.AppError{Status: 409, Code: 409, Reason: "settlement_conflict", Message: "任务结果与执行意图不一致，请先核对原任务"}
	}
	if err := s.repo.ResumePlatformTextDelivery(ctx, p.WorkspaceID, id); err != nil {
		return nil, err
	}
	return s.Get(p, id)
}

func (s *Service) Cancel(ctx context.Context, p identity.Principal, id string) (*model.Task, error) {
	if err := requirePrincipal(p); err != nil {
		return nil, err
	}
	return s.cancelOwned(ctx, p.WorkspaceID, id)
}

func (s *Service) cancelOwned(ctx context.Context, workspaceID, id string) (*model.Task, error) {
	row, err := s.repo.TaskForUser(workspaceID, id)
	if err != nil {
		return nil, err
	}
	intent, err := s.quota.IntentForTask(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if row.Status == model.TaskStatusQueued {
		// 先终结未提交的意图，发送前围栏会阻止并发领取的 worker。
		if err := s.quota.CancelUnsubmitted(ctx, workspaceID, intent.ID); err != nil {
			return nil, err
		}
		_, err = task.NewStore(s.repo).CancelIfStatus(workspaceID, id, model.TaskStatusQueued, time.Now())
		if err != nil {
			return nil, err
		}
		return s.reads.Get(workspaceID, id)
	}
	return nil, &kernel.AppError{Status: 409, Code: 409, Reason: "submission_unknown", Message: "任务可能已提交，保留原任务等待结果确认"}
}

func (s *Service) Log(workspace, taskID, level, message, _ string) error {
	// 平台日志不保存上游正文、请求头或密钥。
	return s.repo.AppendPlatformTaskLog(workspace, taskID, level, message)
}

type leaseRepository struct{ *repository.Repository }

func (r leaseRepository) ClaimNext(ctx context.Context, owner string, ttl time.Duration) (*model.Task, error) {
	return r.WithContext(ctx).ClaimNextPlatformTask(owner, ttl)
}
func (r leaseRepository) RenewLease(ctx context.Context, id, owner string, ttl time.Duration) error {
	return r.WithContext(ctx).RenewTaskLease(id, owner, ttl)
}
func (r leaseRepository) ReleaseLease(id, owner string) error { return r.ReleaseTaskLease(id, owner) }
