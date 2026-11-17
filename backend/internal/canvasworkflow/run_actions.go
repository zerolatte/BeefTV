package canvasworkflow

import (
	"context"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

// Select records the chosen image; choosing twice never submits downstream work twice.
func (r *Runs) Select(user, id, node, resource string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var row model.CanvasWorkflowRun
	if err := r.repo.DB().Where("id = ? AND user_id = ?", id, user).First(&row).Error; err != nil {
		return Run{}, NotFound()
	}
	run, err := RunFromRow(row)
	if err != nil {
		return run, err
	}
	if run.Status != "running" && run.Status != "paused" {
		return run, BadRequest("当前工作流不在等待选择")
	}
	for i := range run.Steps {
		step := &run.Steps[i]
		if step.NodeID != node {
			continue
		}
		if step.Status == "succeeded" && step.ResourceID == resource {
			return run, nil
		}
		if step.Status != "waiting_selection" {
			return run, BadRequest("此步骤不在等待选图")
		}
		for _, candidate := range step.Candidates {
			if candidate.ResourceID != resource {
				continue
			}
			if _, err := r.resource(user, resource, "image"); err != nil {
				return run, err
			}
			step.ResourceID, step.Status = resource, "succeeded"
			if err := r.save(&row, run); err != nil {
				return run, err
			}
			return RunFromRow(row)
		}
		return run, BadRequest("请选择本轮生成的候选图片")
	}
	return run, BadRequest("选择步骤不存在")
}

func (r *Runs) Retry(user, id string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var row model.CanvasWorkflowRun
	if e := r.repo.DB().Where("user_id = ? AND id = ?", user, id).First(&row).Error; e != nil {
		return Run{}, NotFound()
	}
	run, e := RunFromRow(row)
	if e != nil {
		return Run{}, e
	}
	if r.host.Draining() {
		return Run{}, BadRequest("应用正在退出，请稍后重试")
	}
	// 暂停恢复保留执行身份，由调度器通过原准入端口重新校验当前授权及点数。
	if run.Status == "paused" && run.Reason == string(kernel.ReasonQuotaExceeded) {
		run.Status, run.Error, run.Reason = "queued", "", ""
		if e = r.save(&row, run); e != nil {
			return Run{}, e
		}
		return RunFromRow(row)
	}
	if run.Status != "failed" {
		return Run{}, BadRequest("只有失败或额度暂停的运行可以继续")
	}
	for i := range run.Steps {
		step := &run.Steps[i]
		if step.Status != "failed" {
			continue
		}
		if step.TaskID != "" {
			task, e := r.repo.TaskForUser(user, step.TaskID)
			if e != nil {
				return Run{}, e
			}
			if task.Status != model.TaskStatusQueued && task.Status != model.TaskStatusRunning {
				if task, e = r.host.RetryStep(row, step.NodeID, task); e != nil {
					return Run{}, e
				}
			}
			step.TaskID = task.ID
		}
		step.Status = "pending"
		step.Error = ""
	}
	run.Status = "queued"
	run.Error = ""
	run.Reason = ""
	if e = r.save(&row, run); e != nil {
		return Run{}, e
	}
	return RunFromRow(row)
}

func (r *Runs) Cancel(ctx context.Context, user, id string) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var row model.CanvasWorkflowRun
	if e := r.repo.DB().Where("user_id = ? AND id = ?", user, id).First(&row).Error; e != nil {
		return Run{}, NotFound()
	}
	run, e := RunFromRow(row)
	if e != nil {
		return Run{}, e
	}
	if run.Status == "succeeded" || run.Status == "cancelled" {
		return run, nil
	}
	if e = r.restorePendingRetryTasks(row, &run); e != nil {
		return Run{}, e
	}
	run.Status = "cancelled"
	run.Error, run.Reason = "", ""
	for i := range run.Steps {
		step := &run.Steps[i]
		if step.Status == "pending" || step.Status == "running" || step.Status == "waiting_selection" {
			if step.TaskID == "" {
				task, lookupErr := r.repo.TaskByClientOperation(user, StepOperation(row.ID, step.NodeID))
				if lookupErr != nil {
					return Run{}, lookupErr
				}
				if task != nil {
					step.TaskID = task.ID
				}
			}
			if step.TaskID != "" {
				if e = r.host.CancelTask(ctx, user, step.TaskID); e != nil {
					step.Error = "已停止后续步骤；当前上游任务无法立即取消，请在任务列表查看"
				}
			}
			step.Status = "cancelled"
		}
	}
	if e = r.save(&row, run); e != nil {
		return Run{}, e
	}
	return RunFromRow(row)
}
