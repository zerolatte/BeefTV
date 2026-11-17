//go:build platform

package database

import (
	"errors"

	"gorm.io/gorm"
)

// ValidateCanonicalLocalLedger 只读核对导入来源，未知历史分支不得借当前版本号通过审计。
func ValidateCanonicalLocalLedger(db *gorm.DB) error {
	var recorded []localSchemaMigration
	if err := db.Order("version").Find(&recorded).Error; err != nil {
		return err
	}
	expected := canonicalLocalMigrations()
	if len(recorded) != len(expected) {
		return errors.New("导入来源迁移账本不完整")
	}
	for index, row := range recorded {
		if row.Version != expected[index].version || row.Name != expected[index].name {
			return errors.New("本阶段不支持非规范迁移历史，请保留源数据并核对匹配程序")
		}
	}
	return nil
}
