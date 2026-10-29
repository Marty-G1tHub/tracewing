package srv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/judge"
	"github.com/Marty-G1tHub/tracewing/internal/model"
)

// Client 访问自建服务端。HTTP 客户端由调用方给（已决定走哪条路径）。
type Client struct {
	Base  string // http://host:port
	Token string
	HTTP  *http.Client // 未加鉴权头的底层客户端
	nonce string
}

// NormalizeBase 接受 host、host:port、http://host:port，默认端口 9527。
func NormalizeBase(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("服务器地址为空")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("服务器地址无效")
	}
	port := u.Port()
	if port == "" {
		port = "9527"
	}
	return "http://" + net.JoinHostPort(u.Hostname(), port), nil
}

type authRT struct {
	rt     http.RoundTripper
	header string
}

func (a authRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.Header.Set("Authorization", a.header)
	return a.rt.RoundTrip(r2)
}

// Login 取 nonce，之后 Authed() 返回自动带鉴权头的 *http.Client。
func (c *Client) Login(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.Base+"/v1/challenge", nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var v struct{ Nonce, Version string }
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&v) != nil || v.Nonce == "" {
		return "", fmt.Errorf("这不像本程序的服务端（HTTP %d）", resp.StatusCode)
	}
	c.nonce = v.Nonce
	return v.Version, nil
}

// Authed 返回带鉴权头的客户端副本（共享底层 Transport）。
func (c *Client) Authed() *http.Client {
	cc := *c.HTTP
	rt := cc.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	cc.Transport = authRT{rt: rt, header: AuthHeader(c.Token, c.nonce)}
	return &cc
}

func (c *Client) getJSON(ctx context.Context, path string, v any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.Base+path, nil)
	resp, err := c.Authed().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 401 {
		return fmt.Errorf("令牌不对（服务端拒绝）")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, v)
}

func (c *Client) Info(ctx context.Context) (*model.ServerInfo, error) {
	var v model.ServerInfo
	if err := c.getJSON(ctx, "/v1/info", &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func (c *Client) DownURL() string { return c.Base + "/v1/down?bytes={bytes}" }
func (c *Client) UpURL() string   { return c.Base + "/v1/up" }

// Resources 请求服务端在 d 时长内采样（阻塞）。
func (c *Client) Resources(ctx context.Context, d time.Duration) (*model.Resources, error) {
	var v model.Resources
	if err := c.getJSON(ctx, "/v1/res?sec="+strconv.FormatFloat(d.Seconds(), 'f', 1, 64), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// EchoRTT 在一条保持连接上串行发 n 个极小请求：测的是“应用层往返”
// （含服务端处理，不含建连）。timeouts 记为 -1。
func (c *Client) EchoRTT(ctx context.Context, n int, interval time.Duration, sink func(i int)) model.Echo {
	e := model.Echo{What: "HTTP 小请求往返（保持连接，不含建连/TLS；含服务端处理）", Samples: []float64{}}
	hc := c.Authed()
	hcc := *hc
	hcc.Timeout = 3 * time.Second
	// 预热一次建好连接，不计入样本
	if req, err := http.NewRequestWithContext(ctx, "GET", c.Base+"/v1/echo", nil); err == nil {
		if r, err := hcc.Do(req); err == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
		}
	}
	for i := 0; i < n && ctx.Err() == nil; i++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", c.Base+"/v1/echo", nil)
		t0 := time.Now()
		r, err := hcc.Do(req)
		rtt := float64(time.Since(t0)) / float64(time.Millisecond)
		if err == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if r.StatusCode == 200 {
				e.Samples = append(e.Samples, judge.Round2(rtt))
			} else {
				e.Samples = append(e.Samples, -1)
			}
		} else {
			e.Samples = append(e.Samples, -1)
		}
		if sink != nil {
			sink(i + 1)
		}
		select {
		case <-ctx.Done():
		case <-time.After(interval):
		}
	}
	FillEcho(&e)
	return e
}

// FillEcho 由 Samples（<0=丢失）补全统计字段。
func FillEcho(e *model.Echo) {
	var ok []float64
	for _, s := range e.Samples {
		if s >= 0 {
			ok = append(ok, s)
		}
	}
	e.Sent, e.Recv = len(e.Samples), len(ok)
	if e.Sent > 0 {
		e.Timeout = judge.Round2(float64(e.Sent-e.Recv) / float64(e.Sent))
	}
	if len(ok) == 0 {
		return
	}
	e.P50 = judge.Ptr(judge.Round2(judge.Percentile(ok, 50)))
	e.P95 = judge.Ptr(judge.Round2(judge.Percentile(ok, 95)))
	e.P99 = judge.Ptr(judge.Round2(judge.Percentile(ok, 99)))
	mn, _, _, _ := judge.MinMedMax(ok)
	e.Min = judge.Ptr(judge.Round2(mn))
	e.Jitter = judge.Ptr(judge.Round2(judge.MeanAbsDiff(ok)))
}

// TCPConnect 测 n 次纯 TCP 建连时间（SYN→SYN/ACK 的近似），使用直连拨号：
// 它走的是系统路由（含 TUN），不经 HTTP/SOCKS 代理——结果里会注明。
func (c *Client) TCPConnect(ctx context.Context, n int, interval time.Duration) model.Echo {
	u, err := url.Parse(c.Base)
	if err != nil {
		return model.Echo{}
	}
	return TCPConnectTo(ctx, u.Host, n, interval)
}

// TCPConnectTo 测 n 次到 host:port 的纯 TCP 建连时间（SYN→SYN/ACK 的近似）。
// 直接拨号，不经 HTTP/SOCKS 代理；经 TUN 时测到的是“到代理软件”的时延——结果里会注明。
func TCPConnectTo(ctx context.Context, hostport string, n int, interval time.Duration) model.Echo {
	e := model.Echo{What: "TCP 建连耗时（直接拨号，不经 HTTP/SOCKS 代理；经 TUN 时是到代理软件的时延）", Samples: []float64{}}
	d := net.Dialer{Timeout: 3 * time.Second}
	for i := 0; i < n && ctx.Err() == nil; i++ {
		t0 := time.Now()
		cn, err := d.DialContext(ctx, "tcp", hostport)
		if err != nil {
			e.Samples = append(e.Samples, -1)
		} else {
			e.Samples = append(e.Samples, judge.Round2(float64(time.Since(t0))/float64(time.Millisecond)))
			cn.Close()
		}
		select {
		case <-ctx.Done():
		case <-time.After(interval):
		}
	}
	FillEcho(&e)
	return e
}

// UDP 测试：dir=up（本机→服务器）或 down。UDP 不经 HTTP/SOCKS 代理，只走系统路由。
func (c *Client) UDP(ctx context.Context, dir string, mbps float64, secs int) model.UDPResult {
	res := model.UDPResult{Dir: dir, TargetMbps: mbps}
	u, _ := url.Parse(c.Base)
	raddr, err := net.ResolveUDPAddr("udp4", u.Host)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	conn, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer conn.Close()
	sid := rand.Uint32()
	const size = 1200
	// hello：登记地址并确认 UDP 能通
	hb := make([]byte, size)
	ok := false
	for i := 0; i < 5 && !ok && ctx.Err() == nil; i++ {
		putPkt(hb, typHello, sid, 0, time.Now().UnixNano())
		conn.Write(hb)
		conn.SetReadDeadline(time.Now().Add(600 * time.Millisecond))
		rb := make([]byte, 2048)
		if n, err := conn.Read(rb); err == nil {
			if t, s, _, _, good := getPkt(rb[:n]); good && t == typHello && s == sid {
				ok = true
			}
		}
	}
	if !ok {
		res.Err = "UDP 握手无回应：服务端 UDP 端口（与 TCP 同号）可能被防火墙/NAT 拦截"
		return res
	}
	d := time.Duration(secs) * time.Second
	switch dir {
	case "up":
		res.Sent = Blast(ctx, func(b []byte) { conn.Write(b) }, sid, mbps, d, size)
		time.Sleep(400 * time.Millisecond) // 等尾包到达
		var st struct {
			Recv, Bytes, Reordered int
			JitterMs               float64 `json:"jitter_ms"`
			Mbps                   float64
		}
		if err := c.getJSON(ctx, "/v1/udp/stat?sid="+strconv.FormatUint(uint64(sid), 10), &st); err != nil {
			res.Err = err.Error()
			return res
		}
		res.Recv, res.Reordered, res.JitterMs, res.Mbps = st.Recv, st.Reordered, judge.Round2(st.JitterMs), judge.Round2(st.Mbps)
	case "down":
		stat := NewUStat()
		done := make(chan struct{})
		go func() {
			defer close(done)
			rb := make([]byte, 2048)
			for {
				conn.SetReadDeadline(time.Now().Add(d + 5*time.Second))
				n, err := conn.Read(rb)
				if err != nil {
					return
				}
				if t, s, seq, ns, good := getPkt(rb[:n]); good && t == typData && s == sid {
					stat.Add(seq, ns, n, time.Now())
				}
			}
		}()
		var v struct{ Sent int }
		path := fmt.Sprintf("/v1/udp/down?sid=%d&mbps=%g&secs=%d&size=%d", sid, mbps, secs, size)
		err := c.getJSON(ctx, path, &v)
		time.Sleep(400 * time.Millisecond)
		conn.SetReadDeadline(time.Now()) // 结束读协程
		<-done
		if err != nil {
			res.Err = err.Error()
			return res
		}
		res.Sent, res.Recv, res.Reordered = v.Sent, stat.Recv, stat.Reordered
		res.JitterMs, res.Mbps = judge.Round2(stat.Jitter), judge.Round2(stat.Mbps())
	default:
		res.Err = "dir 必须是 up 或 down"
		return res
	}
	if res.Sent > 0 {
		lost := res.Sent - res.Recv
		if lost < 0 {
			lost = 0
		}
		res.LossPct = judge.Round2(float64(lost) / float64(res.Sent) * 100)
	}
	return res
}
