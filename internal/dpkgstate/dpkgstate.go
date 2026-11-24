// Package dpkgstate parses the current-state files used for the baseline
// (DESIGN.md §3.3): /var/lib/dpkg/status and /var/lib/apt/extended_states.
//
// Both are RFC 822-style control files: blank-line-separated stanzas of
// "Field: value" lines, with continuation lines starting with a space or tab.
package dpkgstate

import (
	"io"
	"strings"

	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/logfiles"
)

// Entry is one package in dpkg/status.
type Entry struct {
	Name    string `json:"name"`
	Arch    string `json:"arch,omitempty"`
	Version string `json:"version,omitempty"`
	// State is the third word of Status: installed, config-files,
	// half-configured, unpacked, not-installed, …
	State string `json:"state"`
}

// Installed reports whether the package's files are fully installed.
func (e Entry) Installed() bool { return e.State == "installed" }

// Auto is one Auto-Installed entry from extended_states.
type Auto struct {
	Name string `json:"name"`
	Arch string `json:"arch,omitempty"`
}

// stanzas calls fn with the fields of each stanza. Continuation lines are
// dropped: none of the fields we read span lines.
func stanzas(r io.Reader, file string, warn *diag.List, fn func(line int, f map[string]string)) {
	var (
		cur   map[string]string
		start int
		n     int
	)
	flush := func() {
		if cur != nil {
			fn(start, cur)
			cur = nil
		}
	}
	sc := logfiles.NewScanner(r)
	for sc.Scan() {
		n++
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case strings.TrimSpace(line) == "":
			flush()
		case line[0] == ' ' || line[0] == '\t':
			// continuation of a multi-line field (Description, Conffiles, …)
		default:
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				warn.Add(file, n, "unrecognised line %q; ignored", line)
				continue
			}
			if cur == nil {
				cur, start = map[string]string{}, n
			}
			cur[k] = strings.TrimSpace(v)
		}
	}
	if err := sc.Err(); err != nil {
		warn.Add(file, n, "read error: %v", err)
	}
	flush()
}

// ParseStatus reads dpkg/status. Stanzas with no Package field are skipped
// with a warning; not-installed packages are dropped.
func ParseStatus(r io.Reader, file string, warn *diag.List) []Entry {
	var out []Entry
	stanzas(r, file, warn, func(line int, f map[string]string) {
		name := f["Package"]
		if name == "" {
			warn.Add(file, line, "stanza without Package; ignored")
			return
		}
		st := strings.Fields(f["Status"])
		if len(st) != 3 {
			warn.Add(file, line, "package %s: bad Status %q; ignored", name, f["Status"])
			return
		}
		if st[2] == "not-installed" {
			return
		}
		out = append(out, Entry{Name: name, Arch: f["Architecture"], Version: f["Version"], State: st[2]})
	})
	return out
}

// ParseExtendedStates reads apt's extended_states and returns the packages
// marked Auto-Installed: 1.
func ParseExtendedStates(r io.Reader, file string, warn *diag.List) []Auto {
	var out []Auto
	stanzas(r, file, warn, func(line int, f map[string]string) {
		if f["Package"] == "" {
			warn.Add(file, line, "stanza without Package; ignored")
			return
		}
		if f["Auto-Installed"] == "1" {
			out = append(out, Auto{Name: f["Package"], Arch: f["Architecture"]})
		}
	})
	return out
}
