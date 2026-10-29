package srv

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/ping"
)

// Server 是自建测速服务端。
type Server struct {
	Token   string
	Pinger  ping.Pinger // 回程探测用；nil 则启动时自动选择
	NoRoute bool        // 关闭回程探测
	udp     *udpServer

	routeOnce sync.Once
	gate      *routeGate
}

var randBlk = func() []byte {
	b := make([]byte, 1<<20)
	_, _ = rand.Read(b)
	return b
}()

// Handler 返回 HTTP 处理器。UDP 部分由 ServeUDP 单独启动。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/challenge", func(w http.ResponseWriter, r *http.Request) {
		js(w, map[string]string{"nonce": NewNonce(s.Token, time.Now()), "version": Version})
	})
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !Check(s.Token, r, time.Now()) {
				time.Sleep(300 * time.Millisecond) // 拖慢猜测
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/v1/info", auth(s.info))
	mux.HandleFunc("/v1/down", auth(s.down))
	mux.HandleFunc("/v1/up", auth(s.up))
	mux.HandleFunc("/v1/echo", auth(func(w http.ResponseWriter, r *http.Request) {
		js(w, map[string]int64{"t": time.Now().UnixNano()})
	}))
	mux.HandleFunc("/v1/route/targets", auth(s.routeTargets))
	mux.HandleFunc("/v1/route/trace", auth(s.routeTrace))
	mux.HandleFunc("/v1/res", auth(s.res))
	mux.HandleFunc("/v1/udp/down", auth(s.udpDown))
	mux.HandleFunc("/v1/udp/stat", auth(s.udpStat))
	return mux
}

func js(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	js(w, model.ServerInfo{Version: Version, OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), SeenAs: host})
}

func (s *Server) down(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.ParseInt(r.URL.Query().Get("bytes"), 10, 64)
	if n <= 0 || n > 1<<34 {
		n = 32 << 20
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
	for n > 0 {
		k := int64(len(randBlk))
		if k > n {
			k = n
		}
		if _, err := w.Write(randBlk[:k]); err != nil {
			return
		}
		n -= k
	}
}

func (s *Server) up(w http.ResponseWriter, r *http.Request) {
	n, _ := io.Copy(io.Discard, r.Body)
	js(w, map[string]int64{"bytes": n})
}

func (s *Server) res(w http.ResponseWriter, r *http.Request) {
	sec, _ := strconv.ParseFloat(r.URL.Query().Get("sec"), 64)
	if sec <= 0 || sec > 600 {
		sec = 10
	}
	// 先发响应头：采样可能长达数十秒，不能让客户端的响应头超时先到
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	json.NewEncoder(w).Encode(SampleResources(r.Context(), time.Duration(sec*float64(time.Second))))
}

// Run 启动 HTTP+UDP 并阻塞到 ctx 结束。
func (s *Server) Run(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if pc, err := net.ListenPacket("udp", ln.Addr().String()); err == nil {
		s.udp = newUDPServer(pc.(*net.UDPConn))
		go s.udp.loop()
		defer pc.Close()
	}
	hs := &http.Server{Handler: s.Handler()}
	go func() { <-ctx.Done(); hs.Close() }()
	if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) udpOrErr(w http.ResponseWriter) *udpServer {
	if s.udp == nil {
		http.Error(w, fmt.Sprintf("UDP 端口未能监听"), http.StatusServiceUnavailable)
	}
	return s.udp
}
