package repository

import (
	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
)

// 删除中元数据供持久清理使用，业务查询与关联写入不能把它们重新当作有效对象。
func liveAssets(query *gorm.DB) *gorm.DB {
	return query.Where("status IS NULL OR status <> ?", model.AssetVersionStatusDeleting)
}

func liveResources(query *gorm.DB) *gorm.DB {
	return query.Where("status IS NULL OR status <> ?", model.ResourceStatusDeleted)
}
