package canvasworkflow

import (
	"errors"
	"strings"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

type retryReceipt struct {
	Applied bool              `json:"applied"`
	Tasks   map[string]string `json:"tasks,omitempty"`
}

// 收据随运行状态持久化，不进入公开 Run JSON，也不需要变更业务表结构。
type runState struct {
	Run
	RetryRequests map[string]retryReceipt `json:"retryRequests,omitempty"`
}

type retryOperationHost interface {
	RetryStepWithOperation(model.CanvasWorkflowRun, string, *model.Task, string) (*model.Task, error)
}

// RetryWithOperation 在提交任何重试前固定原任务集合，避免重启或超时把同一批次变成新生成。
func (r *Runs) RetryWithOperation(user, id, operation string) (Run, error) {
	if strings.TrimSpace(operation) == "" || len(operation) > 128 {
		return Run{}, &kernel.AppError{Status: 400, Code: 400, Reason: "idempotency_required", Message: "必须提供有效的 clientOperationId"}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var row model.CanvasWorkflowRun
	if err := r.repo.DB().Where("user_id = ? AND id = ?", user, id).First(&row).Error; err != nil {
		return Run{}, NotFound()
	}
	run, err := RunFromRow(row)
	if err != nil {
		return Run{}, err
	}
	if receipt, exists := run.retryRequests[operation]; exists && receipt.Applied {
		return run, nil
	}
	if r.host.Draining() {
		return Run{}, BadRequest("应用正在退出，请稍后重试")
	}
	if run.retryRequests == nil {
		run.retryRequests = map[string]retryReceipt{}
	}
	receipt, exists := run.retryRequests[operation]
	if exists && run.Status != "failed" && run.Status != "paused" {
		return Run{}, BadRequest("当前运行已结束或取消，不能继续未完成的重试")
	}
	if !exists {
		for _, pending := range run.retryRequests {
			if !pending.Applied {
				return Run{}, &kernel.AppError{Status: 409, Code: 409, Reason: "retry_in_progress", Message: "请使用原操作标识继续未完成的工作流重试"}
			}
		}
		if len(run.retryRequests) >= 1000 {
			return Run{}, BadRequest("当前运行的重试次数已达上限，请创建新的运行")
		}
		if run.Status != "failed" && !(run.Status == "paused" && run.Reason == string(kernel.ReasonQuotaExceeded)) {
			return Run{}, BadRequest("只有失败或额度暂停的运行可以继续")
		}
		receipt = retryReceipt{Tasks: map[string]string{}}
		if run.Status == "failed" {
			for _, step := range run.Steps {
				if step.Status == "failed" {
					receipt.Tasks[step.NodeID] = step.TaskID
				}
			}
		}
		run.retryRequests[operation] = receipt
		if err := r.save(&row, run); err != nil {
			return Run{}, err
		}
	}
	for i := range run.Steps {
		step := &run.Steps[i]
		originalID, retry := receipt.Tasks[step.NodeID]
		if !retry {
			continue
		}
		if originalID != "" {
			original, err := r.repo.TaskForUser(user, originalID)
			if err != nil {
				return Run{}, err
			}
			task := original
			if original.Status != model.TaskStatusQueued && original.Status != model.TaskStatusRunning && original.Status != model.TaskStatusSucceeded {
				host, ok := r.host.(retryOperationHost)
				if !ok {
					return Run{}, BadRequest("当前工作流执行器不支持持久重试")
				}
				key := StepOperation(row.ID+":"+operation, step.NodeID)
				task, err = host.RetryStepWithOperation(row, step.NodeID, original, key)
				if err != nil {
					var admissionError *kernel.AppError
					if errors.As(err, &admissionError) && admissionError.Reason == kernel.ReasonQuotaExceeded {
						run.Status, run.Reason = "paused", string(kernel.ReasonQuotaExceeded)
						run.Error = "AI 点数不足，追加点数后可继续未完成步骤"
						if saveErr := r.save(&row, run); saveErr != nil {
							return Run{}, saveErr
						}
					}
					return Run{}, err
				}
			}
			if task == nil || task.UserID != user {
				return Run{}, BadRequest("工作流重试没有返回当前工作区的任务")
			}
			step.TaskID = task.ID
		}
		step.Status, step.Error = "pending", ""
	}
	receipt.Applied = true
	run.retryRequests[operation] = receipt
	run.Status, run.Error, run.Reason = "queued", "", ""
	if err := r.save(&row, run); err != nil {
		return Run{}, err
	}
	return RunFromRow(row)
}

func (r Run) awaitingRetry(nodeID string) bool {
	for _, request := range r.retryRequests {
		if _, exists := request.Tasks[nodeID]; exists && !request.Applied {
			return true
		}
	}
	return false
}

// 取消前找回已准入但未写入步骤状态的重试任务，避免取消运行后仍有孤立任务提交上游。
func (r *Runs) restorePendingRetryTasks(row model.CanvasWorkflowRun, run *Run) error {
	for operation, receipt := range run.retryRequests {
		if receipt.Applied {
			continue
		}
		for i := range run.Steps {
			step := &run.Steps[i]
			if _, exists := receipt.Tasks[step.NodeID]; !exists {
				continue
			}
			key := StepOperation(row.ID+":"+operation, step.NodeID)
			task, err := r.repo.TaskByClientOperation(row.UserID, key)
			if err != nil {
				return err
			}
			if task != nil {
				step.TaskID, step.Status = task.ID, "pending"
			}
		}
	}
	return nil
}
