// Run with: node --test internal/render/assets/core.test.cjs
// (render_test.go runs this too when node is installed.)
'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const A = require('./core.js');

test('wall reads the logged wall clock, ignoring the offset', () => {
  assert.equal(A.wall('2026-05-10T09:02:18+01:00'), Date.UTC(2026, 4, 10, 9, 2, 18));
  assert.equal(A.wall('2026-03-16T12:48:52Z'), Date.UTC(2026, 2, 16, 12, 48, 52));
  assert.equal(A.wall('2026-11-24T09:37:32.36752711Z'), Date.UTC(2026, 10, 24, 9, 37, 32));
  assert.ok(Number.isNaN(A.wall('nope')));
  assert.equal(A.fmt(A.wall('2026-05-10T09:02:18+01:00')), '2026-05-10 09:02:18');
});

test('fmtDuration', () => {
  assert.equal(A.fmtDuration(45e3), '45s');
  assert.equal(A.fmtDuration(96e3), '1m 36s');
  assert.equal(A.fmtDuration(2 * A.HOUR + 5 * A.MIN), '2h 5m');
  assert.equal(A.fmtDuration(3 * A.DAY + 7), '3d');
});

test('ticks: sensible steps and labels at every zoom level', () => {
  const t0 = Date.UTC(2025, 10, 3), year = Date.UTC(2026, 10, 24);
  const y = A.ticks(t0, year, 10);
  assert.ok(y.length >= 4 && y.length <= 10, `year: ${y.length}`);
  assert.ok(y.some((k) => k.label === 'Jan 2026' && k.major));

  const hour = A.ticks(Date.UTC(2026, 0, 1, 10), Date.UTC(2026, 0, 1, 11), 8);
  assert.deepEqual(hour.map((k) => k.label), ['10:00', '10:15', '10:30', '10:45', '11:00']);

  const minute = A.ticks(Date.UTC(2026, 0, 1, 10), Date.UTC(2026, 0, 1, 10, 1), 8);
  assert.equal(minute[1].label, '10:00:10');

  const days = A.ticks(Date.UTC(2026, 0, 30, 18), Date.UTC(2026, 1, 2, 6), 10);
  assert.ok(days.some((k) => k.label === '31 Jan' && k.major), JSON.stringify(days));

  for (const [a, b] of [[0, 1], [t0, t0 + 1e9], [t0, t0 + 20 * 365 * A.DAY]]) {
    const ks = A.ticks(a, b, 12);
    assert.ok(ks.length <= 12, `${ks.length} ticks`);
    for (const k of ks) assert.ok(k.t >= a && k.t <= b);
  }
});

const tx = (sec, lane, n = 1, dur = 0) => ({ start: sec * 1e3, end: (sec + dur) * 1e3, lane, n });

test('cluster merges near marks greedily, left to right', () => {
  const g = A.cluster([{ x0: 0, x1: 10 }, { x0: 14, x1: 20 }, { x0: 40, x1: 50 }, { x0: 52, x1: 60 }], 6);
  assert.deepEqual(g.map((c) => c.items.length), [2, 2]);
  assert.deepEqual([g[0].x0, g[0].x1], [0, 20]);
  assert.equal(A.cluster([], 6).length, 0);
});

test('cluster stops chaining at maxWidth', () => {
  // Daily runs 4 px apart for 60 days: one 240 px pill without a cap.
  const daily = Array.from({ length: 60 }, (_, i) => ({ x0: i * 4, x1: i * 4 + 3 }));
  assert.equal(A.cluster(daily, 6).length, 1);
  const capped = A.cluster(daily, 6, 64);
  assert.ok(capped.length >= 4, `${capped.length} clusters`);
  for (const c of capped) assert.ok(c.x1 - c.x0 <= 64);
  assert.equal(capped.reduce((n, c) => n + c.items.length, 0), 60);
});

test('layout: clusters never cross lanes', () => {
  const txs = [tx(0, 'user-cli'), tx(1, 'unattended'), tx(2, 'user-cli'), tx(3, 'unattended'), tx(4, 'root')];
  const sc = A.scale(0, 3600e3, 0, 800); // an hour: all five overlap on screen
  const lanes = A.layout(txs, sc, 6);
  assert.equal(lanes.size, 3);
  for (const [lane, clusters] of lanes) for (const c of clusters) for (const it of c.items) assert.equal(it.tx.lane, lane);
  assert.equal(lanes.get('user-cli')[0].items.length, 2);
});

test('layout: zooming all the way in resolves clusters', () => {
  // Unattended-upgrade style: runs 4–9 s apart, big marks.
  const txs = [tx(0, 'unattended', 57, 2), tx(4, 'unattended', 6, 6), tx(13, 'unattended', 1, 1)];
  const wide = A.layout(txs, A.scale(-3600e3, 3600e3, 0, 800), 6).get('unattended');
  assert.equal(wide.length, 1);
  const minSpan = A.layout(txs, A.scale(-20e3, 40e3, 0, 800), 6).get('unattended'); // 1-minute span
  assert.equal(minSpan.length, 3);
});

test('layout: skips transactions off screen, keeps long ones that span it', () => {
  const txs = [tx(-7200, 'root'), tx(-100, 'root', 1, 200), tx(7200, 'root')];
  const lanes = A.layout(txs, A.scale(0, 60e3, 0, 800), 6);
  assert.equal(lanes.get('root').length, 1);
});

test('zoom keeps the pointer time fixed and respects limits', () => {
  const [a, b] = A.zoom(0, 1000, 250, 0.5, 10, 0, 1000);
  assert.deepEqual([a, b], [125, 625]);
  assert.deepEqual(A.zoom(0, 1000, 500, 1e-9, 60, 0, 1000), [470, 530]); // min span
  assert.deepEqual(A.zoom(400, 600, 500, 100, 1, 0, 1000), [0, 1000]); // can't exceed full range
  assert.deepEqual(A.clampRange(-50, 50, 0, 1000), [0, 100]);
  assert.deepEqual(A.clampRange(990, 1090, 0, 1000), [900, 1000]);
});

test('bins: per day, or per week over a year', () => {
  const d = (y, m, dd, h = 12) => Date.UTC(y, m, dd, h);
  const txs = [
    { changes: [{ action: 'install', at: d(2026, 0, 1) }, { action: 'purge', at: d(2026, 0, 1) }] },
    { changes: [{ action: 'downgrade', at: d(2026, 0, 2) }] },
  ];
  const days = A.bins(txs, d(2026, 0, 1), d(2026, 1, 1));
  assert.equal(days.size, A.DAY);
  assert.deepEqual(days.bins.map((b) => [b.install, b.upgrade, b.remove]), [[1, 0, 1], [0, 1, 0]]);
  const weeks = A.bins(txs, d(2024, 0, 1), d(2026, 1, 1));
  assert.equal(weeks.size, 7 * A.DAY);
  assert.equal(weeks.bins.length, 1); // Thu 1 Jan and Fri 2 Jan 2026 share a Monday-based week
  assert.equal(new Date(weeks.bins[0].t0).getUTCDay(), 1);
});

test('matches: origin, change type, problems and text filters', () => {
  const raw = { commandline: 'apt install Firefox', tool: 'apt', user: 'testuser', changes: [{ name: 'firefox' }, { name: 'libnss3' }] };
  const tx = { lane: 'user-cli', bad: false, groups: new Set(['install']), hay: A.haystack(raw) };
  const all = () => ({ origins: new Set(['user-cli', 'unattended']), groups: new Set(['install', 'upgrade', 'remove']), text: '', problems: false });
  assert.ok(A.matches(tx, all()));
  assert.ok(!A.matches(tx, { ...all(), origins: new Set(['unattended']) }));
  assert.ok(!A.matches(tx, { ...all(), groups: new Set(['upgrade']) }));
  assert.ok(A.matches(tx, { ...all(), groups: new Set(['upgrade', 'install']) }));
  assert.ok(!A.matches(tx, { ...all(), problems: true }));
  assert.ok(A.matches(tx, { ...all(), text: '  LIBNSS  test ' }), 'every word, any case');
  assert.ok(!A.matches(tx, { ...all(), text: 'libnss chrome' }));
  const empty = { ...tx, groups: new Set() };
  assert.ok(A.matches(empty, all()), 'no changes: shown when all types are on');
  assert.ok(!A.matches(empty, { ...all(), groups: new Set(['install', 'upgrade']) }));
});

test('lifetimes: installed spans from events', () => {
  const ev = (at, action) => ({ at, action });
  assert.deepEqual(A.lifetimes([ev(10, 'install'), ev(20, 'upgrade'), ev(30, 'remove'), ev(31, 'purge'), ev(40, 'install')], false, 0, 100),
    [[10, 30], [40, 100]]);
  assert.deepEqual(A.lifetimes([ev(20, 'upgrade')], true, 0, 100), [[0, 100]], 'present before the logs');
  assert.deepEqual(A.lifetimes([ev(20, 'remove')], true, 0, 100), [[0, 20]]);
  assert.deepEqual(A.lifetimes([], true, 0, 100), [[0, 100]]);
  assert.deepEqual(A.lifetimes([], false, 0, 100), []);
});

test('searchPackages ranks exact, prefix, substring', () => {
  const pk = (name, arch = 'amd64') => ({ name, arch });
  const pkgs = [pk('libfirefox-extra'), pk('firefox-locale-en'), pk('firefox'), pk('libc6'), pk('libc6', 'i386'), pk('firefoxpwa')];
  assert.deepEqual(A.searchPackages(pkgs, 'Firefox', 10).map((p) => p.name), ['firefox', 'firefoxpwa', 'firefox-locale-en', 'libfirefox-extra']);
  assert.deepEqual(A.searchPackages(pkgs, 'firefox', 2).length, 2);
  assert.deepEqual(A.searchPackages(pkgs, 'libc6:i3', 10), [pk('libc6', 'i386')]);
  assert.deepEqual(A.searchPackages(pkgs, '  ', 10), []);
});

test('sortBy is stable, handles numbers, strings and missing values', () => {
  const rows = [{ k: 2, s: 'b' }, { k: 1, s: 'a' }, { k: 2, s: 'c' }, { k: undefined, s: 'd' }];
  assert.deepEqual(A.sortBy(rows, (r) => r.k, 1).map((r) => r.s), ['d', 'a', 'b', 'c']);
  assert.deepEqual(A.sortBy(rows, (r) => r.k, -1).map((r) => r.s), ['b', 'c', 'a', 'd']);
  assert.deepEqual(A.sortBy(rows, (r) => r.s, -1).map((r) => r.s), ['d', 'c', 'b', 'a']);
});
