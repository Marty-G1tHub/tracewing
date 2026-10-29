package srv

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/ping"
)

type fakeHop struct{}

func (fakeHop) Name() string { return "fake" }
func (fakeHop) Ping(_ context.Context, dst net.IP, ttl int, _ time.Duration) ping.Reply {
	if ttl >= 3 {
		return ping.Reply{Kind: ping.Echo, RTT: time.Millisecond, From: dst}
	}
	return ping.Reply{Kind: ping.Expired, RTT: time.Millisecond, From: net.IPv4(59, 43, 0, byte(ttl))}
}

func startRoute(t *testing.T, s *Server) (string, context.CancelFunc) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Serve(ctx, ln)
	time.Sleep(80 * time.Millisecond)
	return "http://" + ln.Addr().String(), cancel
}

func TestRouteEndpoints(t *testing.T) {
	s := &Server{Token: "unit-test-token", Pinger: fakeHop{}}
	base, stop := startRoute(t, s)
	defer stop()
	c := &Client{Base: base, Token: "unit-test-token", HTTP: http.DefaultClient}
	if _, err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	ri, err := c.RouteInfo(context.Background())
	if err != nil || !ri.Capable || len(ri.Targets) != 12 {
		t.Fatalf("info: %+v %v", ri, err)
	}
	tr, err := c.RouteTrace(context.Background(), "ct-bj")
	if err != nil || !tr.Reached || len(tr.Hops) != 3 || tr.ID != "ct-bj" {
		t.Fatalf("trace: %+v %v", tr, err)
	}
	// 未知目标 / 任意 IP / 额外参数 / 内网来源 全部拒绝
	for _, q := range []string{"target=8.8.8.8", "target=", "target=ct-bj&x=1", "target=client", "ip=1.1.1.1"} {
		err := c.getJSON(context.Background(), "/v1/route/trace?"+q, new(any))
		if err == nil || !strings.Contains(err.Error(), "400") {
			t.Errorf("%q 应返回 400，得到 %v", q, err)
		}
	}
	// 未认证
	resp, _ := http.Get(base + "/v1/route/trace?target=ct-bj")
	if resp.StatusCode != 401 {
		t.Errorf("未认证应 401，得到 %d", resp.StatusCode)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func TestRouteDisabledAndNoCap(t *testing.T) {
	for name, s := range map[string]*Server{
		"no-route": {Token: "unit-test-token", NoRoute: true},
		"tcp-only": {Token: "unit-test-token", Pinger: ping.TCP{Port: 80}},
	} {
		base, stop := startRoute(t, s)
		c := &Client{Base: base, Token: "unit-test-token", HTTP: http.DefaultClient}
		if _, err := c.Login(context.Background()); err != nil {
			t.Fatal(err)
		}
		ri, err := c.RouteInfo(context.Background())
		if err != nil || ri.Capable || ri.Note == "" {
			t.Errorf("%s: info %+v %v", name, ri, err)
		}
		if err := c.getJSON(context.Background(), "/v1/route/trace?target=ct-bj", new(any)); err == nil || !strings.Contains(err.Error(), "501") {
			t.Errorf("%s: 应 501，得到 %v", name, err)
		}
		stop()
	}
}

func TestRouteGate(t *testing.T) {
	g := newGate()
	now := time.Now()
	var rel []func()
	for i := 0; i < routeParallel; i++ {
		r, why := g.take(now)
		if r == nil {
			t.Fatal(why)
		}
		rel = append(rel, r)
	}
	if r, _ := g.take(now); r != nil {
		t.Fatal("并发上限应生效")
	}
	for _, r := range rel {
		r()
	}
	// 令牌桶：burst 次之后耗尽，过一段时间后补充
	n := 0
	for i := 0; i < 100; i++ {
		if r, _ := g.take(now); r != nil {
			r()
			n++
		}
	}
	if n >= routeBurst || n < routeBurst-routeParallel-1 {
		t.Fatalf("令牌桶放行数不合理：%d", n)
	}
	if r, _ := g.take(now.Add(10 * time.Second)); r == nil {
		t.Fatal("10 秒后应补充出令牌")
	}
}
