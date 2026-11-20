package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/credits"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/platform"
	"infinite-canvas/backend/internal/task"
	"strings"
	"time"
)

func RegisterPlatformWorkspace(api *gin.RouterGroup, svc CanvasAPIService, billing *credits.Service, instanceID string) {
	features := platform.FeatureAvailability{ShortDramaEnabled: true, TaskCenterEnabled: true}
	api.GET("/workspace/bootstrap", func(c *gin.Context) {
		u := platformAccount(c)
		limits, err := svc.PublicRuntimeLimits()
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"contractVersion": 1, "profile": "platform", "instanceId": instanceID, "capabilities": gin.H{"localAssets": true, "providerCalls": true}, "user": u, "workspace": gin.H{"id": "platform:" + instanceID + ":" + u.ID, "name": u.DisplayName + "的工作区", "owner": u.ID, "storage": "sqlite"}, "storageMode": "local", "runtimeLimits": limits, "features": features})
	})
	api.GET("/workspace/model-config", func(c *gin.Context) {
		config, err := billing.Catalog(platformAccount(c).ID)
		platformResult(c, gin.H{"config": config, "revision": 1, "health": "ready", "source": "platform"}, err)
	})
	api.GET("/features", func(c *gin.Context) { ok(c, features) })
	api.GET("/credits", func(c *gin.Context) {
		value, err := billing.Account(platformAccount(c).ID)
		platformResult(c, value, err)
	})
	api.GET("/credits/ledger", func(c *gin.Context) {
		p, n := platformPage(c)
		rows, total, err := billing.Ledger(platformAccount(c).ID, p, n)
		platformResult(c, gin.H{"items": rows, "total": total}, err)
	})
	api.POST("/tasks/quote", func(c *gin.Context) {
		if !enforceRateLimit(c, "platform-quote:"+platformAccount(c).ID, 60, time.Minute) {
			return
		}
		var req task.CreateRequest
		if !platformBind(c, &req) {
			return
		}
		value, err := billing.Quote(platformAccount(c).ID, req)
		platformResult(c, value, err)
	})
}

// PlatformCreativeRoutes 只开放账号私有的创作资源；其他注册路由默认拒绝。
func PlatformCreativeRoutes() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := strings.TrimPrefix(c.FullPath(), "/api")
		method := c.Request.Method
		allowed := false
		if strings.HasPrefix(path, "/tasks") {
			allowed = method == "GET" || path == "/tasks" || path == "/tasks/:id/cancel" || path == "/tasks/:id/recover"
		} else {
			for _, prefix := range []string{"/resources", "/assets", "/asset-folders", "/canvas-projects", "/canvas-folders", "/user-data/snapshot", "/projects", "/project-folders"} {
				if path == prefix || strings.HasPrefix(path, prefix+"/") {
					allowed = true
				}
			}
		}
		if path == "/resources/storage-usage" && method != "GET" {
			allowed = false
		}
		if strings.Contains(path, "/workflow") || strings.Contains(path, "/generate") || strings.Contains(path, "/execute") {
			allowed = false
		}
		if !allowed {
			failService(c, kernel.Forbidden("此功能未在平台开放"))
			c.Abort()
			return
		}
		c.Next()
	}
}

func RegisterPlatformCreative(api *gin.RouterGroup, svc CanvasAPIService) {
	r := api.Group("")
	r.Use(PlatformCreativeRoutes())
	RegisterTaskRoutes(r, svc)
	RegisterDesktopUserDataRoutes(r, svc, svc.CanvasService(), svc.PromptService(), svc.AssetLibraryService(), svc.ResourceService(), svc.PlaybackService(), svc, svc, svc)
	RegisterChunkedUploadRoutes(r, svc, false)
	RegisterProjectRoutes(r, svc, svc.ProjectService(), svc)
}
