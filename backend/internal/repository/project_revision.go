package repository

import (
	"infinite-canvas/backend/internal/model"
	"strings"
	"time"

	"gorm.io/gorm"
)

func requireProjectRevision(current, expected int64) error {
	if expected < 1 {
		return ErrExpectedRevisionRequired
	}
	if current != expected {
		return ErrProjectRevisionConflict
	}
	return nil
}

func (r *Repository) MoveProjectAndBump(userID, projectID, folderID string, expectedRevision int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		current, err := ownedProjectTx(tx, userID, projectID)
		if err != nil {
			return err
		}
		if err := requireProjectRevision(current.Revision, expectedRevision); err != nil {
			return err
		}
		folderID = strings.TrimSpace(folderID)
		if folderID != "" {
			var folder model.ProjectFolder
			if err := tx.First(&folder, "id = ? AND user_id = ?", folderID, userID).Error; err != nil {
				return err
			}
		}
		now := time.Now()
		result := tx.Model(&model.Project{}).Where("id = ? AND user_id = ? AND revision = ?", projectID, userID, current.Revision).Updates(map[string]any{
			"folder_id":  folderID,
			"revision":   current.Revision + 1,
			"updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrProjectRevisionConflict
		}
		return nil
	})
}

func (r *Repository) UpdateProjectCAS(userID string, expectedRevision int64, project *model.Project) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		current, err := ownedProjectTx(tx, userID, project.ID)
		if err != nil {
			return err
		}
		if err := requireProjectRevision(current.Revision, expectedRevision); err != nil {
			return err
		}
		result := tx.Model(&model.Project{}).Where("id = ? AND user_id = ? AND revision = ?", project.ID, userID, expectedRevision).Updates(map[string]any{
			"name": project.Name, "type": project.Type, "aspect_ratio": project.AspectRatio, "source_type": project.SourceType,
			"description": project.Description, "cover_resource_id": project.CoverResourceID,
			"style_preset_id": project.StylePresetID, "style_profile_json": project.StyleProfileJSON,
			"default_image_model": project.DefaultImageModel, "default_video_model": project.DefaultVideoModel,
			"status": project.Status, "revision": expectedRevision + 1, "updated_at": project.UpdatedAt,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrProjectRevisionConflict
		}
		return nil
	})
}

func (r *Repository) ReorderProjectUnitsActive(userID, projectID string, unitIDs []string, expectedRevision int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		current, err := requireActiveProjectTx(tx, userID, projectID)
		if err != nil {
			return err
		}
		if err := requireProjectRevision(current.Revision, expectedRevision); err != nil {
			return err
		}
		now := time.Now()
		for position, unitID := range unitIDs {
			result := tx.Model(&model.ProjectUnit{}).Where("id = ? AND project_id = ?", unitID, projectID).Updates(map[string]any{"position": position, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
		}
		return bumpProjectRevisionCASTx(tx, projectID, expectedRevision, now)
	})
}

func (r *Repository) UpdateProjectUnitActive(userID string, expectedRevision int64, unit *model.ProjectUnit, invalidateWorkflow bool) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		current, err := requireActiveProjectTx(tx, userID, unit.ProjectID)
		if err != nil {
			return err
		}
		if err := requireProjectRevision(current.Revision, expectedRevision); err != nil {
			return err
		}
		result := tx.Model(&model.ProjectUnit{}).Where("id = ? AND project_id = ?", unit.ID, unit.ProjectID).Updates(map[string]any{
			"parent_id": unit.ParentID, "title": unit.Title, "source_text": unit.SourceText, "word_count": unit.WordCount, "status": unit.Status, "position": unit.Position, "updated_at": unit.UpdatedAt,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if invalidateWorkflow {
			if err := tx.Model(&model.ShotArtifact{}).Where("project_id = ? AND unit_id = ? AND status NOT IN ?", unit.ProjectID, unit.ID, []string{"failed", "stale"}).Updates(map[string]any{"status": "stale", "selected": false, "updated_at": unit.UpdatedAt}).Error; err != nil {
				return err
			}
			if err := invalidateUnitWorkflowTx(tx, unit.ProjectID, unit.ID, "story", unit.UpdatedAt); err != nil {
				return err
			}
		}
		return bumpProjectRevisionCASTx(tx, unit.ProjectID, expectedRevision, unit.UpdatedAt)
	})
}

// ProjectUnitSnapshot 在同一个读取事务内确定归属、章节正文及项目版本。
func (r *Repository) ProjectUnitSnapshot(userID, projectID, unitID string) (model.ProjectUnit, int64, error) {
	var unit model.ProjectUnit
	var revision int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		project, err := ownedProjectTx(tx, userID, projectID)
		if err != nil {
			return err
		}
		revision = project.Revision
		return tx.First(&unit, "id = ? AND project_id = ?", unitID, projectID).Error
	})
	return unit, revision, err
}

// ProjectUnitIndexSnapshot 让覆盖式排序使用同一次读取的顺序与版本。
func (r *Repository) ProjectUnitIndexSnapshot(userID, projectID string) ([]model.ProjectUnit, map[string]int64, int64, error) {
	var units []model.ProjectUnit
	var counts map[string]int64
	var revision int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		project, err := ownedProjectTx(tx, userID, projectID)
		if err != nil {
			return err
		}
		revision = project.Revision
		reader := New(tx)
		units, err = reader.ProjectUnitSummaries(projectID)
		if err != nil {
			return err
		}
		counts, err = reader.ProjectUnitCanvasCounts(projectID)
		return err
	})
	return units, counts, revision, err
}
