package engine

import (
	"context"
	"net"
	"testing"

	"github.com/Marty-G1tHub/tracewing/internal/judge"
	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/netx"
	"github.com/Marty-G1tHub/tracewing/internal/srv"
)

func TestRunVPSServerLoopback(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	s := &srv.Server{Token: "tok"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Serve(ctx, ln)

	p := &netx.Path{Mode: netx.ModeDirect}
	_ = p.Normalize()
	in := VPSIn{
		Params: model.VPSParams{Source: "server", Profile: "quick", DurationS: 3, Rounds: 1, Streams: 4, EchoCount: 5, EchoMs: 10, UDPMbps: 10},
		Base:   ln.Addr().String(), Token: "tok", Th: judge.DefaultThresholds,
	}
	r := RunVPS(ctx, p, in, nil, nil, nil)
	if r.Status != "PASS" || len(r.Tput) != 4 {
		t.Fatalf("status=%s tput=%+v notes=%+v", r.Status, r.Tput, r.Notes)
	}
	for _, x := range r.Tput {
		if x.Median == nil || *x.Median < 100 {
			t.Fatalf("%s: %+v", x.Key, x)
		}
	}
	if r.Echo == nil || r.Echo.Recv != 5 || r.TCPConnect == nil || len(r.UDP) != 2 || r.Resources == nil || !r.Resources.Available {
		t.Fatalf("%+v", r)
	}
	if r.ServerInfo == nil || r.Server == "" || r.Diagnosis.Code == "" {
		t.Fatalf("%+v", r.Diagnosis)
	}
	t.Logf("diag=%s conf=%s ev=%v lim=%v", r.Diagnosis.Code, r.Diagnosis.Confidence, r.Diagnosis.Evidence, r.Diagnosis.Limitations)

	// 错误令牌 → FAILED 且信息清楚
	in.Token = "bad"
	r = RunVPS(ctx, p, in, nil, nil, nil)
	if r.Status != "FAILED" || len(r.Notes) == 0 {
		t.Fatalf("%+v", r)
	}
	t.Log(r.Notes[0].Text)
}

func TestRunVPSURLNoUpload(t *testing.T) {
	srv := throttledServer(200 << 20)
	defer srv.Close()
	p := &netx.Path{Mode: netx.ModeDirect}
	_ = p.Normalize()
	in := VPSIn{Params: model.VPSParams{Source: "url", DownURL: srv.URL + "/down?bytes={bytes}", DurationS: 3, Rounds: 1, Streams: 4, EchoCount: 3, EchoMs: 10}, Th: judge.DefaultThresholds}
	r := RunVPS(context.Background(), p, in, nil, nil, nil)
	if r.Status != "PARTIAL" || r.Echo != nil || r.TCPConnect == nil {
		t.Fatalf("%s %+v", r.Status, r)
	}
	if r.Tput[0].Error == "" || r.Tput[1].Median == nil {
		t.Fatalf("%+v", r.Tput)
	}
}
