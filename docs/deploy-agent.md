# 部署回程 agent

## 要求
Linux（amd64 / arm64）、systemd、root、curl 或 wget、sha256sum。内存占用约几十 MB。

## 安装
```sh
curl -fsSL https://github.com/Marty-G1tHub/tracewing/releases/download/v0.1.0/install.sh | sudo sh
# 或先下载检查再运行：
curl -fsSLO https://github.com/Marty-G1tHub/tracewing/releases/download/v0.1.0/install.sh
less install.sh && sudo sh install.sh
```
可选：`--port 9527`、`--bind 127.0.0.1`（配合 SSH 隧道 / 反向代理）、`--no-route`（只当测速服务端）、`--rotate-token`。环境变量 `TRACEWING_VERSION`、`TRACEWING_BASE_URL`（镜像或自建发布目录，需包含 `tracewing-linux-<arch>` 与 `SHA256SUMS`）。

## 脚本做的事
1. 下载 `tracewing-linux-<arch>` 与 `SHA256SUMS`，校验 sha256，再试运行 `--version`；
2. 创建系统用户 `tracewing`（nologin、无家目录）；二进制装到 `/usr/local/bin/tracewing`；
3. 首次生成 128 位随机令牌，写入 `/etc/tracewing/agent.env`（`0640 root:tracewing`），在终端打印一次；
4. 安装 `/etc/systemd/system/tracewing.service`（加固项见 [SECURITY.md](../SECURITY.md)）并启动。
不会修改防火墙、sshd、系统包。

## 放行端口
放行 TCP 与 UDP 的 9527（UDP 用于测速里的 UDP 项；只做线路检测可以只放 TCP）。家宽 IP 常会变，对公网开放也可以接受：认证是 HMAC 挑战应答，令牌从不在网络上发送，agent 只能探测固定目标且有限速。但传输是明文 HTTP，**能按来源 IP 限制就更好**。

## 没有 root 时
可以直接运行二进制：`TRACEWING_TOKEN=自定令牌 ./tracewing --server --port 9527`。没有 `CAP_NET_RAW` 时 agent 只能做非逐跳 ICMP，回程接口会返回 501，客户端会明确提示（不会假装成功）。可用 `sudo setcap cap_net_raw+ep ./tracewing` 授权。

## 验证
```sh
systemctl status tracewing
journalctl -u tracewing -n 50
TRACEWING_TOKEN=令牌 tracewing --route-check 127.0.0.1:9527   # 在 VPS 本机试：回程 12 条应有结果，去程会提示“不是公网地址”（正常）
```
`--route-check` 退出码：`0` 全部成功；`1` 出错；`2` 有结果但部分探测失败或不完整——在 VPS 本机自测时因为去程必然失败，返回 2 是正常的。

## 升级 / 换令牌 / 卸载
- 升级：再运行一次安装命令（沿用令牌）。换令牌：`sudo sh install.sh --rotate-token`。
- 卸载：`sudo sh install.sh uninstall`。

## 常见问题
- **回程全是“无法确定”**：VPS 厂商可能屏蔽了出站 ICMP 超时报文，或路径上的路由器不回包；换个时间再测，或在报告里查看逐跳。
- **agent 提示不能逐跳**：服务没有 `CAP_NET_RAW`——用 install.sh 安装，或见上面“没有 root 时”。
- **429**：agent 限速（突发 20、每分钟 30）；客户端会自动退避重试。
