//go:build platform

package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/platformquota"
)

func registerPlatformQuotaRoutes(protected *gin.RouterGroup, auth *platformauth.Service, quota *platformquota.Service) {
	admin := protected.Group("/admin")
	admin.Use(platformAdminMiddleware(auth))
	admin.GET("/users/:id/quota-reconciliation", func(c *gin.Context) {
		result, err := quota.Reconcile(c.Request.Context(), platformAdminActor(c), c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"reconciliation": result})
	})
	admin.POST("/usage/:id/review", func(c *gin.Context) {
		var input platformquota.ReviewInput
		if !bindPlatformAdminJSON(c, &input) {
			return
		}
		result, err := quota.Review(c.Request.Context(), platformAdminActor(c), c.Param("id"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"review": result})
	})
	admin.POST("/users/:id/point-adjustments", func(c *gin.Context) {
		var input platformquota.AdjustmentInput
		if !bindPlatformAdminJSON(c, &input) {
			return
		}
		adjustment, err := quota.Adjust(c.Request.Context(), platformAdminActor(c), c.Param("id"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"adjustment": adjustment})
	})
	admin.GET("/usage", func(c *gin.Context) {
		page, size, valid := platformAdminPage(c)
		if !valid {
			return
		}
		items, total, err := quota.Usage(c.Request.Context(), platformAdminActor(c), c.Query("accountId"), (page-1)*size, size)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"items": items, "total": total, "page": page, "pageSize": size})
	})
}
