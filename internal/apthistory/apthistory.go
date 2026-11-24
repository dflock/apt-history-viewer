// Package apthistory parses /var/log/apt/history.log (DESIGN.md §3.1).
//
// The file is a sequence of blank-line-separated stanzas:
//
//	Start-Date: 2026-11-03  21:37:03
//	Commandline: apt install xdotool xvfb
//	Requested-By: testuser (1000)
//	Install: libxdo3:amd64 (1:3.20160805.1-5build1, automatic), xdotool:amd64 (1:3.20160805.1-5build1)
//	End-Date: 2026-11-03  21:37:05
//
// The parser never fails: anything it can't understand becomes a warning.
package apthistory

import (
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/logfiles"
)

// Action is a package operation named in a stanza.
type Action string

const (
	Install   Action = "install"
	Upgrade   Action = "upgrade"
	Downgrade Action = "downgrade"
	Reinstall Action = "reinstall"
	Remove    Action = "remove"
	Purge     Action = "purge"
)

var actionFields = map[string]Action{
	"Install":   Install,
	"Upgrade":   Upgrade,
	"Downgrade": Downgrade,
	"Reinstall": Reinstall,
	"Remove":    Remove,
	"Purge":     Purge,
}

// Item is one package in an action list.
type Item struct {
	Action    Action `json:"action"`
	Name      string `json:"name"`
	Arch      string `json:"arch,omitempty"`
	Old       string `json:"old,omitempty"` // Upgrade/Downgrade: previous version; Remove/Purge: removed version
	New       string `json:"new,omitempty"` // Install/Upgrade/Downgrade/Reinstall: resulting version
	Automatic bool   `json:"automatic,omitempty"`
}

// Stanza is one apt transaction as recorded in history.log.
type Stanza struct {
	File        string    `json:"file"`
	Line        int       `json:"line"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end,omitzero"` // zero when End-Date is missing (apt killed, or still running)
	Commandline string    `json:"commandline,omitempty"`
	RequestedBy string    `json:"requestedBy,omitempty"` // username
	UID         int       `json:"uid,omitempty"`
	HasUID      bool      `json:"hasUid,omitempty"`
	Error       string    `json:"error,omitempty"`
	Items       []Item    `json:"items,omitempty"`
}

// HasCommandline reports whether the stanza recorded a command line
// (aptitude and PackageKit don't write one).
func (s *Stanza) HasCommandline() bool { return s.Commandline != "" }

const dateLayout = "2006-01-02 15:04:05"

// ParseDate parses history.log's "2006-01-02  15:04:05" (two spaces) leniently,
// as naive local time in loc.
func ParseDate(s string, loc *time.Location) (time.Time, bool) {
	t, err := time.ParseInLocation(dateLayout, strings.Join(strings.Fields(s), " "), loc)
	return t, err == nil && Plausible(t)
}

// Plausible rejects dates no real package log can contain (Debian dates from
// 1993), which also catches corrupt lines that happen to parse.
func Plausible(t time.Time) bool {
	return t.Year() >= 1990 && t.Year() <= 2100
}

// FirstTime returns the first Start-Date in a history log. It is a
// logfiles.FirstTimeFunc (using UTC, which is fine for ordering).
func FirstTime(r io.Reader) (time.Time, bool) {
	sc := logfiles.NewScanner(r)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "Start-Date:"); ok {
			return ParseDate(v, time.UTC)
		}
	}
	return time.Time{}, false
}

// Parse reads one history.log. file is used in stanzas and warnings.
func Parse(r io.Reader, file string, loc *time.Location, warn *diag.List) []Stanza {
	var (
		out []Stanza
		cur *Stanza
		n   int
	)
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}

	sc := logfiles.NewScanner(r)
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			warn.Add(file, n, "unrecognised line: %q", truncate(line))
			continue
		}
		val = strings.TrimSpace(val)

		if key == "Start-Date" {
			flush() // tolerate a missing blank line between stanzas
			t, ok := ParseDate(val, loc)
			if !ok {
				warn.Add(file, n, "bad Start-Date %q; stanza skipped", truncate(val))
				// Swallow the rest of this stanza; a negative Line marks it for dropping.
				cur = &Stanza{File: file, Line: -n}
				continue
			}
			cur = &Stanza{File: file, Line: n, Start: t}
			continue
		}
		if cur == nil {
			warn.Add(file, n, "%q outside a stanza (no Start-Date); ignored", key)
			continue
		}
		if cur.Line < 0 {
			continue // inside a stanza whose Start-Date was unusable
		}

		switch key {
		case "End-Date":
			if t, ok := ParseDate(val, loc); ok {
				cur.End = t
			} else {
				warn.Add(file, n, "bad End-Date %q", truncate(val))
			}
		case "Commandline":
			cur.Commandline = val
		case "Requested-By":
			cur.RequestedBy, cur.UID, cur.HasUID = parseRequestedBy(val)
		case "Error":
			cur.Error = val
		default:
			act, ok := actionFields[key]
			if !ok {
				warn.Add(file, n, "unknown field %q; ignored", key)
				continue
			}
			cur.Items = append(cur.Items, parseItems(act, val, file, n, warn)...)
		}
	}
	if err := sc.Err(); err != nil {
		warn.Add(file, n, "read error: %v", err)
	}
	flush()

	// Drop stanzas whose Start-Date was unusable.
	kept := out[:0]
	for _, s := range out {
		if s.Line > 0 {
			kept = append(kept, s)
		}
	}
	return kept
}

// parseRequestedBy parses "testuser (1000)".
func parseRequestedBy(v string) (user string, uid int, ok bool) {
	user = v
	if i := strings.LastIndex(v, " ("); i >= 0 && strings.HasSuffix(v, ")") {
		if n, err := strconv.Atoi(v[i+2 : len(v)-1]); err == nil {
			return v[:i], n, true
		}
	}
	return user, 0, false
}

// parseItems parses "name:arch (v1[, v2][, automatic]), name:arch (…)".
// Versions never contain '(', ')' or ',', so each item ends at ")".
func parseItems(act Action, v, file string, line int, warn *diag.List) []Item {
	var items []Item
	for rest := v; strings.TrimSpace(rest) != ""; {
		rest = strings.TrimLeft(rest, " ,")
		open := strings.Index(rest, " (")
		close := strings.IndexByte(rest, ')')
		if open < 0 || close < open {
			warn.Add(file, line, "malformed %s item %q; dropped", act, truncate(rest))
			break
		}
		pkg := strings.TrimSpace(rest[:open])
		inner := rest[open+2 : close]
		rest = rest[close+1:]

		it := Item{Action: act}
		it.Name, it.Arch, _ = strings.Cut(pkg, ":")

		var parts []string
		for p := range strings.SplitSeq(inner, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 && parts[len(parts)-1] == "automatic" {
			it.Automatic = true
			parts = parts[:len(parts)-1]
		}

		switch {
		case it.Name == "" || len(parts) == 0:
			warn.Add(file, line, "malformed %s item %q; dropped", act, truncate(pkg+" ("+inner+")"))
			continue
		case act == Upgrade || act == Downgrade:
			if len(parts) != 2 {
				warn.Add(file, line, "%s item %q should have old and new versions; dropped", act, pkg)
				continue
			}
			it.Old, it.New = parts[0], parts[1]
		case act == Remove || act == Purge:
			it.Old = parts[0]
		default:
			it.New = parts[0]
		}
		items = append(items, it)
	}
	return items
}

func truncate(s string) string {
	const max = 80
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
