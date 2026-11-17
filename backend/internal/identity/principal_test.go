package identity

import "testing"

func TestPrincipalRejectsMissingOrMixedIdentity(t *testing.T) {
	local := Principal{Mode: ModeLocal, WorkspaceID: "workspace", Role: RoleAdmin, Status: StatusActive}
	platform := Principal{Mode: ModePlatform, AccountID: "account", WorkspaceID: "workspace", Role: RoleUser, Status: StatusActive, SessionID: "session"}
	for _, principal := range []Principal{local, platform} {
		if err := principal.Validate(); err != nil {
			t.Fatalf("有效身份被拒绝：%v", err)
		}
	}
	tests := []struct {
		name   string
		modify func(*Principal)
	}{
		{"缺少模式", func(p *Principal) { p.Mode = "" }},
		{"未知模式", func(p *Principal) { p.Mode = "server" }},
		{"缺少工作区", func(p *Principal) { p.WorkspaceID = "" }},
		{"工作区有空白", func(p *Principal) { p.WorkspaceID = " workspace" }},
		{"账号有控制符", func(p *Principal) { p.AccountID = "account\x00" }},
		{"缺少账号", func(p *Principal) { p.AccountID = "" }},
		{"缺少会话", func(p *Principal) { p.SessionID = "" }},
		{"未知角色", func(p *Principal) { p.Role = "owner" }},
		{"禁用账号", func(p *Principal) { p.Status = "disabled" }},
		{"平台身份冒充本地", func(p *Principal) { p.Mode = ModeLocal; p.Role = RoleAdmin }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			principal := platform
			tt.modify(&principal)
			if principal.Validate() == nil {
				t.Fatal("无效身份通过校验")
			}
		})
	}
	local.Role = RoleUser
	if local.Validate() == nil {
		t.Fatal("本地身份接受了平台用户角色")
	}
}
