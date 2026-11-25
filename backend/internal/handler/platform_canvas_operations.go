package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"infinite-canvas/backend/internal/canvas"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/operations"
)

// RegisterPlatformCanvasOperations 必须挂在平台会话与首次改密校验之后。
// 账号只取服务端会话，手工保存沿用操作层的事务、幂等回执和归属校验。
func RegisterPlatformCanvasOperations(api *gin.RouterGroup, binder operations.DomainBinder, store *operations.Store) {
	registry := operations.NewRegistry(binder, store)
	operations.RegisterCanvasPersistenceOps(registry)
	for _, op := range registry.List(operations.ManualCaller(false)) {
		operation := op.ID
		api.POST("/ops/"+operation, func(c *gin.Context) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max(int64(agentOpsMaxBody), canvas.EffectiveRequestMaxBodyBytes()))
			var req struct {
				OpID   string          `json:"opId"`
				Params json.RawMessage `json:"params"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					fail(c, http.StatusRequestEntityTooLarge, kernel.BadAuthRequest("请求体超过容量上限"))
				} else {
					failService(c, kernel.BadAuthRequest("请求体不是合法 JSON"))
				}
				return
			}
			result, err := registry.Execute(operations.Request{
				Context: c.Request.Context(), Op: operation, OpID: req.OpID,
				UserID: platformAccount(c).ID, Caller: operations.ManualCaller(false), Params: req.Params,
			})
			if err != nil {
				failPlatformOperation(c, err)
				return
			}
			ok(c, result)
		})
	}
	api.GET("/ops/external-writes", func(c *gin.Context) {
		canvasID := strings.TrimSpace(c.Query("canvasId"))
		if canvasID == "" {
			failService(c, kernel.BadAuthRequest("画布 ID 必填"))
			return
		}
		writes, err := store.ListExternalCanvasWrites(platformAccount(c).ID, canvasID)
		if err != nil {
			failPlatformOperation(c, err)
			return
		}
		ok(c, gin.H{"writes": writes})
	})
}

func failPlatformOperation(c *gin.Context, err error) {
	opErr := operations.AsError(err)
	status := operations.HTTPStatus(opErr.Code)
	c.JSON(status, gin.H{"code": status, "reason": opErr.Reason, "msg": opErr.Message, "details": opErr.Details})
}
