// Package ping 提供无需管理员权限的 ICMP 往返与逐跳探测，以及 TCP 握手时延兜底。
// 时间一律用本程序自己的单调时钟（time.Now）测，不信任系统 API 返回的毫秒整数。
package ping

import (
	"context"
	"net"
	"time"
)

// Kind 说明一次探测的结果类型。
type Kind string

const (
	Echo    Kind = "echo"    // 目标本身回应
	Expired Kind = "expired" // 途中路由器回 TTL 超时（traceroute 的“某一跳”）
	Unreach Kind = "unreach" // 不可达
	Timeout Kind = "timeout"
	Error   Kind = "error"
)

type Reply struct {
	Kind Kind
	RTT  time.Duration
	From net.IP
	Err  string
}

// Pinger 发一次 ICMP Echo。ttl<=0 表示系统默认。
type Pinger interface {
	Name() string
	Ping(ctx context.Context, dst net.IP, ttl int, timeout time.Duration) Reply
}

// New 返回当前平台最合适的 ICMP Pinger；不可用时返回 nil。
func New() Pinger { return newICMP() }

// TCP 通过 TCP 握手测时延（兜底：ICMP 被过滤或不可用时）。
// 测的是“SYN→SYN/ACK”，端口关闭（RST）也算一次有效往返。
type TCP struct{ Port int }

func (t TCP) Name() string { return "tcp" }

func (t TCP) Ping(ctx context.Context, dst net.IP, _ int, timeout time.Duration) Reply {
	port := t.Port
	if port == 0 {
		port = 443
	}
	d := net.Dialer{Timeout: timeout}
	t0 := time.Now()
	c, err := d.DialContext(ctx, "tcp4", net.JoinHostPort(dst.String(), itoa(port)))
	rtt := time.Since(t0)
	if err == nil {
		c.Close()
		return Reply{Kind: Echo, RTT: rtt, From: dst}
	}
	if isRefused(err) {
		return Reply{Kind: Echo, RTT: rtt, From: dst}
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return Reply{Kind: Timeout}
	}
	return Reply{Kind: Error, Err: err.Error()}
}

func isRefused(err error) bool {
	s := err.Error()
	return contains(s, "refused") || contains(s, "actively refused") || contains(s, "forcibly closed")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// HopCapable 说明这个 Pinger 能否做逐跳（TTL）探测。没有实现 Hops 方法的按“能”处理（Windows 的 IcmpSendEcho 可以）。
func HopCapable(p Pinger) bool {
	if h, ok := p.(interface{ Hops() bool }); ok {
		return h.Hops()
	}
	return p != nil && p.Name() != "tcp"
}
