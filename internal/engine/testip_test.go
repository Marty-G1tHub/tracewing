package engine

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/ping"
)

func TestParseTargets(t *testing.T) {
	ok, rej := ParseTargets("8.8.8.8, 1.1.1.1\n8.8.8.8 192.168.1.1 abc 198.18.0.5 ::1")
	if len(ok) != 2 || len(rej) != 4 {
		t.Fatalf("%v %v", ok, rej)
	}
}

func TestPathSummary(t *testing.T) {
	h := []model.Hop{{TTL: 1, IP: "192.168.1.1"}, {TTL: 2, IP: "1.1.1.1", ASN: "AS1", Holder: "A"}, {TTL: 3, IP: "1.1.1.2", ASN: "AS1", Holder: "A"}, {TTL: 4, IP: "2.2.2.2", ASN: "AS2", Holder: "B"}, {TTL: 5}}
	if got := PathSummary(h); got != "192.168.1.1 → AS1 A → AS2 B" {
		t.Fatal(got)
	}
	var long []model.Hop
	for i := 1; i <= 9; i++ {
		long = append(long, model.Hop{TTL: i, IP: "1.1.1." + string(rune('0'+i)), ASN: "AS" + string(rune('0'+i))})
	}
	if got := PathSummary(long); got != "AS1 → AS2 → AS3 → AS4 → AS5 → … → AS9" {
		t.Fatal(got)
	}
}

type fakePinger struct{}

func (fakePinger) Name() string { return "fake" }
func (fakePinger) Ping(_ context.Context, dst net.IP, ttl int, _ time.Duration) ping.Reply {
	if ttl > 0 && ttl < 3 {
		return ping.Reply{Kind: ping.Expired, RTT: time.Duration(ttl) * time.Millisecond, From: net.IPv4(10, 0, 0, byte(ttl))}
	}
	if ttl == 3 {
		return ping.Reply{Kind: ping.Timeout}
	}
	if dst.String() == "9.9.9.9" {
		return ping.Reply{Kind: ping.Timeout}
	}
	return ping.Reply{Kind: ping.Echo, RTT: 20 * time.Millisecond, From: dst}
}

func TestRunTestIPFake(t *testing.T) {
	ts, trusted := RunTestIP(context.Background(), nil, model.IPParams{Count: 5, TimeoutMs: 100, IntervalMs: 1, Hops: 6, Trace: true}, []string{"8.8.8.8", "9.9.9.9"}, &model.Env{}, fakePinger{}, nil)
	if !trusted || len(ts) != 2 {
		t.Fatal(trusted, ts)
	}
	a, b := ts[0], ts[1]
	if a.Recv != 5 || a.P50 == nil || *a.P50 != 20 || !a.Reached || len(a.Hops) != 4 {
		t.Fatalf("%+v", a)
	}
	if !b.NoReply || b.P50 != nil || b.LossPct != 100 || b.Reached {
		t.Fatalf("%+v", b)
	}
	if len(b.Hops) != 2 { // TTL3 超时、TTL4+ 对 9.9.9.9 也超时 → 裁掉尾部
		t.Fatalf("%+v", b.Hops)
	}
	v := TestIPVerdict(ts, false, "icmp")
	if len(v) < 3 {
		t.Fatal(v)
	}
}
