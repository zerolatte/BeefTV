//go:build platform

package platformauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
)

const sessionAbsoluteTTL = 24 * time.Hour
const sessionIdleTTL = 2 * time.Hour

type SessionGrant struct {
	Account   *Account  `json:"account"`
	SessionID string    `json:"sessionId"`
	Token     string    `json:"-"`
	CSRFToken string    `json:"csrfToken"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type AuthenticatedSession struct {
	Account   *Account  `json:"account"`
	SessionID string    `json:"sessionId"`
	CSRFHash  string    `json:"-"`
	CSRFToken string    `json:"-"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func randomToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func digestToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// 从会话随机值派生独立令牌，刷新页面和多个标签页共享会话时无需轮换。
func csrfToken(sessionToken string) string {
	mac := hmac.New(sha256.New, []byte(sessionToken))
	_, _ = mac.Write([]byte("beeftv:platform:csrf:v1"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func newGrant(account *Account, now time.Time) (*SessionGrant, *Session, error) {
	token, err := randomToken()
	if err != nil {
		return nil, nil, err
	}
	csrf := csrfToken(token)
	session := &Session{ID: uuid.NewString(), AccountID: account.ID, TokenHash: digestToken(token), CSRFHash: digestToken(csrf), AuthVersion: account.AuthVersion, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(sessionAbsoluteTTL)}
	return &SessionGrant{Account: account, SessionID: session.ID, Token: token, CSRFToken: csrf, ExpiresAt: session.ExpiresAt}, session, nil
}

func invalidLogin() error {
	return kernel.Unauthorized("用户名或密码不正确，或账号暂不可登录")
}

func (s *Service) Login(ctx context.Context, username, password, ip string) (*SessionGrant, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !s.limiter.allow(username, ip, s.now()) {
		return nil, busyPassword()
	}
	if err := validatePassword(password); err != nil {
		return nil, invalidLogin()
	}
	var account Account
	err := s.db.WithContext(ctx).Where("username = ?", username).Take(&account).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	var credential Credential
	if err == nil {
		err = s.db.WithContext(ctx).Where("account_id = ?", account.ID).Take(&credential).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	encoded := credential.PasswordHash
	if encoded == "" {
		encoded = "$argon2id$v=19$m=65536,t=3,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	valid, hashErr := s.hasher.verify(ctx, password, encoded)
	if hashErr != nil {
		return nil, hashErr
	}
	if !valid || err != nil || account.Status != StatusActive {
		if account.ID != "" {
			if err := addSecurityEvent(s.db.WithContext(ctx), account.ID, "login_failed", s.now()); err != nil {
				return nil, err
			}
		}
		return nil, invalidLogin()
	}
	var grant *SessionGrant
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := loadAccount(tx, account.ID)
		if err != nil {
			return err
		}
		if current.Status != StatusActive || current.AuthVersion != account.AuthVersion {
			return invalidLogin()
		}
		var currentCredential Credential
		if err := tx.Where("account_id = ? AND password_hash = ?", account.ID, credential.PasswordHash).Take(&currentCredential).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return invalidLogin()
			}
			return err
		}
		var session *Session
		grant, session, err = newGrant(current, s.now())
		if err != nil {
			return err
		}
		if err := tx.Create(session).Error; err != nil {
			return err
		}
		return addSecurityEvent(tx, account.ID, "login_succeeded", s.now())
	})
	return grant, err
}

func (s *Service) Authenticate(ctx context.Context, token string) (*AuthenticatedSession, error) {
	return s.authenticate(ctx, token, true)
}

// CheckSession 供会话轮询和长连接复核使用，不把后台探测当作用户活动。
func (s *Service) CheckSession(ctx context.Context, token string) (*AuthenticatedSession, error) {
	return s.authenticate(ctx, token, false)
}

func (s *Service) authenticate(ctx context.Context, token string, touch bool) (*AuthenticatedSession, error) {
	if len(token) != 43 {
		return nil, kernel.Unauthorized("会话已失效，请重新登录")
	}
	var result *AuthenticatedSession
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now()
		var session Session
		if err := tx.Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ? AND last_seen_at > ?", digestToken(token), now, now.Add(-sessionIdleTTL)).Take(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return kernel.Unauthorized("会话已失效，请重新登录")
			}
			return err
		}
		account, err := loadAccount(tx, session.AccountID)
		if err != nil {
			return err
		}
		if account.Status != StatusActive || account.AuthVersion != session.AuthVersion {
			return kernel.Unauthorized("会话已失效，请重新登录")
		}
		if touch {
			if err := tx.Model(&Session{}).Where("id = ? AND revoked_at IS NULL", session.ID).Update("last_seen_at", now).Error; err != nil {
				return err
			}
		}
		result = &AuthenticatedSession{Account: account, SessionID: session.ID, CSRFHash: session.CSRFHash, CSRFToken: csrfToken(token), ExpiresAt: session.ExpiresAt}
		return nil
	})
	return result, err
}

func VerifyCSRF(session *AuthenticatedSession, token string) bool {
	return session != nil && len(token) == 43 && subtle.ConstantTimeCompare([]byte(session.CSRFHash), []byte(digestToken(token))) == 1
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session Session
		if err := tx.Where("id = ? AND revoked_at IS NULL", sessionID).Take(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if err := tx.Model(&Session{}).Where("id = ?", sessionID).Update("revoked_at", s.now()).Error; err != nil {
			return err
		}
		return addSecurityEvent(tx, session.AccountID, "logout", s.now())
	})
}

// ChangePassword 撤销旧会话并原子签发新会话，强制改密后才能进入工作区。
func (s *Service) ChangePassword(ctx context.Context, sessionID, oldPassword, newPassword string) (*SessionGrant, error) {
	if err := validatePassword(oldPassword); err != nil {
		return nil, invalidLogin()
	}
	if oldPassword == newPassword {
		return nil, invalid("新密码不能与旧密码相同")
	}
	var original Session
	if err := s.db.WithContext(ctx).Where("id = ? AND revoked_at IS NULL", sessionID).Take(&original).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, invalidLogin()
		}
		return nil, err
	}
	var credential Credential
	if err := s.db.WithContext(ctx).Where("account_id = ?", original.AccountID).Take(&credential).Error; err != nil {
		return nil, err
	}
	valid, err := s.hasher.verify(ctx, oldPassword, credential.PasswordHash)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, invalidLogin()
	}
	hash, err := s.hasher.hash(ctx, newPassword)
	if err != nil {
		return nil, err
	}
	var grant *SessionGrant
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now()
		var currentSession Session
		if err := tx.Where("id = ? AND revoked_at IS NULL AND expires_at > ? AND last_seen_at > ?", sessionID, now, now.Add(-sessionIdleTTL)).Take(&currentSession).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return invalidLogin()
			}
			return err
		}
		account, err := loadAccount(tx, original.AccountID)
		if err != nil {
			return err
		}
		if account.Status != StatusActive || account.AuthVersion != currentSession.AuthVersion {
			return invalidLogin()
		}
		updated := tx.Model(&Credential{}).Where("account_id = ? AND password_hash = ?", account.ID, credential.PasswordHash).Updates(map[string]any{"password_hash": hash, "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return invalidLogin()
		}
		account.AuthVersion++
		account.MustChangePassword = false
		if err := tx.Model(&Account{}).Where("id = ?", account.ID).Updates(map[string]any{"auth_version": account.AuthVersion, "must_change_password": false, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := revokeSessions(tx, account.ID, now); err != nil {
			return err
		}
		var session *Session
		grant, session, err = newGrant(account, now)
		if err != nil {
			return err
		}
		if err := tx.Create(session).Error; err != nil {
			return err
		}
		return addSecurityEvent(tx, account.ID, "password_changed", now)
	})
	return grant, err
}

func revokeSessions(tx *gorm.DB, accountID string, now time.Time) error {
	return tx.Model(&Session{}).Where("account_id = ? AND revoked_at IS NULL", accountID).Update("revoked_at", now).Error
}

func (s *Service) SecurityEvents(ctx context.Context, accountID string, offset, limit int) ([]SecurityEvent, error) {
	if offset < 0 {
		return nil, invalid("分页起点无效")
	}
	if limit < 1 || limit > 100 {
		return nil, invalid("每页记录数必须为 1—100")
	}
	var events []SecurityEvent
	err := s.db.WithContext(ctx).Where("account_id = ?", accountID).Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&events).Error
	return events, err
}
