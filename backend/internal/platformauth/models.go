//go:build platform

package platformauth

import "time"

const (
	RoleAdmin      = "admin"
	RoleUser       = "user"
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusDeleted  = "deleted"
)

type Account struct {
	ID                 string    `json:"id" gorm:"primaryKey;size:36"`
	Username           string    `json:"username" gorm:"uniqueIndex;size:32;not null"`
	DisplayName        string    `json:"displayName" gorm:"size:80;not null"`
	Role               string    `json:"role" gorm:"size:16;not null;check:role IN ('admin','user')"`
	Status             string    `json:"status" gorm:"size:16;not null;index;check:status IN ('active','disabled','deleted')"`
	MustChangePassword bool      `json:"mustChangePassword" gorm:"not null"`
	AuthVersion        int64     `json:"-" gorm:"not null"`
	WorkspaceID        string    `json:"workspaceId" gorm:"->;-:migration"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

func (Account) TableName() string { return "platform_accounts" }

type Credential struct {
	AccountID    string `gorm:"primaryKey;size:36"`
	PasswordHash string `gorm:"not null"`
	UpdatedAt    time.Time
}

func (Credential) TableName() string { return "platform_credentials" }

type AccountWorkspace struct {
	AccountID   string `gorm:"primaryKey;size:36"`
	WorkspaceID string `gorm:"uniqueIndex;size:36;not null"`
}

func (AccountWorkspace) TableName() string { return "platform_account_workspaces" }

type Session struct {
	ID          string `gorm:"primaryKey;size:36"`
	AccountID   string `gorm:"index;size:36;not null"`
	TokenHash   string `gorm:"uniqueIndex;size:64;not null"`
	CSRFHash    string `gorm:"size:64;not null"`
	AuthVersion int64  `gorm:"not null"`
	CreatedAt   time.Time
	LastSeenAt  time.Time  `gorm:"index;not null"`
	ExpiresAt   time.Time  `gorm:"index;not null"`
	RevokedAt   *time.Time `gorm:"index"`
}

func (Session) TableName() string { return "platform_sessions" }

type SecurityEvent struct {
	ID        string    `json:"id" gorm:"primaryKey;size:36"`
	AccountID string    `json:"-" gorm:"index;size:36;not null"`
	Action    string    `json:"action" gorm:"size:40;not null"`
	CreatedAt time.Time `json:"createdAt" gorm:"index"`
}

func (SecurityEvent) TableName() string { return "platform_security_events" }

type Audit struct {
	ID              string    `json:"id" gorm:"primaryKey;size:36"`
	ActorAccountID  string    `json:"actorAccountId" gorm:"index;size:36;not null"`
	TargetAccountID string    `json:"targetAccountId" gorm:"index;size:36;not null"`
	Action          string    `json:"action" gorm:"size:40;not null"`
	Difference      string    `json:"difference" gorm:"not null"`
	CreatedAt       time.Time `json:"createdAt" gorm:"index"`
}

func (Audit) TableName() string { return "platform_audits" }

type BootstrapMarker struct {
	ID        string `gorm:"primaryKey;size:32"`
	CreatedAt time.Time
}

func (BootstrapMarker) TableName() string { return "platform_bootstrap_markers" }

// 开户即建立额度根，任务预留及结算只追加流水。
type QuotaAccount struct {
	AccountID      string `gorm:"primaryKey;size:36"`
	GrantedPoints  int64  `gorm:"not null;check:granted_points >= 0"`
	SettledPoints  int64  `gorm:"not null;check:settled_points >= 0"`
	ReservedPoints int64  `gorm:"not null;check:reserved_points >= 0"`
	UpdatedAt      time.Time
}

func (QuotaAccount) TableName() string { return "platform_quota_accounts" }

type UsageLedger struct {
	ID        string    `gorm:"primaryKey;size:36"`
	AccountID string    `gorm:"index;size:36;not null"`
	EventKey  string    `gorm:"uniqueIndex;not null"`
	Kind      string    `gorm:"size:32;not null"`
	Points    int64     `gorm:"not null"`
	Reason    string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"index"`
}

func (UsageLedger) TableName() string { return "platform_usage_ledgers" }

type StorageAccount struct {
	AccountID     string `gorm:"primaryKey;size:36"`
	LimitBytes    int64  `gorm:"not null;check:limit_bytes >= 0"`
	UsedBytes     int64  `gorm:"not null;check:used_bytes >= 0"`
	ReservedBytes int64  `gorm:"not null;check:reserved_bytes >= 0"`
	UpdatedAt     time.Time
}

func (StorageAccount) TableName() string { return "platform_storage_accounts" }

type SchemaMigration struct {
	Version   int `gorm:"primaryKey;autoIncrement:false"`
	AppliedAt time.Time
}

func (SchemaMigration) TableName() string { return "platform_schema_migrations" }

type ImportReceipt struct {
	SourceFingerprint string    `gorm:"primaryKey;size:64"`
	SourceWorkspaceID string    `gorm:"uniqueIndex;size:36;not null"`
	AccountID         string    `gorm:"uniqueIndex;size:36;not null"`
	CreatedAt         time.Time `gorm:"not null"`
}

func (ImportReceipt) TableName() string { return "platform_import_receipts" }
