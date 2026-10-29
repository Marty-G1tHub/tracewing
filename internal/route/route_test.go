package route

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/ping"
)

// tr 用 IP 列表构造一条路径；"" 表示该跳无回应。目标放在最后一跳。
func tr(target string, reached bool, ips ...string) Trace {
	t := Trace{Target: target, Reached: reached}
	for i, ip := range ips {
		h := Hop{TTL: i + 1, Sent: 2}
		if ip != "" {
			h.IP, h.Got = ip, 2
		}
		t.Hops = append(t.Hops, h)
	}
	return t
}

func TestPrefixTable(t *testing.T) {
	tb := Default()
	cases := map[string]int{
		"202.97.33.1": 4134, "218.105.1.1": 9929, "210.51.100.1": 9929, "219.158.6.66": 4837,
		"223.120.2.2": 58453, "223.118.32.9": 58807, "221.176.1.1": 9808, "59.43.1.1": 4809, "8.8.8.8": 0,
	}
	for ip, want := range cases {
		if got := tb.ASN(net.ParseIP(ip)); got != want {
			t.Errorf("%s: ASN=%d want %d", ip, got, want)
		}
	}
	if !Is59_43(net.ParseIP("59.43.200.1")) || Is59_43(net.ParseIP("59.44.0.1")) {
		t.Error("Is59_43 判断错误")
	}
}

func TestClassify(t *testing.T) {
	tb := Default()
	const dst = "219.141.136.10"
	type tc struct {
		name, code, conf string
		tr               Trace
	}
	cases := []tc{
		{"CN2 GIA", "cn2_gia", Sure, tr(dst, true, "10.0.0.1", "4.69.1.1", "59.43.246.1", "59.43.18.2", dst)},
		{"CN2 GT：先 163 再 CN2", "cn2_gt", Sure, tr(dst, true, "4.69.1.1", "202.97.60.1", "202.97.33.5", "59.43.18.2", dst)},
		{"GIA 末段混 163", "cn2_gia", Mixed, tr(dst, true, "4.69.1.1", "59.43.246.1", "59.43.18.2", "202.97.1.1", dst)},
		{"163 普通", "as4134", Sure, tr(dst, true, "4.69.1.1", "202.97.60.1", "202.97.33.5", dst)},
		{"联通 9929", "as9929", Sure, tr("210.22.70.3", true, "4.69.1.1", "218.105.2.3", "218.105.130.1", "210.22.70.3")},
		{"联通 4837", "as4837", Sure, tr("210.22.70.3", true, "4.69.1.1", "219.158.6.66", "219.158.33.194", "210.22.70.3")},
		{"联通 4837+9929 混合", "mix_4837_9929", Mixed, tr("210.22.70.3", true, "4.69.1.1", "219.158.6.66", "218.105.2.3", "210.22.70.3")},
		{"移动 CMIN2", "cmin2", Sure, tr("211.136.17.107", true, "4.69.1.1", "223.118.32.9", "221.176.1.1", "211.136.17.107")},
		{"移动 CMI", "cmi", Sure, tr("211.136.17.107", true, "4.69.1.1", "223.120.2.2", "221.176.1.1", "211.136.17.107")},
		{"CN2 与 9929 同现 → 混合", "mixed", Mixed, tr(dst, true, "59.43.1.1", "218.105.2.3", dst)},
		{"无可识别地址", "unknown", Unknown, tr(dst, true, "4.69.1.1", "", "8.8.4.4", dst)},
		{"探测不完整", "unknown", Unknown, tr(dst, false, "10.0.0.1", "", "")},
	}
	for _, c := range cases {
		v := Classify(tb, c.tr)
		if v.Code != c.code || v.Confidence != c.conf {
			t.Errorf("%s: 得到 %s/%s，期望 %s/%s；证据 %v", c.name, v.Code, v.Confidence, c.code, c.conf, v.Evidence)
		}
		if v.Code != "unknown" && len(v.Evidence) == 0 {
			t.Errorf("%s: 缺少证据", c.name)
		}
	}
}

func TestIsPublic(t *testing.T) {
	for _, s := range []string{"10.1.1.1", "127.0.0.1", "169.254.1.1", "172.16.0.1", "192.168.1.1", "100.64.0.1", "224.0.0.1", "0.0.0.0", "255.255.255.255", "198.51.100.1"} {
		if IsPublic(net.ParseIP(s)) {
			t.Errorf("%s 不应是公网", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "59.43.1.1", "219.141.136.10"} {
		if !IsPublic(net.ParseIP(s)) {
			t.Errorf("%s 应是公网", s)
		}
	}
	if IsPublic(net.ParseIP("2001:db8::1")) {
		t.Error("IPv6 暂不支持")
	}
}

func TestTargets(t *testing.T) {
	ts, v := Targets()
	if len(ts) != 12 || v == "" {
		t.Fatalf("目标应为 12 个，实际 %d", len(ts))
	}
	seen := map[string]bool{}
	for _, x := range ts {
		ip := net.ParseIP(x.IP)
		if ip == nil || !IsPublic(ip) || seen[x.ID] {
			t.Errorf("目标 %s 非法或重复", x.ID)
		}
		seen[x.ID] = true
	}
	if _, ok := FindTarget("ct-bj"); !ok {
		t.Error("找不到 ct-bj")
	}
}

// fakePinger 模拟一条 5 跳路径，用来验证并发探测、排序与在目标处截断。
type fakePinger struct{ path []string }

func (f fakePinger) Name() string { return "fake" }
func (f fakePinger) Ping(_ context.Context, dst net.IP, ttl int, _ time.Duration) ping.Reply {
	if ttl >= len(f.path) {
		return ping.Reply{Kind: ping.Echo, RTT: time.Millisecond, From: dst}
	}
	if f.path[ttl-1] == "" {
		return ping.Reply{Kind: ping.Timeout}
	}
	return ping.Reply{Kind: ping.Expired, RTT: time.Duration(ttl) * time.Millisecond, From: net.ParseIP(f.path[ttl-1])}
}

func TestRun(t *testing.T) {
	got := Run(context.Background(), fakePinger{path: []string{"4.69.1.1", "", "59.43.1.1", "202.97.1.1", "x"}}, net.ParseIP("219.141.136.10"), Options{MaxTTL: 12})
	if !got.Reached || len(got.Hops) != 5 {
		t.Fatalf("应在第 5 跳到达目标，得到 reached=%v hops=%d", got.Reached, len(got.Hops))
	}
	for i, h := range got.Hops {
		if h.TTL != i+1 {
			t.Fatalf("TTL 未排序：%v", got.Hops)
		}
	}
	if got.Hops[1].IP != "" || got.Hops[2].IP != "59.43.1.1" || got.Hops[4].IP != "219.141.136.10" {
		t.Errorf("跳数据不对：%+v", got.Hops)
	}
	v := Classify(Default(), got)
	if v.Code != "cn2_gt" && v.Code != "cn2_gia" {
		t.Errorf("应识别为 CN2，得到 %s", v.Code)
	}
}
