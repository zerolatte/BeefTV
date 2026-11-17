package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/generation"
)

// CanvasGenerationPorts 只连接任务文本归档；模型执行继续使用 generation.Runtime。
type CanvasGenerationPorts struct {
	AppendTextDelta func(userID, taskID, delta string) error
	LogWarning      func(userID, taskID, message, payload string)
}

// ExecuteCanvasGenerationTask 统一本地与平台 worker 的输入解析、文本增量和执行入口。
// 身份取自 worker 绑定的 Runtime，不能从任务 JSON 覆盖。
func ExecuteCanvasGenerationTask(ctx context.Context, fallbackPrompt, rawInput string, ports CanvasGenerationPorts) (map[string]any, error) {
	var input generation.Input
	defer func() { generation.RecordTaskDiagnosticInput(ctx, input) }()
	if err := json.Unmarshal([]byte(rawInput), &input); err != nil {
		return nil, fmt.Errorf("任务输入解析失败：%w", err)
	}
	meta, _ := generation.CallMetaFromContext(ctx)
	if strings.TrimSpace(input.Prompt) == "" {
		input.Prompt = fallbackPrompt
	}
	if input.Mode == "" && strings.HasPrefix(meta.TaskType, "video_") {
		input.Mode = "video"
	}
	if input.Mode == "text" && strings.HasPrefix(meta.TaskType, "canvas_text") && input.AgentRequests == nil {
		if ports.AppendTextDelta == nil {
			return nil, errors.New("文本增量持久化端口未接入")
		}
		publisher := &TextStreamPublisher{UserID: meta.UserID, TaskID: meta.TaskID, Append: ports.AppendTextDelta, LogWarn: ports.LogWarning}
		input.OnTextDelta = publisher.Publish
		defer publisher.Close()
	}
	return generation.Execute(ctx, input)
}
