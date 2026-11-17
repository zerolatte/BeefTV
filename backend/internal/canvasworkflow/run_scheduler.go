package canvasworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"strings"
	"time"
)

// StartScheduler advances queued and running runs every second until ctx ends.
func (r *Runs) StartScheduler(ctx context.Context) {
	r.mu.Lock()
	if *r.loop {
		r.mu.Unlock()
		return
	}
	*r.loop = true
	r.mu.Unlock()
	if !r.host.RunLoop(func(workerCtx context.Context) {
		defer func() { r.mu.Lock(); *r.loop = false; r.mu.Unlock() }()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-workerCtx.Done():
				return
			case <-timer.C:
				r.AdvanceAll(workerCtx)
			}
		}
	}) {
		r.mu.Lock()
		*r.loop = false
		r.mu.Unlock()
	}
}

func (r *Runs) save(row *model.CanvasWorkflowRun, run Run) error {
	run.UpdatedAt = row.UpdatedAt
	state, e := json.Marshal(runState{Run: run, RetryRequests: run.retryRequests})
	if e != nil {
		return e
	}
	// 暂停运行仍需对齐漏写的任务回执，无状态变化时不刷新持久记录或通知时间。
	if current, err := RunFromRow(*row); err == nil {
		previous, err := json.Marshal(runState{Run: current, RetryRequests: current.retryRequests})
		if err == nil && string(previous) == string(state) {
			return nil
		}
	}
	row.StateJSON = string(state)
	row.Status = run.Status
	return r.repo.DB().Save(row).Error
}

// AdvanceAll 推进运行，并在暂停期间继续收集已经准入的任务结果。
func (r *Runs) AdvanceAll(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil || r.host.Draining() {
		return
	}
	var rows []model.CanvasWorkflowRun
	if r.repo.DB().Where("status IN ?", []string{"queued", "running", "paused"}).Order("created_at ASC").Find(&rows).Error != nil {
		return
	}
	for i := range rows {
		if ctx.Err() != nil || r.host.Draining() {
			return
		}
		r.Advance(&rows[i])
	}
}

func incomingResource(n Node, d Definition, steps map[string]*Step) string {
	resource := ""
	for _, edge := range d.Edges {
		if edge.To == n.ID {
			resource = steps[edge.From].ResourceID
		}
	}
	return resource
}

// Advance moves one run forward. Callers hold the shared lock.
func (r *Runs) Advance(row *model.CanvasWorkflowRun) {
	run, e := RunFromRow(*row)
	fail := func(message string) {
		run.Status, run.Error, run.Reason = "failed", message, ""
		_ = r.save(row, run)
	}
	if e != nil {
		fail("工作流运行记录无法读取，请检查运行记录")
		return
	}
	if run.Status != "queued" && run.Status != "running" && run.Status != "paused" {
		return
	}
	paused := run.Status == "paused"
	var d Definition
	if json.Unmarshal([]byte(row.DefinitionJSON), &d) != nil {
		fail("工作流定义无法读取，请重新保存模板")
		return
	}
	if _, e = r.host.ReferenceIDs(row.RecipeCipher); e != nil {
		var recipeErr *RecipeError
		if errors.As(e, &recipeErr) && !recipeErr.Decrypt {
			fail("工作流配方无法读取，请重新保存模板")
		} else {
			fail("无法读取工作流配方，请重新保存模板")
		}
		return
	}
	nodes := map[string]Node{}
	steps := map[string]*Step{}
	for _, n := range d.Nodes {
		nodes[n.ID] = n
	}
	for i := range run.Steps {
		steps[run.Steps[i].NodeID] = &run.Steps[i]
	}
	if !paused {
		run.Status, run.Error, run.Reason = "running", "", ""
	}
	for i := range run.Steps {
		step := &run.Steps[i]
		n := nodes[step.NodeID]
		if n.Role == "constant" || n.Role == "input" || step.Status == "succeeded" {
			continue
		}
		if step.Status == "failed" {
			if paused && run.awaitingRetry(step.NodeID) {
				continue
			}
			run.Status = "failed"
			run.Error = step.Error
			break
		}
		ready := true
		for _, edge := range d.Edges {
			if edge.To == n.ID && steps[edge.From].Status != "succeeded" {
				ready = false
			}
		}
		if !ready {
			continue
		}
		if n.OperationID == "image.select" {
			if paused {
				continue
			}
			step.Candidates = imageCandidates(n, d, steps)
			if len(step.Candidates) == 1 {
				step.ResourceID, step.Status = step.Candidates[0].ResourceID, "succeeded"
			} else if len(step.Candidates) > 1 {
				step.Status = "waiting_selection"
			} else {
				step.Status, step.Error, run.Status, run.Error = "failed", "上游没有可选择的图片", "failed", "上游没有可选择的图片"
			}
			if r.save(row, run) != nil {
				return
			}
			continue
		}
		var task *model.Task
		admitting := false
		if step.TaskID != "" {
			task, e = r.repo.TaskForUser(row.UserID, step.TaskID)
		} else {
			op := StepOperation(row.ID, n.ID)
			task, e = r.repo.TaskByClientOperation(row.UserID, op)
			if e == nil && task == nil {
				if paused {
					continue
				}
				admitting = true
				switch {
				case strings.HasSuffix(n.OperationID, ".ai-upscale"):
					task, e = r.host.Upscale(row.UserID, op, n, incomingResource(n, d, steps))
				case n.OperationID == "image.describe":
					task, e = r.host.Describe(row.UserID, op, n, incomingResource(n, d, steps))
				case IsMediaOperation(n.OperationID):
					task, e = r.host.CanvasMedia(row.UserID, op, n, incomingResource(n, d, steps))
				case n.OperationID == "video.trim" || n.OperationID == "video.merge":
					task, e = r.timeline(row.UserID, op, n, d.Edges, steps)
				case n.Role == "depth" || n.OperationID == "video.depth":
					task, e = r.host.DepthCapture(row.UserID, op, incomingResource(n, d, steps))
				default:
					task, e = r.host.Generate(row.UserID, op, row.RecipeCipher, n, nodes, steps)
				}
			}
		}
		if e != nil {
			var admissionError *kernel.AppError
			if admitting && errors.As(e, &admissionError) && admissionError.Reason == kernel.ReasonQuotaExceeded {
				// 仅原子准入明确拒绝时暂停；查询失败或未知执行结果不能释放或重建任务。
				paused = true
				run.Status, run.Reason = "paused", string(kernel.ReasonQuotaExceeded)
				run.Error = "AI 点数不足，追加点数后可继续未完成步骤"
				if r.save(row, run) != nil {
					return
				}
				continue
			}
			step.Status = "failed"
			step.Error = "无法执行此步骤：" + r.host.FailureMessage(e.Error(), "", "")
			run.Status = "failed"
			run.Error = step.Error
			break
		}
		if task == nil {
			step.Status = "failed"
			step.Error = "原任务记录缺失，停止自动提交；请检查任务记录"
			run.Status = "failed"
			run.Error = step.Error
			break
		}
		step.TaskID = task.ID
		step.Status = "running"
		switch task.Status {
		case model.TaskStatusFailed, model.TaskStatusCancelled:
			step.Status = "failed"
			step.Error = r.host.FailureMessage(task.Error, task.Stage, task.ErrorCode)
			if step.Error == "" {
				step.Error = "任务已失败或取消"
			}
			run.Status = "failed"
			run.Error = step.Error
		case model.TaskStatusSucceeded:
			canonical, stored := r.host.Outputs(task)
			if nodes[step.NodeID].MediaType == "text" {
				step.Text = textOutput(task)
				if step.Text != "" {
					step.Status = "succeeded"
				}
			} else {
				r.collectOutputs(row, step, n, d, nodes, task, canonical, stored, &run)
			}
			if step.Status != "succeeded" && (deliveryPaused(task.Stage) || nodes[step.NodeID].MediaType == "text" || len(canonical) == 0) {
				step.Status = "failed"
				step.Error = "任务完成但持久结果不可用，请在任务列表检查并恢复结果"
				run.Status = "failed"
				run.Error = step.Error
			}
		}
		// Persist each admitted task before scheduling a dependent step. The stable
		// operation ID reconciles a crash between admission and this checkpoint.
		if r.save(row, run) != nil {
			return
		}
		if run.Status == "failed" {
			break
		}
	}
	all := true
	run.Outputs = []Output{}
	for _, step := range run.Steps {
		if step.Status != "succeeded" {
			all = false
		}
	}
	for _, id := range d.OutputNodeIDs {
		step := steps[id]
		if step != nil && step.Status == "succeeded" {
			if step.ResourceID == "" && len(step.Candidates) > 1 {
				for _, value := range step.Candidates {
					run.Outputs = append(run.Outputs, Output{NodeID: id, MediaType: nodes[id].MediaType, ResourceID: value.ResourceID})
				}
			} else {
				run.Outputs = append(run.Outputs, Output{NodeID: id, MediaType: nodes[id].MediaType, ResourceID: step.ResourceID, Text: step.Text})
			}
		}
	}
	if all {
		run.Status, run.Error = "succeeded", ""
	}
	if run.Status != "paused" {
		run.Reason = ""
	}
	_ = r.save(row, run)
}
