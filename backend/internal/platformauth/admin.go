//go:build platform

package platformauth

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
)

type UpdateAccountInput struct {
	DisplayName *string `json:"displayName"`
	Role        *string `json:"role"`
	Status      *string `json:"status"`
}

func (s *Service) UpdateAccount(ctx context.Context, actorID, targetID string, input UpdateAccountInput) (*Account, error) {
	if input.DisplayName != nil && (strings.TrimSpace(*input.DisplayName) == "" || !utf8.ValidString(*input.DisplayName) || utf8.RuneCountInString(*input.DisplayName) > 80) {
		return nil, invalid("显示名必须为 1—80 个字符")
	}
	if input.Role != nil && *input.Role != RoleAdmin && *input.Role != RoleUser {
		return nil, invalid("账号角色无效")
	}
	if input.Status != nil && *input.Status != StatusActive && *input.Status != StatusDisabled && *input.Status != StatusDeleted {
		return nil, invalid("账号状态无效")
	}
	var account *Account
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, actorID); err != nil {
			return err
		}
		var err error
		account, err = loadAccount(tx, targetID)
		if err != nil {
			return err
		}
		if account.Status == StatusDeleted {
			return kernel.NotFound("账号不存在")
		}
		role, status := account.Role, account.Status
		if input.Role != nil {
			role = *input.Role
		}
		if input.Status != nil {
			status = *input.Status
		}
		if actorID == targetID && status != StatusActive {
			return kernel.Forbidden("不能禁用或注销当前管理员")
		}
		if account.Role == RoleAdmin && account.Status == StatusActive && !account.MustChangePassword && (role != RoleAdmin || status != StatusActive) {
			var count int64
			if err := tx.Model(&Account{}).Where("role = ? AND status = ? AND must_change_password = ? AND id <> ?", RoleAdmin, StatusActive, false, targetID).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return conflict("last_admin", "必须保留至少一个有效管理员")
			}
		}
		changes := map[string]any{"updated_at": s.now()}
		diff := map[string]any{}
		if input.DisplayName != nil {
			value := strings.TrimSpace(*input.DisplayName)
			changes["display_name"] = value
			diff["displayName"] = map[string]string{"from": account.DisplayName, "to": value}
			account.DisplayName = value
		}
		if role != account.Role {
			changes["role"] = role
			diff["role"] = map[string]string{"from": account.Role, "to": role}
		}
		if status != account.Status {
			changes["status"] = status
			diff["status"] = map[string]string{"from": account.Status, "to": status}
		}
		if role != account.Role || status != account.Status {
			account.AuthVersion++
			changes["auth_version"] = account.AuthVersion
			if err := revokeSessions(tx, targetID, s.now()); err != nil {
				return err
			}
			if err := addSecurityEvent(tx, targetID, "account_access_changed", s.now()); err != nil {
				return err
			}
		}
		account.Role, account.Status = role, status
		if err := tx.Model(&Account{}).Where("id = ?", targetID).Updates(changes).Error; err != nil {
			return err
		}
		encoded, err := json.Marshal(diff)
		if err != nil {
			return err
		}
		return addAudit(tx, actorID, targetID, "account_updated", string(encoded), s.now())
	})
	return account, err
}

func (s *Service) ResetPassword(ctx context.Context, actorID, targetID, password string) error {
	if err := s.RequireAdmin(ctx, actorID); err != nil {
		return err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, actorID); err != nil {
			return err
		}
		account, err := loadAccount(tx, targetID)
		if err != nil {
			return err
		}
		if account.Status == StatusDeleted {
			return kernel.NotFound("账号不存在")
		}
		updated := tx.Model(&Credential{}).Where("account_id = ?", targetID).Updates(map[string]any{"password_hash": hash, "updated_at": s.now()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return kernel.NotFound("密码凭据不存在")
		}
		if err := tx.Model(&Account{}).Where("id = ?", targetID).Updates(map[string]any{"must_change_password": true, "auth_version": gorm.Expr("auth_version + 1"), "updated_at": s.now()}).Error; err != nil {
			return err
		}
		if err := revokeSessions(tx, targetID, s.now()); err != nil {
			return err
		}
		if err := addSecurityEvent(tx, targetID, "password_reset", s.now()); err != nil {
			return err
		}
		return addAudit(tx, actorID, targetID, "password_reset", "{}", s.now())
	})
}

func (s *Service) ListAccounts(ctx context.Context, actorID string, offset, limit int) ([]Account, int64, error) {
	if err := s.RequireAdmin(ctx, actorID); err != nil {
		return nil, 0, err
	}
	if offset < 0 || limit < 1 || limit > 100 {
		return nil, 0, invalid("分页参数无效")
	}
	rows := []Account{}
	var count int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, actorID); err != nil {
			return err
		}
		if err := tx.Model(&Account{}).Count(&count).Error; err != nil {
			return err
		}
		return tx.Model(&Account{}).Select("platform_accounts.*, platform_account_workspaces.workspace_id").Joins("JOIN platform_account_workspaces ON platform_account_workspaces.account_id = platform_accounts.id").Order("platform_accounts.created_at DESC, platform_accounts.id DESC").Offset(offset).Limit(limit).Scan(&rows).Error
	})
	return rows, count, err
}
