package netx

import (
	"net"
	"net/url"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"198.18.0.1": "fake-ip", "198.19.255.255": "fake-ip", "198.20.0.1": "public",
		"10.1.2.3": "private", "192.168.1.1": "private", "172.16.0.1": "private", "172.32.0.1": "public",
		"100.64.0.1": "cgnat", "127.0.0.1": "loopback", "169.254.1.1": "linklocal", "8.8.8.8": "public",
	}
	for ip, want := range cases {
		if got := Classify(net.ParseIP(ip)); got != want {
			t.Errorf("%s: got %s want %s", ip, got, want)
		}
	}
}

func TestValidPublicIPv4(t *testing.T) {
	good := []string{"8.8.8.8", "1.1.1.1", "223.255.255.254"}
	bad := []string{"0.1.2.3", "127.0.0.1", "10.0.0.1", "172.20.1.1", "192.168.0.9", "169.254.0.1", "100.100.1.1", "198.18.0.5", "198.19.0.5", "224.0.0.1", "255.255.255.255", "abc", "::1"}
	for _, s := range good {
		if _, ok := ValidPublicIPv4(s); !ok {
			t.Errorf("%s should be valid", s)
		}
	}
	for _, s := range bad {
		if _, ok := ValidPublicIPv4(s); ok {
			t.Errorf("%s should be rejected", s)
		}
	}
}

func TestParseWinProxy(t *testing.T) {
	sp := parseWinProxy("127.0.0.1:7890", "localhost;127.*;10.*;<local>")
	if sp.HTTP != "http://127.0.0.1:7890" || sp.HTTPS != sp.HTTP {
		t.Fatalf("%+v", sp)
	}
	sp2 := parseWinProxy("http=a:1;https=b:2;socks=c:3", "")
	if sp2.HTTP != "http://a:1" || sp2.HTTPS != "http://b:2" || sp2.SOCKS != "socks5://c:3" {
		t.Fatalf("%+v", sp2)
	}
	u := func(s string) *url.URL { x, _ := url.Parse(s); return x }
	if sp.For(u("https://10.1.1.1/")) != nil {
		t.Error("10.* should bypass")
	}
	if sp.For(u("http://intranet/")) != nil {
		t.Error("<local> should bypass dotless")
	}
	if p := sp.For(u("https://speed.cloudflare.com/")); p == nil || p.Host != "127.0.0.1:7890" {
		t.Errorf("should use proxy, got %v", p)
	}
}

func TestPathNormalize(t *testing.T) {
	p := &Path{Mode: "custom", Custom: "nonsense"}
	if p.Normalize() == nil {
		t.Error("expected error")
	}
	p = &Path{Mode: "custom", Custom: "socks5://127.0.0.1:1080"}
	if err := p.Normalize(); err != nil {
		t.Error(err)
	}
	if got := p.ProxyFor(&url.URL{Scheme: "https", Host: "x.com"}); got == nil || got.Scheme != "socks5" {
		t.Errorf("got %v", got)
	}
	d := &Path{Mode: "direct"}
	d.Normalize()
	if d.ProxyFor(&url.URL{Scheme: "https", Host: "x.com"}) != nil {
		t.Error("direct must not proxy")
	}
}
