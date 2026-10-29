package netx

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

// SysProxy 是从系统读到的代理设置（只读，本程序从不修改）。
type SysProxy struct {
	Source   string   `json:"source"`         // env | registry | none
	HTTP     string   `json:"http,omitempty"` // 例 http://127.0.0.1:7890
	HTTPS    string   `json:"https,omitempty"`
	SOCKS    string   `json:"socks,omitempty"`
	Bypass   []string `json:"bypass,omitempty"`
	PAC      string   `json:"pac,omitempty"`
	AutoDet  bool     `json:"autodetect,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Active 表示系统里确实配了会影响本程序的静态代理。
func (s SysProxy) Active() bool { return s.HTTP != "" || s.HTTPS != "" || s.SOCKS != "" }

// parseWinProxy 解析 WinINET 的 ProxyServer / ProxyOverride 字符串。
//
//	ProxyServer: "host:port" 或 "http=h:p;https=h:p;socks=h:p"
//	ProxyOverride: "localhost;127.*;10.*;<local>"
func parseWinProxy(server, override string) SysProxy {
	sp := SysProxy{Source: "registry"}
	server = strings.TrimSpace(server)
	if server != "" {
		if !strings.Contains(server, "=") {
			u := withScheme(server, "http")
			sp.HTTP, sp.HTTPS = u, u
		} else {
			for _, part := range strings.Split(server, ";") {
				k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
				if !ok || v == "" {
					continue
				}
				switch strings.ToLower(k) {
				case "http":
					sp.HTTP = withScheme(v, "http")
				case "https":
					sp.HTTPS = withScheme(v, "http")
				case "socks", "socks5":
					sp.SOCKS = withScheme(v, "socks5")
				}
			}
		}
	}
	for _, b := range strings.Split(override, ";") {
		if b = strings.ToLower(strings.TrimSpace(b)); b != "" {
			sp.Bypass = append(sp.Bypass, b)
		}
	}
	return sp
}

func withScheme(hp, def string) string {
	if strings.Contains(hp, "://") {
		return hp
	}
	return def + "://" + hp
}

// bypassed 判断 host 是否命中 ProxyOverride。
func (s SysProxy) bypassed(host string) bool {
	host = strings.ToLower(host)
	for _, p := range s.Bypass {
		if p == "<local>" {
			if !strings.Contains(host, ".") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(p, host); ok {
			return true
		}
	}
	return false
}

// For 返回访问 u 时系统会使用的代理（nil=直连）。
func (s SysProxy) For(u *url.URL) *url.URL {
	if !s.Active() || s.bypassed(u.Hostname()) {
		return nil
	}
	pick := s.HTTP
	if u.Scheme == "https" && s.HTTPS != "" {
		pick = s.HTTPS
	}
	if pick == "" {
		pick = s.SOCKS
	}
	if pick == "" {
		return nil
	}
	pu, err := url.Parse(pick)
	if err != nil {
		return nil
	}
	return pu
}

// ReadSysProxy 读取系统代理：优先环境变量（命令行程序的惯例），其次 Windows 注册表（浏览器的惯例）。
func ReadSysProxy() SysProxy {
	req, _ := http.NewRequest("GET", "https://example.com/", nil)
	if pu, err := http.ProxyFromEnvironment(req); err == nil && pu != nil {
		return SysProxy{Source: "env", HTTP: pu.String(), HTTPS: pu.String()}
	}
	return readPlatformProxy()
}
