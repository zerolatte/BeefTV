//go:build platform

// Package platformworkflow 将平台身份与模型合同接到既有工作流模板和调度器。
package platformworkflow

import (
	"context"
	"errors"
	"sync"

	"gorm.io/gorm"
	cw "infinite-canvas/backend/internal/canvasworkflow"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/task"
)

type Tasks interface {
	Available() bool
	Create(context.Context, string, task.CreateRequest) (*model.Task, error)
	Retry(context.Context, string, string, string) (*model.Task, error)
	Cancel(context.Context, string, string) error
}

type Catalog interface {
	Read(context.Context, string, string) (*modelcatalog.PlatformCatalogResponse, error)
}

type Cipher struct {
	Encrypt func(string) (string, error)
	Decrypt func(string) (string, error)
}

type Service struct {
	repo      *repository.Repository
	tasks     Tasks
	catalog   Catalog
	cipher    Cipher
	templates *cw.Templates
	runs      *cw.Runs
	runLoop   func(func(context.Context)) bool
}

func New(repo *repository.Repository, tasks Tasks, catalog Catalog, cipher Cipher) (*Service, error) {
	if repo == nil || repo.DB() == nil || tasks == nil || catalog == nil || cipher.Encrypt == nil || cipher.Decrypt == nil {
		return nil, errors.New("平台工作流装配不完整")
	}
	s := &Service{repo: repo, tasks: tasks, catalog: catalog, cipher: cipher}
	mu, running := &sync.Mutex{}, false
	s.templates = cw.NewTemplates(repo, recipes{s}, mu)
	s.runs = cw.NewRuns(repo, s.templates, runHost{s}, mu, &running)
	return s, nil
}

func (s *Service) account(ctx context.Context, workspaceID string) (*platformauth.Account, error) {
	var account platformauth.Account
	err := s.repo.DB().WithContext(ctx).Model(&platformauth.Account{}).
		Select("platform_accounts.*").
		Joins("JOIN platform_account_workspaces ON platform_account_workspaces.account_id = platform_accounts.id").
		Where("platform_account_workspaces.workspace_id = ? AND platform_accounts.status = ? AND platform_accounts.must_change_password = ?", workspaceID, platformauth.StatusActive, false).
		Take(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, kernel.Unauthorized("平台账号或工作区绑定已失效")
	}
	return &account, err
}

// Workspace 为一次已认证请求建立领域作用域，管理员同样只能读取自己的内容。
func (s *Service) Workspace(ctx context.Context, p identity.Principal) (*Workspace, error) {
	if p.Mode != identity.ModePlatform || p.Validate() != nil {
		return nil, kernel.Unauthorized("需要有效的平台会话")
	}
	account, err := s.account(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if account.ID != p.AccountID {
		return nil, kernel.Unauthorized("平台账号与工作区不匹配")
	}
	return &Workspace{s: s, id: p.WorkspaceID, accountID: p.AccountID, ctx: ctx}, nil
}

// Advance 由平台组合根的工作协程调用，共享调度器拥有状态机和每一步的任务收据。
func (s *Service) Advance(ctx context.Context) {
	if s.tasks.Available() {
		s.runs.AdvanceAll(ctx)
	}
}

func (s *Service) StartScheduler(ctx context.Context, runLoop func(func(context.Context)) bool) {
	if !s.tasks.Available() || runLoop == nil {
		return
	}
	s.runLoop = runLoop
	s.runs.StartScheduler(ctx)
}

type Workspace struct {
	s         *Service
	id        string
	accountID string
	ctx       context.Context
}

func (w *Workspace) List() ([]cw.Template, error)       { return w.s.templates.List(w.id) }
func (w *Workspace) Get(id string) (cw.Template, error) { return w.s.templates.Get(w.id, id) }
func (w *Workspace) Create(req cw.CreateRequest) (cw.Template, error) {
	req.Definition.PresentationVersion = 1
	return w.s.templates.Create(w.id, req)
}
func (w *Workspace) Update(id string, req cw.UpdateRequest) (cw.Template, error) {
	req.Definition.PresentationVersion = 1
	return w.s.templates.Update(w.id, id, req)
}
func (w *Workspace) Delete(id string) error { return w.s.templates.Delete(w.id, id) }
func (w *Workspace) Prepare(d cw.Definition, id string, revision int) (cw.Definition, error) {
	return w.s.templates.Prepare(w.id, d, id, revision)
}
func (w *Workspace) Editor(id string, revision int) (cw.Editor, error) {
	value, err := w.s.templates.Editor(w.id, id, revision)
	if err == nil {
		value.Operations, err = w.Operations()
	}
	return value, err
}
func (w *Workspace) Operations() ([]cw.Operation, error) {
	catalog, err := w.s.catalog.Read(w.ctx, w.accountID, w.id)
	if err != nil {
		return nil, err
	}
	operations := textOperations()
	if w.s.tasks.Available() {
		operations[0].UnavailableReason = ""
	}
	for _, item := range catalog.Models {
		if item.Available && item.PointPrices["text_generation"] > 0 {
			operations[0].Fields[0].Options = append(operations[0].Fields[0].Options, cw.FieldOption{Label: item.Name, Value: item.ID})
		}
	}
	return operations, nil
}
func (w *Workspace) Runs(id string) ([]cw.Run, error) { return w.s.runs.List(w.id, id) }
func (w *Workspace) Run(id string) (cw.Run, error)    { return w.s.runs.Get(w.id, id) }
func (w *Workspace) Start(id string, req cw.RunRequest) (cw.Run, error) {
	if err := operationID(req.ClientOperationID); err != nil {
		return cw.Run{}, err
	}
	if !w.s.tasks.Available() {
		if err := w.checkDefinition(id, req.TemplateRevision); err != nil {
			return cw.Run{}, err
		}
		return cw.Run{}, unavailable()
	}
	return w.s.runs.Start(w.id, id, req)
}
func (w *Workspace) Retry(id, key string) (cw.Run, error) {
	if err := operationID(key); err != nil {
		return cw.Run{}, err
	}
	run, err := w.s.runs.Get(w.id, id)
	if err != nil {
		return cw.Run{}, err
	}
	if run.Reason == string(kernel.ReasonBackupRecoveryRequired) || run.Reason == "import_recovery_required" {
		return cw.Run{}, &kernel.AppError{Status: 409, Code: 409, Reason: kernel.ErrorReason(run.Reason), Message: "恢复或导入的工作流禁止重投，请核对原运行并新建执行"}
	}
	if !w.s.tasks.Available() {
		return cw.Run{}, unavailable()
	}
	if err := w.reserveRetry(id, key); err != nil {
		return cw.Run{}, err
	}
	return w.s.runs.RetryWithOperation(w.id, id, key)
}
func (w *Workspace) Cancel(id string) (cw.Run, error) { return w.s.runs.Cancel(w.ctx, w.id, id) }
func (w *Workspace) Select(id, node, resource string) (cw.Run, error) {
	return w.s.runs.Select(w.id, id, node, resource)
}

func (w *Workspace) checkDefinition(id string, revision int) error {
	row, err := w.s.templates.RevisionRow(w.id, id, revision)
	if err != nil {
		return err
	}
	value, err := cw.TemplateFromRow(row)
	if err != nil {
		return kernel.BadAuthRequest("工作流定义无法读取")
	}
	return validateDefinition(value.Definition)
}

func operationID(key string) error {
	value, err := task.ClientOperationID(map[string]any{"metadata": map[string]any{"clientOperationId": key}})
	if err != nil {
		return err
	}
	if value == "" {
		return &kernel.AppError{Status: 400, Code: 400, Reason: "idempotency_required", Message: "必须提供 clientOperationId"}
	}
	if value != key {
		return kernel.BadAuthRequest("clientOperationId 不能包含首尾空白")
	}
	return nil
}
