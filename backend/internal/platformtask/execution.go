//go:build platform

package platformtask

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"infinite-canvas/backend/internal/generation"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/modelcatalog"
	"infinite-canvas/backend/internal/platformquota"
	"infinite-canvas/backend/internal/task"
	"infinite-canvas/backend/internal/taskruntime"
	"infinite-canvas/backend/internal/textreplay"
)

func (s *Service) Execute(session taskruntime.Session) taskruntime.Outcome {
	row := session.Task()
	terminal := taskruntime.NewTerminal(taskruntime.TerminalDependencies{
		Repository: s.repo, Replay: terminalReplay{s.replay}, Logger: s, Outputs: textOutputs{},
		UserFacingMessage: func(err error) string { return generation.ClassifyError(err).UserMessage() },
		FailureErrorCode:  func(err error) string { return string(generation.ClassifyError(err).Category) },
		CaptureFailure:    generation.CaptureTaskFailureDiagnostics,
	})
	if s.testUpstream == "" || row.Type != "canvas_text" {
		err := terminal.Refuse(row, "平台生成关闭", "当前任务类型未通过平台执行验收")
		return taskruntime.Outcome{Kind: taskruntime.KindRejected, Err: err, Applied: err == nil}
	}
	intent, err := s.quota.IntentForTask(session.Context(), row.UserID, row.ID)
	if err != nil {
		return taskruntime.Outcome{Kind: taskruntime.KindFailed, Err: err}
	}
	outcome := task.ExecuteClaimedTask(task.WorkerExecuteDeps{
		Repo: s.repo, Terminal: terminal, Log: s.Log,
		BeginRouteAttempt: func(row *model.Task) (*model.RouteAttempt, error) { return s.beginAttempt(row, intent) },
		ExecuteRoute: func(ctx context.Context, row *model.Task, attempt *model.RouteAttempt) (task.RouteExecutionResult, error) {
			return s.executeRoute(ctx, row, attempt, intent)
		},
		PersistGeneratedMedia: func(_ model.Task, result map[string]any) (map[string]any, error) { return result, nil },
		FinalizeTurnaround:    func(model.Task, map[string]any) (bool, error) { return false, nil },
		DecryptTaskInput:      func(raw string) (string, error) { return raw, nil },
		ShouldDeferImage:      func(model.Task, error, bool) bool { return false },
		ResolveProviderConfig: func(generation.Config) (generation.Config, error) { return generation.Config{}, unavailable() },
		SaveTaskCompletion:    s.saveCompletion,
	}, session)
	if outcome.Applied && outcome.Kind != taskruntime.KindCompleted {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(session.Context()), 10*time.Second)
		defer cancel()
		// 准备阶段终止只释放仍未提交的意图，未知或已受理的意图保持预留。
		outcome.Err = errors.Join(outcome.Err, s.quota.CancelUnsubmitted(ctx, row.UserID, intent.ID))
	}
	return outcome
}

func (s *Service) beginAttempt(row *model.Task, intent *model.PlatformExecutionIntent) (*model.RouteAttempt, error) {
	if row.ResultJSON != "" {
		return nil, nil
	}
	if intent.State != platformquota.StateReserved {
		return nil, modelcatalog.DispatchUncertainError{Message: "原执行意图已越过提交边界，停止自动重复提交"}
	}
	store := modelcatalog.NewRepoRouteStore(s.repo)
	attempts, err := store.RouteAttempts(row.ID, row.RouteRun)
	if err != nil {
		return nil, err
	}
	if len(attempts) > 0 {
		// 意图仍为 reserved 证明真正发送围栏尚未通过，可复用崩溃前的路由记录。
		return &attempts[len(attempts)-1], nil
	}
	channelModel, err := store.ChannelModel(row.ChannelModelID)
	if err != nil {
		return nil, err
	}
	routed := &modelcatalog.RoutedModel{
		LogicalModel: model.LogicalModel{ID: row.LogicalModelID}, Revision: model.LogicalModelRevision{ID: row.LogicalModelRevisionID},
		Route: model.LogicalModelRoute{ID: row.RouteID}, ChannelModel: *channelModel,
	}
	return modelcatalog.NewRouter(nil, nil, nil, 0, 0).CreateRouteAttempt(store, row, routed, 1)
}

func (s *Service) executeRoute(ctx context.Context, row *model.Task, attempt *model.RouteAttempt, intent *model.PlatformExecutionIntent) (task.RouteExecutionResult, error) {
	if row.ResultJSON != "" {
		var result map[string]any
		if err := json.Unmarshal([]byte(row.ResultJSON), &result); err != nil {
			return task.RouteExecutionResult{}, err
		}
		if err := s.quota.Observe(ctx, row.UserID, intent.ID, platformquota.FactSucceeded); err != nil {
			return task.RouteExecutionResult{}, err
		}
		return task.RouteExecutionResult{Result: result, ProviderSucceeded: true}, nil
	}
	store := modelcatalog.NewRepoRouteStore(s.repo)
	return task.ExecuteRouteAttemptLoop(ctx, task.RouteExecuteDeps{
		MarkDispatching: func(attempt *model.RouteAttempt) error {
			return modelcatalog.MarkRouteAttemptDispatching(store, attempt)
		},
		Process: func(ctx context.Context, value model.Task) (map[string]any, []map[string]any, error) {
			result, err := s.generateText(ctx, &value, intent)
			return result, nil, err
		},
		RefreshState: func(*model.Task) error { return nil },
		FinishAttempt: func(attempt *model.RouteAttempt, row *model.Task, err error) {
			message := ""
			if err != nil {
				message = generation.ClassifyError(err).UserMessage()
			}
			modelcatalog.FinishRouteAttempt(store, attempt, row, err, message, modelcatalog.FailureInfo{}, modelcatalog.ImageSubmissionView{}, time.Now)
		},
		NextAfterFailure: func(*model.Task, *model.RouteAttempt, error) (*model.RouteAttempt, error) { return nil, nil },
		WriteLog:         s.Log,
	}, row, attempt)
}

func (s *Service) generateText(ctx context.Context, row *model.Task, intent *model.PlatformExecutionIntent) (map[string]any, error) {
	ports := &executionPorts{service: s, ctx: ctx, row: row, intent: intent}
	ctx = generation.WithRuntime(ctx, generation.Runtime{
		Config: ports, Limits: ports, Receipts: ports, Images: ports, DispatchAdmission: ports,
		Call: generation.CallMeta{UserID: row.UserID, TaskID: row.ID, ProjectID: row.ProjectID, TaskType: row.Type, Operation: row.Operation, ChannelID: intent.ChannelID, Model: row.Model, Capability: "text", RequestKind: "create"},
	})
	ctx = generation.WithProtocolRegistry(ctx, generation.LoadOfficialFallbackRegistry())
	result, generationErr := task.ExecuteCanvasGenerationTask(ctx, row.Prompt, row.InputJSON, task.CanvasGenerationPorts{
		AppendTextDelta: func(workspace, id, delta string) error { _, err := s.replay.Append(workspace, id, delta); return err },
		LogWarning:      func(workspace, id, message, payload string) { _ = s.Log(workspace, id, "warn", message, payload) },
	})
	// 请求 context 取消不能撤销已发生的上游事实；结算单独使用有界窗口。
	factCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if generationErr != nil {
		current, err := s.quota.IntentForTask(factCtx, row.UserID, row.ID)
		if err != nil {
			return nil, errors.Join(generationErr, err)
		}
		if current.State == platformquota.StateReserved {
			return nil, errors.Join(generationErr, s.quota.CancelUnsubmitted(factCtx, row.UserID, intent.ID))
		}
		if current.State == platformquota.StateReleased {
			return nil, generationErr
		}
		var notSent generation.RequestNotSentError
		if errors.As(generationErr, &notSent) {
			return nil, errors.Join(generationErr, s.quota.Observe(factCtx, row.UserID, intent.ID, platformquota.FactNotAccepted))
		}
		observed := s.quota.Observe(factCtx, row.UserID, intent.ID, platformquota.FactUnknown)
		return nil, generation.SubmissionUnknownError{Cause: errors.Join(generationErr, observed)}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	cacheErr := s.repo.CachePlatformTextResult(factCtx, row, string(encoded))
	// 即使结果缓存失败，上游成功也必须结算，不能作为无成本失败释放。
	settleErr := s.quota.Observe(factCtx, row.UserID, intent.ID, platformquota.FactSucceeded)
	if cacheErr != nil || settleErr != nil {
		return nil, errors.Join(cacheErr, settleErr)
	}
	return result, nil
}

func (s *Service) saveCompletion(row *model.Task, result, _ []byte, _ bool) error {
	row.ResultJSON = string(result)
	row.Status, row.Stage, row.Progress = model.TaskStatusSucceeded, "生成完成", 100
	now := time.Now()
	row.CompletedAt = &now
	return s.repo.SaveTaskCompletion(row, model.TaskStatusRunning, nil)
}

type terminalReplay struct{ *textreplay.Service }

func (r terminalReplay) FinalizeTaskTextReplay(id string, status model.TaskStatus) error {
	return r.Finalize(id, status)
}

// 这个窄执行器只接收不含节点/项目产物绑定元数据的文本请求，结果由任务读取与 SSE 交付。
type textOutputs struct{}

func (textOutputs) DeliverSucceededTask(model.Task) error       { return nil }
func (textOutputs) RegisterTaskOutputFromTask(model.Task) error { return nil }
