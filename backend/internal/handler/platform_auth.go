package handler

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/workspace"
)

const platformCookie = "beeftv_session"
const platformCSRFCookie = "beeftv_csrf"

func PlatformAuthentication(auth *identity.Service, dataDir string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer = platformResponseWriter{c.Writer}
		c.Header("Cache-Control", "no-store")
		token, _ := c.Cookie(platformCookie)
		account, session, err := auth.Authenticate(token)
		if err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" {
			if c.GetHeader("X-Platform-User") != account.ID {
				failService(c, kernel.Unauthorized("登录身份已变化，请重新打开工作区"))
				c.Abort()
				return
			}
			if !identity.ValidCSRF(session, c.GetHeader("X-CSRF-Token")) {
				failService(c, kernel.Forbidden("请求校验失效，请刷新页面"))
				c.Abort()
				return
			}
		}
		if expected := c.GetHeader("X-Platform-User"); expected != "" && expected != account.ID {
			failService(c, kernel.Unauthorized("登录身份已变化，请重新打开工作区"))
			c.Abort()
			return
		}
		if strings.HasSuffix(c.FullPath(), "/text-events") || strings.HasSuffix(c.FullPath(), "/events") {
			ctx, cancel := context.WithCancel(c.Request.Context())
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			go func() {
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						if _, _, err := auth.Authenticate(token); err != nil {
							cancel()
							return
						}
					}
				}
			}()
		}
		c.Set("platform.account", account)
		c.Set(workspaceContextKey, workspace.Context{ID: account.WorkspaceID, DataDir: dataDir})
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

func PlatformReady() gin.HandlerFunc {
	return func(c *gin.Context) {
		if platformAccount(c).MustChangePassword {
			failService(c, &kernel.AppError{Status: 403, Code: 403, Reason: "password_change_required", Message: "请先修改临时密码"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func platformAccount(c *gin.Context) *model.PlatformAccount {
	return c.MustGet("platform.account").(*model.PlatformAccount)
}

func platformSessionCookies(c *gin.Context, result *identity.LoginResult, secure bool) {
	maxAge := int(time.Until(result.ExpiresAt).Seconds())
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(platformCookie, result.Token, maxAge, "/api", "", secure, true)
	c.SetCookie(platformCSRFCookie, result.CSRFToken, maxAge, "/", "", secure, false)
}

func clearPlatformCookies(c *gin.Context, secure bool) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(platformCookie, "", -1, "/api", "", secure, true)
	c.SetCookie(platformCSRFCookie, "", -1, "/", "", secure, false)
}

// 登录也验证来源，防止跨站请求为浏览器替换登录身份。
func PlatformOrigin(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" {
			requestOrigin := strings.TrimRight(c.GetHeader("Origin"), "/")
			if requestOrigin != "" && requestOrigin != origin {
				failService(c, kernel.Forbidden("不允许的请求来源"))
				c.Abort()
				return
			}
			if c.GetHeader("Sec-Fetch-Site") == "cross-site" {
				failService(c, kernel.Forbidden("不允许跨站请求"))
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

func ValidatePlatformOrigin(raw string) (string, bool, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return "", false, kernel.BadAuthRequest("CANVAS_PUBLIC_ORIGIN 必须是平台访问来源")
	}
	secure := u.Scheme == "https"
	if !secure && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return "", false, kernel.BadAuthRequest("平台公网访问必须使用 HTTPS")
	}
	return u.String(), secure, nil
}

func RegisterPlatformLogin(api *gin.RouterGroup, auth *identity.Service, secure bool) {
	api.POST("/auth/login", func(c *gin.Context) {
		if !enforceRateLimit(c, "platform-login:"+c.ClientIP(), 10, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, kernel.BadAuthRequest("登录参数无效"))
			return
		}
		result, err := auth.Login(req.Username, req.Password)
		if err != nil {
			failService(c, err)
			return
		}
		platformSessionCookies(c, result, secure)
		c.Header("Cache-Control", "no-store")
		ok(c, result)
	})
}

func RegisterPlatformSession(api *gin.RouterGroup, auth *identity.Service, secure bool) {
	api.GET("/auth/session", func(c *gin.Context) { ok(c, gin.H{"user": platformAccount(c)}) })
	api.POST("/auth/logout", func(c *gin.Context) {
		token, _ := c.Cookie(platformCookie)
		if err := auth.Logout(token); err != nil {
			failService(c, err)
			return
		}
		clearPlatformCookies(c, secure)
		ok(c, gin.H{"loggedOut": true})
	})
	api.POST("/auth/password", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var req struct {
			OldPassword string `json:"oldPassword"`
			NewPassword string `json:"newPassword"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, kernel.BadAuthRequest("改密参数无效"))
			return
		}
		if !enforceRateLimit(c, "platform-password:"+platformAccount(c).ID, 5, time.Minute) {
			return
		}
		if err := auth.ChangePassword(platformAccount(c).ID, req.OldPassword, req.NewPassword); err != nil {
			failService(c, err)
			return
		}
		clearPlatformCookies(c, secure)
		ok(c, gin.H{"changed": true})
	})
}
