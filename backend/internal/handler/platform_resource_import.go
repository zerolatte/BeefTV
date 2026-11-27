package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/asset"
	"time"
)

type resourceImportOwner interface {
	workspaceOwners
	runtimePolicies
}

func registerPlatformResourceImport(r *gin.RouterGroup, owners resourceImportOwner, resources *asset.Service) {
	r.POST("/resources/import", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, owners)
		if !available || !enforceRateLimit(c, "resources-upload:"+user.ID, policy.Request.ResourceUploadPerMinute, time.Minute) {
			return
		}
		var req struct {
			URL      string `json:"url"`
			Kind     string `json:"kind"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Duration int64  `json:"durationMs"`
		}
		if !platformBind(c, &req) {
			return
		}
		resource, err := resources.ImportURL(c.Request.Context(), user.ID, req.URL, req.Kind, policy.Resource.ResourceUploadMB<<20, req.Width, req.Height, req.Duration, c.GetHeader("X-Idempotency-Key"))
		platformResult(c, gin.H{"resource": resource}, err)
	})
}
