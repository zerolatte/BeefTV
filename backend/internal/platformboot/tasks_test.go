//go:build platform

package platformboot

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platformauth"
	"infinite-canvas/backend/internal/task"
)

func TestPlatformTaskReadsAndStreamUseSessionWorkspace(t *testing.T) {
	fixture := newPlatformFixture(t)
	browsers := []*platformBrowser{fixture.login(t, fixture.admin.Username, false)}
	for _, name := range []string{"alice", "bravo"} {
		account, err := fixture.runtime.Auth.CreateAccount(t.Context(), fixture.admin.ID, platformAccountInput(name))
		if err != nil {
			t.Fatal(err)
		}
		browsers = append(browsers, fixture.login(t, account.Username, true))
	}
	rows := make([]model.Task, len(browsers))
	for i, browser := range browsers {
		rows[i] = model.Task{ID: uuid.NewString(), UserID: browser.account.WorkspaceID, Type: "canvas_text", Operation: "text_generation", Status: model.TaskStatusRunning, InputJSON: `{}`, TextDraft: "属于当前工作区"}
		if err := fixture.runtime.db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, browser := range browsers {
		items := decodePlatformData[[]task.Summary](t, browser.request(t, "GET", "/tasks", nil, 200, nil))
		if len(items) != 1 || items[0].ID != rows[i].ID {
			t.Fatal("任务列表泄露了其他工作区")
		}
		for j, row := range rows {
			status := 200
			if i != j {
				status = 404
			}
			for _, suffix := range []string{"", "/logs", "/text-deltas"} {
				browser.request(t, "GET", "/tasks/"+row.ID+suffix, nil, status, nil)
			}
			if i != j {
				browser.request(t, "GET", "/tasks/"+row.ID+"/text-events", nil, 404, nil)
			}
		}
	}

	browser := browsers[1]
	request, err := http.NewRequestWithContext(t.Context(), "GET", fixture.server.URL+"/api/tasks/"+rows[1].ID+"/text-events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := browser.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("本人文本流不可读", response.StatusCode)
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, response.Body); close(done) }()
	status := platformauth.StatusDisabled
	start := time.Now()
	if _, err := fixture.runtime.Auth.UpdateAccount(t.Context(), fixture.admin.ID, browser.account.ID, platformauth.UpdateAccountInput{Status: &status}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		if time.Since(start) > 5*time.Second {
			t.Fatal("撤销后文本长连接超时")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("账号禁用后文本长连接未在5秒内中断")
	}
}
