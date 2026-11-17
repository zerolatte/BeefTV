package canvasworkflow

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/editing"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"sync"
)

// TaskOutput is one stored output of a finished task.
type TaskOutput struct {
	OutputIndex int
	MediaType   string
	ResourceID  string
}

// RunHost submits and controls the tasks a run's steps depend on. Each method
// uses operation as the task's client operation ID so admission is idempotent.
type RunHost interface {
	// Generate submits a generation step from the template's private recipe.
	Generate(user, operation, recipeCipher string, n Node, nodes map[string]Node, steps map[string]*Step) (*model.Task, error)
	Describe(user, operation string, n Node, resource string) (*model.Task, error)
	Upscale(user, operation string, n Node, resource string) (*model.Task, error)
	CanvasMedia(user, operation string, n Node, resource string) (*model.Task, error)
	DepthCapture(user, operation, resource string) (*model.Task, error)
	TimelineRender(user, operation string, project editing.Project, options editing.Options) (*model.Task, error)
	// RetryStep retries a failed step task, refreshing credentials when needed.
	RetryStep(row model.CanvasWorkflowRun, nodeID string, task *model.Task) (*model.Task, error)
	CancelTask(ctx context.Context, user, id string) error
	// Outputs lists the task's stored outputs, canonical result first.
	Outputs(task *model.Task) (canonical []TaskOutput, stored []TaskOutput)
	// FailureMessage is the user-safe text for a persisted task failure.
	FailureMessage(message, stage, errorCode string) string
	// ReferenceIDs lists every resource a recipe cipher references. A recipe
	// that cannot be read returns a *RecipeError.
	ReferenceIDs(recipeCipher string) (map[string]bool, error)
	Draining() bool
	RunLoop(fn func(context.Context)) bool
}

// RecipeError reports a private recipe that cannot be decrypted or parsed.
type RecipeError struct {
	Decrypt bool
	Err     error
}

func (e *RecipeError) Error() string { return e.Err.Error() }
func (e *RecipeError) Unwrap() error { return e.Err }

// Runs owns workflow run admission, scheduling, selection, retry and cancel.
type Runs struct {
	repo      *repository.Repository
	templates *Templates
	host      RunHost
	mu        *sync.Mutex
	loop      *bool
}

// NewRuns shares mu with Templates; loop records whether the scheduler runs.
func NewRuns(repo *repository.Repository, templates *Templates, host RunHost, mu *sync.Mutex, loop *bool) *Runs {
	return &Runs{repo: repo, templates: templates, host: host, mu: mu, loop: loop}
}

func RunFromRow(row model.CanvasWorkflowRun) (Run, error) {
	var state runState
	err := json.Unmarshal([]byte(row.StateJSON), &state)
	v := state.Run
	v.retryRequests = state.RetryRequests
	if v.Steps == nil {
		v.Steps = []Step{}
	}
	if v.Outputs == nil {
		v.Outputs = []Output{}
	}
	v.ID = row.ID
	v.TemplateID = row.TemplateID
	v.TemplateRevision = NormalizeRevision(row.TemplateRevision)
	v.TemplateName = row.TemplateName
	v.Status = row.Status
	v.CreatedAt = row.CreatedAt
	v.UpdatedAt = row.UpdatedAt
	return v, err
}
