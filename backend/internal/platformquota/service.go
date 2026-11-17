//go:build platform

// Package platformquota 管理平台点数及执行事实；网络与任务循环仍由既有领域负责。
package platformquota

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/task"
)

const (
	StateReserved    = "reserved"
	StateSubmitting  = "submitting"
	StateAccepted    = "accepted"
	StateUnknown     = "unknown"
	StateSettled     = "settled"
	StateReleased    = "released"
	StateCompensated = "compensated"
)

type Service struct {
	db  *gorm.DB
	now func() time.Time
}

func New(db *gorm.DB) *Service {
	return &Service{db: db, now: func() time.Time { return time.Now().UTC() }}
}

func failure(status int, reason, message string) error {
	return &kernel.AppError{Status: status, Code: status, Reason: kernel.ErrorReason(reason), Message: message}
}

func validateRequestKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return failure(400, "idempotency_required", "必须提供 clientOperationId")
	}
	value, err := task.ClientOperationID(map[string]any{"metadata": map[string]any{"clientOperationId": key}})
	if err != nil {
		return err
	}
	if value != key {
		return kernel.BadAuthRequest("clientOperationId 格式无效")
	}
	return nil
}

// Digest 只对规范化后的业务参数求摘要，不包含请求号或会话代次。
func Digest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func (s *Service) transaction(ctx context.Context, fn func(*gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = s.db.WithContext(ctx).Transaction(fn)
		if err == nil {
			return nil
		}
		message := strings.ToLower(err.Error())
		if !strings.Contains(message, "database is locked") && !strings.Contains(message, "database table is locked") && !strings.Contains(message, "sqlite_busy") {
			return err
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 10 * time.Millisecond):
			}
		}
	}
	return &kernel.AppError{Status: 503, Code: 503, Reason: "database_busy", Message: "数据库繁忙，请稍后重试", Retryable: true, Cause: err}
}

func requireAccount(tx *gorm.DB, accountID, workspaceID string, active bool) error {
	query := tx.Table("platform_accounts AS a").Joins("JOIN platform_account_workspaces AS b ON b.account_id = a.id").Where("a.id = ? AND b.workspace_id = ?", accountID, workspaceID)
	if active {
		query = query.Where("a.status = ? AND a.must_change_password = ?", platformauth.StatusActive, false)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return kernel.Unauthorized("平台账号或工作区绑定已失效")
	}
	return nil
}

func requireAdmin(tx *gorm.DB, actorID string) error {
	var count int64
	if err := tx.Model(&platformauth.Account{}).Where("id = ? AND role = ? AND status = ? AND must_change_password = ?", actorID, platformauth.RoleAdmin, platformauth.StatusActive, false).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return kernel.Forbidden("需要有效的管理员权限")
	}
	return nil
}

func loadIntent(tx *gorm.DB, workspaceID, id string) (*model.PlatformExecutionIntent, error) {
	var intent model.PlatformExecutionIntent
	if err := tx.Where("id = ? AND workspace_id = ?", id, workspaceID).Take(&intent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, kernel.NotFound("执行意图不存在")
		}
		return nil, err
	}
	return &intent, nil
}

// IntentForTask 供持久任务恢复读取冻结意图，不重新执行准入或解析当前价格。
func (s *Service) IntentForTask(ctx context.Context, workspaceID, taskID string) (*model.PlatformExecutionIntent, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(taskID) == "" {
		return nil, kernel.NotFound("执行意图不存在")
	}
	var intent model.PlatformExecutionIntent
	if err := s.db.WithContext(ctx).Where("workspace_id = ? AND task_id = ?", workspaceID, taskID).Take(&intent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, kernel.NotFound("执行意图不存在")
		}
		return nil, err
	}
	return &intent, nil
}

func ledger(tx *gorm.DB, accountID, event, kind string, points int64, reason string, now time.Time) error {
	return tx.Create(&platformauth.UsageLedger{ID: kernel.NewID(), AccountID: accountID, EventKey: event, Kind: kind, Points: points, Reason: reason, CreatedAt: now}).Error
}
