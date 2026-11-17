//go:build platform

package platformstorage

import "time"

const (
	PurposeUpload    = "upload"
	PurposeGenerated = "generated"
	PurposeManual    = "manual"
	StateHeld        = "held"
	StateCommitted   = "committed"
	StateReleased    = "released"
)

// Reservation 保留业务身份和最终状态；重启不能靠进程内计数恢复预留。
type Reservation struct {
	ID             string `gorm:"primaryKey;size:36"`
	AccountID      string `gorm:"index;not null"`
	WorkspaceID    string `gorm:"uniqueIndex:platform_storage_business;not null"`
	BusinessKey    string `gorm:"uniqueIndex:platform_storage_business;not null"`
	Purpose        string `gorm:"index;not null;default:manual"`
	RequestedBytes int64  `gorm:"not null;check:requested_bytes >= 0"`
	ReservedBytes  int64  `gorm:"not null;check:reserved_bytes >= 0"`
	State          string `gorm:"index;not null;check:state IN ('held','committed','released')"`
	ObjectIdentity string `gorm:"not null"`
	CommittedBytes int64  `gorm:"not null;check:committed_bytes >= 0"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (Reservation) TableName() string { return "platform_storage_reservations" }

// StoredObject 对同工作区同物理对象只记一次；资源行数量不决定占用量。
type StoredObject struct {
	ID             string `gorm:"primaryKey;size:36"`
	AccountID      string `gorm:"index;not null"`
	WorkspaceID    string `gorm:"uniqueIndex:platform_storage_object;not null"`
	ObjectIdentity string `gorm:"uniqueIndex:platform_storage_object;not null"`
	Size           int64  `gorm:"not null;check:size >= 0"`
	CreatedAt      time.Time
}

func (StoredObject) TableName() string { return "platform_storage_objects" }

type Usage struct {
	AccountID     string `json:"-"`
	LimitBytes    int64  `json:"totalBytes"`
	UsedBytes     int64  `json:"usedBytes"`
	ReservedBytes int64  `json:"reservedBytes"`
}

func (Usage) TableName() string { return "platform_storage_accounts" }
