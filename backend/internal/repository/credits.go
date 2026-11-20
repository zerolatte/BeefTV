package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

func (r *Repository) CreditAccount(userID string) (*model.CreditAccount, error) {
	var row model.CreditAccount
	err := r.db.First(&row, "user_id = ?", userID).Error
	return &row, err
}

func (r *Repository) CreditPrice(modelID, variantID string) (*model.ModelCreditPrice, error) {
	var row model.ModelCreditPrice
	err := r.db.First(&row, "channel_model_id = ? AND variant_id = ?", modelID, variantID).Error
	return &row, err
}

func (r *Repository) SaveCreditPrice(row *model.ModelCreditPrice, audit model.PlatformAudit) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "channel_model_id"}, {Name: "variant_id"}}, DoUpdates: clause.Assignments(map[string]any{"unit_credits": row.UnitCredits, "published": row.Published, "version": gorm.Expr("version + 1"), "updated_at": time.Now().UTC()})}).Create(row).Error; err != nil {
			return err
		}
		return tx.Create(&audit).Error
	})
}

func (r *Repository) CreditPrices() ([]model.ModelCreditPrice, error) {
	rows := []model.ModelCreditPrice{}
	err := r.db.Order("channel_model_id, variant_id").Find(&rows).Error
	return rows, err
}

func (r *Repository) CreateCreditQuote(row *model.CreditQuote) error { return r.db.Create(row).Error }

// AdmitCreditTask 在同一事务中核对报价、扣减可用余额、建立冻结及创建任务。
func (r *Repository) AdmitCreditTask(task *model.Task, limit int, quoteID, requestHash string, validate ...func(*Repository) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var account model.PlatformAccount
		if err := tx.First(&account, "id = ? AND status = ? AND must_change_password = ?", task.UserID, model.UserStatusActive, false).Error; err != nil {
			return kernel.Unauthorized("账号不可用，请重新登录")
		}
		if task.ClientOperationID != nil {
			var existing model.Task
			err := tx.First(&existing, "user_id = ? AND client_operation_id = ?", task.UserID, *task.ClientOperationID).Error
			if err == nil {
				return &ClientOperationReplay{Task: existing}
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var quote model.CreditQuote
		if err := tx.First(&quote, "id = ? AND user_id = ?", quoteID, task.UserID).Error; err != nil {
			return kernel.BadAuthRequest("报价不存在，请重新报价")
		}
		if quote.RequestHash != requestHash {
			return creditError(409, "quote_mismatch", "请求已变化，请重新报价")
		}
		if !time.Now().Before(quote.ExpiresAt) {
			return creditError(409, "quote_expired", "报价已过期，请重新报价")
		}
		var price model.ModelCreditPrice
		if err := tx.First(&price, "id = ? AND version = ? AND published = ?", quote.PriceID, quote.PriceVersion, true).Error; err != nil {
			return creditError(409, "price_changed", "模型价格已变化，请重新报价")
		}
		var catalogModel model.ChannelModel
		if err := tx.First(&catalogModel, "id = ? AND enabled = ?", quote.ModelID, true).Error; err != nil {
			return kernel.Forbidden("模型已停用")
		}
		var channel model.ModelChannel
		if err := tx.First(&channel, "id = ? AND enabled = ? AND scope = ?", catalogModel.ChannelID, true, model.ChannelScopeSystem).Error; err != nil {
			return kernel.Forbidden("模型渠道已停用")
		}
		if !catalogModel.UpdatedAt.Equal(quote.ModelUpdatedAt) || !channel.UpdatedAt.Equal(quote.ChannelUpdatedAt) {
			return creditError(409, "model_changed", "模型配置已变化，请重新报价")
		}
		var used int64
		if err := tx.Model(&model.CreditReservation{}).Where("quote_id = ?", quoteID).Count(&used).Error; err != nil {
			return err
		}
		if used > 0 {
			return creditError(409, "quote_consumed", "报价已经使用")
		}
		updated := tx.Model(&model.CreditAccount{}).Where("user_id = ? AND available >= ?", task.UserID, quote.TotalCredits).
			Updates(map[string]any{"available": gorm.Expr("available - ?", quote.TotalCredits), "frozen": gorm.Expr("frozen + ?", quote.TotalCredits), "updated_at": time.Now().UTC()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return creditError(403, "insufficient_credits", "可用积分不足，请联系管理员")
		}
		task.ChannelModelID = quote.ModelID
		for _, check := range validate {
			if err := check(r.WithTx(tx)); err != nil {
				return err
			}
		}
		if err := r.WithTx(tx).CreateTaskWithActiveLimit(task, limit); err != nil {
			return err
		}
		reservation := model.CreditReservation{ID: kernel.NewID(), UserID: task.UserID, TaskID: task.ID, QuoteID: quote.ID, ModelID: quote.ModelID, PriceVersion: quote.PriceVersion, UnitCredits: quote.UnitCredits, Units: quote.Units, Reserved: quote.TotalCredits, State: "reserved"}
		if err := tx.Create(&reservation).Error; err != nil {
			return err
		}
		return tx.Create(&model.CreditLedger{ID: kernel.NewID(), UserID: task.UserID, OperationID: "reserve:" + task.ID, ReservationID: reservation.ID, Kind: "reserve", Amount: -quote.TotalCredits, Reason: "生成任务预占积分"}).Error
	})
}

func creditError(status int, reason, message string) error {
	return &kernel.AppError{Status: status, Code: status, Reason: kernel.ErrorReason(reason), Message: message}
}

func (r *Repository) AdjustCredits(actor, userID, operationID, reason string, amount int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var old model.CreditLedger
		err := tx.First(&old, "operation_id = ?", "adjust:"+actor+":"+operationID).Error
		if err == nil {
			if old.UserID == userID && old.Amount == amount && old.Reason == reason {
				return nil
			}
			return creditError(409, "operation_conflict", "操作标识已用于其他积分调整")
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		q := tx.Model(&model.CreditAccount{}).Where("user_id = ?", userID)
		if amount < 0 {
			q = q.Where("available >= ?", -amount)
		} else {
			q = q.Where("available + frozen <= ?", int64(1_000_000_000_000)-amount)
		}
		result := q.Updates(map[string]any{"available": gorm.Expr("available + ?", amount), "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return creditError(409, "invalid_credit_adjustment", "积分账户不存在、可用积分不足或超出账户上限")
		}
		if err := tx.Create(&model.CreditLedger{ID: kernel.NewID(), UserID: userID, ActorID: actor, OperationID: "adjust:" + actor + ":" + operationID, Kind: "adjust", Amount: amount, Reason: reason}).Error; err != nil {
			return err
		}
		return tx.Create(&model.PlatformAudit{ID: kernel.NewID(), ActorID: actor, SubjectID: userID, Action: "credits.adjust", Reason: reason}).Error
	})
}

func (r *Repository) PendingCreditReservations(limit int) ([]model.CreditReservation, error) {
	rows := []model.CreditReservation{}
	err := r.db.Where("state IN ? AND task_id IN (SELECT id FROM tasks WHERE status NOT IN ?)", []string{"reserved", "review"}, []model.TaskStatus{model.TaskStatusRunning, model.TaskStatusQueued}).Order("updated_at, id").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *Repository) CreditReservation(id string) (*model.CreditReservation, error) {
	var row model.CreditReservation
	err := r.db.First(&row, "id = ?", id).Error
	return &row, err
}

func (r *Repository) MarkCreditReview(id string) error {
	return r.db.Model(&model.CreditReservation{}).Where("id = ? AND state IN ?", id, []string{"reserved", "review"}).Updates(map[string]any{"state": "review", "updated_at": time.Now().UTC()}).Error
}

func (r *Repository) SettleCredits(id string, units int64, actor, reason string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var row model.CreditReservation
		if err := tx.First(&row, "id = ?", id).Error; err != nil {
			return err
		}
		if row.State == "settled" {
			if row.Charged == units*row.UnitCredits {
				return nil
			}
			return creditError(409, "settlement_conflict", "该记录已经按其他结论结算")
		}
		var current model.Task
		if err := tx.First(&current, "id = ? AND user_id = ?", row.TaskID, row.UserID).Error; err != nil {
			return err
		}
		if current.Status == model.TaskStatusRunning || current.Status == model.TaskStatusQueued || (current.LeaseOwner != "" && current.LeaseExpiresAt != nil && current.LeaseExpiresAt.After(time.Now())) {
			return creditError(409, "task_active", "任务执行或结果取回尚未结束")
		}
		if units < 0 || units > row.Units {
			return kernel.BadAuthRequest("结算数量超出预占数量")
		}
		charged, refund := units*row.UnitCredits, row.Reserved-units*row.UnitCredits
		updated := tx.Model(&model.CreditAccount{}).Where("user_id = ? AND frozen >= ?", row.UserID, row.Reserved).
			Updates(map[string]any{"frozen": gorm.Expr("frozen - ?", row.Reserved), "available": gorm.Expr("available + ?", refund), "updated_at": time.Now().UTC()})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return errors.New("积分冻结账目不一致")
		}
		if err := tx.Model(&row).Updates(map[string]any{"state": "settled", "charged": charged, "refunded": refund}).Error; err != nil {
			return err
		}
		for _, entry := range []model.CreditLedger{
			{ID: kernel.NewID(), UserID: row.UserID, ActorID: actor, OperationID: "charge:" + row.ID, ReservationID: row.ID, Kind: "charge", Amount: -charged, Reason: reason},
			{ID: kernel.NewID(), UserID: row.UserID, ActorID: actor, OperationID: "refund:" + row.ID, ReservationID: row.ID, Kind: "refund", Amount: refund, Reason: reason},
		} {
			if err := tx.Create(&entry).Error; err != nil {
				return err
			}
		}
		if actor != "" {
			return tx.Create(&model.PlatformAudit{ID: kernel.NewID(), ActorID: actor, SubjectID: row.ID, Action: "credits.resolve", Reason: reason}).Error
		}
		return nil
	})
}

func (r *Repository) CreditLedgerPage(userID string, offset, limit int) ([]model.CreditLedger, int64, error) {
	q := r.db.Model(&model.CreditLedger{}).Where("user_id = ?", userID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.CreditLedger{}
	err := q.Order("created_at DESC, id").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func (r *Repository) CreditUsagePage(userID, state string, offset, limit int) ([]model.CreditReservation, int64, error) {
	q := r.db.Model(&model.CreditReservation{})
	if userID != "" {
		q = q.Where("user_id = ?", userID)
	}
	if state != "" {
		q = q.Where("state = ?", state)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := []model.CreditReservation{}
	err := q.Order("created_at DESC, id").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}
