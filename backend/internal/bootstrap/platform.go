package bootstrap

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/credits"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/handler"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/platformadmin"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/runtimeinfo"
	"os"
)

// openPlatform 与桌面路由、外部客户端令牌及默认工作区完全分离。
func openPlatform(cfg Config) (result *Runtime, openErr error) {
	if cfg.DatabaseURL != "" {
		return nil, errors.New("平台数据库必须位于专用数据目录，不支持 DATABASE_URL")
	}
	origin := os.Getenv("CANVAS_PUBLIC_ORIGIN")
	if origin == "" {
		origin = "http://localhost:3000"
	}
	origin, secure, err := handler.ValidatePlatformOrigin(origin)
	if err != nil {
		return nil, err
	}
	if err := database.PreparePlatformDirectory(cfg.DataDir); err != nil {
		return nil, err
	}
	lock, err := runtimeinfo.LockWorkspace(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if openErr != nil {
			_ = lock.Close()
		}
	}()
	db, err := database.Open(database.Config{Driver: cfg.DatabaseDriver, DataDir: cfg.DataDir})
	if err != nil {
		return nil, err
	}
	defer func() {
		if openErr != nil {
			if sqlDB, e := db.DB(); e == nil {
				_ = sqlDB.Close()
			}
		}
	}()
	if err := database.ConfigurePool(db); err != nil {
		return nil, err
	}
	if cfg.AutoMigrate {
		err = database.MigratePlatformSchema(db)
	} else {
		err = database.RequirePlatformSchema(db)
	}
	if err != nil {
		return nil, err
	}
	repo := repository.NewPlatform(db)
	instance, err := repo.PlatformInstance()
	if err != nil {
		return nil, err
	}
	auth := identity.New(repo)
	svc := app.NewLocal(repo, cfg.DataDir)
	defer func() {
		if openErr != nil {
			_ = svc.Close()
		}
	}()
	if err := svc.ValidateRuntime(); err != nil {
		return nil, err
	}
	if err := svc.EnsureDefaultPromptTemplates(); err != nil {
		return nil, err
	}
	if err := svc.EnsureBuiltinProjectWorkflowTemplate(); err != nil {
		return nil, err
	}
	tasks := svc.TaskService()
	billing := credits.New(repo, auth, tasks, svc.RuntimePolicy)
	tasks.ConfigureAdmission(billing)
	router := gin.New()
	_ = router.SetTrustedProxies(nil)
	router.Use(gin.Recovery(), handler.RequestCorrelationMiddleware(), handler.PlatformOrigin(origin))
	api := router.Group("/api")
	api.Use(handler.RuntimeDependenciesMiddleware(handler.RuntimeDependencies{RequestCoordinator: app.NewLocalKernel(svc), Assets: svc.ResourceService(), Projects: svc.ProjectService(), Tasks: tasks, Generation: tasks, TextReplay: svc.TextReplay()}))
	status := newSystemStatus(db, svc, true)
	registerSystemStatusRoutes(api, status)
	handler.RegisterPlatformLogin(api, auth, secure)
	session := api.Group("")
	session.Use(handler.PlatformAuthentication(auth, cfg.DataDir))
	handler.RegisterPlatformSession(session, auth, secure)
	private := session.Group("")
	private.Use(handler.PlatformReady())
	handler.RegisterPlatformWorkspace(private, svc, billing, instance.ID)
	handler.RegisterPlatformAdmin(private, auth, billing, platformadmin.New(auth, repo, svc))
	handler.RegisterPlatformCreative(private, svc)
	router.NoRoute(func(c *gin.Context) { c.JSON(404, gin.H{"code": 404, "reason": "not_found", "msg": "请求不存在"}) })
	return &Runtime{cfg: cfg, db: db, service: svc, handler: router, status: status, workspaceLock: lock, serveErr: make(chan error, 1), platformCredits: billing}, nil
}

func (r *Runtime) runCreditReconciliation(ctx context.Context) {
	if r.platformCredits != nil {
		r.platformCredits.Run(ctx)
	}
}
