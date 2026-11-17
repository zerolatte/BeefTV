//go:build platform

package platformauth

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

const MaxPoints int64 = 1<<53 - 1

type Service struct {
	db      *gorm.DB
	hasher  *passwordHasher
	limiter *loginLimiter
	now     func() time.Time
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, hasher: processPasswordHasher, limiter: newLoginLimiter(), now: func() time.Time { return time.Now().UTC() }}
}

type CreateAccountInput struct {
	Username          string `json:"username"`
	DisplayName       string `json:"displayName"`
	Password          string `json:"password"`
	Role              string `json:"role"`
	InitialPoints     *int64 `json:"initialPoints"`
	StorageLimitBytes *int64 `json:"storageLimitBytes"`
}

func invalid(message string) error { return kernel.BadAuthRequest(message) }
func conflict(reason, message string) error {
	return &kernel.AppError{Status: 409, Code: 409, Reason: kernel.ErrorReason(reason), Message: message}
}
func busyPassword() error {
	return &kernel.AppError{Status: 429, Code: 429, Reason: "rate_limited", Message: "认证请求过多，请稍后重试", Retryable: true}
}

var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,31}$`)

func NormalizeUsername(value string) (string, error) {
	value = strings.TrimSpace(value)
	for _, character := range value {
		if character > 127 {
			return "", invalid("用户名只能包含 ASCII 字符")
		}
	}
	value = strings.ToLower(value)
	if !usernamePattern.MatchString(value) {
		return "", invalid("用户名必须为 3—32 位 ASCII 字母、数字或 ._-，并以字母开头")
	}
	switch value {
	case "local", "system", "root", "anonymous", "guest", "public", "api":
		return "", invalid("该用户名为系统保留名称")
	}
	return value, nil
}

func validateCreate(input CreateAccountInput) (CreateAccountInput, error) {
	var err error
	input.Username, err = NormalizeUsername(input.Username)
	if err != nil {
		return input, err
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" || !utf8.ValidString(input.DisplayName) || utf8.RuneCountInString(input.DisplayName) > 80 {
		return input, invalid("显示名必须为 1—80 个字符")
	}
	if input.Role != RoleAdmin && input.Role != RoleUser {
		return input, invalid("账号角色无效")
	}
	if input.InitialPoints == nil || *input.InitialPoints < 0 || *input.InitialPoints > MaxPoints {
		return input, invalid("必须明确填写范围内的初始整数点数")
	}
	if input.StorageLimitBytes == nil || *input.StorageLimitBytes < 0 || *input.StorageLimitBytes > MaxPoints {
		return input, invalid("必须明确填写范围内的存储上限")
	}
	return input, validatePassword(input.Password)
}

func (s *Service) Bootstrap(ctx context.Context, input CreateAccountInput) (*Account, error) {
	input.Role = RoleAdmin
	return s.create(ctx, "", input, true)
}

func (s *Service) CreateAccount(ctx context.Context, actorAccountID string, input CreateAccountInput) (*Account, error) {
	return s.create(ctx, actorAccountID, input, false)
}

func (s *Service) create(ctx context.Context, actorID string, input CreateAccountInput, bootstrap bool) (*Account, error) {
	input, err := validateCreate(input)
	if err != nil {
		return nil, err
	}
	if !bootstrap {
		if err := s.RequireAdmin(ctx, actorID); err != nil {
			return nil, err
		}
	}
	hash, err := s.hasher.hash(ctx, input.Password)
	if err != nil {
		return nil, err
	}
	now := s.now()
	account := &Account{ID: uuid.NewString(), Username: input.Username, DisplayName: input.DisplayName, Role: input.Role, Status: StatusActive, MustChangePassword: !bootstrap, AuthVersion: 1, WorkspaceID: uuid.NewString(), CreatedAt: now, UpdatedAt: now}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if bootstrap {
			var count int64
			if err := tx.Model(&BootstrapMarker{}).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return conflict("already_initialized", "平台已经完成初始化")
			}
			if err := tx.Model(&Account{}).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return conflict("already_initialized", "平台已有账号，拒绝重新初始化")
			}
			if err := tx.Create(&BootstrapMarker{ID: "administrator", CreatedAt: now}).Error; err != nil {
				return err
			}
		} else if err := requireAdmin(tx, actorID); err != nil {
			return err
		}
		rows := []any{account, &model.Workspace{ID: account.WorkspaceID, Name: account.DisplayName, CreatedAt: now, UpdatedAt: now}, &AccountWorkspace{AccountID: account.ID, WorkspaceID: account.WorkspaceID}, &Credential{AccountID: account.ID, PasswordHash: hash, UpdatedAt: now}, &QuotaAccount{AccountID: account.ID, GrantedPoints: *input.InitialPoints, UpdatedAt: now}, &StorageAccount{AccountID: account.ID, LimitBytes: *input.StorageLimitBytes, UpdatedAt: now}, &UsageLedger{ID: uuid.NewString(), AccountID: account.ID, EventKey: "initial:" + account.ID, Kind: "grant", Points: *input.InitialPoints, Reason: "开户初始发放", CreatedAt: now}}
		for _, row := range rows {
			if err := tx.Create(row).Error; err != nil {
				return mapWriteError(err)
			}
		}
		if bootstrap {
			actorID = account.ID
		}
		difference, err := json.Marshal(map[string]any{"username": account.Username, "displayName": account.DisplayName, "role": account.Role, "initialPoints": *input.InitialPoints, "storageLimitBytes": *input.StorageLimitBytes})
		if err != nil {
			return err
		}
		return addAudit(tx, actorID, account.ID, "account_created", string(difference), now)
	})
	if err != nil {
		return nil, mapWriteError(err)
	}
	return account, nil
}

func mapWriteError(err error) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) || (err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE CONSTRAINT FAILED")) {
		return conflict("account_conflict", "用户名或账号已经存在")
	}
	return err
}

func (s *Service) RequireAdmin(ctx context.Context, accountID string) error {
	return requireAdmin(s.db.WithContext(ctx), accountID)
}

func requireAdmin(db *gorm.DB, accountID string) error {
	var value Account
	if err := db.Where("id = ? AND role = ? AND status = ? AND must_change_password = ?", accountID, RoleAdmin, StatusActive, false).Take(&value).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return kernel.Forbidden("需要有效的管理员权限")
		}
		return err
	}
	return nil
}

func loadAccount(db *gorm.DB, id string) (*Account, error) {
	var account Account
	if err := db.Where("id = ?", id).Take(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, kernel.NotFound("账号不存在")
		}
		return nil, err
	}
	var binding AccountWorkspace
	if err := db.Where("account_id = ?", id).Take(&binding).Error; err != nil {
		return nil, err
	}
	account.WorkspaceID = binding.WorkspaceID
	return &account, nil
}

func addAudit(db *gorm.DB, actorID, targetID, action, difference string, now time.Time) error {
	return db.Create(&Audit{ID: uuid.NewString(), ActorAccountID: actorID, TargetAccountID: targetID, Action: action, Difference: difference, CreatedAt: now}).Error
}

func addSecurityEvent(db *gorm.DB, accountID, action string, now time.Time) error {
	return db.Create(&SecurityEvent{ID: uuid.NewString(), AccountID: accountID, Action: action, CreatedAt: now}).Error
}
