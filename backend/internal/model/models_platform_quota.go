//go:build platform

package model

import "time"

// PlatformExecutionIntent 冻结一次上游执行的归属与版本，不保存提示词或凭据。
type PlatformExecutionIntent struct {
	ID                  string     `json:"id" gorm:"primaryKey;size:36"`
	AccountID           string     `json:"accountId" gorm:"not null;index;size:36"`
	WorkspaceID         string     `json:"-" gorm:"not null;index;size:36"`
	TaskID              string     `json:"taskId" gorm:"not null;index;size:36"`
	LogicalModelID      string     `json:"modelId" gorm:"not null;size:36"`
	RevisionID          string     `json:"-" gorm:"not null;size:36"`
	RouteID             string     `json:"-" gorm:"not null;size:36"`
	ChannelID           string     `json:"-" gorm:"not null;size:36"`
	ChannelModelID      string     `json:"-" gorm:"not null;size:36"`
	CredentialVersionID string     `json:"-" gorm:"not null;size:36"`
	PriceVersionID      string     `json:"priceVersionId" gorm:"not null;size:36"`
	Operation           string     `json:"operation" gorm:"not null;size:64"`
	Points              int64      `json:"points" gorm:"not null;check:points > 0 AND points <= 9007199254740991"`
	State               string     `json:"state" gorm:"not null;index;size:32"`
	ReviewRequired      bool       `json:"reviewRequired" gorm:"not null;index"`
	SubmittedAt         *time.Time `json:"submittedAt"`
	FinalizedAt         *time.Time `json:"finalizedAt"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

func (PlatformExecutionIntent) TableName() string { return "platform_execution_intents" }

type PlatformRequestReceipt struct {
	ID            string `gorm:"primaryKey;size:36"`
	WorkspaceID   string `gorm:"not null;uniqueIndex:idx_platform_request_key,priority:1;size:36"`
	Kind          string `gorm:"not null;uniqueIndex:idx_platform_request_key,priority:2;size:64"`
	RequestKey    string `gorm:"not null;uniqueIndex:idx_platform_request_key,priority:3;size:128"`
	RequestDigest string `gorm:"not null;size:64"`
	IntentID      string `gorm:"not null;uniqueIndex;size:36"`
	CreatedAt     time.Time
}

func (PlatformRequestReceipt) TableName() string { return "platform_request_receipts" }

// 调整收据使管理端网络重试不会重复发放，历史账本始终追加。
type PlatformPointAdjustment struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	ActorAccountID string    `json:"actorAccountId" gorm:"not null;uniqueIndex:idx_platform_adjustment_key,priority:1;size:36"`
	RequestKey     string    `json:"-" gorm:"not null;uniqueIndex:idx_platform_adjustment_key,priority:2;size:128"`
	AccountID      string    `json:"accountId" gorm:"not null;index;size:36"`
	Points         int64     `json:"points" gorm:"not null"`
	Reason         string    `json:"reason" gorm:"not null;size:500"`
	CreatedAt      time.Time `json:"createdAt"`
}

func (PlatformPointAdjustment) TableName() string { return "platform_point_adjustments" }

type PlatformExecutionReview struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	IntentID       string    `json:"intentId" gorm:"not null;uniqueIndex;size:36"`
	ActorAccountID string    `json:"actorAccountId" gorm:"not null;uniqueIndex:idx_platform_review_key,priority:1;size:36"`
	RequestKey     string    `json:"-" gorm:"not null;uniqueIndex:idx_platform_review_key,priority:2;size:128"`
	PreviousState  string    `json:"previousState" gorm:"not null;size:32"`
	Fact           string    `json:"fact" gorm:"not null;size:32"`
	Reason         string    `json:"reason" gorm:"not null;size:500"`
	CreatedAt      time.Time `json:"createdAt"`
}

func (PlatformExecutionReview) TableName() string { return "platform_execution_reviews" }
