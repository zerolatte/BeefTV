//go:build platform

package model

import "time"

// 平台表只记录授权、版本与密文；既有模型及路由结构继续作为能力合同。
type PlatformModelGrant struct {
	AccountID      string    `json:"accountId" gorm:"primaryKey;size:36"`
	LogicalModelID string    `json:"logicalModelId" gorm:"primaryKey;size:36;index"`
	CreatedBy      string    `json:"createdBy" gorm:"size:36;not null"`
	CreatedAt      time.Time `json:"createdAt"`
}

func (PlatformModelGrant) TableName() string { return "platform_model_grants" }

type PlatformChannelState struct {
	ChannelID           string `gorm:"primaryKey;size:36"`
	CurrentCredentialID string `gorm:"size:36;not null"`
	CredentialSequence  int64  `gorm:"not null;check:credential_sequence >= 0"`
}

func (PlatformChannelState) TableName() string { return "platform_channel_states" }

type PlatformCredentialVersion struct {
	ID              string    `json:"id" gorm:"primaryKey;size:36"`
	ChannelID       string    `json:"channelId" gorm:"size:36;not null;uniqueIndex:idx_platform_channel_credential_version,priority:1"`
	Version         int64     `json:"version" gorm:"not null;uniqueIndex:idx_platform_channel_credential_version,priority:2;check:version > 0"`
	Ciphertext      string    `json:"-" gorm:"type:text;not null"`
	UpstreamHost    string    `json:"upstreamHost" gorm:"not null"`
	HasAPIKey       bool      `json:"hasApiKey" gorm:"not null"`
	HasSecretKey    bool      `json:"hasSecretKey" gorm:"not null"`
	HeaderNamesJSON string    `json:"-" gorm:"not null"`
	CreatedBy       string    `json:"createdBy" gorm:"size:36;not null"`
	CreatedAt       time.Time `json:"createdAt"`
}

func (PlatformCredentialVersion) TableName() string { return "platform_credential_versions" }

type PlatformModelPriceHead struct {
	LogicalModelID  string `gorm:"primaryKey;size:36"`
	Operation       string `gorm:"primaryKey;size:64"`
	PriceVersionID  string `gorm:"size:36;not null"`
	VersionSequence int64  `gorm:"not null;check:version_sequence >= 0"`
}

func (PlatformModelPriceHead) TableName() string { return "platform_model_price_heads" }

type PlatformModelPrice struct {
	ID             string    `json:"id" gorm:"primaryKey;size:36"`
	LogicalModelID string    `json:"logicalModelId" gorm:"size:36;not null;uniqueIndex:idx_platform_model_operation_price_version,priority:1"`
	Operation      string    `json:"operation" gorm:"size:64;not null;uniqueIndex:idx_platform_model_operation_price_version,priority:2"`
	Version        int64     `json:"version" gorm:"not null;uniqueIndex:idx_platform_model_operation_price_version,priority:3;check:version > 0"`
	Points         int64     `json:"points" gorm:"not null;check:points > 0 AND points <= 9007199254740991"`
	CreatedBy      string    `json:"createdBy" gorm:"size:36;not null"`
	CreatedAt      time.Time `json:"createdAt"`
}

func (PlatformModelPrice) TableName() string { return "platform_model_prices" }
