#!/usr/bin/env python3
"""从 RIPEstat 重新生成 internal/route/prefixes.json（只含判线路需要的骨干 ASN）。
用法：python3 tools/genprefixes.py > internal/route/prefixes.json
数据来源：RIPEstat announced-prefixes（公开数据），另加少量人工补充（见 MANUAL，注明依据）。
"""
import json, sys, time, urllib.request, datetime

ASNS = {
    4134: "CHINANET-BACKBONE", 4809: "CHINANET-CN2", 4837: "CU-169", 9929: "CU-A-NET", 9808: "CMNET",
    58453: "CMI", 58807: "CMIN2", 10099: "CUG", 23764: "CTG", 4812: "CHINANET-SH", 4847: "CNIX",
    4808: "CU-BJ", 17621: "CU-SH", 17623: "CU-GD", 56040: "CMNET-GD", 24400: "CMNET-SH",
}
# CN2 的 59.43.0.0/16 在 BGP 中不公告（内部骨干地址），公开资料里一致认为属 AS4809；
# 其余为运营商知名骨干/国际出口段，人工补充时注明依据。
MANUAL = [
    ("59.43.0.0/16", 4809, "CN2 骨干内部地址，不在 BGP 公告；GIA/GT 判别依据"),
]

def get(asn):
    url = f"https://stat.ripe.net/data/announced-prefixes/data.json?sourceapp=tracewing-gen&resource=AS{asn}"
    for i in range(3):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers={"User-Agent": "tracewing-gen"}), timeout=60) as r:
                return json.load(r)["data"]["prefixes"]
        except Exception as e:
            time.sleep(2 + i * 3)
    raise SystemExit(f"AS{asn} 取数失败")

rows = {}
for asn in ASNS:
    for p in get(asn):
        pf = p["prefix"]
        if ":" in pf:
            continue
        rows[pf] = asn
    time.sleep(1)
for pf, asn, _ in MANUAL:
    rows.setdefault(pf, asn)

# 去掉被同 ASN 更大前缀完全覆盖的子前缀（最长前缀匹配结果不变），把文件从数十万行压到可读大小
import ipaddress
def aggregate(rows):
    nets = sorted(((ipaddress.ip_network(pf), asn) for pf, asn in rows.items()), key=lambda x: (x[0].prefixlen, int(x[0].network_address)))
    kept = {}
    for n, asn in nets:
        covered = False
        for l in range(n.prefixlen - 1, -1, -1):
            sup = n.supernet(new_prefix=l)
            if kept.get(str(sup)) == asn:
                covered = True
                break
            if str(sup) in kept:   # 被另一个 ASN 的更大前缀盖住：子前缀必须保留
                break
        if not covered:
            kept[str(n)] = asn
    return kept
rows = aggregate(rows)

def key(pf):
    ip, l = pf.split("/")
    return tuple(int(x) for x in ip.split(".")) + (int(l),)

out = {
    "version": datetime.date.today().isoformat(),
    "source": "RIPEstat announced-prefixes + manual",
    "asns": {str(a): n for a, n in ASNS.items()},
    "manual": [{"prefix": p, "asn": a, "why": w} for p, a, w in MANUAL],
    "prefixes": [[pf, rows[pf]] for pf in sorted(rows, key=key)],
}
json.dump(out, sys.stdout, ensure_ascii=False, separators=(",", ":"))
