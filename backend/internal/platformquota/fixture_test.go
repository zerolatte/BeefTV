//go:build platform

package platformquota

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/repository"
)

type fixture struct {
	db      *gorm.DB
	service *Service
	catalog *admin.PlatformCatalog
	modelID string
}

func setup(t *testing.T, points int64) fixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota.db")+"?_foreign_keys=on&_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&model.Workspace{}, &model.Task{}, &model.ModelChannel{}, &model.ChannelModel{}, &model.ChannelModelVariant{}, &model.LogicalModel{}, &model.LogicalModelRevision{}, &model.LogicalModelRoute{}); err != nil {
		t.Fatal(err)
	}
	if err := platformauth.Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"manager", "creator"} {
		role := platformauth.RoleUser
		if id == "manager" {
			role = platformauth.RoleAdmin
		}
		rows := []any{&platformauth.Account{ID: id, Username: id, DisplayName: id, Role: role, Status: platformauth.StatusActive}, &platformauth.AccountWorkspace{AccountID: id, WorkspaceID: "workspace-" + id}, &platformauth.QuotaAccount{AccountID: id, GrantedPoints: points}, &platformauth.StorageAccount{AccountID: id}}
		for _, row := range rows {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Create(&platformauth.UsageLedger{ID: kernel.NewID(), AccountID: id, EventKey: "initial:" + id, Kind: "grant", Points: points, Reason: "测试初始发放"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := admin.NewPlatformCatalog(repository.New(db), admin.PlatformCatalogDependencies{EncryptSecret: func(s string) (string, error) { return "test:" + s, nil }, DecryptSecret: func(s string) (string, error) { return s[5:], nil }})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := catalog.CreateChannel(context.Background(), "manager", admin.PlatformChannelCreateInput{Name: "模拟渠道", Credentials: admin.PlatformCredentialInput{BaseURL: "https://127.0.0.1:9443", APIKey: "fake-key"}})
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := catalog.SaveChannelModel(context.Background(), "manager", channel.ID, "", modelcatalog.ChannelModelRequest{ModelKey: "mock-text", ProviderModelKey: "mock-text", Capability: "text", Protocol: "chat-completion", CapabilityConfig: modelcatalog.DefaultModelCapabilityConfig("chat-completion")})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := modelcatalog.ChannelModelCapabilitySpec(*upstream)
	if err != nil {
		t.Fatal(err)
	}
	spec.Operations = []string{"text_generation"}
	logical, err := catalog.PublishLogicalModel(context.Background(), "manager", "", admin.PlatformLogicalModelInput{Code: "text-test", Name: "模拟文本", Enabled: true, CapabilitySpec: spec, DefaultOptions: map[string]any{}, Routes: []admin.PlatformLogicalRouteInput{{ChannelModelID: upstream.ID, Enabled: true, Weight: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.ReplaceModelGrants(context.Background(), "manager", "creator", []string{logical.Model.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.PublishPrice(context.Background(), "manager", logical.Model.ID, "text_generation", 10); err != nil {
		t.Fatal(err)
	}
	return fixture{db: db, service: New(db), catalog: catalog, modelID: logical.Model.ID}
}

func (f fixture) request(key string) Admission {
	return Admission{AccountID: "creator", WorkspaceID: "workspace-creator", RequestKind: "task:create", RequestKey: key, RequestDigest: Digest([]byte("same-business-request")), LogicalModelID: f.modelID, ModelIntent: modelcatalog.ModelRequestIntent{Capability: "text", Operation: "text_generation", Options: map[string]any{}}}
}

func persistTask(tx *gorm.DB, selection *modelcatalog.PlatformAdmissionSelection, _ string) (*model.Task, error) {
	routed := selection.Routed
	row := &model.Task{ID: kernel.NewID(), UserID: "workspace-creator", Type: "canvas_text", Operation: "text_generation", LogicalModelID: routed.LogicalModel.ID, LogicalModelRevisionID: routed.Revision.ID, RouteID: routed.Route.ID, ChannelModelID: routed.ChannelModel.ID, Status: model.TaskStatusQueued, Prompt: "不会出现在用量响应中的内容"}
	return row, tx.Create(row).Error
}

func (f fixture) balance(t *testing.T) platformauth.QuotaAccount {
	t.Helper()
	var value platformauth.QuotaAccount
	if err := f.db.First(&value, "account_id = ?", "creator").Error; err != nil {
		t.Fatal(err)
	}
	return value
}
func requireReason(t *testing.T, err error, reason string) {
	t.Helper()
	var app *kernel.AppError
	if !errors.As(err, &app) || string(app.Reason) != reason {
		t.Fatalf("期望 %s，实际 %v", reason, err)
	}
}
