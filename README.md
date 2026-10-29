# 迹翼 Tracewing

Tracewing 是一个用于检测 VPS 与中国大陆之间实际线路的桌面网络诊断工具（附带 Linux 回程 agent），输入 VPS 地址与令牌、待测 IP 或测速参数，输出带逐跳证据和置信度的线路判定、吞吐 / 时延数据，并落盘为 `summary.json` 与离线 `report.html`。

## 1. 定位与核心机制

VPS 商家标注的 CN2 GIA / GT、AS9929、AS4837、CMIN2 等线路无法自证，Tracewing 用逐跳证据来判断：客户端探测去程，VPS 上的 agent（同一个二进制的 `--server` 模式）探测到 12 个固定国内目标和调用方出口 IP 的回程，只回传原始跳数据。客户端用内置离线前缀表 `internal/route/prefixes.json`（来自 RIPEstat，另补 `59.43.0.0/16`）做最长前缀匹配，按骨干 ASN 出现的先后顺序给出判定。测速部分用多条并发 TCP 连接跑 HTTP 下载 / 上传，只统计预热后的稳态窗口并多轮取中位；配合 agent 时还测 9527 端口的 TCP / UDP 吞吐、UDP 丢包乱序和服务器 CPU steal / 内存。

## 2. 数据流向

```text
Windows 客户端 tracewing.exe
  WebView2 窗口（无运行时则用默认浏览器）
    └─ 本机网页界面 http://127.0.0.1:<随机端口>   一次性密钥 Cookie + Host 校验
         ├─ 宽带测速：公共端点 / endpoints.json ──HTTP 多连接──▶ 吞吐中位、延迟
         ├─ 测试IP：  目标 IPv4 ──ICMP / TCP 往返 + 可选逐跳 + 可选归属查询（RIPEstat / ipinfo.io）──▶ 时延、丢包、AS 路径
         ├─ VPS测速： agent | 自有 URL | 公共端点 ──正/反向 × 1/4 流 + UDP + echo──▶ 吞吐、时延、启发式诊断
         └─ 线路检测：
              去程：本机 ──ICMP 逐跳──▶ VPS IP
              回程：GET /v1/challenge ─▶ nonce
                    Authorization: TW <nonce> HMAC-SHA256(token, "p|"+nonce)
                    GET /v1/route/targets、/v1/route/trace?target=<id>|client
                    agent ──ICMP 逐跳（CAP_NET_RAW）──▶ 12 个固定目标 + 调用方出口 IP
                    agent ──原始跳数据──▶ 客户端
              客户端：跳 IP ─▶ prefixes.json 最长前缀匹配 ─▶ ASN 序列 ─▶ 判定 + 证据 + 置信度（sure / mixed / unknown）
  结果 ──▶ 数据目录/runs/<id>/summary.json + report.html，索引追加到 history.jsonl

Linux VPS agent：tracewing --server --port 9527（systemd，非特权用户，仅 CAP_NET_RAW）
  TCP 9527：/v1/challenge /v1/info /v1/down /v1/up /v1/echo /v1/res /v1/udp/* /v1/route/*
  UDP 9527：UDP 吞吐测试报文

无界面：tracewing --route-check HOST:9527 [--json] ──▶ stdout 文本或 JSON，退出码 0 / 1 / 2
```

## 3. 仓库拓扑与职责边界

```text
main.go                 解析命令行参数，分发到界面 / --server / --route-check；不含测量逻辑
cli.go                  执行 --route-check 并打印文本或 JSON、决定退出码；不写数据目录
window_windows.go       创建 WebView2 窗口并跑消息循环；不处理业务请求
console_windows.go      附着父控制台、弹提示框；只在 Windows 编译
internal/
  ui/                   提供本机网页界面的 HTTP 接口、调度测速任务、写结果；只监听 127.0.0.1
  web/                  内嵌前端静态文件（index.html / app.js / render.js / app.css / report.html）；无构建步骤
  engine/               发流量并记录字节、时延、逐跳（宽带 / 测试IP / VPS / 线路检测）；不做线路判定
  judge/                把测量结果算成统计值与诊断（纯函数）；不访问网络和磁盘
  route/                做逐跳探测、加载前缀表与 12 个固定目标、判定线路类型；不发起 HTTP 请求
  srv/                  实现 agent 服务端、HMAC 鉴权、UDP 测试与对应客户端；不做判定，不接受任意探测地址
  ping/                 发 ICMP 往返与逐跳（Windows IcmpSendEcho，Linux 原始 / 非特权套接字）及 TCP 握手兜底；不解析 ASN
  netx/                 决定流量出口（系统代理 / 直连 / 自定义代理）、构造 HTTP 客户端、分类 IP；不测速
  probe/                只读探测系统代理、TUN、Fake-IP、出口 IP；不修改任何系统设置
  store/                选定数据目录，读写 runs/、history.jsonl、config.json；不调用网络
  report/               把结果内联成单文件离线 HTML；不读写磁盘
  model/                定义 summary.json 的结构体；不含逻辑
install.sh              在 Linux VPS 安装、覆盖安装或卸载 agent（校验 sha256、建用户、装 systemd 单元）；不改防火墙和 sshd
scripts/release.sh      检查版本号、跑 gofmt / vet / test、交叉编译并写 releases/v<版本>/ 与 SHA256SUMS；不上传
tests/netns_route.sh    在 Linux 网络命名空间里搭假骨干网，端到端验证回程逐跳与判定；不需要真 root
tools/genprefixes.py    从 RIPEstat 重新生成 internal/route/prefixes.json；运行时不使用
winres/                 Windows 图标与版本资源（go-winres 生成 rsrc_windows_*.syso）
docs/                   deploy-agent.md（agent 部署细节）、how-it-works.md（协议与判定规则）
AGENTS.md  SECURITY.md  NOTICE  LICENSE
```

## 4. 快速上手

```sh
# 依赖：Go 1.27.3（见 go.mod），无 CGO
go build -o tracewing .                       # 当前平台
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H=windowsgui -s -w" -o tracewing.exe .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o tracewing-linux-arm64 .
```

```text
:: Windows：双击或在命令行运行，无需管理员
tracewing.exe
tracewing.exe --browser          :: 用默认浏览器
tracewing.exe --data D:\tw       :: 指定数据目录
```

```sh
# VPS（Linux amd64 / arm64，systemd，root）：安装 agent，令牌只打印一次
curl -fsSL https://github.com/Marty-G1tHub/tracewing/releases/download/v0.1.0/install.sh | sudo sh
# 云防火墙放行 TCP+UDP 9527；卸载：sudo sh install.sh uninstall
```

```sh
# 无界面线路检测；退出码 0 成功 / 1 出错 / 2 部分探测失败
TRACEWING_TOKEN=<令牌> ./tracewing --route-check 203.0.113.10:9527 --json
```

## 5. 维护契约与边界

1. 状态与配置：数据目录按 `--data` > 程序旁 `迹翼数据/`（可写时）> 用户配置目录下 `Tracewing/`（Windows 为 `%AppData%\Tracewing`）选定。每次结果写 `runs/<id>/summary.json` 与 `report.html`，索引在 `history.jsonl`，界面设置在 `config.json`；测速端点从同一目录的 `endpoints.json` 读取（Endpoint 数组，整体替换内置表），不存在时用 `internal/engine/endpoints.go` 的内置表。客户端不保存令牌（`config.json` 不含令牌字段），agent 端令牌只在 `/etc/tracewing/agent.env`。
2. 不能破坏的接口：
   - agent 协议：`/v1/*` 路径、`Authorization: TW <nonce> <proof>`、nonce 格式与 `HMAC-SHA256` 计算（`internal/srv/auth.go`），以及 UDP 报文头（`internal/srv/udp.go`）。已部署的 agent 依赖它们。
   - agent 只能探测 `internal/route/targets.json` 的固定目标和调用方自身来源 IP，接口不接受地址参数（`internal/srv/route.go`、`route_test.go`）；线路判定只在客户端（`internal/route/classify.go`）。
   - CLI 参数名（`--server --port --bind --no-route --token --route-check --json --data --browser --no-browser --idle --version`）、环境变量 `TRACEWING_TOKEN`、`--route-check` 退出码 0 / 1 / 2 与 JSON 字段（见 AGENTS.md）。
   - `install.sh` 的路径（`/usr/local/bin/tracewing`、`/etc/tracewing/agent.env`、`/etc/systemd/system/tracewing.service`、用户 `tracewing`）与变量 `TRACEWING_VERSION / TRACEWING_BASE_URL / TRACEWING_PREFIX / TRACEWING_PORT / TRACEWING_BIND / TRACEWING_NO_ROUTE`。
3. 不硬编码任何个人 IP、主机名、令牌或密钥；示例地址只用文档段（如 `203.0.113.0/24`），令牌只来自 `--token`、`TRACEWING_TOKEN` 或随机生成。不复制 GPL 项目代码（见 NOTICE）。
4. 提交前必须通过：
   ```sh
   gofmt -l .                       # 输出为空
   go vet ./... && GOOS=windows go vet ./... && go test ./...
   sh tests/netns_route.sh ./tracewing   # Linux，可选，需要 unshare / ip
   ```
5. 发版：同步修改 `internal/srv/auth.go` 的 `Version`、`winres/winres.json`（4 段版本号，改后运行 `go-winres make --in winres/winres.json --arch amd64,arm64`）和 `install.sh` 的默认 `TRACEWING_VERSION`，打 tag `v<版本>` 后运行 `scripts/release.sh <版本>`；产物在 `releases/v<版本>/`（与 `dist/` 一起被 .gitignore 排除）。更新前缀表：`python3 tools/genprefixes.py > internal/route/prefixes.json`。

许可证：MIT（LICENSE）。第三方说明见 NOTICE，安全模型见 SECURITY.md。
