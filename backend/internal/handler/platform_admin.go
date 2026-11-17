//go:build platform

package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/platformauth"
)

type platformAdminHandler struct{ service *platformauth.Service }

func registerPlatformAdminRoutes(protected *gin.RouterGroup, service *platformauth.Service) {
	h := &platformAdminHandler{service: service}
	admin := protected.Group("/admin")
	admin.Use(platformAdminMiddleware(service))
	admin.GET("/users", h.list)
	admin.POST("/users", h.create)
	admin.PATCH("/users/:id", h.update)
	admin.POST("/users/:id/reset-password", h.resetPassword)
	admin.DELETE("/users/:id", h.delete)
	admin.GET("/users/:id/quota", h.quota)
	admin.PUT("/users/:id/storage-quota", h.storageQuota)
	admin.GET("/audit-logs", h.auditLogs)
}

func platformAdminMiddleware(service *platformauth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, err := CurrentPrincipal(c)
		if err == nil && (principal.Mode != identity.ModePlatform || principal.Role != platformauth.RoleAdmin) {
			err = kernel.Forbidden("需要有效的管理员权限")
		}
		if err == nil {
			err = service.RequireAdmin(c.Request.Context(), principal.AccountID)
		}
		if err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		c.Next()
	}
}

func platformAdminActor(c *gin.Context) string {
	principal, _ := CurrentPrincipal(c)
	return principal.AccountID
}

// 管理写入拒绝未知字段，避免用户名或额度修改被静默忽略。
func bindPlatformAdminJSON(c *gin.Context, input any) bool {
	return bindPlatformJSON(c, input, 4<<10)
}

func bindPlatformJSON(c *gin.Context, input any, maxBytes int64) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		failService(c, kernel.BadAuthRequest("管理请求参数无效"))
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		failService(c, kernel.BadAuthRequest("管理请求只能包含一个 JSON 对象"))
		return false
	}
	return true
}

func platformAdminPage(c *gin.Context) (page, size int, valid bool) {
	page, err := parsePositiveQueryInt(c.Query("page"), 1)
	if err != nil || page > 100000 {
		failService(c, kernel.BadAuthRequest("页码无效"))
		return 0, 0, false
	}
	size, err = parsePositiveQueryInt(c.Query("pageSize"), 20)
	if err != nil || size > 100 {
		failService(c, kernel.BadAuthRequest("每页数量必须为 1—100"))
		return 0, 0, false
	}
	return page, size, true
}

func (h *platformAdminHandler) list(c *gin.Context) {
	page, size, valid := platformAdminPage(c)
	if !valid {
		return
	}
	users, total, err := h.service.ListAccounts(c.Request.Context(), platformAdminActor(c), (page-1)*size, size)
	if err != nil {
		failService(c, err)
		return
	}
	ok(c, gin.H{"users": users, "total": total, "page": page, "pageSize": size})
}

func (h *platformAdminHandler) create(c *gin.Context) {
	var input platformauth.CreateAccountInput
	if !bindPlatformAdminJSON(c, &input) {
		return
	}
	user, err := h.service.CreateAccount(c.Request.Context(), platformAdminActor(c), input)
	if err != nil {
		failService(c, err)
		return
	}
	ok(c, gin.H{"user": user})
}

func (h *platformAdminHandler) update(c *gin.Context) {
	var input platformauth.UpdateAccountInput
	if !bindPlatformAdminJSON(c, &input) {
		return
	}
	if input.DisplayName == nil && input.Role == nil && input.Status == nil {
		failService(c, kernel.BadAuthRequest("请提供要修改的账号字段"))
		return
	}
	if input.Status != nil && *input.Status == platformauth.StatusDeleted {
		failService(c, kernel.BadAuthRequest("请使用注销账号接口"))
		return
	}
	user, err := h.service.UpdateAccount(c.Request.Context(), platformAdminActor(c), c.Param("id"), input)
	if err != nil {
		failService(c, err)
		return
	}
	ok(c, gin.H{"user": user})
}

func (h *platformAdminHandler) resetPassword(c *gin.Context) {
	var input struct {
		Password string `json:"password"`
	}
	if !bindPlatformAdminJSON(c, &input) {
		return
	}
	if err := h.service.ResetPassword(c.Request.Context(), platformAdminActor(c), c.Param("id"), input.Password); err != nil {
		failService(c, err)
		return
	}
	ok(c, gin.H{"reset": true})
}

func (h *platformAdminHandler) delete(c *gin.Context) {
	status := platformauth.StatusDeleted
	user, err := h.service.UpdateAccount(c.Request.Context(), platformAdminActor(c), c.Param("id"), platformauth.UpdateAccountInput{Status: &status})
	if err != nil {
		failService(c, err)
		return
	}
	ok(c, gin.H{"id": user.ID, "status": user.Status})
}

func (h *platformAdminHandler) quota(c *gin.Context) {
	quota, err := h.service.AccountQuota(c.Request.Context(), platformAdminActor(c), c.Param("id"))
	if err != nil {
		failService(c, err)
		return
	}
	ok(c, quota)
}

func (h *platformAdminHandler) storageQuota(c *gin.Context) {
	var input struct {
		LimitBytes *int64 `json:"limitBytes"`
	}
	if !bindPlatformAdminJSON(c, &input) {
		return
	}
	if input.LimitBytes == nil {
		failService(c, kernel.BadAuthRequest("必须明确填写存储上限"))
		return
	}
	quota, err := h.service.UpdateStorageLimit(c.Request.Context(), platformAdminActor(c), c.Param("id"), *input.LimitBytes)
	if err != nil {
		failService(c, err)
		return
	}
	ok(c, quota)
}

func (h *platformAdminHandler) auditLogs(c *gin.Context) {
	page, size, valid := platformAdminPage(c)
	if !valid {
		return
	}
	accountID := strings.TrimSpace(c.Query("accountId"))
	if len(accountID) > 36 {
		failService(c, kernel.BadAuthRequest("账号筛选标识无效"))
		return
	}
	logs, total, err := h.service.AuditLogs(c.Request.Context(), platformAdminActor(c), accountID, (page-1)*size, size)
	if err != nil {
		failService(c, err)
		return
	}
	ok(c, gin.H{"logs": logs, "total": total, "page": page, "pageSize": size})
}
