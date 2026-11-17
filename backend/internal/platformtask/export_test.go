//go:build platform

package platformtask

import (
	"context"
	"net"
	"net/url"
	"testing"

	"infinite-canvas/backend/internal/taskruntime"
)

// EnableLoopbackForTest 只编入测试二进制，正式平台没有启用真实生成的开关。
func EnableLoopbackForTest(t *testing.T, s *Service, endpoint string) {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		t.Fatal("测试上游必须为回环地址")
	}
	s.testUpstream = endpoint
}

func ProcessOneForTest(ctx context.Context, s *Service) taskruntime.Outcome {
	return s.runtime.ProcessOne(ctx)
}
