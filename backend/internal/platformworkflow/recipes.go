//go:build platform

package platformworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"
	cw "infinite-canvas/backend/internal/canvasworkflow"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

type recipes struct{ s *Service }

func textOperations() []cw.Operation {
	return []cw.Operation{{ID: "generate.text", Version: 1, Name: "生成文本", OutputType: "text",
		Inputs:            []cw.InputPort{{ID: "prompt", Label: "提示词", MediaType: "text", Multiple: true}},
		Fields:            []cw.Field{{Key: "modelOption", Label: "模型", Type: "select"}, {Key: "prompt", Label: "提示词", Type: "textarea"}},
		UnavailableReason: "平台生成尚未开放"}}
}

func (h recipes) Operations() []cw.Operation { return textOperations() }

func validateDefinition(d cw.Definition) error {
	if _, err := cw.Validate(d); err != nil {
		return kernel.BadAuthRequest(err.Error())
	}
	for _, n := range d.Nodes {
		if n.MediaType != "text" || n.ResourceID != "" || n.VideoDurationSourceNodeID != "" || (n.Role != "input" && n.Role != "constant" && !(n.Role == "operation" && n.OperationID == "generate.text")) {
			return kernel.BadAuthRequest("当前平台工作流仅支持文本输入和文本生成")
		}
		if n.Role != "operation" && (n.SourceTaskID != "" || n.OperationID != "" || n.OperationVersion != 0) {
			return kernel.BadAuthRequest("文本输入不能携带来源任务或执行配置")
		}
		for key := range n.Config {
			if key != "modelOption" && key != "prompt" {
				return kernel.BadAuthRequest("平台工作流不允许覆盖上游配置")
			}
		}
	}
	return nil
}

func (h recipes) Compile(user string, d cw.Definition, existing *model.CanvasWorkflow, seal bool) (cw.Compiled, error) {
	d.Nodes = append([]cw.Node(nil), d.Nodes...)
	d.Edges = append([]cw.Edge(nil), d.Edges...)
	d = cw.Normalize(d)
	if err := validateDefinition(d); err != nil {
		return cw.Compiled{}, err
	}
	account, err := h.s.account(context.Background(), user)
	if err != nil {
		return cw.Compiled{}, err
	}
	catalog, err := h.s.catalog.Read(context.Background(), account.ID, user)
	if err != nil {
		return cw.Compiled{}, err
	}
	allowed := map[string]bool{}
	for _, item := range catalog.Models {
		allowed[item.ID] = item.Available && item.PointPrices["text_generation"] > 0
	}
	nodes := map[string]cw.Node{}
	for _, n := range d.Nodes {
		nodes[n.ID] = n
	}
	previous := cw.RecipesByNode{}
	previousSources := map[string]string{}
	if existing != nil {
		previous, err = cw.DecodeRecipes(h.s.cipher.Decrypt, existing.RecipeCipher)
		if err != nil {
			return cw.Compiled{}, kernel.BadAuthRequest("工作流配方无法读取")
		}
		old, err := cw.TemplateFromRow(*existing)
		if err != nil {
			return cw.Compiled{}, kernel.BadAuthRequest("工作流定义无法读取")
		}
		for _, n := range old.Definition.Nodes {
			previousSources[n.ID] = n.SourceTaskID
		}
	}
	result := cw.RecipesByNode{}
	for _, n := range d.Nodes {
		if n.Role != "operation" {
			continue
		}
		modelID, _ := n.Config["modelOption"].(string)
		prompt, _ := n.Config["prompt"].(string)
		if n.SourceTaskID != "" {
			var source *model.Task
			if saved, exists := previous[n.ID]; exists && previousSources[n.ID] == n.SourceTaskID {
				source = &model.Task{LogicalModelID: saved.Request.LogicalModelID, Prompt: saved.Request.Prompt}
			} else {
				source, err = h.s.repo.TaskForUser(user, n.SourceTaskID)
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return cw.Compiled{}, kernel.NotFound("来源任务不存在")
				}
				if err != nil {
					return cw.Compiled{}, err
				}
				if source.Type != "canvas_text" || source.Status != model.TaskStatusSucceeded {
					return cw.Compiled{}, kernel.BadAuthRequest("原任务不是成功的文本任务")
				}
			}
			if modelID == "" {
				modelID = source.LogicalModelID
			}
			if _, exists := n.Config["prompt"]; !exists {
				prompt = source.Prompt
			}
		}
		if !allowed[modelID] {
			return cw.Compiled{}, &kernel.AppError{Status: 403, Code: 403, Reason: "model_not_authorized", Message: "模型未授权、不可用或没有有效文本价格"}
		}
		bindings, err := cw.NodePromptBindings(prompt, nil, nodes, d.Edges, n.ID)
		if err != nil {
			return cw.Compiled{}, err
		}
		incoming := []cw.Edge{}
		for _, edge := range d.Edges {
			if edge.To == n.ID {
				incoming = append(incoming, edge)
			}
		}
		for _, edge := range incoming {
			found := false
			for _, b := range bindings {
				found = found || b.NodeID == edge.From
			}
			if !found {
				anchor := nodes[edge.From].Text
				if anchor != "" && strings.Count(prompt, anchor) == 1 {
					bindings = append(bindings, cw.Binding{NodeID: edge.From, Field: "prompt", Original: anchor, Index: strings.Index(prompt, anchor)})
				} else if anchor == "" && len(incoming) == 1 && len(bindings) == 0 {
					bindings = append(bindings, cw.Binding{NodeID: edge.From, Field: "prompt", Original: prompt})
				} else {
					return cw.Compiled{}, kernel.BadAuthRequest("请在提示词中唯一引用每个连接的文本节点")
				}
			}
		}
		if strings.TrimSpace(prompt) == "" && len(bindings) == 0 {
			return cw.Compiled{}, kernel.BadAuthRequest("请输入提示词或连接文本输入")
		}
		if len(prompt) > 100000 {
			return cw.Compiled{}, kernel.BadAuthRequest("提示词过长")
		}
		result[n.ID] = cw.Recipe{Request: cw.RecipeRequest{Type: "canvas_text", Operation: "text_generation", LogicalModelID: modelID, Prompt: prompt, Input: map[string]any{"mode": "text", "prompt": prompt}}, Bindings: bindings}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return cw.Compiled{}, err
	}
	out := cw.Compiled{Presented: present(d, result), References: map[string]bool{}}
	if seal {
		out.Cipher, err = h.s.cipher.Encrypt(string(encoded))
	}
	return out, err
}

func present(d cw.Definition, values cw.RecipesByNode) cw.Definition {
	d.Nodes = append([]cw.Node(nil), d.Nodes...)
	d.PresentationVersion = 1
	for i := range d.Nodes {
		if recipe, exists := values[d.Nodes[i].ID]; exists {
			d.Nodes[i].Config = map[string]any{"modelOption": recipe.Request.LogicalModelID, "prompt": recipe.Request.Prompt}
		}
	}
	return d
}

func (h recipes) Present(d cw.Definition, cipher string) (cw.Definition, error) {
	values, err := cw.DecodeRecipes(h.s.cipher.Decrypt, cipher)
	if err != nil {
		return d, kernel.BadAuthRequest("工作流配方无法读取")
	}
	return present(d, values), nil
}
