//go:build platform

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/localcrypto"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/repository"
)

type platformCatalogFixture struct {
	db              *gorm.DB
	service         *PlatformCatalog
	adminID, userID string
}

func newPlatformCatalogFixture(t *testing.T) platformCatalogFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared&_foreign_keys=on"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ConfigurePool(db); err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&model.Workspace{}, &model.ModelChannel{}, &model.ChannelModel{}, &model.ChannelModelVariant{}, &model.LogicalModel{}, &model.LogicalModelRevision{}, &model.LogicalModelRoute{}); err != nil {
		t.Fatal(err)
	}
	if err := platformauth.Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	adminID, userID := uuid.NewString(), uuid.NewString()
	for _, account := range []platformauth.Account{{ID: adminID, Username: "manager", DisplayName: "管理员", Role: platformauth.RoleAdmin, Status: platformauth.StatusActive, CreatedAt: now}, {ID: userID, Username: "creator", DisplayName: "创作者", Role: platformauth.RoleUser, Status: platformauth.StatusActive, CreatedAt: now}} {
		if err := db.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&platformauth.AccountWorkspace{AccountID: account.ID, WorkspaceID: "workspace-" + account.Username}).Error; err != nil {
			t.Fatal(err)
		}
	}
	directory := t.TempDir()
	service, err := NewPlatformCatalog(repository.New(db), PlatformCatalogDependencies{EncryptSecret: func(value string) (string, error) { return localcrypto.Encrypt(directory, value) }, DecryptSecret: func(value string) (string, error) { return localcrypto.Decrypt(directory, value) }})
	if err != nil {
		t.Fatal(err)
	}
	return platformCatalogFixture{db: db, service: service, adminID: adminID, userID: userID}
}

func (f platformCatalogFixture) createChannel(t *testing.T) *PlatformChannelView {
	t.Helper()
	channel, err := f.service.CreateChannel(context.Background(), f.adminID, PlatformChannelCreateInput{Name: "测试上游", Credentials: PlatformCredentialInput{BaseURL: "https://127.0.0.1:9443/v1", APIKey: "test-secret-original", Headers: []modelcatalog.ChannelHeader{{Name: "X-Test", Value: "private-header-value"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

func (f platformCatalogFixture) createLogicalModel(t *testing.T) (*PlatformLogicalModelView, PlatformLogicalModelInput) {
	t.Helper()
	channel := f.createChannel(t)
	upstream, err := f.service.SaveChannelModel(context.Background(), f.adminID, channel.ID, "", modelcatalog.ChannelModelRequest{ModelKey: "mock-text", ProviderModelKey: "upstream-text", DisplayName: "测试文本", Capability: "text", Protocol: "chat-completion", CapabilityConfig: modelcatalog.DefaultModelCapabilityConfig("chat-completion")})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := modelcatalog.ChannelModelCapabilitySpec(*upstream)
	if err != nil {
		t.Fatal(err)
	}
	spec.Operations = []string{"text_generation"}
	input := PlatformLogicalModelInput{Code: "platform-text", Name: "平台文本", Enabled: true, CapabilitySpec: spec, DefaultOptions: map[string]any{}, Routes: []PlatformLogicalRouteInput{{ChannelModelID: upstream.ID, Enabled: true, Weight: 1}}}
	item, err := f.service.PublishLogicalModel(context.Background(), f.adminID, "", input)
	if err != nil {
		t.Fatal(err)
	}
	return item, input
}

func TestPlatformCatalogCredentialRotationAndRedaction(t *testing.T) {
	f := newPlatformCatalogFixture(t)
	channel := f.createChannel(t)
	var stored model.ModelChannel
	if err := f.db.First(&stored, "id = ?", channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.APIKey != "" || stored.SecretKey != "" || strings.Contains(stored.HeadersJSON, "private") || stored.BaseURL != "" {
		t.Fatal("旧渠道字段保留了平台凭据")
	}
	var version model.PlatformCredentialVersion
	if err := f.db.First(&version, "id = ?", channel.CredentialVersionID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(version.Ciphertext, "test-secret-original") || strings.Contains(version.Ciphertext, "private-header-value") {
		t.Fatal("凭据没有加密")
	}
	rotated, err := f.service.RotateCredential(context.Background(), f.adminID, channel.ID, PlatformCredentialInput{BaseURL: "https://127.0.0.1:9443/new", APIKey: "test-secret-new"})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.CredentialVersion != 2 || rotated.CredentialVersionID == channel.CredentialVersionID {
		t.Fatal("凭据轮换未追加新版本")
	}
	disabled := false
	if _, err := f.service.UpdateChannel(context.Background(), f.adminID, channel.ID, PlatformChannelUpdateInput{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	old, err := f.service.ResolveCredentialVersion(context.Background(), channel.ID, channel.CredentialVersionID)
	if err != nil || old.APIKey != "test-secret-original" || old.Headers[0].Value != "private-header-value" {
		t.Fatalf("旧版本不能继续取回：%v", err)
	}
	if _, err := f.service.ResolveCredentialVersion(context.Background(), uuid.NewString(), channel.CredentialVersionID); err == nil {
		t.Fatal("凭据跨渠道读取未拒绝")
	}
	views, total, err := f.service.Channels(context.Background(), f.adminID, 1, 20)
	if err != nil || total != 1 {
		t.Fatalf("渠道读取错误：%v", err)
	}
	var audits []platformauth.Audit
	if err := f.db.Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(map[string]any{"channels": views, "audits": audits})
	for _, secret := range []string{"test-secret-original", "test-secret-new", "private-header-value", "/new", "/v1"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("管理响应或审计泄露：%s", secret)
		}
	}
}

func TestPlatformCatalogModelRevisionsPricesAndGrants(t *testing.T) {
	f := newPlatformCatalogFixture(t)
	item, input := f.createLogicalModel(t)
	var upstream model.ChannelModel
	if err := f.db.First(&upstream, "id = ?", input.Routes[0].ChannelModelID).Error; err != nil {
		t.Fatal(err)
	}
	_, immutableErr := f.service.SaveChannelModel(context.Background(), f.adminID, upstream.ChannelID, upstream.ID, modelcatalog.ChannelModelRequest{ModelKey: upstream.ModelKey, ProviderModelKey: "changed-upstream", Capability: "text", Protocol: "chat-completion", CapabilityConfig: modelcatalog.DefaultModelCapabilityConfig("chat-completion")})
	var immutable *kernel.AppError
	if !errors.As(immutableErr, &immutable) || immutable.Reason != "model_version_in_use" {
		t.Fatalf("历史路由模型允许原地修改：%v", immutableErr)
	}
	firstRevision := *item.Revision
	input.ExpectedRevisionID = item.Revision.ID
	input.Name = "更新后的文本"
	second, err := f.service.PublishLogicalModel(context.Background(), f.adminID, item.Model.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if second.Model.ID != item.Model.ID || second.Revision.Version != 2 {
		t.Fatal("稳定模型和新版本错误")
	}
	var preserved model.LogicalModelRevision
	if err := f.db.First(&preserved, "id = ?", firstRevision.ID).Error; err != nil {
		t.Fatal(err)
	}
	if preserved != firstRevision {
		t.Fatal("发布覆盖了历史版本")
	}
	if _, err := f.service.PublishLogicalModel(context.Background(), f.adminID, item.Model.ID, input); err == nil {
		t.Fatal("陈旧模型发布未冲突")
	}
	grants, err := f.service.ReplaceModelGrants(context.Background(), f.adminID, f.userID, []string{item.Model.ID})
	if err != nil || len(grants) != 1 {
		t.Fatalf("授权失败：%v", err)
	}
	if _, err := f.service.ReplaceModelGrants(context.Background(), f.adminID, f.userID, []string{uuid.NewString()}); err == nil {
		t.Fatal("不存在模型获授权")
	}
	grants, err = f.service.ModelGrants(context.Background(), f.adminID, f.userID)
	if err != nil || len(grants) != 1 {
		t.Fatal("失败授权破坏了原授权")
	}
	if len(input.CapabilitySpec.Operations) == 0 {
		t.Fatal("文本能力未声明操作")
	}
	operation := input.CapabilitySpec.Operations[0]
	price, err := f.service.PublishPrice(context.Background(), f.adminID, item.Model.ID, operation, 3)
	if err != nil {
		t.Fatal(err)
	}
	newPrice, err := f.service.PublishPrice(context.Background(), f.adminID, item.Model.ID, operation, 5)
	if err != nil || newPrice.Version != 2 || newPrice.ID == price.ID {
		t.Fatalf("价格版本没有追加：%v", err)
	}
	prices, err := f.service.ModelPrices(context.Background(), f.adminID, item.Model.ID)
	if err != nil || len(prices) != 2 || prices[1].Points != 3 {
		t.Fatal("历史价格被覆盖")
	}
	if _, err := f.service.PublishPrice(context.Background(), f.adminID, item.Model.ID, "unsupported", 1); err == nil {
		t.Fatal("未知操作发布了价格")
	}
	if _, err := f.service.PublishPrice(context.Background(), f.adminID, item.Model.ID, operation, 0); err == nil {
		t.Fatal("零价未拒绝")
	}
}

func TestPlatformCatalogAdminAndAuditRollback(t *testing.T) {
	f := newPlatformCatalogFixture(t)
	_, err := f.service.CreateChannel(context.Background(), f.userID, PlatformChannelCreateInput{Name: "拒绝", Credentials: PlatformCredentialInput{BaseURL: "https://127.0.0.1/v1", APIKey: "never-stored"}})
	var appError *kernel.AppError
	if !errors.As(err, &appError) || appError.Status != 403 {
		t.Fatalf("非管理员未拒绝：%v", err)
	}
	if err := f.db.Exec("CREATE TRIGGER reject_catalog_audit BEFORE INSERT ON platform_audits BEGIN SELECT RAISE(ABORT, 'audit failed'); END").Error; err != nil {
		t.Fatal(err)
	}
	_, err = f.service.CreateChannel(context.Background(), f.adminID, PlatformChannelCreateInput{Name: "回滚", Credentials: PlatformCredentialInput{BaseURL: "https://127.0.0.1/v1", APIKey: "never-stored"}})
	if err == nil {
		t.Fatal("审计故障没有中止写入")
	}
	for _, table := range []any{&model.ModelChannel{}, &model.PlatformCredentialVersion{}, &model.PlatformChannelState{}} {
		var count int64
		if err := f.db.Model(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("事务留下半成品：%T %d %v", table, count, err)
		}
	}
}

func TestPlatformCatalogReadAndAdmissionFreeze(t *testing.T) {
	f := newPlatformCatalogFixture(t)
	item, input := f.createLogicalModel(t)
	reader := modelcatalog.NewPlatformCatalogReader(repository.New(f.db))
	ctx := context.Background()
	before, err := reader.Read(ctx, f.userID, "workspace-creator")
	if err != nil || len(before.Models) != 0 {
		t.Fatalf("未授权目录不为空：%v", err)
	}
	if _, err := reader.Read(ctx, f.userID, "workspace-manager"); err == nil {
		t.Fatal("目录未校验工作区绑定")
	}
	if _, err := f.service.ReplaceModelGrants(ctx, f.adminID, f.userID, []string{item.Model.ID}); err != nil {
		t.Fatal(err)
	}
	visible, err := reader.Read(ctx, f.userID, "workspace-creator")
	if err != nil || len(visible.Models) != 1 || visible.Models[0].Available {
		t.Fatalf("无价格模型被标为可用：%v", err)
	}
	operation := input.CapabilitySpec.Operations[0]
	price, err := f.service.PublishPrice(ctx, f.adminID, item.Model.ID, operation, 7)
	if err != nil {
		t.Fatal(err)
	}
	intent := modelcatalog.ModelRequestIntent{Capability: "text", Operation: operation}
	if _, err := modelcatalog.ResolvePlatformAdmission(f.db, f.userID, "workspace-creator", item.Model.ID, intent); err == nil {
		t.Fatal("允许在非事务中准入")
	}
	var frozen *modelcatalog.PlatformAdmissionSelection
	err = f.db.Transaction(func(tx *gorm.DB) error {
		var admissionErr error
		frozen, admissionErr = modelcatalog.ResolvePlatformAdmission(tx, f.userID, "workspace-creator", item.Model.ID, intent)
		return admissionErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Price.ID != price.ID || frozen.Price.Points != 7 || frozen.Routed.Revision.ID != item.Revision.ID || frozen.CredentialVersionID == "" {
		t.Fatal("准入快照缺少模型、价格或凭据引用")
	}
	channelID := frozen.Routed.ChannelModel.ChannelID
	if _, err := f.service.RotateCredential(ctx, f.adminID, channelID, PlatformCredentialInput{BaseURL: "https://127.0.0.1:9443/new", APIKey: "rotated-private-key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.PublishPrice(ctx, f.adminID, item.Model.ID, operation, 11); err != nil {
		t.Fatal(err)
	}
	credential, err := f.service.ResolveCredentialVersion(ctx, channelID, frozen.CredentialVersionID)
	if err != nil || credential.APIKey != "test-secret-original" || frozen.Price.Points != 7 {
		t.Fatal("轮换改变了原冻结快照")
	}
	visible, err = reader.Read(ctx, f.userID, "workspace-creator")
	if err != nil || !visible.Models[0].Available || visible.Models[0].PointPrices[operation] != 11 {
		t.Fatal("目录没有及时反映新价格")
	}
	encoded, _ := json.Marshal(visible)
	for _, secret := range []string{"rotated-private-key", "test-secret-original", "127.0.0.1", "channelId", "credentialVersion"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("用户目录泄露内部配置：%s", secret)
		}
	}
	if _, err := f.service.ReplaceModelGrants(ctx, f.adminID, f.userID, []string{}); err != nil {
		t.Fatal(err)
	}
	visible, err = reader.Read(ctx, f.userID, "workspace-creator")
	if err != nil || len(visible.Models) != 0 {
		t.Fatal("撤权后仍缓存旧目录")
	}
	err = f.db.Transaction(func(tx *gorm.DB) error {
		_, err := modelcatalog.ResolvePlatformAdmission(tx, f.userID, "workspace-creator", item.Model.ID, intent)
		return err
	})
	var denied *kernel.AppError
	if !errors.As(err, &denied) || denied.Status != 403 || denied.Reason != "model_not_authorized" {
		t.Fatalf("撤权后的准入未403：%v", err)
	}
	if _, err := f.service.ResolveCredentialVersion(ctx, channelID, frozen.CredentialVersionID); err != nil {
		t.Fatal("撤权阻止了原凭据取回", err)
	}
}
