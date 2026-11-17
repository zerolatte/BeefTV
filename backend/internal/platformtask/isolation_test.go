//go:build platform

package platformtask_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformtask"
)

func TestTextTaskRelatedProjectAndCancelRemainScoped(t *testing.T) {
	var posts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { posts.Add(1); textResponse(w) }))
	defer upstream.Close()
	f := setup(t, upstream.URL, 100)
	for _, owner := range []string{"creator", "other"} {
		if err := f.db.Create(&model.Project{ID: "project-" + owner, UserID: "workspace-" + owner, Status: model.ProjectStatusActive, Name: owner}).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := f.request("foreign-project")
	request.ProjectID = "project-other"
	if response := call(f.router, "POST", "/api/tasks", request); response.Code != 404 {
		t.Fatalf("跨用户项目生成没有404：%d %s", response.Code, response.Body.String())
	}
	if balance := f.balance(t); balance.ReservedPoints != 0 {
		t.Fatalf("关联校验失败仍预留：%+v", balance)
	}
	request = f.request("own-project-task")
	request.ProjectID = "project-creator"
	row := created(t, call(f.router, "POST", "/api/tasks", request))
	if row.ClientOperationID == nil || *row.ClientOperationID != "own-project-task" {
		t.Fatalf("公开任务没有恢复客户端幂等标识：%+v", row.ClientOperationID)
	}
	for _, action := range []string{"cancel", "recover", "retry"} {
		var body any
		if action == "retry" {
			body = map[string]string{"clientOperationId": "foreign-retry"}
		}
		response := call(router(f.service, principal("other")), "POST", "/api/tasks/"+row.ID+"/"+action, body)
		if response.Code != 404 {
			t.Fatalf("跨用户操作没有404：%s %d %s", action, response.Code, response.Body.String())
		}
	}
	if response := call(f.router, "GET", "/api/tasks/by-operation/own-project-task", nil); response.Code != 200 || !strings.Contains(response.Body.String(), row.ID) {
		t.Fatalf("原提交标识不能取回任务：%d %s", response.Code, response.Body.String())
	}
	for i := 0; i < 2; i++ {
		if _, err := f.service.Cancel(context.Background(), f.p, row.ID); err != nil && i == 0 {
			t.Fatal(err)
		}
	}
	_ = platformtask.ProcessOneForTest(context.Background(), f.service)
	if balance := f.balance(t); posts.Load() != 0 || balance.ReservedPoints != 0 || balance.SettledPoints != 0 {
		t.Fatalf("提交前取消没有释放一次：POST=%d %+v", posts.Load(), balance)
	}
}
