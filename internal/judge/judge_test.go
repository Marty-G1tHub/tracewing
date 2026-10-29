package judge

import (
	"math"
	"testing"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/model"
)

func f(x float64) *float64 { return &x }

func TestPercentile(t *testing.T) {
	xs := []float64{10, 20, 30, 40, 50}
	if Percentile(xs, 50) != 30 || Percentile(xs, 0) != 10 || Percentile(xs, 100) != 50 {
		t.Fatal("percentile basic")
	}
	if got := Percentile(xs, 95); math.Abs(got-48) > 1e-9 {
		t.Fatalf("p95=%v", got)
	}
	if !math.IsNaN(Percentile(nil, 50)) {
		t.Fatal("empty should be NaN")
	}
}

func TestStabilityAndBands(t *testing.T) {
	cases := map[float64]string{0: "很稳", 14.9: "很稳", 15: "通常不错", 29.9: "通常不错", 30: "需要关注", 50: "需要关注", 50.1: "抖动偏大"}
	for j, w := range cases {
		if got := StabilityLabel(j); got != w {
			t.Errorf("jitter %v: %s want %s", j, got, w)
		}
	}
	if UtilBand(0.85) != "near_cap" || UtilBand(0.8) != "normal_with_overhead" || UtilBand(0.6) != "observe" || UtilBand(0.59) != "possibly_abnormal" {
		t.Error("util band")
	}
	if GainBand(1.19) != "similar" || GainBand(1.2) != "some_single_flow_limit" || GainBand(1.8) != "strong_single_flow_limit" {
		t.Error("gain band")
	}
}

func TestPeriod(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 27, h, m, 0, 0, time.Local) }
	want := map[[2]int]string{{18, 0}: "peak", {23, 59}: "peak", {0, 29}: "peak", {0, 30}: "off_peak", {7, 59}: "off_peak", {8, 0}: "normal", {17, 59}: "normal"}
	for hm, w := range want {
		if got := Period(at(hm[0], hm[1])); got != w {
			t.Errorf("%v: %s want %s", hm, got, w)
		}
	}
}

func TestSummarizeRoundsOnlyOK(t *testing.T) {
	rs := []model.Round{{OK: true, Mbps: 100}, {OK: false, Mbps: 5}, {OK: true, Mbps: 300}, {OK: true, Mbps: 200}}
	mn, md, mx := SummarizeRounds(rs)
	if *mn != 100 || *md != 200 || *mx != 300 {
		t.Fatalf("%v %v %v", *mn, *md, *mx)
	}
	a, b, c := SummarizeRounds([]model.Round{{OK: false, Mbps: 1}})
	if a != nil || b != nil || c != nil {
		t.Fatal("all failed must be nil")
	}
}

func TestVerdict(t *testing.T) {
	g := []model.Group{{Region: "cn", Down: &model.Phase{}, Up: &model.Phase{Median: f(97.22)}}}
	got := BroadbandVerdict(g)
	if got != "国内下行失败；国内上行中位 97.22 Mbps (12.15 MB/s)" {
		t.Fatal(got)
	}
	if BroadbandOK(g) != true || BroadbandOK([]model.Group{{Down: &model.Phase{}}}) != false {
		t.Fatal("ok")
	}
}

func TestSameCityBest(t *testing.T) {
	ts := []model.IPTarget{
		{IP: "1.1.1.1", City: "Los Angeles", Country: "US", P50: f(160)},
		{IP: "2.2.2.2", City: "Los Angeles", Country: "US", P50: f(155)},
		{IP: "3.3.3.3", City: "Tokyo", Country: "JP", P50: f(50)},
		{IP: "4.4.4.4", City: "Tokyo", Country: "JP"},
	}
	out := SameCityBest(ts)
	if len(out) != 2 {
		t.Fatalf("%v", out)
	}
	if want := "Los Angeles·US：有回包的 2 个里 P50 最低的是 2.2.2.2（155.0 ms；最高 160.0 ms）。"; out[0] != want {
		t.Fatalf("%q", out[0])
	}
	none := SameCityBest([]model.IPTarget{{IP: "1.1.1.1"}})
	if len(none) != 1 {
		t.Fatal("none")
	}
}

func med(f1, r1, f4, r4 float64) map[string]*float64 {
	m := map[string]*float64{}
	if f1 >= 0 {
		m["fwd1"] = &f1
	}
	if r1 >= 0 {
		m["rev1"] = &r1
	}
	if f4 >= 0 {
		m["fwd4"] = &f4
	}
	if r4 >= 0 {
		m["rev4"] = &r4
	}
	return m
}

func TestDiagnosePriority(t *testing.T) {
	th := DefaultThresholds
	// 全失败
	if d := Diagnose(DiagIn{Med: med(-1, -1, -1, -1), Th: th}); d.Code != "FAILED" {
		t.Error(d.Code)
	}
	// 宿主超卖优先于一切
	res := &model.Resources{Available: true, StealPct: f(12), BusyPct: f(99), IdlePct: f(0), MemAvailPct: f(5)}
	if d := Diagnose(DiagIn{Med: med(100, 20, 100, 100), Res: res, Th: th}); d.Code != "LIKELY_HOST_OVERSUBSCRIPTION" {
		t.Error(d.Code)
	}
	res2 := &model.Resources{Available: true, StealPct: f(1), BusyPct: f(99), IdlePct: f(0), MemAvailPct: f(5)}
	if d := Diagnose(DiagIn{Med: med(100, 20, 100, 100), Res: res2, Th: th}); d.Code != "LIKELY_MEMORY_PRESSURE" {
		t.Error(d.Code)
	}
	res3 := &model.Resources{Available: true, StealPct: f(1), BusyPct: f(90), IdlePct: f(10), MemAvailPct: f(50)}
	if d := Diagnose(DiagIn{Med: med(100, 20, 100, 100), Res: res3, Th: th}); d.Code != "LIKELY_SERVER_CPU_BOTTLENECK" {
		t.Error(d.Code)
	}
	// 本地上行上限仅在设了 cap 时
	if d := Diagnose(DiagIn{Med: med(80, 80, 90, 90), Th: th}); d.Code == "EXPECTED_LOCAL_UPLINK_LIMIT" {
		t.Error("no cap must not trigger")
	}
	if d := Diagnose(DiagIn{Med: med(40, 80, 45, 90), UploadCap: f(50), Th: th}); d.Code != "EXPECTED_LOCAL_UPLINK_LIMIT" {
		t.Error(d.Code)
	}
	// 反向单流限制
	if d := Diagnose(DiagIn{Med: med(100, 20, 100, 77), Th: th}); d.Code != "LIKELY_REVERSE_SINGLE_FLOW_LIMITATION" {
		t.Error(d.Code)
	}
	// 线路拥塞：需要基线 + 高峰
	b := &model.Baseline{N: 3, Median: map[string]float64{"fwd1": 100, "rev1": 100, "fwd4": 200, "rev4": 200}}
	if d := Diagnose(DiagIn{Med: med(50, 60, 90, 100), Period: "peak", Baseline: b, Th: th}); d.Code != "LIKELY_LINE_CONGESTION" {
		t.Error(d.Code)
	}
	if d := Diagnose(DiagIn{Med: med(50, 60, 90, 100), Period: "normal", Baseline: b, Th: th}); d.Code == "LIKELY_LINE_CONGESTION" {
		t.Error("only peak")
	}
	// 健康
	b2 := &model.Baseline{N: 3, Median: map[string]float64{"fwd1": 100, "rev1": 95, "fwd4": 105, "rev4": 105}}
	if d := Diagnose(DiagIn{Med: med(95, 90, 100, 100), Period: "normal", Baseline: b2, Th: th}); d.Code != "HEALTHY" {
		t.Error(d.Code)
	}
	if d := Diagnose(DiagIn{Med: med(95, 90, 100, 100), Th: th}); d.Code != "HEALTHY" || d.Confidence != "low" {
		t.Error(d.Code, d.Confidence)
	}
	// 部分
	if d := Diagnose(DiagIn{Med: med(5, -1, 100, 100), Th: th}); d.Code != "PARTIAL" {
		t.Error(d.Code)
	}
	if d := Diagnose(DiagIn{Med: med(5, 5, 5, 5), Th: th}); d.Code != "INSUFFICIENT_EVIDENCE" {
		t.Error(d.Code)
	}
}

func TestBaseline(t *testing.T) {
	h := []HistRec{{"off_peak", map[string]float64{"fwd1": 90}}, {"peak", map[string]float64{"fwd1": 10}}, {"off_peak", map[string]float64{"fwd1": 100}}}
	if BuildBaseline(h, DefaultThresholds) != nil {
		t.Fatal("2 off-peak is insufficient")
	}
	h = append(h, HistRec{"off_peak", map[string]float64{"fwd1": 110}})
	b := BuildBaseline(h, DefaultThresholds)
	if b == nil || b.N != 3 || b.Median["fwd1"] != 100 {
		t.Fatalf("%+v", b)
	}
}

func TestForwardUtil(t *testing.T) {
	if ForwardUtil(med(40, 1, 45, 1), nil) != nil {
		t.Fatal("no cap → nil")
	}
	if u := ForwardUtil(med(40, 1, 45, 1), f(50)); math.Abs(*u-0.9) > 1e-9 {
		t.Fatal(*u)
	}
}

func TestMeanAbsDiff(t *testing.T) {
	if got := MeanAbsDiff([]float64{10, 20, 10, 20}); got != 10 {
		t.Fatal(got)
	}
}

func TestLatencyStats(t *testing.T) {
	l := LatencyStats([]float64{10, 12, -1, 11, 40, -1, 10, 10, 10, 10})
	if l.Sent != 10 || l.Recv != 8 || l.LossPct != 20 {
		t.Fatalf("%+v", l)
	}
	if *l.Min != 10 || *l.Max != 40 || *l.P50 != 10 || l.Jitter == nil {
		t.Fatalf("%v %v %v", *l.Min, *l.Max, *l.P50)
	}
	e := LatencyStats([]float64{-1, -1})
	if e.P50 != nil || e.LossPct != 100 {
		t.Fatalf("%+v", e)
	}
}
