package task

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"infinite-canvas/backend/internal/generation"
)

type canvasExecutionTestPorts struct{}

func (canvasExecutionTestPorts) Intercept(*http.Request) (bool, []byte, string, error) {
	return false, nil, "", nil
}
func (canvasExecutionTestPorts) Observe(generation.TransportObservation)   {}
func (canvasExecutionTestPorts) NotifyPoll(context.Context, string, error) {}
func (canvasExecutionTestPorts) SyncProgress(string, []byte)               {}
func (canvasExecutionTestPorts) GeneratedFileBytes(context.Context) (int64, error) {
	return 1 << 20, nil
}
func (canvasExecutionTestPorts) ResourceUploadBytes(context.Context) (int64, error) {
	return 1 << 20, nil
}
func (canvasExecutionTestPorts) CircuitOpen(context.Context, string) (bool, error) {
	return false, nil
}
func (canvasExecutionTestPorts) AcquireChannelSlot(context.Context, string, string, time.Duration) (func(), int, error) {
	return func() {}, 1, nil
}
func (canvasExecutionTestPorts) RecordChannelResult(context.Context, string, bool) error {
	return nil
}

func TestCanvasGenerationArchivesServerTextWithWorkerIdentity(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	for _, prompt := range []string{"", "明确输入"} {
		t.Run(prompt, func(t *testing.T) {
			var calls atomic.Int32
			var postedPrompt string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var payload struct {
					Stream   bool `json:"stream"`
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || !payload.Stream || len(payload.Messages) == 0 {
					t.Errorf("请求未进入现有文本流协议：%+v, %v", payload, err)
				}
				if len(payload.Messages) > 0 {
					postedPrompt = payload.Messages[len(payload.Messages)-1].Content
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"内部分析\",\"content\":\"可见答案\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			input, err := json.Marshal(generation.Input{Mode: "text", Prompt: prompt, Config: generation.Config{
				BaseURL: server.URL, APIKey: "test-key", Model: "text-model", InterfaceType: "chat-completion",
			}})
			if err != nil {
				t.Fatal(err)
			}
			ports := canvasExecutionTestPorts{}
			ctx := generation.WithRuntime(context.Background(), generation.Runtime{
				Images: ports, Receipts: ports, Limits: ports,
				Call: generation.CallMeta{UserID: "workspace-a", TaskID: "task-a", TaskType: "canvas_text", RequestKind: "create"},
			})
			ctx = generation.WithProtocolRegistry(ctx, generation.LoadOfficialFallbackRegistry())
			var mu sync.Mutex
			var archived strings.Builder
			result, err := ExecuteCanvasGenerationTask(ctx, "默认提示词", string(input), CanvasGenerationPorts{
				AppendTextDelta: func(userID, taskID, delta string) error {
					if userID != "workspace-a" || taskID != "task-a" {
						t.Errorf("文本归属被覆盖：%s %s", userID, taskID)
					}
					mu.Lock()
					defer mu.Unlock()
					_, _ = archived.WriteString(delta)
					return nil
				},
			})
			if err != nil || result["text"] != "可见答案" || calls.Load() != 1 {
				t.Fatalf("执行结果=%#v 调用数=%d 错误=%v", result, calls.Load(), err)
			}
			mu.Lock()
			defer mu.Unlock()
			if archived.String() != "可见答案" {
				t.Fatalf("返回前未刷新文本归档或泄漏思考内容：%q", archived.String())
			}
			want := prompt
			if want == "" {
				want = "默认提示词"
			}
			if postedPrompt != want {
				t.Fatalf("上游提示词=%q，期望=%q", postedPrompt, want)
			}
		})
	}
}

func TestCanvasGenerationRejectsMalformedInputAndMissingArchive(t *testing.T) {
	ctx := generation.WithRuntime(context.Background(), generation.Runtime{Call: generation.CallMeta{UserID: "workspace-a", TaskID: "task-a", TaskType: "canvas_text"}})
	for _, tc := range []struct{ input, reason string }{
		{`{`, "任务输入解析失败"},
		{`{"mode":"text"}`, "文本增量持久化端口未接入"},
	} {
		_, err := ExecuteCanvasGenerationTask(ctx, "提示词", tc.input, CanvasGenerationPorts{})
		if err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Fatalf("输入=%s 错误=%v", tc.input, err)
		}
	}
}
