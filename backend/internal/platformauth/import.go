//go:build platform

package platformauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

type ImportAccountInput struct {
	CreateAccountInput
	WorkspaceID       string
	SourceFingerprint string
}

// ImportAccount 绑定已迁入数据库的唯一工作区，并原子建立首个管理员与额度根。
func (s *Service) ImportAccount(ctx context.Context, input ImportAccountInput) (*Account, error) {
	input.CreateAccountInput.Role = RoleAdmin
	created, err := validateCreate(input.CreateAccountInput)
	if err != nil {
		return nil, err
	}
	input.CreateAccountInput = created
	if input.WorkspaceID == "" || len(input.WorkspaceID) > 36 || strings.TrimSpace(input.WorkspaceID) != input.WorkspaceID || len(input.SourceFingerprint) != 64 || !isLowerHex(input.SourceFingerprint) {
		return nil, invalid("导入工作区或来源指纹无效")
	}
	hash, err := s.hasher.hash(ctx, input.Password)
	if err != nil {
		return nil, err
	}
	now := s.now()
	account := &Account{ID: uuid.NewString(), Username: input.Username, DisplayName: input.DisplayName, Role: RoleAdmin, Status: StatusActive, AuthVersion: 1, WorkspaceID: input.WorkspaceID, CreatedAt: now, UpdatedAt: now}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&BootstrapMarker{}).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return conflict("already_initialized", "平台已经完成初始化")
		}
		if err := tx.Model(&Account{}).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return conflict("already_initialized", "平台已有账号，拒绝重复导入")
		}
		var existing ImportReceipt
		if err := tx.Where("source_fingerprint = ? OR source_workspace_id = ?", input.SourceFingerprint, input.WorkspaceID).Take(&existing).Error; err == nil {
			return conflict("import_conflict", "此来源工作区已经导入")
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Model(&model.Workspace{}).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return invalid("导入只支持唯一工作区")
		}
		var workspace model.Workspace
		if err := tx.Where("id = ?", input.WorkspaceID).Take(&workspace).Error; err != nil {
			return invalid("迁入工作区不存在")
		}
		rows := []any{
			&BootstrapMarker{ID: "administrator", CreatedAt: now}, account,
			&AccountWorkspace{AccountID: account.ID, WorkspaceID: input.WorkspaceID},
			&Credential{AccountID: account.ID, PasswordHash: hash, UpdatedAt: now},
			&QuotaAccount{AccountID: account.ID, GrantedPoints: *input.InitialPoints, UpdatedAt: now},
			&StorageAccount{AccountID: account.ID, LimitBytes: *input.StorageLimitBytes, UpdatedAt: now},
			&UsageLedger{ID: uuid.NewString(), AccountID: account.ID, EventKey: "initial:" + account.ID, Kind: "grant", Points: *input.InitialPoints, Reason: "导入初始发放", CreatedAt: now},
			&ImportReceipt{SourceFingerprint: input.SourceFingerprint, SourceWorkspaceID: input.WorkspaceID, AccountID: account.ID, CreatedAt: now},
		}
		for _, row := range rows {
			if err := tx.Create(row).Error; err != nil {
				return mapWriteError(err)
			}
		}
		difference, err := json.Marshal(map[string]any{"username": account.Username, "displayName": account.DisplayName, "role": account.Role, "workspaceId": input.WorkspaceID, "initialPoints": *input.InitialPoints, "storageLimitBytes": *input.StorageLimitBytes, "sourceFingerprint": input.SourceFingerprint})
		if err != nil {
			return err
		}
		return addAudit(tx, account.ID, account.ID, "workspace_imported", string(difference), now)
	})
	if err != nil {
		return nil, mapWriteError(err)
	}
	return account, nil
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
