package handler

import (
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	localplayback "infinite-canvas/backend/internal/playback"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// 平台与本地复用资源归属、文件和Range读取，平台响应不写浏览器磁盘缓存。
func registerResourceReadRoutes(r *gin.RouterGroup, owners workspaceOwners, resources *asset.Service, playback *localplayback.Service, platformMode bool) {
	r.GET("/resources", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		pageSize, err := parsePositiveQueryInt(c.Query("pageSize"), 200)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		resources, err := resources.Resources(user.ID, pageSize)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"resources": resources})
	})
	r.GET("/resources/:id", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		resource, err := resources.Resource(user.ID, c.Param("id"))
		if err == nil && c.Query("metadata") == "ensure" {
			resource, err = resources.EnsureMediaMetadata(c.Request.Context(), user.ID, c.Param("id"))
			if err != nil {
				if errors.Is(err, context.Canceled) {
					writeFailure(c, 499, 499, "request_cancelled", "检查素材已取消")
					return
				}
				if errors.Is(err, context.DeadlineExceeded) {
					writeFailure(c, http.StatusRequestTimeout, http.StatusRequestTimeout, kernel.ReasonTimeout, "检查素材超时，请重试")
					return
				}
				failService(c, err)
				return
			}
		}
		if err != nil {
			fail(c, http.StatusNotFound, err)
			return
		}
		ok(c, gin.H{"resource": resource})
	})
	r.GET("/resources/:id/file", func(c *gin.Context) {
		user, err := currentUser(c, owners)
		if err != nil {
			failService(c, err)
			return
		}
		delivery, err := resources.PrepareDelivery(user.ID, c.Param("id"), assets.ResourceDeliveryOptions{
			ForceDirect: c.Query("direct") == "1",
			ForceProxy:  c.Query("proxy") == "1",
		})
		if err != nil {
			failService(c, err)
			return
		}
		if delivery.RedirectURL != "" {
			if platformMode {
				failService(c, kernel.Forbidden("平台资源不允许公开跳转"))
				return
			}
			// CDN 或对象存储直连地址允许安全短期缓存
			c.Header("Cache-Control", "private, max-age=86400, stale-while-revalidate=3600")
			c.Header("Referrer-Policy", "no-referrer")
			c.Header("X-Content-Type-Options", "nosniff")
			c.Redirect(http.StatusTemporaryRedirect, delivery.RedirectURL)
			return
		}
		resource := delivery.Resource
		etag := resourceResponseETag(resource)
		// variant=playback：serve 浏览器兼容播放副本（H.265→H.264 转码）。
		// 副本就绪时用独立 ETag 后缀，避免浏览器拿原件缓存命中 304 而继续黑屏。
		usePlayback := c.Query("variant") == "playback" && resource.Provider == "local" &&
			resource.PlaybackStatus == model.PlaybackStatusReady && resource.PlaybackObjectKey != "" && playback != nil
		var stream *assets.ResourceStream
		if usePlayback {
			stream, err = playback.OpenRange(user.ID, resource.ID)
			if errors.Is(err, localplayback.ErrNotReady) {
				usePlayback = false
			} else if err != nil {
				failService(c, err)
				return
			} else {
				defer stream.Body.Close()
				resource = stream.Resource
			}
		}
		serveETag := etag
		if usePlayback {
			serveETag = playbackResponseETag(etag)
		}
		rangeHeader := c.GetHeader("Range")
		if ifRange := strings.TrimSpace(c.GetHeader("If-Range")); ifRange != "" && ifRange != serveETag {
			rangeHeader = ""
		}
		if stream == nil {
			stream, err = resources.OpenRange(user.ID, resource.ID, rangeHeader)
			if err != nil {
				failService(c, err)
				return
			}
			defer stream.Body.Close()
		}
		// 资源 ID 内容不可变（上传永远生成新 ID，不会原地覆盖）：图片可以放心交给浏览器
		// 磁盘强缓存 30 天，大画布二次打开零请求直读磁盘缓存。视频/音频涉及转码副本
		// 就绪与 Range 语义，保持逐次条件请求（304）。
		if platformMode {
			c.Header("Cache-Control", "no-store")
		} else if strings.HasPrefix(resource.MimeType, "image/") {
			c.Header("Cache-Control", "private, max-age=2592000, stale-while-revalidate=86400")
		} else {
			c.Header("Cache-Control", "private, no-cache")
		}
		c.Header("ETag", serveETag)
		c.Header("Accept-Ranges", "bytes")
		c.Header("X-Content-Type-Options", "nosniff")
		if resource.Kind == "file" {
			c.Header("Content-Disposition", "attachment")
			c.Header("Content-Security-Policy", "sandbox")
		}
		if ifNoneMatch(c.GetHeader("If-None-Match"), serveETag) {
			c.Status(http.StatusNotModified)
			return
		}
		if resource.MimeType == "" {
			resource.MimeType = "application/octet-stream"
		}
		if resource.Provider == "local" {
			if seeker, ok := stream.Body.(io.ReadSeeker); ok {
				c.Header("Content-Type", resource.MimeType)
				http.ServeContent(c.Writer, c.Request, resource.ID, resource.UpdatedAt, seeker)
				return
			}
		}
		if stream.ContentRange != "" {
			c.Header("Content-Range", stream.ContentRange)
		}
		if stream.AcceptRanges != "" {
			c.Header("Accept-Ranges", stream.AcceptRanges)
		}
		c.DataFromReader(stream.StatusCode, stream.ContentLength, resource.MimeType, stream.Body, nil)
	})
}

func resourceResponseETag(resource *model.Resource) string {
	value := strings.Trim(strings.TrimSpace(resource.ETag), `"`)
	if value == "" {
		value = fmt.Sprintf("%s-%d-%d", resource.ID, resource.Size, resource.UpdatedAt.UnixNano())
	}
	return strconv.Quote(value)
}

func ifNoneMatch(header string, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(candidate), "W/"))
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}

func playbackResponseETag(original string) string {
	return strings.TrimSuffix(original, `"`) + `:pb"`
}
