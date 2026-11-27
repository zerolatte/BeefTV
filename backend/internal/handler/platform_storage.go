package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/storage"
)

// 继承平台会话、来源与 CSRF 中间件；权限由存储服务再次校验。
func RegisterPlatformStorage(api *gin.RouterGroup, svc *storage.Service) {
	r := api.Group("/admin/storage")
	r.GET("", func(c *gin.Context) { result, err := svc.Get(platformAccount(c).ID); platformResult(c, result, err) })
	r.POST("/probe", func(c *gin.Context) {
		if !enforceRateLimit(c, "storage-probe:"+platformAccount(c).ID, 10, time.Minute) {
			return
		}
		var req storage.ProbeRequest
		if !storageBind(c, &req) {
			return
		}
		result, err := svc.Probe(c.Request.Context(), platformAccount(c).ID, req)
		storageResult(c, result, err)
	})
	r.PUT("", func(c *gin.Context) {
		if !enforceRateLimit(c, "storage-save:"+platformAccount(c).ID, 10, time.Minute) {
			return
		}
		var req storage.SaveRequest
		if !storageBind(c, &req) {
			return
		}
		result, err := svc.Save(c.Request.Context(), platformAccount(c).ID, req)
		storageResult(c, result, err)
	})
}
func storageBind(c *gin.Context, value any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if c.ShouldBindJSON(value) != nil {
		failService(c, kernel.BadAuthRequest("存储配置格式无效"))
		return false
	}
	return true
}
func storageResult(c *gin.Context, result any, err error) {
	if errors.Is(err, context.Canceled) {
		writeFailure(c, 499, 499, "request_cancelled", "存储操作已取消")
		return
	}
	platformResult(c, result, err)
}
