//go:build platform

package platformimport

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
)

func TestImportRejectsUnsafeSourcesWithoutChangingSource(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		prepare      func(*testing.T, *sourceFixture)
	}{
		{"未知表", "不支持的数据库表", func(t *testing.T, f *sourceFixture) { sql(t, f, "CREATE TABLE unknown_data (id TEXT)") }},
		{"未知字段", "未支持字段", func(t *testing.T, f *sourceFixture) { sql(t, f, "ALTER TABLE projects ADD COLUMN unknown_field TEXT") }},
		{"未来版本", "schema v29", func(t *testing.T, f *sourceFixture) {
			sql(t, f, "INSERT INTO local_schema_migrations(version,name) VALUES(30,'future')")
		}},
		{"未知迁移分支", "非规范迁移历史", func(t *testing.T, f *sourceFixture) {
			sql(t, f, "UPDATE local_schema_migrations SET name='unknown-preview' WHERE version=17")
		}},
		{"触发器", "触发器", func(t *testing.T, f *sourceFixture) {
			sql(t, f, "CREATE TRIGGER unexpected AFTER UPDATE OF mode ON runtime_mode_markers BEGIN UPDATE platform_credentials SET password_hash='attacker-controlled'; END")
		}},
		{"混合工作区", "跨工作区", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.Project{ID: "foreign-project", UserID: "foreign", Name: "不属于源"})
		}},
		{"缺失父对象", "缺失父对象", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.Project{ID: "project", UserID: f.workspace, Name: "项目", CoverResourceID: "missing"})
		}},
		{"空素材父对象", "缺失必填父对象", func(t *testing.T, f *sourceFixture) {
			sql(t, f, "INSERT INTO asset_versions(id,asset_id) VALUES('orphan','')")
		}},
		{"空章节父对象", "缺失必填父对象", func(t *testing.T, f *sourceFixture) {
			sql(t, f, "INSERT INTO project_units(id,project_id) VALUES('orphan','')")
		}},
		{"重复JSON键", "重复键", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.Task{ID: "duplicate", UserID: f.workspace, Type: "canvas_text", Status: model.TaskStatusSucceeded, InputJSON: `{"apiKey":"secret","apiKey":"replacement"}`})
		}},
		{"跨项目章节", "跨项目关联", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.Project{ID: "first", UserID: f.workspace, Name: "第一项目"})
			insert(t, f.db, &model.Project{ID: "second", UserID: f.workspace, Name: "第二项目"})
			sql(t, f, "INSERT INTO project_units(id,project_id) VALUES('parent','first')")
			sql(t, f, "INSERT INTO project_units(id,project_id,parent_id) VALUES('child','second','parent')")
		}},
		{"运行工作流", "未终结的本地工作流", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.CanvasWorkflow{ID: "template", UserID: f.workspace, Name: "模板"})
			insert(t, f.db, &model.CanvasWorkflowRevision{TemplateID: "template", Revision: 1, UserID: f.workspace})
			insert(t, f.db, &model.CanvasWorkflowRun{ID: "run", UserID: f.workspace, TemplateID: "template", Status: "running"})
		}},
		{"缺工作流版本", "不可变版本", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.CanvasWorkflow{ID: "missing-version", UserID: f.workspace})
		}},
		{"目录循环", "父子循环", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.ProjectFolder{ID: "cycle", UserID: f.workspace, Name: "循环", ParentID: "cycle"})
		}},
		{"执行尝试任务混用", "不属于同一任务", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.Task{ID: "first", UserID: f.workspace, Type: "canvas_text", Status: model.TaskStatusSucceeded})
			insert(t, f.db, &model.Task{ID: "second", UserID: f.workspace, Type: "canvas_text", Status: model.TaskStatusSucceeded})
			insert(t, f.db, &model.RouteAttempt{ID: "attempt", TaskID: "first"})
			insert(t, f.db, &model.ImageSubmission{AttemptID: "attempt", TaskID: "second", UserID: f.workspace})
		}},
		{"资源缺失", "文件缺失", func(t *testing.T, f *sourceFixture) {
			addResource(t, f)
			if err := os.Remove(filepath.Join(f.source, "resources", "sample.png")); err != nil {
				t.Fatal(err)
			}
		}},
		{"路径逃逸", "安全相对路径", func(t *testing.T, f *sourceFixture) {
			addResource(t, f)
			sql(t, f, "UPDATE resources SET object_key='../sample.png'")
		}},
		{"播放副本", "播放缓存副本", func(t *testing.T, f *sourceFixture) {
			addResource(t, f)
			sql(t, f, "UPDATE resources SET playback_object_key='copy.mp4',playback_status='ready'")
		}},
		{"外部存储", "外部对象存储", func(t *testing.T, f *sourceFixture) {
			addResource(t, f)
			sql(t, f, "UPDATE resources SET provider='oss'")
		}},
		{"运行中任务", "未完成本地任务", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.Task{ID: "running", UserID: f.workspace, Status: model.TaskStatusRunning, Type: "canvas_text"})
		}},
		{"未交付成功任务", "未完成或不可读", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.Task{ID: "unfetched", UserID: f.workspace, Status: model.TaskStatusSucceeded, Type: "canvas_image", ResultJSON: `{"images":["https://example.invalid/image.png"]}`})
		}},
		{"删除队列", "resource_deletion_jobs", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.ResourceDeletionJob{ID: "deletion", UserID: f.workspace})
		}},
		{"上传预留", "user_upload_reservations", func(t *testing.T, f *sourceFixture) {
			sql(t, f, "INSERT INTO user_upload_reservations(id,user_id) VALUES('upload',?)", f.workspace)
		}},
		{"关闭模块历史", "非空历史表", func(t *testing.T, f *sourceFixture) {
			sql(t, f, "INSERT INTO agent_profiles(id,user_id) VALUES('profile',?)", f.workspace)
		}},
		{"未知密文", "未知密文", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.Task{ID: "cipher", UserID: f.workspace, Status: model.TaskStatusSucceeded, Type: "canvas_text", Prompt: encrypt(t, f.source, "hidden-value")})
		}},
		{"请求头残留", "凭据残留", func(t *testing.T, f *sourceFixture) {
			insert(t, f.db, &model.ModelChannel{ID: "channel", Scope: "user", UserID: f.workspace, HeadersJSON: `[{"key":"Authorization","value":"Bearer header-private-token"}]`})
			insert(t, f.db, &model.Task{ID: "leak", UserID: f.workspace, Status: model.TaskStatusSucceeded, Type: "canvas_text", Prompt: "历史正文含 header-private-token"})
		}},
		{"本地配置残留", "凭据残留", func(t *testing.T, f *sourceFixture) {
			raw := encode(t, map[string]any{"encryptedConfig": encrypt(t, f.source, `{"config":{"apiKey":"file-private-token"}}`)})
			if err := writeFile(filepath.Join(f.source, "local-model-config.json"), []byte(raw)); err != nil {
				t.Fatal(err)
			}
			insert(t, f.db, &model.Task{ID: "leak", UserID: f.workspace, Status: model.TaskStatusSucceeded, Type: "canvas_text", Prompt: "历史正文含 file-private-token"})
		}},
		{"不支持目录", "非空目录", func(t *testing.T, f *sourceFixture) {
			dir := filepath.Join(f.source, "skill-packages")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := writeFile(filepath.Join(dir, "data"), []byte("保留")); err != nil {
				t.Fatal(err)
			}
		}},
		{"未知根文件", "不识别源文件", func(t *testing.T, f *sourceFixture) {
			if err := writeFile(filepath.Join(f.source, "unknown.bin"), []byte("保留")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSource(t)
			test.prepare(t, f)
			f.close(t)
			before, err := os.ReadFile(filepath.Join(f.source, databaseName))
			if err != nil {
				t.Fatal(err)
			}
			key, err := os.ReadFile(filepath.Join(f.source, ".settings-key"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Import(context.Background(), f.input()); err == nil || !strings.Contains(err.Error(), test.reason) {
				t.Fatalf("期望 %s，实际 %v", test.reason, err)
			}
			after, err := os.ReadFile(filepath.Join(f.source, databaseName))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("拒绝导入却改变源数据库", err)
			}
			afterKey, err := os.ReadFile(filepath.Join(f.source, ".settings-key"))
			if err != nil || !bytes.Equal(key, afterKey) {
				t.Fatal("拒绝导入却改变源密钥", err)
			}
			if _, err := os.Lstat(f.destination); !os.IsNotExist(err) {
				t.Fatal("失败导入产生了可交付目标", err)
			}
		})
	}
}

func sql(t *testing.T, f *sourceFixture, query string, args ...any) {
	t.Helper()
	if err := f.db.Exec(query, args...).Error; err != nil {
		t.Fatal(err)
	}
}

func TestImportFailureAfterSnapshotKeepsSourceAndPublishesNothing(t *testing.T) {
	f := newSource(t)
	addResource(t, f)
	f.close(t)
	before := fingerprint(t, f.source)
	input := f.input()
	input.Account.Password = "too-short"
	if _, err := Import(context.Background(), input); err == nil {
		t.Fatal("错误密码未阻止开户")
	}
	if fingerprint(t, f.source) != before {
		t.Fatal("失败开户改变了源业务文件")
	}
	if _, err := os.Lstat(f.destination); !os.IsNotExist(err) {
		t.Fatal("失败开户交付了目标")
	}
	entries, err := os.ReadDir(filepath.Dir(f.destination))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".platform-import-") {
			t.Fatal("失败后残留暂存目录")
		}
	}
}

func TestPublishStageNeverOverwritesExistingTarget(t *testing.T) {
	parent := t.TempDir()
	stage := filepath.Join(parent, "stage")
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(stage, database.PlatformRestoreIncompleteFileName), []byte("incomplete")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := publishStage(stage, target); err == nil {
		t.Fatal("已存在空目录被覆盖")
	}
}

func TestImportDoesNotCopyExcludedCaches(t *testing.T) {
	f := newSource(t)
	cache := filepath.Join(f.source, "cache")
	if err := os.Mkdir(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(cache, "local-only"), []byte("保留源缓存")); err != nil {
		t.Fatal(err)
	}
	f.close(t)
	if _, err := Import(context.Background(), f.input()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.destination, "cache")); !os.IsNotExist(err) {
		t.Fatal("复制了排除目录")
	}
	if _, err := os.Stat(filepath.Join(f.source, "cache", "local-only")); err != nil {
		t.Fatal("改变了源缓存")
	}
	if _, err := os.Stat(filepath.Join(f.destination, database.PlatformRestoreIncompleteFileName)); !os.IsNotExist(err) {
		t.Fatal("导入完成未解除启动阻断")
	}
}
