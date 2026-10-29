package engine

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/netx"
	"github.com/Marty-G1tHub/tracewing/internal/ping"
	"github.com/Marty-G1tHub/tracewing/internal/route"
	"github.com/Marty-G1tHub/tracewing/internal/srv"
)

// RouteIn 是线路检测的输入。Token 为空时只做“去程”（本机 → 服务器）。
type RouteIn struct {
	Base  string
	Token string
}

// RunRoute 做两件事：
//  1. 去程：本机逐跳探测到服务器 IP（需要本机能发 TTL：Windows 可以；Linux 需 CAP_NET_RAW）；
//  2. 回程：让服务器上的 agent 逐跳探测到 12 个固定国内目标和“你的出口 IP”。
//
// agent 只回传原始跳数据，线路判定一律在这里用内置前缀表完成。
// clientSide 标在“因本机环境而做不了”的结果前面，避免用户误以为是 VPS 的问题。
const clientSide = "本机限制（不是 VPS 的问题）："

func RunRoute(ctx context.Context, p *netx.Path, in RouteIn, env *model.Env, local ping.Pinger, sink Sink) *model.RouteReport {
	sink = sinkOr(sink)
	tb := route.Default()
	_, tv := route.Targets()
	out := &model.RouteReport{PrefixVersion: tb.Version, TargetsVersion: tv, Env: *env}
	out.Params.Return = in.Token != ""
	out.Params.Outbound = true
	note := func(level, code, text string) {
		out.Notes = append(out.Notes, model.Note{Level: level, Code: code, Text: text})
	}

	b, err := srv.NormalizeBase(in.Base)
	if err != nil {
		note("bad", "ADDR", err.Error())
		return out
	}
	out.Server = b
	u, _ := url.Parse(b)
	host := u.Hostname()

	if env.TUN || env.FakeIP {
		note("warn", "TUN", "检测到 TUN / Fake-IP：本机发出的探测可能被代理软件接管，去程结果不可信；回程 12 条不受影响（探测在服务器上做）。“回到你的出口 IP”一项测的是代理出口，可能无法确定。这些都是本机（客户端）环境造成的限制，不是 VPS 的问题。")
	}
	if env.SysProxy.HTTP != "" || env.SysProxy.HTTPS != "" || env.SysProxy.SOCKS != "" {
		note("info", "PROXY", "系统代理只影响 HTTP 请求；ICMP 去程探测不经过代理。若服务器地址经代理访问，agent 看到的“你的出口 IP”会是代理的出口。")
	}

	// ---- 去程 ----
	total := 1.0
	if in.Token != "" {
		total = 15
	}
	done := 0.0
	step := func(t string) { sink.Status(t); sink.Live(-1, done/total) }
	step("去程：本机 → 服务器 逐跳探测")
	line := &model.RouteLine{ID: "outbound", Label: "本机 → " + host}
	out.Outbound = line
	var dst net.IP
	if ip := net.ParseIP(host); ip != nil {
		dst = ip.To4()
	} else if ips, err := net.DefaultResolver.LookupIPAddr(ctx, host); err == nil {
		for _, a := range ips {
			if v4 := a.IP.To4(); v4 != nil {
				dst = v4
				break
			}
		}
	}
	switch {
	case dst == nil:
		line.Err = "无法解析为 IPv4 地址"
	case !route.IsPublic(dst):
		line.Err = clientSide + "服务器地址解析为 " + dst.String() + "，不是公网 IPv4（可能是内网地址或本机代理的 Fake-IP），无法探测去程"
	case local == nil:
		line.Err = clientSide + "本机无法发 ICMP，去程不可用（可改在能发 ICMP 的机器上运行）"
	case !ping.HopCapable(local):
		line.Err = clientSide + "本机只有非特权 ICMP，不能逐跳（Linux 需要 root 或 CAP_NET_RAW；Windows 无此限制）"
	default:
		out.Params.Method = local.Name()
		tr := route.Run(ctx, local, dst, route.Options{})
		tr.ID, tr.Label = "outbound", line.Label
		v := route.Classify(tb, tr)
		line.Trace, line.Verdict = &tr, &v
		out.Headlines = append(out.Headlines, "去程："+v.Label+"（"+route.ConfLabel(v.Confidence)+"）")
	}
	done++

	if in.Token == "" {
		note("info", "NOTOKEN", "没有填令牌，只检查了去程。填上服务器令牌即可检查回程（服务器 → 国内三网）。")
		return out
	}

	// ---- 回程 ----
	c := &srv.Client{Base: b, Token: in.Token, HTTP: p.Client()}
	step("回程：连接服务器 agent")
	if _, err := c.Login(ctx); err != nil {
		note("bad", "LOGIN", "连不上服务端 "+b+"："+err.Error())
		return out
	}
	info, err := c.Info(ctx)
	if err != nil {
		note("bad", "INFO", err.Error())
		return out
	}
	out.ServerInfo = info
	ri, err := c.RouteInfo(ctx)
	if err != nil {
		note("bad", "ROUTE", "这台服务端没有回程探测接口（/v1/route/targets 不可用）："+err.Error())
		return out
	}
	out.AgentMethod = ri.Method
	if !ri.Capable {
		note("warn", "AGENT_NOCAP", "agent 不能逐跳探测："+ri.Note+"。用 install.sh 安装的服务已授予 CAP_NET_RAW；手动运行请加 sudo 或 setcap cap_net_raw+ep。")
		return out
	}

	type job struct {
		id, label, carrier, city string
		client                   bool
	}
	var jobs []job
	for _, t := range ri.Targets {
		jobs = append(jobs, job{id: t.ID, label: t.Label, carrier: t.Carrier, city: t.City})
	}
	jobs = append(jobs, job{id: "client", label: "你的出口 IP", client: true})
	res := make([]model.RouteLine, len(jobs))
	var mu sync.Mutex
	sem := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, j job) {
			defer wg.Done()
			defer func() { <-sem }()
			l := model.RouteLine{ID: j.id, Label: j.label, Carrier: j.carrier, City: j.city}
			var tr route.Trace
			var err error
			for try := 0; try < 3; try++ {
				if ctx.Err() != nil {
					err = ctx.Err()
					break
				}
				tr, err = c.RouteTrace(ctx, j.id)
				if err == nil || !strings.Contains(err.Error(), "429") {
					break
				}
				select {
				case <-time.After(6 * time.Second):
				case <-ctx.Done():
				}
			}
			if err != nil {
				l.Err = err.Error()
			} else {
				v := route.Classify(tb, tr)
				l.Trace, l.Verdict = &tr, &v
			}
			res[i] = l
			mu.Lock()
			done++
			step(fmt.Sprintf("回程：服务器 → %s（%d/%d）", j.label, int(done)-1, len(jobs)))
			mu.Unlock()
		}(i, j)
	}
	wg.Wait()
	for i, j := range jobs {
		if j.client {
			l := res[i]
			out.Client = &l
		} else {
			out.Return = append(out.Return, res[i])
		}
	}
	out.Headlines = append(out.Headlines, returnHeadlines(out.Return)...)
	if out.Client != nil && out.Client.Verdict != nil {
		out.Headlines = append(out.Headlines, "回到你的出口 IP："+out.Client.Verdict.Label+"（"+route.ConfLabel(out.Client.Verdict.Confidence)+"）")
	}
	return out
}

// returnHeadlines 按运营商汇总回程：每家一句，说明四地是否一致。
func returnHeadlines(ls []model.RouteLine) []string {
	names := map[string]string{"telecom": "电信", "unicom": "联通", "mobile": "移动"}
	var hs []string
	for _, c := range []string{"telecom", "unicom", "mobile"} {
		cnt := map[string]int{}
		var order []string
		n, ok := 0, 0
		for _, l := range ls {
			if l.Carrier != c {
				continue
			}
			n++
			if l.Verdict == nil {
				continue
			}
			ok++
			lab := l.Verdict.Label
			if l.Verdict.Confidence == route.Unknown {
				lab = "无法确定"
			}
			if cnt[lab] == 0 {
				order = append(order, lab)
			}
			cnt[lab]++
		}
		if n == 0 {
			continue
		}
		var parts []string
		for _, lab := range order {
			parts = append(parts, fmt.Sprintf("%s ×%d", lab, cnt[lab]))
		}
		if ok == 0 {
			parts = []string{"全部探测失败"}
		}
		hs = append(hs, fmt.Sprintf("回程 · %s：%s", names[c], strings.Join(parts, "，")))
	}
	return hs
}
