//go:build platform

package platformstorage

import (
	"errors"
	"math"
	"strings"
	"time"

	"infinite-canvas/backend/internal/kernel"

	"gorm.io/gorm"
)

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db: db} }

func quotaExceeded() error {
	return &kernel.AppError{Status: 403, Code: 403, Reason: "storage_quota_exceeded", Message: "存储空间不足，请释放空间后重试"}
}

func conflict() error {
	return &kernel.AppError{Status: 409, Code: 409, Reason: "idempotency_conflict", Message: "存储操作标识与已有请求不一致"}
}

func validate(scope, key string, size int64) error {
	if strings.TrimSpace(scope) == "" || strings.TrimSpace(key) == "" || len(key) > 512 || size < 0 || size > math.MaxInt64/2 {
		return kernel.BadAuthRequest("存储工作区、业务标识或字节数无效")
	}
	return nil
}

// transaction 只重试没有外部副作用的数据库事务，文件写入不得放进该闭包。
func (s *Service) transaction(fn func(*gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = s.db.Transaction(fn)
		if err == nil || !databaseBusy(err) {
			return err
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
		}
	}
	return &kernel.AppError{Status: 503, Code: 503, Reason: "database_busy", Message: "数据库繁忙，请稍后重试", Retryable: true, Cause: err}
}

func databaseBusy(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "database is locked") || strings.Contains(text, "database table is locked") || strings.Contains(text, "sqlite_busy")
}

func accountForWorkspace(tx *gorm.DB, workspaceID string, requireActive bool) (string, error) {
	var row struct{ AccountID string }
	query := tx.Table("platform_account_workspaces AS b").Select("b.account_id").Joins("JOIN platform_accounts AS a ON a.id = b.account_id").Where("b.workspace_id = ?", workspaceID)
	if requireActive {
		query = query.Where("a.status = ?", "active")
	}
	if err := query.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", kernel.NotFound("工作区不存在")
		}
		return "", err
	}
	return row.AccountID, nil
}

func (s *Service) Usage(workspaceID string) (*Usage, error) {
	accountID, err := accountForWorkspace(s.db, workspaceID, false)
	if err != nil {
		return nil, err
	}
	var usage Usage
	if err := s.db.Where("account_id = ?", accountID).First(&usage).Error; err != nil {
		return nil, err
	}
	return &usage, nil
}

func (s *Service) Reservation(workspaceID, key string) (*Reservation, error) {
	if err := validate(workspaceID, key, 0); err != nil {
		return nil, err
	}
	var value Reservation
	if err := s.db.Where("workspace_id = ? AND business_key = ?", workspaceID, key).First(&value).Error; err != nil {
		return nil, err
	}
	return &value, nil
}

// Reserve 的同键重放只接受相同大小。已终结预留不会因网络重试再次占用空间。
func (s *Service) Reserve(workspaceID, key string, size int64) (*Reservation, error) {
	if err := validate(workspaceID, key, size); err != nil {
		return nil, err
	}
	var result Reservation
	err := s.transaction(func(tx *gorm.DB) error {
		var err error
		result, err = reserve(tx, workspaceID, key, size, PurposeManual, false)
		return err
	})
	return &result, err
}

func reserve(tx *gorm.DB, workspaceID, key string, size int64, purpose string, retry bool) (Reservation, error) {
	accountID, err := accountForWorkspace(tx, workspaceID, true)
	if err != nil {
		return Reservation{}, err
	}
	var existing Reservation
	err = tx.Where("workspace_id = ? AND business_key = ?", workspaceID, key).First(&existing).Error
	if err == nil {
		if existing.RequestedBytes != size || existing.Purpose != purpose {
			return existing, conflict()
		}
		if retry && purpose == PurposeUpload && existing.State == StateCommitted {
			var count int64
			if err := tx.Table("resources").Where("user_id = ? AND (upload_key = ? OR id = ?)", workspaceID, key, key).Count(&count).Error; err != nil {
				return existing, err
			}
			if count == 0 {
				return existing, &kernel.AppError{Status: 409, Code: 409, Reason: "upload_result_deleted", Message: "原上传结果已删除，请发起新的上传操作"}
			}
		}
		if !retry || existing.State != StateReleased {
			return existing, nil
		}
		if err := addReservation(tx, accountID, size); err != nil {
			return existing, err
		}
		if err := tx.Model(&existing).Updates(map[string]any{"state": StateHeld, "reserved_bytes": size, "updated_at": time.Now().UTC()}).Error; err != nil {
			return existing, err
		}
		existing.State, existing.ReservedBytes = StateHeld, size
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Reservation{}, err
	}
	if err := addReservation(tx, accountID, size); err != nil {
		return Reservation{}, err
	}
	result := Reservation{ID: kernel.NewID(), AccountID: accountID, WorkspaceID: workspaceID, BusinessKey: key, Purpose: purpose, RequestedBytes: size, ReservedBytes: size, State: StateHeld}
	return result, tx.Create(&result).Error
}

func addReservation(tx *gorm.DB, accountID string, bytes int64) error {
	// 用减法判断避免加法溢出；已有占用超限时连零字节新预留也不能绕过。
	result := tx.Table("platform_storage_accounts").Where("account_id = ? AND used_bytes <= limit_bytes AND reserved_bytes <= limit_bytes - used_bytes AND ? <= limit_bytes - used_bytes - reserved_bytes", accountID, bytes).
		Updates(map[string]any{"reserved_bytes": gorm.Expr("reserved_bytes + ?", bytes), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return quotaExceeded()
	}
	return nil
}

// Extend 按累计容量扩展；较小目标是幂等重放，不会缩减其他在途写入的容量。
func (s *Service) Extend(workspaceID, key string, targetBytes int64) error {
	if err := validate(workspaceID, key, targetBytes); err != nil {
		return err
	}
	return s.transaction(func(tx *gorm.DB) error { return extend(tx, workspaceID, key, targetBytes) })
}

func extend(tx *gorm.DB, workspaceID, key string, targetBytes int64) error {
	if _, err := accountForWorkspace(tx, workspaceID, true); err != nil {
		return err
	}
	var held Reservation
	if err := tx.Where("workspace_id = ? AND business_key = ?", workspaceID, key).First(&held).Error; err != nil {
		return err
	}
	if held.State != StateHeld {
		return conflict()
	}
	if targetBytes <= held.ReservedBytes {
		return nil
	}
	if err := addReservation(tx, held.AccountID, targetBytes-held.ReservedBytes); err != nil {
		return err
	}
	return tx.Model(&held).Updates(map[string]any{"reserved_bytes": targetBytes, "updated_at": time.Now().UTC()}).Error
}
