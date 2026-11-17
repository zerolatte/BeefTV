package handler

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/canvas"
	"infinite-canvas/backend/internal/kernel"
	"log"
	"net/http"
	"time"
)

func registerCanvasProjectRoutes(r *gin.RouterGroup, owners workspaceOwners, canvasData *canvas.Service, usage runtimePolicies) {
	r.GET("/canvas-projects", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		if c.Query("page") != "" {
			page, pageSize, pageErr := parsePaginationQuery(c, 40)
			if pageErr != nil {
				fail(c, http.StatusBadRequest, pageErr)
				return
			}
			result, pageErr := canvasData.UserCanvasProjectsPage(user.ID, page, pageSize, c.Query("projectId"), c.Query("q"), c.Query("sort"))
			if pageErr != nil {
				failService(c, pageErr)
				return
			}
			ok(c, result)
			return
		}
		projects, err := canvasData.UserCanvasProjectSummaries(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"projects": projects})
	})
	r.GET("/canvas-projects/:id", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		project, err := canvasData.UserCanvasProject(user.ID, c.Param("id"))
		if err != nil {
			fail(c, http.StatusNotFound, err)
			return
		}
		ok(c, gin.H{"project": project})
	})
	r.GET("/canvas-projects/:id/events", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		project, err := canvasData.UserCanvasProject(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		var initial struct {
			Revision int64 `json:"revision"`
		}
		_ = json.Unmarshal(project, &initial)
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			return
		}
		_, _ = fmt.Fprintf(c.Writer, "event: ready\ndata: {\"revision\":%d}\n\n", initial.Revision)
		flusher.Flush()
		last := initial.Revision
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-c.Request.Context().Done():
				return
			case <-ticker.C:
				latest, readErr := canvasData.UserCanvasProject(user.ID, c.Param("id"))
				if readErr != nil {
					return
				}
				var state struct {
					Revision int64 `json:"revision"`
				}
				if json.Unmarshal(latest, &state) != nil || state.Revision <= last {
					continue
				}
				last = state.Revision
				_, _ = fmt.Fprintf(c.Writer, "event: canvas.updated\ndata: {\"revision\":%d}\n\n", last)
				flusher.Flush()
			}
		}
	})
	r.GET("/canvas-projects/:id/history", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		result, err := canvasData.CanvasHistory(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.GET("/canvas-projects/:id/history/:snapshotId", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		snapshot, err := canvasData.CanvasHistorySnapshot(user.ID, c.Param("id"), c.Param("snapshotId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"snapshot": snapshot, "project": json.RawMessage(snapshot.PayloadJSON)})
	})
	r.POST("/canvas-projects/:id/history/:snapshotId/restore", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, usage)
		if !available || !enforceRateLimit(c, "canvas-write:"+user.ID, policy.Request.CanvasWritePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
		var req struct {
			Revision *int64 `json:"revision"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, kernel.BadAuthRequest("恢复请求格式错误"))
			return
		}
		project, err := canvasData.RestoreCanvasHistory(user.ID, c.Param("id"), c.Param("snapshotId"), req.Revision)
		if err != nil {
			failService(c, err)
			return
		}
		log.Printf("canvas_restore request_id=%q trace_id=%q actor=%q canvas=%q snapshot=%q base_revision=%d revision=%d", RequestID(c), TraceID(c), user.ID, c.Param("id"), c.Param("snapshotId"), *req.Revision, project.Revision)
		ok(c, gin.H{"project": project})
	})
	r.PUT("/canvas-projects/:id", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, usage)
		if !available || !enforceRateLimit(c, "canvas-write:"+user.ID, policy.Request.CanvasWritePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, canvasData.RequestMaxBodyBytes())
		var req struct {
			Project json.RawMessage `json:"project"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(req.Project, &identity) != nil || identity.ID != c.Param("id") {
			fail(c, http.StatusBadRequest, kernel.BadAuthRequest("画布 ID 与请求路径不一致"))
			return
		}
		var audit struct {
			Revision    *int64            `json:"revision"`
			Nodes       []json.RawMessage `json:"nodes"`
			Connections []json.RawMessage `json:"connections"`
		}
		_ = json.Unmarshal(req.Project, &audit)
		baseRevision := int64(-1)
		if audit.Revision != nil {
			baseRevision = *audit.Revision
		}
		project, err := canvasData.UpsertUserCanvasProject(user.ID, req.Project)
		defer func() {
			nodesBefore, nodesAfter := -1, len(audit.Nodes)
			if project.SaveAudit != nil {
				nodesBefore, nodesAfter = project.SaveAudit.NodesBefore, project.SaveAudit.NodesAfter
			}
			// Metadata only: never log prompts, media URLs, cookies, or the canvas payload.
			log.Printf("canvas_save request_id=%q trace_id=%q actor=%q canvas=%q base_revision=%d revision=%d nodes_before=%d nodes_after=%d connections=%d status=%d", RequestID(c), TraceID(c), user.ID, identity.ID, baseRevision, project.Revision, nodesBefore, nodesAfter, len(audit.Connections), c.Writer.Status())
		}()
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"project": project})
	})
	r.DELETE("/canvas-projects/:id", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		if err := canvasData.DeleteUserCanvasProject(user.ID, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"id": c.Param("id")})
	})
}
