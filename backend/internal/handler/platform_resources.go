//go:build platform

package handler

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformstorage"

	"github.com/gin-gonic/gin"
)

type platformUploads struct {
	resources *asset.Service
	storage   *platformstorage.Service
}

func (p platformUploads) StartChunkedResourceUpload(w string, req asset.ChunkedUploadStart) (asset.ChunkedUploadSessionInfo, error) {
	if err := p.storage.BindUploadRequest(w, req); err != nil {
		return asset.ChunkedUploadSessionInfo{}, err
	}
	return p.resources.StartChunkedUpload(w, req)
}
func (p platformUploads) PutChunkedResourceUpload(w, id string, index int, body io.Reader) error {
	return p.resources.PutChunkedUpload(w, id, index, body)
}
func (p platformUploads) CompleteChunkedResourceUpload(w, id string) (*model.Resource, error) {
	return p.resources.CompleteChunkedUpload(w, id)
}

func registerPlatformResourceRoutes(r *gin.RouterGroup, resources *asset.Service, storage *platformstorage.Service, policy runtimePolicies) {
	registerResourceReadRoutes(r, nil, resources, nil, true)
	registerChunkedUploadRoutes(r, nil, policy, platformUploads{resources, storage}, true)
	r.GET("/resources/storage-usage", func(c *gin.Context) {
		principal, err := CurrentPrincipal(c)
		if err != nil {
			failService(c, err)
			return
		}
		usage, err := storage.Usage(principal.WorkspaceID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"usage": usage})
	})
	r.POST("/resources", func(c *gin.Context) {
		principal, err := CurrentPrincipal(c)
		if err != nil {
			failService(c, err)
			return
		}
		limits, available := loadRuntimePolicy(c, policy)
		if !available || !enforceRateLimit(c, "resources-upload:"+principal.WorkspaceID, limits.Request.ResourceUploadPerMinute, time.Minute) {
			return
		}
		identity := strings.TrimSpace(c.GetHeader("X-Idempotency-Key"))
		if identity == "" || len(identity) > 256 {
			failService(c, &kernel.AppError{Status: 400, Code: 400, Reason: "idempotency_required", Message: "上传操作标识不能为空"})
			return
		}
		size, err := strconv.ParseInt(c.GetHeader("X-Upload-Size"), 10, 64)
		if err != nil || size <= 0 || size > 50<<20 {
			failService(c, kernel.BadAuthRequest("请提供有效的 X-Upload-Size；超过 50 MiB 请使用分片上传"))
			return
		}
		key := asset.NormalizedUploadKey([]string{identity})
		if key == nil {
			failService(c, kernel.BadAuthRequest("上传操作标识无效"))
			return
		}
		if err := storage.EnsureUploadReservation(principal.WorkspaceID, *key, size); err != nil {
			failService(c, err)
			return
		}
		defer func() { _ = storage.ReleaseUnstartedUpload(principal.WorkspaceID, *key) }()
		digest := c.GetHeader("X-Upload-Digest")
		if err := asset.ValidateUploadDigest(digest); err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, size+(1<<20))
		defer func() {
			if c.Request.MultipartForm != nil {
				_ = c.Request.MultipartForm.RemoveAll()
			}
		}()
		file, err := c.FormFile("file")
		if err != nil {
			fail(c, http.StatusBadRequest, kernel.BadAuthRequest("上传内容不完整或超过声明大小"))
			return
		}
		if file.Size != size || len(c.Request.MultipartForm.File["file"]) != 1 || len(c.Request.MultipartForm.File) != 1 {
			failService(c, kernel.BadAuthRequest("上传文件与声明大小不一致"))
			return
		}
		width, _ := strconv.Atoi(c.PostForm("width"))
		height, _ := strconv.Atoi(c.PostForm("height"))
		duration, _ := strconv.ParseInt(c.PostForm("durationMs"), 10, 64)
		if err := storage.BindUploadRequest(principal.WorkspaceID, asset.ChunkedUploadStart{
			FileName: file.Filename, Kind: c.PostForm("kind"), Size: size, Width: width, Height: height, DurationMs: duration,
			IdempotencyKey: identity, ContentDigest: digest,
		}); err != nil {
			failService(c, err)
			return
		}
		body, err := file.Open()
		if err != nil {
			failService(c, err)
			return
		}
		err = asset.VerifyUploadContent(body, digest)
		_ = body.Close()
		if err != nil {
			failService(c, err)
			return
		}
		resource, err := resources.Upload(principal.WorkspaceID, file, c.PostForm("kind"), width, height, duration, identity)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"resource": resource})
	})
}
