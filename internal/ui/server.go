// Package ui 是本机网页界面的后端：只监听 127.0.0.1 随机端口，
// 用一次性密钥 cookie + Host 校验 + JSON 内容类型防止其他网页/程序借它发请求。
package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/netx"
	"github.com/Marty-G1tHub/tracewing/internal/probe"
	"github.com/Marty-G1tHub/tracewing/internal/srv"
	"github.com/Marty-G1tHub/tracewing/internal/store"
	"github.com/Marty-G1tHub/tracewing/internal/web"
)

type App struct {
	Version string
	OS      string
	Store   *store.Store
	Quit    func()                    // 退出程序
	Open    func(target string) error // 用系统默认程序打开文件或网址

	key    string
	port   int
	mode   string // window | browser
	notice string // 回退到浏览器时的一句话说明

	mu   sync.Mutex
	job  *Job
	last time.Time
}

func normBase(s string) (string, error) { return srv.NormalizeBase(s) }

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Listen 绑定 127.0.0.1 随机端口，返回带密钥的入口地址。
func (a *App) Listen() (net.Listener, string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	a.key = newKey()
	a.port = ln.Addr().(*net.TCPAddr).Port
	a.last = time.Now()
	return ln, fmt.Sprintf("http://127.0.0.1:%d/?k=%s", a.port, a.key), nil
}

// SetMode 设置界面承载方式；window 模式关窗即退出，不做空闲超时。
func (a *App) SetMode(mode, notice string) {
	a.mu.Lock()
	a.mode, a.notice = mode, notice
	a.mu.Unlock()
}

// Serve 阻塞直到 ctx 结束；空闲 idle 时间无请求且没有任务在跑则调用 Quit。
func (a *App) Serve(ctx context.Context, ln net.Listener, idle time.Duration) error {
	hs := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sc, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		hs.Shutdown(sc)
	}()
	go func() {
		for ctx.Err() == nil {
			time.Sleep(10 * time.Second)
			a.mu.Lock()
			quiet := a.mode != "window" && time.Since(a.last) > idle && (a.job == nil || !a.job.running())
			a.mu.Unlock()
			if quiet && a.Quit != nil {
				a.Quit()
				return
			}
		}
	}()
	err := hs.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(web.FS, ".")
	static := http.FileServer(http.FS(sub))
	mux.HandleFunc("/static/", a.get(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/static")
		if r.URL.Path == "/report.html" || r.URL.Path == "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		static.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if !a.guard(w, r) {
			return
		}
		// 首次进入：带正确的 ?k= 则种 cookie 并跳转去掉参数
		if k := r.URL.Query().Get("k"); k != "" {
			if subtle.ConstantTimeCompare([]byte(k), []byte(a.key)) == 1 {
				http.SetCookie(w, &http.Cookie{Name: "k", Value: a.key, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}
		if !a.authed(r) {
			http.Error(w, "请使用程序打开的地址访问", 401)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, web.Read("index.html"))
	})
	mux.HandleFunc("/report/", a.get(func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Store.Report(strings.TrimPrefix(r.URL.Path, "/report/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}))
	mux.HandleFunc("/api/ping", a.get(func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]bool{"ok": true}) }))
	mux.HandleFunc("/api/info", a.get(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		mode, notice := a.mode, a.notice
		a.mu.Unlock()
		jsonOut(w, 200, map[string]string{"version": a.Version, "data_dir": a.Store.Dir, "os": a.OS, "mode": mode, "notice": notice})
	}))
	mux.HandleFunc("/api/config", a.any(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			b, _ := io.ReadAll(io.LimitReader(r.Body, 65<<10))
			if err := a.Store.SaveConfig(b); err != nil {
				jsonErr(w, 400, err)
				return
			}
			jsonOut(w, 200, map[string]bool{"ok": true})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(a.Store.LoadConfig())
	}))
	mux.HandleFunc("/api/history", a.get(func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, a.Store.List()) }))
	mux.HandleFunc("/api/run/", a.get(func(w http.ResponseWriter, r *http.Request) {
		b, err := a.Store.Summary(strings.TrimPrefix(r.URL.Path, "/api/run/"))
		if err != nil {
			jsonErr(w, 404, fmt.Errorf("记录不存在"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}))
	mux.HandleFunc("/api/delete", a.post(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ ID string }
		if !decode(w, r, &q) {
			return
		}
		if err := a.Store.Delete(q.ID); err != nil {
			jsonErr(w, 400, err)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/env", a.post(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Path netx.Path }
		if !decode(w, r, &q) {
			return
		}
		if err := q.Path.Normalize(); err != nil {
			jsonErr(w, 400, err)
			return
		}
		ctx, c := context.WithTimeout(r.Context(), 40*time.Second)
		defer c()
		jsonOut(w, 200, probe.Detect(ctx, &q.Path))
	}))
	// 用系统默认浏览器打开离线报告文件（窗口里不放新窗口 / 下载；报告本来就是可打印的单文件）
	mux.HandleFunc("/api/open-report", a.post(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ ID string }
		if !decode(w, r, &q) {
			return
		}
		p, err := a.Store.ReportPath(q.ID)
		if err != nil || a.Open == nil {
			jsonErr(w, 404, fmt.Errorf("报告不存在"))
			return
		}
		if err := a.Open(p); err != nil {
			jsonErr(w, 500, err)
			return
		}
		jsonOut(w, 200, map[string]string{"path": p})
	}))
	mux.HandleFunc("/api/run", a.post(a.handleRun))
	mux.HandleFunc("/api/job", a.get(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		j := a.job
		a.mu.Unlock()
		if j == nil {
			jsonOut(w, 200, map[string]string{"state": "idle"})
			return
		}
		jsonOut(w, 200, j.View())
	}))
	mux.HandleFunc("/api/cancel", a.post(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		if a.job != nil && a.job.running() && a.job.cancel != nil {
			a.job.cancel()
		}
		a.mu.Unlock()
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/quit", a.post(func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 200, map[string]bool{"ok": true})
		if a.Quit != nil {
			go func() { time.Sleep(200 * time.Millisecond); a.Quit() }()
		}
	}))
	return mux
}

func (a *App) handleRun(w http.ResponseWriter, r *http.Request) {
	rq := new(RunReq)
	if !decode(w, r, rq) {
		return
	}
	a.mu.Lock()
	if a.job != nil && a.job.running() {
		a.mu.Unlock()
		jsonErr(w, 409, fmt.Errorf("已有测速在进行，请先等待或取消"))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &Job{ID: newKey()[:8], Kind: rq.Kind, State: "running", Mbps: -1, Start: time.Now(), cancel: cancel, Text: "准备中"}
	a.job = j
	a.mu.Unlock()
	go func() {
		defer cancel()
		id, err := a.execute(ctx, j, rq)
		j.mu.Lock()
		defer j.mu.Unlock()
		switch {
		case ctx.Err() != nil:
			j.State = "canceled"
		case err != nil:
			j.State, j.Err = "error", err.Error()
		default:
			j.State, j.RunID, j.Frac = "done", id, 1
		}
	}()
	jsonOut(w, 200, map[string]string{"id": j.ID})
}

// ---- 安全与小工具 ----

func (a *App) guard(w http.ResponseWriter, r *http.Request) bool {
	h := r.Host
	if h != fmt.Sprintf("127.0.0.1:%d", a.port) && h != fmt.Sprintf("localhost:%d", a.port) {
		http.Error(w, "bad host", 403)
		return false
	}
	a.mu.Lock()
	a.last = time.Now()
	a.mu.Unlock()
	return true
}

func (a *App) authed(r *http.Request) bool {
	c, err := r.Cookie("k")
	return err == nil && c.Value == a.key
}

func (a *App) get(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.guard(w, r) || !a.need(w, r) {
			return
		}
		h(w, r)
	}
}

func (a *App) post(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.guard(w, r) || !a.need(w, r) {
			return
		}
		if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			jsonErr(w, 415, fmt.Errorf("需要 POST + application/json"))
			return
		}
		h(w, r)
	}
}

func (a *App) any(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			a.post(h)(w, r)
		} else {
			a.get(h)(w, r)
		}
	}
}

func (a *App) need(w http.ResponseWriter, r *http.Request) bool {
	if !a.authed(r) {
		jsonErr(w, 401, fmt.Errorf("未授权：请用程序打开的地址访问"))
		return false
	}
	return true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		jsonErr(w, 400, fmt.Errorf("请求格式错误"))
		return false
	}
	return true
}

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, err error) {
	jsonOut(w, code, map[string]string{"error": err.Error()})
}
