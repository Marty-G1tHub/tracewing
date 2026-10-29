# AGENTS.md — 给 AI 代理的部署与使用说明

目标：帮用户在一台 Linux VPS 上部署迹翼 agent，并从用户电脑或本机检查去程 / 回程线路。

## 部署（在 VPS 上，需要 root + systemd）
```sh
curl -fsSL https://github.com/Marty-G1tHub/tracewing/releases/download/v0.1.0/install.sh | sudo sh
```
- 幂等：重复运行会升级二进制并**沿用原令牌**；`--rotate-token` 换新令牌；`--port N`、`--bind IP`、`--no-route` 可选。
- 令牌只在**首次安装**时打印一次，请从命令输出里取出并交给用户；之后只在 `/etc/tracewing/agent.env`（仅 root 与 tracewing 用户可读）。**不要把令牌写进日志、提交记录或公开渠道。**
- 成功标志：命令退出码 0，且输出 “迹翼 agent … 已安装”。验证：`systemctl is-active tracewing`；`curl -s http://127.0.0.1:9527/v1/challenge`（应返回含 `nonce` 与 `version` 的 JSON；该接口无需令牌）。
- 失败时：sha256 校验失败 → 不要绕过，报告给用户；`journalctl -u tracewing -n 50` 看服务日志；没有 systemd 的系统脚本只安装文件并打印手动运行命令。
- 不要替用户改云防火墙 / 安全组；告诉用户放行 TCP+UDP 9527。对公网开放可以接受（家宽 IP 常变；HMAC 挑战应答，令牌不上网），能按来源 IP 限制更好。
- 卸载：`sudo sh install.sh uninstall`（删除服务、二进制、令牌、用户）。

## 使用
- 桌面：Windows 上运行 `tracewing.exe` → 线路检测 → 填 `VPS_IP:9527` 与令牌。
- 无界面（脚本/AI）：
  ```sh
  TRACEWING_TOKEN=<令牌> tracewing --route-check VPS_IP:9527 --json
  ```
  退出码：0 正常；1 出错（连不上/令牌错）；2 有结果但部分探测失败。JSON 字段：`outbound`（去程）、`return[]`（12 条回程，各含 `id,label,trace.hops[],verdict{code,label,confidence,evidence[],as_path[],reached}`）、`client_return`（回到调用方出口 IP，同结构）、`headlines[]`、`notes[]`、`prefix_version`。
- 令牌优先用环境变量，不要放进命令行参数（会出现在进程列表里）。

## 解读结果时请注意
- `confidence`：`sure` 表示证据明确；`mixed` 表示证据矛盾或不典型（如 CN2 末段混有 163）；`unknown` 表示没有可识别的骨干地址或探测不完整——**不要把 unknown 当成“线路差”**。
- CN2 GIA / GT 的区分是经验规则，不是运营商标注。必须把 `evidence` 一并转述给用户。
- 本机有 TUN / Fake-IP 时去程不可信（`notes` 里有 `TUN`）；回程不受影响。
- 回程目标是各地运营商的公开 DNS，不等于用户要访问的某个网站。

## 开发
```sh
gofmt -l .                                  # 输出应为空
go vet ./... && GOOS=windows go vet ./... && go test ./...
sh tests/netns_route.sh <linux 二进制>     # 需要 unshare/ip（Linux），无需真 root
```
约束：agent 的接口**不得**接受任意目标地址（见 `internal/srv/route.go` 顶部注释与 `route_test.go`）；判定规则只放在客户端（`internal/route/classify.go`）；不得复制 GPL 项目的代码（见 NOTICE）；更新前缀表用 `python3 tools/genprefixes.py > internal/route/prefixes.json`。
