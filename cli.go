package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/engine"
	"github.com/Marty-G1tHub/tracewing/internal/model"
	"github.com/Marty-G1tHub/tracewing/internal/netx"
	"github.com/Marty-G1tHub/tracewing/internal/ping"
	"github.com/Marty-G1tHub/tracewing/internal/probe"
	"github.com/Marty-G1tHub/tracewing/internal/route"
)

type cliSink struct{ json bool }

func (c cliSink) Status(t string) {
	if !c.json {
		fmt.Fprintln(os.Stderr, "…", t)
	}
}
func (cliSink) Live(float64, float64) {}

// runRouteCheck 是给脚本与 AI 用的无界面入口。退出码：0 正常；1 出错；2 有结果但回程/去程部分失败。
func runRouteCheck(ctx context.Context, addr, token string, asJSON bool) int {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	p := &netx.Path{}
	if err := p.Normalize(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	env := probe.Detect(ctx, p)
	rr := engine.RunRoute(ctx, p, engine.RouteIn{Base: addr, Token: token}, env, ping.New(), cliSink{asJSON})
	rr.Meta = model.Meta{Kind: "route", Schema: model.SchemaVersion, AppVersion: version, CapturedAt: time.Now().Format(time.RFC3339)}
	if asJSON {
		b, _ := json.MarshalIndent(rr, "", "  ")
		fmt.Println(string(b))
	} else {
		printRoute(rr)
	}
	for _, n := range rr.Notes {
		if n.Level == "bad" {
			return 1
		}
	}
	bad := rr.Outbound != nil && rr.Outbound.Err != ""
	for _, l := range rr.Return {
		bad = bad || l.Err != ""
	}
	if bad {
		return 2
	}
	return 0
}

func printRoute(r *model.RouteReport) {
	fmt.Printf("服务器 %s · 前缀表 %s\n", r.Server, r.PrefixVersion)
	for _, h := range r.Headlines {
		fmt.Println("•", h)
	}
	show := func(l *model.RouteLine) {
		if l == nil {
			return
		}
		if l.Err != "" {
			fmt.Printf("  %-18s 失败：%s\n", l.Label, l.Err)
			return
		}
		v := l.Verdict
		fmt.Printf("  %-18s %s [%s] AS路径 %s\n", l.Label, v.Label, route.ConfLabel(v.Confidence), asPath(v.ASPath))
		for _, e := range v.Evidence {
			fmt.Println("      -", e)
		}
	}
	if r.Outbound != nil {
		fmt.Println("去程：")
		show(r.Outbound)
	}
	if len(r.Return) > 0 {
		fmt.Println("回程：")
		for i := range r.Return {
			show(&r.Return[i])
		}
		show(r.Client)
	}
	for _, n := range r.Notes {
		fmt.Printf("[%s] %s\n", n.Level, n.Text)
	}
}

func asPath(a []int) string {
	var s []string
	for _, x := range a {
		s = append(s, fmt.Sprintf("AS%d", x))
	}
	if len(s) == 0 {
		return "-"
	}
	return strings.Join(s, ">")
}
