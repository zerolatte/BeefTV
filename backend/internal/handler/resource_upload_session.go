package handler

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	localasset "infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

const chunkUploadBodyCapJSON = 16 << 10

type chunkedUploadService interface {
	workspaceOwners
	runtimePolicies
	chunkedUploadOperations
}

type chunkedUploadOperations interface {
	StartChunkedResourceUpload(userID string, req localasset.ChunkedUploadStart) (localasset.ChunkedUploadSessionInfo, error)
	PutChunkedResourceUpload(userID string, uploadID string, index int, body io.Reader) error
	CompleteChunkedResourceUpload(userID string, uploadID string) (*model.Resource, error)
}

// RegisterChunkedUploadRoutes 注册本地媒体分片上传三条接口（POST 开始 / PUT 上传片 / POST 合并）。
func RegisterChunkedUploadRoutes(r *gin.RouterGroup, svc chunkedUploadService, hostedProfile ...bool) {
	_ = hostedProfile
	registerChunkedUploadRoutes(r, svc, svc, svc, false)
}

func registerChunkedUploadRoutes(r *gin.RouterGroup, owners workspaceOwners, policies runtimePolicies, svc chunkedUploadOperations, requireIdentity bool) {
	r.POST("/resources/uploads", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, policies)
		if !available || !enforceRateLimit(c, "resources-upload:"+user.ID, policy.Request.ResourceUploadPerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, chunkUploadBodyCapJSON)
		var req struct {
			FileName       string `json:"fileName"`
			Kind           string `json:"kind"`
			Size           int64  `json:"size"`
			Width          int    `json:"width"`
			Height         int    `json:"height"`
			DurationMs     int64  `json:"durationMs"`
			IdempotencyKey string `json:"idempotencyKey"`
			ContentDigest  string `json:"contentDigest"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(req.IdempotencyKey) == "" {
			req.IdempotencyKey = c.GetHeader("X-Idempotency-Key")
		}
		if requireIdentity && strings.TrimSpace(req.IdempotencyKey) == "" {
			failService(c, &kernel.AppError{Status: 400, Code: 400, Reason: "idempotency_required", Message: "上传操作标识不能为空"})
			return
		}
		if requireIdentity && (len(req.IdempotencyKey) > 256 || req.Size <= 0 || req.Size > 1<<62) {
			failService(c, kernel.BadAuthRequest("上传操作标识或字节数无效"))
			return
		}
		if requireIdentity {
			if err := localasset.ValidateUploadDigest(req.ContentDigest); err != nil {
				failService(c, err)
				return
			}
		}
		session, err := svc.StartChunkedResourceUpload(user.ID, localasset.ChunkedUploadStart{
			FileName: req.FileName, Kind: req.Kind, Size: req.Size,
			Width: req.Width, Height: req.Height, DurationMs: req.DurationMs,
			IdempotencyKey: req.IdempotencyKey, ContentDigest: req.ContentDigest,
		})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"uploadId": session.UploadID, "chunkSize": session.ChunkSize, "chunkCount": session.ChunkCount, "received": session.Received})
	})

	r.PUT("/resources/uploads/:id/chunks/:index", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		index, err := strconv.Atoi(c.Param("index"))
		if err != nil {
			failService(c, localasset.UploadChunkIndexInvalid())
			return
		}
		if err := svc.PutChunkedResourceUpload(user.ID, c.Param("id"), index, c.Request.Body); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"index": index})
	})

	r.POST("/resources/uploads/:id/complete", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		resource, err := svc.CompleteChunkedResourceUpload(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"resource": resource})
	})
}
