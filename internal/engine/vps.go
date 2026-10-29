package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/judge"
	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/netx"
	"github.com/Marty-G1tHub/tracewing/internal/srv"
)

// VPSIn 是 VPS 测速的输入。
type VPSIn struct {
	Params model.VPSParams
	Base   string // server 模式：http://host:port
	Token  string // server 模式：令牌（只用于计算 HMAC，不写入结果）
	Hist   []judge.HistRec
	Th     judge.Thresholds
}

// VPSProfile 返回档位参数（轮数、每轮秒数、轮间隔秒、echo 次数、echo 间隔毫秒）。数值是经验设定：quick 8s×1，standard 15s×3、间隔 5s。
func VPSProfile(name string) (rounds, dur, gap, echoN, echoMs int) {
	if name == "quick" {
		return 1, 8, 2, 15, 200
	}
	return 3, 15, 5, 30, 200
}

type vpsTarget struct {
	down, up string
	rng      bool
	total    int64
	hc       *http.Client
	via      string
	hostport string
	cli      *srv.Client
	err      string
}

// RunVPS 测“本机 ↔ 一台服务器”的吞吐/时延，并给出启发式诊断。
// 来源：server=自带服务端（本程序 --server）；url=你自己的下载/上传地址；public=公共端点（只是参考路径，不是你的 VPS）。
func RunVPS(ctx context.Context, p *netx.Path, in VPSIn, eps []Endpoint, env *model.Env, sink Sink) *model.VPSTest {
	sink = sinkOr(sink)
	prm := in.Params
	out := &model.VPSTest{Params: prm, Tput: []model.Tput{}}
	if env != nil {
		out.Env = *env
	}
	out.Period = judge.Period(time.Now())
	addNote := func(l, c, t string) { out.Notes = append(out.Notes, model.Note{Level: l, Code: c, Text: t}) }

	tg := resolveVPS(ctx, p, in, eps, sink, out)
	if tg.err != "" {
		addNote("bad", "TARGET", tg.err)
		out.Diagnosis = judge.Diagnose(judge.DiagIn{Med: map[string]*float64{}, Th: in.Th})
		out.Status = "FAILED"
		return out
	}
	out.Via = tg.via
	if prm.Source == "public" {
		addNote("warn", "PUBLIC_ONLY", "公共端点只能反映“本机到该公共节点”的路径，不是你的 VPS；要测你的 VPS，请用“自带服务端”或填你自己的下载地址。")
	}
	if prm.Source != "server" {
		addNote("info", "NO_SERVER_SIDE", "没有服务端配合：无应用层 echo、无服务器 CPU/内存/steal、无 UDP 测试。")
	}

	// 1) 时延（空闲时先测）
	if tg.hostport != "" {
		sink.Status("测 TCP 建连时延")
		e := srv.TCPConnectTo(ctx, tg.hostport, prm.EchoCount, time.Duration(prm.EchoMs)*time.Millisecond)
		out.TCPConnect = &e
	}
	if tg.cli != nil && ctx.Err() == nil {
		sink.Status("测应用层 echo 时延")
		e := tg.cli.EchoRTT(ctx, prm.EchoCount, time.Duration(prm.EchoMs)*time.Millisecond, func(i int) {
			sink.Live(-1, float64(i)/float64(prm.EchoCount))
		})
		out.Echo = &e
	}

	// 2) 吞吐：4 组 × rounds；服务端模式并行采样资源
	groups := []struct {
		key, dir string
		streams  int
	}{{"fwd1", "up", 1}, {"rev1", "down", 1}, {"fwd4", "up", 4}, {"rev4", "down", 4}}
	totalSec := len(groups)*prm.Rounds*(prm.DurationS+prm.GapS) + 4
	var resWG sync.WaitGroup
	var resv *model.Resources
	if tg.cli != nil {
		resWG.Add(1)
		go func() {
			defer resWG.Done()
			if r, err := tg.cli.Resources(ctx, time.Duration(totalSec)*time.Second); err == nil {
				resv = r
			} else {
				resv = &model.Resources{Note: "资源采样失败：" + err.Error()}
			}
		}()
	}
	med := map[string]*float64{}
	for _, g := range groups {
		if ctx.Err() != nil {
			break
		}
		t := model.Tput{Key: g.key, Dir: g.dir, Streams: g.streams, Rounds: []model.Round{}}
		u := tg.down
		if g.dir == "up" {
			u = tg.up
		}
		if u == "" {
			t.Error = "没有提供上传地址，该方向未测"
			out.Tput = append(out.Tput, t)
			med[g.key] = nil
			continue
		}
		chunk := int64(32 << 20)
		if g.dir == "up" {
			chunk = 16 << 20
		}
		spec := FlowSpec{Client: tg.hc, Dir: g.dir, URL: u, Range: tg.rng && g.dir == "down", TotalSize: tg.total,
			Streams: g.streams, Duration: time.Duration(prm.DurationS) * time.Second,
			Warmup: 2 * time.Second, Chunk: chunk}
		if prm.DurationS <= 8 {
			spec.Warmup = 1500 * time.Millisecond
		}
		for n := 1; n <= prm.Rounds && ctx.Err() == nil; n++ {
			sink.Status(fmt.Sprintf("吞吐 %s：%s %d 流，第 %d/%d 轮", keyCN(g.key), map[string]string{"up": "本机→服务器", "down": "服务器→本机"}[g.dir], g.streams, n, prm.Rounds))
			r := RunFlow(ctx, spec, sink)
			r.N = n
			t.Rounds = append(t.Rounds, r)
			if n < prm.Rounds {
				sleep(ctx, time.Duration(prm.GapS)*time.Second)
			}
		}
		t.Min, t.Median, t.Max = judge.SummarizeRounds(t.Rounds)
		if t.Median == nil {
			t.Error = "所有轮次均失败"
		}
		med[g.key] = t.Median
		out.Tput = append(out.Tput, t)
	}
	if ctx.Err() != nil {
		addNote("warn", "CANCELED", "测试被取消，结果不完整。")
	}

	// 3) UDP（可选）
	if prm.UDPMbps > 0 && tg.cli != nil && ctx.Err() == nil {
		for i, d := range []string{"up", "down"} {
			sink.Live(-1, float64(i)/2)
			sink.Status("UDP " + map[string]string{"up": "上行", "down": "下行"}[d])
			out.UDP = append(out.UDP, tg.cli.UDP(ctx, d, prm.UDPMbps, 5))
		}
		addNote("info", "UDP_ROUTE", "UDP 不经 HTTP/SOCKS 代理，只走系统路由；TUN 环境下它走的是 TUN。")
	}
	resWG.Wait()
	out.Resources = resv

	// 4) 判定
	out.ReverseGain = judge.ReverseGainOf(med)
	if out.ReverseGain != nil {
		out.GainBand = judge.GainBand(*out.ReverseGain)
	}
	out.Util = judge.ForwardUtil(med, prm.UploadCap)
	if out.Util != nil {
		out.UtilBand = judge.UtilBand(*out.Util)
	}
	out.Baseline = judge.BuildBaseline(in.Hist, in.Th)
	if out.Baseline != nil {
		out.Baseline.Drop, _, _ = judge.DropVsBaseline(out.Baseline, med)
	}
	out.Diagnosis = judge.Diagnose(judge.DiagIn{Med: med, Res: out.Resources, UploadCap: prm.UploadCap, Period: out.Period, Baseline: out.Baseline, Th: in.Th})
	out.Status = judge.StatusOf(med)
	if env != nil && (env.TUN || env.FakeIP) {
		out.Diagnosis.Limitations = append(out.Diagnosis.Limitations, "本机处于 TUN/Fake-IP 环境：吞吐代表“经代理软件”的路径，诊断可能把代理的限制误当成线路问题。")
	}
	return out
}

func keyCN(k string) string {
	return map[string]string{"fwd1": "正向单流", "rev1": "反向单流", "fwd4": "正向4流", "rev4": "反向4流"}[k]
}

func hostPortOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[u.Scheme]
	}
	if port == "" {
		return ""
	}
	return net.JoinHostPort(u.Hostname(), port)
}

func resolveVPS(ctx context.Context, p *netx.Path, in VPSIn, eps []Endpoint, sink Sink, out *model.VPSTest) (tg vpsTarget) {
	prm := in.Params
	base := p.Client()
	switch prm.Source {
	case "server":
		b, err := srv.NormalizeBase(in.Base)
		if err != nil {
			tg.err = err.Error()
			return
		}
		if in.Token == "" {
			tg.err = "没有填令牌"
			return
		}
		c := &srv.Client{Base: b, Token: in.Token, HTTP: base}
		sink.Status("连接服务端")
		if _, err := c.Login(ctx); err != nil {
			tg.err = "连不上服务端 " + b + "：" + err.Error()
			return
		}
		info, err := c.Info(ctx)
		if err != nil {
			tg.err = err.Error()
			return
		}
		out.Server, out.ServerInfo = b, info
		tg.cli, tg.hc = c, c.Authed()
		tg.down, tg.up = c.DownURL(), c.UpURL()
		tg.hostport = hostPortOf(b)
		u, _ := url.Parse(b)
		tg.via = p.Describe(u)
	case "url":
		if prm.DownURL == "" && prm.UpURL == "" {
			tg.err = "请至少填一个下载或上传地址"
			return
		}
		tg.hc, tg.down, tg.up = base, prm.DownURL, prm.UpURL
		ref := firstNonEmpty(prm.DownURL, prm.UpURL)
		out.Server = hostOnly(ref)
		tg.hostport = hostPortOf(ref)
		if u, err := url.Parse(ref); err == nil {
			tg.via = p.Describe(u)
		}
		if prm.DownURL != "" && !strings.Contains(prm.DownURL, "{bytes}") {
			sink.Status("探测下载地址")
			pr := Probe(ctx, base, Endpoint{Dir: "down", URL: prm.DownURL, Range: true})
			if !pr.OK {
				tg.err = "下载地址不可用：" + pr.Err
				return
			}
			tg.rng, tg.total = pr.Range, pr.Total
		}
	case "public":
		region := prm.Region
		if region == "" {
			region = "global"
		}
		for _, dir := range []string{"down", "up"} {
			for _, ep := range Pick(eps, region, dir) {
				sink.Status("探测公共端点 " + ep.Label)
				pr := Probe(ctx, base, ep)
				if !pr.OK {
					continue
				}
				if dir == "down" {
					tg.down, tg.rng, tg.total = ep.URL, ep.Range && pr.Range, pr.Total
					out.Server = ep.Label + "（公共端点）"
					tg.hostport = hostPortOf(ep.URL)
					if u, err := url.Parse(ep.URL); err == nil {
						tg.via = p.Describe(u)
					}
				} else {
					tg.up = ep.URL
				}
				break
			}
		}
		tg.hc = base
		if tg.down == "" && tg.up == "" {
			tg.err = "没有可用的公共端点（该区域在你的网络里不可达？）"
		}
	default:
		tg.err = "未知来源 " + prm.Source
	}
	return
}

func hostOnly(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return raw
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
