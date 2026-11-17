package canvasworkflow

import (
	"infinite-canvas/backend/internal/model"
)

// collectOutputs records a finished media task's saved outputs on its step.
func (r *Runs) collectOutputs(row *model.CanvasWorkflowRun, step *Step, n Node, d Definition, nodes map[string]Node, task *model.Task, canonical, stored []TaskOutput, run *Run) {
	step.Candidates = nil
	values := append(append([]TaskOutput{}, canonical...), stored...)
	seen := map[string]bool{}
	readyOutputs := map[int]bool{}
	for _, value := range values {
		if value.MediaType == n.MediaType && value.ResourceID != "" {
			if _, err := r.resource(row.UserID, value.ResourceID, n.MediaType); err != nil {
				continue
			}
			readyOutputs[value.OutputIndex] = true
			if seen[value.ResourceID] {
				continue
			}
			ref := model.CanvasWorkflowResource{OwnerID: row.ID, UserID: row.UserID, ResourceID: value.ResourceID}
			if r.repo.DB().Where("owner_id = ? AND resource_id = ?", row.ID, value.ResourceID).FirstOrCreate(&ref).Error != nil {
				continue
			}
			step.Candidates = append(step.Candidates, Value{ResourceID: value.ResourceID})
			seen[value.ResourceID] = true
		}
	}
	if n.MediaType == "image" && len(canonical) > len(readyOutputs) && len(canonical) > 1 {
		step.Status = "running"
		return
	}
	if n.MediaType == "image" && len(step.Candidates) > 1 {
		step.Status, step.ResourceID = "succeeded", ""
		for _, edge := range d.Edges {
			if edge.From == n.ID && nodes[edge.To].OperationID != "image.select" {
				step.Status, step.Error, run.Status, run.Error = "failed", "多张图片需先经过选择图片结果", "failed", "多张图片需先经过选择图片结果"
			}
		}
		return
	}
	id := OutputResource(task.ResultJSON)
	if id == "" {
		for _, output := range stored {
			if output.MediaType == n.MediaType && output.ResourceID != "" {
				id = output.ResourceID
				break
			}
		}
	}
	if id == "" {
		return
	}
	if _, e := r.resource(row.UserID, id, n.MediaType); e == nil {
		step.ResourceID = id
		step.Status = "succeeded"
		ref := model.CanvasWorkflowResource{OwnerID: row.ID, UserID: row.UserID, ResourceID: id}
		if e = r.repo.DB().Where("owner_id = ? AND resource_id = ?", row.ID, id).FirstOrCreate(&ref).Error; e != nil {
			step.Status = "running"
		}
	}
}

func imageCandidates(n Node, d Definition, steps map[string]*Step) []Value {
	values := []Value{}
	seen := map[string]bool{}
	for _, edge := range d.Edges {
		if edge.To != n.ID {
			continue
		}
		source := steps[edge.From]
		if source == nil {
			continue
		}
		candidates := source.Candidates
		if source.ResourceID != "" {
			candidates = []Value{{ResourceID: source.ResourceID}}
		}
		for _, value := range candidates {
			if value.ResourceID != "" && !seen[value.ResourceID] {
				values = append(values, value)
				seen[value.ResourceID] = true
			}
		}
	}
	return values
}
