package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/judge"
	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/netx"
	"github.com/Marty-G1tHub/tracewing/internal/ping"
)

// ParseTargets 把用户粘贴的文本拆成 IPv4 列表；非法/内网/保留地址进 rejected 并写明原因。
func ParseTargets(text string) (ok []string, rejected []string) {
	seen := map[string]bool{}
	f := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '，' || r == '；'
	})
	for _, s := range f {
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() == nil {
			rejected = append(rejected, s+"（不是 IPv4 地址）")
			continue
		}
		if _, good := netx.ValidPublicIPv4(s); !good {
			rejected = append(rejected, s+"（"+netx.Classify(ip)+"：不是可在公网测量的地址）")
			continue
		}
		if !seen[ip.String()] {
			seen[ip.String()] = true
			ok = append(ok, ip.String())
		}
	}
	return
}

// RunTestIP 对每个目标：ICMP/TCP 多次往返 → 统计 →（可选）逐跳去程 →（可选）归属查询。
func RunTestIP(ctx context.Context, p *netx.Path, prm model.IPParams, ips []string, env *model.Env, pg ping.Pinger, sink Sink) (ts []model.IPTarget, traceTrusted bool) {
	sink = sinkOr(sink)
	traceTrusted = env == nil || (!env.TUN && !env.FakeIP)
	var lk *lookup
	if prm.Lookup {
		lk = &lookup{c: lookupClient(p), cache: map[string]lkInfo{}}
	}
	for i, s := range ips {
		if ctx.Err() != nil {
			break
		}
		ip := net.ParseIP(s)
		t := model.IPTarget{IP: s, Samples: []float64{}}
		sink.Status(fmt.Sprintf("测试 %s（%d/%d）：延迟", s, i+1, len(ips)))
		timeout := time.Duration(prm.TimeoutMs) * time.Millisecond
		for n := 0; n < prm.Count && ctx.Err() == nil; n++ {
			r := pg.Ping(ctx, ip, 0, timeout)
			if r.Kind == ping.Echo {
				t.Samples = append(t.Samples, round2(float64(r.RTT)/float64(time.Millisecond)))
			} else {
				t.Samples = append(t.Samples, -1)
				if r.Kind == ping.Error && t.Err == "" {
					t.Err = r.Err
				}
			}
			sink.Live(-1, (float64(i)+float64(n+1)/float64(prm.Count))/float64(len(ips)))
			sleep(ctx, time.Duration(prm.IntervalMs)*time.Millisecond)
		}
		l := judge.LatencyStats(t.Samples)
		t.Sent, t.Recv, t.LossPct = l.Sent, l.Recv, l.LossPct
		t.P50, t.P95, t.Jitter, t.Avg, t.Min, t.Max = l.P50, l.P95, l.Jitter, l.Avg, l.Min, l.Max
		t.NoReply = t.Sent > 0 && t.Recv == 0
		if t.Jitter != nil {
			t.Stability = judge.StabilityLabel(*t.Jitter)
		}
		if prm.Trace && pg.Name() != "tcp" && ctx.Err() == nil {
			sink.Status(fmt.Sprintf("测试 %s（%d/%d）：去程", s, i+1, len(ips)))
			t.Hops, t.Reached = trace(ctx, pg, ip, prm.Hops)
		}
		if prm.Lookup && ctx.Err() == nil {
			sink.Status(fmt.Sprintf("测试 %s（%d/%d）：归属查询", s, i+1, len(ips)))
			info := lk.identity(ctx, s)
			t.City, t.Region, t.Country, t.ASN, t.Holder = info.City, info.Region, info.Country, info.ASN, info.Holder
			for h := range t.Hops {
				if t.Hops[h].IP != "" {
					a := lk.asnOnly(ctx, t.Hops[h].IP)
					t.Hops[h].ASN, t.Hops[h].Holder = a.ASN, a.Holder
				}
			}
		}
		t.PathSum = PathSummary(t.Hops)
		ts = append(ts, t)
	}
	return
}

// trace 并发探测 TTL 1..max，截到第一个“目标本身回应”的跳。
// 每跳一次探测；中间跳 * 只表示该路由器不回 TTL 超时，不算丢包。
func trace(ctx context.Context, pg ping.Pinger, dst net.IP, max int) ([]model.Hop, bool) {
	if max <= 0 {
		max = 18
	}
	hops := make([]model.Hop, max)
	reached := make([]bool, max)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for ttl := 1; ttl <= max; ttl++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(ttl int) {
			defer wg.Done()
			defer func() { <-sem }()
			h := model.Hop{TTL: ttl, RTT: -1}
			r := pg.Ping(ctx, dst, ttl, 1500*time.Millisecond)
			if r.From != nil && (r.Kind == ping.Echo || r.Kind == ping.Expired || r.Kind == ping.Unreach) {
				h.IP = r.From.String()
				h.RTT = round2(float64(r.RTT) / float64(time.Millisecond))
			}
			reached[ttl-1] = r.Kind == ping.Echo
			hops[ttl-1] = h
		}(ttl)
	}
	wg.Wait()
	for i := range hops {
		if reached[i] {
			return hops[:i+1], true
		}
	}
	// 没到终点：去掉尾部连续无回应的跳
	n := len(hops)
	for n > 0 && hops[n-1].IP == "" {
		n--
	}
	return hops[:n], false
}

// PathSummary：相邻同 ASN 合并；超过 6 段只显示前 5 + … + 终点。
func PathSummary(hops []model.Hop) string {
	var segs []string
	last := ""
	for _, h := range hops {
		if h.IP == "" {
			continue
		}
		label := h.ASN
		if label == "" {
			label = h.IP
		} else if h.Holder != "" {
			label = h.ASN + " " + h.Holder
		}
		if label == last {
			continue
		}
		segs = append(segs, label)
		last = label
	}
	if len(segs) > 6 {
		segs = append(append(append([]string{}, segs[:5]...), "…"), segs[len(segs)-1])
	}
	return strings.Join(segs, " → ")
}

// TestIPVerdict 汇报文案：同城才比、终点无回包≠线路坏、去程可信度。
func TestIPVerdict(ts []model.IPTarget, trusted bool, method string) []string {
	v := judge.SameCityBest(ts)
	for _, t := range ts {
		if t.NoReply {
			v = append(v, fmt.Sprintf("%s 没有回应 %s（目标可能屏蔽了探测，不等于线路坏）。", t.IP, map[string]string{"icmp": "ICMP", "tcp": "TCP 握手"}[method]))
		}
	}
	if !trusted {
		v = append(v, "本机处于 TUN/Fake-IP 环境：延迟数字仅供参考，去程各跳不可信（可能被代理软件收掉了中间骨干）。")
	}
	return v
}

// ---------- 归属查询（失败静默留空；查的是目标归属，与本机出口无关）----------

type lkInfo struct{ City, Region, Country, ASN, Holder string }

type lookup struct {
	c     *http.Client
	mu    sync.Mutex
	cache map[string]lkInfo
}

func lookupClient(p *netx.Path) *http.Client {
	c := p.Client()
	cc := *c
	cc.Timeout = 8 * time.Second
	return &cc
}

func (l *lookup) getJSON(ctx context.Context, url string, v any) bool {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "tracewing/1")
	resp, err := l.c.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return json.Unmarshal(b, v) == nil
}

func (l *lookup) asnOnly(ctx context.Context, ip string) lkInfo {
	l.mu.Lock()
	if v, ok := l.cache["a"+ip]; ok {
		l.mu.Unlock()
		return v
	}
	l.mu.Unlock()
	var r struct {
		Data struct {
			ASNs []struct {
				ASN    int    `json:"asn"`
				Holder string `json:"holder"`
			} `json:"asns"`
		} `json:"data"`
	}
	var out lkInfo
	if l.getJSON(ctx, "https://stat.ripe.net/data/prefix-overview/data.json?resource="+ip, &r) && len(r.Data.ASNs) > 0 {
		out.ASN = fmt.Sprintf("AS%d", r.Data.ASNs[0].ASN)
		out.Holder = r.Data.ASNs[0].Holder
	}
	l.mu.Lock()
	l.cache["a"+ip] = out
	l.mu.Unlock()
	return out
}

func (l *lookup) identity(ctx context.Context, ip string) lkInfo {
	out := l.asnOnly(ctx, ip)
	var r struct{ City, Region, Country string }
	if l.getJSON(ctx, "https://ipinfo.io/"+ip+"/json", &r) {
		out.City, out.Region, out.Country = r.City, r.Region, r.Country
	}
	return out
}

// SortByP50 不改变输入，仅用于展示：有回包者按 P50 升序。
func SortByP50(ts []model.IPTarget) []model.IPTarget {
	out := append([]model.IPTarget(nil), ts...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].P50 == nil || out[j].P50 == nil {
			return out[i].P50 != nil
		}
		return *out[i].P50 < *out[j].P50
	})
	return out
}
