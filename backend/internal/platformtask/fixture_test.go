//go:build platform

package platformtask_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/admin"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/handler"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/platformtask"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/task"
)

type fixture struct {
	db                                 *gorm.DB
	service                            *platformtask.Service
	quota                              *platformquota.Service
	catalog                            *admin.PlatformCatalog
	router                             *gin.Engine
	modelID, channelID, channelModelID string
	p                                  identity.Principal
}

func setup(t *testing.T, endpoint string, points int64) fixture {
	t.Helper()
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tasks.db")+"?_foreign_keys=on&_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(database.LocalModels()...); err != nil {
		t.Fatal(err)
	}
	if err := platformauth.Migrate(db); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"manager", "creator", "other"} {
		role := platformauth.RoleUser
		if id == "manager" {
			role = platformauth.RoleAdmin
		}
		for _, row := range []any{&model.Workspace{ID: "workspace-" + id}, &platformauth.Account{ID: id, Username: id, DisplayName: id, Role: role, Status: platformauth.StatusActive}, &platformauth.AccountWorkspace{AccountID: id, WorkspaceID: "workspace-" + id}, &platformauth.QuotaAccount{AccountID: id, GrantedPoints: points}, &platformauth.StorageAccount{AccountID: id}} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Create(&platformauth.UsageLedger{ID: kernel.NewID(), AccountID: id, EventKey: "initial:" + id, Kind: "grant", Points: points, Reason: "测试初始发放"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := admin.NewPlatformCatalog(repository.New(db), admin.PlatformCatalogDependencies{EncryptSecret: func(v string) (string, error) { return "test:" + v, nil }, DecryptSecret: func(v string) (string, error) { return v[5:], nil }})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := catalog.CreateChannel(context.Background(), "manager", admin.PlatformChannelCreateInput{Name: "模拟渠道", Credentials: admin.PlatformCredentialInput{BaseURL: endpoint, APIKey: "old-key"}})
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := catalog.SaveChannelModel(context.Background(), "manager", channel.ID, "", modelcatalog.ChannelModelRequest{ModelKey: "mock-text", ProviderModelKey: "upstream-text", Capability: "text", Protocol: "chat-completion", CapabilityConfig: modelcatalog.DefaultModelCapabilityConfig("chat-completion")})
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
	if _, err = catalog.ReplaceModelGrants(context.Background(), "manager", "creator", []string{logical.Model.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = catalog.PublishPrice(context.Background(), "manager", logical.Model.ID, "text_generation", 10); err != nil {
		t.Fatal(err)
	}
	quota := platformquota.New(db)
	service, err := platformtask.New(repository.New(db), quota, catalog)
	if err != nil {
		t.Fatal(err)
	}
	platformtask.EnableLoopbackForTest(t, service, endpoint)
	p := principal("creator")
	f := fixture{db: db, service: service, quota: quota, catalog: catalog, modelID: logical.Model.ID, channelID: channel.ID, channelModelID: upstream.ID, p: p}
	f.router = router(service, p)
	return f
}

func principal(account string) identity.Principal {
	return identity.Principal{Mode: identity.ModePlatform, AccountID: account, WorkspaceID: "workspace-" + account, SessionID: "session-" + account, Role: identity.RoleUser, Status: identity.StatusActive}
}

func router(service *platformtask.Service, p identity.Principal) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api")
	g.Use(func(c *gin.Context) { _ = handler.SetPrincipal(c, p); c.Next() })
	handler.RegisterPlatformTaskRoutes(g, service)
	return r
}

func (f fixture) request(key string) task.CreateRequest {
	return task.CreateRequest{Type: "canvas_text", Operation: "text_generation", LogicalModelID: f.modelID, Prompt: "测试问题", Input: map[string]any{"mode": "text", "metadata": map[string]any{"clientOperationId": key}}}
}

func call(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}

func created(t *testing.T, response *httptest.ResponseRecorder) model.Task {
	t.Helper()
	var envelope struct {
		Data model.Task `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Data.ID == "" {
		t.Fatalf("创建失败：%d %s", response.Code, response.Body.String())
	}
	return envelope.Data
}

func (f fixture) balance(t *testing.T) platformauth.QuotaAccount {
	t.Helper()
	var value platformauth.QuotaAccount
	if err := f.db.First(&value, "account_id = ?", "creator").Error; err != nil {
		t.Fatal(err)
	}
	return value
}
