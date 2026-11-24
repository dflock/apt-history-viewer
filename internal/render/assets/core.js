// core.js: pure functions for the report (no DOM), so they can be tested with
// `node --test` (see core.test.cjs). Exposed as window.AHV in the page.
//
// Times: every timestamp is the logged machine's local wall-clock time. The
// JSON carries it with an offset ("2026-05-10T09:02:18+01:00"); we read the
// wall-clock part as if it were UTC ("wall ms") and do all arithmetic and
// formatting in UTC. The report then shows the machine's own clock, whatever
// the viewer's time zone, and needs no time zone database.
'use strict';
(function (root) {
  const SEC = 1e3, MIN = 60 * SEC, HOUR = 60 * MIN, DAY = 24 * HOUR, WEEK = 7 * DAY;
  const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

  /** "2026-05-10T09:02:18+01:00" → wall-clock ms (NaN if unparsable). */
  function wall(s) {
    const m = /^(\d{4})-(\d\d)-(\d\d)T(\d\d):(\d\d):(\d\d)/.exec(s || '');
    return m ? Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +m[6]) : NaN;
  }

  const pad = (n) => String(n).padStart(2, '0');

  /** wall ms → "2026-05-10 09:02:18" (or without seconds). */
  function fmt(ms, seconds = true) {
    const d = new Date(ms);
    const s = `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())} ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`;
    return seconds ? `${s}:${pad(d.getUTCSeconds())}` : s;
  }

  function fmtDate(ms) {
    const d = new Date(ms);
    return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
  }

  /** Duration in ms → "1m 36s", "2h 5m", "3d 4h", "45s". */
  function fmtDuration(ms) {
    const s = Math.round(ms / SEC);
    if (s < 60) return `${s}s`;
    const parts = [[Math.floor(s / 86400), 'd'], [Math.floor(s / 3600) % 24, 'h'], [Math.floor(s / 60) % 60, 'm'], [s % 60, 's']];
    const i = parts.findIndex(([n]) => n > 0);
    return parts.slice(i, i + 2).filter(([n]) => n > 0).map(([n, u]) => n + u).join(' ');
  }

  /** Linear time → pixel scale over domain [d0, d1] and range [r0, r1]. */
  function scale(d0, d1, r0, r1) {
    const k = (r1 - r0) / ((d1 - d0) || 1);
    return { d0, d1, r0, r1, x: (t) => r0 + (t - d0) * k, t: (x) => d0 + (x - r0) / k };
  }

  /** Change actions → the three groups the overview and totals use. */
  function actionGroup(a) {
    if (a === 'install' || a === 'reinstall') return 'install';
    if (a === 'upgrade' || a === 'downgrade') return 'upgrade';
    return 'remove'; // remove, purge
  }

  // Tick steps, smallest first. Fixed steps in ms, then calendar steps.
  const STEPS = [
    [10 * SEC], [30 * SEC], [MIN], [5 * MIN], [15 * MIN], [30 * MIN], [HOUR], [3 * HOUR], [6 * HOUR], [12 * HOUR],
    [DAY], [2 * DAY], [WEEK],
    ['month', 1], ['month', 3], ['month', 6], ['year', 1], ['year', 2], ['year', 5], ['year', 10],
  ];
  const approx = ([a, n]) => (a === 'month' ? n * 30.44 * DAY : a === 'year' ? n * 365.25 * DAY : a);
  const MONDAY = 4 * DAY; // 1970-01-05 was a Monday

  /** Axis ticks for [d0, d1], at most about maxTicks: [{t, label, major}]. */
  function ticks(d0, d1, maxTicks) {
    const span = d1 - d0;
    const step = STEPS.find((s) => span / approx(s) <= maxTicks) || STEPS[STEPS.length - 1];
    const out = [];
    if (typeof step[0] === 'number') {
      const ms = step[0];
      const off = ms === WEEK ? MONDAY : 0;
      for (let t = Math.ceil((d0 - off) / ms) * ms + off; t <= d1; t += ms) {
        const d = new Date(t);
        const midnight = t % DAY === 0;
        let label;
        if (ms < DAY) label = midnight ? `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]}` : `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}${ms < MIN ? ':' + pad(d.getUTCSeconds()) : ''}`;
        else label = `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]}`;
        out.push({ t, label, major: ms < DAY ? midnight : d.getUTCDate() === 1 });
      }
    } else {
      const [unit, n] = step;
      const s = new Date(d0);
      let y = s.getUTCFullYear(), m = unit === 'year' ? 0 : s.getUTCMonth();
      if (unit === 'year') y = Math.floor(y / n) * n; else m = Math.floor(m / n) * n;
      for (;;) {
        const t = Date.UTC(y, m, 1);
        if (t > d1) break;
        if (t >= d0) out.push({ t, label: unit === 'year' || m === 0 ? (unit === 'year' ? `${y}` : `${MONTHS[m]} ${y}`) : MONTHS[m], major: m === 0 });
        if (unit === 'year') y += n; else { m += n; if (m >= 12) { y += Math.floor(m / 12); m %= 12; } }
      }
    }
    return out;
  }

  /** Radius of a transaction mark with n changes: grows with √n, capped. */
  const radius = (n) => Math.min(14, 3.5 + 1.5 * Math.sqrt(Math.max(1, n)));

  /**
   * Greedy left-to-right clustering of marks in one lane (DESIGN §6).
   * items: [{x0, x1, ...}] on-screen extents. Marks closer than gap px merge,
   * but a cluster stops growing at maxWidth px: otherwise daily runs chain into
   * one pill spanning months, hiding how dense each part of it is.
   * Returns [{x0, x1, items}], one per cluster or single mark.
   */
  function cluster(items, gap, maxWidth = Infinity) {
    const sorted = items.slice().sort((a, b) => a.x0 - b.x0);
    const out = [];
    let cur = null;
    for (const it of sorted) {
      if (cur && it.x0 - cur.x1 < gap && it.x1 - cur.x0 <= maxWidth) {
        cur.items.push(it);
        cur.x1 = Math.max(cur.x1, it.x1);
      } else {
        cur = { x0: it.x0, x1: it.x1, items: [it] };
        out.push(cur);
      }
    }
    return out;
  }

  /**
   * Lays out the visible transactions: per lane, marks clustered at the current
   * zoom. Lanes are separate, so a cluster never mixes origins.
   * txs: [{start, end, n, lane}] (wall ms); sc: scale. Returns Map lane → clusters,
   * where each item is {x0, x1, cx, r, tx}.
   */
  function layout(txs, sc, gap, maxWidth) {
    const lanes = new Map();
    const lo = sc.t(sc.r0 - 20), hi = sc.t(sc.r1 + 20);
    for (const tx of txs) {
      if (tx.end < lo || tx.start > hi) continue;
      const r = radius(tx.n), cx = sc.x(tx.start);
      const item = { x0: cx - r, x1: Math.max(cx + r, sc.x(tx.end)), cx, r, tx };
      if (!lanes.has(tx.lane)) lanes.set(tx.lane, []);
      lanes.get(tx.lane).push(item);
    }
    for (const [lane, items] of lanes) lanes.set(lane, cluster(items, gap, maxWidth));
    return lanes;
  }

  /**
   * Zoom [d0, d1] by factor around time c, keeping c at the same screen spot,
   * clamped to a minimum span and to [f0, f1]. Returns [d0, d1].
   */
  function zoom(d0, d1, c, factor, minSpan, f0, f1) {
    const span = Math.min(f1 - f0, Math.max(minSpan, (d1 - d0) * factor));
    const k = (c - d0) / ((d1 - d0) || 1);
    return clampRange(c - k * span, c - k * span + span, f0, f1);
  }

  /** Shift [d0, d1] so it lies inside [f0, f1], keeping its span. */
  function clampRange(d0, d1, f0, f1) {
    const span = Math.min(d1 - d0, f1 - f0);
    if (d0 < f0) return [f0, f0 + span];
    if (d0 + span > f1) return [f1 - span, f1];
    return [d0, d0 + span];
  }

  /**
   * Overview bars: change counts per day, or per week when the range is
   * longer than a year. Returns {size, bins: [{t0, t1, install, upgrade, remove}]}.
   */
  function bins(txs, f0, f1) {
    const size = f1 - f0 > 366 * DAY ? WEEK : DAY;
    const off = size === WEEK ? MONDAY : 0;
    const first = Math.floor((f0 - off) / size) * size + off;
    const map = new Map();
    for (const tx of txs) {
      for (const c of tx.changes) {
        const t0 = Math.floor((c.at - off) / size) * size + off;
        let b = map.get(t0);
        if (!b) map.set(t0, (b = { t0, t1: t0 + size, install: 0, upgrade: 0, remove: 0 }));
        b[actionGroup(c.action)]++;
      }
    }
    return { size, first, bins: [...map.values()].sort((a, b) => a.t0 - b.t0) };
  }

  const AHV = { SEC, MIN, HOUR, DAY, wall, fmt, fmtDate, fmtDuration, scale, actionGroup, ticks, radius, cluster, layout, zoom, clampRange, bins };
  if (typeof module === 'object' && module.exports) module.exports = AHV;
  else root.AHV = AHV;
})(this);
