// tracewing（迹翼）：线路检测 / 宽带测速 / 测试IP / VPS测速。单文件、免安装、免管理员（Windows）。
//
//	tracewing.exe                              打开本机网页界面
//	tracewing --server --port 9527             在你的 VPS 上充当服务端 / 回程 agent
//	tracewing --route-check 1.2.3.4:9527       命令行线路检测（给脚本和 AI 用）
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/srv"
	"github.com/Marty-G1tHub/tracewing/internal/store"
	"github.com/Marty-G1tHub/tracewing/internal/ui"
)

const version = srv.Version

func main() {
	attachConsole()
	var (
		server     = flag.Bool("server", false, "以测速服务端模式运行（部署在你的 VPS 上）")
		port       = flag.Int("port", 9527, "服务端监听端口（TCP 与 UDP 同号）")
		token      = flag.String("token", "", "服务端令牌；留空则随机生成并打印（也可用环境变量 TRACEWING_TOKEN）")
		dataDir    = flag.String("data", "", "数据目录；留空=程序旁的“迹翼数据”，不可写则用用户配置目录")
		noBrowser  = flag.Bool("no-browser", false, "不打开窗口也不打开浏览器，只打印地址")
		useBrowser = flag.Bool("browser", false, "用默认浏览器而不是独立窗口")
		idle       = flag.Duration("idle", 5*time.Minute, "浏览器模式下无人使用多久后自动退出（0=不退出；窗口模式关窗即退出）")
		bind       = flag.String("bind", "", "服务端监听地址（默认全部网卡；例 127.0.0.1 配合反向隧道）")
		noRoute    = flag.Bool("no-route", false, "服务端：关闭回程探测接口")
		routeCheck = flag.String("route-check", "", "命令行线路检测：服务器地址（令牌用 --token 或环境变量 TRACEWING_TOKEN；不给令牌则只测去程；退出码 0 成功 / 1 出错 / 2 部分探测失败）")
		asJSON     = flag.Bool("json", false, "与 --route-check 合用：输出完整 JSON")
		showVer    = flag.Bool("version", false, "打印版本")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "用法：tracewing [选项]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVer {
		fmt.Println("tracewing", version)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *routeCheck != "" {
		tk := *token
		if tk == "" {
			tk = os.Getenv("TRACEWING_TOKEN")
		}
		os.Exit(runRouteCheck(ctx, *routeCheck, tk, *asJSON))
	}
	if *server {
		runServer(ctx, *bind, *port, *token, *noRoute)
		return
	}

	dir, err := store.Resolve(*dataDir)
	if err != nil {
		fatal("无法创建数据目录：" + err.Error())
	}
	st, err := store.Open(dir)
	if err != nil {
		fatal("无法打开数据目录：" + err.Error())
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	app := &ui.App{Version: version, OS: runtime.GOOS + "/" + runtime.GOARCH, Store: st, Quit: cancel, Open: openBrowser}
	ln, url, err := app.Listen()
	if err != nil {
		fatal("无法监听本机端口：" + err.Error())
	}
	fmt.Println("测速界面：", url)
	fmt.Println("数据目录：", dir)

	idleFor := *idle
	if idleFor <= 0 {
		idleFor = 100 * 365 * 24 * time.Hour
	}
	app.Quit = cancel
	app.SetMode("browser", "")
	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(ctx, ln, idleFor) }() // 窗口模式下 Serve 忽略 idle：关窗才退出
	if *noBrowser {
		wait(serveErr)
		return
	}

	// 默认：独立的 WebView2 窗口（关窗即退出）。没有运行时 / 创建失败 / 指定 --browser：回退到默认浏览器。
	notice := ""
	if !*useBrowser && runtime.GOOS == "windows" {
		if windowAvailable() {
			app.Quit = func() { cancel(); closeWindow() }
			app.SetMode("window", "")
			if openWindow(url) {
				cancel() // 窗口已关闭：退出
				return
			}
			app.Quit = cancel
			notice = "WebView2 窗口创建失败，已改用默认浏览器打开。"
		} else {
			notice = "未检测到 Microsoft Edge WebView2 运行时，已改用默认浏览器打开。（安装运行时后即可使用独立窗口）"
		}
	}
	app.SetMode("browser", notice)
	if notice != "" {
		go notify(notice)
	}
	if err := openBrowser(url); err != nil {
		fmt.Println("未能自动打开浏览器，请手动打开上面的地址：", err)
	}
	wait(serveErr)
}

func wait(c <-chan error) {
	if err := <-c; err != nil {
		fatal(err.Error())
	}
}

func runServer(ctx context.Context, bind string, port int, token string, noRoute bool) {
	generated := false
	if token == "" {
		token = os.Getenv("TRACEWING_TOKEN")
	}
	if token == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		token = base64.RawURLEncoding.EncodeToString(b)
		generated = true
	}
	if len(token) < 8 {
		fatal("令牌至少 8 个字符")
	}
	fmt.Printf("tracewing 服务端（agent）v%s 监听 %s:%d（TCP+UDP）\n", version, bind, port)
	if generated {
		// 只有随机生成的令牌才会打印；通过参数/环境变量给定的令牌绝不回显（避免进日志）
		fmt.Printf("未指定令牌，已随机生成（重启会变）：%s\n", token)
	}
	fmt.Println("注意：未加密（明文 HTTP）。令牌不会在网络上发送，但测速数据本身是明文；请只放行你自己的来源 IP 或测完即关。")
	s := &srv.Server{Token: token, NoRoute: noRoute}
	if err := s.Run(ctx, net.JoinHostPort(bind, fmt.Sprint(port))); err != nil {
		fatal(err.Error())
	}
}
