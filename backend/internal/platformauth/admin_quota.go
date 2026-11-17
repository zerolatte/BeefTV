//go:build platform

package platformauth

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
)

type AccountQuota struct {
	AccountID string         `json:"accountId"`
	Points    PointBalance   `json:"points"`
	Storage   StorageBalance `json:"storage"`
}

type PointBalance struct {
	Granted   int64 `json:"granted"`
	Settled   int64 `json:"settled"`
	Reserved  int64 `json:"reserved"`
	Available int64 `json:"available"`
}

type StorageBalance struct {
	LimitBytes    int64 `json:"limitBytes"`
	UsedBytes     int64 `json:"usedBytes"`
	ReservedBytes int64 `json:"reservedBytes"`
}

func readAccountQuota(tx *gorm.DB, accountID string) (*AccountQuota, error) {
	var points QuotaAccount
	if err := tx.Where("account_id = ?", accountID).Take(&points).Error; err != nil {
		return nil, err
	}
	var storage StorageAccount
	if err := tx.Where("account_id = ?", accountID).Take(&storage).Error; err != nil {
		return nil, err
	}
	// 额度必须可由浏览器整数精确表达，异常账户不能以默认零余额掩盖。
	for _, value := range []int64{points.GrantedPoints, points.SettledPoints, points.ReservedPoints, storage.LimitBytes, storage.UsedBytes, storage.ReservedBytes} {
		if value < 0 || value > MaxPoints {
			return nil, errors.New("平台额度账户数值异常")
		}
	}
	if points.SettledPoints > points.GrantedPoints || points.ReservedPoints > points.GrantedPoints-points.SettledPoints {
		return nil, errors.New("平台点数账户不平衡")
	}
	return &AccountQuota{
		AccountID: accountID,
		Points:    PointBalance{Granted: points.GrantedPoints, Settled: points.SettledPoints, Reserved: points.ReservedPoints, Available: points.GrantedPoints - points.SettledPoints - points.ReservedPoints},
		Storage:   StorageBalance{LimitBytes: storage.LimitBytes, UsedBytes: storage.UsedBytes, ReservedBytes: storage.ReservedBytes},
	}, nil
}

func (s *Service) AccountQuota(ctx context.Context, actorID, targetID string) (*AccountQuota, error) {
	var result *AccountQuota
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, actorID); err != nil {
			return err
		}
		if _, err := loadAccount(tx, targetID); err != nil {
			return err
		}
		var err error
		result, err = readAccountQuota(tx, targetID)
		return err
	})
	return result, err
}

func (s *Service) UpdateStorageLimit(ctx context.Context, actorID, targetID string, limitBytes int64) (*AccountQuota, error) {
	if limitBytes < 0 || limitBytes > MaxPoints {
		return nil, invalid("存储上限必须为范围内的非负整数")
	}
	var result *AccountQuota
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		previous, err := readAccountQuota(tx, targetID)
		if err != nil {
			return err
		}
		// 调低上限只影响新预留，已用数据及已有预留原样保留。
		updated := tx.Model(&StorageAccount{}).Where("account_id = ?", targetID).Updates(map[string]any{"limit_bytes": limitBytes, "updated_at": s.now()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return kernel.NotFound("存储账户不存在")
		}
		difference, err := json.Marshal(map[string]any{"storageLimitBytes": map[string]int64{"from": previous.Storage.LimitBytes, "to": limitBytes}})
		if err != nil {
			return err
		}
		if err := addAudit(tx, actorID, targetID, "storage_limit_updated", string(difference), s.now()); err != nil {
			return err
		}
		result, err = readAccountQuota(tx, targetID)
		return err
	})
	return result, err
}

func (s *Service) AuditLogs(ctx context.Context, actorID, accountID string, offset, limit int) ([]Audit, int64, error) {
	if offset < 0 || offset > 10000000 || limit < 1 || limit > 100 {
		return nil, 0, invalid("分页参数无效")
	}
	rows := []Audit{}
	var total int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireAdmin(tx, actorID); err != nil {
			return err
		}
		query := tx.Model(&Audit{})
		if accountID != "" {
			query = query.Where("target_account_id = ?", accountID)
		}
		if err := query.Count(&total).Error; err != nil {
			return err
		}
		return query.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	})
	return rows, total, err
}
