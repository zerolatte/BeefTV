//go:build platform

package platformimport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	localtask "infinite-canvas/backend/internal/task"
	"infinite-canvas/backend/internal/taskdelivery"
)

var unsupportedTables = map[string]bool{
	"skills": true, "skill_versions": true, "skill_files": true, "user_skill_states": true,
	"plugin_platform_states": true, "user_plugin_states": true,
	"assistant_turns": true, "assistant_continuations": true, "assistant_skill_contexts": true, "assistant_skill_context_mutations": true,
	"creation_runs": true, "creation_submissions": true, "creation_conversations": true,
	"cloud_agent_executions": true, "cloud_agent_canvas_mutations": true, "agent_profiles": true, "agent_lessons": true, "agent_memory_settings": true, "agent_op_records": true,
}

func inspectSource(ctx context.Context, db *gorm.DB, source string) (string, error) {
	var marker modeMarker
	if err := readJSON(filepath.Join(source, database.RuntimeModeFileName), &marker); err != nil {
		return "", errors.New("仅支持已有 local 运行标记的工作区，请先使用匹配的本地程序完成迁移")
	}
	if marker.Mode != identity.ModeLocal || marker.Version != 1 {
		return "", errors.New("源目录不是受支持的本地工作区")
	}
	if _, err := uuid.Parse(marker.DirectoryID); err != nil {
		return "", errors.New("源运行标记损坏")
	}
	if err := validateMarker(ctx, db, marker); err != nil {
		return "", err
	}
	if err := databaseIntegrity(ctx, db); err != nil {
		return "", err
	}
	var versions []int64
	if err := db.WithContext(ctx).Table("local_schema_migrations").Order("version").Pluck("version", &versions).Error; err != nil {
		return "", err
	}
	if len(versions) != 28 || versions[len(versions)-1] != 29 {
		return "", errors.New("本阶段仅支持迁移账本完整的本地 schema v29")
	}
	for i, value := range versions {
		expected := int64(i + 1)
		if expected >= 7 {
			expected++
		}
		if value != expected {
			return "", errors.New("本地迁移账本包含未支持版本")
		}
	}
	if err := database.ValidateCanonicalLocalLedger(db.WithContext(ctx)); err != nil {
		return "", err
	}
	key, err := os.ReadFile(filepath.Join(source, ".settings-key"))
	if err != nil || len(key) != 32 {
		return "", errors.New("源工作区主密钥缺失或损坏")
	}
	clear(key)
	if err := validateLocalConfigFiles(source); err != nil {
		return "", err
	}
	var workspaces []model.Workspace
	if err := db.WithContext(ctx).Find(&workspaces).Error; err != nil {
		return "", err
	}
	if len(workspaces) != 1 || workspaces[0].ID == "" || strings.TrimSpace(workspaces[0].ID) != workspaces[0].ID {
		return "", errors.New("导入要求唯一且有效的本地工作区")
	}
	workspace := workspaces[0].ID
	if err := auditTables(ctx, db, workspace); err != nil {
		return "", err
	}
	if err := auditRelations(ctx, db); err != nil {
		return "", err
	}
	if err := auditInactive(ctx, db); err != nil {
		return "", err
	}
	if err := validateResources(ctx, db, source); err != nil {
		return "", err
	}
	return workspace, nil
}

func validateMarker(ctx context.Context, db *gorm.DB, marker modeMarker) error {
	var rows []modeMarker
	if err := db.WithContext(ctx).Table("runtime_mode_markers").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].ID != 1 || rows[0].Version != marker.Version || rows[0].Mode != marker.Mode || rows[0].DirectoryID != marker.DirectoryID {
		return errors.New("源数据库和目录运行标记不一致")
	}
	return nil
}

func databaseIntegrity(ctx context.Context, db *gorm.DB) error {
	var rows []string
	if err := db.WithContext(ctx).Raw("PRAGMA integrity_check").Scan(&rows).Error; err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != "ok" {
		return errors.New("数据库完整性校验未通过")
	}
	var foreign []map[string]any
	if err := db.WithContext(ctx).Raw("PRAGMA foreign_key_check").Scan(&foreign).Error; err != nil {
		return err
	}
	if len(foreign) != 0 {
		return errors.New("数据库存在外键引用损坏")
	}
	return nil
}

func modelSchemas() (map[string]*schema.Schema, error) {
	result := map[string]*schema.Schema{}
	cache := &sync.Map{}
	for _, item := range database.LocalModels() {
		s, err := schema.Parse(item, cache, schema.NamingStrategy{})
		if err != nil {
			return nil, err
		}
		result[s.Table] = s
	}
	return result, nil
}

func auditTables(ctx context.Context, db *gorm.DB, workspace string) error {
	if err := noRows(ctx, db, "SELECT COUNT(*) FROM sqlite_master WHERE type IN ('view', 'trigger')", "本阶段不支持自定义视图或触发器"); err != nil {
		return err
	}
	allowed, err := modelSchemas()
	if err != nil {
		return err
	}
	var tables []string
	if err := db.WithContext(ctx).Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables).Error; err != nil {
		return err
	}
	for _, table := range tables {
		if table == "local_schema_migrations" || table == "runtime_mode_markers" {
			continue
		}
		s, ok := allowed[table]
		if !ok {
			return fmt.Errorf("不支持的数据库表 %s，拒绝混合或未知格式", table)
		}
		columns, err := db.Migrator().ColumnTypes(table)
		if err != nil {
			return err
		}
		for _, column := range columns {
			if _, ok := s.FieldsByDBName[column.Name()]; !ok {
				return fmt.Errorf("表 %s 包含未支持字段 %s", table, column.Name())
			}
		}
		if len(columns) != len(s.FieldsByDBName) {
			return fmt.Errorf("表 %s 缺失受支持结构字段", table)
		}
		var count int64
		if unsupportedTables[table] {
			if err := db.WithContext(ctx).Table(table).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("本阶段暂不支持非空历史表 %s，源历史数据未改变", table)
			}
		}
		if _, ok := s.FieldsByDBName["user_id"]; ok {
			query := db.WithContext(ctx).Table(table).Where("user_id IS NULL OR user_id <> ?", workspace)
			if table == "model_channels" {
				query = db.WithContext(ctx).Table(table).Where("COALESCE(user_id, '') NOT IN (?, '') OR scope NOT IN ('user', 'system')", workspace)
			}
			if err := query.Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("表 %s 存在跨工作区或缺失归属", table)
			}
		}
	}
	if len(tables) != len(allowed)+2 {
		return errors.New("源数据库缺失受支持的业务表")
	}
	return auditJSONReferences(ctx, db, allowed)
}

func auditJSONReferences(ctx context.Context, db *gorm.DB, schemas map[string]*schema.Schema) error {
	var resourceIDs []string
	if err := db.WithContext(ctx).Table("resources").Pluck("id", &resourceIDs).Error; err != nil {
		return err
	}
	resources := map[string]bool{}
	for _, id := range resourceIDs {
		resources[id] = true
	}
	for table, s := range schemas {
		if unsupportedTables[table] || !db.Migrator().HasTable(table) {
			continue
		}
		for column := range s.FieldsByDBName {
			if !strings.HasSuffix(column, "_json") || column == "headers_json" || !db.Migrator().HasColumn(table, column) {
				continue
			}
			var values []string
			if err := db.WithContext(ctx).Table(table).Where(column+" IS NOT NULL AND "+column+" <> ''").Pluck(column, &values).Error; err != nil {
				return err
			}
			for _, value := range values {
				if _, err := strictJSON(value); err != nil {
					return fmt.Errorf("%s.%s 包含无法审计的 JSON：%w", table, column, err)
				}
				ids := map[string]struct{}{}
				if err := assets.CollectOwnedDocumentReferences(value, ids); err != nil {
					return fmt.Errorf("%s.%s 包含无法审计的 JSON", table, column)
				}
				for id := range ids {
					if !resources[id] {
						return fmt.Errorf("%s.%s 引用缺失资源", table, column)
					}
				}
			}
		}
	}
	return nil
}

func auditInactive(ctx context.Context, db *gorm.DB) error {
	for _, table := range []string{"resource_deletion_jobs", "user_upload_reservations"} {
		var count int64
		if err := db.WithContext(ctx).Table(table).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("存在未完成的 %s，请在源工作区完成操作后导入", table)
		}
	}
	var tasks []model.Task
	if err := db.WithContext(ctx).Find(&tasks).Error; err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Status != model.TaskStatusSucceeded && task.Status != model.TaskStatusFailed && task.Status != model.TaskStatusCancelled {
			return errors.New("存在未完成本地任务，请先结束或核对后导入")
		}
		if task.MediaSettlementPending() || task.NextPollAt != nil || task.PollStage != "" || task.ProviderCancelStatus == model.ProviderCancelStatusRequested || task.ProviderCancelStatus == model.ProviderCancelStatusUncertain {
			return errors.New("存在仍需查询、交付或结算的本地任务")
		}
		if task.Status != model.TaskStatusSucceeded && (task.ProviderRequestID != "" || task.Stage == "submission_unknown") && (task.RecoveryOutcome == nil || (*task.RecoveryOutcome != "no_result" && *task.RecoveryOutcome != "unrecoverable")) {
			return errors.New("存在上游结果未知的本地任务")
		}
		if task.Status == model.TaskStatusSucceeded {
			var results []model.Result
			if err := db.WithContext(ctx).Where("task_id = ? AND kind = ?", task.ID, localtask.ResultKindGenerationOutput).Find(&results).Error; err != nil {
				return err
			}
			outputs, err := taskdelivery.DecodeStoredOutputs(results)
			projection := taskdelivery.Project(task, outputs, err, false)
			if projection.Err != nil || projection.ResultState != localtask.ResultStateReady {
				return errors.New("成功任务仍有未完成或不可读的本地交付")
			}
			for _, output := range projection.Outputs {
				var count int64
				if output.ResourceID == "" || output.MaterializedAssetID == "" {
					return errors.New("成功媒体任务必须已经保存资源和素材")
				}
				if err := db.WithContext(ctx).Table("asset_representations r").Joins("JOIN asset_versions v ON v.id = r.asset_version_id").Joins("JOIN assets a ON a.id = v.asset_id").Where("a.id = ? AND a.user_id = ? AND r.resource_id = ?", output.MaterializedAssetID, task.UserID, output.ResourceID).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					return errors.New("成功媒体任务的本地素材与资源关联不完整")
				}
			}
		}
	}
	var count int64
	if err := db.WithContext(ctx).Table("canvas_workflow_runs").Where("status NOT IN ('succeeded', 'failed', 'cancelled')").Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return errors.New("存在未终结的本地工作流运行")
	}
	return nil
}
