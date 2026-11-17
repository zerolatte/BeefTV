//go:build platform

package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	cw "infinite-canvas/backend/internal/canvasworkflow"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/platformworkflow"
)

// RegisterPlatformWorkflowRoutes 必须装配到启用平台会话、Origin 与 CSRF 校验的路由组。
func RegisterPlatformWorkflowRoutes(r *gin.RouterGroup, service *platformworkflow.Service) {
	handle := func(method, path string, action func(*gin.Context, *platformworkflow.Workspace) (any, error)) {
		r.Handle(method, path, func(c *gin.Context) {
			principal, err := CurrentPrincipal(c)
			if err != nil {
				failService(c, err)
				return
			}
			workspace, err := service.Workspace(c.Request.Context(), principal)
			if err != nil {
				failService(c, err)
				return
			}
			result, err := action(c, workspace)
			if err != nil {
				failService(c, err)
				return
			}
			ok(c, result)
		})
	}
	handle("GET", "/canvas-workflow-operations", func(_ *gin.Context, w *platformworkflow.Workspace) (any, error) {
		values, err := w.Operations()
		return gin.H{"operations": values}, err
	})
	handle("GET", "/canvas-workflows", func(_ *gin.Context, w *platformworkflow.Workspace) (any, error) {
		values, err := w.List()
		return gin.H{"workflows": values}, err
	})
	handle("GET", "/canvas-workflows/:id", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) { return w.Get(c.Param("id")) })
	handle("GET", "/canvas-workflows/:id/editor", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) {
		revision := 0
		if raw := c.Query("revision"); raw != "" {
			var err error
			revision, err = strconv.Atoi(raw)
			if err != nil || revision < 1 {
				return nil, kernel.BadAuthRequest("工作流版本必须为正整数")
			}
		}
		return w.Editor(c.Param("id"), revision)
	})
	handle("POST", "/canvas-workflows/validate", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) {
		var req struct {
			Definition       cw.Definition `json:"definition"`
			TemplateID       string        `json:"templateId"`
			TemplateRevision int           `json:"templateRevision"`
		}
		if err := decodePlatformWorkflowRequest(c, &req); err != nil {
			return nil, err
		}
		value, err := w.Prepare(req.Definition, req.TemplateID, req.TemplateRevision)
		return gin.H{"valid": true, "definition": value}, err
	})
	handle("POST", "/canvas-workflows", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) {
		var req cw.CreateRequest
		if err := decodePlatformWorkflowRequest(c, &req); err != nil {
			return nil, err
		}
		return w.Create(req)
	})
	handle("PUT", "/canvas-workflows/:id", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) {
		var req cw.UpdateRequest
		if err := decodePlatformWorkflowRequest(c, &req); err != nil {
			return nil, err
		}
		return w.Update(c.Param("id"), req)
	})
	handle("DELETE", "/canvas-workflows/:id", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) {
		return gin.H{"id": c.Param("id")}, w.Delete(c.Param("id"))
	})
	handle("GET", "/canvas-workflows/:id/runs", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) {
		values, err := w.Runs(c.Param("id"))
		return gin.H{"runs": values}, err
	})
	handle("POST", "/canvas-workflows/:id/runs", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) {
		var req cw.RunRequest
		if err := decodePlatformWorkflowRequest(c, &req); err != nil {
			return nil, err
		}
		return w.Start(c.Param("id"), req)
	})
	handle("GET", "/canvas-workflow-runs/:id", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) { return w.Run(c.Param("id")) })
	handle("POST", "/canvas-workflow-runs/:id/retry", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) {
		var req struct {
			ClientOperationID string `json:"clientOperationId"`
		}
		if err := decodePlatformWorkflowRequest(c, &req); err != nil {
			return nil, err
		}
		return w.Retry(c.Param("id"), req.ClientOperationID)
	})
	handle("POST", "/canvas-workflow-runs/:id/select-image", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) {
		var req struct {
			NodeID     string `json:"nodeId"`
			ResourceID string `json:"resourceId"`
		}
		if err := decodePlatformWorkflowRequest(c, &req); err != nil {
			return nil, err
		}
		return w.Select(c.Param("id"), req.NodeID, req.ResourceID)
	})
	handle("POST", "/canvas-workflow-runs/:id/cancel", func(c *gin.Context, w *platformworkflow.Workspace) (any, error) { return w.Cancel(c.Param("id")) })
}

func decodePlatformWorkflowRequest(c *gin.Context, output any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return kernel.BadAuthRequest("工作流请求格式无效")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return kernel.BadAuthRequest("工作流请求只能包含一个对象")
	}
	return nil
}
