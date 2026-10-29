package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/judge"
	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/netx"
)

// BBProfile 返回档位参数。quick：1 轮 × 8s；standard：3 轮 × 10s。
func BBProfile(name string) (rounds, durS int, warm float64) {
	if name == "quick" {
		return 1, 8, 1.5
	}
	return 3, 10, 2
}

func regionLabel(r string) string {
	switch r {
	case "cn":
		return "国内源"
	case "global":
		return "境外源"
	}
	return r
}

// RunBroadband 对每个区域：选源（探测）→ 多轮测量 → 取中位。
// 引擎不选路：走哪条路由 Path 决定，并把实际路径与 DNS 证据写进结果。
func RunBroadband(ctx context.Context, p *netx.Path, prm model.BBParams, eps []Endpoint, env *model.Env, sink Sink) []model.Group {
	sink = sinkOr(sink)
	client := p.Client()
	var groups []model.Group
	for _, region := range prm.Regions {
		g := model.Group{Region: region, Label: regionLabel(region)}
		if env != nil {
			if region == "cn" {
				g.Exit = env.ExitCN
			} else {
				g.Exit = env.ExitGlobal
			}
		}
		for _, dir := range []string{"down", "up"} {
			if (dir == "down" && !prm.DoDown) || (dir == "up" && !prm.DoUp) || ctx.Err() != nil {
				continue
			}
			name := regionLabel(region) + map[string]string{"down": "下行", "up": "上行"}[dir]
			ph := runPhase(ctx, p, client, prm, eps, env, region, dir, name, sink)
			if dir == "down" {
				g.Down = ph
			} else {
				g.Up = ph
			}
		}
		groups = append(groups, g)
	}
	return groups
}

func runPhase(ctx context.Context, p *netx.Path, client *http.Client, prm model.BBParams, eps []Endpoint, env *model.Env, region, dir, name string, sink Sink) *model.Phase {
	ph := &model.Phase{Dir: dir, Rounds: []model.Round{}}
	cands := Pick(eps, region, dir)
	if len(cands) == 0 {
		ph.Error = "端点表里没有该区域/方向的端点"
		return ph
	}
	var chosen *Endpoint
	var pr ProbeResult
	for i := range cands {
		ep := cands[i]
		sink.Status(fmt.Sprintf("%s：探测 %s", name, ep.Label))
		pr = Probe(ctx, client, ep)
		if ctx.Err() != nil {
			ph.Error = "已取消"
			return ph
		}
		if pr.OK {
			ph.Tried = append(ph.Tried, fmt.Sprintf("%s: 可用 (HTTP %d, %d 字节)", ep.ID, pr.Status, pr.Bytes))
			chosen = &ep
			break
		}
		ph.Tried = append(ph.Tried, fmt.Sprintf("%s: 不可用 (%s)", ep.ID, pr.Err))
	}
	if chosen == nil {
		ph.Error = "所有候选源都探测失败（见 tried）。可能被代理/TUN 拦截，或该区域在你的网络里不可达。"
		return ph
	}
	ph.EndpointID, ph.URL = chosen.ID, chosen.URL
	if u, err := url.Parse(chosen.URL); err == nil {
		ph.Via = p.Describe(u)
		ev := p.Lookup(ctx, u.Hostname())
		ph.DNS = &ev
		ph.Warnings = pathWarnings(region, ev, env, p, u)
	}
	plan := prm.PlanDown
	if dir == "up" {
		plan = prm.PlanUp
	}
	ph.PlanMbps = plan

	chunk := int64(32 << 20)
	if dir == "up" {
		chunk = 16 << 20
	}
	if strings.Contains(chosen.URL, "{bytes}") {
		chunk = 25_000_000
	}
	spec := FlowSpec{
		Client: client, Dir: dir, URL: chosen.URL,
		Range: chosen.Range && pr.Range, TotalSize: pr.Total,
		Streams: prm.Streams, Duration: time.Duration(prm.DurationS) * time.Second,
		Warmup: time.Duration(prm.WarmupS * float64(time.Second)), Chunk: chunk,
	}
	for n := 1; n <= prm.Rounds; n++ {
		if ctx.Err() != nil {
			break
		}
		sink.Status(fmt.Sprintf("%s：%s，第 %d/%d 轮（%d 流 × %d 秒）", name, chosen.Label, n, prm.Rounds, prm.Streams, prm.DurationS))
		r := RunFlow(ctx, spec, sink)
		r.N = n
		ph.Rounds = append(ph.Rounds, r)
		if n < prm.Rounds {
			sleep(ctx, 1500*time.Millisecond)
		}
	}
	ph.Min, ph.Median, ph.Max = judge.SummarizeRounds(ph.Rounds)
	if ph.Median != nil {
		ph.MedianMBps = judge.Ptr(judge.Round2(*ph.Median / 8))
		if plan != nil && *plan > 0 {
			ph.UtilPct = judge.Ptr(judge.Round1(*ph.Median / *plan * 100))
		}
	} else if ph.Error == "" {
		ph.Error = "所有轮次均失败"
	}
	return ph
}

// pathWarnings：只提示，不改变 ok。
func pathWarnings(region string, ev netx.DNSEvidence, env *model.Env, p *netx.Path, u *url.URL) []string {
	var w []string
	proxied := p.ProxyFor(u) != nil
	if region == "cn" && ev.Class == "fake-ip" {
		w = append(w, "domestic_source_resolved_fake_ip：国内源被本机解析成 Fake-IP（"+first(ev.IPs)+"）——国内域名被代理软件接管，这组读数代表代理路径，不是运营商直连。")
	}
	if region == "global" && env != nil && env.FakeIP && ev.Class == "public" && !proxied {
		w = append(w, "proxy_source_resolved_public_not_fake_ip：环境是 Fake-IP，但境外源解析到了真实公网 IP——境外流量可能没有被代理接管（直连境外）。")
	}
	if proxied && ev.Class != "error" {
		w = append(w, "本机侧 DNS 仅作参考：经代理访问时，真正的解析发生在代理端。")
	}
	return w
}

func first(ss []string) string {
	if len(ss) > 0 {
		return ss[0]
	}
	return ""
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
