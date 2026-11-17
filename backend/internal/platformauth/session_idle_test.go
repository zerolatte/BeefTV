//go:build platform

package platformauth

import (
	"testing"
	"time"
)

func TestSessionChecksDoNotExtendIdleExpiry(t *testing.T) {
	s := testService(t)
	clock := time.Now().UTC()
	s.now = func() time.Time { return clock }
	bootstrapForTest(t, s)
	grant, err := s.Login(t.Context(), "manager", testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(119 * time.Minute)
	if _, err := s.CheckSession(t.Context(), grant.Token); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)
	if _, err := s.CheckSession(t.Context(), grant.Token); err == nil {
		t.Fatal("后台轮询不应延长闲置会话")
	}
	grant, err = s.Login(t.Context(), "manager", testPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(119 * time.Minute)
	if _, err := s.Authenticate(t.Context(), grant.Token); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Minute)
	if _, err := s.CheckSession(t.Context(), grant.Token); err != nil {
		t.Fatalf("真实业务访问应延长闲置期限：%v", err)
	}
}
