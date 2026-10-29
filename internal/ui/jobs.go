package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/engine"
	"github.com/Marty-G1tHub/tracewing/internal/judge"
	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/netx"
	"github.com/Marty-G1tHub/tracewing/internal/ping"
	"github.com/Marty-G1tHub/tracewing/internal/probe"
	"github.com/Marty-G1tHub/tracewing/internal/report"
	"github.com/Marty-G1tHub/tracewing/internal/store"
)

// RunReq 是前端提交的一次测速请求。
type RunReq struct {
	Kind string    `json:"kind"` // broadband | testip | vps | route
	Path netx.Path `json:"path"`
	BB   struct {
		Profile  string   `json:"profile"`
		Regions  []string `json:"regions"`
		Down     bool     `json:"down"`
		Up       bool     `json:"up"`
		PlanDown *float64 `json:"plan_down"`
		PlanUp   *float64 `json:"plan_up"`
		Streams  int      `json:"streams"`
	} `json:"bb"`
	IP struct {
		Targets string `json:"targets"`
		Method  string `json:"method"`
		TCPPort int    `json:"tcp_port"`
		Count   int    `json:"count"`
		Trace   bool   `json:"trace"`
		Lookup  bool   `json:"lookup"`
	} `json:"ip"`
	Route struct {
		Server string `json:"server"`
		Token  string `json:"token"`
	} `json:"route"`
	VPS struct {
		Source    string   `json:"source"`
		Server    string   `json:"server"`
		Token     string   `json:"token"`
		Region    string   `json:"region"`
		DownURL   string   `json:"down_url"`
		UpURL     string   `json:"up_url"`
		Profile   string   `json:"profile"`
		UploadCap *float64 `json:"upload_cap"`
		UDPMbps   float64  `json:"udp_mbps"`
	} `json:"vps"`
}

// Job 是当前任务的可序列化状态（前端轮询）。同一时间只跑一个——并行测速会互相污染。
type Job struct {
	mu     sync.Mutex
	ID     string
	Kind   string
	State  string // running | done | error | canceled
	Text   string
	Mbps   float64 // <0 无
	Frac   float64
	Err    string
	RunID  string // 完成后的记录 ID
	Start  time.Time
	cancel context.CancelFunc
}

func (j *Job) running() bool   { j.mu.Lock(); defer j.mu.Unlock(); return j.State == "running" }
func (j *Job) Status(t string) { j.mu.Lock(); j.Text = t; j.mu.Unlock() }
func (j *Job) Live(m, f float64) {
	j.mu.Lock()
	j.Mbps, j.Frac = m, f
	j.mu.Unlock()
}

type jobView struct {
	ID    string  `json:"id"`
	Kind  string  `json:"kind"`
	State string  `json:"state"`
	Text  string  `json:"text"`
	Mbps  float64 `json:"mbps"`
	Frac  float64 `json:"frac"`
	Err   string  `json:"err,omitempty"`
	RunID string  `json:"run_id,omitempty"`
	Secs  float64 `json:"elapsed_s"`
}

func (j *Job) View() jobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	return jobView{j.ID, j.Kind, j.State, j.Text, j.Mbps, j.Frac, j.Err, j.RunID, time.Since(j.Start).Seconds()}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (a *App) execute(ctx context.Context, j *Job, rq *RunReq) (runID string, err error) {
	if err := rq.Path.Normalize(); err != nil {
		return "", err
	}
	p := &rq.Path
	now := time.Now()
	id := store.NewID(rq.Kind, now)
	meta := model.Meta{Kind: rq.Kind, Schema: model.SchemaVersion, AppVersion: a.Version, ID: id, CapturedAt: now.Format(time.RFC3339), OS: a.OS}

	j.Status("环境探测：代理 / TUN / Fake-IP / 出口 IP")
	env := probe.Detect(ctx, p)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	eps, from, _ := engine.LoadEndpoints(a.Store.Dir)

	var (
		summary any
		entry   = store.Entry{ID: id, Kind: rq.Kind, At: meta.CapturedAt}
	)
	switch rq.Kind {
	case "broadband":
		rounds, dur, warm := engine.BBProfile(rq.BB.Profile)
		meta.Profile = rq.BB.Profile
		regs := []string{}
		for _, r := range rq.BB.Regions {
			if r == "cn" || r == "global" {
				regs = append(regs, r)
			}
		}
		if len(regs) == 0 || (!rq.BB.Down && !rq.BB.Up) {
			return "", fmt.Errorf("请至少选一个区域和一个方向")
		}
		prm := model.BBParams{Profile: rq.BB.Profile, Rounds: rounds, DurationS: dur, WarmupS: warm,
			Streams: clamp(rq.BB.Streams, 1, 16), Regions: regs, DoDown: rq.BB.Down, DoUp: rq.BB.Up,
			PlanDown: rq.BB.PlanDown, PlanUp: rq.BB.PlanUp, EndpointsFrom: from}
		groups := engine.RunBroadband(ctx, p, prm, eps, env, j)
		bb := &model.Broadband{Meta: meta, Params: prm, Env: *env, Groups: groups, Verdict: judge.BroadbandVerdict(groups)}
		for _, n := range env.Notes {
			if n.Level != "info" {
				bb.Warnings = append(bb.Warnings, n.Text)
			}
		}
		summary = bb
		entry.Title = "宽带测速"
		entry.Headline = bb.Verdict
	case "testip":
		ips, rejected := engine.ParseTargets(rq.IP.Targets)
		if len(ips) == 0 {
			return "", fmt.Errorf("没有可测的公网 IPv4 地址%s", rejHint(rejected))
		}
		if len(ips) > 50 {
			return "", fmt.Errorf("一次最多 50 个地址（现在 %d 个）", len(ips))
		}
		method := rq.IP.Method
		var pg ping.Pinger
		var fallback string
		if method == "tcp" {
			pg = ping.TCP{Port: clamp(rq.IP.TCPPort, 1, 65535)}
		} else {
			method = "icmp"
			if pg = ping.New(); pg == nil {
				pg, method, fallback = ping.TCP{Port: 443}, "tcp", "本机无法使用 ICMP，已改用 TCP 443 握手时延。"
			}
		}
		prm := model.IPParams{Method: method, Count: clamp(rq.IP.Count, 3, 500), TimeoutMs: 2000, IntervalMs: 200,
			Hops: 18, Trace: rq.IP.Trace && method == "icmp", Lookup: rq.IP.Lookup}
		if method == "tcp" {
			prm.TCPPort = pg.(ping.TCP).Port
		}
		ts, trusted := engine.RunTestIP(ctx, p, prm, ips, env, pg, j)
		t := &model.IPTest{Meta: meta, Params: prm, Env: *env, Targets: ts, Rejected: rejected, TraceTrust: trusted,
			Verdict: engine.TestIPVerdict(ts, trusted, method),
			Scope:   "本次只测 ICMP/TCP 延迟、丢包与去程；不含吞吐、UDP、回程与晚高峰。ICMP 不经 HTTP/SOCKS 代理，只受 TUN 影响。"}
		if fallback != "" {
			t.Verdict = append([]string{fallback}, t.Verdict...)
		}
		summary = t
		entry.Title = fmt.Sprintf("测试IP · %d 个地址", len(ts))
		if len(t.Verdict) > 0 {
			entry.Headline = t.Verdict[0]
		}
	case "vps":
		rounds, dur, gap, en, em := engine.VPSProfile(rq.VPS.Profile)
		meta.Profile = rq.VPS.Profile
		src := rq.VPS.Source
		if src != "server" && src != "url" && src != "public" {
			return "", fmt.Errorf("请选择测速来源")
		}
		prm := model.VPSParams{Source: src, Region: rq.VPS.Region, DownURL: rq.VPS.DownURL, UpURL: rq.VPS.UpURL,
			Profile: rq.VPS.Profile, DurationS: dur, Rounds: rounds, GapS: gap, Streams: 4, EchoCount: en, EchoMs: em,
			UploadCap: rq.VPS.UploadCap, UDPMbps: rq.VPS.UDPMbps}
		in := engine.VPSIn{Params: prm, Base: rq.VPS.Server, Token: rq.VPS.Token, Th: judge.DefaultThresholds}
		if src == "server" {
			if b, err := normBase(rq.VPS.Server); err == nil {
				in.Hist = a.Store.VPSHistory(b, src)
			}
		}
		t := engine.RunVPS(ctx, p, in, eps, env, j)
		t.Meta = meta
		summary = t
		entry.Title = "VPS测速 · " + t.Server
		entry.Headline = fmt.Sprintf("%s · %s", t.Status, t.Diagnosis.Code)
		entry.Server, entry.Source, entry.Period = t.Server, src, t.Period
		entry.Med = map[string]float64{}
		for _, x := range t.Tput {
			if x.Median != nil {
				entry.Med[x.Key] = *x.Median
			}
		}
	case "route":
		if strings.TrimSpace(rq.Route.Server) == "" {
			return "", fmt.Errorf("请填写服务器地址")
		}
		rr := engine.RunRoute(ctx, p, engine.RouteIn{Base: rq.Route.Server, Token: rq.Route.Token}, env, ping.New(), j)
		rr.Meta = meta
		summary = rr
		entry.Title = "线路 · " + rr.Server
		entry.Server = rr.Server
		if len(rr.Headlines) > 0 {
			entry.Headline = strings.Join(rr.Headlines[:min(len(rr.Headlines), 2)], "；")
		} else if len(rr.Notes) > 0 {
			entry.Headline = rr.Notes[0].Text
		}
	default:
		return "", fmt.Errorf("未知测速类型")
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	html, _ := report.Build(rq.Kind, summary)
	if err := a.Store.Save(entry, summary, html); err != nil {
		return "", fmt.Errorf("结果已测出但保存失败：%v", err)
	}
	return id, nil
}

func rejHint(r []string) string {
	if len(r) == 0 {
		return ""
	}
	if len(r) > 3 {
		r = r[:3]
	}
	return "（被拒：" + fmt.Sprint(r) + "）"
}
