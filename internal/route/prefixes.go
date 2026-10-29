package route

import (
	_ "embed"
	"encoding/json"
	"net"
	"net/netip"
)

//go:embed prefixes.json
var prefixesJSON []byte

// PrefixTable 是“前缀 → 骨干 ASN”的静态表，版本化随程序发布。
// 之所以不在线查：CN2 的 59.43.0.0/16 不在 BGP 中公告，在线 BGP 数据查不到；离线表也让判定可复现、不外发任何 IP。
type PrefixTable struct {
	Version string
	Names   map[int]string
	byLen   [33]map[uint32]int
}

type prefixFile struct {
	Version  string            `json:"version"`
	ASNs     map[string]string `json:"asns"`
	Prefixes [][2]any          `json:"prefixes"`
}

var defaultTable *PrefixTable

// Default 返回内置前缀表。
func Default() *PrefixTable {
	if defaultTable == nil {
		t, err := LoadTable(prefixesJSON)
		if err != nil {
			panic("prefixes.json 损坏：" + err.Error())
		}
		defaultTable = t
	}
	return defaultTable
}

// LoadTable 解析前缀表 JSON。
func LoadTable(b []byte) (*PrefixTable, error) {
	var f prefixFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	t := &PrefixTable{Version: f.Version, Names: map[int]string{}}
	for k, v := range f.ASNs {
		var n int
		for _, c := range k {
			n = n*10 + int(c-'0')
		}
		t.Names[n] = v
	}
	for _, row := range f.Prefixes {
		s, _ := row[0].(string)
		asn, _ := row[1].(float64)
		p, err := netip.ParsePrefix(s)
		if err != nil || !p.Addr().Is4() {
			continue
		}
		p = p.Masked()
		l := p.Bits()
		if t.byLen[l] == nil {
			t.byLen[l] = map[uint32]int{}
		}
		a := p.Addr().As4()
		t.byLen[l][uint32(a[0])<<24|uint32(a[1])<<16|uint32(a[2])<<8|uint32(a[3])] = int(asn)
	}
	return t, nil
}

// ASN 做最长前缀匹配，返回 ASN；不在表里返回 0。
func (t *PrefixTable) ASN(ip net.IP) int {
	v4 := ip.To4()
	if v4 == nil {
		return 0
	}
	x := uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])
	for l := 32; l >= 0; l-- {
		m := t.byLen[l]
		if m == nil {
			continue
		}
		var mask uint32
		if l > 0 {
			mask = ^uint32(0) << (32 - l)
		}
		if asn, ok := m[x&mask]; ok {
			return asn
		}
	}
	return 0
}

// Is59_43 判断是否在 CN2 骨干内部地址段 59.43.0.0/16。
func Is59_43(ip net.IP) bool {
	v := ip.To4()
	return v != nil && v[0] == 59 && v[1] == 43
}
