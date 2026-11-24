# Apt & dpkg History Viewer — Design

Status: draft, 2026-11-24. Expands `plan.md`.

## 1. Goal

A single static Go binary, `apt-history-viewer`, that reads the apt and dpkg logs
on the local machine and writes **one self-contained HTML file** (inline SVG, CSS
and JS, no network access needed) showing the system's package history:
installs, upgrades, downgrades, reinstalls, removals and purges, distinguishing
manual from automatic activity.

## 2. Review of `plan.md`

The plan is a good one-paragraph brief. Gaps worth deciding up front:

| # | Gap | Decision |
|---|-----|----------|
| 1 | **"Manual" is ambiguous.** It can mean *who started the transaction* (you typing `apt install` vs `unattended-upgrade`) or *why a package was installed* (named explicitly vs pulled in as a dependency). | Model both, as two independent axes: transaction **Origin** and per-package **Requested/Dependency**. See §5. |
| 2 | Only installs and upgrades are mentioned. | Also show remove, purge, downgrade, reinstall. On this machine there are 124 `Remove:` stanzas vs 41 `Install:`. |
| 3 | **History is short.** logrotate keeps 12 rotations, so this machine's logs start 2025-11-03. Anything older is invisible. | State the visible time range prominently. Add a "baseline" of currently installed packages from `/var/lib/dpkg/status` so pre-history packages appear as "present before logs begin". Later, etckeeper history can reach further back (§3.6). |
| 4 | `apt-mark manual/auto` is not logged anywhere. | Can't be shown historically. Show *current* manual/auto state from `/var/lib/apt/extended_states` in the package view, and say so. |
| 5 | apt and dpkg disagree on architecture: history.log says `mingw-w64-common:amd64`, dpkg.log says `mingw-w64-common:all`. | Join on package name, then accept arch equal or dpkg arch `all`. |
| 6 | Timestamps are local time with no zone. | Treat as naive local time, record the generating machine's TZ in the output, and keep the original file order as a tie-break around DST changes. |
| 7 | "Single static binary": the *output* should be standalone too. | Output HTML embeds everything, with no CDN, so it works offline and can be emailed or archived. |
| 8 | Scope of "package history". | apt/dpkg only. Snap, Flatpak and pip are out of scope (say so in README). |
| 9 | Privacy: output contains usernames and full command lines. | Fine by default. Add `--redact` to strip `Requested-By` and command-line arguments for sharing. |
| 10 | **`Requested-By` without `Commandline` is ambiguous.** First guessed as PackageKit/GUI, but on this machine all 60 such stanzas since 2026-05-03 match an aptitude report 0–1 s before their `Start-Date`. aptitude omits `Commandline`. | Identify the tool from `/var/log/aptitude*` (§3.7) and, when available, etckeeper's `made by "…"` (§3.6). Only fall back to "user, tool unknown". |

## 3. Data sources

All are world-readable on stock Debian/Ubuntu, so root is not required.
(`term.log` is root-only and not needed.) The paths below are relative to an
**input root**: `/` by default, or a directory or tarball from another machine (§3.5).

### 3.1 `/var/log/apt/history.log*` — primary source

Blank-line-separated stanzas:

```
Start-Date: 2026-11-03  13:11:39
Commandline: apt install xdotool xvfb
Requested-By: testuser (1000)
Install: libxdo3:amd64 (1:3.20160805.1-5build1, automatic), xdotool:amd64 (1:3.20160805.1-5build1)
End-Date: 2026-11-03  13:11:49
```

* Fields: `Start-Date`, `End-Date`, `Commandline`, `Requested-By`, `Error`, and
  action lists `Install`, `Upgrade`, `Downgrade`, `Reinstall`, `Remove`, `Purge`.
* Note the **two spaces** between date and time.
* Item grammar: `name:arch (ver[, ver2][, automatic])`, comma-separated. Versions can
  contain `:` `~` `+`, but never `,` `(` `)`. Upgrade/Downgrade items carry `(old, new)`.
  Split on `), ` rather than `, `.
* `, automatic` on an Install item = pulled in as a dependency.
* Observed on this machine (651 stanzas):
  * 439 `/usr/bin/unattended-upgrade`
  * 120 **no `Commandline`, has `Requested-By`**: **aptitude** (verified: every one
    since the aptitude logs begin has an aptitude report 0–1 s before it). PackageKit
    produces the same shape elsewhere, so this alone does not identify the tool (§3.7).
  * 86 `apt …` and 6 `synaptic` with `Requested-By`
* The last stanza may be incomplete (no `End-Date`) if apt is running or crashed.

### 3.2 `/var/log/dpkg.log*` — precise, and catches non-apt activity

```
2026-11-03 13:11:39 startup archives unpack
2026-11-03 13:11:39 install binutils-mingw-w64-i686:amd64 <none> 2.41.90...
2026-11-03 13:11:39 upgrade foo:amd64 1.0-1 1.0-2
2026-11-03 13:11:39 remove foo:amd64 1.0-2 <none>
2026-11-03 13:11:39 status unpacked foo:amd64 1.0-2
```

* Keep: `startup`, `install`, `upgrade`, `remove`, `purge`, and `status` lines only to
  detect failures (a package left `half-configured` / `half-installed` at the end of a run).
* Drop: `configure`, `trigproc`, `conffile`, other `status` lines. They are about 85% of
  the volume (32,904 lines here).
* A dpkg **run** = lines from one `startup` to the next.

### 3.3 State files — current snapshot, for the baseline

* `/var/lib/dpkg/status`: currently installed packages and versions.
* `/var/lib/apt/extended_states`: `Auto-Installed: 1` = currently automatic.
* Optional: earliest timestamp in `/var/log/installer/` ≈ OS install date.

### 3.4 Discovering rotated files

Glob `history.log*` / `dpkg.log*`. Handle plain and `.gz` (stdlib). Don't rely on
rotation numbering (`dateext` setups name files `history.log-20251103.gz`):
read each file's first timestamp and sort by it. `.xz` / `.zst` are skipped with a
warning in v1 (pure-Go decoders exist if ever needed, and stay static).

### 3.5 Inputs from other machines: directories and tarballs

`--input` accepts:

* **A directory** holding a copy of the other machine's filesystem layout
  (`<dir>/var/log/apt/…`, `<dir>/var/log/dpkg.log*`, `<dir>/var/lib/dpkg/status`, …).
* **A flat directory** of log files (`history.log*`, `dpkg.log*`, and optionally
  `status`, `extended_states`) as someone might `scp` them.
* **A tarball**: `.tar`, `.tar.gz` / `.tgz`, in either of the layouts above, at any
  path depth (e.g. `myhost/var/log/apt/history.log`).

Layout is detected by looking for the known filenames anywhere in the input and
using the shallowest match of each. Collection is easy, so the README documents a one-liner:

```
tar czf myhost-pkglogs.tgz /var/log/apt/history.log* /var/log/dpkg.log* \
    /var/log/aptitude* /var/lib/dpkg/status /var/lib/apt/extended_states \
    /etc/hostname /etc/timezone
```

Implementation: every source is exposed as an `fs.FS`. Directories use `os.DirFS`.
Tarballs are read in one streaming pass (`archive/tar` + `compress/gzip`, stdlib), and
only members whose basename matches a known pattern are kept, in an in-memory
`fstest.MapFS`-style FS. Nothing is extracted to disk, so path traversal isn't possible.
Cap each member at 256 MiB and skip non-regular entries (symlinks, devices).

Host and timezone for foreign inputs come from `etc/hostname` and `etc/timezone` when
present. Otherwise host = input basename and TZ = "unknown (local time of source machine)".
`--input` may be repeated to make several reports in one go (one HTML per input).
The collection one-liner also includes `/var/log/aptitude*` (§3.7).

### 3.6 etckeeper history (Milestone 7)

[etckeeper](https://etckeeper.branchable.com/) commits `/etc` to version control
before and after every apt run, plus a daily autocommit. Its commit messages
normally include the package-list diff:

```
committing changes in /etc made by "apt install xdotool xvfb"

Package changes:
+libxdo3 1:3.20160805.1-5build1 amd64
+xdotool 1:3.20160805.1-5build1 amd64
```

**Verified against this machine.** Fixture: `testdata/etckeeper/git-log-default.txt`
(anonymised copy of `sudo git -C /etc log -5`), cross-checked against history.log and dpkg.log.

Commit kinds seen:

| Subject | Body | Meaning |
|---|---|---|
| `saving uncommitted changes in /etc prior to apt run` | empty here | Pre-run hook. Timestamp = history.log `Start-Date` (11:58:30 = 11:58:30) or 1 s before (06:21:19 → 06:21:20). |
| `committing changes in /etc made by "<cmd>"` | `Package changes:` block, optionally preceded by a `Packages with configuration changes:` block | Post-run hook. Timestamp = `End-Date` + 0–1 s (06:21:30 → 06:21:30, 12:00:06 → 12:00:07). |
| `daily autocommit` | empty here | Daily timer. No package list seen, so it carries no package history. |
| `Initial commit` | none | Created by `etckeeper init`: a full snapshot of `/etc`, with no package list. Its date marks when etckeeper history begins (`EtckeeperFrom`). Never treat its thousands of added files as `EtcFiles` of a transaction. |

Whole-history check on this machine (2026-11-24, 180 commits): 73 `daily autocommit`,
58 pre-run, 48 post-run `made by "…"`, 1 `Initial commit`; no `after apt run` variant.
0 autocommits contain `Package changes:`. Compared with 651 apt transactions in the past
year alone, only 48 post-run commits over etckeeper's whole history confirms how sparse it is (below).

Format details:

* **Two `-`/`+` blocks can appear.** `Packages with configuration changes:` lists packages
  whose conffiles changed. It is *not* the package-change list, and the same packages
  reappear under `Package changes:`. Parse by section header, and keep the first block only
  as a per-package "touched config" flag.
* **`-`/`+` lines are not adjacent pairs.** In `e01290c` the libc block lists all eight `-` lines,
  then all eight `+` lines. In the hplip commit, `-hplip`, `-hplip-data` come before both `+` lines.
  Pair by (name, arch) across the whole block, never by position.
* Line format `<±><name> <version> <arch>`. Arch is part of the key (`libc6 amd64` and
  `libc6 i386` both appear), and arch `all` is used like dpkg.log, so use the §5.4 normalisation.
* `+` with no matching `-` = install, `-` with no `+` = removal. There's **no dependency/automatic
  marker** (history.log says `automatic` for `libdistro-info-perl`, etckeeper doesn't).
* `made by "<cmd>"` is the *process* command line, which can differ from history.log's
  `Commandline`: etckeeper gives `/usr/bin/python3 /usr/bin/unattended-upgrade`, history.log
  gives `/usr/bin/unattended-upgrade`. For aptitude, history.log has no Commandline at all,
  and etckeeper supplies it (`aptitude --full-resolver -q safe-upgrade`).
* Author = the sudo user's git identity, **including their email address**. Keep only the
  name as a `User` fallback, never put the email in the report, and drop the name under `--redact`.
* Confirmed from etckeeper's own hook scripts (`/etc/etckeeper/daily`,
  `post-install.d/50vcs-commit`, `/etc/apt/apt.conf.d/05etckeeper`):
  * The package list is `diff -U0` of the installed-package list from before and after the run,
    which is why each changed block of the diff lists all its `-` lines, then all its `+` lines.
    `Packages with configuration changes:` = the subset whose packages own a changed `/etc` file.
  * It is written **only by the post-install hook**, which runs only from apt's
    `DPkg::Pre-/Post-Invoke`, and only if `/etc` is unclean.
  * **Daily autocommits never carry a package list** (`etckeeper commit "daily autocommit"`).
  * **Plain `dpkg -i` produces no etckeeper commit.** Its `/etc` changes are swept into the
    next autocommit or pre-run commit with no package list, so dpkg.log is the only record.
  * Subject variants: `made by "<cmd>"`, or `committing changes in /etc after <apt> run`
    when the parent command can't be found. If the pre-run package list is missing or in a
    different format, the commit has **no** package list.

**Important limitation: etckeeper's package history is sparse.** etckeeper only commits when
`/etc` has changed, and each post-run diff covers only that apt run. On 2026-11-12 unattended-upgrade
did three apt runs (06:21:20 gstreamer, 06:21:24 hplip, 06:21:33 python3-tornado). Only the hplip
run touched `/etc`, so **only hplip appears in etckeeper**. The gstreamer and tornado upgrades are
absent, and no later commit picks them up. So before the apt log window, etckeeper shows
*only runs that changed /etc*: typically new services, packages with conffiles, libc/locales,
and kernels. It does not show every upgrade.

**Why it's still worth it**: git history is never rotated, so on long-lived machines it reaches
years past the apt log window (gap #3), even if partial. It also brings commit timestamps *with*
a timezone, the `/etc` files each run touched (the most useful "what did that upgrade change?"
detail), and the real command line for aptitude runs. In the report, the etckeeper-only period
must be labelled **"partial: only package runs that changed /etc"**, not presented as complete history.

**On this machine**: etckeeper 1.18.20, VCS=git, active since about 2025-08-16, so only
about 2.5 months before the apt logs begin. The gain here is small; on older machines it is large.

**Scope**: git backend only (skip hg, brz and darcs, which are rare). `/etc/.git` is mode 0700,
so this needs root (locally) or a pre-exported log (remote).

**Approach: parse plain `git log` text, no git library.** This keeps the binary dependency-free
(go-git is large) and makes local and remote inputs work the same way. Accept git's **default
output format**: exactly what users naturally run, and what the fixture is. Optionally
`--name-status` output is accepted as well:

* Records start at an unindented `commit <sha>` line, followed by `Author:` and `Date:` headers
  (`Date:   Fri Nov 13 12:00:07 2026 +0000`, Go layout `Mon Jan _2 15:04:05 2006 -0700`;
  also accept `--date=iso-strict`). The message is indented 4 spaces, so a body line can never be
  mistaken for a header. Name-status lines (`M\tapt/sources.list`) are unindented, tab-separated,
  after the message.
* Ignore any decoration after the sha (`(HEAD -> master)`).
* **Local**: if `/etc/.git` is readable and `git` is on `PATH`, run
  `git -C /etc --no-pager log --name-status --no-color` (etckeeper depends on git, so it is
  present wherever etckeeper is). If `/etc/.git` is unreadable, add a warning: "etckeeper history
  found but needs root; re-run with sudo or use --etckeeper-log".
* **Remote**: the collection one-liner (§3.5) gains an optional step, picked up by `internal/source`
  like any other known file:

  ```
  sudo git -C /etc --no-pager log --name-status > etckeeper.log   # add to the tar
  ```

### 3.7 aptitude log (v1, Milestone 1)

`/var/log/aptitude` (plus rotations, `.N.gz`) has one report per run:

```
Aptitude 0.8.13: log report
Wed, Nov  4 2026 21:32:15 +0000
…
========================================
[UPGRADE] apt:amd64 2.8.3 -> 2.8.4ubuntu0.1
…
========================================
Log complete.
```

Its timestamp is 0–1 s before the history.log `Start-Date` (60 runs on this machine: 24 at 0 s,
36 at 1 s), so match with a window of `Start − 2s … Start`.
Use it **only to identify the tool**: a `Requested-By`, no-`Commandline` stanza with an
aptitude report within 2 s before `Start` → `Tool = "aptitude"`. Ignore its package lines,
which only list *intended* actions, and history.log/dpkg.log are authoritative. It does not
record the command line (etckeeper does, §3.6). The parse is tiny and it fixes a real
misclassification, so it ships in v1.

**Parsing**: from each commit, take the timestamp (with offset), the subject (to recover
the triggering command from `made by "…"`, or classify `daily autocommit` and
`saving uncommitted changes … prior to apt run`), the `Package changes:` block
(`+`/`-` lines → per-package adds and removes; a `-` and `+` for the same (name, arch)
anywhere in the block → upgrade or downgrade, decided by Debian version comparison), and the changed
paths from `--name-status`.

## 4. Data model

```go
type Origin string
const (
    OriginUserCLI    Origin = "user-cli"    // Requested-By + Commandline (apt, apt-get, aptitude)
    OriginUserGUI    Origin = "user-gui"    // synaptic, or PackageKit (Requested-By, no Commandline, not aptitude)
    OriginUnattended Origin = "unattended"  // Commandline contains unattended-upgrade
    OriginRoot       Origin = "root"        // Commandline but no Requested-By (root shell, scripts, cloud-init)
    OriginDpkg       Origin = "dpkg"        // dpkg run with no matching apt transaction (dpkg -i, etc.)
    OriginEtckeeper  Origin = "etckeeper"   // (M7) package changes only seen in etckeeper, e.g. before the log window
)

type Action string // install upgrade downgrade reinstall remove purge

type Transaction struct {
    ID          int
    Start, End  time.Time
    Origin      Origin
    Commandline string // from history.log, else etckeeper `made by "…"` (M7)
    Tool        string // apt, apt-get, aptitude, synaptic, packagekit, unattended-upgrade, dpkg, "" = unknown
    User        string // from Requested-By
    UID         int
    Error       string
    Incomplete  bool   // missing End-Date or dpkg left packages half-done
    Changes     []Change
    Sources     []string // which logs evidence this: "apt", "dpkg", "etckeeper" (M7)
    EtcFiles    []EtcFile // /etc paths changed, from etckeeper (M7)
}

type EtcFile struct { Status, Path string } // Status: A/M/D/R from --name-status

type Change struct {
    Name, Arch  string
    Action      Action
    OldVersion  string
    NewVersion  string
    Dependency  bool      // apt ", automatic" marker
    At          time.Time // precise time from dpkg.log when matched, else Transaction.Start
}

type Package struct {           // derived, for the per-package view
    Name, Arch    string
    Installed     string        // current version, "" if not installed
    AutoNow       bool          // from extended_states
    PreHistory    bool          // installed, but no install event in the log window
    Events        []EventRef    // (transaction ID, change index)
}

type Report struct {
    Host, TZ        string
    Generated       time.Time
    From, To        time.Time   // visible log window
    EtckeeperFrom   time.Time   // (M7) earliest etckeeper commit, if any
    Transactions    []Transaction
    Packages        []Package
    Warnings        []string    // skipped files, parse errors (with file:line)
}
```

## 5. Correlation and classification

1. Parse all history.log stanzas → `Transaction`s (apt-sourced).
2. Parse dpkg.log into runs.
3. **Match runs to transactions**: a run belongs to transaction T if its first line
   falls in `[T.Start − 2s, T.End + 2s]`. One apt transaction often owns several runs
   (download/unpack/configure phases). Use a sweep over both time-sorted lists, O(n).
4. For matched runs, enrich each `Change` with the dpkg timestamp (`At`). Join key:
   name + (arch equal or dpkg arch `all`).
5. **Unmatched runs** become synthetic `Transaction{Origin: OriginDpkg}`, with
   changes built from their `install/upgrade/remove/purge` lines.
6. Origin rules, in order: `unattended-upgrade` in Commandline → unattended;
   `synaptic` → user-gui; Requested-By and no Commandline → aptitude report 0–2 s before
   `Start` (or etckeeper `made by "aptitude…"`) → user-cli with `Tool=aptitude`, otherwise
   user-gui with `Tool=""` (probably PackageKit, shown as "user, tool unknown");
   Requested-By → user-cli; Commandline only → root; else dpkg.
7. Per-package "requested" = action is not Install, or Install without `automatic`.
   For user-cli, also cross-check against names on the Commandline.
8. Baseline: packages in `dpkg/status` with no install event in the window → `PreHistory`.
9. **etckeeper (M7)**: match commits to apt transactions by time, confirmed by packages:
   * Post-run commit C ↔ the transaction T with the latest `End ≤ C.time + 2s` and
     `C.time − End ≤ 60s` (observed 0–1 s), **and** whose package set overlaps C's
     `Package changes`. The overlap check matters: unattended-upgrade does several runs a few seconds
     apart, and only some get commits. Don't require command equality (`python3` wrapper differs).
     Compare commands only as a tie-break, by basename of the first non-interpreter word.
   * Pre-run commits: use only as a start-time hint for etckeeper-only transactions. Otherwise ignore them.
   * Matched → add `"etckeeper"` to `Sources`, attach `EtcFiles` and config-changed flags, fill
     a missing `Commandline` from `made by` (aptitude) and re-run step 6, and ignore its package list.
   * Unmatched post-run commits with package changes become transactions. Origin comes from the
     `made by` command with the step 6 rules (`unattended-upgrade` → unattended, `aptitude`/`apt` →
     user-cli or root depending on author presence, else `OriginEtckeeper`). Time span: from the
     preceding pre-run commit (if within 1 h) to the commit time.
   * `Initial commit`: set `Report.EtckeeperFrom`, nothing else.
   * Daily autocommits and pre-run commits never carry package lists (confirmed from the hook
     source), so use them only for `EtcFiles` and timing hints, never for package history.
   * Packages first seen in etckeeper history are no longer `PreHistory`.

### 5.10 Details settled while building M2

* **Arch names**: a package that dpkg (status or dpkg.log) reports as `Architecture: all` is
  keyed `name:all` everywhere, though history.log and extended_states write it with the
  native arch. A package that changed between `any` and `all` (linux-firmware did,
  amd64 → all) is merged under `all`.
* **Arch switches and half-done states**: during such a switch dpkg leaves the old instance
  `half-installed`. Final states are therefore merged per normalised name, and within one run
  a good state beats a broken one. `Incomplete` means the last state is `half-installed`,
  `half-configured` or `unpacked`; `triggers-pending` doesn't count.
* **Tie-break**: a run inside `[Start, End]` beats one only within the 2 s slack. When two
  transactions are equally close, the later one wins, since apt starts dpkg after `Start-Date`.
* **Missing End-Date**: the matching window stays open until the next transaction starts
  (at most 1 h). `End` becomes the latest matched run's end, or `Start` if there is none.
* **dpkg-only transactions**: unmatched runs ≤ 2 s apart are one invocation (`dpkg -i` logs an
  unpack run and a configure run). Groups with no install/upgrade/remove/purge (trigger or
  configure-only runs, a `dpkg -i` that did nothing) are dropped. Upgrades with old = new
  become `reinstall`. A remove then purge of the same package becomes one `purge`.
  Downgrades show as `upgrade` until `debversion` exists (M7).
* **Named**: for apt/apt-get/aptitude command lines, arguments after the verb (stripped of
  `=ver`, `/release`, `:arch` and aptitude's `+ - _ &` suffixes) mark changes `Named`.
* **`Report.AptFrom` / `DpkgFrom`**: where each log starts. dpkg runs before `AptFrom` look
  like dpkg-only transactions, but they may be apt runs whose history.log entry has rotated
  away. The view should shade that period.
* **On this machine**: 651 transactions. All 1,459 dpkg runs match an apt transaction, and all
  3,004 apt changes find their dpkg event. 0 incomplete. 2,779 packages, 2,546 of them
  PreHistory. 0.07 s, 19 MB RAM, 1.7 MB JSON.

## 6. Output: the HTML report

One file, default `./apt-history.html`. Built from an `embed`ded `html/template`,
with the `Report` serialised as JSON in `<script type="application/json" id="data">`.
`encoding/json` escapes `<>&`, so command lines can't break out of the tag.
Vanilla JS (no framework, no CDN) renders SVG client-side. All strings are inserted via
`textContent`, never `innerHTML`.

Why render in the browser and not in Go: zooming, filtering and tooltips are easy
in the browser, and the JSON is small (this machine ≈ 650 transactions → a few hundred KB).

### Layout (top to bottom)

1. **Header**: host, visible window ("logs cover 2025-11-03 → 2026-11-24"; with M7,
   also "etckeeper extends back to 2025-08-16"), totals by action and origin, warnings.
2. **Overview strip**: one stacked bar per day (or week, when range > 1 year):
   installs / upgrades / removes. Drag to brush a range, which zooms everything below.
3. **Transaction timeline**: x = time, one horizontal lane per Origin
   (User CLI · User GUI · Root · dpkg direct · Unattended).
   Each transaction is a mark sized by √(package count).
   * **Manual** origins: filled diamond. **Automatic** (unattended): hollow circle.
     Shape and colour both, so it reads without colour.
   * Errors / incomplete: red outline.
   * Hover → tooltip (time, command line, user, counts). Click → detail panel.
   * **Clustering at low zoom**: after each zoom/pan, marks in the same lane whose
     on-screen extents would overlap (< ~6 px apart) are merged greedily left-to-right into a
     **cluster mark**: a pill spanning the cluster's time extent with a count badge.
     Lanes are per Origin, so a cluster never mixes manual and automatic.
     The cluster tooltip summarises it (N transactions, M packages, time span).
     Clicking it zooms to its extent. The detail panel lists its transactions.
   * **Zoom all the way in**: wheel/pinch/brush zoom with no fixed limit beyond a
     1-minute minimum visible span, so any cluster can be resolved down to individual
     transactions, even ones seconds apart. A "reset zoom" button returns to the full range.
   * Clustering is a pure view-time computation in JS over the visible transactions
     (O(n) per redraw), so the data in the file is never aggregated or lost.
4. **Detail panel**: the transaction's changes grouped by action. Requested packages
   in bold, dependencies collapsed under "+N dependencies". Old → new versions.
   With M7: a collapsible "/etc files changed" list, and source badges (apt / dpkg / etckeeper).
   The timeline shades the etckeeper-only period and labels it "partial: only package runs
   that changed /etc", since it has no dependency/requested distinction and misses runs that
   didn't touch /etc (§3.6).
5. **Package view**: search box. Selecting a package draws its own lane: version
   history over time (install ▲, upgrade ●, remove ▼), current version, auto/manual now,
   "present before logs begin" marker.
6. **Transactions table**: sortable, filterable by origin/action/text. Mirrors the brush.

Light and dark themes via `prefers-color-scheme`. Works at narrow widths (lanes stack).

### 6.1 Details settled while building M3

* **Files**: `internal/render/assets/`: `report.tmpl`, `app.css`, `core.js` (pure functions:
  time, scales, ticks, clustering, zoom, overview bins) and `app.js` (DOM). All are inlined into
  the one HTML file. `core.test.cjs` holds the `node --test` tests, and `go test` runs them
  (plus `node --check` on the page script) when node is installed. They are skipped otherwise.
* **Time display**: the page shows the logged machine's wall-clock time. JS reads the local part
  of each RFC 3339 timestamp as if it were UTC and formats in UTC, so it doesn't depend on the
  viewer's time zone or need a zone database. Across a DST fall-back, that hour's times can sort
  out of order by up to an hour, which doesn't matter at timeline scale.
* **Cluster width cap**: the greedy merge also stops when a cluster would grow wider than
  64 px. Without the cap, daily unattended runs chain into one pill spanning months
  (seen on real data), which hides how dense each part of it is.
* **Overview**: bar height ∝ √changes, so ordinary weeks stay visible next to the occasional
  release upgrade. The legend says so. Clicking the overview re-centres the timeline. Brushing
  is M4.
* **Transactions with no changes**: aptitude logs entries even when it changes nothing
  (20 on this machine, probably mark-only changes). They are kept and shown as
  "no package changes".
* **Output**: default `./apt-history-<host>.html` (host sanitised to `[A-Za-z0-9._-]`), written
  to a temporary file and renamed into place. With several `--input`, `-o` is a directory and
  repeated hosts get `-2`, `-3`. Live machine: 1.07 MB of HTML.

### 6.2 Details settled while building M4

* **Filters** (in the timeline card) apply to the timeline, the overview bars and the table: origin
  toggles (hidden origins drop their lane), change-type toggles (a transaction shows if it has any
  change of a selected type), "errors & incomplete only", and a text box. Every word in the text box
  must appear in the command line, tool, user, error or a package name (`AHV.matches`).
* **Brush**: drag on the overview, or Shift+drag on the timeline, to zoom to a range. A plain
  click on the overview still re-centres the timeline.
* **Package view**: the search ranks exact matches, then prefix, then substring, shortest first. `name:arch`
  narrows the arch. Selecting a package (from search, or by clicking a package name in the detail
  panel) adds a lane above the origin lanes. It shows installed spans (`AHV.lifetimes`: from the
  log start for PreHistory packages, ending at each remove or purge) and ▲ install, ● upgrade,
  ▼ remove marks, each linked to its transaction. The card shows the current version, the
  auto/manual mark, PreHistory, and an event table, newest first.
* **Transactions table** lists the filtered transactions overlapping the visible range (the title
  says "All transactions" at full zoom). It is sortable by any column, rendered 200 rows at a
  time with "Show more", and redrawn 150 ms after zooming stops rather than on every frame.
* **Dark mode** shipped in M3 (`prefers-color-scheme`).

### 6.3 M5 implementation notes

* **`--redact`** (`model.Report.Redact`) runs after correlation, so origins are still decided
  from the real `Requested-By` and command lines. It drops `User` and `UID`, and cuts each
  command line to the program and its subcommand (`apt install …`,
  `/usr/bin/python3 /usr/bin/unattended-upgrade -d` → `unattended-upgrade …`). Paths inside
  the input keep their part below it behind the input's base name
  (`/home/alice/box.tgz:var/log/dpkg.log` → `box.tgz:var/log/dpkg.log`). Paths under
  `/var/log`, `/var/lib`, `/etc`, `/usr`, `/bin`, `/sbin` and `/lib` are kept. Any other path
  is cut to its base name, including paths inside error and warning text. Quoted log text in
  warnings becomes `"…"`, because a bad line may be a `Requested-By` line. Package names,
  versions and the host are kept. Since a host name often names its owner, stderr suggests
  `--host` when it isn't given. The report sets `redacted: true` and the page says so.
  `--raw` is refused with `--redact`.
* **`--open`** starts `xdg-open` on each HTML file written, without waiting for it (it can
  block until the browser exits). It is a usage error with `--json`, `--raw` or `-o -`. A
  missing `xdg-open` is a warning, not a failure.
* **Builds:** `make build | test | check | release`. `release` builds static
  `dist/apt-history-viewer-<version>-linux-{amd64,arm64}` (version from `git describe`) and
  `SHA256SUMS`. `check` also fuzzes each parser and `Build` for 10 s.

## 7. CLI

```
apt-history-viewer [flags]
  -o, --output FILE       output path (default ./apt-history-<host>.html, or stdout for
                          --json/--raw; "-" = stdout; with several --input, a directory)
  -i, --input PATH        input root: directory or .tar/.tar.gz/.tgz (default /; repeatable)
      --apt-dir DIR       override: apt log directory (default <input>/var/log/apt)
      --dpkg-log PATH     override: dpkg log (rotations found alongside)
      --dpkg-status PATH  override: dpkg status file
      --since DATE        --until DATE   (M8) restrict window
      --json              write the Report as JSON instead of HTML (debugging, tests)
      --raw               write the uncorrelated parse of every log as JSON (debugging)
      --redact            drop usernames, command-line arguments and private paths (§6.3)
      --open              open the result with xdg-open (HTML files only)
      --host NAME         override the host name shown in the report
      --etckeeper MODE    (M7) auto | off (default auto: use it if readable/present)
      --etckeeper-log PATH (M7) pre-exported `git log` output (see §3.6)
      --version
```

stdlib `flag` only. Exit non-zero only if *no* log could be read. Partial parse
problems go into `Report.Warnings` and stderr.

## 8. Project layout

```
go.mod                         module github.com/<user>/apt-history-viewer, go 1.27
cmd/apt-history-viewer/main.go flags, wiring
internal/source/               --input → fs.FS (dir, flat dir, tar, tar.gz), layout detection
internal/logfiles/             discover + order rotated files within an fs.FS, transparent gunzip
internal/apthistory/           history.log stanza parser
internal/dpkglog/              dpkg.log line parser, run grouping
internal/dpkgstate/            dpkg/status + extended_states parsers (RFC822-ish)
internal/aptitudelog/          /var/log/aptitude* report headers → run timestamps
internal/etckeeper/            (M7) run/locate git log output, parse commits
internal/debversion/           (M7) Debian version comparison (epoch:upstream-revision, ~ rules)
internal/model/                types, correlate, classify, build Packages
internal/render/               html/template + //go:embed assets/{report.tmpl,app.js,app.css}
testdata/                      small anonymised log fixtures
```

**Zero third-party dependencies**: `archive/tar`, `io/fs`, `bufio`, `compress/gzip`, `encoding/json`,
`html/template`, `embed`, `time`, `flag`.

## 9. Build

```
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$VERSION" ./cmd/apt-history-viewer
```

This gives a fully static ELF (check with `file` / `ldd`). Cross-compile with
`GOARCH=arm64` etc. The `Makefile` handles build / test / release (§6.3).

## 10. Testing

Fixtures live in `testdata/`. `testdata/README.md` lists each file, what it covers and the
expected result. `testdata/real/` is also a ready-made `--input` root.

* Parser unit tests with fixtures covering: multi-item lists, epochs (`1:3.2…`), `~` and `+`
  in versions, `Error:` stanzas, missing `End-Date`, truncated final stanza, double-space dates,
  Purge/Downgrade/Reinstall, `:all` vs `:amd64` arch.
* `go test -fuzz` on both line parsers: they must never panic on garbage input.
* Golden test: fixture logs → `--json` output compared to `testdata/*.golden.json`.
* Correlation tests: overlapping/adjacent transactions, dpkg-only runs, runs straddling
  a transaction's edges.
* Source tests: same fixture logs as a dir, a flat dir, a `.tar` and a `.tgz` give identical
  `--json` output. Tarballs with `../` paths, symlinks and huge members are handled safely.
* Clustering (JS): small test page or `node --test` over the pure clustering function,
  checking that clusters never cross lanes and disappear at max zoom.
* Classification: a no-Commandline stanza with and without a aptitude report 0–2 s before Start
  (→ aptitude vs "tool unknown").
* etckeeper (M7): `testdata/etckeeper/git-log-default.txt` covers post-run (aptitude and
  unattended-upgrade), pre-run, and daily autocommit commits, a non-adjacent `-`/`+` block (libc6
  amd64+i386), and a `Packages with configuration changes:` block that must not be counted as
  changes. Matching test from the 2026-11-12 06:21 runs: three apt runs, one commit → only the
  hplip run matches. Further fixtures of real `git log` output (captured with sudo from this machine)
  covering apt-run commits, pre-run "saving uncommitted changes" commits, daily autocommits,
  commits without a package list, and multi-line bodies. Debian version-comparison table
  tests (taken from dpkg's own test cases). Matching tests within and before the apt log window.
* Manual check: run against this machine's real logs and eyeball the report.

## 11. Milestones

1. **Parsers + `--json`**: source (dir/tar inputs), logfiles, apthistory, dpkglog, aptitudelog with tests.
2. **Model**: correlation, origin classification, baseline, packages.
3. **Report v1**: header, overview strip, origin-lane timeline with zoom and clustering, tooltips, detail panel.
4. **Report v2**: brush zoom, package view, transactions table, filters, dark mode.
5. **Polish**: `--redact`, `--open`, README, release builds for amd64/arm64.
6. **Later**: pre-rendered static `<svg>` overview in the HTML for no-JS viewing and printing.
7. **Later**: etckeeper history (§3.6): parser, local `git log` and pre-exported log input,
   matching and extension, `/etc` files in the detail panel, shaded etckeeper-only period.
8. **Later**: `--since DATE` / `--until DATE` to restrict the report to a time window.
   Not built yet. To decide when it is: the date format (`YYYY-MM-DD`, plus full
   timestamps?), whether dates are read in the source machine's zone, and whether
   transactions that cross the edges and the packages' "present before" state are kept.

## 12. Decisions on former open questions (2026-11-24)

1. **Pre-rendered static SVG fallback**: later. Not in v1, see Milestone 6.
2. **Logs from other machines and tarballs**: yes, in v1. See §3.5 and `--input`.
3. **Chatty periods**: yes. Merge into clusters at low zoom and allow zooming in
   until every transaction is shown individually. See §6, item 3 (transaction timeline).
4. **etckeeper**: worth supporting later, as Milestone 7 (§3.6). Parse `git log` text
   rather than linking a git library.
