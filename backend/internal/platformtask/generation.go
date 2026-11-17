//go:build platform

package platformtask

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"infinite-canvas/backend/internal/generation"
	"infinite-canvas/backend/internal/model"
)

type executionPorts struct {
	service *Service
	ctx     context.Context
	row     *model.Task
	intent  *model.PlatformExecutionIntent
}

func (p *executionPorts) Resolve(input generation.Config) (generation.Config, error) {
	if p.intent.TaskID != p.row.ID || p.intent.WorkspaceID != p.row.UserID || input.ChannelID != p.intent.ChannelID {
		return generation.Config{}, errors.New("任务冻结归属不一致")
	}
	if input.InterfaceType == "" || input.APIFormat == "" || input.Model == "" || input.ChannelModelKey == "" || input.CapabilityConfig == nil {
		return generation.Config{}, errors.New("任务冻结模型配置不完整")
	}
	secret, err := p.service.catalog.ResolveCredentialVersion(p.ctx, p.intent.ChannelID, p.intent.CredentialVersionID)
	if err != nil {
		return generation.Config{}, err
	}
	allowed, err := url.Parse(p.service.testUpstream)
	if err != nil || allowed.Scheme != "http" || net.ParseIP(allowed.Hostname()) == nil || !net.ParseIP(allowed.Hostname()).IsLoopback() || secret.BaseURL != p.service.testUpstream {
		return generation.Config{}, unavailable()
	}
	// 非敏感协议参数来自准入快照，渠道后续编辑不得覆盖在途任务。
	input.BaseURL, input.APIKey, input.SecretKey, input.Headers = secret.BaseURL, secret.APIKey, secret.SecretKey, secret.Headers
	return input, nil
}

func (p *executionPorts) BeforeDispatch(ctx context.Context, call generation.CallMeta) error {
	if call.TaskID != p.row.ID || call.UserID != p.row.UserID || call.RequestKind != "create" {
		return errors.New("任务发送身份不一致")
	}
	allowed, err := p.service.quota.BeginSubmission(ctx, p.row.UserID, p.intent.ID)
	if err != nil {
		return err
	}
	if !allowed {
		return generation.SubmissionUnknownError{Cause: errors.New("原执行意图不能再次提交，请核对原任务")}
	}
	return nil
}

func (*executionPorts) ApplyCapabilities(context.Context, *generation.Input) error { return nil }
func (*executionPorts) SyncArkPrivateAssets(context.Context, string, *generation.Input) error {
	return nil
}
func (*executionPorts) Intercept(*http.Request) (bool, []byte, string, error) {
	return false, nil, "", nil
}
func (*executionPorts) Observe(generation.TransportObservation)            {}
func (*executionPorts) NotifyPoll(context.Context, string, error)          {}
func (*executionPorts) SyncProgress(string, []byte)                        {}
func (*executionPorts) GeneratedFileBytes(context.Context) (int64, error)  { return 1 << 20, nil }
func (*executionPorts) ResourceUploadBytes(context.Context) (int64, error) { return 1 << 20, nil }
func (*executionPorts) CircuitOpen(context.Context, string) (bool, error)  { return false, nil }
func (*executionPorts) AcquireChannelSlot(context.Context, string, string, time.Duration) (func(), int, error) {
	return func() {}, 1, nil
}
func (*executionPorts) RecordChannelResult(context.Context, string, bool) error { return nil }

var _ generation.DispatchAdmissionPort = (*executionPorts)(nil)
