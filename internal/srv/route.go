package srv

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/ping"
	"github.com/Marty-G1tHub/tracewing/internal/route"
)

// 回程探测的安全边界（agent 端）：
//   - 只能探测 route.Targets() 里的固定目标，或“请求方自己的来源 IP”（取 TCP 连接的对端地址，不信任任何头）；
//     接口不接受任意 IP / 域名参数，所以不能被拿来扫描第三方；
//   - 来源 IP 必须是公网地址，私有 / 保留段一律拒绝；
//   - 全局限速（令牌桶）+ 并发上限，防止被滥用成 ICMP 放大源；
//   - 只回传原始跳数据，不执行任何命令，不解析任何外部输入。
const (
	routeBurst    = 20               // 令牌桶容量：一轮完整检测（12 个目标 + 来源 IP）一次放得下
	routePerMin   = 30               // 每分钟补充的令牌数
	routeParallel = 3                // 同时进行的探测数
	routeMaxWait  = 45 * time.Second // 单次探测的总时限
)

type routeGate struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
	sem    chan struct{}
}

func newGate() *routeGate {
	return &routeGate{tokens: routeBurst, last: time.Now(), sem: make(chan struct{}, routeParallel)}
}

// take 取一个令牌与一个并发名额；失败返回 false 与原因。
func (g *routeGate) take(now time.Time) (release func(), why string) {
	g.mu.Lock()
	g.tokens += now.Sub(g.last).Seconds() * routePerMin / 60
	if g.tokens > routeBurst {
		g.tokens = routeBurst
	}
	g.last = now
	if g.tokens < 1 {
		g.mu.Unlock()
		return nil, "请求太频繁，稍后再试"
	}
	g.tokens--
	g.mu.Unlock()
	select {
	case g.sem <- struct{}{}:
		return func() { <-g.sem }, ""
	default:
		return nil, "同时进行的探测太多，稍后再试"
	}
}

// RouteInfo 描述 agent 的回程探测能力。
type RouteInfo struct {
	Capable bool           `json:"capable"` // 能否逐跳探测（需要 CAP_NET_RAW 或 Windows）
	Method  string         `json:"method"`
	Version string         `json:"data_version"`
	Targets []route.Target `json:"targets"`
	Note    string         `json:"note,omitempty"`
}

func (s *Server) routeInit() {
	s.routeOnce.Do(func() {
		s.gate = newGate()
		if s.Pinger == nil && !s.NoRoute {
			s.Pinger = ping.New()
		}
	})
}

func (s *Server) routeInfo() RouteInfo {
	s.routeInit()
	ts, ver := route.Targets()
	ri := RouteInfo{Version: ver, Targets: ts}
	switch {
	case s.NoRoute:
		ri.Note = "管理员已用 --no-route 关闭回程探测"
	case s.Pinger == nil:
		ri.Note = "没有可用的 ICMP：需要 CAP_NET_RAW（用 install.sh 安装的服务已授予）"
	case !ping.HopCapable(s.Pinger):
		ri.Method = s.Pinger.Name()
		ri.Note = "只有非特权 ICMP，不能逐跳；需要 CAP_NET_RAW"
	default:
		ri.Capable, ri.Method = true, s.Pinger.Name()
	}
	return ri
}

func (s *Server) routeTargets(w http.ResponseWriter, r *http.Request) { js(w, s.routeInfo()) }

func (s *Server) routeTrace(w http.ResponseWriter, r *http.Request) {
	ri := s.routeInfo()
	if !ri.Capable {
		http.Error(w, "回程探测不可用："+ri.Note, http.StatusNotImplemented)
		return
	}
	q := r.URL.Query()
	for k := range q { // 只认 target 一个参数，其余一律拒绝，避免日后有人误加“任意地址”口子
		if k != "target" {
			http.Error(w, "不支持的参数 "+k, http.StatusBadRequest)
			return
		}
	}
	id := q.Get("target")
	var dst net.IP
	var label string
	if id == "client" {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		dst = net.ParseIP(host)
		if dst == nil || !route.IsPublic(dst) {
			http.Error(w, "你的来源地址不是公网 IPv4（"+host+"），无法探测回到你的出口 IP（调用方环境的限制，不是 VPS 的问题）", http.StatusBadRequest)
			return
		}
		label = "你的出口 IP"
	} else {
		t, ok := route.FindTarget(id)
		if !ok {
			http.Error(w, "未知目标："+id+"（只能探测固定目标或 client）", http.StatusBadRequest)
			return
		}
		dst, label = net.ParseIP(t.IP), t.Label
	}
	release, why := s.gate.take(time.Now())
	if release == nil {
		w.Header().Set("Retry-After", "5")
		http.Error(w, why, http.StatusTooManyRequests)
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(r.Context(), routeMaxWait)
	defer cancel()
	tr := route.Run(ctx, s.Pinger, dst, route.Options{})
	tr.ID, tr.Label = id, label
	js(w, tr)
}

// ---- 客户端 ----

// RouteInfo 查询 agent 的回程探测能力与固定目标表。
func (c *Client) RouteInfo(ctx context.Context) (RouteInfo, error) {
	var ri RouteInfo
	err := c.getJSON(ctx, "/v1/route/targets", &ri)
	return ri, err
}

// RouteTrace 让 agent 对固定目标 id（或 "client"）做一次回程探测。
func (c *Client) RouteTrace(ctx context.Context, id string) (route.Trace, error) {
	var tr route.Trace
	err := c.getJSON(ctx, "/v1/route/trace?target="+id, &tr)
	return tr, err
}
