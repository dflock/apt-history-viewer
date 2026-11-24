// Package collect finds and parses every log in a Source.
//
// Its Result is the raw parse, before correlation: that's the M1 --json
// output. The model package (M2) turns it into transactions.
package collect

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/dflock/apt-history-viewer/internal/apthistory"
	"github.com/dflock/apt-history-viewer/internal/aptitudelog"
	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/dpkglog"
	"github.com/dflock/apt-history-viewer/internal/dpkgstate"
	"github.com/dflock/apt-history-viewer/internal/logfiles"
	"github.com/dflock/apt-history-viewer/internal/source"
)

// Overrides point individual logs at paths on the local disk, outside the
// input (the --apt-dir and --dpkg-log flags).
type Overrides struct {
	AptDir     string // directory holding history.log*
	DpkgLog    string // path of dpkg.log; rotations are found alongside
	DpkgStatus string // path of dpkg's status file
}

// Result is everything read from one input.
type Result struct {
	Input           string               `json:"input"`
	Host            string               `json:"host"`
	TZ              string               `json:"tz"`
	Layout          source.Layout        `json:"layout"`
	Files           []string             `json:"files"` // every log read, oldest first per kind
	AptHistory      []apthistory.Stanza  `json:"aptHistory"`
	DpkgRuns        []dpkglog.Run        `json:"dpkgRuns"`
	AptitudeReports []aptitudelog.Report `json:"aptitudeReports"`
	DpkgStatus      []dpkgstate.Entry    `json:"dpkgStatus"`    // current packages, for the baseline
	AutoInstalled   []dpkgstate.Auto     `json:"autoInstalled"` // from extended_states
	Warnings        []diag.Warning       `json:"warnings"`
}

// Collect reads all logs from src. It never fails: missing or unreadable
// logs become warnings. Callers decide whether an empty Result is an error.
func Collect(src *source.Source, ov Overrides, warn *diag.List) *Result {
	loc, tzName := src.Location(warn)
	res := &Result{
		Input:  src.Name,
		Host:   src.Host(),
		TZ:     tzName,
		Layout: src.Layout,
	}

	// display names: real disk paths for directories, "archive:member" for tars.
	// (base is set only for --apt-dir/--dpkg-log overrides, which live on disk.)
	display := func(base, p string) string {
		if base != "" {
			return filepath.Join(base, filepath.FromSlash(p))
		}
		if src.Tar {
			return src.Name + ":" + p
		}
		return filepath.Join(src.Name, filepath.FromSlash(p))
	}

	type logSet struct {
		what      string
		fsys      fs.FS
		base      string // disk dir for overrides, for display
		dir, stem string
		first     logfiles.FirstTimeFunc
		parse     func(r io.Reader, name string)
	}
	sets := []logSet{
		{
			what: "apt history", fsys: src.FS, dir: src.Layout.AptDir, stem: "history.log",
			first: apthistory.FirstTime,
			parse: func(r io.Reader, name string) {
				res.AptHistory = append(res.AptHistory, apthistory.Parse(r, name, loc, warn)...)
			},
		},
		{
			what: "dpkg", fsys: src.FS, dir: src.Layout.DpkgLogDir, stem: "dpkg.log",
			first: dpkglog.FirstTime,
			parse: func(r io.Reader, name string) {
				res.DpkgRuns = append(res.DpkgRuns, dpkglog.Parse(r, name, loc, warn)...)
			},
		},
		{
			what: "aptitude", fsys: src.FS, dir: src.Layout.AptitudeDir, stem: "aptitude",
			first: aptitudelog.FirstTime,
			parse: func(r io.Reader, name string) {
				res.AptitudeReports = append(res.AptitudeReports, aptitudelog.Parse(r, name, warn)...)
			},
		},
	}
	if ov.AptDir != "" {
		sets[0].fsys, sets[0].base, sets[0].dir = os.DirFS(ov.AptDir), ov.AptDir, "."
	}
	if ov.DpkgLog != "" {
		d := filepath.Dir(ov.DpkgLog)
		sets[1].fsys, sets[1].base, sets[1].dir, sets[1].stem = os.DirFS(d), d, ".", filepath.Base(ov.DpkgLog)
	}

	for _, s := range sets {
		if s.dir == "" {
			if s.what != "aptitude" { // aptitude is optional; the others are worth flagging
				warn.Add(src.Name, 0, "no %s logs found", s.what)
			}
			continue
		}
		for _, f := range logfiles.Find(s.fsys, s.dir, s.stem, s.first, warn) {
			name := display(s.base, f.Path)
			rc, err := logfiles.Open(s.fsys, f.Path)
			if err != nil {
				warn.Add(name, 0, "cannot open: %v", err)
				continue
			}
			s.parse(rc, name)
			rc.Close()
			res.Files = append(res.Files, name)
		}
	}

	readState(src, ov, res, display, warn)

	res.Warnings = *warn
	if res.Warnings == nil {
		res.Warnings = []diag.Warning{}
	}
	return res
}

// Empty reports whether nothing at all was read.
func (r *Result) Empty() bool {
	return len(r.AptHistory) == 0 && len(r.DpkgRuns) == 0
}

// Span returns the earliest and latest times seen across the apt and dpkg logs.
func (r *Result) Span() (from, to time.Time) {
	see := func(t time.Time) {
		if t.IsZero() {
			return
		}
		if from.IsZero() || t.Before(from) {
			from = t
		}
		if t.After(to) {
			to = t
		}
	}
	for _, s := range r.AptHistory {
		see(s.Start)
		see(s.End)
	}
	for _, run := range r.DpkgRuns {
		see(run.Start)
		see(run.End)
	}
	return from, to
}

// readState reads the current-state files (DESIGN.md §3.3). They are optional:
// copied logs often come without them, and then there is simply no baseline.
func readState(src *source.Source, ov Overrides, res *Result, display func(base, p string) string, warn *diag.List) {
	read := func(fsys fs.FS, base, p string, parse func(io.Reader, string)) {
		if p == "" {
			return
		}
		name := display(base, p)
		f, err := fsys.Open(p)
		if err != nil {
			warn.Add(name, 0, "cannot open: %v", err)
			return
		}
		defer f.Close()
		parse(f, name)
		res.Files = append(res.Files, name)
	}
	statusFS, statusBase, statusPath := src.FS, "", src.Layout.DpkgStatus
	if ov.DpkgStatus != "" {
		d := filepath.Dir(ov.DpkgStatus)
		statusFS, statusBase, statusPath = os.DirFS(d), d, filepath.Base(ov.DpkgStatus)
	}
	read(statusFS, statusBase, statusPath, func(r io.Reader, name string) {
		res.DpkgStatus = dpkgstate.ParseStatus(r, name, warn)
	})
	read(src.FS, "", src.Layout.ExtendedStates, func(r io.Reader, name string) {
		res.AutoInstalled = dpkgstate.ParseExtendedStates(r, name, warn)
	})
}
