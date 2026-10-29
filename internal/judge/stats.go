// Package judge 是纯函数：输入测量结果，输出判定。不碰网络、不碰磁盘，便于单测。
package judge

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Percentile 线性插值分位数（p∈[0,100]）。空输入返回 NaN。
func Percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if len(s) == 1 {
		return s[0]
	}
	pos := p / 100 * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

func Median(xs []float64) float64 { return Percentile(xs, 50) }

// MinMedMax 返回 min/median/max；空输入返回 ok=false。
func MinMedMax(xs []float64) (min, med, max float64, ok bool) {
	if len(xs) == 0 {
		return 0, 0, 0, false
	}
	min, max = xs[0], xs[0]
	for _, x := range xs {
		min = math.Min(min, x)
		max = math.Max(max, x)
	}
	return min, Median(xs), max, true
}

// MeanAbsDiff 相邻样本差的平均绝对值（vps echo 抖动口径）。
func MeanAbsDiff(xs []float64) float64 {
	if len(xs) < 2 {
		return math.NaN()
	}
	var s float64
	for i := 1; i < len(xs); i++ {
		s += math.Abs(xs[i] - xs[i-1])
	}
	return s / float64(len(xs)-1)
}

// Ptr 把有限数转成指针；NaN/Inf → nil（JSON 里就是 null，而不是编造的 0）。
func Ptr(f float64) *float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}

// Round1 保留 1 位小数。
func Round1(f float64) float64 { return math.Round(f*10) / 10 }

// Round2 保留 2 位小数。
func Round2(f float64) float64 { return math.Round(f*100) / 100 }

// MBps 由 Mbps 换算 MB/s（1 MB/s = 8 Mbps）。
func MBps(mbps float64) float64 { return mbps / 8 }

// Fmt 同时给 Mbps 与 MB/s。nil → “—”。
func Fmt(mbps *float64) string {
	if mbps == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f Mbps (%.2f MB/s)", *mbps, *mbps/8)
}

// StabilityLabel 按抖动（P95−P50, ms）给稳定度标签：<15 很稳；<30 通常不错；≤50 需要关注；>50 抖动偏大。
func StabilityLabel(jitter float64) string {
	switch {
	case jitter < 15:
		return "很稳"
	case jitter < 30:
		return "通常不错"
	case jitter <= 50:
		return "需要关注"
	}
	return "抖动偏大"
}

// Period 按本机本地时间分时段：peak 18:00–00:30；off_peak 00:30–08:00；其余 normal。
func Period(t time.Time) string {
	m := t.Hour()*60 + t.Minute()
	switch {
	case m >= 18*60 || m < 30:
		return "peak"
	case m < 8*60:
		return "off_peak"
	}
	return "normal"
}

// Lat 是一组往返时延样本的统计（毫秒；<0 的样本=丢失）。
type Lat struct {
	Sent, Recv int
	LossPct    float64
	P50, P95   *float64
	Jitter     *float64 // P95 − P50
	Avg, Min   *float64
	Max        *float64
}

func LatencyStats(samples []float64) Lat {
	l := Lat{Sent: len(samples)}
	var ok []float64
	var sum float64
	for _, s := range samples {
		if s >= 0 {
			ok = append(ok, s)
			sum += s
		}
	}
	l.Recv = len(ok)
	if l.Sent > 0 {
		l.LossPct = Round1(float64(l.Sent-l.Recv) / float64(l.Sent) * 100)
	}
	if len(ok) == 0 {
		return l
	}
	mn, md, mx, _ := MinMedMax(ok)
	p95 := Percentile(ok, 95)
	l.P50, l.P95 = Ptr(Round2(md)), Ptr(Round2(p95))
	l.Jitter = Ptr(Round2(p95 - md))
	l.Avg, l.Min, l.Max = Ptr(Round2(sum/float64(len(ok)))), Ptr(Round2(mn)), Ptr(Round2(mx))
	return l
}
