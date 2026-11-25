package bootstrap

import (
	"context"
	"testing"

	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/repository"
)

func TestPlatformCanvasDocumentPersistence(t *testing.T) {
	t.Setenv("CANVAS_PUBLIC_ORIGIN", "http://localhost:3000")
	runtime, err := Open(context.Background(), Config{Profile: ProfileServer, DataDir: t.TempDir(), AutoMigrate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	auth := identity.New(repository.NewPlatform(runtime.db))
	admin, err := auth.InitializeAdmin("canvas-admin", "canvas-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	manager := &platformClient{runtime: runtime}
	manager.call(t, "POST", "/auth/login", map[string]any{"username": admin.Username, "password": "canvas-admin-password"}, 200)
	newClient := func(name string) *platformClient {
		account, temporary, err := auth.CreateAccount(admin.ID, name, "画布验证用户")
		if err != nil {
			t.Fatal(err)
		}
		if err := auth.ChangePassword(account.ID, temporary, "canvas-user-password"); err != nil {
			t.Fatal(err)
		}
		client := &platformClient{runtime: runtime}
		client.call(t, "POST", "/auth/login", map[string]any{"username": name, "password": "canvas-user-password"}, 200)
		return client
	}
	owner, other := newClient("canvas-owner"), newClient("canvas-other")
	canvasID := kernel.NewID()
	owner.call(t, "PUT", "/canvas-projects/"+canvasID, map[string]any{"project": map[string]any{
		"id": canvasID, "title": "保存前", "revision": 0, "nodes": []any{}, "connections": []any{},
	}}, 200)
	readCanvas := func() map[string]any {
		return data(owner.call(t, "GET", "/canvas-projects/"+canvasID, nil, 200))["project"].(map[string]any)
	}
	revision := readCanvas()["revision"].(float64)
	params := map[string]any{"canvasId": canvasID, "expectedRevision": revision, "document": map[string]any{
		"title": "保存后的画布", "nodes": []any{map[string]any{
			"id": "text-node", "type": "text", "title": "文本节点", "content": "刷新后仍然存在", "position": map[string]any{"x": 120, "y": 80},
		}},
	}}
	request := map[string]any{"opId": "canvas-save-1", "params": params}
	path := "/ops/canvas.document.commit"
	anonymous := &platformClient{runtime: runtime}
	anonymous.call(t, "POST", path, request, 401)
	csrf := owner.csrf
	owner.csrf = "invalid"
	owner.call(t, "POST", path, request, 403)
	owner.csrf = csrf
	first := data(owner.call(t, "POST", path, request, 200))
	if first["replayed"] != false || first["caller"] != "manual" {
		t.Fatalf("保存回执错误: %v", first)
	}
	saved := readCanvas()
	if saved["title"] != "保存后的画布" || saved["revision"] != revision+1 {
		t.Fatalf("画布未持久化: %v", saved)
	}
	nodes := saved["nodes"].([]any)
	if len(nodes) != 1 || nodes[0].(map[string]any)["content"] != "刷新后仍然存在" {
		t.Fatal("节点正文未持久化")
	}
	replayed := data(owner.call(t, "POST", path, request, 200))
	if replayed["replayed"] != true || readCanvas()["revision"] != revision+1 {
		t.Fatal("重复提交没有回放原保存回执")
	}
	// 同一幂等编号不能跨账号回放，管理员也无权覆盖其他人的画布。
	for _, client := range []*platformClient{other, manager} {
		client.call(t, "POST", path, request, 404)
		writes := data(client.call(t, "GET", "/ops/external-writes?canvasId="+canvasID, nil, 200))["writes"].([]any)
		if len(writes) != 0 {
			t.Fatal("泄露其他账号的画布操作")
		}
	}
	request["opId"] = "canvas-save-stale"
	conflict := owner.call(t, "POST", path, request, 409)
	if conflict["reason"] != "stale_revision" {
		t.Fatalf("版本冲突原因错误: %v", conflict)
	}
	params["expectedRevision"] = revision + 1
	request["opId"] = "canvas-save-1"
	owner.call(t, "POST", path, request, 409)
	request["opId"] = ""
	owner.call(t, "POST", path, request, 400)
	owner.call(t, "GET", "/ops/external-writes?canvasId="+canvasID, nil, 200)
	owner.call(t, "GET", "/ops/external-writes", nil, 400)
	// 仅开放保存与结果回填，模型提交、脚本和客户端登记仍不可调用。
	for _, op := range []string{"clients", "generation.submit", "skill.script.run", "canvas.create"} {
		owner.call(t, "POST", "/ops/"+op, request, 404)
	}
	bound := owner.call(t, "POST", "/ops/canvas.task.bind", map[string]any{
		"opId": "attach-node:missing-task:text-node:0", "params": map[string]any{
			"canvasId": canvasID, "taskId": "missing-task", "nodeId": "text-node", "outputIndex": 0,
		},
	}, 404)
	if bound["reason"] == "not_found" && bound["msg"] == "请求不存在" {
		t.Fatal("结果回填路由没有注册")
	}
	if readCanvas()["revision"] != revision+1 {
		t.Fatal("被拒绝的请求修改了画布")
	}
}
