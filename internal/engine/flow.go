package engine

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/model"
)

// FlowSpec 一次 HTTP 吞吐测量：N 条独立 TCP 连接并发，持续 Duration，
// 只统计预热 Warmup 之后的稳态窗口。
type FlowSpec struct {
	Client    *http.Client
	Dir       string // down | up
	URL       string // 下载 URL 可含 {bytes}
	Range     bool   // 下载用 HTTP Range 分段
	TotalSize int64  // Range 模式下文件总大小（未知=0）
	Streams   int
	Duration  time.Duration
	Warmup    time.Duration
	Chunk     int64 // 每个请求的字节数
}

var (
	randOnce  sync.Once
	randBlock []byte
)

// 上传数据用随机字节（全零在有压缩的链路上会虚高）。1MiB 随机块循环使用。
func block() []byte {
	randOnce.Do(func() {
		randBlock = make([]byte, 1<<20)
		_, _ = rand.Read(randBlock)
	})
	return randBlock
}

type upBody struct {
	left int64
	pos  int
	add  func(int)
	ctx  context.Context
}

func (b *upBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, io.EOF
	}
	if b.ctx.Err() != nil {
		return 0, b.ctx.Err()
	}
	blk := block()
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n := copy(p, blk[b.pos:])
	b.pos = (b.pos + n) % len(blk)
	b.left -= int64(n)
	if b.add != nil {
		b.add(n)
	}
	return n, nil
}

// UserAgent：不少镜像站会拒绝 Go 默认 UA（TUNA/BFSU/阿里云返回 403，NJU 返回 429），
// 也有的对 "Mozilla/…" 开头但不像浏览器的请求返回 403（Cloudflare 防护），
// 实测简短的 "tracewing/1.0" 这类自定义 UA 通过率最高。
const UserAgent = "tracewing/1.0"

// RunFlow 执行一轮。永远返回一个 Round：失败/部分完成时 OK=false 并写明原因，
// 不会把“部分完成”的低速当成结果。
func RunFlow(ctx context.Context, sp FlowSpec, sink Sink) model.Round {
	sink = sinkOr(sink)
	if sp.Chunk <= 0 {
		sp.Chunk = 32 << 20
	}
	if sp.Streams < 1 {
		sp.Streams = 1
	}
	rctx, cancel := context.WithTimeout(ctx, sp.Duration)
	defer cancel()

	var m Meter
	start := time.Now()
	var (
		mu       sync.Mutex
		firstErr string
		dead     int
		perBytes = make([]int64, sp.Streams)
	)
	var wg sync.WaitGroup
	for i := 0; i < sp.Streams; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var mine Meter
			cnt := &countMeter{a: &m, b: &mine}
			err := streamLoop(rctx, sp, i, cnt)
			mu.Lock()
			perBytes[i] = mine.Total()
			if err != nil && rctx.Err() == nil {
				dead++
				if firstErr == "" {
					firstErr = err.Error()
				}
			}
			mu.Unlock()
		}(i)
	}

	cum := Sampler(rctx, &m, start, 100*time.Millisecond, func(c []Sample) {
		sink.Live(trailingMbps(c, 1.0), c[len(c)-1].T/sp.Duration.Seconds())
	})
	wg.Wait()
	// 若 ctx 被上层取消（用户停止），也走到这里。
	r := model.Round{Streams: sp.Streams, Bytes: m.Total()}
	for _, b := range perBytes {
		if b > 0 {
			r.OKStreams++
		}
	}
	mu.Lock()
	defer mu.Unlock()
	end := sp.Duration.Seconds()
	mbps, win, wb, ok := Window(cum, sp.Warmup.Seconds(), end)
	r.Mbps = round2(mbps)
	r.MBps = round2(mbps / 8)
	r.WindowSec = round2(win)
	r.Series = Series(cum)
	switch {
	case ctx.Err() != nil:
		r.Err = "已取消"
	case dead > 0 || r.OKStreams < sp.Streams:
		r.Err = fmt.Sprintf("parallel_incomplete ok=%d/%d bytes=%d", sp.Streams-dead, sp.Streams, r.Bytes)
		if firstErr != "" {
			r.Err += "：" + firstErr
		}
	case !ok || wb < 50000:
		r.Err = fmt.Sprintf("数据不足（统计窗口字节=%d）", wb)
		if firstErr != "" {
			r.Err += "：" + firstErr
		}
	default:
		r.OK = true
	}
	if !r.OK {
		r.Mbps, r.MBps = round2(mbps), round2(mbps/8) // 仅供参考；汇总只取 OK 轮次
	}
	return r
}

type countMeter struct{ a, b *Meter }

func (c *countMeter) Add(n int) { c.a.Add(n); c.b.Add(n) }

// streamLoop 单流：不断发请求直到 ctx 结束。同一流内允许 2 次重连，超过算该流失败。
func streamLoop(ctx context.Context, sp FlowSpec, idx int, cnt *countMeter) error {
	off := int64(0)
	if sp.Range && sp.TotalSize > 0 {
		off = sp.TotalSize / int64(sp.Streams) * int64(idx)
	}
	buf := make([]byte, 256<<10)
	fails := 0
	for ctx.Err() == nil {
		var err error
		if sp.Dir == "up" {
			err = oneUpload(ctx, sp, cnt)
		} else {
			var n int64
			n, err = oneDownload(ctx, sp, off, buf, cnt)
			if sp.Range && sp.TotalSize > 0 {
				off += sp.Chunk
				if off+sp.Chunk > sp.TotalSize {
					off = 0
				}
			} else {
				_ = n
			}
		}
		if err == nil {
			fails = 0
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		fails++
		if fails > 2 {
			return err
		}
		wait := 200 * time.Millisecond
		if strings.Contains(err.Error(), "HTTP 429") || strings.Contains(err.Error(), "HTTP 503") {
			wait = 1500 * time.Millisecond // 被限流：退避后再试
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
	return nil
}

func oneDownload(ctx context.Context, sp FlowSpec, off int64, buf []byte, cnt *countMeter) (int64, error) {
	u := strings.ReplaceAll(sp.URL, "{bytes}", strconv.FormatInt(sp.Chunk, 10))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return 0, err
	}
	if sp.Range {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+sp.Chunk-1))
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := sp.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var total int64
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			cnt.Add(n)
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func oneUpload(ctx context.Context, sp FlowSpec, cnt *countMeter) error {
	body := &upBody{left: sp.Chunk, add: cnt.Add, ctx: ctx}
	req, err := http.NewRequestWithContext(ctx, "POST", sp.URL, body)
	if err != nil {
		return err
	}
	req.ContentLength = sp.Chunk
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := sp.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 && resp.StatusCode != 201 && resp.StatusCode != 204 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// ---------- 探测 ----------

// ProbeResult 是对一个端点的快速可用性探测。
type ProbeResult struct {
	OK     bool
	Status int
	Bytes  int64
	Total  int64 // 文件总大小（Range 模式）
	Range  bool  // 服务端确实支持 Range（返回 206）
	Err    string
}

// Probe 下载端点：取前 1 MiB（或 {bytes}=1.5MB）；≥200000 字节算可用。上传端点：POST 1 MiB，200/204 算可用。
func Probe(ctx context.Context, c *http.Client, ep Endpoint) ProbeResult {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var res ProbeResult
	if ep.Dir == "up" {
		body := &upBody{left: 1 << 20, ctx: ctx}
		req, err := http.NewRequestWithContext(ctx, "POST", ep.URL, body)
		if err != nil {
			res.Err = err.Error()
			return res
		}
		req.ContentLength = 1 << 20
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("User-Agent", UserAgent)
		resp, err := c.Do(req)
		if err != nil {
			res.Err = shortErr(err)
			return res
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		res.Status = resp.StatusCode
		res.Bytes = 1 << 20
		res.OK = resp.StatusCode == 200 || resp.StatusCode == 201 || resp.StatusCode == 204
		if !res.OK {
			res.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return res
	}
	u := strings.ReplaceAll(ep.URL, "{bytes}", "1500000")
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	if ep.Range {
		req.Header.Set("Range", "bytes=0-1048575")
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.Do(req)
	if err != nil {
		res.Err = shortErr(err)
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		res.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return res
	}
	n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
	res.Bytes = n
	if resp.StatusCode == 206 {
		res.Range = true
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndex(cr, "/"); i >= 0 {
				res.Total, _ = strconv.ParseInt(cr[i+1:], 10, 64)
			}
		}
	} else {
		res.Total = resp.ContentLength
	}
	if n < 200000 {
		res.Err = fmt.Sprintf("只读到 %d 字节（<200000）", n)
		return res
	}
	res.OK = true
	return res
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
