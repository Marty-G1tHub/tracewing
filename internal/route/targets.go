package route

import (
	_ "embed"
	"encoding/json"
	"net"
	"net/netip"
)

//go:embed targets.json
var targetsJSON []byte

// Target 是 agent 允许探测的固定目标之一。
type Target struct {
	ID      string `json:"id"`
	Carrier string `json:"carrier"` // telecom / unicom / mobile
	City    string `json:"city"`
	Label   string `json:"label"`
	IP      string `json:"ip"`
}

type targetFile struct {
	Version string   `json:"version"`
	Note    string   `json:"note"`
	Targets []Target `json:"targets"`
}

// Targets 返回内置的 12 个固定目标（3 家运营商 × 北京 / 上海 / 广州 / 成都）及数据版本。
func Targets() ([]Target, string) {
	var f targetFile
	if err := json.Unmarshal(targetsJSON, &f); err != nil {
		panic("targets.json 损坏：" + err.Error())
	}
	return f.Targets, f.Version
}

// FindTarget 按 id 找固定目标。
func FindTarget(id string) (Target, bool) {
	ts, _ := Targets()
	for _, t := range ts {
		if t.ID == id {
			return t, true
		}
	}
	return Target{}, false
}

var reserved = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// IsPublic 报告 ip 是否是可路由的公网 IPv4。私有、回环、链路本地、CGNAT、文档、组播、保留段一律拒绝。
func IsPublic(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip.To4())
	if !ok {
		return false
	}
	for _, p := range reserved {
		if p.Contains(a) {
			return false
		}
	}
	return true
}
