// 结果渲染：同一份代码既用于在线页面，也用于离线 report.html。纯函数，输入 summary.json，输出 HTML。
(function (g) {
  const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const n1 = v => v == null ? '—' : (Math.round(v * 10) / 10).toString();
  const n2 = v => v == null ? '—' : v.toFixed(2);
  const ms = v => v == null ? '—' : (v < 10 ? v.toFixed(2) : v.toFixed(1)) + ' ms';
  const msn = v => v == null ? '—' : (v < 10 ? v.toFixed(2) : v.toFixed(1));
  // 主数字：≥100 取整并加千分位，否则 1 位小数
  const spd = v => v == null ? '—' : v >= 100 ? Math.round(v).toLocaleString('en-US') : (Math.round(v * 10) / 10).toFixed(1);
  const list = a => (a || []);
  const fmtT = s => { try { return new Date(s).toLocaleString('zh-CN', { hour12: false }); } catch { return s || ''; } };

  const IC = {
    down: '<svg viewBox="0 0 24 24"><path d="M12 5v13M6.5 12.5L12 18l5.5-5.5"/></svg>',
    up: '<svg viewBox="0 0 24 24"><path d="M12 19V6M6.5 11.5L12 6l5.5 5.5"/></svg>',
    swap: '<svg viewBox="0 0 24 24"><path d="M7 7h11M14 3.5L18 7l-4 3.5M17 17H6M10 13.5L6 17l4 3.5"/></svg>',
    clock: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/></svg>',
  };
  const LVT = { ok: '健康', warn: '一般', bad: '异常', none: '已完成' };
  const chip = (l, t) => `<span class="vchip ${l}"><i></i>${esc(t || LVT[l])}</span>`;
  const worst = ls => ls.includes('bad') ? 'bad' : ls.includes('warn') ? 'warn' : ls.includes('ok') ? 'ok' : 'none';

  const PERIOD = { peak: '晚高峰（18:00–00:30）', off_peak: '非高峰（00:30–08:00）', normal: '平时' };
  const DIAG = {
    HEALTHY: ['健康', 'ok'], INSUFFICIENT_EVIDENCE: ['证据不足，不下结论', 'warn'], PARTIAL: ['结果不完整', 'warn'], FAILED: ['测试失败', 'bad'],
    LIKELY_HOST_OVERSUBSCRIPTION: ['疑似宿主机超卖（CPU steal 高）', 'bad'], LIKELY_MEMORY_PRESSURE: ['疑似内存吃紧', 'bad'],
    LIKELY_SERVER_CPU_BOTTLENECK: ['疑似服务器 CPU 是瓶颈', 'bad'], EXPECTED_LOCAL_UPLINK_LIMIT: ['已到本地上行上限，属预期', 'ok'],
    LIKELY_REVERSE_SINGLE_FLOW_LIMITATION: ['疑似反向（下行）单流受限，多流正常', 'warn'], LIKELY_LINE_CONGESTION: ['疑似线路拥塞 / 绕路', 'bad'],
  };
  const UTIL = { near_cap: '接近上限', normal_with_overhead: '正常（含协议开销）', observe: '需观察', possibly_abnormal: '可能异常' };
  const GAIN = { similar: '单流与多流接近', some_single_flow_limit: '单流有一定限制', strong_single_flow_limit: '单流限制明显' };
  const KEYN = { fwd1: ['上行 · 单流', '本机 → 服务器', 'up'], fwd4: ['上行 · 4 流', '本机 → 服务器', 'up'], rev1: ['下行 · 单流', '服务器 → 本机', 'down'], rev4: ['下行 · 4 流', '服务器 → 本机', 'down'] };

  // 迷你面积图：多轮序列首尾相连，横轴按点序号，不假装连续时间
  function spark(rounds) {
    const pts = list(rounds).flatMap(r => r.ok ? list(r.series) : []);
    if (pts.length < 3) return '';
    const W = 200, H = 38, mx = Math.max(...pts.map(p => p[1])) || 1;
    const xy = pts.map((p, i) => [i / (pts.length - 1) * W, H - 3 - p[1] / mx * (H - 8)]);
    const d = xy.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1)).join('');
    return `<svg class="spark" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" aria-hidden="true"><path class="ar" d="${d}L${W} ${H}L0 ${H}Z"/><path class="ln" d="${d}"/></svg>`;
  }

  function notes(ns) { return list(ns).map(n => `<div class="note ${esc(n.level)}">${esc(n.text)}</div>`).join(''); }
  // 折叠内容包一层 .fold-b：宽表格在窄卡片里横向滚动，不溢出到相邻卡片上
  const fold = (t, body, open) => `<details class="fold"${open ? ' open' : ''}><summary>${t}</summary><div class="fold-b">${body}</div></details>`;

  function exitLine(e) {
    if (!e) return '';
    if (e.err) return `${esc(e.label)}：<span class="fail">取不到</span>`;
    return `${esc(e.label)}：<b>${esc(e.ip)}</b> ${esc(e.country || '')} <span class="faint">（${esc(e.via)}）</span>`;
  }

  // 环境一句话（侧栏胶囊用）
  function envBrief(e) {
    if (!e) return { lv: '', a: '未知', b: '' };
    const sp = e.sys_proxy || {}, act = !!(sp.http || sp.https || sp.socks);
    if (e.tun) return { lv: 'warn', a: '检测到 TUN', b: e.path_label || '' };
    if (e.fake_ip) return { lv: 'warn', a: 'Fake-IP', b: e.path_label || '' };
    if (act) return { lv: 'warn', a: '系统代理', b: e.path_label || '' };
    return { lv: 'ok', a: '直连', b: e.path_label || '' };
  }

  function env(e) {
    if (!e) return '';
    const sp = e.sys_proxy || {}, act = !!(sp.http || sp.https || sp.socks);
    const chips = [
      `<span class="chip">路径：${esc(e.path_label)}</span>`,
      `<span class="chip ${act ? 'warn' : ''}">系统代理：${esc(act ? [sp.http, sp.https, sp.socks].filter(Boolean)[0] : sp.pac ? 'PAC（本程序不支持）' : '无')}</span>`,
      `<span class="chip ${e.tun ? 'warn' : 'ok'}">TUN：${e.tun ? '疑似存在' : '未发现'}</span>`,
      `<span class="chip ${e.fake_ip ? 'warn' : 'ok'}">Fake-IP：${e.fake_ip ? '是' : '否'}</span>`,
    ].join('');
    const ex = [exitLine(e.exit_cn), exitLine(e.exit_global)].filter(Boolean).map(x => `<div class="exit">${x}</div>`).join('');
    return `<div class="chips">${chips}</div>${ex}${notes(e.notes)}`;
  }
  // 结果页里的环境：有警告 / 异常的提示始终可见，其余折叠
  function envFold(e) {
    if (!e) return '';
    const b = envBrief(e), loud = list(e.notes).filter(n => n.level === 'warn' || n.level === 'bad');
    const rest = Object.assign({}, e, { notes: list(e.notes).filter(n => !loud.includes(n)) });
    return `${notes(loud)}<div class="card fold-card">${fold(`测试环境 · ${esc(b.a)}${b.b ? ' · ' + esc(b.b) : ''}`, env(rest))}</div>`;
  }

  // 一张结论卡：状态胶囊 + 一句话结论 + 补充
  function vcard(lv, head, sub, meta, body) {
    return `<section class="card vcard ${lv}"><div class="vc-top">${chip(lv)}</div>
      <h2 class="vc-head">${head}</h2>${sub ? `<p class="vc-sub">${sub}</p>` : ''}${body ? `<div class="vc-body">${body}</div>` : ''}${meta ? `<div class="vc-meta">${meta}</div>` : ''}</section>`;
  }

  // ---------- 宽带 ----------
  function phase(dir, p) {
    if (!p) return '';
    const label = dir === 'down' ? '下行' : '上行', ic = dir === 'down' ? IC.down : IC.up;
    if (!p.median_mbps) {
      return `<div class="tile"><div class="tl">${ic}${label}</div><div class="bignum bad">失败</div><div class="sub">${esc(p.error || '没有完整的有效轮次')}</div>${notes((p.warnings || []).map(t => ({ level: 'warn', text: t })))}</div>`;
    }
    const u = p.plan_util_pct, lv = u == null ? '' : u >= 80 ? 'ok' : u >= 50 ? 'warn' : 'bad';
    const rows = list(p.rounds).map(r => `<tr><td class="l">第 ${r.n} 轮</td><td>${r.ok ? n1(r.mbps) : '<span class="fail">失败</span>'}</td><td class="l">${r.ok ? `${r.ok_streams}/${r.streams} 流 · ${r.window_sec.toFixed(1)}s` : esc(r.err || '')}</td></tr>`).join('');
    return `<div class="tile"><div class="tl">${ic}${label}<span class="meta">中位数</span></div>
      <div class="bignum">${spd(p.median_mbps)}<small>Mbps</small></div>
      <div class="sub">${n2(p.median_mb_per_s)} MB/s · 区间 ${n1(p.min_mbps)} – ${n1(p.max_mbps)}</div>
      ${u != null ? `<div class="util"><div class="util-h"><span>套餐利用率</span><b class="${lv}">${n1(u)}%</b></div><div class="bar"><i class="${lv}" style="width:${Math.min(100, u)}%"></i></div></div>` : ''}
      ${spark(p.rounds)}
      ${fold('每轮明细 / 来源', `<table><tr><th class="l">轮次</th><th>Mbps</th><th class="l">说明</th></tr>${rows}</table>
      <div class="meta" style="margin-top:8px">源：${esc(p.endpoint_id || '')} · ${esc(p.via || '')}${p.dns ? ` · DNS ${esc(p.dns.class)} ${esc(list(p.dns.ips).slice(0, 2).join(','))}` : ''}</div>
      ${list(p.tried).length ? `<div class="meta">探测：${esc(p.tried.join('；'))}</div>` : ''}`)}
      ${notes((p.warnings || []).map(t => ({ level: 'warn', text: t })))}</div>`;
  }

  function broadband(d) {
    const ph = list(d.groups).flatMap(g => [g.down, g.up]).filter(Boolean);
    const failed = ph.some(p => !p.median_mbps), utils = ph.filter(p => p.plan_util_pct != null).map(p => p.plan_util_pct);
    const mu = utils.length ? Math.min(...utils) : null;
    let lv, head;
    if (failed) { lv = 'bad'; head = '有测速阶段失败'; }
    else if (mu != null) { lv = mu >= 80 ? 'ok' : mu >= 50 ? 'warn' : 'bad'; head = lv === 'ok' ? `宽带状态良好，最低跑到套餐的 ${n1(mu)}%` : lv === 'warn' ? `略低于套餐，最低为套餐的 ${n1(mu)}%` : `明显低于套餐，最低仅为套餐的 ${n1(mu)}%`; }
    else { lv = 'none'; head = '测速完成'; }
    const sub = mu == null && !failed ? '填入套餐速率（高级）后，可判断是否跑满。' : '';
    const meta = `${esc(fmtT(d.meta.captured_at))} · ${d.params.profile === 'quick' ? '快速' : '标准'}档 ${d.params.rounds} 轮 × ${d.params.duration_s}s（预热 ${d.params.warmup_s}s 不计）· ${d.params.streams} 并发流 · 测速源：${d.params.endpoints_from === 'file' ? '自定义 endpoints.json' : '内置'}`;
    const gs = list(d.groups).map(g => `<div class="sec-h"><h3>${esc(g.label)}</h3>${g.exit ? `<span class="exit">${exitLine(g.exit)}</span>` : ''}</div>
      <div class="tiles">${phase('down', g.down)}${phase('up', g.up)}</div>`).join('');
    return `${vcard(lv, esc(head), sub || esc(d.verdict), meta, sub ? `<p class="vc-sub" style="margin:0">${esc(d.verdict)}</p>` : '')}${gs}${envFold(d.env)}
      <p class="hint">说明：下行 / 上行数字是“本机到公共测速源”的路径，不代表整条线路到任意网站的体验；上行在真实公网上可能因系统发送缓冲略微偏高。</p>`;
  }

  // ---------- 测试 IP ----------
  function ipLevel(t) {
    if (t.err || t.no_icmp_reply || t.p50_ms == null) return 'bad';
    if (t.loss_pct >= 10) return 'bad';
    if (t.loss_pct >= 2 || t.stability === '需要关注' || t.stability === '抖动偏大') return 'warn';
    return 'ok';
  }
  function ipCard(t) {
    const lv = ipLevel(t), where = [t.country, t.region, t.city].filter(Boolean).join(' ');
    const hops = list(t.hops).length ? fold(`去程 ${esc(t.path_summary || '')}`, `<table><tr><th>跳</th><th class="l">IP</th><th>RTT</th><th class="l">ASN / 持有者</th></tr>${t.hops.map(h => `<tr><td>${h.ttl}</td><td class="l mono">${esc(h.ip || '*')}</td><td>${h.rtt_ms < 0 ? '—' : ms(h.rtt_ms)}</td><td class="l">${esc([h.asn, h.holder].filter(Boolean).join(' '))}</td></tr>`).join('')}</table>`) : '';
    const main = t.p50_ms == null ? `<div class="pc-main bad">${t.no_icmp_reply ? '无回应' : '不通'}</div>` : `<div class="pc-main">${msn(t.p50_ms)}<small>ms · P50</small></div>`;
    return `<div class="pcard"><div class="pc-h"><i class="dot ${lv}"></i><span class="mono">${esc(t.ip)}</span></div>
      <div class="pc-where">${esc([where, [t.asn, t.holder].filter(Boolean).join(' ')].filter(Boolean).join(' · '))}</div>${main}
      <div class="pc-stats"><div><span>丢包</span><b class="${t.loss_pct >= 5 ? 'fail' : ''}">${t.no_icmp_reply ? '100%' : n1(t.loss_pct) + '%'}</b></div><div><span>P95</span><b>${ms(t.p95_ms).replace(' ms', '')}</b></div><div><span>抖动</span><b>${ms(t.jitter_ms).replace(' ms', '')}</b></div></div>
      <div class="pc-foot">${t.stability ? `<span class="chip ${lv === 'ok' ? 'ok' : lv === 'warn' ? 'warn' : ''}">${esc(t.stability)}</span>` : ''}${t.p50_ms != null ? `<span>${n1(t.min_ms)}–${n1(t.max_ms)} ms</span>` : ''}${t.err ? `<span class="fail">${esc(t.err)}</span>` : ''}</div>${hops}</div>`;
  }
  function testip(d) {
    const ts = list(d.targets), ls = ts.map(ipLevel), bads = ls.filter(x => x === 'bad').length;
    const lv = !ts.length ? 'none' : bads === ts.length ? 'bad' : worst(ls) === 'ok' ? 'ok' : 'warn';
    const head = lv === 'ok' ? `${ts.length} 个地址全部连通、稳定` : lv === 'bad' ? '这些地址都没有正常回应' : `${ts.length} 个地址中 ${ts.length - ls.filter(x => x === 'ok').length} 个有丢包、抖动或不通`;
    const meta = `${esc(fmtT(d.meta.captured_at))} · ${d.params.method === 'icmp' ? 'ICMP' : 'TCP ' + d.params.tcp_port + ' 握手'} × ${d.params.count} 次 · 去程 ${d.params.trace ? (d.trace_trusted ? '已测' : '已测但不可信') : '未测'}`;
    return `${vcard(lv, esc(head), '', meta, list(d.verdict).map(v => `<div class="note info">${esc(v)}</div>`).join(''))}
      <div class="pgrid">${ts.map(ipCard).join('')}</div>
      ${list(d.rejected).length ? `<p class="hint">已忽略：${esc(d.rejected.join('、'))}</p>` : ''}${envFold(d.env)}<p class="hint">${esc(d.scope)}</p>`;
  }

  // ---------- vps ----------
  function echoBox(title, e) {
    if (!e) return '';
    return `<div class="tile"><div class="tl">${IC.clock}${title}</div><div class="bignum sm">${msn(e.p50_ms)}<small>ms · P50</small></div>
      <div class="sub">P95 ${ms(e.p95_ms)} · P99 ${ms(e.p99_ms)} · 抖动 ${ms(e.jitter_ms)} · 超时 ${n1(e.timeout_rate * 100)}%（${e.recv}/${e.sent}）</div><div class="meta" style="margin-top:4px">${esc(e.what)}</div></div>`;
  }

  function vps(d) {
    const dg = d.diagnosis || {}, dn = DIAG[dg.code] || [dg.code || '—', 'warn'];
    const tp = k => list(d.throughput).find(x => x.key === k);
    const cell = k => {
      const t = tp(k), nm = KEYN[k], ic = nm[2] === 'down' ? IC.down : IC.up;
      if (!t) return `<div class="tile"><div class="tl">${ic}${nm[0]}</div><div class="sub">未测</div></div>`;
      if (!t.median_mbps) return `<div class="tile"><div class="tl">${ic}${nm[0]}<span class="meta">${nm[1]}</span></div><div class="bignum bad">失败</div><div class="sub">${esc(t.error || '没有有效轮次')}</div></div>`;
      const dp = d.baseline && d.baseline.drop_pct && d.baseline.drop_pct[k];
      return `<div class="tile"><div class="tl">${ic}${nm[0]}<span class="meta">${nm[1]}</span></div><div class="bignum">${spd(t.median_mbps)}<small>Mbps</small></div>
        <div class="sub">${n2(t.median_mbps / 8)} MB/s · 区间 ${n1(t.min_mbps)} – ${n1(t.max_mbps)}${dp != null ? ` · 较非高峰基线 ${dp > 0 ? '低' : '高'} ${n1(Math.abs(dp))}%` : ''}</div>${spark(t.rounds)}</div>`;
    };
    const udp = list(d.udp).length ? `<div class="card fold-card">${fold('UDP 吞吐', `<table><tr><th class="l">方向</th><th>目标</th><th>实收 Mbps</th><th>丢包</th><th>乱序</th><th>抖动</th><th class="l"></th></tr>${d.udp.map(u => `<tr><td class="l">${u.dir === 'up' ? '上行' : '下行'}</td><td>${n1(u.target_mbps)}</td><td>${u.err ? '—' : n1(u.recv_mbps)}</td><td>${u.err ? '—' : n1(u.loss_pct) + '%'}</td><td>${u.reordered}</td><td>${n2(u.jitter_ms)} ms</td><td class="l">${u.err ? `<span class="fail">${esc(u.err)}</span>` : ''}</td></tr>`).join('')}</table>
      <p class="hint" style="margin-top:8px">UDP 目标速率越过链路能力时，丢包是预期；看“实收 Mbps”随目标上升是否到顶，和丢包拐点在哪里。UDP 不经 HTTP/SOCKS 代理，只受 TUN 影响。</p>`, true)}</div>` : '';
    const rs = d.resources;
    const res = rs ? `<div class="card fold-card">${fold('服务器资源（测速期间）', rs.available ? `<div class="chips"><span class="chip">CPU steal ${n1(rs.cpu_steal_pct)}%</span><span class="chip">CPU 忙 ${n1(rs.cpu_busy_pct)}%</span><span class="chip">CPU 闲 ${n1(rs.cpu_idle_pct)}%</span><span class="chip">最低可用内存 ${n1(rs.mem_avail_pct)}%</span></div><p class="hint" style="margin-top:8px">采样 ${n1(rs.seconds)} 秒，由服务端回传（仅 Linux 服务端）。</p>` : `<p class="hint">${esc(rs.note || '服务端未回传资源数据')}；因此不输出与服务器资源相关的诊断。</p>`, rs.available)}</div>` : '';
    const ul = (t, a) => list(a).length ? `<div class="meta" style="margin-top:10px;font-weight:600">${t}</div><ul class="ul">${a.map(x => `<li>${esc(x)}</li>`).join('')}</ul>` : '';
    const si = d.server_info ? ` · 服务端 v${esc(d.server_info.version)} ${esc(d.server_info.os)}/${esc(d.server_info.arch)} ${d.server_info.cpus} 核${d.server_info.client_seen_as ? ' · 服务端看到你的出口 ' + esc(d.server_info.client_seen_as) : ''}` : '';
    const more = ul('反证 / 不支持', dg.counter_evidence) + ul('局限', dg.limitations);
    const body = ul('证据', dg.evidence) + (more ? fold('反证与局限', more) : '') + notes(d.notes) +
      `<p class="hint" style="margin-top:10px">诊断为启发式推断，阈值是经验值、未经二次复核；不是确定结论。</p>`;
    const meta = `${esc(fmtT(d.meta.captured_at))} · ${esc(d.server)}${si}<br>${esc(d.via)} · ${esc(PERIOD[d.period] || d.period)} · ${d.params.profile === 'quick' ? '快速' : '标准'}档 ${d.params.rounds} 轮 × ${d.params.duration_s}s`;
    const card = vcard(dn[1], esc(dn[0]), `置信度 ${esc(dg.confidence || '—')}${d.status && d.status !== 'ok' && d.status !== 'OK' ? ' · 状态 ' + esc(d.status) : ''}`, meta, body);
    const gain = `${d.reverse_gain != null ? `<span class="chip">下行多流增益 ${n2(d.reverse_gain)}× · ${esc(GAIN[d.reverse_gain_band] || d.reverse_gain_band)}</span>` : ''}${d.forward_utilization != null ? `<span class="chip">上行利用率 ${n1(d.forward_utilization * 100)}% · ${esc(UTIL[d.utilization_band] || d.utilization_band)}</span>` : ''}${d.params.upload_cap_mbps ? `<span class="chip">已设上行上限 ${n1(d.params.upload_cap_mbps)} Mbps（仅用于判断）</span>` : '<span class="chip">未设上行上限，不限速</span>'}`;
    return `${card}
      <div class="sec-h"><h3>吞吐 · ${esc(d.params.source === 'server' ? '自带服务端' : d.params.source === 'url' ? '自备地址' : '公共端点 · 仅参考路径')}</h3></div>
      <div class="tiles">${cell('fwd1')}${cell('rev1')}${cell('fwd4')}${cell('rev4')}</div><div class="chips" style="margin-top:-6px">${gain}</div>
      <div class="sec-h"><h3>时延</h3></div><div class="tiles">${echoBox('应用层往返（echo）', d.echo)}${echoBox('TCP 握手', d.tcp_connect)}</div>
      ${udp}${res}
      ${d.baseline ? `<p class="hint">历史基线：同一服务器的非高峰记录 ${d.baseline.offpeak_records} 次；本次相对基线的差见上面各格。</p>` : ''}${envFold(d.env)}`;
  }


  // ---------- 线路（去程 / 回程）----------
  const CONF = { sure: ['确定', 'none'], mixed: ['混合', 'warn'], unknown: ['无法确定', ''] };
  const CARR = { telecom: '电信', unicom: '联通', mobile: '移动' };
  function hopTable(tr) {
    if (!tr || !list(tr.hops).length) return '<p class="hint">没有跳数据。</p>';
    const rows = tr.hops.map(h => `<tr><td class="l">${h.ttl}</td><td class="l">${esc(h.ip || '*')}</td><td>${h.rtt_ms ? ms(h.rtt_ms) : '—'}</td><td>${h.got}/${h.sent}</td></tr>`).join('');
    return `<table><tr><th class="l">跳</th><th class="l">地址</th><th>最小往返</th><th>回应</th></tr>${rows}</table><div class="meta" style="margin-top:6px">探测方式 ${esc(tr.method)} · ${tr.reached ? '已到达目标' : '未到达目标（目标或沿途可能屏蔽 ICMP）'}</div>`;
  }
  function lineRow(l) {
    if (l.err) return `<div class="rt-row"><div class="rt-h"><b>${esc(l.label)}</b><span class="vchip"><i></i>失败</span></div><div class="note warn">${esc(l.err)}</div></div>`;
    const v = l.verdict, c = CONF[v.confidence] || CONF.unknown, path = list(v.as_path).map(a => 'AS' + a).join(' → ');
    const ev = list(v.evidence).map(e => `<li>${esc(e)}</li>`).join('');
    return `<div class="rt-row"><div class="rt-h"><b>${esc(l.label)}</b><span class="rt-v">${esc(v.label)}</span><span class="vchip ${c[1]}"><i></i>${c[0]}</span></div>
      <div class="meta">${path ? 'AS 路径：' + esc(path) : '路径中没有识别出骨干 AS'} · ${list(l.trace && l.trace.hops).length} 跳${v.reached ? '' : ' · 未到达目标'}</div>
      ${fold('证据与逐跳', `<ul class="ev">${ev}</ul>${hopTable(l.trace)}`)}</div>`;
  }
  function route(d) {
    const hs = list(d.headlines), ret = list(d.return), out = d.outbound;
    const head = hs.length ? esc(hs[0]) : '没有得到线路结果';
    const body = (hs.length > 1 ? `<ul class="ev">${hs.slice(1).map(h => `<li>${esc(h)}</li>`).join('')}</ul>` : '') + notes(d.notes);
    const card = vcard('none', head, `服务器 ${esc(d.server)}${d.server_info ? ' · agent v' + esc(d.server_info.version) + ' · ' + esc(d.server_info.os) + '/' + esc(d.server_info.arch) : ''}`,
      `${esc(fmtT(d.meta.captured_at))} · 前缀表 ${esc(d.prefix_version)} · 目标表 ${esc(d.targets_version)}`, body);
    let r = card;
    if (ret.length) {
      const by = c => ret.filter(l => l.carrier === c);
      r += `<div class="sec-h"><h3>回程 · 服务器 → 国内（探测在服务器上完成）</h3></div>` +
        ['telecom', 'unicom', 'mobile'].map(c => by(c).length ? `<div class="card rt-card"><h4>${CARR[c]}</h4>${by(c).map(lineRow).join('')}</div>` : '').join('');
    }
    if (d.client_return) r += `<div class="sec-h"><h3>回到你：服务器 → 你的出口 IP</h3></div><div class="card rt-card">${lineRow(d.client_return)}</div>`;
    if (out) r += `<div class="sec-h"><h3>去程 · 本机 → 服务器</h3></div><div class="card rt-card">${lineRow(out)}</div>`;
    r += `<p class="hint">线路判定依据路径上出现的骨干网地址与先后顺序（内置前缀表，不外发任何 IP）；“确定”只表示有明确证据，“混合”表示证据矛盾或顺序不典型，“无法确定”表示没有可识别地址。CN2 的 GIA / GT 区分是行业经验规则，不是运营商官方标注。回程的目标是各地运营商的公开 DNS 服务器，不等于你要访问的任意网站。</p>${envFold(d.env)}`;
    return r;
  }

  // 历史列表用：由 headline 等无法得知等级，这里只给图标
  g.Render = {
    esc, env, envBrief, logo: '',
    page(kind, d) { return kind === 'broadband' ? broadband(d) : kind === 'testip' ? testip(d) : kind === 'route' ? route(d) : vps(d); },
    title(kind) { return { broadband: '宽带测速', testip: '测试IP', vps: 'VPS测速', route: '线路检测' }[kind] || kind; },
  };
})(window);
