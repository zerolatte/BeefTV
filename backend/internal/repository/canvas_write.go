package repository

import (
	"time"

	"infinite-canvas/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *Repository) UpsertCanvasProject(project *model.CanvasProject) error {
	if project.UserID == "" {
		return gorm.ErrRecordNotFound
	}
	expected := project.Revision
	if expected < 0 {
		return ErrCanvasRevisionConflict
	}
	if project.ProjectID != "" {
		var count int64
		if err := r.db.Model(&model.Project{}).Where("id = ? AND user_id = ?", project.ProjectID, project.UserID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return gorm.ErrRecordNotFound
		}
	}
	if expected == 0 {
		created := *project
		created.Revision = 1
		result := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&created)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return canvasWriteConflict(r.db, project.UserID, project.ID)
		}
		project.Revision = 1
		return nil
	}
	// 版本条件和递增属于同一语句，缺失或跨工作区的记录统一按不存在处理。
	result := r.db.Model(&model.CanvasProject{}).
		Where("id = ? AND user_id = ? AND revision = ?", project.ID, project.UserID, expected).
		Updates(map[string]any{"project_id": project.ProjectID, "library_folder_id": project.LibraryFolderID, "title": project.Title, "payload_json": project.PayloadJSON, "updated_at": project.UpdatedAt, "revision": expected + 1})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return canvasWriteConflict(r.db, project.UserID, project.ID)
	}
	project.Revision = expected + 1
	return nil
}

func canvasWriteConflict(db *gorm.DB, userID, id string) error {
	var count int64
	if err := db.Model(&model.CanvasProject{}).Where("id = ? AND user_id = ?", id, userID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return gorm.ErrRecordNotFound
	}
	return ErrCanvasRevisionConflict
}

func (r *Repository) DeleteCanvasProject(userID string, id string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// 先确认当前工作区的画布存在，再删除其关联，避免跨域请求删除他人的章节链接。
		locked := tx.Model(&model.CanvasProject{}).Where("user_id = ? AND id = ?", userID, id).UpdateColumn("revision", gorm.Expr("revision"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		var snapshotIDs []string
		if err := tx.Model(&model.CanvasSnapshot{}).Where("user_id = ? AND canvas_id = ?", userID, id).Pluck("id", &snapshotIDs).Error; err != nil {
			return err
		}
		if err := deleteCanvasSnapshots(tx, snapshotIDs); err != nil {
			return err
		}
		if err := tx.Where("canvas_id = ?", id).Delete(&model.CanvasUnitLink{}).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&model.CanvasDrawing{}) {
			if err := New(tx).TombstoneCanvasDrawingsForCanvas(userID, id, time.Now().UTC()); err != nil {
				return err
			}
		}
		// 历史任务保留画布关联，后续重试因画布不存在而停止，不产生新提交。
		return tx.Delete(&model.CanvasProject{}, "id = ? AND user_id = ?", id, userID).Error
	})
}
