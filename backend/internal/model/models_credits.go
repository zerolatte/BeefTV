package model

import "time"

type CreditAccount struct {
	UserID    string    `json:"userId" gorm:"primaryKey;size:36"`
	Available int64     `json:"available" gorm:"not null;check:available >= 0"`
	Frozen    int64     `json:"frozen" gorm:"not null;check:frozen >= 0"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ModelCreditPrice 是当前发布价格，修改时增加版本，已提交任务保留自己的价格快照。
type ModelCreditPrice struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	ChannelModelID string    `json:"modelId" gorm:"size:36;not null;uniqueIndex:idx_credit_model_variant,priority:1"`
	VariantID      string    `json:"variantId" gorm:"size:36;not null;uniqueIndex:idx_credit_model_variant,priority:2"`
	UnitCredits    int64     `json:"unitCredits" gorm:"not null;check:unit_credits >= 0"`
	Version        int64     `json:"version" gorm:"not null"`
	Published      bool      `json:"published"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type CreditQuote struct {
	ModelUpdatedAt   time.Time `json:"-"`
	ChannelUpdatedAt time.Time `json:"-"`
	ID               string    `json:"id" gorm:"primaryKey;size:36"`
	UserID           string    `json:"-" gorm:"size:36;index"`
	RequestHash      string    `json:"-" gorm:"size:64"`
	PriceID          string    `json:"-" gorm:"size:36"`
	PriceVersion     int64     `json:"priceVersion"`
	ModelID          string    `json:"modelId" gorm:"size:36"`
	UnitCredits      int64     `json:"unitCredits"`
	Units            int64     `json:"units"`
	TotalCredits     int64     `json:"totalCredits"`
	ExpiresAt        time.Time `json:"expiresAt" gorm:"index"`
	CreatedAt        time.Time `json:"createdAt"`
}

type CreditReservation struct {
	ID           string    `json:"id" gorm:"primaryKey;size:36"`
	UserID       string    `json:"userId" gorm:"size:36;index"`
	TaskID       string    `json:"taskId" gorm:"size:36;uniqueIndex;not null"`
	QuoteID      string    `json:"quoteId" gorm:"size:36;uniqueIndex;not null"`
	ModelID      string    `json:"modelId" gorm:"size:36;index"`
	PriceVersion int64     `json:"priceVersion"`
	UnitCredits  int64     `json:"unitCredits"`
	Units        int64     `json:"units"`
	Reserved     int64     `json:"reserved"`
	Charged      int64     `json:"charged"`
	Refunded     int64     `json:"refunded"`
	State        string    `json:"state" gorm:"size:24;index"`
	CreatedAt    time.Time `json:"createdAt" gorm:"index"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type CreditLedger struct {
	ID            string    `json:"id" gorm:"primaryKey;size:36"`
	UserID        string    `json:"userId" gorm:"size:36;index:idx_credit_ledger_user_time,priority:1"`
	ActorID       string    `json:"actorId,omitempty" gorm:"size:36"`
	OperationID   string    `json:"operationId" gorm:"size:128;not null;uniqueIndex"`
	ReservationID string    `json:"reservationId,omitempty" gorm:"size:36;index"`
	Kind          string    `json:"kind" gorm:"size:24"`
	Amount        int64     `json:"amount"`
	Reason        string    `json:"reason" gorm:"size:500"`
	CreatedAt     time.Time `json:"createdAt" gorm:"index:idx_credit_ledger_user_time,priority:2"`
}
