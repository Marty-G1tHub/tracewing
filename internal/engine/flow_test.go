package engine

import (
	"context"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 以固定速率输出的 Reader：每 10ms 放行 rate/100 字节。
type slowReader struct {
	rate  int64
	left  int64
	last  time.Time
	quota int64
}

func (s *slowReader) Read(p []byte) (int, error) {
	if s.left <= 0 {
		return 0, io.EOF
	}
	for s.quota <= 0 {
		time.Sleep(10 * time.Millisecond)
		s.quota += s.rate / 100
	}
	n := int64(len(p))
	if n > s.quota {
		n = s.quota
	}
	if n > s.left {
		n = s.left
	}
	s.quota -= n
	s.left -= n
	return int(n), nil
}

func throttledServer(rate int64) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
		n := int64(8 << 20)
		if b := r.URL.Query().Get("bytes"); b != "" {
			n, _ = strconv.ParseInt(b, 10, 64)
		}
		w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
		io.Copy(w, &slowReader{rate: rate, left: n})
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		// 以同样速率消费
		io.Copy(io.Discard, &limitedBody{r: r.Body, rate: rate})
		w.WriteHeader(200)
	})
	mux.HandleFunc("/flaky", func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		c, _, _ := hj.Hijack()
		c.Close()
	})
	return httptest.NewServer(mux)
}

type limitedBody struct {
	r     io.Reader
	rate  int64
	quota int64
}

func (l *limitedBody) Read(p []byte) (int, error) {
	for l.quota <= 0 {
		time.Sleep(10 * time.Millisecond)
		l.quota += l.rate / 100
	}
	if int64(len(p)) > l.quota {
		p = p[:l.quota]
	}
	n, err := l.r.Read(p)
	l.quota -= int64(n)
	return n, err
}

func TestRunFlowDownRate(t *testing.T) {
	const perStream = 2 << 20 // 2 MiB/s
	srv := throttledServer(perStream)
	defer srv.Close()
	r := RunFlow(context.Background(), FlowSpec{
		Client: srv.Client(), Dir: "down", URL: srv.URL + "/down?bytes={bytes}",
		Streams: 4, Duration: 4 * time.Second, Warmup: time.Second, Chunk: 3 << 20,
	}, nil)
	if !r.OK {
		t.Fatalf("not ok: %+v", r.Err)
	}
	want := float64(4*perStream) * 8 / 1e6
	if math.Abs(r.Mbps-want)/want > 0.12 {
		t.Fatalf("mbps=%.2f want≈%.2f", r.Mbps, want)
	}
	if r.OKStreams != 4 || r.WindowSec < 2.9 || len(r.Series) == 0 {
		t.Fatalf("%+v", r)
	}
}

func TestRunFlowUpRate(t *testing.T) {
	const perStream = 1 << 20
	srv := throttledServer(perStream)
	defer srv.Close()
	// 本机回环的内核发送缓冲有几 MB，会把慢速上传“吸收”成突发；
	// 测试里把发送缓冲压小，才能验证计数本身的准确性。
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, n, a string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, n, a)
		if err == nil {
			c.(*net.TCPConn).SetWriteBuffer(32 << 10)
		}
		return c, err
	}
	r := RunFlow(context.Background(), FlowSpec{
		Client: &http.Client{Transport: tr}, Dir: "up", URL: srv.URL + "/up",
		Streams: 2, Duration: 4 * time.Second, Warmup: time.Second, Chunk: 2 << 20,
	}, nil)
	if !r.OK {
		t.Fatal(r.Err)
	}
	want := float64(2*perStream) * 8 / 1e6
	// 发送端计数会略高于接收端（socket 缓冲），允许 +35%/-12%
	if r.Mbps < want*0.88 || r.Mbps > want*1.35 {
		t.Fatalf("mbps=%.2f want≈%.2f", r.Mbps, want)
	}
}

func TestRunFlowPartialIsFailure(t *testing.T) {
	srv := throttledServer(1 << 20)
	defer srv.Close()
	// /flaky 总是断开连接：所有流失败，不能给出“低速结果”
	r := RunFlow(context.Background(), FlowSpec{
		Client: srv.Client(), Dir: "down", URL: srv.URL + "/flaky",
		Streams: 4, Duration: 3 * time.Second, Warmup: time.Second,
	}, nil)
	if r.OK || !strings.HasPrefix(r.Err, "parallel_incomplete ok=0/4") {
		t.Fatalf("%+v", r)
	}
}

func TestRunFlowCancel(t *testing.T) {
	srv := throttledServer(1 << 20)
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(500 * time.Millisecond); cancel() }()
	t0 := time.Now()
	r := RunFlow(ctx, FlowSpec{Client: srv.Client(), Dir: "down", URL: srv.URL + "/down", Streams: 2, Duration: 10 * time.Second, Warmup: time.Second}, nil)
	if r.OK || time.Since(t0) > 3*time.Second {
		t.Fatalf("cancel not honored: %v %v", r.Err, time.Since(t0))
	}
}

func TestProbe(t *testing.T) {
	srv := throttledServer(50 << 20)
	defer srv.Close()
	ok := Probe(context.Background(), srv.Client(), Endpoint{Dir: "down", URL: srv.URL + "/down?bytes={bytes}"})
	if !ok.OK || ok.Bytes < 1_000_000 {
		t.Fatalf("%+v", ok)
	}
	bad := Probe(context.Background(), srv.Client(), Endpoint{Dir: "down", URL: srv.URL + "/nothing"})
	if bad.OK {
		t.Fatal("404 should fail")
	}
	up := Probe(context.Background(), srv.Client(), Endpoint{Dir: "up", URL: srv.URL + "/up"})
	if !up.OK {
		t.Fatalf("%+v", up)
	}
}

func TestWindow(t *testing.T) {
	// 恒定 10 MB/s：每 0.1s 1MB
	var cum []Sample
	for i := 1; i <= 100; i++ {
		cum = append(cum, Sample{T: float64(i) / 10, B: int64(i) * 1_000_000})
	}
	mbps, win, _, ok := Window(cum, 2, 10)
	if !ok || math.Abs(mbps-80) > 0.01 || math.Abs(win-8) > 1e-9 {
		t.Fatalf("%v %v", mbps, win)
	}
	if _, _, _, ok := Window(cum[:3], 2, 10); ok {
		t.Fatal("window too short must be !ok")
	}
}
