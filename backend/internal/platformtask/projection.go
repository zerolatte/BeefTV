//go:build platform

package platformtask

import (
	"encoding/json"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/task"
	"infinite-canvas/backend/internal/taskdelivery"
)

type presenter struct{ service *Service }

func (p presenter) Task(value model.Task) *model.Task {
	projected := []model.Task{value}
	p.decorate(projected)
	out := task.TaskForOutput(projected[0])
	out.FailureDiagnostics = nil
	var input map[string]any
	if json.Unmarshal([]byte(value.InputJSON), &input) == nil {
		if key, err := task.ClientOperationID(input); err == nil && key != "" {
			out.ClientOperationID = &key
		}
	}
	return out
}

func (p presenter) Summaries(values []model.Task) []task.Summary {
	outputs := p.decorate(values)
	out := task.SummariesForOutput(values)
	for i := range out {
		out[i].FailureDiagnostics = nil
		out[i].Outcome = values[i].Outcome
		out[i].ResultState = values[i].ResultState
		out[i].Outputs = outputs[values[i].ID]
	}
	return out
}

func (presenter) Logs(values []model.TaskLog) []model.TaskLog { return task.PresentTaskLogs(values) }

func (p presenter) decorate(values []model.Task) map[string][]task.CanonicalOutput {
	outputs := map[string][]task.CanonicalOutput{}
	if len(values) == 0 {
		return outputs
	}
	ids := make([]string, 0, len(values))
	for _, value := range values {
		ids = append(ids, value.ID)
	}
	intents, err := p.service.repo.PlatformTaskIntents(values[0].UserID, ids)
	results, resultErr := p.service.repo.PlatformTaskOutputResults(values[0].UserID, ids)
	stored, decodeErrors := taskdelivery.DecodeStoredOutputsByTask(results)
	byTask := map[string]model.PlatformExecutionIntent{}
	ambiguous := map[string]bool{}
	for _, intent := range intents {
		if _, exists := byTask[intent.TaskID]; exists {
			ambiguous[intent.TaskID] = true
		}
		byTask[intent.TaskID] = intent
	}
	for i := range values {
		value := &values[i]
		intent, found := byTask[value.ID]
		facts := task.OutcomeFacts{Unreadable: err != nil || resultErr != nil || decodeErrors[value.ID] != nil || ambiguous[value.ID]}
		if intent.State == platformquota.StateReleased {
			facts.RequestNotSent = true
		}
		loadErr := resultErr
		if loadErr == nil {
			loadErr = decodeErrors[value.ID]
		}
		projected := taskdelivery.ProjectWithFacts(*value, stored[value.ID], loadErr, false, facts)
		outcome := projected.Outcome
		switch {
		case facts.Unreadable:
			outcome = task.Outcome{Phase: "failed", Regenerate: "blocked", Reason: "evidence_unavailable"}
		case !found || value.Type != "canvas_text":
			outcome.Regenerate = "blocked"
			if value.Status != model.TaskStatusSucceeded {
				outcome.Phase = "failed"
				outcome.Reason = "platform_task_not_admitted"
			}
		case value.Status == model.TaskStatusFailed || value.Status == model.TaskStatusCancelled:
			switch {
			case value.ResultJSON != "":
				outcome = task.Outcome{Phase: "retrievable", Regenerate: "blocked", Reason: "delivery_pending"}
			case intent.State == platformquota.StateSettled:
				outcome = task.Outcome{Phase: "failed", Regenerate: "confirm", Reason: task.ReasonChargedResultUnrecoverable}
			case intent.State != platformquota.StateReleased && intent.State != platformquota.StateCompensated:
				outcome = task.Outcome{Phase: "failed", Regenerate: "blocked", Reason: "submission_unknown"}
			}
		}
		if p.service.testUpstream == "" {
			outcome.Regenerate = "blocked"
		}
		value.Outcome = &outcome
		value.ResultState = projected.ResultState
		value.Outputs = taskdelivery.ModelTaskOutputs(projected.Outputs)
		outputs[value.ID] = projected.Outputs
	}
	return outputs
}
