//go:build platform

package platformquota

import (
	"context"
	"errors"
	"math/big"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/platformauth"
)

type Reconciliation struct {
	AccountID     string `json:"accountId"`
	Consistent    bool   `json:"consistent"`
	LedgerEntries int64  `json:"ledgerEntries"`
	Granted       string `json:"granted"`
	Settled       string `json:"settled"`
	Reserved      string `json:"reserved"`
}

// Reconcile 重新汇总追加流水和未终结意图，不用修正计数掩盖差异。
func (s *Service) Reconcile(ctx context.Context, actorID, accountID string) (*Reconciliation, error) {
	var result *Reconciliation
	err := s.transaction(ctx, func(tx *gorm.DB) error {
		if err := requireAdmin(tx, actorID); err != nil {
			return err
		}
		var account platformauth.QuotaAccount
		if err := tx.Where("account_id = ?", accountID).Take(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return kernel.NotFound("点数账户不存在")
			}
			return err
		}
		rows, err := tx.Model(&platformauth.UsageLedger{}).Select("kind, points").Where("account_id = ?", accountID).Rows()
		if err != nil {
			return err
		}
		defer rows.Close()
		granted, settled, reserved := new(big.Int), new(big.Int), new(big.Int)
		result = &Reconciliation{AccountID: accountID}
		valid := true
		for rows.Next() {
			var kind string
			var points int64
			if err := rows.Scan(&kind, &points); err != nil {
				return err
			}
			result.LedgerEntries++
			amount := big.NewInt(points)
			switch kind {
			case "grant", "adjustment":
				granted.Add(granted, amount)
			case "reserve":
				reserved.Add(reserved, amount)
			case "release":
				reserved.Sub(reserved, amount)
			case "settle":
				settled.Add(settled, amount)
				reserved.Sub(reserved, amount)
			case "compensate":
				settled.Sub(settled, amount)
			default:
				valid = false
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		var held int64
		if err := tx.Table("platform_execution_intents").Select("COALESCE(SUM(points), 0)").Where("account_id = ? AND state IN ?", accountID, []string{StateReserved, StateSubmitting, StateAccepted, StateUnknown}).Scan(&held).Error; err != nil {
			return err
		}
		result.Granted, result.Settled, result.Reserved = granted.String(), settled.String(), reserved.String()
		result.Consistent = valid && granted.Sign() >= 0 && settled.Sign() >= 0 && reserved.Sign() >= 0 && granted.Cmp(big.NewInt(account.GrantedPoints)) == 0 && settled.Cmp(big.NewInt(account.SettledPoints)) == 0 && reserved.Cmp(big.NewInt(account.ReservedPoints)) == 0 && reserved.Cmp(big.NewInt(held)) == 0
		return nil
	})
	return result, err
}
