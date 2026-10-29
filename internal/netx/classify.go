// Package netx 负责“走哪条路”：代理解析、HTTP 客户端、DNS 证据。
package netx

import "net"

var (
	fakeNet  = mustCIDR("198.18.0.0/15")
	cgnatNet = mustCIDR("100.64.0.0/10")
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// IsFakeIP 判断是否在 198.18.0.0/15（Clash/OpenClash 等 Fake-IP 默认网段，也是基准测试保留网段）。
func IsFakeIP(ip net.IP) bool { return fakeNet.Contains(ip) }

// Classify 把地址分类：fake-ip / loopback / private / cgnat / linklocal / public。
func Classify(ip net.IP) string {
	switch {
	case IsFakeIP(ip):
		return "fake-ip"
	case ip.IsLoopback() || ip.IsUnspecified():
		return "loopback"
	case ip.IsPrivate():
		return "private"
	case cgnatNet.Contains(ip):
		return "cgnat"
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		return "linklocal"
	}
	return "public"
}

// ValidPublicIPv4 判断 s 是否可作为“测试 IP”目标：必须是公网 IPv4。
// 拒收：0/8、127/8、10/8、172.16/12、192.168/16、169.254/16、100.64/10、198.18/15、>=224。
func ValidPublicIPv4(s string) (net.IP, bool) {
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, false
	}
	v4 := ip.To4()
	if v4 == nil || v4[0] == 0 || v4[0] >= 224 {
		return nil, false
	}
	if Classify(v4) != "public" {
		return nil, false
	}
	return v4, true
}
