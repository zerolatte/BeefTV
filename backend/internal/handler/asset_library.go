package handler

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/canvas"
	"infinite-canvas/backend/internal/kernel"
	"net/http"
	"strings"
	"time"
)

func registerAssetLibraryRoutes(r *gin.RouterGroup, owners workspaceOwners, canvasData *canvas.Service, library *asset.Library, usage runtimePolicies) {
	r.POST("/assets/batch", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		assets, err := canvasData.UserAssetsByIDs(user.ID, req.IDs)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"assets": assets})
	})
	r.GET("/assets", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		if _, paged := c.GetQuery("page"); paged || hasUserAssetPageFilters(c) {
			page, pageSize, pageErr := parsePaginationQuery(c, 40)
			if pageErr != nil {
				fail(c, http.StatusBadRequest, pageErr)
				return
			}
			var folderID *string
			if value, present := c.GetQuery("folderId"); present {
				folderID = &value
			}
			assets, pageErr := canvasData.UserAssetsPage(user.ID, page, pageSize, asset.UserAssetPageFilter{
				Kind: c.Query("kind"), Category: c.Query("category"), FolderID: folderID,
				Uncategorized: queryFlag(c, "uncategorized"), Status: c.Query("status"), Query: c.Query("q"),
				Favorite: queryFlag(c, "favorite"), Recent: queryFlag(c, "recent"), Project: c.Query("project"),
				Generated: queryFlag(c, "generated"), Personal: queryFlag(c, "personal"),
			})
			if pageErr != nil {
				failService(c, pageErr)
				return
			}
			ok(c, assets)
			return
		}
		assets, err := canvasData.UserAssetSummaries(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"assets": assets})
	})
	r.GET("/asset-folders", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		folders, err := library.AssetFolders(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"folders": folders})
	})
	r.POST("/asset-folders", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		var req asset.CreateAssetFolderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		folder, err := library.CreateAssetFolder(user.ID, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"folder": folder})
	})
	r.PATCH("/asset-folders/:id", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		var req asset.UpdateAssetFolderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		folder, err := library.UpdateAssetFolder(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"folder": folder})
	})
	r.DELETE("/asset-folders/:id", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		if err := library.DeleteAssetFolder(user.ID, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"id": c.Param("id")})
	})
	r.PATCH("/assets/folder", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		var req asset.MoveUserAssetsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if err := library.MoveUserAssetsToFolder(user.ID, req); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"assetIds": req.AssetIDs, "folderId": req.FolderID})
	})
	r.GET("/assets/:id", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		asset, err := canvasData.UserAsset(user.ID, c.Param("id"))
		if err != nil {
			fail(c, http.StatusNotFound, err)
			return
		}
		ok(c, gin.H{"asset": asset})
	})
	r.PUT("/assets/:id", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, usage)
		if !available || !enforceRateLimit(c, "assets-write:"+user.ID, policy.Request.AssetWritePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 5<<20)
		var req struct {
			Asset json.RawMessage `json:"asset"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(req.Asset, &identity) != nil || identity.ID != c.Param("id") {
			fail(c, http.StatusBadRequest, kernel.BadAuthRequest("素材 ID 与请求路径不一致"))
			return
		}
		asset, err := canvasData.UpsertUserAsset(user.ID, req.Asset)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"asset": asset})
	})
	r.DELETE("/assets/:id", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		if err := canvasData.DeleteUserAsset(user.ID, c.Param("id"), strings.TrimSpace(c.Query("expectedStatus"))); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"id": c.Param("id")})
	})
}
