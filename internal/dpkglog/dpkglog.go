// Package dpkglog parses /var/log/dpkg.log (DESIGN.md §3.2).
//
//	2026-11-03 13:11:39 startup archives unpack
//	2026-11-03 13:11:39 install binutils-mingw-w64-i686:amd64 <none> 2.41.90…
//	2026-11-03 13:11:39 upgrade foo:amd64 1.0-1 1.0-2
//	2026-11-03 13:11:39 status unpacked foo:amd64 1.0-2
//
// Lines are grouped into runs: one dpkg invocation, from a "startup" line to
// the next. apt usually calls dpkg several times per transaction (unpack,
// then configure), so a transaction owns several runs; grouping them is the
// model's job (M2).
//
// Only install/upgrade/remove/purge become events. "status" lines only feed
// FinalStatus, used to spot failures; configure/trigproc/conffile are noise.
package dpkglog

import (
	"io"
	"strings"
	"time"

	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/logfiles"
)

// Event is a package change performed by dpkg.
type Event struct {
	Time   time.Time `json:"time"`
	Line   int       `json:"line"`
	Action string    `json:"action"` // install, upgrade, remove, purge
	Name   string    `json:"name"`
	Arch   string    `json:"arch,omitempty"`
	Old    string    `json:"old,omitempty"` // "" for <none>
	New    string    `json:"new,omitempty"` // "" for <none>
}

// Run is one dpkg invocation.
type Run struct {
	File   string    `json:"file"`
	Line   int       `json:"line"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`             // latest time seen in the run
	Phase  string    `json:"phase,omitempty"` // e.g. "archives unpack"; "" if the run had no startup line
	Events []Event   `json:"events,omitempty"`
	// FinalStatus maps "name:arch" to the last status dpkg reported for it in
	// this run (installed, unpacked, half-configured, config-files, …).
	// Mid-run half-configured states are normal; only the final one matters.
	FinalStatus map[string]string `json:"finalStatus,omitempty"`
}

const timeLayout = "2006-01-02 15:04:05"

func parseTime(date, clock string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation(timeLayout, date+" "+clock, loc)
	return t, err == nil && t.Year() >= 1990 && t.Year() <= 2100
}

// FirstTime returns the first timestamp in a dpkg log (a logfiles.FirstTimeFunc).
func FirstTime(r io.Reader) (time.Time, bool) {
	sc := logfiles.NewScanner(r)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 2 {
			if t, ok := parseTime(f[0], f[1], time.UTC); ok {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

var changeVerbs = map[string]bool{"install": true, "upgrade": true, "remove": true, "purge": true}

var ignoredVerbs = map[string]bool{"configure": true, "trigproc": true, "conffile": true}

// Parse reads one dpkg.log.
func Parse(r io.Reader, file string, loc *time.Location, warn *diag.List) []Run {
	var (
		runs []Run
		cur  *Run
		n    int
	)
	flush := func() {
		if cur != nil {
			runs = append(runs, *cur)
			cur = nil
		}
	}
	ensure := func(t time.Time, line int) {
		if cur == nil {
			cur = &Run{File: file, Line: line, Start: t}
		}
	}

	sc := logfiles.NewScanner(r)
	for sc.Scan() {
		n++
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) < 3 {
			warn.Add(file, n, "short line; ignored")
			continue
		}
		t, ok := parseTime(f[0], f[1], loc)
		if !ok {
			warn.Add(file, n, "bad timestamp %q; line ignored", f[0]+" "+f[1])
			continue
		}
		verb, args := f[2], f[3:]

		switch {
		case verb == "startup":
			flush()
			cur = &Run{File: file, Line: n, Start: t, End: t, Phase: strings.Join(args, " ")}
			continue
		case changeVerbs[verb]:
			if len(args) < 3 {
				warn.Add(file, n, "%s line needs package, old and new versions; ignored", verb)
				continue
			}
			ensure(t, n)
			name, arch, _ := strings.Cut(args[0], ":")
			cur.Events = append(cur.Events, Event{
				Time: t, Line: n, Action: verb,
				Name: name, Arch: arch, Old: none(args[1]), New: none(args[2]),
			})
		case verb == "status":
			if len(args) < 2 {
				warn.Add(file, n, "status line needs state and package; ignored")
				continue
			}
			ensure(t, n)
			if cur.FinalStatus == nil {
				cur.FinalStatus = map[string]string{}
			}
			cur.FinalStatus[args[1]] = args[0]
		case ignoredVerbs[verb]:
			ensure(t, n)
		default:
			warn.Add(file, n, "unknown action %q; ignored", verb)
			continue
		}
		// Max, not last: local time can step backwards (DST fall-back, clock fixes).
		if t.After(cur.End) {
			cur.End = t
		}
	}
	if err := sc.Err(); err != nil {
		warn.Add(file, n, "read error: %v", err)
	}
	flush()
	return runs
}

func none(v string) string {
	if v == "<none>" {
		return ""
	}
	return v
}
