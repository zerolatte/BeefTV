//go:build platform

package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformauth"
)

// 模型管理仅注册精确路由，普通用户目录由独立的授权读取端口提供。
func registerPlatformCatalogAdminRoutes(protected *gin.RouterGroup, auth *platformauth.Service, catalog *admin.PlatformCatalog) {
	group := protected.Group("/admin")
	group.Use(platformAdminMiddleware(auth))
	group.GET("/model-protocols", func(c *gin.Context) {
		protocols, err := catalog.Protocols(c.Request.Context(), platformAdminActor(c))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"protocols": protocols})
	})
	group.GET("/channels", func(c *gin.Context) {
		page, size, valid := platformAdminPage(c)
		if !valid {
			return
		}
		channels, total, err := catalog.Channels(c.Request.Context(), platformAdminActor(c), page, size)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"channels": channels, "total": total, "page": page, "pageSize": size})
	})
	group.POST("/channels", func(c *gin.Context) {
		var input admin.PlatformChannelCreateInput
		if !bindPlatformCatalogJSON(c, &input) {
			return
		}
		channel, err := catalog.CreateChannel(c.Request.Context(), platformAdminActor(c), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"channel": channel})
	})
	group.PATCH("/channels/:channelId", func(c *gin.Context) {
		var input admin.PlatformChannelUpdateInput
		if !bindPlatformCatalogJSON(c, &input) {
			return
		}
		channel, err := catalog.UpdateChannel(c.Request.Context(), platformAdminActor(c), c.Param("channelId"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"channel": channel})
	})
	group.POST("/channels/:channelId/credentials", func(c *gin.Context) {
		var input admin.PlatformCredentialInput
		if !bindPlatformCatalogJSON(c, &input) {
			return
		}
		channel, err := catalog.RotateCredential(c.Request.Context(), platformAdminActor(c), c.Param("channelId"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"channel": channel})
	})
	group.GET("/channels/:channelId/models", func(c *gin.Context) {
		models, err := catalog.ChannelModels(c.Request.Context(), platformAdminActor(c), c.Param("channelId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"models": models})
	})
	saveChannelModel := func(c *gin.Context) {
		var input modelcatalog.ChannelModelRequest
		if !bindPlatformCatalogJSON(c, &input) {
			return
		}
		item, err := catalog.SaveChannelModel(c.Request.Context(), platformAdminActor(c), c.Param("channelId"), c.Param("modelId"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"model": item})
	}
	group.POST("/channels/:channelId/models", saveChannelModel)
	group.PUT("/channels/:channelId/models/:modelId", saveChannelModel)
	group.GET("/models", func(c *gin.Context) {
		page, size, valid := platformAdminPage(c)
		if !valid {
			return
		}
		models, total, err := catalog.LogicalModels(c.Request.Context(), platformAdminActor(c), page, size)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"models": models, "total": total, "page": page, "pageSize": size})
	})
	group.GET("/models/:modelId", func(c *gin.Context) {
		item, err := catalog.LogicalModel(c.Request.Context(), platformAdminActor(c), c.Param("modelId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, item)
	})
	publishModel := func(c *gin.Context) {
		var input admin.PlatformLogicalModelInput
		if !bindPlatformCatalogJSON(c, &input) {
			return
		}
		item, err := catalog.PublishLogicalModel(c.Request.Context(), platformAdminActor(c), c.Param("modelId"), input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, item)
	}
	group.POST("/models", publishModel)
	group.PUT("/models/:modelId", publishModel)
	group.GET("/models/:modelId/prices", func(c *gin.Context) {
		prices, err := catalog.ModelPrices(c.Request.Context(), platformAdminActor(c), c.Param("modelId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"prices": prices})
	})
	group.POST("/models/:modelId/prices", func(c *gin.Context) {
		var input struct {
			Operation string `json:"operation"`
			Points    int64  `json:"points"`
		}
		if !bindPlatformCatalogJSON(c, &input) {
			return
		}
		price, err := catalog.PublishPrice(c.Request.Context(), platformAdminActor(c), c.Param("modelId"), input.Operation, input.Points)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"price": price})
	})
	group.GET("/users/:id/model-grants", func(c *gin.Context) {
		ids, err := catalog.ModelGrants(c.Request.Context(), platformAdminActor(c), c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"logicalModelIds": ids})
	})
	group.PUT("/users/:id/model-grants", func(c *gin.Context) {
		var input struct {
			IDs []string `json:"logicalModelIds"`
		}
		if !bindPlatformCatalogJSON(c, &input) {
			return
		}
		ids, err := catalog.ReplaceModelGrants(c.Request.Context(), platformAdminActor(c), c.Param("id"), input.IDs)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"logicalModelIds": ids})
	})
}

func bindPlatformCatalogJSON(c *gin.Context, input any) bool {
	return bindPlatformJSON(c, input, 128<<10)
}
