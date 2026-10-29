package route

import (
	"fmt"
	"net"
	"strings"
)

// 置信度。判定只依据“路径上出现了哪些骨干网的地址、出现的先后顺序”，并且每个结论都附带证据（哪一跳、哪个 IP、属于哪个 ASN）。
const (
	Sure    = "sure"    // 确定：有明确的骨干网证据，且没有互相矛盾的证据
	Mixed   = "mixed"   // 混合：同时出现多种骨干网，或顺序不典型，结论只是倾向
	Unknown = "unknown" // 无法确定：没有可识别的骨干网地址，或探测不完整
)

// Verdict 是对一条路径的线路判定。
type Verdict struct {
	Code       string   `json:"code"`       // cn2_gia / cn2_gt / as9929 / as4837 / mix_4837_9929 / cmin2 / cmi / cmnet / as4134 / mixed / unknown
	Label      string   `json:"label"`      // 给人看的名称
	Confidence string   `json:"confidence"` // sure / mixed / unknown
	Evidence   []string `json:"evidence"`   // 每条一行，如“第 7 跳 59.43.x.x → AS4809 (CN2)”
	ASPath     []int    `json:"as_path"`    // 路径上依次出现的已识别 ASN（相邻去重）
	Reached    bool     `json:"reached"`
}

type seen struct {
	ttl int
	ip  string
	asn int
	cn2 bool // 命中 59.43.0.0/16
}

// Classify 判定一条路径。同一份 Trace 在不同前缀表版本下可能得到不同结论，所以结果里不存前缀表，调用方可记录 PrefixTable.Version。
func Classify(t *PrefixTable, tr Trace) Verdict {
	v := Verdict{Reached: tr.Reached}
	var hs []seen
	for _, h := range tr.Hops {
		ip := net.ParseIP(h.IP)
		if ip == nil || !IsPublic(ip) || h.IP == tr.Target { // 目标自己的地址不算“路径证据”
			continue
		}
		s := seen{ttl: h.TTL, ip: h.IP, asn: t.ASN(ip), cn2: Is59_43(ip)}
		if s.cn2 {
			s.asn = 4809
		}
		if s.asn != 0 {
			hs = append(hs, s)
			if n := len(v.ASPath); n == 0 || v.ASPath[n-1] != s.asn {
				v.ASPath = append(v.ASPath, s.asn)
			}
		}
	}
	name := func(asn int) string {
		if n, ok := t.Names[asn]; ok {
			return n
		}
		return "AS" + fmt.Sprint(asn)
	}
	ev := func(s seen) string {
		extra := ""
		if s.cn2 {
			extra = "（59.43.0.0/16，CN2 骨干内部地址）"
		}
		return fmt.Sprintf("第 %d 跳 %s → AS%d %s%s", s.ttl, s.ip, s.asn, name(s.asn), extra)
	}
	pick := func(match func(seen) bool, max int) (out []seen) {
		for _, s := range hs {
			if match(s) {
				out = append(out, s)
				if len(out) == max {
					break
				}
			}
		}
		return
	}
	add := func(ss []seen) {
		for _, s := range ss {
			v.Evidence = append(v.Evidence, ev(s))
		}
	}
	has := func(asn int) bool { return len(pick(func(s seen) bool { return s.asn == asn }, 1)) > 0 }
	first := func(match func(seen) bool) int {
		for i, s := range hs {
			if match(s) {
				return i
			}
		}
		return -1
	}
	set := func(code, label, conf string) Verdict { v.Code, v.Label, v.Confidence = code, label, conf; return v }

	cn2, n4134 := has(4809), has(4134)
	n4837, n9929 := has(4837), has(9929)
	cmin2, cmi := has(58807), has(58453)

	incomplete := len(tr.Hops) < 3 || (!tr.Reached && len(hs) == 0)

	switch {
	// 电信：CN2
	case cn2:
		add(pick(func(s seen) bool { return s.asn == 4809 }, 3))
		fc := first(func(s seen) bool { return s.asn == 4809 })
		f4134 := first(func(s seen) bool { return s.asn == 4134 })
		other := n4837 || n9929 || cmin2 || cmi
		if other {
			add(pick(func(s seen) bool { return s.asn == 4837 || s.asn == 9929 || s.asn == 58807 || s.asn == 58453 }, 2))
			return set("mixed", "混合：同时出现 CN2 与其他运营商骨干", Mixed)
		}
		if n4134 && f4134 >= 0 && f4134 < fc {
			// 先走 163 骨干（AS4134）再进 CN2：典型的 CN2 GT（普通 CN2，国内段与 163 混跑）
			add(pick(func(s seen) bool { return s.asn == 4134 }, 1))
			return set("cn2_gt", "电信 CN2 GT", Sure)
		}
		if n4134 {
			// 进 CN2 之后才出现 4134：多半是到目的地的最后一段接入，仍按 GIA 看，但降为“混合”以示保守
			add(pick(func(s seen) bool { return s.asn == 4134 }, 1))
			return set("cn2_gia", "电信 CN2 GIA（末段混有 163 地址）", Mixed)
		}
		return set("cn2_gia", "电信 CN2 GIA", Sure)
	// 联通
	case n9929:
		add(pick(func(s seen) bool { return s.asn == 9929 }, 3))
		if n4837 {
			add(pick(func(s seen) bool { return s.asn == 4837 }, 2))
			return set("mix_4837_9929", "联通 9929（A 网）与 4837 混合", Mixed)
		}
		return set("as9929", "联通 AS9929（A 网 / CUG 精品）", Sure)
	// 移动
	case cmin2:
		add(pick(func(s seen) bool { return s.asn == 58807 }, 3))
		return set("cmin2", "移动 CMIN2（AS58807 精品）", Sure)
	case cmi:
		add(pick(func(s seen) bool { return s.asn == 58453 }, 3))
		if has(9808) {
			add(pick(func(s seen) bool { return s.asn == 9808 }, 1))
		}
		return set("cmi", "移动 CMI（AS58453 国际）", Sure)
	case has(9808) && !n4837 && !n4134:
		add(pick(func(s seen) bool { return s.asn == 9808 }, 3))
		return set("cmnet", "移动 CMNET 骨干（AS9808）", Sure)
	case n4837 && !n4134:
		add(pick(func(s seen) bool { return s.asn == 4837 }, 3))
		return set("as4837", "联通 AS4837（169 骨干，普通线路）", Sure)
	case n4134 && !n4837:
		add(pick(func(s seen) bool { return s.asn == 4134 }, 3))
		return set("as4134", "电信 AS4134（163 骨干，普通线路）", Sure)
	case n4134 && n4837:
		add(pick(func(s seen) bool { return s.asn == 4134 || s.asn == 4837 }, 3))
		return set("mixed", "混合：电信 163 与联通 169 骨干都出现", Mixed)
	case len(hs) > 0:
		add(hs[:1])
		return set("unknown", "只识别到局部地址，无法确定线路", Unknown)
	}
	if incomplete {
		v.Evidence = []string{"探测不完整：回应的跳太少或没有到达目标，不下结论"}
	} else {
		v.Evidence = []string{"路径中没有可识别的骨干网地址（可能 ICMP 被屏蔽、或走的是本表未收录的网络）"}
	}
	return set("unknown", "无法确定", Unknown)
}

// ConfLabel 返回置信度的中文名。
func ConfLabel(c string) string {
	switch c {
	case Sure:
		return "确定"
	case Mixed:
		return "混合"
	}
	return "无法确定"
}

// Summary 把多条路径的判定合成一句话，用于顶部概览。
func Summary(vs map[string]Verdict) string {
	cnt := map[string]int{}
	for _, v := range vs {
		cnt[v.Label]++
	}
	var parts []string
	for l, n := range cnt {
		parts = append(parts, fmt.Sprintf("%s ×%d", l, n))
	}
	sortStrings(parts)
	return strings.Join(parts, "；")
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}
