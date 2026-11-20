package identity

import (
	"crypto/subtle"
	"errors"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const SessionLifetime = 12 * time.Hour

// Store 是身份领域所需的持久化接口。
type Store interface {
	PlatformAccount(string) (*model.PlatformAccount, error)
	PlatformAccountByName(string) (*model.PlatformAccount, error)
	PlatformAccountCount() (int64, error)
	CreatePlatformAccount(*model.PlatformAccount, bool, model.PlatformAudit) error
	PlatformAccounts(string, string, int, int) ([]model.PlatformAccount, int64, error)
	UpdatePlatformAccount(string, int64, map[string]any, bool, model.PlatformAudit) error
	CreatePlatformSession(*model.PlatformSession) error
	PlatformSession(string) (*model.PlatformSession, error)
	DeletePlatformSession(string) error
}

type Service struct {
	store         Store
	dummyHash     string
	passwordSlots chan struct{}
}

func New(store Store) *Service {
	// 未知用户名也执行一次相同的密码哈希，避免快速枚举用户名。
	dummy, _ := hashPassword("not-a-real-account-password")
	return &Service{store: store, dummyHash: dummy, passwordSlots: make(chan struct{}, 4)}
}

func failure(status int, reason, message string) error {
	return &kernel.AppError{Status: status, Code: status, Reason: kernel.ErrorReason(reason), Message: message}
}

func audit(actor, subject, action, reason string) model.PlatformAudit {
	return model.PlatformAudit{ID: kernel.NewID(), ActorID: actor, SubjectID: subject, Action: action, Reason: reason, CreatedAt: time.Now().UTC()}
}

func (s *Service) RequireActive(id string) (*model.PlatformAccount, error) {
	account, err := s.store.PlatformAccount(id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, failure(401, "auth_required", "请先登录")
	}
	if err != nil {
		return nil, err
	}
	if account.Status != model.UserStatusActive {
		return nil, failure(401, "account_disabled", "账号已停用，请联系管理员")
	}
	return account, nil
}

func (s *Service) RequireAdmin(id string) (*model.PlatformAccount, error) {
	account, err := s.RequireActive(id)
	if err != nil {
		return nil, err
	}
	if account.MustChangePassword {
		return nil, failure(403, "password_change_required", "请先修改临时密码")
	}
	if account.Role != model.UserRoleAdmin {
		return nil, kernel.Forbidden("需要管理员权限")
	}
	return account, nil
}

func (s *Service) Initialized() (bool, error) {
	n, err := s.store.PlatformAccountCount()
	return n > 0, err
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$`)

func (s *Service) create(username, displayName, password, actor string, initial bool) (*model.PlatformAccount, error) {
	username, displayName = strings.TrimSpace(username), strings.TrimSpace(displayName)
	if !usernamePattern.MatchString(username) {
		return nil, failure(400, "invalid_username", "用户名需为 3～64 位字母、数字、下划线、点或连字符")
	}
	if displayName == "" || len([]rune(displayName)) > 80 {
		return nil, kernel.BadAuthRequest("显示名称需要 1～80 个字符")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	role := model.UserRoleUser
	if initial {
		role = model.UserRoleAdmin
	}
	account := &model.PlatformAccount{User: model.User{ID: kernel.NewID(), Username: username, DisplayName: displayName, Role: role, Status: model.UserStatusActive}, WorkspaceID: kernel.NewID(), LoginName: strings.ToLower(username), PasswordHash: hash, MustChangePassword: !initial, AuthVersion: 1}
	if initial {
		actor = account.ID
	}
	err = s.store.CreatePlatformAccount(account, initial, audit(actor, account.ID, "account.create", "管理员开户"))
	if errors.Is(err, repository.ErrPlatformInitialized) {
		return nil, failure(409, "already_initialized", err.Error())
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return nil, failure(409, "username_taken", "用户名已存在")
	}
	return account, err
}

func (s *Service) InitializeAdmin(username, password string) (*model.PlatformAccount, error) {
	return s.create(username, username, password, "", true)
}

// RecoverAdmin 仅供持有数据目录访问权的本机命令调用，不注册 HTTP 接口。
func (s *Service) RecoverAdmin(username, password string) error {
	account, err := s.store.PlatformAccountByName(strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		return err
	}
	if account.Role != model.UserRoleAdmin {
		return kernel.Forbidden("只能恢复管理员账号")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	return s.store.UpdatePlatformAccount(account.ID, account.AuthVersion, map[string]any{"password_hash": hash, "must_change_password": false, "status": model.UserStatusActive}, true, audit(account.ID, account.ID, "account.recover", "本机恢复管理员"))
}

func (s *Service) CreateAccount(actor, username, displayName string) (*model.PlatformAccount, string, error) {
	if _, err := s.RequireAdmin(actor); err != nil {
		return nil, "", err
	}
	password, err := randomToken()
	if err != nil {
		return nil, "", err
	}
	account, err := s.create(username, displayName, password, actor, false)
	return account, password, err
}

type LoginResult struct {
	Account   *model.PlatformAccount `json:"user"`
	CSRFToken string                 `json:"csrfToken"`
	Token     string                 `json:"-"`
	ExpiresAt time.Time              `json:"expiresAt"`
}

func (s *Service) Login(username, password string) (*LoginResult, error) {
	select {
	case s.passwordSlots <- struct{}{}:
		defer func() { <-s.passwordSlots }()
	default:
		return nil, failure(429, "login_busy", "登录请求较多，请稍后重试")
	}
	account, err := s.store.PlatformAccountByName(strings.ToLower(strings.TrimSpace(username)))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		verifyPassword(s.dummyHash, password)
		return nil, failure(401, "invalid_credentials", "用户名或密码错误")
	}
	if err != nil {
		return nil, err
	}
	if !verifyPassword(account.PasswordHash, password) {
		return nil, failure(401, "invalid_credentials", "用户名或密码错误")
	}
	if account.Status != model.UserStatusActive {
		return nil, failure(401, "account_disabled", "账号已停用，请联系管理员")
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken()
	if err != nil {
		return nil, err
	}
	expires := time.Now().UTC().Add(SessionLifetime)
	err = s.store.CreatePlatformSession(&model.PlatformSession{TokenHash: tokenHash(token), CSRFHash: tokenHash(csrf), UserID: account.ID, AuthVersion: account.AuthVersion, ExpiresAt: expires})
	if err != nil {
		return nil, err
	}
	return &LoginResult{Account: account, Token: token, CSRFToken: csrf, ExpiresAt: expires}, nil
}

func (s *Service) Authenticate(token string) (*model.PlatformAccount, *model.PlatformSession, error) {
	if len(token) != 43 {
		return nil, nil, failure(401, "auth_required", "请先登录")
	}
	session, err := s.store.PlatformSession(tokenHash(token))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, failure(401, "session_expired", "登录已失效，请重新登录")
	}
	if err != nil {
		return nil, nil, err
	}
	if !time.Now().Before(session.ExpiresAt) {
		return nil, nil, failure(401, "session_expired", "登录已过期，请重新登录")
	}
	account, err := s.RequireActive(session.UserID)
	if err != nil {
		return nil, nil, err
	}
	if account.AuthVersion != session.AuthVersion {
		return nil, nil, failure(401, "session_expired", "登录已失效，请重新登录")
	}
	return account, session, nil
}

func ValidCSRF(session *model.PlatformSession, value string) bool {
	return session != nil && value != "" && subtle.ConstantTimeCompare([]byte(session.CSRFHash), []byte(tokenHash(value))) == 1
}

func (s *Service) Logout(token string) error { return s.store.DeletePlatformSession(tokenHash(token)) }

func (s *Service) ChangePassword(id, oldPassword, newPassword string) error {
	account, err := s.RequireActive(id)
	if err != nil {
		return err
	}
	if !verifyPassword(account.PasswordHash, oldPassword) {
		return failure(400, "invalid_password", "当前密码不正确")
	}
	if oldPassword == newPassword {
		return kernel.BadAuthRequest("新密码不能与原密码相同")
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.store.UpdatePlatformAccount(id, account.AuthVersion, map[string]any{"password_hash": hash, "must_change_password": false}, true, audit(id, id, "account.password", "修改密码"))
}

func (s *Service) ResetPassword(actor, id string) (string, error) {
	if _, err := s.RequireAdmin(actor); err != nil {
		return "", err
	}
	account, err := s.store.PlatformAccount(id)
	if err != nil {
		return "", err
	}
	if account.Role == model.UserRoleAdmin {
		return "", kernel.Forbidden("管理员密码请通过本机恢复命令重置")
	}
	password, err := randomToken()
	if err != nil {
		return "", err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}
	err = s.store.UpdatePlatformAccount(id, account.AuthVersion, map[string]any{"password_hash": hash, "must_change_password": true}, true, audit(actor, id, "account.password_reset", "管理员重置临时密码"))
	return password, err
}

func (s *Service) UpdateAccount(actor, id, displayName string, status model.UserStatus) error {
	if _, err := s.RequireAdmin(actor); err != nil {
		return err
	}
	account, err := s.store.PlatformAccount(id)
	if err != nil {
		return err
	}
	if status != model.UserStatusActive && status != model.UserStatusDisabled {
		return kernel.BadAuthRequest("账号状态无效")
	}
	if account.Role == model.UserRoleAdmin && status != model.UserStatusActive {
		return kernel.Forbidden("不能停用管理员账号")
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len([]rune(displayName)) > 80 {
		return kernel.BadAuthRequest("显示名称需要 1～80 个字符")
	}
	return s.store.UpdatePlatformAccount(id, account.AuthVersion, map[string]any{"display_name": displayName, "status": status}, status != account.Status, audit(actor, id, "account.update", "更新账号资料或状态"))
}

func (s *Service) Accounts(actor, keyword, status string, page, limit int) ([]model.PlatformAccount, int64, error) {
	if _, err := s.RequireAdmin(actor); err != nil {
		return nil, 0, err
	}
	if page < 1 || limit < 1 || limit > 100 {
		return nil, 0, kernel.BadAuthRequest("分页参数无效")
	}
	return s.store.PlatformAccounts(strings.TrimSpace(keyword), status, (page-1)*limit, limit)
}
