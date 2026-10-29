// Package route 做逐跳路由探测（traceroute）与“线路类型”判定。
// 探测在能发 TTL 的一端完成（agent 或本机 Windows），判定一律在客户端用版本化前缀表完成。
package route

import (
	"context"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/ping"
)

// Hop 是某一跳的汇总。IP 为空表示这一跳所有探测都没有回应（常见于被限速或屏蔽 ICMP 的路由器）。
type Hop struct {
	TTL  int     `json:"ttl"`
	IP   string  `json:"ip,omitempty"`
	RTT  float64 `json:"rtt_ms,omitempty"` // 该跳回应中的最小往返（ms）
	Sent int     `json:"sent"`
	Got  int     `json:"got"`
}

// Trace 是一次到某目标的探测结果，只含原始跳数据，不含任何判定。
type Trace struct {
	Target  string `json:"target"`       // 目标 IP
	ID      string `json:"id,omitempty"` // 固定目标 id；“client”表示请求方自己
	Label   string `json:"label,omitempty"`
	Reached bool   `json:"reached"` // 目标本身是否回应
	Hops    []Hop  `json:"hops"`
	Method  string `json:"method"` // 探测方式，如 icmp(raw)
	Note    string `json:"note,omitempty"`
}

// Options 控制探测强度。零值使用默认：最大 30 跳、每跳 2 个探测、每个等 1.2 秒、并发 8。
type Options struct {
	MaxTTL, Probes, Parallel int
	Timeout                  time.Duration
}

func (o *Options) fill() {
	if o.MaxTTL <= 0 || o.MaxTTL > 40 {
		o.MaxTTL = 30
	}
	if o.Probes <= 0 || o.Probes > 5 {
		o.Probes = 2
	}
	if o.Parallel <= 0 || o.Parallel > 16 {
		o.Parallel = 8
	}
	if o.Timeout <= 0 {
		o.Timeout = 1200 * time.Millisecond
	}
}

// Run 对 dst 做逐跳探测。各 TTL 并发探测以缩短总耗时，最后按 TTL 排序，并在目标首次回应处截断。
func Run(ctx context.Context, p ping.Pinger, dst net.IP, opt Options) Trace {
	opt.fill()
	tr := Trace{Target: dst.String(), Method: p.Name()}
	hops := make([]Hop, opt.MaxTTL)
	reachedAt := make([]bool, opt.MaxTTL)
	sem := make(chan struct{}, opt.Parallel)
	var wg sync.WaitGroup
	for i := 0; i < opt.MaxTTL; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			ttl := i + 1
			h := Hop{TTL: ttl}
			counts := map[string]int{}
			for k := 0; k < opt.Probes; k++ {
				if ctx.Err() != nil {
					break
				}
				h.Sent++
				r := p.Ping(ctx, dst, ttl, opt.Timeout)
				switch r.Kind {
				case ping.Echo, ping.Expired, ping.Unreach:
					h.Got++
					ms := float64(r.RTT.Microseconds()) / 1000
					if h.RTT == 0 || ms < h.RTT {
						h.RTT = ms
					}
					if r.From != nil {
						counts[r.From.String()]++
					}
					if r.Kind == ping.Echo {
						reachedAt[i] = true
					}
				}
			}
			best, bn := "", 0
			for ip, n := range counts {
				if n > bn || (n == bn && ip < best) {
					best, bn = ip, n
				}
			}
			h.IP = best
			hops[i] = h
		}(i)
	}
	wg.Wait()
	end := len(hops)
	for i, ok := range reachedAt {
		if ok {
			tr.Reached = true
			end = i + 1
			break
		}
	}
	hops = hops[:end]
	if !tr.Reached { // 去掉末尾连续无回应的跳
		for len(hops) > 0 && hops[len(hops)-1].IP == "" {
			hops = hops[:len(hops)-1]
		}
	}
	sort.Slice(hops, func(i, j int) bool { return hops[i].TTL < hops[j].TTL })
	tr.Hops = hops
	return tr
}
