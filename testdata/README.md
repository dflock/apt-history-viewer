# Test data

Two kinds of fixture:

* **`real/`, `etckeeper/`**: excerpts of real logs from the author's machine, anonymised
  (username → `testuser`, git author → `Test User <user@example.com>`, hostname → `testhost`).
  Use these for realistic parsing and correlation tests.
* **`synthetic/`**: hand-written cases that the real machine has never produced
  (errors, downgrades, crashes, odd rotation naming, garbage). Each file is small and
  targets one behaviour.

Compressed rotations (`.gz`) and tarball inputs (`.tar`, `.tgz`) are **not stored**:
tests build them from these plain files at run time, so every input type is checked
against identical content and no binary blobs are committed.

## `real/`: an input root (`--input testdata/real`)

Laid out like a filesystem, so it doubles as the directory-input fixture.

| File | Contents |
|---|---|
| `var/log/apt/history.log` | 10 stanzas, chosen to cover each origin and action (below) |
| `var/log/dpkg.log` | every dpkg.log line within ±2 s of those stanzas (878 lines, 24 dpkg runs). Includes all `status`/`configure`/`trigproc` noise, as the parser must skip it |
| `var/log/aptitude` | the one aptitude report matching the 2026-11-13 aptitude run |
| `var/lib/dpkg/status` | 16 full stanzas (15 packages; `libc6` twice: amd64 + i386) |
| `var/lib/apt/extended_states` | the matching entries that exist |
| `etc/hostname`, `etc/timezone` | `testhost`, `Europe/London` |

history.log stanzas and what each one tests:

| Start-Date | What | Tests |
|---|---|---|
| 2026-03-16 12:48:52 | `apt upgrade` spotify-client | epoch versions (`1:1.2…`) |
| 2026-05-08 22:56:58 | `apt remove ocelotgui` | Remove; package absent from `status` |
| 2026-05-10 09:02:18 | synaptic install, 2 `automatic` deps | origin user-gui; Dependency flag; **BST** (UTC+1) local time |
| 2026-05-10 09:03:29 | synaptic install + **Purge** | Purge action |
| 2026-11-03 21:37:03 | `apt install xdotool xvfb` | one `automatic` dep; epochs `1:` and `2:` |
| 2026-11-12 06:21:20 / :24 / :33 | three unattended-upgrade runs | origin unattended. Only the :24 run (hplip) has an etckeeper commit, so this is the sparse-matching test (DESIGN §3.6, §5.9) |
| 2026-11-13 11:58:30 | **no `Commandline`**, `Requested-By` only | aptitude detection: report at 11:58:29 (1 s before Start). Matches etckeeper commits `4a4948a` (pre) and `e01290c` (post, 12:00:07) |
| 2026-11-13 12:01:07 | `apt install -qq /tmp/obsidianmd.deb` | local .deb on the command line |

`status` extras: `bash`, `coreutils` never appear in the logs, so they should be **PreHistory**.
`jellyfin-ffmpeg7` is `deinstall ok config-files`, so it is not installed.

## `etckeeper/`: real `git log` output (M7)

| File | Command | Notes |
|---|---|---|
| `git-log-default.txt` | `git -C /etc log -5` | default format, no file lists. Post-run (aptitude, unattended), pre-run, daily autocommit. Non-adjacent `-`/`+` (libc6 amd64+i386). `Packages with configuration changes:` block |
| `git-log-name-status.txt` | `git -C /etc --no-pager log -n 6 --name-status` | same commits + one more, with `A`/`M`/`D` and **`R064` renames (two tab-separated paths)** |

## `synthetic/`

| Path | Tests | Expected |
|---|---|---|
| `apthistory/error.log` | `Error:` field | transaction has `Error`, marked red |
| `apthistory/downgrade-reinstall.log` | `Downgrade:`, `Reinstall:`; `:all` arch in history | actions downgrade, reinstall |
| `apthistory/missing-end.log` | stanza with no `End-Date` (apt killed), then a retry | first → `Incomplete`, End = Start; both parsed |
| `apthistory/truncated.log` | last stanza cut mid-item, no trailing newline | first stanza parsed; second → `Incomplete`, partial item dropped with a warning, no panic |
| `apthistory/origins.log` | Requested-By w/o Commandline and **no** aptitude report; Commandline w/o Requested-By; unattended with `:all` | user-gui with tool unknown; root; unattended |
| `apthistory/odd-versions.log` | `g++-13`, `libstdc++6` (amd64 and i386), `python3.12`, `lib.dotted+name`, versions with `~`, `+`, epochs, `rc`, `bpo`; Remove + Purge in one stanza | names and versions split correctly; `libstdc++6` keyed by arch |
| `dpkglog/dpkg-direct.log` | a `dpkg -i` run with no apt stanza | synthetic transaction, origin dpkg |
| `dpkglog/half-configured.log` | run whose package ends `half-configured` | `Incomplete`. Note: real logs show `half-configured` *mid-run* during normal upgrades, so only the **final** status per package counts |
| `dpkglog/remove-purge.log` | remove → purge → not-installed, `conffile` line | actions remove, purge; `conffile` ignored |
| `aptitude/actions.log` | `[INSTALL]`, `[INSTALL, DEPENDENCIES]`, `[UPGRADE] a -> b`, `[REMOVE]`, `[HOLD]` | header timestamp parsed (`Mon, Feb  9 2026 18:00:00 +0000`, double space) |
| `etckeeper/variants.txt` | `after apt run` subject; `made by` with no package list; decorated sha line with `Merge:` header; `Initial commit`; `+0100` offsets | subjects classified; Initial → `EtckeeperFrom` |
| `rotation-numeric/` | `history.log`, `.1`, `.2`, `.10` | read order .10 → .2 → .1 → current (lexical order would be wrong) |
| `rotation-dateext/` | `history.log-YYYYMMDD` (the date is the *rotation* day, after the content) | ordered by first timestamp, not by name |
| `root-packagekit/` | input root with no dpkg.log, no aptitude log | works with only history.log; warnings, no failure |
| `garbage/*.log` | invalid dates, unbalanced parens, NUL/0xFF bytes, missing fields | warnings only, never panic. Also seed corpus for `go test -fuzz` |

## `golden/`

| File | Contents |
|---|---|
| `real.json` | `--json -i ../../testdata/real` output, checked by `cmd/apt-history-viewer` tests. After an intended output change: `go test ./cmd/... -update`, then review the diff |

## Fuzz corpora

`go test -fuzz` saves failing inputs under `internal/<pkg>/testdata/fuzz/FuzzParse/`.
They are committed and run as regression tests on every `go test`. Inputs found so far:

* `apthistory/…/37fe208bedaa3c2b`: `Start-Date:0001-01-01 0:00:00`. Dates outside
  1990–2100 are now rejected as corrupt.
* `dpkglog/…/590ca2b1d61abe0e`: time going backwards inside a run (as DST fall-back
  does with dpkg's local timestamps). A run's `End` is now the latest time seen, not the last line's.
