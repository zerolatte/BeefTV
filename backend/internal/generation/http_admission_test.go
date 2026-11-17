package generation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type dispatchLimits struct {
	stubLimits
	circuit  bool
	slotErr  error
	onSlot   func()
	releases int
}

type dispatchAdmissionFunc func(context.Context, CallMeta) error

func (f dispatchAdmissionFunc) BeforeDispatch(ctx context.Context, call CallMeta) error {
	return f(ctx, call)
}

func (l *dispatchLimits) CircuitOpen(context.Context, string) (bool, error) { return l.circuit, nil }
func (l *dispatchLimits) AcquireChannelSlot(context.Context, string, string, time.Duration) (func(), int, error) {
	if l.onSlot != nil {
		l.onSlot()
	}
	if l.slotErr != nil {
		return nil, 1, l.slotErr
	}
	return func() { l.releases++ }, 1, nil
}

func TestTransportDispatchAdmissionAfterPreflight(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	for _, stage := range []string{"circuit", "slot", "cancel-after-slot", "url", "fence", "sent"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			limits := &dispatchLimits{}
			url := server.URL
			switch stage {
			case "circuit":
				limits.circuit = true
			case "slot":
				limits.slotErr = errors.New("slot unavailable")
			case "cancel-after-slot":
				limits.onSlot = cancel
			case "url":
				url = "file:///denied"
			}
			receipts := &recordingReceipts{}
			ctx = WithRuntime(ctx, Runtime{Limits: limits, Receipts: receipts})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
			if err != nil {
				t.Fatal(err)
			}
			admissions := 0
			before := hits.Load()
			_, _, err = DoBinaryWithDispatchAdmission(req, func() error {
				admissions++
				if stage == "fence" {
					return errors.New("stale owner")
				}
				return nil
			})
			wantAdmissions, wantHits := 0, int32(0)
			if stage == "fence" || stage == "sent" {
				wantAdmissions = 1
			}
			if stage == "sent" {
				wantHits = 1
			}
			if admissions != wantAdmissions || hits.Load()-before != wantHits || (err == nil) != (stage == "sent") {
				t.Fatalf("admissions=%d hits=%d err=%v", admissions, hits.Load()-before, err)
			}
			observed := receipts.snapshot()
			if len(observed) != 1 || observed[0].Dispatched != (stage == "sent") {
				t.Fatalf("receipt: %#v", observed)
			}
			if stage != "circuit" && stage != "slot" && limits.releases != 1 {
				t.Fatal("channel slot leaked")
			}
		})
	}
}

func TestRuntimeDispatchAdmissionRejectsBeforeHTTP(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	for _, kind := range []string{"create", "poll", "download", ""} {
		t.Run(kind, func(t *testing.T) {
			admissions := 0
			rejected := errors.New("执行意图已经提交或账号授权失效")
			receipts := &recordingReceipts{}
			ctx := WithRuntime(context.Background(), Runtime{
				Call: CallMeta{UserID: "workspace-a", TaskID: "task-a", RequestKind: kind}, Receipts: receipts,
				DispatchAdmission: dispatchAdmissionFunc(func(ctx context.Context, call CallMeta) error {
					admissions++
					if call.UserID != "workspace-a" || call.TaskID != "task-a" {
						t.Errorf("发送校验丢失服务端身份：%+v", call)
					}
					return rejected
				}),
			})
			method := http.MethodPost
			if kind == "download" {
				method = http.MethodGet
			}
			req, err := http.NewRequestWithContext(ctx, method, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			before := hits.Load()
			_, _, err = DoBinaryWithConsumer(req, func(string, []byte) {})
			allowed := kind == "poll" || kind == "download"
			if allowed {
				if err != nil || hits.Load()-before != 1 || admissions != 0 {
					t.Fatalf("查询或取回重复进入收费准入：调用=%d 准入=%d 错误=%v", hits.Load()-before, admissions, err)
				}
			} else if err == nil || hits.Load() != before || (kind == "create" && (admissions != 1 || !errors.Is(err, rejected))) {
				t.Fatalf("拒绝后仍发出上游请求：调用=%d 准入=%d 错误=%v", hits.Load()-before, admissions, err)
			}
			observed := receipts.snapshot()
			if len(observed) != 1 || observed[0].Dispatched != allowed {
				t.Fatalf("请求事实不准确：%+v", observed)
			}
		})
	}
}

func TestRuntimeDispatchAdmissionPreflightAndRedirect(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/create" {
			http.Redirect(w, r, "/second-create", http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	for _, circuit := range []bool{true, false} {
		admissions := 0
		ctx := WithRuntime(context.Background(), Runtime{
			Call:              CallMeta{UserID: "workspace-a", TaskID: "task-a", RequestKind: "create"},
			Limits:            &dispatchLimits{circuit: circuit},
			DispatchAdmission: dispatchAdmissionFunc(func(context.Context, CallMeta) error { admissions++; return nil }),
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/create", nil)
		if err != nil {
			t.Fatal(err)
		}
		before := hits.Load()
		_, _, err = DoBinary(req)
		want := 1
		if circuit {
			want = 0
		}
		if err == nil || admissions != want || int(hits.Load()-before) != want {
			t.Fatalf("熔断=%v 准入=%d 发送=%d 错误=%v", circuit, admissions, hits.Load()-before, err)
		}
		cleared, _ := RuntimeFromContext(WithoutCallAccounting(ctx))
		if cleared.DispatchAdmission != nil {
			t.Fatal("控制面请求继承了生成执行意图")
		}
	}
}
