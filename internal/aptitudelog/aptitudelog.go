// Package aptitudelog parses /var/log/aptitude (DESIGN.md §3.7).
//
//	Aptitude 0.8.13: log report
//	Wed, Nov  4 2026 21:32:15 +0000
//	…
//	[UPGRADE] apt:amd64 2.8.3 -> 2.8.4ubuntu0.1
//	…
//	Log complete.
//
// Only the report timestamps are used: aptitude writes history.log stanzas
// with Requested-By but no Commandline, and a report 0–1 s before the
// stanza's Start-Date is what identifies the tool. The action lines list
// intended actions only, so history.log and dpkg.log stay authoritative.
package aptitudelog

import (
	"io"
	"strings"
	"time"

	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/logfiles"
)

// Report is one aptitude run.
type Report struct {
	File    string    `json:"file"`
	Line    int       `json:"line"`
	Time    time.Time `json:"time"`    // includes the zone offset aptitude wrote
	Actions int       `json:"actions"` // number of [ACTION] lines, for diagnostics
}

const headerSuffix = ": log report"

func isHeader(line string) bool {
	return strings.HasPrefix(line, "Aptitude ") && strings.HasSuffix(line, headerSuffix)
}

// parseTime parses "Wed, Nov  4 2026 21:32:15 +0000" (note the padded day).
func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse("Mon, Jan 2 2006 15:04:05 -0700", strings.Join(strings.Fields(s), " "))
	return t, err == nil && t.Year() >= 1990 && t.Year() <= 2100
}

// FirstTime returns the first report's time (a logfiles.FirstTimeFunc).
func FirstTime(r io.Reader) (time.Time, bool) {
	sc := logfiles.NewScanner(r)
	for sc.Scan() {
		if isHeader(sc.Text()) && sc.Scan() {
			return parseTime(sc.Text())
		}
	}
	return time.Time{}, false
}

// Parse reads one aptitude log.
func Parse(r io.Reader, file string, warn *diag.List) []Report {
	var (
		out        []Report
		cur        = -1 // index into out of the report being read
		n          int
		wantHeader bool
	)
	sc := logfiles.NewScanner(r)
	for sc.Scan() {
		n++
		line := sc.Text()
		switch {
		case isHeader(line):
			wantHeader = true
			cur = -1
		case wantHeader:
			wantHeader = false
			t, ok := parseTime(line)
			if !ok {
				warn.Add(file, n, "bad aptitude report time %q; report skipped", line)
				continue
			}
			out = append(out, Report{File: file, Line: n - 1, Time: t})
			cur = len(out) - 1
		case cur >= 0 && strings.HasPrefix(line, "["):
			out[cur].Actions++
		}
	}
	if err := sc.Err(); err != nil {
		warn.Add(file, n, "read error: %v", err)
	}
	return out
}
