//go:build platform

package handler

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/platformauth"

	"github.com/gin-gonic/gin"
)

const platformSessionKey = "canvas.platform-session"

type platformAuthentication struct {
	service *platformauth.Service
	origin  string
	secure  bool
	key     []byte
}

func newPlatformAuthentication(service *platformauth.Service, origin string, insecureLoopback bool) (*platformAuthentication, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Opaque != "" {
		return nil, errors.New("平台来源必须为完整且不含路径的 HTTPS Origin")
	}
	secure := u.Scheme == "https"
	if !secure && !(insecureLoopback && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return nil, errors.New("平台必须使用 HTTPS；HTTP 只允许显式启用的本机开发来源")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &platformAuthentication{service: service, origin: origin, secure: secure, key: key}, nil
}

func (a *platformAuthentication) cookieName() string {
	if a.secure {
		return "__Host-beeftv-session"
	}
	return "beeftv-dev-session"
}

func (a *platformAuthentication) csrfCookieName() string {
	if a.secure {
		return "__Host-beeftv-login-csrf"
	}
	return "beeftv-dev-login-csrf"
}

func (a *platformAuthentication) cookie(c *gin.Context, name, value string, expires time.Time, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: maxAge})
}

func (a *platformAuthentication) grant(c *gin.Context, grant *platformauth.SessionGrant) {
	a.cookie(c, a.cookieName(), grant.Token, grant.ExpiresAt, int(time.Until(grant.ExpiresAt).Seconds()))
	a.cookie(c, a.csrfCookieName(), "", time.Unix(1, 0), -1)
	ok(c, grant)
}

func (a *platformAuthentication) sign(payload string) string {
	h := hmac.New(sha256.New, a.key)
	_, _ = h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (a *platformAuthentication) csrfBootstrap(c *gin.Context) {
	if origin := c.GetHeader("Origin"); origin != "" && origin != a.origin {
		failService(c, kernel.Forbidden("请求来源无效"))
		return
	}
	if c.GetHeader("Sec-Fetch-Site") == "cross-site" {
		failService(c, kernel.Forbidden("请求来源无效"))
		return
	}
	if token, err := c.Cookie(a.cookieName()); err == nil && c.Query("purpose") != "login" {
		if session, err := a.service.CheckSession(c.Request.Context(), token); err == nil {
			ok(c, gin.H{"csrfToken": session.CSRFToken})
			return
		}
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		failService(c, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(nonce)
	expires := time.Now().Add(10 * time.Minute)
	payload := token + "." + strconv.FormatInt(expires.Unix(), 10)
	a.cookie(c, a.csrfCookieName(), payload+"."+a.sign(payload), expires, 600)
	ok(c, gin.H{"csrfToken": token})
}

func (a *platformAuthentication) validLoginCSRF(c *gin.Context) bool {
	cookie, err := c.Cookie(a.csrfCookieName())
	if err != nil {
		return false
	}
	parts := strings.Split(cookie, ".")
	if len(parts) != 3 {
		return false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || expires <= time.Now().Unix() || expires > time.Now().Add(10*time.Minute).Unix() {
		return false
	}
	return hmac.Equal([]byte(parts[2]), []byte(a.sign(parts[0]+"."+parts[1]))) && subtle.ConstantTimeCompare([]byte(parts[0]), []byte(c.GetHeader("X-CSRF-Token"))) == 1
}

func (a *platformAuthentication) login(c *gin.Context) {
	if c.GetHeader("Origin") != a.origin || !a.validLoginCSRF(c) {
		failService(c, &kernel.AppError{Status: 403, Code: 403, Reason: "csrf_invalid", Message: "登录验证已失效，请刷新后重试"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		failService(c, kernel.BadAuthRequest("登录参数无效"))
		return
	}
	grant, err := a.service.Login(c.Request.Context(), req.Username, req.Password, c.ClientIP())
	if err != nil {
		failService(c, err)
		return
	}
	a.grant(c, grant)
}

func (a *platformAuthentication) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Cookie(a.cookieName())
		authenticate := a.service.Authenticate
		if c.FullPath() == "/api/auth/me" || strings.HasSuffix(c.FullPath(), "/events") || strings.HasSuffix(c.FullPath(), "/text-events") {
			authenticate = a.service.CheckSession
		}
		session, err := authenticate(c.Request.Context(), token)
		if err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		if expected := c.GetHeader("X-Platform-Session"); expected != "" && expected != session.SessionID {
			failService(c, &kernel.AppError{Status: 401, Code: 401, Reason: "session_mismatch", Message: "会话已在其他标签页切换，请重新确认账号"})
			c.Abort()
			return
		}
		c.Header("X-Platform-Session", session.SessionID)
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Request.Method != http.MethodOptions {
			if c.GetHeader("Origin") != a.origin || !platformauth.VerifyCSRF(session, c.GetHeader("X-CSRF-Token")) {
				failService(c, &kernel.AppError{Status: 403, Code: 403, Reason: "csrf_invalid", Message: "请求验证失败，请刷新后重试"})
				c.Abort()
				return
			}
		}
		if session.Account.MustChangePassword && !passwordRecoveryRoute(c.Request.Method, c.FullPath()) {
			failService(c, &kernel.AppError{Status: 403, Code: 403, Reason: "password_change_required", Message: "请先修改初始密码"})
			c.Abort()
			return
		}
		principal := identity.Principal{Mode: identity.ModePlatform, AccountID: session.Account.ID, WorkspaceID: session.Account.WorkspaceID, Role: session.Account.Role, Status: session.Account.Status, SessionID: session.SessionID}
		if err := SetPrincipal(c, principal); err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		c.Set(platformSessionKey, session)
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		// 长连接定期重验；禁用或改密无需等待 SSE 自然结束。
		go a.watchSession(ctx, cancel, token)
		c.Next()
	}
}

func passwordRecoveryRoute(method, path string) bool {
	return method == http.MethodGet && (path == "/api/auth/me" || path == "/api/auth/security-events") || method == http.MethodPost && (path == "/api/auth/logout" || path == "/api/auth/password")
}

func (a *platformAuthentication) watchSession(ctx context.Context, cancel context.CancelFunc, token string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, stop := context.WithTimeout(ctx, time.Second)
			_, err := a.service.CheckSession(checkCtx, token)
			stop()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

func (a *platformAuthentication) register(public, protected *gin.RouterGroup) {
	public.GET("/auth/csrf", a.csrfBootstrap)
	public.POST("/auth/login", a.login)
	protected.GET("/auth/me", func(c *gin.Context) {
		session := c.MustGet(platformSessionKey).(*platformauth.AuthenticatedSession)
		ok(c, gin.H{"account": session.Account, "sessionId": session.SessionID, "csrfToken": session.CSRFToken, "expiresAt": session.ExpiresAt})
	})
	protected.POST("/auth/logout", func(c *gin.Context) {
		session := c.MustGet(platformSessionKey).(*platformauth.AuthenticatedSession)
		if err := a.service.Logout(c.Request.Context(), session.SessionID); err != nil {
			failService(c, err)
			return
		}
		a.cookie(c, a.cookieName(), "", time.Unix(1, 0), -1)
		ok(c, gin.H{"loggedOut": true})
	})
	protected.POST("/auth/password", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
		var req struct {
			OldPassword string `json:"oldPassword"`
			NewPassword string `json:"newPassword"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, kernel.BadAuthRequest("密码参数无效"))
			return
		}
		session := c.MustGet(platformSessionKey).(*platformauth.AuthenticatedSession)
		grant, err := a.service.ChangePassword(c.Request.Context(), session.SessionID, req.OldPassword, req.NewPassword)
		if err != nil {
			failService(c, err)
			return
		}
		a.grant(c, grant)
	})
	protected.GET("/auth/security-events", func(c *gin.Context) {
		session := c.MustGet(platformSessionKey).(*platformauth.AuthenticatedSession)
		page, err := parsePositiveQueryInt(c.Query("page"), 1)
		if err != nil || page > 100000 {
			failService(c, kernel.BadAuthRequest("页码无效"))
			return
		}
		events, err := a.service.SecurityEvents(c.Request.Context(), session.Account.ID, (page-1)*50, 50)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"events": events, "page": page})
	})
}
