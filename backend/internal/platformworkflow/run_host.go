//go:build platform

package platformworkflow

import (
	"context"

	cw "infinite-canvas/backend/internal/canvasworkflow"
	"infinite-canvas/backend/internal/editing"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/task"
)

type runHost struct{ s *Service }

func (h runHost) ValidateDefinition(_ string, d cw.Definition) error { return validateDefinition(d) }

func unavailable() error {
	return &kernel.AppError{Status: 403, Code: 403, Reason: "generation_unavailable", Message: "平台尚未开放该工作流操作"}
}
func (h runHost) Generate(user, operation, cipher string, n cw.Node, _ map[string]cw.Node, steps map[string]*cw.Step) (*model.Task, error) {
	values, err := cw.DecodeRecipes(h.s.cipher.Decrypt, cipher)
	if err != nil {
		return nil, kernel.BadAuthRequest("工作流配方无法读取")
	}
	recipe, exists := values[n.ID]
	if !exists || n.OperationID != "generate.text" || recipe.Request.Type != "canvas_text" || recipe.Request.Operation != "text_generation" || recipe.Request.Provider != "" || recipe.Request.Model != "" || recipe.Request.LogicalModelID == "" {
		return nil, unavailable()
	}
	prompt, err := cw.RenderPromptBindings(recipe.Request.Prompt, recipe.Bindings, steps)
	if err != nil {
		return nil, err
	}
	return h.s.tasks.Create(context.Background(), user, task.CreateRequest{Type: "canvas_text", Operation: "text_generation", LogicalModelID: recipe.Request.LogicalModelID, Prompt: prompt,
		Input: map[string]any{"mode": "text", "metadata": map[string]any{"clientOperationId": operation}}})
}
func (h runHost) ReferenceIDs(cipher string) (map[string]bool, error) {
	return cw.RecipeReferenceIDs(h.s.cipher.Decrypt, cipher)
}
func (h runHost) RetryStep(model.CanvasWorkflowRun, string, *model.Task) (*model.Task, error) {
	return nil, kernel.BadAuthRequest("工作流重试必须携带操作标识")
}
func (h runHost) RetryStepWithOperation(row model.CanvasWorkflowRun, _ string, original *model.Task, key string) (*model.Task, error) {
	return h.s.tasks.Retry(context.Background(), row.UserID, original.ID, key)
}
func (h runHost) CancelTask(ctx context.Context, user, id string) error {
	return h.s.tasks.Cancel(ctx, user, id)
}
func (h runHost) Outputs(*model.Task) ([]cw.TaskOutput, []cw.TaskOutput) { return nil, nil }
func (h runHost) FailureMessage(message, stage, _ string) string {
	return task.SafePersistedFailureMessage(message, stage)
}
func (h runHost) Draining() bool { return false }
func (h runHost) RunLoop(fn func(context.Context)) bool {
	return h.s.runLoop != nil && h.s.runLoop(fn)
}
func (h runHost) Describe(string, string, cw.Node, string) (*model.Task, error) {
	return nil, unavailable()
}
func (h runHost) Upscale(string, string, cw.Node, string) (*model.Task, error) {
	return nil, unavailable()
}
func (h runHost) CanvasMedia(string, string, cw.Node, string) (*model.Task, error) {
	return nil, unavailable()
}
func (h runHost) DepthCapture(string, string, string) (*model.Task, error) { return nil, unavailable() }
func (h runHost) TimelineRender(string, string, editing.Project, editing.Options) (*model.Task, error) {
	return nil, unavailable()
}

var _ cw.RunHost = runHost{}
