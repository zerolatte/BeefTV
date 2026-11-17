package handler

import (
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"

	"github.com/gin-gonic/gin"
)

// workspaceOwners is the only capability handlers need to resolve the caller.
type workspaceOwners interface {
	WorkspaceOwner(string) (*model.User, error)
	LocalWorkspaceOwner() (*model.User, error)
}

// currentUser resolves the stable workspace owner. Local routes never inspect
// cookies and never create a session record.
func currentUser(c *gin.Context, svc workspaceOwners) (*model.User, error) {
	if _, exists := c.Get(principalContextKey); exists {
		principal, err := CurrentPrincipal(c)
		if err != nil {
			return nil, err
		}
		if principal.Mode == identity.ModePlatform {
			// 旧业务端口的 User.ID 始终表示工作区，不以账号 ID 查询内容。
			return &model.User{ID: principal.WorkspaceID, Role: model.UserRole(principal.Role), Status: model.UserStatus(principal.Status)}, nil
		}
	}
	if _, exists := c.Get(workspaceContextKey); exists {
		scope, err := CurrentWorkspace(c)
		if err != nil {
			return nil, err
		}
		return svc.WorkspaceOwner(scope.ID)
	}
	return svc.LocalWorkspaceOwner()
}
