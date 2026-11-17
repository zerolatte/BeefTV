//go:build platform

package platformauth

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

const testPassword = "test-password-123!"
const replacementPassword = "replacement-password-456!"

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "platform.db")+"?_busy_timeout=5000&_foreign_keys=on"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	connection.SetMaxOpenConns(1)
	connection.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = connection.Close() })
	if err := db.AutoMigrate(&model.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	return NewService(db)
}

func testCreateInput(username string) CreateAccountInput {
	points, storage := int64(100), int64(1024*1024)
	return CreateAccountInput{Username: username, DisplayName: "测试用户", Password: testPassword, Role: RoleUser, InitialPoints: &points, StorageLimitBytes: &storage}
}

func bootstrapForTest(t *testing.T, s *Service) *Account {
	t.Helper()
	account, err := s.Bootstrap(context.Background(), testCreateInput("manager"))
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func requireReason(t *testing.T, err error, reason string) {
	t.Helper()
	var appErr *kernel.AppError
	if !errors.As(err, &appErr) || string(appErr.Reason) != reason {
		t.Fatalf("期望原因 %s，实际 %v", reason, err)
	}
}

func TestUsernameAndRequiredQuota(t *testing.T) {
	for _, invalid := range []string{"ro", "local", "ROOT", "SYSTEM", "guest", "public", "api", "anonymous", "1user", "中文用户", "Kelvin", "user+name", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if _, err := NormalizeUsername(invalid); err == nil {
			t.Fatalf("应拒绝用户名 %s", invalid)
		}
	}
	got, err := NormalizeUsername("  Alice_1.X-2 ")
	if err != nil || got != "alice_1.x-2" {
		t.Fatalf("用户名未正确规范化: %s %v", got, err)
	}
	input := testCreateInput("valid")
	input.InitialPoints = nil
	if _, err := validateCreate(input); err == nil {
		t.Fatal("不得默认初始点数")
	}
	input = testCreateInput("valid")
	input.StorageLimitBytes = nil
	if _, err := validateCreate(input); err == nil {
		t.Fatal("不得默认存储额度")
	}
	input = testCreateInput("valid")
	*input.InitialPoints = 0
	*input.StorageLimitBytes = 0
	if _, err := validateCreate(input); err != nil {
		t.Fatal("应允许显式零额度", err)
	}
}

func TestBootstrapIsAtomicAndCannotRepeat(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if err := s.db.Exec("CREATE TRIGGER reject_initial_ledger BEFORE INSERT ON platform_usage_ledgers BEGIN SELECT RAISE(ABORT, 'ledger unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bootstrap(ctx, testCreateInput("manager")); err == nil {
		t.Fatal("流水失败必须回滚开户")
	}
	for _, table := range []any{&Account{}, &Credential{}, &AccountWorkspace{}, &BootstrapMarker{}, &QuotaAccount{}, &StorageAccount{}, &model.Workspace{}} {
		var count int64
		if err := s.db.Model(table).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("事务回滚后仍有数据: %T", table)
		}
	}
	if err := s.db.Exec("DROP TRIGGER reject_initial_ledger").Error; err != nil {
		t.Fatal(err)
	}
	admin := bootstrapForTest(t, s)
	if admin.Role != RoleAdmin || admin.MustChangePassword || admin.WorkspaceID == admin.ID {
		t.Fatal("引导主体不符合合同")
	}
	if _, err := s.Bootstrap(ctx, testCreateInput("another")); err == nil {
		t.Fatal("不允许重复引导")
	} else {
		requireReason(t, err, "already_initialized")
	}
	var ledger UsageLedger
	if err := s.db.Where("account_id = ?", admin.ID).Take(&ledger).Error; err != nil {
		t.Fatal(err)
	}
	if ledger.Points != 100 {
		t.Fatal("初始发放不正确")
	}
	if err := Migrate(s.db); err != nil {
		t.Fatal("迁移重复运行失败", err)
	}
}

func TestConcurrentNormalizedUsernameCreatesOnlyOneAccount(t *testing.T) {
	s := testService(t)
	admin := bootstrapForTest(t, s)
	var wg sync.WaitGroup
	errorsChannel := make(chan error, 2)
	for _, username := range []string{"Alice", " alice "} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, err := s.CreateAccount(context.Background(), admin.ID, testCreateInput(name))
			errorsChannel <- err
		}(username)
	}
	wg.Wait()
	close(errorsChannel)
	successes := 0
	for err := range errorsChannel {
		if err == nil {
			successes++
		} else {
			requireReason(t, err, "account_conflict")
		}
	}
	if successes != 1 {
		t.Fatalf("并发开户成功数错误: %d", successes)
	}
	rows, count, err := s.ListAccounts(context.Background(), admin.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(rows) != 2 {
		t.Fatal("账号列表错误")
	}
	for _, row := range rows {
		if row.WorkspaceID == "" {
			t.Fatal("账号列表缺少工作区绑定")
		}
	}
}

func TestSessionsExpireAndCSRFRemainsBound(t *testing.T) {
	s := testService(t)
	admin := bootstrapForTest(t, s)
	ctx := context.Background()
	loginStarted := time.Now()
	grant, err := s.Login(ctx, "MANAGER", testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Argon2id 登录耗时: %s", time.Since(loginStarted))
	if grant.Account.ID != admin.ID || len(grant.Token) != 43 {
		t.Fatal("登录未生成随机会话")
	}
	var stored Session
	if err := s.db.Where("id = ?", grant.SessionID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.TokenHash == grant.Token || stored.CSRFHash == grant.CSRFToken {
		t.Fatal("数据库不应保存原始令牌")
	}
	authStarted := time.Now()
	authenticated, err := s.Authenticate(ctx, grant.Token)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("会话数据库校验耗时: %s", time.Since(authStarted))
	if !VerifyCSRF(authenticated, grant.CSRFToken) || VerifyCSRF(authenticated, grant.Token) || authenticated.CSRFToken != grant.CSRFToken {
		t.Fatal("CSRF绑定或刷新恢复错误")
	}
	second, err := s.Login(ctx, "manager", testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if second.CSRFToken == grant.CSRFToken {
		t.Fatal("新会话必须更换CSRF令牌")
	}
	if VerifyCSRF(authenticated, second.CSRFToken) {
		t.Fatal("跨会话CSRF令牌被接受")
	}
	if err := s.Logout(ctx, grant.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, grant.Token); err == nil {
		t.Fatal("登出后会话未撤销")
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(2*time.Hour + time.Second) }
	if _, err := s.Authenticate(ctx, second.Token); err == nil {
		t.Fatal("空闲过期未生效")
	}
	s.now = func() time.Time { return now }
	third, err := s.Login(ctx, "manager", testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&Session{}).Where("id = ?", third.SessionID).Update("last_seen_at", now.Add(23*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now.Add(24*time.Hour + time.Second) }
	if _, err := s.Authenticate(ctx, third.Token); err == nil {
		t.Fatal("绝对过期未生效")
	}
}

func TestPasswordChangeAndResetRevokeEverySession(t *testing.T) {
	s := testService(t)
	admin := bootstrapForTest(t, s)
	ctx := context.Background()
	user, err := s.CreateAccount(ctx, admin.ID, testCreateInput("alice"))
	if err != nil {
		t.Fatal(err)
	}
	if !user.MustChangePassword {
		t.Fatal("开户密码必须要求修改")
	}
	first, err := s.Login(ctx, "alice", testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Login(ctx, "alice", testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := s.ChangePassword(ctx, first.SessionID, testPassword, replacementPassword)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Account.MustChangePassword {
		t.Fatal("改密后仍强制改密")
	}
	for _, previous := range []*SessionGrant{first, second} {
		if _, err := s.Authenticate(ctx, previous.Token); err == nil {
			t.Fatal("改密后旧会话未撤销")
		}
	}
	if _, err := s.Authenticate(ctx, changed.Token); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPassword(ctx, admin.ID, user.ID, testPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, changed.Token); err == nil {
		t.Fatal("重置后会话未撤销")
	}
	reset, err := s.Login(ctx, "alice", testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !reset.Account.MustChangePassword {
		t.Fatal("重置密码必须要求修改")
	}
}

func TestAdminLifecycleProtectsLastAdminAndRetainsIdentity(t *testing.T) {
	s := testService(t)
	admin := bootstrapForTest(t, s)
	ctx := context.Background()
	userRole, disabled, deleted := RoleUser, StatusDisabled, StatusDeleted
	if _, err := s.UpdateAccount(ctx, admin.ID, admin.ID, UpdateAccountInput{Role: &userRole}); err == nil {
		t.Fatal("不能降级最后一个管理员")
	} else {
		requireReason(t, err, "last_admin")
	}
	if _, err := s.UpdateAccount(ctx, admin.ID, admin.ID, UpdateAccountInput{Status: &disabled}); err == nil {
		t.Fatal("不能停用自己")
	}
	user, err := s.CreateAccount(ctx, admin.ID, testCreateInput("alice"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAccount(ctx, user.ID, testCreateInput("mallory")); err == nil {
		t.Fatal("普通用户不能开户")
	}
	grant, err := s.Login(ctx, "alice", testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccount(ctx, admin.ID, user.ID, UpdateAccountInput{Status: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, grant.Token); err == nil {
		t.Fatal("禁用未撤销会话")
	}
	if _, err := s.Login(ctx, "alice", testPassword, "127.0.0.1"); err == nil {
		t.Fatal("禁用账号不得登录")
	}
	if _, err := s.UpdateAccount(ctx, admin.ID, user.ID, UpdateAccountInput{Status: &deleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAccount(ctx, admin.ID, testCreateInput("alice")); err == nil {
		t.Fatal("注销后用户名仍须保留")
	}
	var workspace model.Workspace
	if err := s.db.Where("id = ?", user.WorkspaceID).Take(&workspace).Error; err != nil {
		t.Fatal("注销不得删除工作区", err)
	}
}

func TestLoginFailureDoesNotRevealAccountExistence(t *testing.T) {
	s := testService(t)
	bootstrapForTest(t, s)
	ctx := context.Background()
	_, missingErr := s.Login(ctx, "missing", testPassword, "127.0.0.1")
	_, wrongErr := s.Login(ctx, "manager", replacementPassword, "127.0.0.1")
	if missingErr == nil || wrongErr == nil || missingErr.Error() != wrongErr.Error() {
		t.Fatal("登录失败泄露账号状态")
	}
}

func TestLoginRateLimits(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Now()
	for i := 0; i < 10; i++ {
		if !limiter.allow("alice", "ip-a", now) {
			t.Fatal("提前触发账号限速")
		}
	}
	if limiter.allow("alice", "ip-b", now) {
		t.Fatal("账号限速可以更换IP绕过")
	}
	for i := 0; i < 20; i++ {
		if !limiter.allow(string(rune('a'+i)), "ip-a", now) {
			t.Fatal("提前触发IP限速")
		}
	}
	if limiter.allow("another", "ip-a", now) {
		t.Fatal("IP限速可以更换账号绕过")
	}
	if !limiter.allow("alice", "ip-a", now.Add(16*time.Minute)) {
		t.Fatal("限速窗口未过期")
	}
}

func TestConcurrentAdminDemotionRetainsOneAdministrator(t *testing.T) {
	s := testService(t)
	first := bootstrapForTest(t, s)
	ctx := context.Background()
	input := testCreateInput("second_manager")
	input.Role = RoleAdmin
	second, err := s.CreateAccount(ctx, first.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := s.Login(ctx, second.Username, testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangePassword(ctx, grant.SessionID, testPassword, replacementPassword); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	role := RoleUser
	for _, accountID := range []string{first.ID, second.ID} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			_, err := s.UpdateAccount(ctx, id, id, UpdateAccountInput{Role: &role})
			results <- err
		}(accountID)
	}
	wait.Wait()
	close(results)
	var succeeded int
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			requireReason(t, err, "last_admin")
		}
	}
	if succeeded != 1 {
		t.Fatalf("应只允许一名管理员降级，实际成功 %d 次", succeeded)
	}
	var remaining int64
	if err := s.db.Model(&Account{}).Where("role = ? AND status = ?", RoleAdmin, StatusActive).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("管理员数量错误: %d", remaining)
	}
}
