//go:build platform

package platformstorage

import "gorm.io/gorm"

// MigrateDeletionGuards 阻止旧请求在删除期间复活素材；已终结的删除身份也不可复用。
func MigrateDeletionGuards(tx *gorm.DB) error {
	for _, statement := range []string{
		`CREATE TRIGGER IF NOT EXISTS platform_asset_deletion_insert_guard BEFORE INSERT ON assets
		 WHEN EXISTS (SELECT 1 FROM platform_asset_deletions WHERE asset_id = NEW.id)
		 BEGIN SELECT RAISE(IGNORE); END`,
		`CREATE TRIGGER IF NOT EXISTS platform_asset_deletion_update_guard BEFORE UPDATE ON assets
		 WHEN OLD.status = 'deleting'
		 BEGIN SELECT RAISE(IGNORE); END`,
	} {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}
