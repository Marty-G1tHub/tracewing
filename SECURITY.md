# 安全说明 / Security

## 威胁模型（agent，即 `tracewing --server`）
agent 部署在你自己的 VPS 上，由你的电脑上的迹翼客户端调用。设计目标：**即使令牌泄露，它也不能被用来攻击第三方或控制这台机器。**

| 能做 | 不能做 |
|---|---|
| 对 **12 个固定的国内目标**（`internal/route/targets.json`）和**调用方自己的来源 IP** 做 ICMP 逐跳探测 | 探测任意 IP / 域名（接口不接受地址参数，多余参数一律 400） |
| 提供测速用的流量接口、回传 CPU / 内存占用 | 执行命令、读写文件、访问内网（来源 IP 不是公网地址会被拒绝） |
| 全局限速：突发 20 次、每分钟 30 次、并发 3、单次最长 45 秒 | 回传任何判定——只回传原始跳数据，判定在客户端完成 |

- **认证**：无状态 HMAC 挑战应答。令牌从不在网络上发送；失败后延迟 300 ms。**没有 TLS**：令牌不泄露，但流量（含路径数据）是明文，对公网开放可以接受（家宽 IP 常会变），能用云防火墙按来源 IP 限制更好，或用完即关。
- **systemd 加固**（`install.sh` 安装）：非特权系统用户、仅 `CAP_NET_RAW`、`NoNewPrivileges`、`ProtectSystem=strict`、`ProtectHome`、`PrivateTmp/Devices`、`RestrictAddressFamilies=AF_INET AF_INET6`、`SystemCallFilter=@system-service`、`MemoryDenyWriteExecute`、`MemoryMax=192M`。令牌文件 `0640 root:tracewing`。
- **安装脚本**：只从本仓库的 GitHub Release 下载，并用同一 Release 的 `SHA256SUMS` 校验，不通过即中止；不改防火墙、不改 sshd、不装依赖。注意：SHA256SUMS 与二进制来自同一处，所以它防的是传输损坏与镜像错误，**不能防仓库本身被攻破**；想要更强保证请自己从源码构建（`go build`）并用 `TRACEWING_BASE_URL` 指向你自己的目录。
- **客户端界面**：仅监听 127.0.0.1 随机端口；一次性密钥 Cookie、Host 校验、写操作要求 POST+JSON；不保存令牌。
- **隐私**：线路判定用内置前缀表在本机完成，不把任何 IP 发给第三方；IP 归属查询（测试IP 页）会访问 RIPEstat / ipinfo.io，可在界面取消。

## 报告漏洞
请在 GitHub 仓库的 **Security → Report a vulnerability**（私密安全公告）提交；若不可用，开一个不含利用细节的 Issue 请求联系方式。请不要在公开 Issue 里贴可利用的细节。

---
**English**: The agent can only probe 12 fixed Chinese-carrier targets plus the caller's own public source IP, never arbitrary addresses, never runs commands, and is rate-limited. Auth is HMAC challenge-response (the token never crosses the wire) but the transport is plain HTTP: leaving the port public is acceptable, restricting by source IP is better when possible. The installer verifies SHA256SUMS from the same release (integrity, not provenance). Report vulnerabilities via GitHub private advisories.
