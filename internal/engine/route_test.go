package engine

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/netx"
	"github.com/Marty-G1tHub/tracewing/internal/ping"
	"github.com/Marty-G1tHub/tracewing/internal/srv"
)

// 假的“服务器侧”探测：每条回程都是 国外 → 59.43（CN2）→ 目标。
type cn2Pinger struct{}

func (cn2Pinger) Name() string { return "fake" }
func (cn2Pinger) Ping(_ context.Context, dst net.IP, ttl int, _ time.Duration) ping.Reply {
	switch ttl {
	case 1:
		return ping.Reply{Kind: ping.Expired, RTT: time.Millisecond, From: net.IPv4(4, 69, 1, 1)}
	case 2:
		return ping.Reply{Kind: ping.Expired, RTT: time.Millisecond, From: net.IPv4(59, 43, 1, 1)}
	}
	return ping.Reply{Kind: ping.Echo, RTT: time.Millisecond, From: dst}
}

func TestRunRouteEndToEnd(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &srv.Server{Token: "unit-test-token", Pinger: cn2Pinger{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx, ln)
	time.Sleep(80 * time.Millisecond)

	p := &netx.Path{}
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	r := RunRoute(ctx, p, RouteIn{Base: ln.Addr().String(), Token: "unit-test-token"}, &model.Env{}, nil, nil)
	if len(r.Return) != 12 {
		t.Fatalf("应有 12 条回程，得到 %d；notes=%v", len(r.Return), r.Notes)
	}
	for _, l := range r.Return {
		if l.Err != "" || l.Verdict == nil || l.Verdict.Code != "cn2_gia" {
			t.Errorf("%s: %+v", l.ID, l)
		}
	}
	if len(r.Headlines) < 3 {
		t.Errorf("headlines: %v", r.Headlines)
	}
	// 回环地址不是公网：去程应给出原因而不是崩溃；来源 IP 回程同理
	if r.Outbound == nil || r.Outbound.Err == "" {
		t.Error("去程应说明不是公网地址")
	}
	if r.Client == nil || r.Client.Err == "" {
		t.Error("来源为回环地址时 client 回程应被拒绝")
	}
	// 令牌错误
	r = RunRoute(ctx, p, RouteIn{Base: ln.Addr().String(), Token: "wrong-token-1"}, &model.Env{}, nil, nil)
	if len(r.Return) != 0 || len(r.Notes) == 0 {
		t.Error("错误令牌不应得到回程")
	}
	// 没有令牌：只测去程
	r = RunRoute(ctx, p, RouteIn{Base: ln.Addr().String()}, &model.Env{}, nil, nil)
	if r.Params.Return || len(r.Return) != 0 {
		t.Error("无令牌应只测去程")
	}
}
