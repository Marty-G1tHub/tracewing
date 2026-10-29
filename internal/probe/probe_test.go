package probe

import "testing"

func TestParseKV(t *testing.T) {
	m := parseKV("fl=1\nip=1.2.3.4\nloc=SG\ncolo=SIN\n")
	if m["ip"] != "1.2.3.4" || m["loc"] != "SG" || m["colo"] != "SIN" {
		t.Fatal(m)
	}
}

func TestReIPIP(t *testing.T) {
	m := reIPIP.FindStringSubmatch("当前 IP：117.1.2.3  来自于：中国 上海 上海  电信")
	if m == nil || m[1] != "117.1.2.3" {
		t.Fatal(m)
	}
}

func TestTunName(t *testing.T) {
	yes := []string{"Meta", "Clash", "Wintun Userspace Tunnel", "tun0", "utun3", "WireGuard Tunnel", "Mihomo", "OpenVPN TAP-Windows6", "sing-box", "wg0"}
	no := []string{"以太网", "Ethernet", "Wi-Fi", "WLAN", "vEthernet (WSL)", "Loopback Pseudo-Interface 1", "eth0", "Bluetooth Network Connection"}
	for _, s := range yes {
		if !tunName.MatchString(s) {
			t.Errorf("%q should match", s)
		}
	}
	for _, s := range no {
		if tunName.MatchString(s) {
			t.Errorf("%q should not match", s)
		}
	}
}
