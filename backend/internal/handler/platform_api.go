//go:build platform

package handler

import (
	"errors"
	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/canvas"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platform"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/platformstorage"
	"infinite-canvas/backend/internal/platformtask"
	"infinite-canvas/backend/internal/platformworkflow"
	"infinite-canvas/backend/internal/prompts"
	"net/http"

	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/project"
	"infinite-canvas/backend/internal/projectread"

	"github.com/gin-gonic/gin"
)

type PlatformDependencies struct {
	Auth             *platformauth.Service
	CatalogAdmin     *admin.PlatformCatalog
	CatalogReader    *modelcatalog.PlatformCatalogReader
	Projects         *project.Service
	ProjectReads     *projectread.Service
	Canvas           *canvas.Service
	Policy           *platform.Service
	Prompts          *prompts.Service
	Resources        *asset.Service
	Storage          *platformstorage.Service
	Quota            *platformquota.Service
	Tasks            *platformtask.Service
	Workflows        *platformworkflow.Service
	Origin           string
	InsecureLoopback bool
	InstanceID       string
}

// NewPlatformRouter 只注册已接入平台身份与领域校验的入口。
func NewPlatformRouter(deps PlatformDependencies) (*gin.Engine, error) {
	if deps.Auth == nil || deps.Projects == nil || deps.ProjectReads == nil || deps.InstanceID == "" {
		return nil, errors.New("平台服务装配不完整")
	}
	auth, err := newPlatformAuthentication(deps.Auth, deps.Origin, deps.InsecureLoopback)
	if err != nil {
		return nil, err
	}
	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		return nil, err
	}
	router.Use(func(c *gin.Context) {
		// 默认 Recovery 会记录请求及头部，认证入口只输出固定错误。
		defer func() {
			if recover() != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 500, "data": nil, "msg": "系统处理失败", "reason": "internal_error"})
			}
		}()
		c.Next()
	})
	router.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "same-origin")
		c.Next()
	})
	router.NoRoute(func(c *gin.Context) {
		c.JSON(404, gin.H{"code": 404, "data": nil, "msg": "接口不存在", "reason": "not_found"})
	})
	public := router.Group("/api")
	public.GET("/runtime", func(c *gin.Context) {
		ok(c, gin.H{"mode": "platform", "contractVersion": 1, "instanceId": deps.InstanceID})
	})
	public.GET("/healthz", func(c *gin.Context) { ok(c, gin.H{"status": "ok"}) })
	protected := public.Group("")
	protected.Use(auth.middleware())
	protected.Use(RuntimeDependenciesMiddleware(RuntimeDependencies{RequestCoordinator: deps.Policy}))
	auth.register(public, protected)
	registerPlatformAdminRoutes(protected, deps.Auth)
	if deps.Quota != nil {
		registerPlatformQuotaRoutes(protected, deps.Auth, deps.Quota)
	}
	if deps.Tasks != nil {
		RegisterPlatformTaskRoutes(protected, deps.Tasks)
	}
	if deps.Workflows != nil {
		RegisterPlatformWorkflowRoutes(protected, deps.Workflows)
	}
	if deps.CatalogAdmin != nil {
		registerPlatformCatalogAdminRoutes(protected, deps.Auth, deps.CatalogAdmin)
	}
	if deps.CatalogReader != nil {
		protected.GET("/models/catalog", func(c *gin.Context) {
			principal, err := CurrentPrincipal(c)
			if err != nil {
				failService(c, err)
				return
			}
			catalog, err := deps.CatalogReader.Read(c.Request.Context(), principal.AccountID, principal.WorkspaceID)
			if err != nil {
				failService(c, err)
				return
			}
			ok(c, catalog)
		})
	}
	protected.GET("/workspace/bootstrap", func(c *gin.Context) {
		session := c.MustGet(platformSessionKey).(*platformauth.AuthenticatedSession)
		ok(c, gin.H{
			"contractVersion": 1, "profile": "platform", "storageMode": "remote",
			"instanceId": deps.InstanceID,
			"account":    session.Account, "sessionId": session.SessionID,
			"user":         session.Account,
			"workspace":    gin.H{"id": session.Account.WorkspaceID, "name": session.Account.DisplayName, "owner": "platform", "storage": "sqlite"},
			"capabilities": gin.H{"localAssets": false, "providerCalls": false},
			"features":     gin.H{"shortDrama": true, "assistant": false, "plugins": false, "skills": false, "generation": false},
		})
	})
	protected.GET("/features", func(c *gin.Context) {
		ok(c, gin.H{"shortDrama": true, "assistant": false, "plugins": false, "skills": false, "generation": false})
	})
	// 项目 handler 的平台身份由会话中间件注入，不提供本地所有者回退。
	RegisterProjectRoutes(protected, nil, deps.Projects, deps.ProjectReads)
	if deps.Canvas != nil && deps.Policy != nil {
		registerCanvasProjectRoutes(protected, nil, deps.Canvas, deps.Policy)
		registerCanvasLibraryRoutes(protected, nil, deps.Canvas, platformCanvasWrites{Service: deps.Canvas, policy: deps.Policy})
		if deps.Resources != nil && deps.Storage != nil {
			registerAssetLibraryRoutes(protected, nil, deps.Canvas, deps.Canvas.Library(), deps.Policy)
		}
	}
	if deps.Prompts != nil {
		registerPromptPreferenceRoutes(protected, nil, deps.Prompts)
		RegisterStyleProfileRoutes(protected, nil, deps.Prompts)
	}
	if deps.Resources != nil && deps.Storage != nil && deps.Policy != nil {
		registerPlatformResourceRoutes(protected, deps.Resources, deps.Storage, deps.Policy)
	}
	if err := RegisterPlatformOpenAPIRoutes(protected, router, auth.cookieName()); err != nil {
		return nil, err
	}
	return router, nil
}

type platformCanvasWrites struct {
	*canvas.Service
	policy *platform.Service
}

func (w platformCanvasWrites) RuntimePolicy() (platform.RuntimePolicySetting, error) {
	return w.policy.RuntimePolicy()
}
