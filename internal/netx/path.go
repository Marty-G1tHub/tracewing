package netx

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// 路径模式
const (
	ModeSystem = "system" // 跟随系统：环境变量 / Windows 系统代理（与浏览器一致）
	ModeDirect = "direct" // 显式绕过系统代理（TUN 仍会接管，那是系统路由层的事）
	ModeCustom = "custom" // 指定代理 URL（http/https/socks5）
)

// Path 描述“流量怎么出去”。它是所有测量共用的唯一入口，
// 这样“测的到底是哪条路”只在这里决定，并且可以原样写进结果。
type Path struct {
	Mode   string `json:"mode"`
	Custom string `json:"custom,omitempty"` // Mode=custom 时的代理 URL
	DNS    string `json:"dns,omitempty"`    // 可选：指定 DNS 服务器 host[:port]；空=系统 DNS

	sys  SysProxy
	once sync.Once
}

// Normalize 校验并填充默认值。
func (p *Path) Normalize() error {
	switch p.Mode {
	case "", ModeSystem:
		p.Mode = ModeSystem
	case ModeDirect:
	case ModeCustom:
		u, err := url.Parse(p.Custom)
		if err != nil || u.Host == "" {
			return fmt.Errorf("自定义代理地址无效（示例 http://127.0.0.1:7890 或 socks5://127.0.0.1:1080）")
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("自定义代理仅支持 http / https / socks5")
		}
	default:
		return fmt.Errorf("未知路径模式 %q", p.Mode)
	}
	return nil
}

func (p *Path) loadSys() {
	p.once.Do(func() { p.sys = ReadSysProxy() })
}

// Sys 返回读到的系统代理快照。
func (p *Path) Sys() SysProxy { p.loadSys(); return p.sys }

// ProxyFor 返回访问 u 会用的代理（nil=直连）。
func (p *Path) ProxyFor(u *url.URL) *url.URL {
	switch p.Mode {
	case ModeDirect:
		return nil
	case ModeCustom:
		pu, _ := url.Parse(p.Custom)
		return pu
	}
	if pu, err := http.ProxyFromEnvironment(&http.Request{URL: u}); err == nil && pu != nil {
		return pu
	}
	p.loadSys()
	if p.sys.Source == "registry" {
		return p.sys.For(u)
	}
	return nil
}

// Describe 用一句中文说明访问 host 时实际所走的路径（写进结果用）。
func (p *Path) Describe(u *url.URL) string {
	pu := p.ProxyFor(u)
	switch {
	case pu != nil:
		return "经代理 " + redact(pu)
	case p.Mode == ModeDirect:
		return "直连（已绕过系统代理）"
	default:
		return "直连（无系统代理命中）"
	}
}

func redact(u *url.URL) string {
	c := *u
	c.User = nil
	return c.String()
}

// Label 给环境条显示。
func (p *Path) Label() string {
	switch p.Mode {
	case ModeDirect:
		return "直连（绕过系统代理）"
	case ModeCustom:
		u, _ := url.Parse(p.Custom)
		if u != nil {
			return "自定义代理 " + redact(u)
		}
	}
	return "跟随系统"
}

// Resolver 返回按 Path.DNS 配置的解析器（空=系统解析器）。
func (p *Path) Resolver() *net.Resolver {
	if p.DNS == "" {
		return net.DefaultResolver
	}
	addr := p.DNS
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "53")
	}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
}

// Transport 构造测速专用 Transport：
//   - 关闭 HTTP/2：多流测速必须是多条独立 TCP 连接，h2 会把它们复用成一条；
//   - 关闭压缩：避免读数被压缩影响；
//   - 加大读写缓冲，千兆线路下不被 4KiB 缓冲拖累。
func (p *Path) Transport() *http.Transport {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Resolver: p.Resolver()}
	return &http.Transport{
		Proxy:                 func(r *http.Request) (*url.URL, error) { return p.ProxyFor(r.URL), nil },
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       30 * time.Second,
		ReadBufferSize:        256 << 10,
		WriteBufferSize:       256 << 10,
		ResponseHeaderTimeout: 20 * time.Second,
	}
}

// Client 返回不限总超时的客户端（超时由调用方 context 控制）。
func (p *Path) Client() *http.Client {
	return &http.Client{Transport: p.Transport()}
}

// DNSEvidence 是一次解析的证据。
type DNSEvidence struct {
	Host  string   `json:"host"`
	IPs   []string `json:"ips"`
	Class string   `json:"class"` // fake-ip | private | public | mixed | ...
	Err   string   `json:"err,omitempty"`
}

// Lookup 在本机用 Path 的 DNS 解析 host，并分类。
// 注意：经代理访问时，真正的解析发生在代理端，这里只是本机侧证据。
func (p *Path) Lookup(ctx context.Context, host string) DNSEvidence {
	ev := DNSEvidence{Host: host}
	if ip := net.ParseIP(host); ip != nil {
		ev.IPs = []string{ip.String()}
		ev.Class = Classify(ip)
		return ev
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := p.Resolver().LookupIPAddr(ctx, host)
	if err != nil {
		ev.Err = err.Error()
		ev.Class = "error"
		return ev
	}
	classes := map[string]bool{}
	for _, a := range addrs {
		if a.IP.To4() == nil {
			continue // 只报告 IPv4，避免噪音
		}
		ev.IPs = append(ev.IPs, a.IP.String())
		classes[Classify(a.IP)] = true
	}
	switch len(classes) {
	case 0:
		ev.Class = "none"
	case 1:
		for c := range classes {
			ev.Class = c
		}
	default:
		ev.Class = "mixed"
	}
	return ev
}
