package project

import (
	"errors"
	"testing"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
)

func requireRevisionError(t *testing.T, err error, status int, reason string) {
	t.Helper()
	var typed *kernel.AppError
	if !errors.As(err, &typed) || typed.Status != status || string(typed.Reason) != reason {
		t.Fatalf("错误 = %#v，期望 %d/%s", err, status, reason)
	}
}

func TestProjectClientRevisionRejectsStaleAndMissingWrites(t *testing.T) {
	svc, db := newTestService(t, nil)
	project := seedProject(t, db, model.Project{ID: "revision-project", UserID: "user-1", Name: "原项目"})
	first, err := svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{ExpectedRevision: project.Revision, Name: "设备一修改"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{ExpectedRevision: project.Revision, Name: "设备二旧草稿"})
	requireRevisionError(t, err, 409, "revision_conflict")
	_, err = svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{Name: "缺少版本"})
	requireRevisionError(t, err, 400, "expected_revision_required")
	stored, err := svc.Owned("user-1", project.ID)
	if err != nil || stored.Name != first.Name || stored.Revision != first.Revision {
		t.Fatalf("拒绝的请求改变了项目：%+v，%v", stored, err)
	}
	if _, err := svc.UpdateProject("other-user", project.ID, UpdateProjectRequest{}); !IsNotFound(err) {
		t.Fatalf("异域项目应返回未找到，实际 %v", err)
	}
}

func TestProjectConcurrentClientsOnlyOneRevisionWins(t *testing.T) {
	svc, db := newTestService(t, nil)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	project := seedProject(t, db, model.Project{ID: "parallel-project", UserID: "user-1", Name: "原项目"})
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, name := range []string{"客户端甲", "客户端乙"} {
		go func(name string) {
			<-start
			_, err := svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{ExpectedRevision: project.Revision, Name: name})
			results <- err
		}(name)
	}
	close(start)
	succeeded := 0
	for range 2 {
		if err := <-results; err == nil {
			succeeded++
		} else {
			requireRevisionError(t, err, 409, "revision_conflict")
		}
	}
	if succeeded != 1 || ownedRevision(t, svc, project.ID) != project.Revision+1 {
		t.Fatalf("并发写入成功次数 = %d，应为 1", succeeded)
	}
}

func TestChapterSnapshotRevisionPreventsStaleContentAndSideEffects(t *testing.T) {
	svc, db := newTestService(t, nil)
	project := seedProject(t, db, model.Project{ID: "chapter-project", UserID: "user-1", Name: "章节项目"})
	unit, err := svc.CreateProjectUnit("user-1", project.ID, CreateProjectUnitRequest{Title: "第一章", SourceText: "初稿"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.GetProjectUnitSnapshot("user-1", project.ID, unit.ID)
	if err != nil || snapshot.Unit.SourceText != "初稿" || snapshot.ProjectRevision != ownedRevision(t, svc, project.ID) {
		t.Fatalf("章节读取快照 = %+v，%v", snapshot, err)
	}
	_, err = svc.UpdateProjectUnit("user-1", project.ID, unit.ID, UpdateProjectUnitRequest{ExpectedRevision: snapshot.ProjectRevision, Title: "新章名", SourceText: "设备一正文"})
	if err != nil {
		t.Fatal(err)
	}
	artifact := model.ShotArtifact{ID: "ready-artifact", ProjectID: project.ID, UnitID: unit.ID, ShotID: "shot", Status: "ready", Selected: true}
	if err := db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateProjectUnit("user-1", project.ID, unit.ID, UpdateProjectUnitRequest{ExpectedRevision: snapshot.ProjectRevision, Title: "陈旧章名", SourceText: "设备二旧正文"})
	requireRevisionError(t, err, 409, "revision_conflict")
	_, err = svc.UpdateProjectUnit("user-1", project.ID, unit.ID, UpdateProjectUnitRequest{SourceText: "无版本正文"})
	requireRevisionError(t, err, 400, "expected_revision_required")
	after, err := svc.GetProjectUnitSnapshot("user-1", project.ID, unit.ID)
	if err != nil || after.Unit.SourceText != "设备一正文" || after.ProjectRevision != snapshot.ProjectRevision+1 {
		t.Fatalf("旧请求改变正文或版本：%+v，%v", after, err)
	}
	if err := db.First(&artifact, "id = ?", artifact.ID).Error; err != nil || artifact.Status != "ready" || !artifact.Selected {
		t.Fatalf("旧请求改变了衍生结果：%+v，%v", artifact, err)
	}
	if _, err := svc.GetProjectUnitSnapshot("other-user", project.ID, unit.ID); !IsNotFound(err) {
		t.Fatalf("异域读取章节应返回未找到，实际 %v", err)
	}
	foreign := seedProject(t, db, model.Project{ID: "foreign-project", UserID: "other-user", Name: "异域项目"})
	if _, err := svc.UpdateProjectUnit("other-user", foreign.ID, unit.ID, UpdateProjectUnitRequest{ExpectedRevision: foreign.Revision}); !IsNotFound(err) {
		t.Fatalf("关联异域章节应返回未找到，实际 %v", err)
	}
}

func TestProjectCoverAndRevisionProtectedAssociations(t *testing.T) {
	svc, db := newTestService(t, nil)
	project := seedProject(t, db, model.Project{ID: "association-project", UserID: "user-1", Name: "关联项目"})
	resource := model.Resource{ID: "foreign-cover", UserID: "other-user", Kind: "image", Status: model.ResourceStatusReady}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateProject("user-1", project.ID, UpdateProjectRequest{ExpectedRevision: project.Revision, CoverResourceID: &resource.ID}); !IsNotFound(err) {
		t.Fatalf("异域封面应返回未找到，实际 %v", err)
	}
	unit, err := svc.CreateProjectUnit("user-1", project.ID, CreateProjectUnitRequest{Title: "第一章"})
	if err != nil {
		t.Fatal(err)
	}
	requireRevisionError(t, svc.ReorderProjectUnits("user-1", project.ID, ReorderProjectUnitsRequest{ExpectedRevision: project.Revision, UnitIDs: []string{unit.ID}}), 409, "revision_conflict")
	requireRevisionError(t, svc.MoveProjectToFolder("user-1", project.ID, "", project.Revision), 409, "revision_conflict")
	requireRevisionError(t, svc.MoveProjectToFolder("user-1", project.ID, "", 0), 400, "expected_revision_required")
}
