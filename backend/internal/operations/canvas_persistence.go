package operations

import "encoding/json"

// RegisterCanvasPersistenceOps 注册画布界面保存与已交付结果回填所需的操作。
// 平台与桌面共用同一实现，不开放模型提交或外部客户端能力。
func RegisterCanvasPersistenceOps(r *Registry) {
	r.Register(Op{ReplaySafe: true, ID: "canvas.document.commit", Summary: "按 revision 提交整份画布文档", Scope: ScopeCanvas,
		Params:  json.RawMessage(`{"type":"object","properties":{"canvasId":{"type":"string"},"expectedRevision":{"type":"integer"},"document":{"type":"object"}},"required":["canvasId","expectedRevision","document"]}`),
		Handler: opCanvasDocumentCommit})
	r.Register(Op{ReplaySafe: true, AssistantCanvas: true, ID: "canvas.task.bind", Summary: "把已交付任务产物绑定到节点", Scope: ScopeCanvas,
		Params:  json.RawMessage(`{"type":"object","properties":{"canvasId":{"type":"string"},"taskId":{"type":"string"},"nodeId":{"type":"string"},"outputIndex":{"type":"integer"}},"required":["canvasId","taskId","nodeId"]}`),
		Handler: opCanvasTaskBind, ProjectReplay: projectCanvasTaskBindReplay})
}
