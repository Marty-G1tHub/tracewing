//go:build linux

package ping

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"time"
)

// Linux 实现两种方式：
//   - 原始套接字（需要 CAP_NET_RAW，agent 的 systemd 单元只授予这一项能力）：可发任意 TTL，
//     能收到途中路由器的“TTL 超时”，因此能做逐跳 traceroute；
//   - 非特权 ICMP 数据报套接字（需要 net.ipv4.ping_group_range 放行）：只能得到目标的回应，没有逐跳。
type linICMP struct{ raw bool }

func newICMP() Pinger {
	if fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_ICMP); err == nil {
		syscall.Close(fd)
		return linICMP{raw: true}
	}
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_ICMP)
	if err != nil {
		return nil
	}
	syscall.Close(fd)
	return linICMP{}
}

func (l linICMP) Name() string {
	if l.raw {
		return "icmp(raw)"
	}
	return "icmp(dgram)"
}

// Hops 说明能否做逐跳探测（只有原始套接字可以）。
func (l linICMP) Hops() bool { return l.raw }

func csum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	s = (s >> 16) + (s & 0xffff)
	s += s >> 16
	return ^uint16(s)
}

var seq atomic.Uint32

func (l linICMP) Ping(ctx context.Context, dst net.IP, ttl int, timeout time.Duration) Reply {
	d4 := dst.To4()
	if d4 == nil {
		return Reply{Kind: Error, Err: "仅支持 IPv4"}
	}
	if l.raw {
		return rawPing(ctx, d4, ttl, timeout)
	}
	return dgramPing(ctx, d4, ttl, timeout)
}

// rawPing：自己构造 Echo，按 (id, seq) 在收到的 ICMP 里认领回应；
// TTL 超时 / 不可达报文里带着原始 IP 头 + 原始 ICMP 前 8 字节，同样按 (id, seq) 认领。
func rawPing(ctx context.Context, d4 net.IP, ttl int, timeout time.Duration) Reply {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_ICMP)
	if err != nil {
		return Reply{Kind: Error, Err: err.Error()}
	}
	defer syscall.Close(fd)
	if ttl > 0 {
		syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, syscall.IP_TTL, ttl)
	}
	id := uint16(os.Getpid())
	sq := uint16(seq.Add(1))
	msg := make([]byte, 8+24)
	msg[0] = 8
	binary.BigEndian.PutUint16(msg[4:], id)
	binary.BigEndian.PutUint16(msg[6:], sq)
	for i := 8; i < len(msg); i++ {
		msg[i] = byte(i)
	}
	binary.BigEndian.PutUint16(msg[2:], csum(msg))
	var sa syscall.SockaddrInet4
	copy(sa.Addr[:], d4)
	t0 := time.Now()
	if err := syscall.Sendto(fd, msg, 0, &sa); err != nil {
		return Reply{Kind: Error, Err: err.Error()}
	}
	deadline := t0.Add(timeout)
	buf := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return Reply{Kind: Error, Err: "canceled"}
		}
		left := time.Until(deadline)
		if left <= 0 {
			return Reply{Kind: Timeout}
		}
		tv := syscall.NsecToTimeval(int64(minDur(left, 200*time.Millisecond)))
		syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
		n, from, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK || err == syscall.EINTR {
				continue
			}
			return Reply{Kind: Error, Err: err.Error()}
		}
		rtt := time.Since(t0)
		src, ok := from.(*syscall.SockaddrInet4)
		if !ok {
			continue
		}
		k, match := parseRawReply(buf[:n], id, sq)
		if !match {
			continue
		}
		return Reply{Kind: k, RTT: rtt, From: net.IP(src.Addr[:]).To4()}
	}
}

// parseRawReply 解析原始套接字收到的 IPv4 包（含 IP 头），判断是不是对 (id, sq) 这次探测的回应。
func parseRawReply(b []byte, id, sq uint16) (Kind, bool) {
	if len(b) < 20 {
		return "", false
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || len(b) < ihl+8 {
		return "", false
	}
	ic := b[ihl:]
	switch ic[0] {
	case 0: // Echo Reply
		if binary.BigEndian.Uint16(ic[4:]) == id && binary.BigEndian.Uint16(ic[6:]) == sq {
			return Echo, true
		}
	case 11, 3: // TTL 超时 / 目的不可达：负载是原始 IP 头 + 原始 ICMP 前 8 字节
		in := ic[8:]
		if len(in) < 28 {
			return "", false
		}
		ihl2 := int(in[0]&0x0f) * 4
		if ihl2 < 20 || len(in) < ihl2+8 || in[9] != 1 || in[ihl2] != 8 {
			return "", false
		}
		if binary.BigEndian.Uint16(in[ihl2+4:]) == id && binary.BigEndian.Uint16(in[ihl2+6:]) == sq {
			if ic[0] == 11 {
				return Expired, true
			}
			return Unreach, true
		}
	}
	return "", false
}

func dgramPing(ctx context.Context, d4 net.IP, ttl int, timeout time.Duration) Reply {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_ICMP)
	if err != nil {
		return Reply{Kind: Error, Err: err.Error()}
	}
	f := os.NewFile(uintptr(fd), "icmp")
	defer f.Close()
	if ttl > 0 {
		syscall.SetsockoptInt(fd, syscall.IPPROTO_IP, syscall.IP_TTL, ttl)
	}
	msg := make([]byte, 16)
	msg[0] = 8 // echo request
	binary.BigEndian.PutUint16(msg[6:], uint16(seq.Add(1)))
	binary.BigEndian.PutUint16(msg[2:], csum(msg))
	var sa syscall.SockaddrInet4
	copy(sa.Addr[:], d4)
	t0 := time.Now()
	if err := syscall.Sendto(fd, msg, 0, &sa); err != nil {
		return Reply{Kind: Error, Err: err.Error()}
	}
	deadline := t0.Add(timeout)
	buf := make([]byte, 512)
	for {
		if ctx.Err() != nil {
			return Reply{Kind: Error, Err: "canceled"}
		}
		left := time.Until(deadline)
		if left <= 0 {
			return Reply{Kind: Timeout}
		}
		tv := syscall.NsecToTimeval(int64(minDur(left, 200*time.Millisecond)))
		syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
		n, from, err := syscall.Recvfrom(fd, buf, 0)
		if err == nil {
			if n >= 1 && buf[0] == 0 {
				var ip net.IP
				if s, ok := from.(*syscall.SockaddrInet4); ok {
					ip = net.IP(s.Addr[:]).To4()
				}
				return Reply{Kind: Echo, RTT: time.Since(t0), From: ip}
			}
			continue
		}
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK || err == syscall.EINTR {
			continue
		}
		return Reply{Kind: Error, Err: err.Error()}
	}
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
