package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/project"
	"infinite-canvas/backend/internal/projectread"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/task"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type emptyProjectTasks struct{}

func (emptyProjectTasks) TasksWithOptions(string, task.ListOptions) ([]task.Summary, error) {
	return []task.Summary{}, nil
}

func TestProjectHTTPRevisionAndWorkspaceIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "projects.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if err := database.MigrateLocalSchema(db); err != nil {
		t.Fatal(err)
	}
	projects := project.New(repository.New(db), project.Dependencies{})
	first, err := projects.CreateProject("workspace-a", project.CreateProjectRequest{Name: "甲的项目"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := projects.CreateProject("workspace-b", project.CreateProjectRequest{Name: "乙的私密项目"})
	if err != nil {
		t.Fatal(err)
	}
	firstUnit, err := projects.CreateProjectUnit("workspace-a", first.ID, project.CreateProjectUnitRequest{Title: "甲章节", SourceText: "甲正文"})
	if err != nil {
		t.Fatal(err)
	}
	secondUnit, err := projects.CreateProjectUnit("workspace-b", second.ID, project.CreateProjectUnitRequest{Title: "乙章节", SourceText: "乙私密正文"})
	if err != nil {
		t.Fatal(err)
	}
	character, err := projects.CreateProjectCharacter("workspace-a", first.ID, project.CreateProjectCharacterRequest{Name: "角色"})
	if err != nil {
		t.Fatal(err)
	}
	foreignFolder, err := projects.CreateProjectFolder("workspace-b", project.CreateProjectFolderRequest{Name: "乙目录"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.Resource{ID: "foreign-image", UserID: "workspace-b", Kind: "image", Status: model.ResourceStatusReady},
		&model.Resource{ID: "foreign-audio", UserID: "workspace-b", Kind: "audio", MimeType: "audio/mpeg", Status: model.ResourceStatusReady},
		&model.CanvasProject{ID: "foreign-canvas", UserID: "workspace-b", Title: "乙画布", Revision: 1, PayloadJSON: "{}"},
		&model.VoiceProfile{ID: "foreign-voice", UserID: "workspace-b", Name: "乙声音", Status: "active"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	api := router.Group("/api")
	api.Use(func(c *gin.Context) {
		role := identity.RoleUser
		if c.GetHeader("X-Test-Admin") == "1" {
			role = identity.RoleAdmin
		}
		if err := SetPrincipal(c, identity.Principal{Mode: identity.ModePlatform, AccountID: "account-a", WorkspaceID: "workspace-a", Role: role, Status: identity.StatusActive, SessionID: "session-a"}); err != nil {
			t.Fatal(err)
		}
		c.Next()
	})
	// 不提供本地 owner，确保平台调用不走默认工作区兜底。
	RegisterProjectRoutes(api, nil, projects, projectread.New(projects, emptyProjectTasks{}))
	call := func(method, path string, body any, admin bool) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, "/api"+path, bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		if admin {
			req.Header.Set("X-Test-Admin", "1")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	current, err := projects.Owned("workspace-a", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, admin := range []bool{false, true} {
		for _, request := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, "/projects/" + second.ID, nil},
			{http.MethodGet, "/projects/" + second.ID + "/core", nil},
			{http.MethodPatch, "/projects/" + second.ID, map[string]any{"name": "尝试覆盖", "expectedRevision": second.Revision}},
			{http.MethodDelete, "/projects/" + second.ID, nil},
			{http.MethodGet, "/projects/" + first.ID + "/units/" + secondUnit.ID, nil},
			{http.MethodPatch, "/projects/" + first.ID + "/units/" + secondUnit.ID, map[string]any{"sourceText": "越权", "expectedRevision": current.Revision}},
			{http.MethodPatch, "/projects/" + first.ID, map[string]any{"coverResourceId": "foreign-image", "expectedRevision": current.Revision}},
			{http.MethodPatch, "/projects/" + first.ID + "/folder", map[string]any{"folderId": foreignFolder.ID, "expectedRevision": current.Revision}},
			{http.MethodPost, "/projects/" + first.ID + "/canvas-links", map[string]any{"canvasId": "foreign-canvas", "unitId": firstUnit.ID}},
			{http.MethodPost, "/projects/" + first.ID + "/assets", map[string]any{"assetId": "foreign-image", "category": "image"}},
			{http.MethodPut, "/projects/" + first.ID + "/characters/" + character.Asset.ID + "/representations", map[string]any{"representations": []any{map[string]any{"resourceId": "foreign-image", "role": "primary"}}}},
			{http.MethodPut, "/projects/" + first.ID + "/characters/" + character.Asset.ID + "/voice", map[string]any{"voiceProfileId": "foreign-voice"}},
			{http.MethodPut, "/projects/" + first.ID + "/characters/" + character.Asset.ID + "/voice", map[string]any{"sampleResourceId": "foreign-audio"}},
		} {
			response := call(request.method, request.path, request.body, admin)
			if response.Code != http.StatusNotFound {
				t.Fatalf("admin=%v %s %s = %d %s，期望 404", admin, request.method, request.path, response.Code, response.Body.String())
			}
		}
		list := call(http.MethodGet, "/projects", nil, admin)
		if list.Code != http.StatusOK || strings.Contains(list.Body.String(), second.ID) || !strings.Contains(list.Body.String(), first.ID) {
			t.Fatalf("列表未按工作区隔离：%s", list.Body.String())
		}
	}
	write := map[string]any{"name": "甲新名称", "expectedRevision": current.Revision}
	if response := call(http.MethodPatch, "/projects/"+first.ID, write, false); response.Code != http.StatusOK {
		t.Fatalf("首个客户端写入失败：%s", response.Body.String())
	}
	if response := call(http.MethodPatch, "/projects/"+first.ID, write, false); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"reason":"revision_conflict"`) {
		t.Fatalf("陈旧客户端未收到版本冲突：%d %s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPatch, "/projects/"+first.ID, map[string]any{"name": "缺版本"}, false); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"reason":"expected_revision_required"`) {
		t.Fatalf("缺失版本未被拒绝：%d %s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/projects/"+first.ID+"/units/"+firstUnit.ID+"/workspace", nil, false); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"projectRevision":`) {
		t.Fatalf("章节读取未返回版本：%d %s", response.Code, response.Body.String())
	}
}
