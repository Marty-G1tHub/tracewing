// Package probe 做只读的环境探测：系统代理、TUN、Fake-IP、出口 IP。
// 它**只读**，从不修改代理 / 路由 / 系统设置。
package probe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/netx"
)

// 出口回显端点（可被环境变量覆盖前先保持简单：常量）。
const (
	exitGlobalURL = "https://speed.cloudflare.com/cdn-cgi/trace"
	exitCNURL     = "https://myip.ipip.net"
)

// 用来发现 Fake-IP 的域名：国内、境外各取代表。
var fakeProbeHosts = []string{"www.baidu.com", "speed.cloudflare.com", "mirrors.ustc.edu.cn"}

// 虚拟网卡名特征（**启发式，未经大范围实测**）。
var tunName = regexp.MustCompile(`(?i)(wintun|\btun\d*\b|^tun|\btap\b|^tap|wireguard|^wg\d|clash|mihomo|^meta$|\bmeta\b|v2ray|xray|sing-?box|hysteria|utun|openvpn|nordlynx|proton|warp|surfshark|expressvpn|vpn)`)

// Detect 执行一次完整探测。
func Detect(ctx context.Context, p *netx.Path) *model.Env {
	env := &model.Env{
		Time:      time.Now().Format(time.RFC3339),
		PathMode:  p.Mode,
		PathLabel: p.Label(),
		SysProxy:  p.Sys(),
		Adapters:  []model.Adapter{},
		Notes:     []model.Note{},
	}
	detectLocal(env)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); detectFake(ctx, p, env) }()
	go func() { defer wg.Done(); env.ExitCN = exitCN(ctx, p) }()
	go func() { defer wg.Done(); env.ExitGlobal = exitGlobal(ctx, p) }()
	wg.Wait()

	if a, b := env.ExitCN, env.ExitGlobal; a != nil && b != nil && a.IP != "" && b.IP != "" {
		d := a.IP != b.IP
		env.ExitDiffer = &d
	}
	env.Notes = buildNotes(env, p)
	return env
}

// detectLocal：网卡 + 去往公网的默认路由出口。
func detectLocal(env *model.Env) {
	ifs, _ := net.Interfaces()
	byIP := map[string]string{}
	for _, ifc := range ifs {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				byIP[ipn.IP.String()] = ifc.Name
			}
		}
		if ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		if tunName.MatchString(ifc.Name) {
			env.Adapters = append(env.Adapters, model.Adapter{Name: ifc.Name, Up: ifc.Flags&net.FlagUp != 0, Suspect: true})
		}
	}
	// UDP “连接”不发包，只让系统路由表选出源地址 → 反推默认路由所在网卡。
	if c, err := net.Dial("udp4", "1.1.1.1:53"); err == nil {
		src := c.LocalAddr().(*net.UDPAddr).IP
		c.Close()
		env.RouteSrc = src.String()
		env.RouteIface = byIP[env.RouteSrc]
		switch {
		case netx.IsFakeIP(src):
			env.TUN, env.TUNReason = true, "默认路由的源地址在 198.18.0.0/15（Fake-IP/TUN 网段）"
		case env.RouteIface != "" && tunName.MatchString(env.RouteIface):
			env.TUN, env.TUNReason = true, "默认路由经网卡 "+env.RouteIface+"（名称像 TUN/VPN）"
		}
	}
}

func detectFake(ctx context.Context, p *netx.Path, env *model.Env) {
	res := make([]netx.DNSEvidence, len(fakeProbeHosts))
	var wg sync.WaitGroup
	for i, h := range fakeProbeHosts {
		wg.Add(1)
		go func(i int, h string) { defer wg.Done(); res[i] = p.Lookup(ctx, h) }(i, h)
	}
	wg.Wait()
	env.FakeIPHosts = res
	for _, r := range res {
		if r.Class == "fake-ip" || r.Class == "mixed" {
			env.FakeIP = true
		}
	}
}

func buildNotes(env *model.Env, p *netx.Path) []model.Note {
	var ns []model.Note
	add := func(l, c, t string) { ns = append(ns, model.Note{Level: l, Code: c, Text: t}) }
	sp := env.SysProxy
	switch {
	case p.Mode == netx.ModeDirect && sp.Active():
		add("info", "PROXY_BYPASSED", "系统设置了代理（"+firstNonEmpty(sp.HTTPS, sp.HTTP, sp.SOCKS)+"），但你选了“直连”，本程序已绕过它。")
	case p.Mode == netx.ModeSystem && sp.Active():
		add("warn", "SYSTEM_PROXY", "系统代理 "+firstNonEmpty(sp.HTTPS, sp.HTTP, sp.SOCKS)+"（来源："+sp.Source+"）：测速流量会经过它，读数是“经代理”的路径。")
	case p.Mode == netx.ModeCustom:
		add("info", "CUSTOM_PROXY", "使用自定义代理："+p.Label()+"。")
	}
	for _, w := range sp.Warnings {
		add("warn", "PAC", w)
	}
	if env.TUN {
		add("warn", "TUN", "疑似 TUN/VPN 接管："+env.TUNReason+"。系统层会把流量交给代理软件，结果是“经代理软件”的路径，不是运营商直连；测试IP 的去程不可信。")
	}
	if env.FakeIP {
		add("warn", "FAKE_IP", "DNS 返回了 198.18.x.x（Fake-IP）：域名由代理软件接管解析。国内源若也被解析成 Fake-IP，说明国内流量也在走代理。")
	}
	if env.ExitDiffer != nil {
		if *env.ExitDiffer {
			add("info", "SPLIT_EXIT", "国内与境外回显的出口 IP 不同：当前是分流环境（国内直连、境外走代理，或相反）。宽带测速的国内/境外两组各自对应不同出口。")
		} else {
			add("info", "SAME_EXIT", "国内与境外回显的出口 IP 相同：两类流量走同一出口（无代理，或全局代理 / 都未分流）。")
		}
	}
	if len(ns) == 0 || (!env.TUN && !env.FakeIP && !sp.Active() && p.Mode != netx.ModeCustom) {
		add("info", "CLEAN", "未发现系统代理 / TUN / Fake-IP：按直连测量。")
	}
	return ns
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---------- 出口 IP ----------

func echoClient(p *netx.Path) *http.Client {
	t := p.Transport()
	d := &net.Dialer{Timeout: 8 * time.Second, Resolver: p.Resolver()}
	// 回显只用 IPv4，保证“国内/境外是否同一出口”的比较口径一致。
	t.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return d.DialContext(ctx, "tcp4", addr)
	}
	return &http.Client{Transport: t, Timeout: 12 * time.Second}
}

func fetch(ctx context.Context, c *http.Client, u string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("User-Agent", "curl/8")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(b), nil
}

func exitVia(p *netx.Path, raw string) string {
	u, _ := url.Parse(raw)
	return p.Describe(u)
}

func exitGlobal(ctx context.Context, p *netx.Path) *model.ExitInfo {
	e := &model.ExitInfo{Label: "境外回显 (Cloudflare)", Via: exitVia(p, exitGlobalURL)}
	body, err := fetch(ctx, echoClient(p), exitGlobalURL)
	if err != nil {
		e.Err = err.Error()
		return e
	}
	kv := parseKV(body)
	e.IP, e.Country = kv["ip"], kv["loc"]
	e.Detail = "数据中心 " + kv["colo"]
	return e
}

var reIPIP = regexp.MustCompile(`IP[：:]\s*([0-9a-fA-F.:]+)\s*来自于[：:]\s*(.+)`)

func exitCN(ctx context.Context, p *netx.Path) *model.ExitInfo {
	e := &model.ExitInfo{Label: "国内回显 (ipip.net)", Via: exitVia(p, exitCNURL)}
	body, err := fetch(ctx, echoClient(p), exitCNURL)
	if err != nil {
		e.Err = err.Error()
		return e
	}
	m := reIPIP.FindStringSubmatch(body)
	if m == nil {
		e.Err = "无法解析回显内容"
		return e
	}
	e.IP = m[1]
	e.Detail = strings.Join(strings.Fields(strings.TrimSpace(m[2])), " ")
	if f := strings.Fields(e.Detail); len(f) > 0 {
		e.Country = f[0]
	}
	return e
}

func parseKV(s string) map[string]string {
	m := map[string]string{}
	for _, ln := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(ln), "="); ok {
			m[k] = v
		}
	}
	return m
}
