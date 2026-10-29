package ping

import (
	"encoding/binary"
	"testing"
)

func ipHdr(proto byte, payload []byte) []byte {
	b := make([]byte, 20, 20+len(payload))
	b[0], b[9] = 0x45, proto
	return append(b, payload...)
}

func echo(typ byte, id, sq uint16) []byte {
	b := make([]byte, 8)
	b[0] = typ
	binary.BigEndian.PutUint16(b[4:], id)
	binary.BigEndian.PutUint16(b[6:], sq)
	return b
}

func TestParseRawReply(t *testing.T) {
	// Echo Reply
	if k, ok := parseRawReply(ipHdr(1, echo(0, 7, 9)), 7, 9); !ok || k != Echo {
		t.Error("echo reply")
	}
	if _, ok := parseRawReply(ipHdr(1, echo(0, 7, 8)), 7, 9); ok {
		t.Error("seq 不符不应匹配")
	}
	// TTL 超时：负载为原始 IP 头 + 原始 echo 请求
	orig := ipHdr(1, echo(8, 7, 9))
	te := append(echo(11, 0, 0), orig...)
	if k, ok := parseRawReply(ipHdr(1, te), 7, 9); !ok || k != Expired {
		t.Error("ttl exceeded")
	}
	un := append(echo(3, 0, 0), orig...)
	if k, ok := parseRawReply(ipHdr(1, un), 7, 9); !ok || k != Unreach {
		t.Error("unreachable")
	}
	if _, ok := parseRawReply(ipHdr(1, te), 8, 9); ok {
		t.Error("别人的探测不应匹配")
	}
	// 截断 / 垃圾输入不 panic
	for _, b := range [][]byte{nil, {1, 2, 3}, ipHdr(1, nil), ipHdr(1, echo(11, 0, 0))} {
		if _, ok := parseRawReply(b, 7, 9); ok {
			t.Error("垃圾输入不应匹配")
		}
	}
}
