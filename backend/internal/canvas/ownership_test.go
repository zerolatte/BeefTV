package canvas

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/gorm"
)

func requireCanvasNotFound(t *testing.T, err error) {
	t.Helper()
	var appError *kernel.AppError
	if !errors.Is(err, gorm.ErrRecordNotFound) && (!errors.As(err, &appError) || appError.Status != 404) {
		t.Fatalf("跨域或缺失对象应为404，实际: %v", err)
	}
}

func TestCanvasProjectAssociationAndIDCollisionAreScoped(t *testing.T) {
	svc := newCanvasHistoryTestService(t)
	for _, project := range []model.Project{{ID: "project-a", UserID: "a", Name: "A"}, {ID: "project-b", UserID: "b", Name: "B"}} {
		if err := svc.repo.DB().Create(&project).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, projectID := range []string{"project-b", "missing"} {
		raw, _ := json.Marshal(map[string]any{"id": "canvas-a", "revision": 0, "projectId": projectID, "nodes": []any{}})
		_, err := svc.UpsertUserCanvasProject("a", raw)
		requireCanvasNotFound(t, err)
	}
	created, err := svc.UpsertUserCanvasProject("a", json.RawMessage(`{"id":"canvas-a","revision":0,"projectId":"project-a","nodes":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpsertUserCanvasProject("a", json.RawMessage(`{"id":"canvas-a","revision":1,"projectId":"project-b","nodes":[]}`))
	requireCanvasNotFound(t, err)
	for _, revision := range []int{0, 1} {
		raw, _ := json.Marshal(map[string]any{"id": "canvas-a", "revision": revision, "nodes": []any{}})
		_, err := svc.UpsertUserCanvasProject("b", raw)
		requireCanvasNotFound(t, err)
	}
	stored, err := svc.repo.CanvasProjectForUser("a", "canvas-a")
	if err != nil || stored.ProjectID != "project-a" || stored.Revision != created.Revision {
		t.Fatalf("拒绝的关联更新改变了画布: %+v %v", stored, err)
	}
}

func TestForeignCanvasDeletionPreservesProjectLinks(t *testing.T) {
	svc := newCanvasHistoryTestService(t)
	if _, err := svc.UpsertUserCanvasProject("b", json.RawMessage(`{"id":"canvas-b","revision":0,"nodes":[]}`)); err != nil {
		t.Fatal(err)
	}
	link := model.CanvasUnitLink{ID: "link-b", ProjectID: "project-b", CanvasID: "canvas-b", UnitID: "unit-b"}
	if err := svc.repo.DB().Create(&link).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"canvas-b", "missing"} {
		requireCanvasNotFound(t, svc.DeleteUserCanvasProject("a", id))
	}
	var count int64
	if err := svc.repo.DB().Model(&model.CanvasUnitLink{}).Where("id = ?", link.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("跨域删除移除了他人的章节关联")
	}
	if _, err := svc.repo.CanvasProjectForUser("b", "canvas-b"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteUserCanvasProject("b", "canvas-b"); err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.DB().Model(&model.CanvasUnitLink{}).Where("id = ?", link.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("正常删除留下了章节关联")
	}
}

func TestCanvasFoldersAndNestedResourceReferencesAreScoped(t *testing.T) {
	svc := newCanvasHistoryTestService(t)
	if err := svc.repo.CreateResource(&model.Resource{ID: "resource-b", UserID: "b", Status: model.ResourceStatusReady}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertUserCanvasFolder("b", "folder-b", json.RawMessage(`{"name":"B"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := svc.UpsertUserCanvasFolder("a", "folder-b", json.RawMessage(`{"name":"伪造覆盖"}`))
	requireCanvasNotFound(t, err)
	_, err = svc.UpsertUserCanvasFolder("a", "folder-a", json.RawMessage(`{"coverResourceId":"resource-b"}`))
	requireCanvasNotFound(t, err)
	_, err = svc.UpsertUserCanvasProject("a", json.RawMessage(`{"id":"canvas-a","revision":0,"folderId":"folder-b","nodes":[]}`))
	requireCanvasNotFound(t, err)
	_, err = svc.UpsertUserCanvasProject("a", json.RawMessage(`{"id":"canvas-a","revision":0,"nodes":[{"id":"text","type":"text","metadata":{"coverResourceId":"resource-b"}}]}`))
	requireCanvasNotFound(t, err)
	if _, err := svc.UpsertUserCanvasProject("a", json.RawMessage(`{"id":"canvas-a","revision":0,"nodes":[]}`)); err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpsertUserCanvasDrawing("a", "canvas-a", "drawing", json.RawMessage(`{"revision":0,"snapshot":{"files":{"image":{"dataURL":"/api/resources/resource-b/file"}}}}`))
	requireCanvasNotFound(t, err)
	_, err = svc.UpsertUserCanvasDrawing("a", "canvas-a", "drawing", json.RawMessage(`{"revision":0,"snapshot":{},"render":{"storageKey":"resource:resource-b"}}`))
	requireCanvasNotFound(t, err)
	folder, err := svc.repo.CanvasLibraryFolderForUser("b", "folder-b")
	if err != nil || folder.Name != "B" {
		t.Fatalf("跨域写入改变了文件夹: %+v %v", folder, err)
	}
}

func TestCanvasSnapshotsValidateBothOwnersAndResourceScope(t *testing.T) {
	svc := newCanvasHistoryTestService(t)
	for _, resource := range []model.Resource{{ID: "resource-a", UserID: "a", Status: model.ResourceStatusReady}, {ID: "resource-b", UserID: "b", Status: model.ResourceStatusReady}} {
		if err := svc.repo.CreateResource(&resource); err != nil {
			t.Fatal(err)
		}
	}
	ready, err := svc.repo.ReadyResourceIDs("a", []string{"resource-a", "resource-b"})
	if err != nil || len(ready) != 1 || ready[0] != "resource-a" {
		t.Fatalf("就绪资源查询泄露其他工作区: %v %v", ready, err)
	}
	project := model.CanvasProject{ID: "canvas-a", UserID: "a", Title: "原文", PayloadJSON: `{"nodes":[]}`}
	if err := svc.repo.UpsertCanvasProject(&project); err != nil {
		t.Fatal(err)
	}
	snapshot := model.CanvasSnapshot{ID: "snapshot-a", CanvasID: project.ID, UserID: "b", Revision: 1, PayloadJSON: project.PayloadJSON, CreatedAt: time.Now()}
	copy := project
	copy.Title = "不应保存"
	err = svc.repo.SaveCanvasWithSnapshot(&copy, &snapshot, nil, nil, time.Now(), 20, true)
	requireCanvasNotFound(t, err)
	snapshot.UserID = "a"
	err = svc.repo.SaveCanvasWithSnapshot(&copy, &snapshot, []string{"resource-b"}, nil, time.Now(), 20, true)
	if !errors.Is(err, repository.ErrCanvasHistoryResourceMissing) {
		t.Fatalf("外域历史资源未被拒绝: %v", err)
	}
	stored, err := svc.repo.CanvasProjectForUser("a", project.ID)
	if err != nil || stored.Revision != 1 || stored.Title != "原文" {
		t.Fatalf("失败的历史事务未回滚: %+v %v", stored, err)
	}
	if err := svc.repo.SaveCanvasWithSnapshot(&copy, &snapshot, []string{"resource-a"}, nil, time.Now(), 20, true); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{"b", "administrator-workspace"} {
		refs, err := svc.repo.CanvasHistoryResourceReferences(userID, []string{"resource-a"})
		if err != nil || len(refs) != 0 {
			t.Fatalf("历史引用信息跨域泄露: %v %v", refs, err)
		}
	}
	refs, err := svc.repo.CanvasHistoryResourceReferences("a", []string{"resource-a"})
	if err != nil || len(refs) != 1 {
		t.Fatalf("本人历史引用不可读: %v %v", refs, err)
	}
}
