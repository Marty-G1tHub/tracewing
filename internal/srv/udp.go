package srv

import (
	"context"
	"encoding/binary"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// UDP 报文：magic"STUD"(4) type(1) sid(4) seq(4) sendNs(8) + 填充。
const (
	udpHdr   = 21
	typHello = 1
	typData  = 2
)

var magic = [4]byte{'S', 'T', 'U', 'D'}

func putPkt(b []byte, typ byte, sid, seq uint32, ns int64) {
	copy(b[:4], magic[:])
	b[4] = typ
	binary.BigEndian.PutUint32(b[5:], sid)
	binary.BigEndian.PutUint32(b[9:], seq)
	binary.BigEndian.PutUint64(b[13:], uint64(ns))
}

func getPkt(b []byte) (typ byte, sid, seq uint32, ns int64, ok bool) {
	if len(b) < udpHdr || b[0] != magic[0] || b[1] != magic[1] || b[2] != magic[2] || b[3] != magic[3] {
		return
	}
	return b[4], binary.BigEndian.Uint32(b[5:]), binary.BigEndian.Uint32(b[9:]), int64(binary.BigEndian.Uint64(b[13:])), true
}

// UStat 接收侧统计：丢包按“发送总数 − 收到数”由调用方算；这里负责乱序、抖动（RFC 3550）、速率。
type UStat struct {
	Recv, Bytes, Reordered int
	MaxSeq                 int64
	Jitter                 float64 // ms
	first, last            time.Time
	firstBytes             int
	prev                   float64
	havePrev               bool
}

func NewUStat() *UStat { return &UStat{MaxSeq: -1} }

func (u *UStat) Add(seq uint32, sendNs int64, size int, now time.Time) {
	if u.Recv == 0 {
		u.first, u.firstBytes = now, size
	}
	u.last = now
	u.Recv++
	u.Bytes += size
	if int64(seq) < u.MaxSeq {
		u.Reordered++
	} else {
		u.MaxSeq = int64(seq)
	}
	// 发送端与接收端时钟偏移是常数，在相邻差里抵消。
	transit := float64(now.UnixNano()-sendNs) / 1e6
	if u.havePrev {
		d := math.Abs(transit - u.prev)
		u.Jitter += (d - u.Jitter) / 16
	}
	u.prev, u.havePrev = transit, true
}

// Mbps 以首包到末包的时间窗计算（不含首包字节）。
func (u *UStat) Mbps() float64 {
	d := u.last.Sub(u.first).Seconds()
	if u.Recv < 2 || d <= 0 {
		return 0
	}
	return float64(u.Bytes-u.firstBytes) * 8 / d / 1e6
}

type sess struct {
	addr *net.UDPAddr
	st   *UStat
	seen time.Time
}

type udpServer struct {
	c  *net.UDPConn
	mu sync.Mutex
	m  map[uint32]*sess
}

func newUDPServer(c *net.UDPConn) *udpServer { return &udpServer{c: c, m: map[uint32]*sess{}} }

func (u *udpServer) loop() {
	buf := make([]byte, 2048)
	for {
		n, from, err := u.c.ReadFromUDP(buf)
		if err != nil {
			return
		}
		typ, sid, seq, ns, ok := getPkt(buf[:n])
		if !ok {
			continue
		}
		now := time.Now()
		u.mu.Lock()
		s := u.m[sid]
		if s == nil {
			if len(u.m) > 256 { // 清理陈旧会话
				for k, v := range u.m {
					if now.Sub(v.seen) > 10*time.Minute {
						delete(u.m, k)
					}
				}
			}
			s = &sess{st: NewUStat()}
			u.m[sid] = s
		}
		s.addr, s.seen = from, now
		if typ == typData {
			s.st.Add(seq, ns, n, now)
		}
		u.mu.Unlock()
		if typ == typHello {
			_, _ = u.c.WriteToUDP(buf[:n], from)
		}
	}
}

func (s *Server) udpStat(w http.ResponseWriter, r *http.Request) {
	u := s.udpOrErr(w)
	if u == nil {
		return
	}
	sid, _ := strconv.ParseUint(r.URL.Query().Get("sid"), 10, 32)
	u.mu.Lock()
	ss := u.m[uint32(sid)]
	var out map[string]any
	if ss != nil {
		out = map[string]any{"recv": ss.st.Recv, "bytes": ss.st.Bytes, "reordered": ss.st.Reordered, "jitter_ms": ss.st.Jitter, "mbps": ss.st.Mbps()}
	}
	u.mu.Unlock()
	if out == nil {
		http.Error(w, "no such session (没收到任何 UDP 包：UDP 被防火墙/NAT 拦了？)", http.StatusNotFound)
		return
	}
	js(w, out)
}

// udpDown：服务端按目标速率向已“打洞”的客户端地址发 UDP。
func (s *Server) udpDown(w http.ResponseWriter, r *http.Request) {
	u := s.udpOrErr(w)
	if u == nil {
		return
	}
	q := r.URL.Query()
	sid, _ := strconv.ParseUint(q.Get("sid"), 10, 32)
	mbps, _ := strconv.ParseFloat(q.Get("mbps"), 64)
	secs, _ := strconv.ParseFloat(q.Get("secs"), 64)
	size, _ := strconv.Atoi(q.Get("size"))
	if size < udpHdr || size > 1472 {
		size = 1200
	}
	if mbps <= 0 || mbps > 10000 || secs <= 0 || secs > 60 {
		http.Error(w, "bad params", 400)
		return
	}
	u.mu.Lock()
	ss := u.m[uint32(sid)]
	u.mu.Unlock()
	if ss == nil || ss.addr == nil {
		http.Error(w, "客户端 UDP 地址未登记（hello 没到服务端）", http.StatusNotFound)
		return
	}
	sent := Blast(r.Context(), func(b []byte) { _, _ = u.c.WriteToUDP(b, ss.addr) }, uint32(sid), mbps, time.Duration(secs*float64(time.Second)), size)
	js(w, map[string]int{"sent": sent})
}

// Blast 以目标速率发包，返回发出的包数。send 失败的包也算“发了”（丢包由接收端统计）。
func Blast(ctx context.Context, send func([]byte), sid uint32, mbps float64, d time.Duration, size int) int {
	buf := make([]byte, size)
	bytesPerSec := mbps * 1e6 / 8
	start := time.Now()
	n := 0
	for {
		el := time.Since(start)
		if el >= d || ctx.Err() != nil {
			return n
		}
		due := int(bytesPerSec * el.Seconds() / float64(size))
		if due-n > 2000 { // 落后太多（睡眠粒度）不一次性补发爆发
			due = n + 2000
		}
		for ; n < due; n++ {
			putPkt(buf, typData, sid, uint32(n), time.Now().UnixNano())
			send(buf)
		}
		time.Sleep(time.Millisecond)
	}
}
