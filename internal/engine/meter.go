// Package engine 是测量引擎：只负责“发流量、记字节/延迟”，不做判定。
package engine

import (
	"context"
	"sync/atomic"
	"time"
)

// Sink 接收进度。所有引擎对它容忍 nil。
type Sink interface {
	Status(text string)              // 当前阶段
	Live(mbps float64, frac float64) // 实时速度（Mbps）与本阶段进度 0..1；mbps<0 表示无速度可显示
}

type nopSink struct{}

func (nopSink) Status(string)         {}
func (nopSink) Live(float64, float64) {}

func sinkOr(s Sink) Sink {
	if s == nil {
		return nopSink{}
	}
	return s
}

// Sample 是累计字节随时间的一个点。
type Sample struct {
	T float64 // 自开始的秒
	B int64   // 累计字节
}

// Meter 线程安全的字节计数器。
type Meter struct{ n atomic.Int64 }

func (m *Meter) Add(n int)    { m.n.Add(int64(n)) }
func (m *Meter) Total() int64 { return m.n.Load() }

// Sampler 每 100ms 记录一次累计字节，直到 ctx 结束。返回前补一个终点样本。
func Sampler(ctx context.Context, m *Meter, start time.Time, every time.Duration, tick func([]Sample)) []Sample {
	var out []Sample
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			s := Sample{time.Since(start).Seconds(), m.Total()}
			out = append(out, s)
			return out
		case <-tk.C:
			s := Sample{time.Since(start).Seconds(), m.Total()}
			out = append(out, s)
			if tick != nil {
				tick(out)
			}
		}
	}
}

// bytesAt 在累计样本里线性插值 t 时刻的累计字节。
func bytesAt(cum []Sample, t float64) float64 {
	if len(cum) == 0 {
		return 0
	}
	if t <= cum[0].T {
		if cum[0].T <= 0 {
			return float64(cum[0].B)
		}
		return float64(cum[0].B) * t / cum[0].T
	}
	for i := 1; i < len(cum); i++ {
		if t <= cum[i].T {
			a, b := cum[i-1], cum[i]
			if b.T == a.T {
				return float64(b.B)
			}
			return float64(a.B) + float64(b.B-a.B)*(t-a.T)/(b.T-a.T)
		}
	}
	return float64(cum[len(cum)-1].B)
}

// Window 计算稳态窗口 [warm, end] 内的吞吐（Mbps）。
// end 取 min(end, 最后样本时刻)；窗口不足 0.5s 时返回 ok=false。
func Window(cum []Sample, warm, end float64) (mbps, winSec float64, winBytes int64, ok bool) {
	if len(cum) == 0 {
		return 0, 0, 0, false
	}
	if last := cum[len(cum)-1].T; end > last {
		end = last
	}
	if end-warm < 0.5 {
		return 0, 0, 0, false
	}
	b := bytesAt(cum, end) - bytesAt(cum, warm)
	return b * 8 / (end - warm) / 1e6, end - warm, int64(b), true
}

// Series 把累计样本转成 [t, 近 1s 瞬时 Mbps]，约每 0.5s 一点，用于曲线与原始记录。
func Series(cum []Sample) [][2]float64 {
	var out [][2]float64
	if len(cum) == 0 {
		return out
	}
	end := cum[len(cum)-1].T
	for t := 0.5; t <= end+1e-9; t += 0.5 {
		lo := t - 1
		if lo < 0 {
			lo = 0
		}
		if t-lo < 0.25 {
			continue
		}
		b := bytesAt(cum, t) - bytesAt(cum, lo)
		out = append(out, [2]float64{round2(t), round2(b * 8 / (t - lo) / 1e6)})
	}
	return out
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

// trailingMbps 取最近 win 秒的速度（实时显示用）。
func trailingMbps(cum []Sample, win float64) float64 {
	if len(cum) == 0 {
		return 0
	}
	t := cum[len(cum)-1].T
	lo := t - win
	if lo < 0 {
		lo = 0
	}
	if t-lo < 0.2 {
		return 0
	}
	return (bytesAt(cum, t) - bytesAt(cum, lo)) * 8 / (t - lo) / 1e6
}
