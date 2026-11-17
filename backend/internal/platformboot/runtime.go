//go:build platform

// Package platformboot 装配平台进程，所有内容归属均来自账号绑定。
package platformboot

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/asset"
	"infinite-canvas/backend/internal/canvas"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/handler"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platform"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/platformbackup"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/platformstorage"
	"infinite-canvas/backend/internal/platformtask"
	"infinite-canvas/backend/internal/platformworkflow"
	"infinite-canvas/backend/internal/project"
	"infinite-canvas/backend/internal/projectread"
	"infinite-canvas/backend/internal/prompts"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/runtimeinfo"

	"gorm.io/gorm"
)

type Config struct {
	DataDir          string
	Origin           string
	InsecureLoopback bool
}

type Runtime struct {
	db              *gorm.DB
	lock            *runtimeinfo.WorkspaceLock
	cancel          context.CancelFunc
	workers         sync.WaitGroup
	Auth            *platformauth.Service
	Handler         http.Handler
	MigrationBackup string
}

func Open(ctx context.Context, cfg Config) (_ *Runtime, openErr error) {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, errors.New("必须显式配置独立的平台数据目录")
	}
	guard, err := database.PrepareRuntimeDirectory(cfg.DataDir, identity.ModePlatform)
	if err != nil {
		return nil, err
	}
	cfg.DataDir = guard.CanonicalPath()
	lock, err := runtimeinfo.LockWorkspace(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{lock: lock}
	defer func() {
		if openErr != nil {
			_ = runtime.Close()
		}
	}()
	if err := guard.ValidateDatabaseDSN(""); err != nil {
		return nil, err
	}
	db, err := database.Open(database.Config{DataDir: cfg.DataDir})
	if err != nil {
		return nil, err
	}
	runtime.db = db
	if err := database.ConfigurePool(db); err != nil {
		return nil, err
	}
	if err := guard.CheckDatabase(db); err != nil {
		return nil, err
	}
	if err := prepareMasterKey(db, cfg.DataDir); err != nil {
		return nil, err
	}
	runtime.MigrationBackup, err = platformbackup.BeforeMigration(ctx, db, cfg.DataDir, platformauth.CurrentSchemaVersion)
	if err != nil {
		return nil, err
	}
	if err := database.MigrateLocalSchema(db.WithContext(ctx)); err != nil {
		return nil, err
	}
	if err := platformauth.Migrate(db.WithContext(ctx)); err != nil {
		return nil, err
	}
	runtime.Auth = platformauth.NewService(db)
	repo := repository.New(db)
	storage := platformstorage.New(db)
	resources := asset.NewService(asset.Dependencies{Repository: storage.ResourceRepository(repo), Blobs: asset.NewFileStore(cfg.DataDir), Quota: storage.Quota(), LocalStorage: true, DataDir: cfg.DataDir, ResumeChunkedUploads: true})
	catalogAdmin, err := admin.NewPlatformCatalog(repo, admin.PlatformCatalogDependencies{
		EncryptSecret: func(value string) (string, error) { return localcrypto.Encrypt(cfg.DataDir, value) },
		DecryptSecret: func(value string) (string, error) { return localcrypto.Decrypt(cfg.DataDir, value) },
	})
	if err != nil {
		return nil, err
	}
	projects := project.New(repo, project.Dependencies{})
	promptService := prompts.New(repo)
	if err := promptService.EnsureDefaultPromptTemplates(); err != nil {
		return nil, err
	}
	quota := platformquota.New(db)
	platformTasks, err := platformtask.New(repo, quota, catalogAdmin)
	if err != nil {
		return nil, err
	}
	catalogReader := modelcatalog.NewPlatformCatalogReader(repo)
	workflows, err := platformworkflow.New(repo, platformTasks.WorkflowTasks(), catalogReader, platformworkflow.Cipher{
		Encrypt: func(value string) (string, error) { return localcrypto.Encrypt(cfg.DataDir, value) },
		Decrypt: func(value string) (string, error) { return localcrypto.Decrypt(cfg.DataDir, value) },
	})
	if err != nil {
		return nil, err
	}
	coordinator, err := platform.NewCoordinator("")
	if err != nil {
		return nil, err
	}
	policy := platform.New(repo, coordinator, nil)
	var storageMu sync.Mutex
	canvasData := canvas.New(repo, canvas.NewHost(canvas.HostConfig{
		Repo:                        repo,
		EncryptSecret:               func(value string) (string, error) { return localcrypto.Encrypt(cfg.DataDir, value) },
		DecryptSecret:               func(value string) (string, error) { return localcrypto.Decrypt(cfg.DataDir, value) },
		LockStorage:                 func(fn func() error) error { storageMu.Lock(); defer storageMu.Unlock(); return fn() },
		DeleteUserAssetWithResource: resources.DeleteUserAssetWithResources,
		ResourcePolicy: func(tx *repository.Repository) (platform.RuntimeResourcePolicy, error) {
			value, err := platform.RuntimePolicyWithRepo(tx, false)
			return value.Resource, err
		},
	}))
	runtime.Handler, err = handler.NewPlatformRouter(handler.PlatformDependencies{
		Auth: runtime.Auth, Projects: projects, ProjectReads: projectread.New(projects, platformTasks), Origin: cfg.Origin, InsecureLoopback: cfg.InsecureLoopback,
		Canvas: canvasData, Policy: policy, Prompts: promptService,
		Resources: resources, Storage: storage, Quota: quota, Tasks: platformTasks, Workflows: workflows,
		CatalogAdmin: catalogAdmin, CatalogReader: catalogReader,
		InstanceID: guard.InstanceID,
	})
	if err != nil {
		return nil, err
	}
	workerContext, cancel := context.WithCancel(ctx)
	runtime.cancel = cancel
	resources.StartDeletionWorker(workerContext, func(run func(context.Context)) bool {
		runtime.workers.Go(func() { run(workerContext) })
		return true
	})
	return runtime, nil
}

func (r *Runtime) Close() error {
	var errs []error
	if r.cancel != nil {
		r.cancel()
		r.workers.Wait()
		r.cancel = nil
	}
	if r.db != nil {
		if db, err := r.db.DB(); err == nil {
			errs = append(errs, db.Close())
		} else {
			errs = append(errs, err)
		}
		r.db = nil
	}
	if r.lock != nil {
		errs = append(errs, r.lock.Close())
		r.lock = nil
	}
	return errors.Join(errs...)
}
