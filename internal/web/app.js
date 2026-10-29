(() => {
  const $ = s => document.querySelector(s), $$ = s => [...document.querySelectorAll(s)];
  const E = Render.esc;
  const reduce = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const api = async (path, body) => {
    let r;
    try { r = await fetch(path, body === undefined ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }); }
    catch { throw new Error('连接不上迹翼程序，它可能已经退出。'); }
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw new Error(j.error || ('请求失败（HTTP ' + r.status + '）'));
    return j;
  };
  const val = n => (document.querySelector(`input[name=${n}]:checked`) || {}).value;
  const num = id => { const v = parseFloat($(id).value); return isFinite(v) && v > 0 ? v : null; };
  const KINDS = ['broadband', 'testip', 'vps', 'route'];
  let cur = 'broadband', running = false, jobKind = null, poll = null, mode = 'browser';

  // 演示 / 截图用：?theme=dark|light 强制主题（默认跟随系统）
  const th = new URLSearchParams(location.search).get('theme');
  if (th === 'dark' || th === 'light') document.documentElement.dataset.theme = th;

  // ---- 仪表圆弧：240° 开口向下 ----
  (function () {
    const R = 96, cx = 120, cy = 108, p = a => [cx + R * Math.cos(a * Math.PI / 180), cy + R * Math.sin(a * Math.PI / 180)].map(v => v.toFixed(2));
    const [x1, y1] = p(150), [x2, y2] = p(30), d = `M${x1} ${y1}A${R} ${R} 0 1 1 ${x2} ${y2}`;
    ['#g-track', '#g-val', '#gg-track'].forEach(s => $(s).setAttribute('d', d));
  })();

  // ---- 提示 ----
  const showErr = (m, info) => { $('#err').className = 'alert ' + (info ? 'info' : 'bad'); $('#err-t').textContent = m; };
  const hideErr = () => $('#err').classList.add('hide');
  $('#err-x').onclick = hideErr; $('#banner-x').onclick = () => $('#banner').classList.add('hide');

  // ---- 导航 ----
  function go(k) {
    cur = k;
    $$('.nv').forEach(t => t.classList.toggle('on', t.dataset.k === k));
    ['broadband', 'testip', 'vps', 'route', 'history'].forEach(x => { const p = $('#p-' + x); p.classList.toggle('hide', x !== k); });
    hideErr(); sync();
    if (k === 'history') { backToList(); loadHist(); }
    $('#main').scrollTop = 0;
  }
  $$('.nv').forEach(t => t.onclick = () => go(t.dataset.k));
  function sync() {
    const live = running && jobKind === cur;
    document.body.classList.toggle('running', live);
    $('#live').classList.toggle('hide', !live);
    $$('.nv').forEach(t => t.classList.toggle('busy', running && t.dataset.k === jobKind));
    $$('.run').forEach(b => b.disabled = running);
  }

  // ---- 表单联动 ----
  $$('input[name=pm]').forEach(x => x.onchange = () => $('#customf').classList.toggle('hide', val('pm') !== 'custom'));
  $$('input[name=ipm]').forEach(x => x.onchange = () => $('#ip-portf').classList.toggle('hide', val('ipm') !== 'tcp'));
  $$('input[name=vs]').forEach(x => x.onchange = () => ['server', 'url', 'public'].forEach(s => $('#vs-' + s).classList.toggle('hide', s !== val('vs'))));
  $$('[data-toggle-setup]').forEach(b => b.onclick = () => { const s = b.closest('.setup'); const o = s.classList.toggle('open'); b.textContent = o ? '收起设置' : '调整设置'; });
  $$('[data-adv]').forEach(b => b.onclick = () => { const d = $('#' + b.dataset.adv); d.open = true; setTimeout(() => ($('#bb-pd') || d).focus(), 30); });
  $$('.page input[type=text],.page input[type=password],.page input[type=number]').forEach(i => i.addEventListener('keydown', e => { if (e.key === 'Enter') { const b = $('#p-' + cur + ' .run'); if (b && !b.disabled) b.click(); } }));

  // ---- 网络环境（抽屉 + 侧栏胶囊）----
  const path = () => ({ mode: val('pm'), custom: $('#pcustom').value.trim(), dns: $('#pdns').value.trim() });
  const openDrawer = () => { $('#drawer').classList.remove('hide'); $('#drawer-x').focus(); };
  const closeDrawer = () => { $('#drawer').classList.add('hide'); $('#netbtn').focus(); };
  $('#netbtn').onclick = openDrawer; $('#scrim').onclick = closeDrawer; $('#drawer-x').onclick = closeDrawer;
  document.addEventListener('keydown', e => { if (e.key === 'Escape' && !$('#drawer').classList.contains('hide')) closeDrawer(); });
  async function checkEnv() {
    $('#envbox').innerHTML = '<span class="meta"><span class="spin"></span>正在检查环境…（最多十几秒）</span>';
    $('#net-a').textContent = '检查网络…'; $('#net-b').textContent = '网络环境'; $('.netpill .dot').className = 'dot';
    try {
      const e = await api('/api/env', { path: path() });
      $('#envbox').innerHTML = Render.env(e);
      const b = Render.envBrief(e); $('#net-a').textContent = b.a; $('#net-b').textContent = b.b || '网络环境'; $('.netpill .dot').className = 'dot ' + b.lv;
    } catch (e) {
      $('#envbox').innerHTML = `<div class="note bad">${E(e.message)}</div>`;
      $('#net-a').textContent = '检查失败'; $('.netpill .dot').className = 'dot bad';
    }
  }
  $('#recheck').onclick = checkEnv;

  // ---- 提交 ----
  function req(k) {
    const r = { kind: k, path: path() };
    if (k === 'broadband') r.bb = { profile: val('bbp'), regions: [$('#bb-cn').checked && 'cn', $('#bb-gl').checked && 'global'].filter(Boolean), down: $('#bb-dn').checked, up: $('#bb-up').checked, streams: +$('#bb-st').value, plan_down: num('#bb-pd'), plan_up: num('#bb-pu') };
    if (k === 'testip') r.ip = { targets: $('#ip-t').value, method: val('ipm'), tcp_port: +$('#ip-port').value, count: +$('#ip-n').value, trace: $('#ip-tr').checked, lookup: $('#ip-lk').checked };
    if (k === 'route') r.route = { server: $('#r-srv').value.trim(), token: $('#r-tok').value };
    if (k === 'vps') r.vps = { source: val('vs'), server: $('#v-srv').value.trim(), token: $('#v-tok').value, region: $('#v-reg').value, down_url: $('#v-du').value.trim(), up_url: $('#v-uu').value.trim(), profile: val('vp'), upload_cap: num('#v-cap'), udp_mbps: +$('#v-udp').value || 0 };
    return r;
  }
  $$('.run').forEach(b => b.onclick = async () => {
    if (running) return;
    const k = b.dataset.k; hideErr();
    try { await api('/api/run', req(k)); } catch (e) { return showErr(e.message); }
    $('#res-' + k).innerHTML = ''; $('#p-' + k).classList.remove('has-result');
    startPoll(k); saveCfg();
  });
  let cancelT;
  $('#cancel').onclick = () => { $('#cancel').textContent = '正在取消…'; api('/api/cancel', {}).catch(() => 0); clearTimeout(cancelT); cancelT = setTimeout(() => $('#cancel').textContent = '取消测速', 4000); };
  let quitT;
  $('#quit').onclick = async () => {
    const b = $('#quit');
    if (!b.dataset.sure) { b.dataset.sure = 1; b.textContent = '再点一次确认退出'; b.style.color = 'var(--bad)'; quitT = setTimeout(() => { delete b.dataset.sure; b.textContent = '退出程序'; b.style.color = ''; }, 3000); return; }
    clearTimeout(quitT); await api('/api/quit', {}).catch(() => 0);
    $('#lost h2').textContent = '已退出'; $('#lost p').textContent = '迹翼程序已退出，可以关闭此标签页。'; $('#lost').classList.remove('hide');
  };

  // ---- 实时视图：数字与圆弧平滑逼近目标值 ----
  const STEPS = [50, 100, 200, 500, 1000, 2000, 5000, 10000, 20000, 50000, 100000, 200000, 500000];
  const L = { disp: 0, target: 0, scale: 100, progress: false, none: true, pts: [], raf: 0, shown: '' };
  function resetLive() { Object.assign(L, { disp: 0, target: 0, scale: 100, none: true, pts: [], shown: '' }); $('#g-val').style.strokeDashoffset = 100; $('#g-num').textContent = '—'; $('#lspark').innerHTML = ''; $('#live-b').style.width = '0'; }
  function draw() {
    const gmax = L.progress ? 1 : L.scale, pct = Math.max(0, Math.min(1, L.disp / gmax));
    $('#g-val').style.strokeDashoffset = (100 - pct * 100).toFixed(2);
    const t = L.none ? '—' : L.progress ? Math.round(L.disp * 100) + '%' : L.disp >= 100 ? Math.round(L.disp).toLocaleString('en-US') : L.disp.toFixed(1);
    if (t !== L.shown) { L.shown = t; $('#g-num').textContent = t; }
  }
  function frame() {
    if (!running) { L.raf = 0; return; }
    const gmax = L.progress ? 1 : L.scale, diff = L.target - L.disp;
    L.disp = reduce || Math.abs(diff) < gmax * 0.0008 ? L.target : L.disp + diff * 0.14;
    draw(); L.raf = requestAnimationFrame(frame);
  }
  function drawSpark() {
    const el = $('#lspark'), W = 600, H = 78, p = L.pts.slice(-90);
    if (p.length < 2) { el.setAttribute('viewBox', `0 0 ${W} ${H}`); el.innerHTML = `<line class="base" x1="0" y1="${H - 1}" x2="${W}" y2="${H - 1}"/>`; return; }
    const mx = Math.max(...p, 1) * 1.12, xy = p.map((v, i) => [i / (p.length - 1) * W, H - 4 - v / mx * (H - 10)]);
    let d = `M${xy[0][0].toFixed(1)} ${xy[0][1].toFixed(1)}`;
    for (let i = 1; i < xy.length; i++) { const a = xy[i - 1], b = xy[i], mxp = (a[0] + b[0]) / 2; d += `C${mxp.toFixed(1)} ${a[1].toFixed(1)} ${mxp.toFixed(1)} ${b[1].toFixed(1)} ${b[0].toFixed(1)} ${b[1].toFixed(1)}`; }
    el.setAttribute('viewBox', `0 0 ${W} ${H}`);
    el.innerHTML = `<line class="base" x1="0" y1="${H - 1}" x2="${W}" y2="${H - 1}"/><path class="ar" d="${d}L${W} ${H}L0 ${H}Z"/><path class="ln" d="${d}"/>`;
  }
  function startPoll(k) {
    running = true; jobKind = k; L.progress = k === 'testip' || k === 'route'; resetLive();
    $('#live-t').textContent = Render.title(k) + ' · 进行中'; $('#cancel').textContent = '取消测速';
    $('#g-unit').textContent = L.progress ? '进度' : 'Mbps'; $('#lspark').classList.toggle('hide', L.progress);
    if (cur !== k && cur !== 'history') go(k); else sync();
    clearInterval(poll); poll = setInterval(tick, 500); tick();
    if (!L.raf) L.raf = requestAnimationFrame(frame);
  }
  async function tick() {
    let j; try { j = await api('/api/job'); } catch { return; }
    if (j.state === 'running') {
      if (!running) { running = true; jobKind = j.kind; sync(); }
      if (L.progress) { L.none = false; L.target = j.frac || 0; }
      else if (j.mbps >= 0) {
        L.none = false; L.target = j.mbps; L.pts.push(j.mbps);
        while (j.mbps * 1.12 > L.scale && L.scale < STEPS[STEPS.length - 1]) L.scale = STEPS.find(s => s > L.scale);
        $('#g-max').textContent = L.scale.toLocaleString('en-US'); drawSpark();
      } else { L.target = 0; L.none = true; }
      if (L.progress) $('#g-max').textContent = '100%';
      $('#live-b').style.width = Math.round((j.frac || 0) * 100) + '%';
      $('#live-s').textContent = j.text || '';
      $('#live-el').textContent = '已用 ' + Math.round(j.elapsed_s) + ' 秒';
      return;
    }
    clearInterval(poll); running = false; sync();
    if (j.state === 'done') showRun(j.run_id, jobKind || j.kind);
    else if (j.state === 'error') showErr(j.err);
    else if (j.state === 'canceled') showErr('已取消。', true);
  }

  // ---- 结果 ----
  const ACT = id => `<div class="res-act noprint"><button class="btn" data-open="${E(id)}">打开独立报告<span class="meta">可打印 / 存 PDF</span></button><span class="meta">记录保存在：${E((window.DATADIR || '') + '\\runs\\' + id)}</span></div>`;
  function bindAct(root) {
    root.querySelectorAll('[data-open]').forEach(b => b.onclick = () => api('/api/open-report', { id: b.dataset.open }).catch(e => showErr(e.message)));
  }
  async function showRun(id, k) {
    try {
      const d = await api('/api/run/' + id), kind = d.meta.kind, box = $('#res-' + kind);
      box.innerHTML = ACT(id) + Render.page(kind, d); bindAct(box);
      $('#p-' + kind).classList.add('has-result');
      { const rb = $('#p-' + kind + ' .run'), t = rb.lastChild; if (t && t.nodeType === 3) t.textContent = '重新测速'; }
      if (cur !== kind) go(kind); else sync();
      $('#main').scrollTop = 0;
    } catch (e) { showErr(e.message); }
  }

  // ---- 历史 ----
  const ICON = k => (document.querySelector(`.nv[data-k=${k}] svg`) || { outerHTML: '' }).outerHTML;
  function when(s) {
    const d = new Date(s), n = new Date(), hm = d.toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' });
    const day = x => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime(), diff = Math.round((day(n) - day(d)) / 864e5);
    return (diff === 0 ? '今天' : diff === 1 ? '昨天' : `${d.getMonth() + 1}月${d.getDate()}日`) + ' ' + hm;
  }
  // 打开详情前记下列表的滚动位置，返回时还原（详情与列表共用 #main 的滚动条）
  let listScroll = 0;
  function backToList(restore) {
    $('#hist-detail').classList.add('hide'); $('#hist-list').classList.remove('hide');
    $('#main').scrollTop = restore ? listScroll : 0;
  }
  async function loadHist() {
    let h = []; try { h = await api('/api/history'); } catch (e) { return showErr(e.message); }
    $('#hist-list').innerHTML = h.length ? `<div class="hl">${h.map(e => `<div class="hi" data-id="${E(e.id)}"><div class="hi-ic">${ICON(e.kind)}</div><div class="hi-b"><b>${E(String(e.title).replace(/^vps/, "VPS"))}</b><div>${E(e.headline || '')}</div></div><span class="hi-t">${E(when(e.at))}</span><button class="del" data-id="${E(e.id)}" title="删除这条记录">删除</button></div>`).join('')}</div>`
      : `<div class="card empty"><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/></svg><h2>还没有记录</h2><p>完成一次测速后，结果会保存在这里，方便回看。</p><button class="btn" id="h-go">去测速</button></div>`;
    $('#datadir').textContent = window.DATADIR ? '数据目录：' + window.DATADIR : '';
    const gb = $('#h-go'); if (gb) gb.onclick = () => go('broadband');
    $$('#hist-list .hi').forEach(r => r.onclick = ev => { if (!ev.target.closest('.del')) openHist(r.dataset.id); });
    $$('#hist-list .del').forEach(b => b.onclick = async () => {
      if (!b.dataset.sure) { b.dataset.sure = 1; b.classList.add('sure'); b.textContent = '确认删除'; setTimeout(() => { if (b.isConnected) { delete b.dataset.sure; b.classList.remove('sure'); b.textContent = '删除'; } }, 3000); return; }
      try { await api('/api/delete', { id: b.dataset.id }); loadHist(); } catch (e) { showErr(e.message); }
    });
  }
  async function openHist(id) {
    try {
      const d = await api('/api/run/' + id), box = $('#hist-detail');
      listScroll = $('#main').scrollTop;
      box.innerHTML = `<button class="btn ghost back" id="h-back">← 返回列表</button><div class="result">${ACT(id)}${Render.page(d.meta.kind, d)}</div>`;
      $('#h-back').onclick = () => backToList(true); bindAct(box);
      $('#hist-list').classList.add('hide'); box.classList.remove('hide'); $('#main').scrollTop = 0;
    } catch (e) { showErr(e.message); }
  }

  // ---- 记住表单（不含令牌）----
  const FIELDS = ['#pcustom', '#pdns', '#bb-pd', '#bb-pu', '#ip-t', '#ip-port', '#ip-n', '#v-srv', '#r-srv', '#v-du', '#v-uu', '#v-cap', '#v-udp', '#bb-st', '#v-reg'];
  const RADIOS = ['pm', 'bbp', 'ipm', 'vs', 'vp'];
  function saveCfg() {
    const c = { f: {}, r: {} };
    FIELDS.forEach(f => c.f[f] = $(f).value); RADIOS.forEach(r => c.r[r] = val(r));
    api('/api/config', c).catch(() => 0);
  }
  async function loadCfg() {
    try {
      const c = await api('/api/config');
      Object.entries(c.f || {}).forEach(([k, v]) => { const el = $(k); if (el) el.value = v; });
      Object.entries(c.r || {}).forEach(([n, v]) => { const el = document.querySelector(`input[name=${n}][value="${v}"]`); if (el) { el.checked = true; el.dispatchEvent(new Event('change')); } });
    } catch { }
    if ($('#bb-pd').value || $('#bb-pu').value) $('#adv-broadband').open = true;
  }

  // ---- 启动 ----
  (async () => {
    const i = await api('/api/info').catch(() => ({}));
    window.DATADIR = i.data_dir; $('#ver').textContent = i.version ? 'v' + i.version : '';
    mode = i.mode || 'browser';
    if (mode === 'window') $('#quit').classList.add('hide');
    if (i.notice) { $('#banner-t').textContent = i.notice; $('#banner').classList.remove('hide'); }
    await loadCfg();
    const j = await api('/api/job').catch(() => ({}));
    if (j.state === 'running') startPoll(j.kind);
    checkEnv();
    let fails = 0;
    setInterval(() => api('/api/ping').then(() => { fails = 0; $('#lost').classList.add('hide'); }).catch(() => { if (++fails >= 2) $('#lost').classList.remove('hide'); }), 6000);
  })();
})();
