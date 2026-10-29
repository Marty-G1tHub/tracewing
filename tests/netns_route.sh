#!/bin/sh
# 回程探测端到端测试：用 Linux 网络命名空间搭一条“假骨干网”（路由器地址取 59.43.x / 202.97.x），
# 让真正的原始套接字 ICMP 探测走一遍，验证逐跳与线路判定。不需要真 root：用 unshare 的用户命名空间即可。
#   sh tests/netns_route.sh ./tracewing-linux-amd64
set -eu
BIN="${1:-./tracewing}"
case "$BIN" in /*) ;; *) BIN="$PWD/$BIN" ;; esac
[ -x "$BIN" ] || { echo "找不到二进制 $BIN" >&2; exit 2; }
if [ "${TW_INNER:-0}" != 1 ]; then
  exec env TW_INNER=1 unshare -Urnm sh "$0" "$BIN"
fi
mount -t tmpfs tmpfs /run 2>/dev/null || true
mkdir -p /run/netns
ip link set lo up
ip netns add r1; ip netns add r2; ip netns add r3
# agent ─59.43.0.0/30─ r1 ─202.97.0.0/30─ r2 ─10.9.0.0/30─ r3（r3 的 lo 上挂着 12 个固定目标地址）
ip link add a0 type veth peer name r1a; ip link set r1a netns r1
ip link add r1b netns r1 type veth peer name r2b netns r2
ip addr add 59.43.0.2/30 dev a0; ip link set a0 up; ip route add default via 59.43.0.1
ip -n r1 addr add 59.43.0.1/30 dev r1a; ip -n r1 addr add 202.97.0.1/30 dev r1b
ip -n r1 link set lo up; ip -n r1 link set r1a up; ip -n r1 link set r1b up
ip -n r2 addr add 202.97.0.2/30 dev r2b; ip -n r2 link set lo up; ip -n r2 link set r2b up
ip -n r2 route add default via 202.97.0.1
ip link add r2c netns r2 type veth peer name r3c netns r3
ip -n r2 addr add 10.9.0.1/30 dev r2c; ip -n r2 link set r2c up
ip -n r3 addr add 10.9.0.2/30 dev r3c; ip -n r3 link set lo up; ip -n r3 link set r3c up
ip -n r3 route add default via 10.9.0.1
for ns in r1 r2 r3; do ip netns exec $ns sh -c 'echo 1 > /proc/sys/net/ipv4/ip_forward; echo 0 > /proc/sys/net/ipv4/icmp_ratelimit'; done
for t in 219.141.136.10 202.96.209.5 202.96.128.86 61.139.2.69 202.106.0.20 210.22.70.3 210.21.196.6 119.6.6.6 211.136.17.107 211.136.150.66 211.136.192.6 211.137.96.205; do
  ip -n r3 addr add "$t/32" dev lo
  ip -n r2 route add "$t/32" via 10.9.0.2
  ip -n r1 route add "$t/32" via 202.97.0.2
done

TOKEN=netnstoken-1234567
TRACEWING_TOKEN=$TOKEN "$BIN" --server --port 19527 --bind 127.0.0.1 >/tmp/tw-agent.log 2>&1 &
APID=$!
trap 'kill $APID 2>/dev/null || true' EXIT
sleep 1
OUT=$(TRACEWING_TOKEN=$TOKEN "$BIN" --route-check 127.0.0.1:19527 --json 2>/dev/null) || true
echo "$OUT" > /tmp/tw-route.json
python3 - <<'PY'
import json,sys
d=json.load(open('/tmp/tw-route.json'))
ret=d.get('return') or []
assert d.get('agent_method')=='icmp(raw)', ('agent method', d.get('agent_method'), d.get('notes'))
assert len(ret)==12, ('return lines', len(ret), d.get('notes'))
bad=0
for l in ret:
    tr=l['trace']; ips=[h.get('ip','') for h in tr['hops']]
    ok = tr['reached'] and ips[:2]==['59.43.0.1','202.97.0.2'] and 4809 in l['verdict']['as_path'] and 4134 in l['verdict']['as_path']
    if not ok: bad+=1; print('FAIL', l['id'], ips, l['verdict'])
print('回程 12 条：', 12-bad, '条符合预期；示例判定：', ret[0]['verdict']['label'], ret[0]['verdict']['confidence'], ret[0]['verdict']['evidence'])
assert bad==0
print('PASS')
PY
