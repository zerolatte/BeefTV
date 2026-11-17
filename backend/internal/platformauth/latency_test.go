//go:build platform

package platformauth

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// 延迟只记录实测分布，不把开发机性能写成固定验收阈值。
func TestAuthenticationLatency(t *testing.T) {
	s := testService(t)
	account := bootstrapForTest(t, s)
	var grant *SessionGrant
	loginTimes := make([]time.Duration, 0, 5)
	for range 5 {
		started := time.Now()
		var err error
		grant, err = s.Login(t.Context(), account.Username, testPassword, "127.0.0.1")
		loginTimes = append(loginTimes, time.Since(started))
		if err != nil || grant == nil || grant.Account.ID != account.ID {
			t.Fatalf("登录行为异常：%v", err)
		}
	}
	logLatency(t, "Argon2id 登录", loginTimes)
	for _, check := range []struct {
		name string
		run  func() (*AuthenticatedSession, error)
	}{
		{"业务会话校验（更新活动时间）", func() (*AuthenticatedSession, error) { return s.Authenticate(t.Context(), grant.Token) }},
		{"后台会话复核（不延长空闲期）", func() (*AuthenticatedSession, error) { return s.CheckSession(t.Context(), grant.Token) }},
	} {
		t.Run(check.name, func(t *testing.T) {
			const requests = 20
			durations := make(chan time.Duration, requests)
			var group sync.WaitGroup
			started := time.Now()
			for range requests {
				group.Go(func() {
					begin := time.Now()
					got, err := check.run()
					durations <- time.Since(begin)
					if err != nil || got == nil || got.Account.ID != account.ID || got.Account.WorkspaceID != account.WorkspaceID {
						t.Errorf("并发会话校验未保持原身份：%v", err)
					}
				})
			}
			group.Wait()
			close(durations)
			measured := make([]time.Duration, 0, requests)
			for elapsed := range durations {
				measured = append(measured, elapsed)
			}
			t.Logf("%d 个并发请求总耗时=%s", requests, time.Since(started))
			logLatency(t, check.name, measured)
		})
	}
}

func logLatency(t *testing.T, name string, samples []time.Duration) {
	t.Helper()
	slices.Sort(samples)
	t.Logf("%s：样本=%d，P50=%s，P95=%s，最大=%s", name, len(samples), samples[(len(samples)-1)/2], samples[(len(samples)*95+99)/100-1], samples[len(samples)-1])
}
