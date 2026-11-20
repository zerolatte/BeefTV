package repository

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

var ErrPlatformInitialized = errors.New("平台管理员已经初始化")
var ErrAccountChanged = errors.New("账号状态已变化，请重新登录后重试")

func (r *Repository) PlatformAccount(id string) (*model.PlatformAccount, error) {
	var row model.PlatformAccount
	err := r.db.First(&row, "id = ?", id).Error
	return &row, err
}

func (r *Repository) PlatformAccountByName(name string) (*model.PlatformAccount, error) {
	var row model.PlatformAccount
	err := r.db.First(&row, "login_name = ?", name).Error
	return &row, err
}

func (r *Repository) PlatformAccountCount() (int64, error) {
	var n int64
	err := r.db.Model(&model.PlatformAccount{}).Count(&n).Error
	return n, err
}

func (r *Repository) CreatePlatformAccount(row *model.PlatformAccount, initial bool, audit model.PlatformAudit) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if initial {
			var count int64
			if err := tx.Model(&model.PlatformAccount{}).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return ErrPlatformInitialized
			}
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.Workspace{ID: row.WorkspaceID, Name: row.DisplayName + "的工作区"}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.CreditAccount{UserID: row.ID}).Error; err != nil {
			return err
		}
		return tx.Create(&audit).Error
	})
}

func (r *Repository) PlatformAccounts(keyword, status string, offset, limit int) ([]model.PlatformAccount, int64, error) {
	q := r.db.Model(&model.PlatformAccount{})
	if keyword != "" {
		pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(keyword) + "%"
		q = q.Where("(username LIKE ? ESCAPE '\\' OR display_name LIKE ? ESCAPE '\\')", pattern, pattern)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.PlatformAccount{}
	err := q.Order("created_at DESC, id").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func (r *Repository) UpdatePlatformAccount(id string, version int64, changes map[string]any, revoke bool, audit model.PlatformAudit) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		changes["updated_at"] = time.Now().UTC()
		if revoke {
			changes["auth_version"] = gorm.Expr("auth_version + 1")
		}
		result := tx.Model(&model.PlatformAccount{}).Where("id = ? AND auth_version = ?", id, version).Updates(changes)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAccountChanged
		}
		if revoke {
			if err := tx.Where("user_id = ?", id).Delete(&model.PlatformSession{}).Error; err != nil {
				return err
			}
		}
		if changes["status"] == model.UserStatusDisabled {
			if err := tx.Model(&model.Task{}).Where("user_id = ? AND status = ? AND provider_request_id = '' AND started_at IS NULL", id, model.TaskStatusQueued).
				Updates(map[string]any{"status": model.TaskStatusCancelled, "stage": "账号已停用", "completed_at": time.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&audit).Error
	})
}

func (r *Repository) CreatePlatformSession(session *model.PlatformSession) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var account model.PlatformAccount
		if err := tx.First(&account, "id = ? AND status = ? AND auth_version = ?", session.UserID, model.UserStatusActive, session.AuthVersion).Error; err != nil {
			return err
		}
		if err := tx.Where("expires_at <= ?", time.Now().UTC()).Delete(&model.PlatformSession{}).Error; err != nil {
			return err
		}
		return tx.Create(session).Error
	})
}

func (r *Repository) PlatformSession(hash string) (*model.PlatformSession, error) {
	var row model.PlatformSession
	err := r.db.First(&row, "token_hash = ?", hash).Error
	return &row, err
}

func (r *Repository) DeletePlatformSession(hash string) error {
	return r.db.Where("token_hash = ?", hash).Delete(&model.PlatformSession{}).Error
}

func (r *Repository) RecordPlatformAudit(row model.PlatformAudit) error {
	return r.db.Create(&row).Error
}

func (r *Repository) PlatformInstance() (*model.PlatformInstance, error) {
	var row model.PlatformInstance
	err := r.db.First(&row).Error
	return &row, err
}
