//go:build platform

package handler

import (
	"context"
	"net/http"
	"testing"

	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformworkflow"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/task"
)

type closedWorkflowTasks struct{}

func (closedWorkflowTasks) Available() bool { return false }
func (closedWorkflowTasks) Create(context.Context, string, task.CreateRequest) (*model.Task, error) {
	return nil, kernel.Forbidden("生成关闭")
}
func (closedWorkflowTasks) Retry(context.Context, string, string, string) (*model.Task, error) {
	return nil, kernel.Forbidden("生成关闭")
}
func (closedWorkflowTasks) Cancel(context.Context, string, string) error {
	return kernel.Forbidden("生成关闭")
}

func TestPlatformWorkflowRoutesRequireCookieAndCSRF(t *testing.T) {
	env := newPlatformAuthTestEnvironment(t)
	repo := repository.New(env.db)
	identityCipher := func(value string) (string, error) { return value, nil }
	workflows, err := platformworkflow.New(repo, closedWorkflowTasks{}, modelcatalog.NewPlatformCatalogReader(repo), platformworkflow.Cipher{Encrypt: identityCipher, Decrypt: identityCipher})
	if err != nil {
		t.Fatal(err)
	}
	routes := env.router.Group("/api")
	routes.Use(env.auth.middleware())
	RegisterPlatformWorkflowRoutes(routes, workflows)
	for _, path := range []string{"/api/canvas-workflow-operations", "/api/canvas-workflows", "/api/canvas-workflow-runs/foreign"} {
		response := platformAuthRequest(t, env.router, "GET", path, nil, nil, map[string]string{"X-Canvas-Owner-Token": "local-token"})
		platformAuthResponse(t, response, 401)
	}
	grant, cookie := loginForPlatformTest(t, env, env.admin.Username, testPlatformPassword)
	for _, path := range []string{"/api/canvas-workflows", "/api/canvas-workflow-operations"} {
		response := platformAuthRequest(t, env.router, "GET", path, nil, []*http.Cookie{cookie}, nil)
		platformAuthResponse(t, response, 200)
	}
	path := "/api/canvas-workflows/missing"
	for _, headers := range []map[string]string{nil, {"Origin": "https://foreign.test", "X-CSRF-Token": grant["csrfToken"].(string)}} {
		response := platformAuthRequest(t, env.router, "DELETE", path, nil, []*http.Cookie{cookie}, headers)
		if platformAuthResponse(t, response, 403)["reason"] != "csrf_invalid" {
			t.Fatal("工作流写入绕过CSRF")
		}
	}
	response := platformAuthRequest(t, env.router, "DELETE", path, nil, []*http.Cookie{cookie}, map[string]string{"Origin": testPlatformOrigin, "X-CSRF-Token": grant["csrfToken"].(string)})
	platformAuthResponse(t, response, 404)
}
