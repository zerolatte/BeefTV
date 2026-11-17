//go:build platform

package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/platformtask"
	"infinite-canvas/backend/internal/task"
)

// RegisterPlatformTaskRoutes 必须装配到已启用平台会话、Origin 和 CSRF 的路由组。
func RegisterPlatformTaskRoutes(r *gin.RouterGroup, service *platformtask.Service) {
	r.POST("/tasks", func(c *gin.Context) {
		principal, err := CurrentPrincipal(c)
		if err != nil {
			failService(c, err)
			return
		}
		var request task.CreateRequest
		decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			fail(c, 400, errors.New("任务请求格式无效"))
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			fail(c, 400, errors.New("任务请求只能包含一个对象"))
			return
		}
		result, err := service.Create(c.Request.Context(), principal, request)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.GET("/tasks", func(c *gin.Context) {
		principal, err := CurrentPrincipal(c)
		if err != nil {
			failService(c, err)
			return
		}
		limit, _ := strconv.Atoi(c.Query("limit"))
		result, err := service.List(principal, task.ListOptions{Limit: limit, ProjectID: c.Query("projectId"), ActiveOnly: c.Query("activeOnly") == "true"})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.GET("/tasks/by-operation/:operationId", func(c *gin.Context) {
		principal, err := CurrentPrincipal(c)
		if err != nil {
			failService(c, err)
			return
		}
		result, err := service.FindOperation(principal, c.Param("operationId"), c.Query("kind"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.GET("/tasks/:id", func(c *gin.Context) {
		principal, err := CurrentPrincipal(c)
		if err != nil {
			failService(c, err)
			return
		}
		result, err := service.Get(principal, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.GET("/tasks/:id/logs", func(c *gin.Context) {
		principal, err := CurrentPrincipal(c)
		if err != nil {
			failService(c, err)
			return
		}
		result, err := service.Logs(principal, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	for _, action := range []string{"retry", "recover", "query-provider", "cancel"} {
		action := action
		r.POST("/tasks/:id/"+action, func(c *gin.Context) {
			principal, err := CurrentPrincipal(c)
			if err != nil {
				failService(c, err)
				return
			}
			switch action {
			case "retry":
				var request struct {
					ClientOperationID string `json:"clientOperationId"`
				}
				decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
				decoder.DisallowUnknownFields()
				decodeErr := decoder.Decode(&request)
				if decodeErr != nil && decodeErr != io.EOF {
					fail(c, 400, errors.New("重试请求格式无效"))
					return
				}
				if decodeErr == nil && decoder.Decode(new(any)) != io.EOF {
					fail(c, 400, errors.New("重试请求只能包含一个对象"))
					return
				}
				result, err := service.Retry(c.Request.Context(), principal, c.Param("id"), request.ClientOperationID)
				if err != nil {
					failService(c, err)
					return
				}
				ok(c, result)
			case "cancel":
				result, err := service.Cancel(c.Request.Context(), principal, c.Param("id"))
				if err != nil {
					failService(c, err)
					return
				}
				ok(c, result)
			default:
				result, err := service.Recover(c.Request.Context(), principal, c.Param("id"))
				if err != nil {
					failService(c, err)
					return
				}
				ok(c, result)
			}
		})
	}
	registerTaskTextReadRoutes(r, nil, service.TextReplay())
}
