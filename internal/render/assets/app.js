// app.js: draws the report from the embedded JSON. Uses core.js (window.AHV).
// All log-derived strings are inserted with textContent, never as HTML.
'use strict';
(function () {
  const A = window.AHV;
  const NS = 'http://www.w3.org/2000/svg';
  const $ = (id) => document.getElementById(id);
  const payload = JSON.parse($('data').textContent);
  const R = payload.report;

  const LANES = [
    ['user-cli', 'User · command line'],
    ['user-gui', 'User · GUI'],
    ['root', 'Root'],
    ['dpkg', 'dpkg direct'],
    ['unattended', 'Unattended'],
  ];
  const LANE_NAME = Object.fromEntries(LANES);
  const ACTIONS = ['install', 'upgrade', 'downgrade', 'reinstall', 'remove', 'purge'];
  const ACTION_LABEL = { install: 'Installed', upgrade: 'Upgraded', downgrade: 'Downgraded', reinstall: 'Reinstalled', remove: 'Removed', purge: 'Purged' };
  const GROUPS = [['install', 'installed'], ['upgrade', 'upgraded'], ['remove', 'removed']];
  const MIN_SPAN = A.MIN; // DESIGN §6: zoom down to a 1-minute window
  const CLUSTER_GAP = 6; // px
  const CLUSTER_MAX = 64; // px; see AHV.cluster
  const LANE_H = 46, AXIS_H = 24, EDGE = 10;

  // ---- data ----------------------------------------------------------------

  const txs = R.transactions.map((t) => ({
    raw: t,
    id: t.id,
    lane: t.origin,
    start: A.wall(t.start),
    end: A.wall(t.end),
    n: t.changes.length,
    bad: !!(t.error || t.incomplete),
    changes: t.changes.map((c) => ({ action: c.action, at: A.wall(c.at) })),
  }));
  const byId = new Map(txs.map((t) => [t.id, t]));
  const lanes = LANES.filter(([k]) => txs.some((t) => t.lane === k));

  // Most common arch: shown names omit it ("libc6", but "libc6:i386").
  const archCount = {};
  for (const t of R.transactions) for (const c of t.changes) if (c.arch && c.arch !== 'all') archCount[c.arch] = (archCount[c.arch] || 0) + 1;
  const nativeArch = Object.keys(archCount).sort((a, b) => archCount[b] - archCount[a])[0];

  let F0 = Math.min(...txs.map((t) => t.start)), F1 = Math.max(...txs.map((t) => t.end));
  if (!txs.length) { F0 = A.wall(R.from) || Date.now(); F1 = A.wall(R.to) || F0; }
  {
    const pad = Math.max((F1 - F0) * 0.02, 30 * A.MIN);
    F0 -= pad; F1 += pad;
  }
  let view = [F0, F1];
  let selected = null; // {tx: id} or {ids: Set}

  // ---- helpers -------------------------------------------------------------

  function el(tag, attrs, ...kids) {
    const e = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs || {})) {
      if (v == null || v === false) continue;
      if (k === 'class') e.className = v;
      else if (k.startsWith('on')) e.addEventListener(k.slice(2), v);
      else e.setAttribute(k, v === true ? '' : v);
    }
    for (const k of kids.flat()) if (k != null && k !== false) e.append(k instanceof Node ? k : String(k));
    return e;
  }

  function sv(tag, attrs, parent) {
    const e = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs || {})) if (v != null) e.setAttribute(k, v);
    if (parent) parent.append(e);
    return e;
  }

  const swatch = (color) => { const s = el('span', { class: 'swatch' }); s.style.background = color; return s; };
  const plural = (n, word) => `${n.toLocaleString()} ${word}${n === 1 ? '' : 's'}`;
  const pkgName = (c) => (c.arch && c.arch !== 'all' && c.arch !== nativeArch ? `${c.name}:${c.arch}` : c.name);
  const truncate = (s, n) => (s.length > n ? s.slice(0, n - 1) + '…' : s);

  function summary(changes) {
    const n = { install: 0, upgrade: 0, remove: 0 };
    for (const c of changes) n[A.actionGroup(c.action)]++;
    return GROUPS.filter(([g]) => n[g]).map(([g, w]) => `${n[g]} ${w}`).join(', ') || 'no package changes';
  }

  function version(c) {
    if (c.action === 'upgrade' || c.action === 'downgrade') return `${c.old || '?'} → ${c.new || '?'}`;
    return c.new || c.old || '';
  }

  // ---- header --------------------------------------------------------------

  function drawHeader() {
    $('host').textContent = R.host;
    let cov = txs.length
      ? `Logs cover ${A.fmtDate(A.wall(R.from))} → ${A.fmtDate(A.wall(R.to))}`
      : 'No package transactions found in the logs';
    if (R.tz) cov += ` · times are ${R.tz} local time`;
    if (!R.hasBaseline) cov += ' · no dpkg status file was read, so current versions are unknown';
    $('coverage').textContent = cov;

    const tg = el('div', { class: 'group' }, el('span', {}, 'Transactions'));
    for (const [k, name] of lanes) {
      tg.append(el('span', { class: 'chip' }, swatch(`var(--o-${k})`), name, el('b', {}, txs.filter((t) => t.lane === k).length.toLocaleString())));
    }
    const bad = txs.filter((t) => t.bad).length;
    if (bad) tg.append(el('span', { class: 'chip' }, swatch('var(--error)'), 'error or incomplete', el('b', {}, bad)));

    const n = { install: 0, upgrade: 0, remove: 0 };
    for (const t of txs) for (const c of t.changes) n[A.actionGroup(c.action)]++;
    const cg = el('div', { class: 'group' }, el('span', {}, 'Package changes'));
    for (const [g, w] of GROUPS) cg.append(el('span', { class: 'chip' }, swatch(`var(--a-${g})`), w, el('b', {}, n[g].toLocaleString())));
    $('totals').replaceChildren(tg, cg);

    if (R.warnings && R.warnings.length) {
      const w = $('warnings');
      w.hidden = false;
      w.querySelector('summary').textContent = `${plural(R.warnings.length, 'warning')} while reading the logs`;
      const ul = w.querySelector('ul');
      for (const x of R.warnings.slice(0, 1000)) {
        ul.append(el('li', {}, (x.file ? x.file + (x.line ? `:${x.line}` : '') + ': ' : '') + x.msg));
      }
      if (R.warnings.length > 1000) ul.append(el('li', {}, `… and ${R.warnings.length - 1000} more (see stderr)`));
    }

    $('footer').textContent = `Generated by apt-history-viewer ${payload.version} on ${A.fmt(A.wall(R.generated), false)} from ${plural((R.files || []).length, 'file')}.`;

    const ol = $('ov-legend');
    for (const [g, w] of GROUPS) ol.append(el('span', {}, swatch(`var(--a-${g})`), w));
    ol.append(el('span', {}, 'bar height ∝ √changes'));

    const tl = $('tl-legend');
    const icon = (draw) => { const s = sv('svg', { width: 16, height: 16, viewBox: '-8 -8 16 16', 'aria-hidden': 'true' }); draw(s); return s; };
    tl.append(
      el('span', {}, icon((s) => sv('path', { d: 'M0,-6L6,0L0,6L-6,0Z', fill: 'var(--muted)' }, s)), 'manual'),
      el('span', {}, icon((s) => sv('circle', { r: 5, fill: 'none', stroke: 'var(--muted)', 'stroke-width': 2 }, s)), 'automatic'),
      el('span', {}, icon((s) => sv('circle', { r: 5, fill: 'none', stroke: 'var(--error)', 'stroke-width': 2.5 }, s)), 'error or incomplete'),
      el('span', {}, icon((s) => sv('rect', { x: -8, y: -5, width: 16, height: 10, rx: 5, fill: 'var(--muted)', 'fill-opacity': 0.3, stroke: 'var(--muted)' }, s)), 'cluster (click to zoom)'),
      el('span', {}, 'size ∝ √packages'),
    );
  }

  // ---- overview strip --------------------------------------------------------

  const ov = { svg: null, viewport: null, sc: null, bins: A.bins(txs, F0, F1) };
  $('ov-unit').textContent = ov.bins.size > A.DAY ? 'week' : 'day';

  function drawOverview() {
    const box = $('overview');
    const W = Math.max(box.clientWidth, 200), H = 70, BAR_H = 48;
    const sc = (ov.sc = A.scale(F0, F1, 0, W));
    const svg = sv('svg', { viewBox: `0 0 ${W} ${H}`, height: H, role: 'img', 'aria-label': `Package changes per ${ov.bins.size > A.DAY ? 'week' : 'day'}` });
    for (const k of A.ticks(F0, F1, Math.max(2, W / 110))) {
      const x = sc.x(k.t);
      sv('line', { class: 'grid' + (k.major ? ' major' : ''), x1: x, x2: x, y1: 0, y2: BAR_H }, svg);
      if (x < W - 7 * k.label.length) sv('text', { x: x + 3, y: H - 6 }, svg).textContent = k.label;
    }
    const max = Math.max(1, ...ov.bins.bins.map((b) => b.install + b.upgrade + b.remove));
    const h = (n) => (n ? Math.max(1.5, (Math.sqrt(n) / Math.sqrt(max)) * BAR_H) : 0);
    for (const b of ov.bins.bins) {
      const x = sc.x(b.t0), w = Math.max(1, sc.x(b.t1) - x - (sc.x(b.t1) - x > 4 ? 1 : 0));
      let y = BAR_H;
      for (const [g] of GROUPS) {
        const hh = h(b.install + b.upgrade + b.remove) * (b[g] / (b.install + b.upgrade + b.remove));
        if (!hh) continue;
        y -= hh;
        sv('rect', { class: `bar-${g}`, x, y, width: w, height: hh }, svg);
      }
    }
    ov.viewport = sv('rect', { class: 'viewport', y: 0, height: BAR_H }, svg);

    svg.addEventListener('pointermove', (e) => {
      const t = sc.t(e.offsetX * (W / svg.clientWidth));
      const b = ov.bins.bins.find((b) => t >= b.t0 && t < b.t1);
      if (!b) return hideTip();
      const label = ov.bins.size > A.DAY ? `Week of ${A.fmtDate(b.t0)}` : A.fmtDate(b.t0);
      showTip(e.clientX, e.clientY, [[label, 't'], [GROUPS.filter(([g]) => b[g]).map(([g, w]) => `${b[g]} ${w}`).join(', ')]]);
    });
    svg.addEventListener('pointerleave', hideTip);
    svg.addEventListener('click', (e) => {
      // Centre the timeline on the clicked time, keeping the zoom level.
      const t = sc.t(e.offsetX * (W / svg.clientWidth)), span = view[1] - view[0];
      setView(A.clampRange(t - span / 2, t + span / 2, F0, F1));
    });
    box.replaceChildren(svg);
    ov.svg = svg;
    drawViewport();
  }

  function drawViewport() {
    if (!ov.viewport) return;
    const x0 = ov.sc.x(view[0]), x1 = ov.sc.x(view[1]);
    ov.viewport.setAttribute('x', x0);
    ov.viewport.setAttribute('width', Math.max(2, x1 - x0));
    ov.viewport.style.display = view[0] <= F0 && view[1] >= F1 ? 'none' : '';
  }

  // ---- timeline --------------------------------------------------------------

  const tl = { svg: null, W: 0, sc: null };

  function drawTimeline() {
    const box = $('timeline');
    if (!txs.length) {
      box.replaceChildren(el('p', { class: 'empty' }, 'Nothing to show.'));
      return;
    }
    const W = (tl.W = Math.max(box.clientWidth, 200));
    const H = lanes.length * LANE_H + AXIS_H;
    const sc = (tl.sc = A.scale(view[0], view[1], EDGE, W - EDGE));
    let svg = tl.svg;
    if (!svg) {
      svg = tl.svg = sv('svg', { role: 'group', 'aria-label': 'Transactions over time, one lane per origin' });
      attachTimelineEvents(svg);
      box.replaceChildren(svg);
    }
    svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
    svg.setAttribute('height', H);
    svg.replaceChildren();

    lanes.forEach(([, name], i) => {
      sv('rect', { class: 'lane-bg', x: 0, y: i * LANE_H + 1, width: W, height: LANE_H - 2, rx: 4 }, svg);
    });
    for (const k of A.ticks(view[0], view[1], Math.max(3, W / 75))) {
      const x = sc.x(k.t);
      sv('line', { class: 'grid' + (k.major ? ' major' : ''), x1: x, x2: x, y1: 0, y2: H - AXIS_H + 4 }, svg);
      if (x < W - 7 * k.label.length) sv('text', { x: x + 3, y: H - 7 }, svg).textContent = k.label; // don't clip at the edge
    }
    lanes.forEach(([, name], i) => {
      sv('text', { class: 'lane-label', x: 6, y: i * LANE_H + 13 }, svg).textContent = name;
    });

    const laneIndex = new Map(lanes.map(([k], i) => [k, i]));
    const laid = A.layout(txs, sc, CLUSTER_GAP, CLUSTER_MAX);
    for (const [lane, clusters] of laid) {
      const cy = laneIndex.get(lane) * LANE_H + LANE_H / 2 + 5;
      for (const c of clusters) svg.append(c.items.length === 1 ? drawMark(c.items[0], cy) : drawCluster(c, lane, cy));
    }

    $('range').textContent = `${A.fmt(view[0], false)} → ${A.fmt(view[1], false)} (${A.fmtDuration(view[1] - view[0])})`;
    drawViewport();
  }

  function isSelected(ids) {
    if (!selected) return false;
    if (selected.tx != null) return ids.includes(selected.tx);
    return ids.length === selected.ids.size && ids.every((id) => selected.ids.has(id));
  }

  function drawMark(it, cy) {
    const t = it.tx, color = `var(--o-${t.lane})`, manual = t.raw.manual;
    const g = sv('g', { class: 'mark' + (t.bad ? ' bad' : '') + (isSelected([t.id]) ? ' selected' : ''), tabindex: 0, role: 'button' });
    g.setAttribute('aria-label', `${A.fmt(t.start)}, ${LANE_NAME[t.lane]}${t.raw.tool ? ' (' + t.raw.tool + ')' : ''}: ${summary(t.raw.changes)}`);
    const xEnd = tl.sc.x(t.end);
    if (xEnd - it.cx > it.r * 2) {
      const d = sv('line', { class: 'dur', x1: it.cx, x2: xEnd, y1: cy, y2: cy }, g);
      d.style.stroke = color;
    }
    const r = it.r;
    const shape = manual
      ? sv('path', { class: 'shape', d: `M${it.cx},${cy - r}L${it.cx + r},${cy}L${it.cx},${cy + r}L${it.cx - r},${cy}Z` }, g)
      : sv('circle', { class: 'shape', cx: it.cx, cy, r: r - 1 }, g);
    shape.style.fill = manual ? color : 'var(--card)';
    shape.style.stroke = color;
    if (!manual) shape.style.strokeWidth = 2;
    g._activate = () => select({ tx: t.id });
    g._tip = () => txTip(t);
    return g;
  }

  function drawCluster(c, lane, cy) {
    const items = c.items.map((i) => i.tx), color = `var(--o-${lane})`;
    const ids = items.map((t) => t.id);
    const bad = items.some((t) => t.bad);
    const g = sv('g', { class: 'mark cluster' + (bad ? ' bad' : '') + (isSelected(ids) || (selected && selected.tx != null && ids.includes(selected.tx)) ? ' selected' : ''), tabindex: 0, role: 'button' });
    g.setAttribute('aria-label', `Cluster of ${items.length} transactions in ${LANE_NAME[lane]}; activate to zoom in`);
    const w = Math.max(c.x1 - c.x0, 22), x = (c.x0 + c.x1) / 2 - w / 2;
    const rect = sv('rect', { class: 'shape', x, y: cy - 10, width: w, height: 20, rx: 10 }, g);
    rect.style.fill = color;
    rect.style.fillOpacity = 0.25;
    rect.style.stroke = color;
    if (lane === 'unattended') rect.style.strokeDasharray = '3 2';
    const label = sv('text', { class: 'count', x: x + w / 2, y: cy + 4, 'text-anchor': 'middle' }, g);
    label.textContent = items.length;
    g._activate = () => {
      const t0 = Math.min(...items.map((t) => t.start)), t1 = Math.max(...items.map((t) => t.end));
      const pad = Math.max((t1 - t0) * 0.15, 15 * A.SEC);
      select({ ids: new Set(ids) }, false);
      setView(A.zoom(t0 - pad, t1 + pad, (t0 + t1) / 2, 1, MIN_SPAN, F0, F1));
    };
    g._tip = () => clusterTip(items);
    return g;
  }

  function setView(v) {
    view = v;
    schedule();
  }

  let pending = false;
  function schedule() {
    if (pending) return;
    pending = true;
    requestAnimationFrame(() => { pending = false; drawTimeline(); });
  }

  function zoomBy(factor, c) {
    setView(A.zoom(view[0], view[1], c ?? (view[0] + view[1]) / 2, factor, MIN_SPAN, F0, F1));
  }

  function attachTimelineEvents(svg) {
    const pointers = new Map();
    let drag = null; // {x, view, moved, mark}
    let pinch = null; // {dist, mid}
    const px = (e) => (e.clientX - svg.getBoundingClientRect().left) * (tl.W / svg.clientWidth);

    svg.addEventListener('wheel', (e) => {
      e.preventDefault();
      const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? 400 : 1;
      if (Math.abs(e.deltaX) > Math.abs(e.deltaY)) {
        const dt = (e.deltaX * unit) * (view[1] - view[0]) / tl.W;
        setView(A.clampRange(view[0] + dt, view[1] + dt, F0, F1));
      } else {
        zoomBy(Math.exp(e.deltaY * unit * 0.0015), tl.sc.t(px(e)));
      }
    }, { passive: false });

    svg.addEventListener('pointerdown', (e) => {
      if (e.button !== 0) return;
      pointers.set(e.pointerId, e);
      try { svg.setPointerCapture(e.pointerId); } catch (_) { /* synthetic or already-released pointer */ }
      if (pointers.size === 1) {
        drag = { x: px(e), view: view.slice(), moved: false, mark: e.target.closest('.mark') };
      } else if (pointers.size === 2) {
        const [a, b] = [...pointers.values()];
        pinch = { dist: Math.abs(px(a) - px(b)) || 1 };
        drag = null;
      }
    });
    svg.addEventListener('pointermove', (e) => {
      if (!pointers.has(e.pointerId)) {
        const m = e.target.closest('.mark');
        if (m) showTip(e.clientX, e.clientY, m._tip()); else hideTip();
        return;
      }
      pointers.set(e.pointerId, e);
      if (pinch && pointers.size === 2) {
        const [a, b] = [...pointers.values()];
        const dist = Math.abs(px(a) - px(b)) || 1;
        zoomBy(pinch.dist / dist, tl.sc.t((px(a) + px(b)) / 2));
        pinch.dist = dist;
      } else if (drag) {
        const dx = px(e) - drag.x;
        if (Math.abs(dx) > 3) drag.moved = true;
        if (drag.moved) {
          hideTip();
          svg.classList.add('panning');
          const dt = (-dx * (drag.view[1] - drag.view[0])) / (tl.W - 2 * EDGE);
          setView(A.clampRange(drag.view[0] + dt, drag.view[1] + dt, F0, F1));
        }
      }
    });
    const end = (e) => {
      if (!pointers.has(e.pointerId)) return;
      pointers.delete(e.pointerId);
      svg.classList.remove('panning');
      if (drag && !drag.moved && e.type === 'pointerup' && drag.mark && drag.mark._activate) drag.mark._activate();
      if (pointers.size < 2) pinch = null;
      drag = null;
    };
    svg.addEventListener('pointerup', end);
    svg.addEventListener('pointercancel', end);
    svg.addEventListener('pointerleave', (e) => { if (!pointers.size) hideTip(); });
    svg.addEventListener('keydown', (e) => {
      const m = e.target.closest && e.target.closest('.mark');
      if (m && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); m._activate(); }
    });
    svg.addEventListener('focusin', (e) => {
      const m = e.target.closest('.mark');
      if (!m) return;
      const r = m.getBoundingClientRect();
      showTip(r.right, r.bottom, m._tip());
    });
    svg.addEventListener('focusout', hideTip);
  }

  // ---- tooltip -------------------------------------------------------------

  function txTip(t) {
    const r = t.raw;
    const lines = [[`${A.fmt(t.start)} · ${LANE_NAME[t.lane]}${r.tool ? ' · ' + r.tool : ''}`, 't']];
    lines.push(r.commandline ? [truncate(r.commandline, 160), 'code'] : ['(no command line recorded)']);
    if (r.user) lines.push([`by ${r.user}`]);
    lines.push([summary(r.changes) + (t.end - t.start >= A.SEC ? ` · took ${A.fmtDuration(t.end - t.start)}` : '')]);
    if (r.error) lines.push([`Error: ${truncate(r.error, 160)}`]);
    else if (r.incomplete) lines.push(['Incomplete: interrupted, or dpkg left a package half-done']);
    return lines;
  }

  function clusterTip(items) {
    const n = items.reduce((s, t) => s + t.n, 0);
    const t0 = Math.min(...items.map((t) => t.start)), t1 = Math.max(...items.map((t) => t.end));
    return [
      [`${items.length} transactions · ${plural(n, 'package change')}`, 't'],
      [`${A.fmt(t0, false)} → ${A.fmt(t1, false)}`],
      ['Click to zoom in'],
    ];
  }

  const tip = $('tooltip');
  function showTip(x, y, lines) {
    tip.replaceChildren(...lines.map(([text, kind]) => el('div', { class: kind === 't' ? 't' : null }, kind === 'code' ? el('code', {}, text) : text)));
    tip.hidden = false;
    const w = tip.offsetWidth, h = tip.offsetHeight;
    tip.style.left = Math.max(8, Math.min(x + 14, innerWidth - w - 8)) + 'px';
    tip.style.top = (y + 16 + h > innerHeight - 8 ? y - h - 12 : y + 16) + 'px';
  }
  function hideTip() { tip.hidden = true; }

  // ---- detail panel ----------------------------------------------------------

  function select(sel, redraw = true) {
    selected = sel;
    if (sel.tx != null) showTx(byId.get(sel.tx));
    else showCluster([...sel.ids].map((id) => byId.get(id)));
    if (redraw) schedule();
  }

  function showTx(t) {
    const r = t.raw;
    const meta = el('dl', { class: 'meta' });
    const row = (k, v) => meta.append(el('dt', {}, k), el('dd', {}, v));
    row('When', `${A.fmt(t.start)} → ${A.fmt(t.end)}${t.end > t.start ? ` (${A.fmtDuration(t.end - t.start)})` : ''}`);
    row('Origin', `${LANE_NAME[t.lane]} · ${r.manual ? 'manual' : 'automatic'}`);
    row('Tool', r.tool || 'unknown (no command line recorded, no aptitude log entry)');
    if (r.commandline) row('Command', el('code', {}, r.commandline));
    if (r.user) row('User', r.uid != null ? `${r.user} (uid ${r.uid})` : r.user);
    if (r.error) row('Error', el('span', { class: 'err' }, r.error));
    if (r.incomplete) row('Status', el('span', { class: 'err' }, 'Incomplete: no End-Date was logged, or dpkg left a package half-installed or half-configured'));
    row('Sources', (r.sources || []).map((s) => el('span', { class: 'badge' }, s)).flatMap((b, i) => (i ? [' ', b] : [b])));
    if (r.file) row('Logged in', `${r.file}${r.line ? ':' + r.line : ''}`);

    const groups = el('div', { class: 'groups' });
    for (const a of ACTIONS) {
      const cs = r.changes.filter((c) => c.action === a).sort((x, y) => (x.name < y.name ? -1 : x.name > y.name ? 1 : 0));
      if (!cs.length) continue;
      const item = (c) => {
        const li = el('li', { class: c.requested ? 'req' : null }, el('span', { class: 'name' }, pkgName(c)), ' ', el('span', { class: 'ver' }, version(c)));
        const at = A.wall(c.at);
        if (at - t.start >= A.SEC) li.title = `dpkg: ${A.fmt(at)}`;
        return li;
      };
      const req = cs.filter((c) => !c.dependency), deps = cs.filter((c) => c.dependency);
      const box = el('div', {}, el('h3', {}, swatch(`var(--a-${A.actionGroup(a)})`), `${ACTION_LABEL[a]} (${cs.length})`), el('ul', {}, req.map(item)));
      if (deps.length) box.append(el('details', {}, el('summary', {}, `+${deps.length} ${deps.length === 1 ? 'dependency' : 'dependencies'}`), el('ul', {}, deps.map(item))));
      groups.append(box);
    }
    $('detail').replaceChildren(el('h2', {}, `Transaction #${r.id}`), meta, r.changes.length ? groups : el('p', { class: 'empty' }, 'No package changes recorded.'));
  }

  function showCluster(items) {
    items.sort((a, b) => a.start - b.start);
    const n = items.reduce((s, t) => s + t.n, 0);
    const t0 = items[0].start, t1 = Math.max(...items.map((t) => t.end));
    const tbody = el('tbody');
    for (const t of items.slice(0, 500)) {
      const open = () => select({ tx: t.id });
      tbody.append(el('tr', {
        class: 'row', tabindex: 0,
        onclick: open,
        onkeydown: (e) => { if (e.key === 'Enter') open(); },
      },
      el('td', {}, A.fmt(t.start)),
      el('td', {}, t.raw.commandline ? el('code', {}, truncate(t.raw.commandline, 90)) : el('span', { class: 'muted' }, t.raw.tool ? `${t.raw.tool} (no command line logged)` : 'no command line logged')),
      el('td', {}, summary(t.raw.changes)),
      el('td', {}, t.bad ? el('span', { class: 'err' }, t.raw.error ? 'error' : 'incomplete') : '')));
    }
    $('detail').replaceChildren(
      el('h2', {}, `${items.length} transactions`),
      el('p', { class: 'sub' }, `${A.fmt(t0, false)} → ${A.fmt(t1, false)} · ${plural(n, 'package change')} · ${LANE_NAME[items[0].lane]}`),
      el('table', {}, el('thead', {}, el('tr', {}, el('th', {}, 'Started'), el('th', {}, 'Command'), el('th', {}, 'Changes'), el('th', {}, ''))), tbody),
      items.length > 500 ? el('p', { class: 'sub' }, `Showing the first 500. Zoom in to see the rest.`) : '',
    );
  }

  // ---- wiring --------------------------------------------------------------

  $('zoom-in').addEventListener('click', () => zoomBy(0.5));
  $('zoom-out').addEventListener('click', () => zoomBy(2));
  $('zoom-reset').addEventListener('click', () => setView([F0, F1]));

  drawHeader();
  drawOverview();
  drawTimeline();
  let lastW = 0;
  new ResizeObserver(() => {
    const w = $('timeline').clientWidth;
    if (w === lastW) return;
    lastW = w;
    drawOverview();
    drawTimeline();
  }).observe($('timeline'));
})();
