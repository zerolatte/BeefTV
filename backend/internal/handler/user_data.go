package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/canvas"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/localapp"
	"infinite-canvas/backend/internal/model"
	localplayback "infinite-canvas/backend/internal/playback"
	"infinite-canvas/backend/internal/prompts"

	"github.com/gin-gonic/gin"
)

// userDataUsage is the storage meter the resource page still reads from the
// composition root. Playback preparation stays there because it reloads the
// resource after the domain prepares it.
type userDataUsage interface {
	runtimePolicies
	AccountFileStorageUsage(userID string) (*app.AccountFileStorageUsage, error)
	PrepareResourcePlayback(userID, resourceID string) (*model.Resource, error)
}

type userDataWorkflows interface {
	canvasWorkflowService
	localapp.AssetPort
}

func RegisterUserDataRoutes(r *gin.RouterGroup, owners workspaceOwners, canvasData *canvas.Service, promptsDomain *prompts.Service, library *asset.Library, resources *asset.Service, playbackRuntime *localplayback.Service, usage userDataUsage, canvasWrites canvasLibraryWrites, workflows userDataWorkflows) {
	registerUserDataRoutes(r, owners, canvasData, promptsDomain, library, resources, playbackRuntime, usage, canvasWrites, workflows)
}

// RegisterDesktopUserDataRoutes keeps hosted object-storage controls out of
// the native app while retaining local resource and workspace CRUD.
func RegisterDesktopUserDataRoutes(r *gin.RouterGroup, owners workspaceOwners, canvasData *canvas.Service, promptsDomain *prompts.Service, library *asset.Library, resources *asset.Service, playbackRuntime *localplayback.Service, usage userDataUsage, canvasWrites canvasLibraryWrites, workflows userDataWorkflows) {
	registerUserDataRoutes(r, owners, canvasData, promptsDomain, library, resources, playbackRuntime, usage, canvasWrites, workflows)
}

func registerUserDataRoutes(r *gin.RouterGroup, owners workspaceOwners, canvasData *canvas.Service, promptDomain *prompts.Service, library *asset.Library, resources *asset.Service, playback *localplayback.Service, usage userDataUsage, canvasWrites canvasLibraryWrites, workflows userDataWorkflows) {
	registerCanvasLibraryRoutes(r, owners, canvasData, canvasWrites)
	registerCanvasWorkflowRoutes(r, workflows)
	registerAssetLibraryRoutes(r, owners, canvasData, library, usage)
	registerPromptPreferenceRoutes(r, owners, promptDomain)
	registerResourceReadRoutes(r, owners, resources, playback, false)
	r.GET("/resources/storage-usage", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		usage, err := usage.AccountFileStorageUsage(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"usage": usage, "canvasDocumentMB": canvasData.CanvasDocumentLimitMB()})
	})
	r.PATCH("/resources/storage-usage", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
		var req struct {
			CanvasDocumentMB *int64 `json:"canvasDocumentMB"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.CanvasDocumentMB == nil {
			fail(c, http.StatusBadRequest, kernel.BadAuthRequest("请填写有效的单画布容量上限"))
			return
		}
		limitMB, err := canvasData.UpdateCanvasDocumentLimitMBBy(user.ID, *req.CanvasDocumentMB)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"canvasDocumentMB": limitMB})
	})
	r.DELETE("/resources/playback-cache", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		cleared, err := playback.ClearCache(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"cleared": cleared})
	})
	r.POST("/resources", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, usage)
		if !available || !enforceRateLimit(c, "resources-upload:"+user.ID, policy.Request.ResourceUploadPerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (policy.Resource.ResourceUploadMB<<20)+(1<<20))
		file, err := c.FormFile("file")
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				// 超 MaxBytesReader 上限时 FormFile 返回英文 http 错误，转成与 service 配额校验一致的中文文案。
				fail(c, http.StatusBadRequest, fmt.Errorf("单个上传文件必须小于 %dMB", policy.Resource.ResourceUploadMB))
				return
			}
			fail(c, http.StatusBadRequest, err)
			return
		}
		width, _ := strconv.Atoi(c.PostForm("width"))
		height, _ := strconv.Atoi(c.PostForm("height"))
		durationMs, _ := strconv.ParseInt(c.PostForm("durationMs"), 10, 64)
		resource, err := requestAssetPort(c, workflows).UploadLocalResource(user.ID, file, c.PostForm("kind"), width, height, durationMs, c.GetHeader("X-Idempotency-Key"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"resource": resource})
	})
	r.POST("/resources/:id/playback", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		resource, err := usage.PrepareResourcePlayback(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"resource": resource})
	})
	registerResourceDeriveRoutes(r, owners, resources, usage)
	r.GET("/user-data/snapshot", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		snapshot, err := canvasData.UserDataSnapshot(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, snapshot)
	})
	registerCanvasProjectRoutes(r, owners, canvasData, usage)
}

func hasUserAssetPageFilters(c *gin.Context) bool {
	for _, key := range []string{"pageSize", "kind", "category", "folderId", "uncategorized", "status", "q", "favorite", "recent", "project", "generated", "personal"} {
		if _, present := c.GetQuery(key); present {
			return true
		}
	}
	return false
}

func queryFlag(c *gin.Context, key string) bool {
	value := strings.TrimSpace(c.Query(key))
	return value == "1" || strings.EqualFold(value, "true")
}
