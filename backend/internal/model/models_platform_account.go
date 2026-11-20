package model

import "time"

// PlatformAccount 将登录身份与私有工作区明确关联，凭据不参与 JSON 序列化。
type PlatformAccount struct {
	User               `gorm:"embedded"`
	WorkspaceID        string `json:"workspaceId" gorm:"size:36;not null;uniqueIndex"`
	LoginName          string `json:"-" gorm:"size:80;not null;uniqueIndex"`
	PasswordHash       string `json:"-"`
	MustChangePassword bool   `json:"mustChangePassword"`
	AuthVersion        int64  `json:"-"`
}

type PlatformSession struct {
	TokenHash   string    `json:"-" gorm:"primaryKey;size:64"`
	CSRFHash    string    `json:"-" gorm:"size:64"`
	UserID      string    `json:"-" gorm:"size:36;not null;index"`
	AuthVersion int64     `json:"-"`
	ExpiresAt   time.Time `json:"-" gorm:"index"`
	CreatedAt   time.Time `json:"-"`
}

type PlatformInstance struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	CreatedAt time.Time `json:"createdAt"`
}

// PlatformAudit 只记录操作归因，不收集密码、模型密钥或创作正文。
type PlatformAudit struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	ActorID   string    `gorm:"size:36;index" json:"actorId"`
	SubjectID string    `gorm:"size:80;index" json:"subjectId"`
	Action    string    `gorm:"size:64;index" json:"action"`
	Reason    string    `gorm:"size:500" json:"reason"`
	CreatedAt time.Time `gorm:"index" json:"createdAt"`
}
