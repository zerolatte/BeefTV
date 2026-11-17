package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/textreplay"
)

type taskTextReadFixture struct {
	textReplayAPI
	userID, taskID string
	after          int64
}

func (f *taskTextReadFixture) Read(userID, taskID string, after int64) (*textreplay.Result, error) {
	f.userID, f.taskID, f.after = userID, taskID, after
	return &textreplay.Result{Status: model.TaskStatusSucceeded, Complete: true}, nil
}

func (f *taskTextReadFixture) CachedRead(_ context.Context, userID, taskID string, after int64) (*textreplay.Result, error) {
	return f.Read(userID, taskID, after)
}

func TestTaskTextReadRoutesUseWorkspaceAndRejectClientWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if err := SetPrincipal(c, identity.Principal{Mode: identity.ModePlatform, AccountID: "account-a", WorkspaceID: "workspace-a", SessionID: "session-a", Role: identity.RoleUser, Status: identity.StatusActive}); err != nil {
			t.Fatal(err)
		}
	})
	replay := &taskTextReadFixture{}
	registerTaskTextReadRoutes(router.Group("/api"), nil, replay)
	for _, path := range []string{"text-deltas", "text-events"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/tasks/task-a/"+path+"?after=7", nil))
		if response.Code != http.StatusOK || replay.userID != "workspace-a" || replay.taskID != "task-a" || replay.after != 7 {
			t.Fatalf("读取归属或游标错误：响应=%d 归属=%s 任务=%s 游标=%d", response.Code, replay.userID, replay.taskID, replay.after)
		}
		if path == "text-events" && !strings.Contains(response.Body.String(), "event: terminal") {
			t.Fatalf("没有复用任务终态事件：%s", response.Body.String())
		}
	}
	for _, path := range []string{"text-deltas", "text-replay-complete"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/tasks/task-a/"+path, strings.NewReader(`{"text":"伪造结果"}`)))
		if response.Code != http.StatusNotFound {
			t.Fatalf("客户端写入入口被开放：%s status=%d", path, response.Code)
		}
	}
}
