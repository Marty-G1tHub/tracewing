package srv

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func start(t *testing.T, token string) (*Server, string, context.CancelFunc) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Token: token}
	ctx, cancel := context.WithCancel(context.Background())
	go s.Serve(ctx, ln)
	time.Sleep(100 * time.Millisecond)
	return s, "http://" + ln.Addr().String(), cancel
}

func TestAuth(t *testing.T) {
	n := NewNonce("tok", time.Now())
	if !verifyNonce("tok", n, time.Now()) || verifyNonce("other", n, time.Now()) {
		t.Fatal("nonce")
	}
	if verifyNonce("tok", n, time.Now().Add(2*time.Hour)) {
		t.Fatal("expired nonce accepted")
	}
	r, _ := http.NewRequest("GET", "http://x", nil)
	r.Header.Set("Authorization", AuthHeader("tok", n))
	if !Check("tok", r, time.Now()) || Check("bad", r, time.Now()) {
		t.Fatal("check")
	}
	r.Header.Set("Authorization", "TW "+n+" 00")
	if Check("tok", r, time.Now()) {
		t.Fatal("bad proof accepted")
	}
}

func TestServerClient(t *testing.T) {
	_, base, cancel := start(t, "secret")
	defer cancel()
	ctx := context.Background()

	// 错误令牌
	bad := &Client{Base: base, Token: "nope", HTTP: http.DefaultClient}
	if _, err := bad.Login(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Info(ctx); err == nil || !strings.Contains(err.Error(), "令牌") {
		t.Fatalf("want token error, got %v", err)
	}
	// 未带头
	resp, _ := http.Get(base + "/v1/down?bytes=10")
	if resp.StatusCode != 401 {
		t.Fatal(resp.StatusCode)
	}

	c := &Client{Base: base, Token: "secret", HTTP: http.DefaultClient}
	if _, err := c.Login(ctx); err != nil {
		t.Fatal(err)
	}
	info, err := c.Info(ctx)
	if err != nil || info.SeenAs != "127.0.0.1" || info.Version != Version {
		t.Fatalf("%+v %v", info, err)
	}
	e := c.EchoRTT(ctx, 10, time.Millisecond, nil)
	if e.Recv != 10 || e.P50 == nil {
		t.Fatalf("%+v", e)
	}
	tc := c.TCPConnect(ctx, 5, time.Millisecond)
	if tc.Recv != 5 {
		t.Fatalf("%+v", tc)
	}
	// 下载字节数
	req, _ := http.NewRequest("GET", strings.ReplaceAll(c.DownURL(), "{bytes}", "3000000"), nil)
	r, err := c.Authed().Do(req)
	if err != nil || r.ContentLength != 3000000 {
		t.Fatal(err, r)
	}
	r.Body.Close()
}

func TestResources(t *testing.T) {
	r := SampleResources(context.Background(), 600*time.Millisecond)
	if r.Seconds < 0.5 {
		t.Fatalf("%+v", r)
	}
	t.Logf("%+v", r)
}

func TestUDP(t *testing.T) {
	_, base, cancel := start(t, "secret")
	defer cancel()
	c := &Client{Base: base, Token: "secret", HTTP: http.DefaultClient}
	ctx := context.Background()
	if _, err := c.Login(ctx); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"up", "down"} {
		r := c.UDP(ctx, dir, 20, 2)
		if r.Err != "" {
			t.Fatalf("%s: %s", dir, r.Err)
		}
		// 回环上基本不丢包；目标 20Mbps×2s ≈ 4166 包
		if r.Sent < 3500 || r.Recv < r.Sent*95/100 || r.Mbps < 17 || r.Mbps > 23 {
			t.Fatalf("%s: %+v", dir, r)
		}
		t.Logf("%s: %+v", dir, r)
	}
}

func TestUStat(t *testing.T) {
	u := NewUStat()
	now := time.Now()
	// seq 0,1,3,2 → 1 次乱序；固定传输时延 → 抖动 ≈ 0
	for i, seq := range []uint32{0, 1, 3, 2} {
		at := now.Add(time.Duration(i) * 10 * time.Millisecond)
		u.Add(seq, at.UnixNano()-5_000_000, 1000, at)
	}
	if u.Recv != 4 || u.Reordered != 1 || u.Jitter > 0.001 {
		t.Fatalf("%+v", u)
	}
	// 3 个包间隔 10ms*3 = 30ms，后三个包 3000B → 800 kbps
	if m := u.Mbps(); m < 0.79 || m > 0.81 {
		t.Fatal(m)
	}
}

func TestNormalizeBase(t *testing.T) {
	for in, want := range map[string]string{"1.2.3.4": "http://1.2.3.4:9527", "a.com:80": "http://a.com:80", "http://x.y:1": "http://x.y:1"} {
		got, err := NormalizeBase(in)
		if err != nil || got != want {
			t.Errorf("%s → %s %v", in, got, err)
		}
	}
	if _, err := NormalizeBase("  "); err == nil {
		t.Fatal("empty should fail")
	}
}
