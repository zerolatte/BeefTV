package identity

import (
	"errors"
	"strings"
	"unicode"
)

type Mode string

const (
	ModeLocal    Mode = "local"
	ModePlatform Mode = "platform"
	RoleAdmin         = "admin"
	RoleUser          = "user"
	StatusActive      = "active"
)

// Principal 明确区分登录账号与业务工作区，不能把缺失身份补成本地默认工作区。
type Principal struct {
	Mode        Mode   `json:"mode"`
	AccountID   string `json:"accountId,omitempty"`
	WorkspaceID string `json:"workspaceId"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	SessionID   string `json:"sessionId,omitempty"`
}

func (p Principal) Validate() error {
	if !validIdentityID(p.WorkspaceID) || p.Status != StatusActive {
		return errors.New("工作区身份无效或账号不可用")
	}
	switch p.Mode {
	case ModeLocal:
		if p.AccountID != "" || p.SessionID != "" || p.Role != RoleAdmin {
			return errors.New("本地身份不能携带平台账号或会话")
		}
	case ModePlatform:
		if !validIdentityID(p.AccountID) || !validIdentityID(p.SessionID) {
			return errors.New("平台身份缺少有效账号或会话")
		}
		if p.Role != RoleAdmin && p.Role != RoleUser {
			return errors.New("平台身份角色无效")
		}
	default:
		return errors.New("运行模式无效")
	}
	return nil
}

func validIdentityID(value string) bool {
	return value != "" && len(value) <= 128 && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) == -1
}
