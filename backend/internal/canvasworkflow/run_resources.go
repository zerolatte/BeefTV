package canvasworkflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	localasset "infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"strings"
)

// StepOperation is the stable client operation ID of one run step.
func StepOperation(run, node string) string {
	sum := sha256.Sum256([]byte(run + ":" + node))
	return "canvas-workflow:" + hex.EncodeToString(sum[:])
}

// ReadyResource returns the user's saved resource, optionally of one kind.
func ReadyResource(repo *repository.Repository, user, id, kind string) (*model.Resource, error) {
	r, e := repo.ResourceForUser(user, id)
	if e != nil || r == nil || r.Status != model.ResourceStatusReady {
		return nil, BadRequest("工作流素材不存在或尚未保存，请先保存素材")
	}
	if kind != "" && localasset.NormalizeKind(r.Kind, r.MimeType) != kind {
		return nil, BadRequest("工作流素材类型与节点不一致")
	}
	return r, nil
}

// ReferenceID reads the resource ID out of one stored media reference.
func ReferenceID(v any) string {
	m, _ := v.(map[string]any)
	for _, key := range []string{"resourceId", "storageKey", "url", "dataUrl", "id"} {
		raw, _ := m[key].(string)
		if strings.HasPrefix(raw, "resource:") {
			return strings.TrimPrefix(raw, "resource:")
		}
		if id := assets.IDFromFileURL(raw); id != "" {
			return id
		}
		if key == "resourceId" && raw != "" {
			return raw
		}
	}
	return ""
}

// ReferenceIDs collects every resource referenced anywhere in value.
func ReferenceIDs(value any) map[string]bool {
	ids := map[string]bool{}
	var visit func(any)
	visit = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			if id := ReferenceID(item); id != "" {
				ids[id] = true
			}
			for _, child := range item {
				visit(child)
			}
		case []any:
			for _, child := range item {
				visit(child)
			}
		}
	}
	visit(value)
	return ids
}

// OutputResource finds the first resource in a task result.
func OutputResource(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return ""
	}
	var walk func(any) string
	walk = func(v any) string {
		switch x := v.(type) {
		case map[string]any:
			if id := ReferenceID(x); id != "" {
				return id
			}
			for _, key := range []string{"images", "videos", "audios", "outputs", "result", "data", "video", "audio", "image"} {
				if id := walk(x[key]); id != "" {
					return id
				}
			}
		case []any:
			for _, item := range x {
				if id := walk(item); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return walk(v)
}

func textOutput(task *model.Task) string {
	var v map[string]any
	_ = json.Unmarshal([]byte(task.ResultJSON), &v)
	for _, key := range []string{"text", "content", "output"} {
		if text, ok := v[key].(string); ok && text != "" {
			return text
		}
	}
	return task.TextDraft
}

func deliveryPaused(stage string) bool {
	return stage == "delivery_paused" || stage == "delivery_timed_out"
}

func (r *Runs) resource(user, id, kind string) (*model.Resource, error) {
	return ReadyResource(r.repo, user, id, kind)
}
