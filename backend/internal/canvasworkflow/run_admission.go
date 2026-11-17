package canvasworkflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"strings"
)

func (r *Runs) Get(user, id string) (Run, error) {
	var row model.CanvasWorkflowRun
	if e := r.repo.DB().Where("id = ? AND user_id = ?", id, user).First(&row).Error; e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return Run{}, NotFound()
		}
		return Run{}, e
	}
	return RunFromRow(row)
}

func (r *Runs) List(user, template string) ([]Run, error) {
	if _, e := r.templates.Get(user, template); e != nil {
		return nil, e
	}
	var rows []model.CanvasWorkflowRun
	if e := r.repo.DB().Where("user_id = ? AND template_id = ?", user, template).Order("created_at DESC").Limit(100).Find(&rows).Error; e != nil {
		return nil, e
	}
	out := []Run{}
	for _, row := range rows {
		v, e := RunFromRow(row)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

func (r *Runs) Start(user, id string, req RunRequest) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.host.Draining() {
		return Run{}, BadRequest("应用正在退出，请稍后重试")
	}
	if strings.TrimSpace(req.ClientOperationID) == "" || len(req.ClientOperationID) > 128 {
		return Run{}, BadRequest("缺少有效的本次运行标识")
	}
	req.TemplateRevision = NormalizeRevision(req.TemplateRevision)
	raw, _ := json.Marshal(struct {
		Template string
		Inputs   map[string]Value
		Revision int
	}{id, req.Inputs, req.TemplateRevision})
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	var old model.CanvasWorkflowRun
	e := r.repo.DB().Where("user_id = ? AND client_operation_id = ?", user, req.ClientOperationID).First(&old).Error
	if e == nil {
		if old.RequestHash != hash {
			// Before revision pinning, revision 1 requests hashed only template and inputs.
			legacyRaw, _ := json.Marshal(struct {
				Template string
				Inputs   map[string]Value
			}{id, req.Inputs})
			legacyDigest := sha256.Sum256(legacyRaw)
			if req.TemplateRevision != 1 || NormalizeRevision(old.TemplateRevision) != 1 || old.RequestHash != hex.EncodeToString(legacyDigest[:]) {
				return Run{}, &kernel.AppError{Status: 409, Code: 409, Reason: "idempotency_conflict", Message: "本次运行标识已用于不同输入，请重新确认运行"}
			}
		}
		return RunFromRow(old)
	}
	if !errors.Is(e, gorm.ErrRecordNotFound) {
		return Run{}, e
	}
	template, e := r.templates.RevisionRow(user, id, req.TemplateRevision)
	if e != nil {
		return Run{}, e
	}
	public, e := TemplateFromRow(template)
	if e != nil {
		return Run{}, e
	}
	if policy, ok := r.host.(interface {
		ValidateDefinition(string, Definition) error
	}); ok {
		if e := policy.ValidateDefinition(user, public.Definition); e != nil {
			return Run{}, e
		}
	}
	order, e := Validate(public.Definition)
	if e != nil {
		return Run{}, e
	}
	knownInputs := map[string]bool{}
	run := Run{Status: "queued", Steps: []Step{}, Outputs: []Output{}}
	refs := map[string]bool{}
	for _, n := range order {
		step := Step{NodeID: n.ID, Title: n.Title, Status: "pending"}
		if n.Role == "input" || n.Role == "constant" {
			v := Value{ResourceID: n.ResourceID, Text: n.Text}
			if n.Role == "input" {
				knownInputs[n.ID] = true
				if override, ok := req.Inputs[n.ID]; ok {
					v = override
				}
			}
			if n.MediaType == "text" {
				if strings.TrimSpace(v.Text) == "" || len(v.Text) > 100000 {
					return Run{}, BadRequest("请输入有效文本：" + n.Title)
				}
				v.ResourceID = ""
			} else {
				if _, e = r.resource(user, v.ResourceID, n.MediaType); e != nil {
					return Run{}, e
				}
				refs[v.ResourceID] = true
				v.Text = ""
			}
			step.Status = "succeeded"
			step.ResourceID = v.ResourceID
			step.Text = v.Text
		}
		run.Steps = append(run.Steps, step)
	}
	for key := range req.Inputs {
		if !knownInputs[key] {
			return Run{}, BadRequest("输入不属于此工作流或不允许替换：" + key)
		}
	}
	// Template references protect every revision, but admission must only depend
	// on this revision's immutable recipe and definition.
	frozenIDs, e := r.host.ReferenceIDs(template.RecipeCipher)
	if e != nil {
		var recipeErr *RecipeError
		if errors.As(e, &recipeErr) {
			return Run{}, recipeErr.Err
		}
		return Run{}, e
	}
	for _, n := range public.Definition.Nodes {
		if n.ResourceID != "" {
			frozenIDs[n.ResourceID] = true
		}
	}
	for resource := range frozenIDs {
		if _, e = r.resource(user, resource, ""); e != nil {
			return Run{}, e
		}
		refs[resource] = true
	}
	state, _ := json.Marshal(run)
	row := model.CanvasWorkflowRun{ID: kernel.NewID(), TemplateRevision: req.TemplateRevision, UserID: user, ClientOperationID: req.ClientOperationID, RequestHash: hash, TemplateID: id, TemplateName: template.Name, DefinitionJSON: template.DefinitionJSON, RecipeCipher: template.RecipeCipher, StateJSON: string(state), Status: run.Status}
	e = r.repo.DB().Transaction(func(tx *gorm.DB) error {
		if e := tx.Create(&row).Error; e != nil {
			return e
		}
		return PersistReferences(tx, row.ID, user, refs)
	})
	if e != nil {
		return Run{}, e
	}
	return RunFromRow(row)
}
