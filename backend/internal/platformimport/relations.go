//go:build platform

package platformimport

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

type parentRelation struct{ table, column, parent string }

// 外键按实际领域父对象列举；不能靠字段后缀猜测归属。
var parentRelations = []parentRelation{
	{"projects", "folder_id", "project_folders"}, {"projects", "cover_resource_id", "resources"},
	{"project_folders", "parent_id", "project_folders"}, {"project_units", "project_id", "projects"}, {"project_units", "parent_id", "project_units"},
	{"assets", "folder_id", "asset_folders"}, {"assets", "primary_version_id", "asset_versions"},
	{"asset_versions", "asset_id", "assets"}, {"asset_representations", "asset_version_id", "asset_versions"}, {"asset_representations", "resource_id", "resources"}, {"asset_representations", "task_id", "tasks"},
	{"project_asset_links", "project_id", "projects"}, {"project_asset_links", "asset_id", "assets"}, {"project_asset_links", "folder_id", "project_asset_folders"},
	{"project_asset_folders", "project_id", "projects"}, {"project_asset_folders", "parent_id", "project_asset_folders"},
	{"project_asset_candidates", "project_id", "projects"}, {"project_asset_candidates", "unit_id", "project_units"}, {"project_asset_candidates", "shot_id", "shots"}, {"project_asset_candidates", "resolved_asset_id", "assets"},
	{"voice_profiles", "sample_resource_id", "resources"}, {"character_voice_bindings", "asset_version_id", "asset_versions"}, {"character_voice_bindings", "voice_profile_id", "voice_profiles"},
	{"canvas_projects", "project_id", "projects"}, {"canvas_projects", "library_folder_id", "canvas_library_folders"},
	{"canvas_snapshots", "canvas_id", "canvas_projects"}, {"canvas_snapshot_resources", "snapshot_id", "canvas_snapshots"}, {"canvas_snapshot_resources", "resource_id", "resources"},
	{"canvas_library_folders", "cover_resource_id", "resources"}, {"canvas_drawings", "canvas_id", "canvas_projects"}, {"canvas_drawings", "preview_resource_id", "resources"}, {"canvas_drawings", "render_resource_id", "resources"},
	{"canvas_unit_links", "project_id", "projects"}, {"canvas_unit_links", "canvas_id", "canvas_projects"}, {"canvas_unit_links", "unit_id", "project_units"},
	{"shots", "project_id", "projects"}, {"shots", "unit_id", "project_units"}, {"shots", "current_revision_id", "shot_revisions"},
	{"shot_revisions", "shot_id", "shots"}, {"shot_asset_references", "shot_id", "shots"}, {"shot_asset_references", "asset_version_id", "asset_versions"},
	{"shot_artifacts", "project_id", "projects"}, {"shot_artifacts", "unit_id", "project_units"}, {"shot_artifacts", "shot_id", "shots"}, {"shot_artifacts", "revision_id", "shot_revisions"}, {"shot_artifacts", "task_id", "tasks"}, {"shot_artifacts", "resource_id", "resources"},
	{"workflow_instances", "project_id", "projects"}, {"workflow_instances", "unit_id", "project_units"}, {"workflow_instances", "template_version_id", "workflow_template_versions"},
	{"workflow_step_instances", "workflow_instance_id", "workflow_instances"}, {"workflow_step_tasks", "workflow_step_id", "workflow_step_instances"}, {"workflow_step_tasks", "task_id", "tasks"},
	{"production_task_links", "task_id", "tasks"}, {"production_task_links", "project_id", "projects"}, {"production_task_links", "canvas_id", "canvas_projects"}, {"production_task_links", "unit_id", "project_units"}, {"production_task_links", "shot_id", "shots"}, {"production_task_links", "workflow_step_id", "workflow_step_instances"},
	{"task_text_delta", "task_id", "tasks"}, {"task_logs", "task_id", "tasks"}, {"results", "task_id", "tasks"}, {"image_submissions", "task_id", "tasks"}, {"route_attempts", "task_id", "tasks"},
	{"image_submissions", "attempt_id", "route_attempts"},
	{"route_attempts", "logical_model_id", "logical_models"}, {"route_attempts", "logical_model_revision_id", "logical_model_revisions"}, {"route_attempts", "route_id", "logical_model_routes"}, {"route_attempts", "channel_model_id", "channel_models"}, {"route_attempts", "channel_id", "model_channels"},
	{"ark_private_asset_bindings", "resource_id", "resources"},
	{"canvas_workflow_revisions", "template_id", "canvas_workflows"}, {"canvas_workflow_runs", "template_id", "canvas_workflows"}, {"canvas_workflow_resources", "resource_id", "resources"},
	{"channel_models", "channel_id", "model_channels"}, {"channel_model_variants", "channel_model_id", "channel_models"},
	{"logical_model_revisions", "logical_model_id", "logical_models"}, {"logical_model_routes", "logical_model_revision_id", "logical_model_revisions"}, {"logical_model_routes", "channel_model_id", "channel_models"},
	{"logical_models", "active_revision_id", "logical_model_revisions"}, {"logical_models", "source_channel_model_id", "channel_models"},
	{"tasks", "logical_model_id", "logical_models"}, {"tasks", "logical_model_revision_id", "logical_model_revisions"}, {"tasks", "route_id", "logical_model_routes"}, {"tasks", "channel_model_id", "channel_models"},
}

func auditRelations(ctx context.Context, db *gorm.DB) error {
	for _, edge := range []struct{ table, column string }{
		{"project_units", "project_id"}, {"asset_versions", "asset_id"}, {"asset_representations", "asset_version_id"},
		{"project_asset_links", "project_id"}, {"project_asset_links", "asset_id"}, {"project_asset_folders", "project_id"}, {"project_asset_candidates", "project_id"},
		{"character_voice_bindings", "asset_version_id"}, {"character_voice_bindings", "voice_profile_id"},
		{"canvas_snapshots", "canvas_id"}, {"canvas_snapshot_resources", "snapshot_id"}, {"canvas_snapshot_resources", "resource_id"}, {"canvas_drawings", "canvas_id"},
		{"canvas_unit_links", "project_id"}, {"canvas_unit_links", "canvas_id"}, {"canvas_unit_links", "unit_id"},
		{"shots", "project_id"}, {"shot_revisions", "shot_id"}, {"shot_asset_references", "shot_id"}, {"shot_asset_references", "asset_version_id"}, {"shot_artifacts", "project_id"},
		{"workflow_instances", "project_id"}, {"workflow_step_instances", "workflow_instance_id"}, {"workflow_step_tasks", "workflow_step_id"}, {"workflow_step_tasks", "task_id"},
		{"production_task_links", "project_id"}, {"production_task_links", "task_id"},
		{"task_text_delta", "task_id"}, {"task_logs", "task_id"}, {"results", "task_id"}, {"image_submissions", "task_id"}, {"image_submissions", "attempt_id"}, {"route_attempts", "task_id"},
		{"ark_private_asset_bindings", "resource_id"}, {"canvas_workflow_revisions", "template_id"}, {"canvas_workflow_runs", "template_id"}, {"canvas_workflow_resources", "owner_id"}, {"canvas_workflow_resources", "resource_id"},
		{"channel_models", "channel_id"}, {"channel_model_variants", "channel_model_id"}, {"logical_model_revisions", "logical_model_id"}, {"logical_model_routes", "logical_model_revision_id"}, {"logical_model_routes", "channel_model_id"},
	} {
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s IS NULL OR TRIM(%s) = ''", edge.table, edge.column, edge.column)
		if err := noRows(ctx, db, query, edge.table+"."+edge.column+" 缺失必填父对象"); err != nil {
			return err
		}
	}
	for _, edge := range parentRelations {
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s c WHERE COALESCE(c.%s, '') <> '' AND NOT EXISTS (SELECT 1 FROM %s p WHERE p.id = c.%s)", edge.table, edge.column, edge.parent, edge.column)
		if err := noRows(ctx, db, query, edge.table+"."+edge.column+" 引用缺失父对象"); err != nil {
			return err
		}
	}
	for _, edge := range []struct{ table, column, parent, target string }{
		{"project_units", "parent_id", "project_units", "project_id"},
		{"project_asset_folders", "parent_id", "project_asset_folders", "project_id"},
		{"project_asset_links", "folder_id", "project_asset_folders", "project_id"},
		{"project_asset_candidates", "unit_id", "project_units", "project_id"}, {"project_asset_candidates", "shot_id", "shots", "project_id"},
		{"shots", "unit_id", "project_units", "project_id"}, {"shot_artifacts", "unit_id", "project_units", "project_id"}, {"shot_artifacts", "shot_id", "shots", "project_id"},
		{"canvas_unit_links", "unit_id", "project_units", "project_id"}, {"canvas_unit_links", "canvas_id", "canvas_projects", "project_id"},
		{"workflow_instances", "unit_id", "project_units", "project_id"},
		{"production_task_links", "unit_id", "project_units", "project_id"}, {"production_task_links", "shot_id", "shots", "project_id"}, {"production_task_links", "canvas_id", "canvas_projects", "project_id"},
	} {
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s c JOIN %s p ON p.id = c.%s WHERE COALESCE(c.%s,'') <> COALESCE(p.%s,'')", edge.table, edge.parent, edge.column, edge.target, edge.target)
		if err := noRows(ctx, db, query, edge.table+" 存在跨项目关联"); err != nil {
			return err
		}
	}
	for _, query := range []struct{ sql, message string }{
		{"SELECT COUNT(*) FROM image_submissions s JOIN route_attempts a ON a.id=s.attempt_id WHERE s.task_id<>a.task_id", "图片冻结请求与执行尝试不属于同一任务"},
		{"SELECT COUNT(*) FROM canvas_workflows t WHERE NOT EXISTS (SELECT 1 FROM canvas_workflow_revisions r WHERE r.template_id=t.id AND r.revision=t.revision)", "工作流缺少当前不可变版本"},
		{"SELECT COUNT(*) FROM canvas_workflow_runs n WHERE NOT EXISTS (SELECT 1 FROM canvas_workflow_revisions r WHERE r.template_id=n.template_id AND r.revision=n.template_revision)", "工作流运行缺少所引用的不可变版本"},
		{"SELECT COUNT(*) FROM assets a JOIN asset_versions v ON v.id = a.primary_version_id WHERE v.asset_id <> a.id", "素材当前版本不属于素材"},
		{"SELECT COUNT(*) FROM shots s JOIN shot_revisions r ON r.id = s.current_revision_id WHERE r.shot_id <> s.id", "镜头当前版本不属于镜头"},
		{"SELECT COUNT(*) FROM shot_artifacts a JOIN shot_revisions r ON r.id = a.revision_id WHERE r.shot_id <> a.shot_id", "镜头产物版本不属于镜头"},
		{"SELECT COUNT(*) FROM canvas_workflow_resources r WHERE NOT EXISTS (SELECT 1 FROM canvas_workflows t WHERE t.id = r.owner_id) AND NOT EXISTS (SELECT 1 FROM canvas_workflow_runs n WHERE n.id = r.owner_id)", "工作流资源缺失所属模板或运行"},
		{"SELECT COUNT(*) FROM tasks t WHERE COALESCE(t.project_id,'') <> '' AND NOT EXISTS (SELECT 1 FROM projects p WHERE p.id = t.project_id) AND NOT EXISTS (SELECT 1 FROM canvas_projects c WHERE c.id = t.project_id)", "任务缺失所属项目或画布"},
		{"SELECT COUNT(*) FROM tasks WHERE creation_submission_id IS NOT NULL AND creation_submission_id <> ''", "任务仍引用未支持的提案执行"},
		{"SELECT COUNT(*) FROM production_task_links p JOIN workflow_step_instances s ON s.id=p.workflow_step_id JOIN workflow_instances w ON w.id=s.workflow_instance_id WHERE w.project_id<>p.project_id", "制作记录跨项目关联工作流步骤"},
	} {
		if err := noRows(ctx, db, query.sql, query.message); err != nil {
			return err
		}
	}
	return auditParentCycles(ctx, db)
}

func auditParentCycles(ctx context.Context, db *gorm.DB) error {
	for _, table := range []string{"project_folders", "project_units", "project_asset_folders"} {
		var rows []struct{ ID, ParentID string }
		if err := db.WithContext(ctx).Table(table).Select("id,parent_id").Scan(&rows).Error; err != nil {
			return err
		}
		parents := map[string]string{}
		for _, row := range rows {
			parents[row.ID] = row.ParentID
		}
		visited := map[string]bool{}
		for start := range parents {
			path := map[string]bool{}
			for id := start; id != "" && !visited[id]; id = parents[id] {
				if path[id] {
					return fmt.Errorf("导入归属审计失败：%s 存在父子循环", table)
				}
				path[id] = true
			}
			for id := range path {
				visited[id] = true
			}
		}
	}
	return nil
}

func noRows(ctx context.Context, db *gorm.DB, query, message string) error {
	var count int64
	if err := db.WithContext(ctx).Raw(query).Scan(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("导入归属审计失败：%s", message)
	}
	return nil
}
