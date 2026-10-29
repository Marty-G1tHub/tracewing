//go:build windows

package ping

import (
	"context"
	"encoding/binary"
	"net"
	"syscall"
	"time"
	"unsafe"
)

// Windows 的 IcmpSendEcho（iphlpapi.dll）：普通用户即可调用，不需要管理员。
var (
	iphlp         = syscall.NewLazyDLL("iphlpapi.dll")
	pIcmpCreate   = iphlp.NewProc("IcmpCreateFile")
	pIcmpClose    = iphlp.NewProc("IcmpCloseHandle")
	pIcmpSendEcho = iphlp.NewProc("IcmpSendEcho")
)

const (
	ipSuccess        = 0
	ipTTLExpired     = 11013
	ipTimedOut       = 11010
	ipDestNetUnreach = 11002
	ipReqTimedOut    = 11010
)

type ipOptionInfo struct {
	Ttl         uint8
	Tos         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData uintptr
}

type winICMP struct{}

func newICMP() Pinger {
	if pIcmpCreate.Find() != nil || pIcmpSendEcho.Find() != nil {
		return nil
	}
	return winICMP{}
}

func (winICMP) Name() string { return "icmp(IcmpSendEcho)" }

func (winICMP) Ping(ctx context.Context, dst net.IP, ttl int, timeout time.Duration) Reply {
	if ctx.Err() != nil {
		return Reply{Kind: Error, Err: "canceled"}
	}
	d4 := dst.To4()
	if d4 == nil {
		return Reply{Kind: Error, Err: "仅支持 IPv4"}
	}
	h, _, _ := pIcmpCreate.Call()
	if h == 0 || h == ^uintptr(0) {
		return Reply{Kind: Error, Err: "IcmpCreateFile 失败"}
	}
	defer pIcmpClose.Call(h)

	payload := []byte("tracewing-probe-0123456789abcdef")
	buf := make([]byte, 512)
	var opt *ipOptionInfo
	if ttl > 0 {
		opt = &ipOptionInfo{Ttl: uint8(ttl)}
	}
	// 目标地址：网络字节序的 uint32，内存布局 = IPv4 字节顺序（小端机上 LittleEndian 读出）。
	addr := binary.LittleEndian.Uint32(d4)
	ms := uint32(timeout / time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	t0 := time.Now()
	n, _, _ := pIcmpSendEcho.Call(h, uintptr(addr),
		uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)),
		uintptr(unsafe.Pointer(opt)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(ms))
	rtt := time.Since(t0)
	if n == 0 {
		// 无回应：ICMP_ECHO_REPLY 可能仍写了状态；超时是最常见情形
		return Reply{Kind: Timeout}
	}
	// ICMP_ECHO_REPLY: Address u32 @0, Status u32 @4
	from := net.IPv4(buf[0], buf[1], buf[2], buf[3])
	status := binary.LittleEndian.Uint32(buf[4:8])
	switch status {
	case ipSuccess:
		return Reply{Kind: Echo, RTT: rtt, From: from}
	case ipTTLExpired:
		return Reply{Kind: Expired, RTT: rtt, From: from}
	case ipTimedOut:
		return Reply{Kind: Timeout}
	default:
		if status >= 11002 && status <= 11005 {
			return Reply{Kind: Unreach, RTT: rtt, From: from}
		}
		return Reply{Kind: Error, Err: "ICMP status " + itoa(int(status))}
	}
}
