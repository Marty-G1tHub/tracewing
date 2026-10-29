//go:build linux

package srv

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/model"
)

type cpuT struct{ user, nice, sys, idle, iow, irq, sirq, steal float64 }

func readCPU() (cpuT, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuT{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return cpuT{}, false
	}
	fs := strings.Fields(sc.Text())
	if len(fs) < 9 || fs[0] != "cpu" {
		return cpuT{}, false
	}
	v := make([]float64, 8)
	for i := range v {
		v[i], _ = strconv.ParseFloat(fs[i+1], 64)
	}
	return cpuT{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]}, true
}

func memAvail() (float64, bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	var tot, av float64
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			tot, _ = strconv.ParseFloat(f[1], 64)
		case "MemAvailable:":
			av, _ = strconv.ParseFloat(f[1], 64)
		}
	}
	if tot == 0 {
		return 0, false
	}
	return av / tot * 100, true
}

// SampleResources 在 d 时长内对 CPU（含 steal）与内存取样。
func SampleResources(ctx context.Context, d time.Duration) model.Resources {
	r := model.Resources{}
	a, ok := readCPU()
	if !ok {
		r.Note = "无法读取 /proc/stat"
		return r
	}
	t0 := time.Now()
	minMem := 101.0
	end := time.After(d)
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-end:
			break loop
		case <-time.After(500 * time.Millisecond):
			if m, ok := memAvail(); ok && m < minMem {
				minMem = m
			}
		}
	}
	b, _ := readCPU()
	r.Seconds = time.Since(t0).Seconds()
	tot := func(c cpuT) float64 { return c.user + c.nice + c.sys + c.idle + c.iow + c.irq + c.sirq + c.steal }
	dt := tot(b) - tot(a)
	if dt <= 0 {
		r.Note = "取样间隔内 CPU 计数无变化"
		return r
	}
	pct := func(x float64) *float64 { v := round1(x / dt * 100); return &v }
	r.Available = true
	r.StealPct = pct(b.steal - a.steal)
	r.IdlePct = pct((b.idle - a.idle) + (b.iow - a.iow))
	busy := dt - (b.idle - a.idle) - (b.iow - a.iow) - (b.steal - a.steal)
	r.BusyPct = pct(busy)
	if minMem <= 100 {
		v := round1(minMem)
		r.MemAvailPct = &v
	}
	return r
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
