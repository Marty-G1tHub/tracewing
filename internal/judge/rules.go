package judge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Marty-G1tHub/tracewing/internal/model"
)

// ---------- 吞吐档位 ----------

// UtilBand 正向利用率档：≥0.85 near_cap；≥0.75 normal_with_overhead；≥0.60 observe；其余 possibly_abnormal。
func UtilBand(u float64) string {
	switch {
	case u >= 0.85:
		return "near_cap"
	case u >= 0.75:
		return "normal_with_overhead"
	case u >= 0.60:
		return "observe"
	}
	return "possibly_abnormal"
}

// GainBand 反向多流增益档：<1.20 similar；<1.80 some_single_flow_limit；其余 strong_single_flow_limit。
func GainBand(g float64) string {
	switch {
	case g < 1.20:
		return "similar"
	case g < 1.80:
		return "some_single_flow_limit"
	}
	return "strong_single_flow_limit"
}

// ---------- 宽带 ----------

// SummarizeRounds 只用 ok 且 mbps>0 的轮次求 min/median/max；没有就全 nil（“失败”，而不是 0）。
func SummarizeRounds(rs []model.Round) (min, med, max *float64) {
	var xs []float64
	for _, r := range rs {
		if r.OK && r.Mbps > 0 {
			xs = append(xs, r.Mbps)
		}
	}
	a, m, b, ok := MinMedMax(xs)
	if !ok {
		return nil, nil, nil
	}
	return Ptr(Round2(a)), Ptr(Round2(m)), Ptr(Round2(b))
}

func regionName(r string) string {
	switch r {
	case "cn":
		return "国内"
	case "global":
		return "境外"
	}
	return r
}

// BroadbandVerdict 结论句：Mbps 与 MB/s 同给；失败写“失败”；有套餐才写利用率。
func BroadbandVerdict(groups []model.Group) string {
	var parts []string
	one := func(name string, p *model.Phase) {
		if p == nil {
			return
		}
		if p.Median == nil {
			parts = append(parts, name+"失败")
			return
		}
		s := fmt.Sprintf("%s中位 %.2f Mbps (%.2f MB/s", name, *p.Median, *p.Median/8)
		if p.UtilPct != nil {
			s += fmt.Sprintf(", 套餐利用率 %.1f%%", *p.UtilPct)
		}
		parts = append(parts, s+")")
	}
	for _, g := range groups {
		n := regionName(g.Region)
		one(n+"下行", g.Down)
		one(n+"上行", g.Up)
	}
	if len(parts) == 0 {
		return "未执行任何阶段"
	}
	return strings.Join(parts, "；")
}

// BroadbandOK 任一阶段成功即算整次成功。
func BroadbandOK(groups []model.Group) bool {
	for _, g := range groups {
		for _, p := range []*model.Phase{g.Down, g.Up} {
			if p != nil && p.Median != nil {
				return true
			}
		}
	}
	return false
}

// ---------- 测试 IP ----------

// SameCityBest 只在“同一城市”内比 P50；城市不同就分组说，不排总名次；
// 没有任何回包 → “排不出延迟”。
func SameCityBest(ts []model.IPTarget) []string {
	type key struct{ city, country string }
	groups := map[key][]model.IPTarget{}
	var order []key
	replied := 0
	for _, t := range ts {
		if t.P50 == nil {
			continue
		}
		replied++
		k := key{t.City, t.Country}
		if t.City == "" {
			k = key{"", ""} // 未知地点：不参与比较
		}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], t)
	}
	if replied == 0 {
		return []string{"所有地址都没有回包，排不出延迟（目标不回 ICMP ≠ 线路坏）。"}
	}
	var out []string
	for _, k := range order {
		g := groups[k]
		sort.Slice(g, func(i, j int) bool { return *g[i].P50 < *g[j].P50 })
		if k.city == "" {
			out = append(out, fmt.Sprintf("地点未知的 %d 个地址不比较 P50（未查到归属，无法确认是否同城）。", len(g)))
			continue
		}
		loc := k.city
		if k.country != "" {
			loc += "·" + k.country
		}
		if len(g) == 1 {
			out = append(out, fmt.Sprintf("%s：仅 1 个有回包地址 %s（P50 %.1f ms），无可比对象。", loc, g[0].IP, *g[0].P50))
		} else {
			out = append(out, fmt.Sprintf("%s：有回包的 %d 个里 P50 最低的是 %s（%.1f ms；最高 %.1f ms）。", loc, len(g), g[0].IP, *g[0].P50, *g[len(g)-1].P50))
		}
	}
	return out
}

// ---------- VPS 诊断 ----------

// Thresholds 启发式阈值。默认值是经验值，**未二次复核**，可调，不是定论。
type Thresholds struct {
	StealPct    float64 // ≥ 判宿主超卖
	MemAvailPct float64 // < 判内存压力
	CPUBusyPct  float64 // ≥ 判 CPU 瓶颈
	CPUIdlePct  float64 // ≤ 判 CPU 瓶颈
	UtilLimit   float64 // ≥ 判已到本地上行上限（仅设了 cap 时）
	GainStrong  float64 // ≥ 判反向单流限制
	PeakDropPct float64 // 高峰比基线降幅 ≥ 判线路拥塞
	HealthyMin  float64 // 健康：单流上/下行都 ≥ 该值 Mbps
	HealthyDrop float64 // 健康：相对基线降幅 <
	BaselineMin int     // 基线所需非高峰记录数
}

var DefaultThresholds = Thresholds{
	StealPct: 10, MemAvailPct: 10, CPUBusyPct: 85, CPUIdlePct: 15,
	UtilLimit: 0.75, GainStrong: 1.80, PeakDropPct: 40,
	HealthyMin: 30, HealthyDrop: 20, BaselineMin: 3,
}

type DiagIn struct {
	Med       map[string]*float64 // fwd1 rev1 fwd4 rev4 的中位；nil=该组失败
	Res       *model.Resources    // 仅服务端模式可能有
	UploadCap *float64
	Period    string
	Baseline  *model.Baseline
	Th        Thresholds
}

func v(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

// ForwardUtil 正向利用率 = 正向（本机→服务器）最好的中位 / 上行上限。无上限返回 nil。
func ForwardUtil(med map[string]*float64, cap *float64) *float64 {
	if cap == nil || *cap <= 0 {
		return nil
	}
	best := 0.0
	for _, k := range []string{"fwd1", "fwd4"} {
		if x, ok := v(med[k]); ok && x > best {
			best = x
		}
	}
	if best == 0 {
		return nil
	}
	return Ptr(best / *cap)
}

// ReverseGainOf 反向 4 流中位 / 单流中位。
func ReverseGainOf(med map[string]*float64) *float64 {
	a, ok1 := v(med["rev4"])
	b, ok2 := v(med["rev1"])
	if !ok1 || !ok2 || b <= 0 {
		return nil
	}
	return Ptr(a / b)
}

// BuildBaseline 由历史记录算非高峰基线；记录数不足返回 nil。
func BuildBaseline(hist []HistRec, th Thresholds) *model.Baseline {
	by := map[string][]float64{}
	n := 0
	for _, h := range hist {
		if h.Period != "off_peak" {
			continue
		}
		n++
		for k, x := range h.Med {
			by[k] = append(by[k], x)
		}
	}
	if n < th.BaselineMin {
		return nil
	}
	b := &model.Baseline{N: n, Median: map[string]float64{}}
	for k, xs := range by {
		b.Median[k] = Round2(Median(xs))
	}
	return b
}

// HistRec 是历史里参与基线的最小信息。
type HistRec struct {
	Period string
	Med    map[string]float64
}

// DropVsBaseline 返回各 key 相对基线的降幅%（正=变慢），以及最大降幅。
func DropVsBaseline(b *model.Baseline, med map[string]*float64) (map[string]float64, float64, bool) {
	if b == nil {
		return nil, 0, false
	}
	drops := map[string]float64{}
	worst, any := -1e9, false
	for k, base := range b.Median {
		cur, ok := v(med[k])
		if !ok || base <= 0 {
			continue
		}
		d := (base - cur) / base * 100
		drops[k] = Round1(d)
		if d > worst {
			worst = d
		}
		any = true
	}
	return drops, worst, any
}

var keyName = map[string]string{"fwd1": "正向单流", "rev1": "反向单流", "fwd4": "正向4流", "rev4": "反向4流"}

// Diagnose 启发式诊断，优先级自上而下（宿主超卖→内存→CPU→本地上行上限→反向单流限制→线路拥塞→健康→部分/证据不足/失败）。
// 阈值是经验值，未二次复核；LIKELY_PROTOCOL_SPECIFIC 需要协议 A/B 对照，本程序不产生。
func Diagnose(in DiagIn) model.Diagnosis {
	d := model.Diagnosis{Confidence: "low", Evidence: []string{}, CounterEvidence: []string{}}
	d.Limitations = []string{"诊断阈值为启发式，不是定论。"}
	th := in.Th
	have := 0
	for _, k := range []string{"fwd1", "rev1", "fwd4", "rev4"} {
		if _, ok := v(in.Med[k]); ok {
			have++
		}
	}
	if have == 0 {
		d.Code = "FAILED"
		d.Evidence = append(d.Evidence, "四组吞吐测量均无有效结果。")
		return d
	}
	missing := 4 - have
	if missing > 0 {
		d.Limitations = append(d.Limitations, fmt.Sprintf("有 %d 组吞吐测量失败，结论证据不全。", missing))
	}
	res := in.Res
	if res != nil && res.Available {
		d.Limitations = append(d.Limitations, "服务器资源为 /proc 粗采样，非绝对结论。")
		if x, ok := v(res.StealPct); ok && x >= th.StealPct {
			d.Code, d.Confidence = "LIKELY_HOST_OVERSUBSCRIPTION", "medium"
			d.Evidence = append(d.Evidence, fmt.Sprintf("测试期间服务器 CPU steal %.1f%% ≥ %.0f%%，疑似宿主机超卖。", x, th.StealPct))
			return d
		}
		if x, ok := v(res.MemAvailPct); ok && x < th.MemAvailPct {
			d.Code, d.Confidence = "LIKELY_MEMORY_PRESSURE", "medium"
			d.Evidence = append(d.Evidence, fmt.Sprintf("服务器可用内存 %.1f%% < %.0f%%。", x, th.MemAvailPct))
			return d
		}
		busy, okb := v(res.BusyPct)
		idle, oki := v(res.IdlePct)
		if (okb && busy >= th.CPUBusyPct) || (oki && idle <= th.CPUIdlePct) {
			d.Code, d.Confidence = "LIKELY_SERVER_CPU_BOTTLENECK", "medium"
			d.Evidence = append(d.Evidence, fmt.Sprintf("测试期间服务器 CPU busy %.1f%% / idle %.1f%%。", busy, idle))
			return d
		}
		d.CounterEvidence = append(d.CounterEvidence, "服务器 CPU / 内存 / steal 未见异常。")
	} else {
		d.Limitations = append(d.Limitations, "没有服务器资源数据（仅“自带服务端”模式可采集），未评估宿主超卖 / 内存 / CPU 瓶颈。")
	}
	if u := ForwardUtil(in.Med, in.UploadCap); u != nil && *u >= th.UtilLimit {
		d.Code, d.Confidence = "EXPECTED_LOCAL_UPLINK_LIMIT", "medium"
		d.Evidence = append(d.Evidence, fmt.Sprintf("正向最好中位占你填写的上行上限 %.0f%%（≥%.0f%%）：更像已到本地上行上限。", *u*100, th.UtilLimit*100))
		return d
	}
	if g := ReverseGainOf(in.Med); g != nil && *g >= th.GainStrong {
		d.Code, d.Confidence = "LIKELY_REVERSE_SINGLE_FLOW_LIMITATION", "medium"
		d.Evidence = append(d.Evidence, fmt.Sprintf("反向 4 流是单流的 %.2f 倍（≥%.2f）。", *g, th.GainStrong))
		d.Limitations = append(d.Limitations, "单看多流增益不能断言根因（可能是单连接拥塞窗口、每流整形、代理/封装行为等）。")
		return d
	}
	drops, worst, hasDrop := DropVsBaseline(in.Baseline, in.Med)
	if in.Period == "peak" && hasDrop && worst >= th.PeakDropPct {
		d.Code, d.Confidence = "LIKELY_LINE_CONGESTION", "medium"
		for k, x := range drops {
			d.Evidence = append(d.Evidence, fmt.Sprintf("%s 比非高峰基线低 %.0f%%。", keyName[k], x))
		}
		sort.Strings(d.Evidence)
		d.Evidence = append([]string{fmt.Sprintf("高峰时段，最大降幅 %.0f%% ≥ %.0f%%。", worst, th.PeakDropPct)}, d.Evidence...)
		return d
	}
	if in.Baseline == nil {
		d.Limitations = append(d.Limitations, "非高峰历史记录不足（需 ≥3 次同服务器记录），无法判断线路拥塞。")
	}
	f1, okf := v(in.Med["fwd1"])
	r1, okr := v(in.Med["rev1"])
	if okf && okr && f1 >= th.HealthyMin && r1 >= th.HealthyMin && (!hasDrop || worst < th.HealthyDrop) {
		d.Code = "HEALTHY"
		d.Confidence = "medium"
		if !hasDrop {
			d.Confidence = "low"
		}
		d.Evidence = append(d.Evidence, fmt.Sprintf("单流正向 %.1f / 反向 %.1f Mbps，均 ≥ %.0f Mbps。", f1, r1, th.HealthyMin))
		if hasDrop {
			d.Evidence = append(d.Evidence, fmt.Sprintf("相对非高峰基线最大降幅 %.0f%% < %.0f%%。", worst, th.HealthyDrop))
		}
		return d
	}
	if missing > 0 {
		d.Code = "PARTIAL"
		d.Evidence = append(d.Evidence, fmt.Sprintf("%d/4 组吞吐有效。", have))
		return d
	}
	d.Code = "INSUFFICIENT_EVIDENCE"
	d.Evidence = append(d.Evidence, "四组吞吐都有结果，但没有触发任何已知模式，也不满足“健康”条件。")
	return d
}

// StatusOf PASS/PARTIAL/FAILED：能产出吞吐数据→PASS；部分→PARTIAL；全无→FAILED。
func StatusOf(med map[string]*float64) string {
	n := 0
	for _, k := range []string{"fwd1", "rev1", "fwd4", "rev4"} {
		if med[k] != nil {
			n++
		}
	}
	switch n {
	case 0:
		return "FAILED"
	case 4:
		return "PASS"
	}
	return "PARTIAL"
}
