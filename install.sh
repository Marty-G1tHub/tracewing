#!/bin/sh
# 迹翼 Tracewing agent 安装脚本（POSIX sh；需要 root；支持 systemd 的 Linux amd64 / arm64）
#
#   curl -fsSL https://github.com/Marty-G1tHub/tracewing/releases/download/v0.1.0/install.sh | sudo sh
#   sudo sh install.sh uninstall
#
# 做了什么（全部可在下面读到，没有别的）：
#   1. 从本仓库的 GitHub Release 下载二进制 + SHA256SUMS，校验 sha256，不通过就退出；
#   2. 装到 /usr/local/bin/tracewing；创建无登录、无家目录的系统用户 tracewing；
#   3. 生成随机令牌写入 /etc/tracewing/agent.env（0640 root:tracewing），只在首次安装时打印一次；
#   4. 安装一个加固的 systemd 服务（只授予 CAP_NET_RAW，只读文件系统，禁止提权）并启动；
#   5. 不修改防火墙、不改 sshd、不装任何依赖、不外发任何数据。
#
# 可选参数 / 环境变量：
#   --port N         监听端口（默认 9527，TCP+UDP）      TRACEWING_PORT
#   --bind ADDR      监听地址（默认全部网卡）             TRACEWING_BIND
#   --no-route       关闭回程探测，只当测速服务端          TRACEWING_NO_ROUTE=1
#   --rotate-token   重新生成令牌（旧令牌立即失效）
#   TRACEWING_VERSION   要安装的版本（默认 v0.1.0）
#   TRACEWING_BASE_URL  下载目录（默认本仓库该版本的 Release；仅用于镜像/测试）
#   TRACEWING_PREFIX    安装到该目录下（测试用；设置后不创建用户、不调用 systemctl）
set -eu

VERSION="${TRACEWING_VERSION:-v0.1.0}"
REPO="Marty-G1tHub/tracewing"
BASE_URL="${TRACEWING_BASE_URL:-https://github.com/$REPO/releases/download/$VERSION}"
PREFIX="${TRACEWING_PREFIX:-}"
PORT="${TRACEWING_PORT:-9527}"
BIND="${TRACEWING_BIND:-}"
NOROUTE="${TRACEWING_NO_ROUTE:-0}"
ROTATE=0
ACTION=install
SVC=tracewing
USER_NAME=tracewing

BIN="$PREFIX/usr/local/bin/tracewing"
ENVDIR="$PREFIX/etc/tracewing"
ENVFILE="$ENVDIR/agent.env"
UNIT="$PREFIX/etc/systemd/system/$SVC.service"

say() { printf '%s\n' "$*"; }
die() { printf '错误：%s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    install|uninstall) ACTION="$1" ;;
    --port) [ $# -ge 2 ] || die "--port 需要一个值"; PORT="$2"; shift ;;
    --bind) [ $# -ge 2 ] || die "--bind 需要一个值"; BIND="$2"; shift ;;
    --no-route) NOROUTE=1 ;;
    --rotate-token) ROTATE=1 ;;
    -h|--help) sed -n '2,24p' "$0" 2>/dev/null || true; exit 0 ;;
    *) die "不认识的参数：$1（用 --help 查看）" ;;
  esac
  shift
done

case "$PORT" in ''|*[!0-9]*) die "端口必须是数字" ;; esac
if [ "$PORT" -lt 1024 ] || [ "$PORT" -gt 65535 ]; then die "端口需在 1024–65535（服务以非特权用户运行，不绑定低端口）"; fi
case "$BIND" in *[!0-9a-fA-F:.]*) die "--bind 只接受 IP 地址" ;; esac

[ "$(id -u)" = 0 ] || die "需要 root（请在命令前加 sudo）"
[ "$(uname -s)" = Linux ] || die "只支持 Linux"

HAVE_SYSTEMD=0
if [ -z "$PREFIX" ] && [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then HAVE_SYSTEMD=1; fi

sysctl_() { if [ "$HAVE_SYSTEMD" = 1 ]; then systemctl "$@" || true; fi; }

if [ "$ACTION" = uninstall ]; then
  sysctl_ disable --now "$SVC" >/dev/null 2>&1
  rm -f "$UNIT" "$BIN"
  rm -rf "$ENVDIR"
  sysctl_ daemon-reload
  if [ -z "$PREFIX" ] && id "$USER_NAME" >/dev/null 2>&1; then
    userdel "$USER_NAME" >/dev/null 2>&1 || deluser "$USER_NAME" >/dev/null 2>&1 || say "提示：用户 $USER_NAME 未能自动删除，可手动执行 userdel $USER_NAME"
  fi
  say "已卸载迹翼 agent（服务、二进制、配置与令牌均已删除）。"
  exit 0
fi

# ---- 下载与校验 ----
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "不支持的 CPU 架构：$(uname -m)（只提供 amd64 / arm64）" ;;
esac
ASSET="tracewing-linux-$ARCH"

if command -v curl >/dev/null 2>&1; then fetch() { curl -fsSL --proto '=https,http' --max-time 120 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then fetch() { wget -q -T 120 -O "$2" "$1"; }
else die "需要 curl 或 wget"; fi
if command -v sha256sum >/dev/null 2>&1; then sha() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
else die "需要 sha256sum 或 shasum"; fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM
say "下载 $ASSET ($VERSION) …"
fetch "$BASE_URL/$ASSET" "$TMP/$ASSET" || die "下载失败：$BASE_URL/$ASSET"
fetch "$BASE_URL/SHA256SUMS" "$TMP/SHA256SUMS" || die "下载失败：$BASE_URL/SHA256SUMS"
WANT="$(awk -v f="$ASSET" '$2==f || $2=="*"f {print $1; exit}' "$TMP/SHA256SUMS")"
[ -n "$WANT" ] || die "SHA256SUMS 里没有 $ASSET"
GOT="$(sha "$TMP/$ASSET")"
[ "$WANT" = "$GOT" ] || die "sha256 校验失败（期望 $WANT，实际 $GOT），已中止，未做任何修改"
say "sha256 校验通过：$GOT"
chmod 0755 "$TMP/$ASSET"
"$TMP/$ASSET" --version >/dev/null 2>&1 || die "二进制无法在本机运行（架构或 libc 不符？）"

# ---- 用户、文件 ----
if [ -z "$PREFIX" ] && ! id "$USER_NAME" >/dev/null 2>&1; then
  NOLOGIN=/usr/sbin/nologin; [ -x "$NOLOGIN" ] || NOLOGIN=/sbin/nologin; [ -x "$NOLOGIN" ] || NOLOGIN=/bin/false
  useradd --system --no-create-home --home-dir /nonexistent --shell "$NOLOGIN" "$USER_NAME" 2>/dev/null \
    || adduser -S -H -D -s "$NOLOGIN" "$USER_NAME" 2>/dev/null \
    || die "创建系统用户 $USER_NAME 失败"
fi

mkdir -p "$(dirname "$BIN")" "$ENVDIR" "$(dirname "$UNIT")"
# 先停服务再替换二进制，避免 “text file busy”
sysctl_ stop "$SVC" >/dev/null 2>&1
install -m 0755 "$TMP/$ASSET" "$BIN"

NEWTOKEN=""
if [ ! -s "$ENVFILE" ] || [ "$ROTATE" = 1 ]; then
  NEWTOKEN="$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-32)"
  [ "${#NEWTOKEN}" -ge 32 ] || die "无法生成随机令牌"
  ( umask 077; printf 'TRACEWING_TOKEN=%s\n' "$NEWTOKEN" > "$ENVFILE" )
fi
chmod 0750 "$ENVDIR"; chmod 0640 "$ENVFILE"
if [ -z "$PREFIX" ]; then chown -R "root:$USER_NAME" "$ENVDIR"; fi

ARGS="--server --port $PORT"
if [ -n "$BIND" ]; then ARGS="$ARGS --bind $BIND"; fi
if [ "$NOROUTE" = 1 ]; then ARGS="$ARGS --no-route"; fi

cat > "$UNIT" <<EOF
[Unit]
Description=Tracewing agent (route and speed test server)
Documentation=https://github.com/$REPO
After=network-online.target
Wants=network-online.target

[Service]
User=$USER_NAME
Group=$USER_NAME
EnvironmentFile=$ENVFILE
ExecStart=/usr/local/bin/tracewing $ARGS
Restart=on-failure
RestartSec=3
# 权限：只给原始 ICMP（逐跳探测）所需的 CAP_NET_RAW，其余全部收回
CapabilityBoundingSet=CAP_NET_RAW
AmbientCapabilities=CAP_NET_RAW
NoNewPrivileges=yes
# 文件系统与内核：只读、私有、看不到家目录与设备
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
# 其他
RestrictAddressFamilies=AF_INET AF_INET6
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources
UMask=0077
MemoryMax=192M
TasksMax=256

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 "$UNIT"

if [ "$HAVE_SYSTEMD" = 1 ]; then
  systemctl daemon-reload
  systemctl enable --now "$SVC" >/dev/null 2>&1 || die "服务启动失败，查看：journalctl -u $SVC -n 50"
  sleep 1
  systemctl is-active --quiet "$SVC" || die "服务没有保持运行，查看：journalctl -u $SVC -n 50"
else
  say "提示：没有检测到 systemd（或处于测试前缀模式），已安装文件但未启动服务。"
  say "手动运行（需要 CAP_NET_RAW 才能做回程探测）："
  say "  sudo setcap cap_net_raw+ep $BIN && TRACEWING_TOKEN=\$(sed -n 's/^TRACEWING_TOKEN=//p' $ENVFILE) $BIN $ARGS"
fi

say ""
say "迹翼 agent $VERSION 已安装：监听 ${BIND:-0.0.0.0}:$PORT（TCP+UDP，明文 HTTP）"
if [ -n "$NEWTOKEN" ]; then
  say "令牌（只显示这一次，请保存）：$NEWTOKEN"
else
  say "令牌：沿用已有的（文件 $ENVFILE，仅 root 与 $USER_NAME 可读；要换新令牌请加 --rotate-token）"
fi
say "请在云厂商防火墙 / 安全组里放行 $PORT 的 TCP 与 UDP。家宽 IP 常会变，对公网开放也可以接受（HMAC 挑战应答认证，令牌从不在网络上发送）；能按来源 IP 限制更好（流量是明文）。"
say "在你电脑上的「迹翼」里：服务器地址填 <本机公网IP>:$PORT，令牌填上面这一串。"
say "卸载：sudo sh install.sh uninstall"
