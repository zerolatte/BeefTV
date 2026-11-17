//go:build platform

package platformauth

import "gorm.io/gorm"

// RequireAdminInTransaction 供管理领域在同一写事务内复用账号权限规则。
func RequireAdminInTransaction(tx *gorm.DB, accountID string) error {
	return requireAdmin(tx, accountID)
}
