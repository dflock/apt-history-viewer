// Package source turns an --input argument into an fs.FS plus a Layout that
// says where each known log or state file lives inside it.
//
// Supported inputs (DESIGN.md §3.5):
//   - a directory laid out like a filesystem root ("/", or a copy of one),
//   - a flat directory of collected log files,
//   - a .tar, .tar.gz or .tgz archive in either layout, at any path depth.
//
// Archives are read in one streaming pass and only members with a known
// basename are kept, in memory. Nothing is ever extracted to disk.
package source

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"
	"time"

	"github.com/dflock/apt-history-viewer/internal/diag"
)

// MaxMemberSize caps how much of a single archive member is read into memory.
// Larger members are skipped with a warning.
var MaxMemberSize int64 = 256 << 20

// maxWalkDepth bounds the layout search in directories that are not laid out
// like a filesystem root.
const maxWalkDepth = 6

// Layout records where each known file lives inside Source.FS.
// Empty fields mean "not found". All paths are fs.FS paths (slash-separated,
// unrooted, "." for the root).
type Layout struct {
	AptDir         string `json:"aptDir,omitempty"`         // holds history.log*
	DpkgLogDir     string `json:"dpkgLogDir,omitempty"`     // holds dpkg.log*
	AptitudeDir    string `json:"aptitudeDir,omitempty"`    // holds aptitude*
	DpkgStatus     string `json:"dpkgStatus,omitempty"`     // var/lib/dpkg/status
	ExtendedStates string `json:"extendedStates,omitempty"` // var/lib/apt/extended_states
	Hostname       string `json:"hostname,omitempty"`       // etc/hostname
	Timezone       string `json:"timezone,omitempty"`       // etc/timezone
	Localtime      string `json:"localtime,omitempty"`      // etc/localtime (symlink; directories only)
	EtckeeperLog   string `json:"etckeeperLog,omitempty"`   // collected `git log` output (M7)
}

// Source is an opened input.
type Source struct {
	Name   string // the --input argument as given
	FS     fs.FS
	Tar    bool // true when read from an archive
	Layout Layout
}

// Open opens path as a directory or tar archive and detects its layout.
// Problems that don't prevent reading (skipped archive members, etc.) are
// added to warn.
func Open(p string, warn *diag.List) (*Source, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	s := &Source{Name: p}
	if info.IsDir() {
		s.FS = os.DirFS(p)
		s.Layout = detectDir(s.FS)
	} else {
		fsys, err := readTar(p, warn)
		if err != nil {
			return nil, fmt.Errorf("%s: not a directory or readable tar archive: %w", p, err)
		}
		s.FS, s.Tar = fsys, true
		s.Layout = detectWalk(fsys, -1)
	}
	return s, nil
}

// IsLocalRoot reports whether the source is this machine's own root filesystem.
func (s *Source) IsLocalRoot() bool {
	if s.Tar {
		return false
	}
	abs, err := filepath.Abs(s.Name)
	return err == nil && abs == "/"
}

// Host returns the input machine's hostname: etc/hostname if present,
// os.Hostname for the local root, else the input's basename.
func (s *Source) Host() string {
	if s.Layout.Hostname != "" {
		if b, err := fs.ReadFile(s.FS, s.Layout.Hostname); err == nil {
			if h := strings.TrimSpace(string(b)); h != "" {
				return h
			}
		}
	}
	if s.IsLocalRoot() {
		if h, err := os.Hostname(); err == nil {
			return h
		}
	}
	base := filepath.Base(filepath.Clean(s.Name))
	for _, ext := range []string{".tgz", ".tar.gz", ".tar"} {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

// Location returns the timezone the input's logs were written in, and a name
// to display for it. The logs store naive local times, so this matters.
func (s *Source) Location(warn *diag.List) (*time.Location, string) {
	try := func(name string) (*time.Location, bool) {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, false
		}
		loc, err := time.LoadLocation(name)
		if err != nil {
			warn.Add(s.Name, 0, "unknown timezone %q: %v", name, err)
			return nil, false
		}
		return loc, true
	}
	if s.Layout.Timezone != "" {
		if b, err := fs.ReadFile(s.FS, s.Layout.Timezone); err == nil {
			if loc, ok := try(string(b)); ok {
				return loc, loc.String()
			}
		}
	}
	if s.Layout.Localtime != "" {
		if target, err := fs.ReadLink(s.FS, s.Layout.Localtime); err == nil {
			if i := strings.Index(target, "zoneinfo/"); i >= 0 {
				if loc, ok := try(target[i+len("zoneinfo/"):]); ok {
					return loc, loc.String()
				}
			}
		}
	}
	if s.IsLocalRoot() {
		return time.Local, time.Now().Format("MST")
	}
	warn.Add(s.Name, 0, "timezone unknown: no etc/timezone; times shown as recorded, in the source machine's local time")
	return time.UTC, "unknown (local time of source machine)"
}

// --- layout detection ---

// kind describes one known file: how to recognise it by basename, the path
// suffix that marks the conventional location, and whether the Layout stores
// its directory (for rotated logs) or the file itself.
type kind struct {
	match  func(base string) bool
	suffix string // preferred path suffix, e.g. "dpkg/status"
	dir    bool
	set    func(*Layout, string)
}

func rotatedOf(stem string) func(string) bool {
	return func(b string) bool {
		return b == stem || strings.HasPrefix(b, stem+".") || strings.HasPrefix(b, stem+"-")
	}
}

func exactly(name string) func(string) bool { return func(b string) bool { return b == name } }

var kinds = []kind{
	{rotatedOf("history.log"), "apt/history.log", true, func(l *Layout, p string) { l.AptDir = p }},
	{rotatedOf("dpkg.log"), "log/dpkg.log", true, func(l *Layout, p string) { l.DpkgLogDir = p }},
	{rotatedOf("aptitude"), "log/aptitude", true, func(l *Layout, p string) { l.AptitudeDir = p }},
	{exactly("status"), "dpkg/status", false, func(l *Layout, p string) { l.DpkgStatus = p }},
	{exactly("extended_states"), "apt/extended_states", false, func(l *Layout, p string) { l.ExtendedStates = p }},
	{exactly("hostname"), "etc/hostname", false, func(l *Layout, p string) { l.Hostname = p }},
	{exactly("timezone"), "etc/timezone", false, func(l *Layout, p string) { l.Timezone = p }},
	{exactly("etckeeper.log"), "etckeeper.log", false, func(l *Layout, p string) { l.EtckeeperLog = p }},
}

// isKnown reports whether an archive member with this basename may be needed.
func isKnown(base string) bool {
	for _, k := range kinds {
		if k.match(base) {
			return true
		}
	}
	return false
}

// canonical is the standard Debian layout, relative to a filesystem root.
var canonical = Layout{
	AptDir:         "var/log/apt",
	DpkgLogDir:     "var/log",
	AptitudeDir:    "var/log",
	DpkgStatus:     "var/lib/dpkg/status",
	ExtendedStates: "var/lib/apt/extended_states",
	Hostname:       "etc/hostname",
	Timezone:       "etc/timezone",
	Localtime:      "etc/localtime",
}

// detectDir handles directory inputs. If the directory looks like a filesystem
// root, the canonical paths are used directly (no walk, which matters for "/").
// Otherwise the tree is searched to a bounded depth.
func detectDir(fsys fs.FS) Layout {
	if hasGlob(fsys, canonical.AptDir, "history.log*") ||
		hasGlob(fsys, canonical.DpkgLogDir, "dpkg.log*") ||
		exists(fsys, canonical.DpkgStatus) {
		var l Layout
		if hasGlob(fsys, canonical.AptDir, "history.log*") {
			l.AptDir = canonical.AptDir
		}
		if hasGlob(fsys, canonical.DpkgLogDir, "dpkg.log*") {
			l.DpkgLogDir = canonical.DpkgLogDir
		}
		if hasGlob(fsys, canonical.AptitudeDir, "aptitude*") {
			l.AptitudeDir = canonical.AptitudeDir
		}
		for _, f := range []struct {
			p   string
			dst *string
		}{
			{canonical.DpkgStatus, &l.DpkgStatus},
			{canonical.ExtendedStates, &l.ExtendedStates},
			{canonical.Hostname, &l.Hostname},
			{canonical.Timezone, &l.Timezone},
		} {
			if exists(fsys, f.p) {
				*f.dst = f.p
			}
		}
		if _, err := fs.ReadLink(fsys, canonical.Localtime); err == nil {
			l.Localtime = canonical.Localtime
		}
		if exists(fsys, "etckeeper.log") {
			l.EtckeeperLog = "etckeeper.log"
		}
		return l
	}
	return detectWalk(fsys, maxWalkDepth)
}

// detectWalk searches the whole FS (to maxDepth, or unbounded if < 0) and
// picks, for each kind, the best candidate: one ending in the conventional
// suffix if any, then the shallowest, then the lexically first.
func detectWalk(fsys fs.FS, maxDepth int) Layout {
	cands := make([][]string, len(kinds))
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip it
		}
		if d.IsDir() {
			if maxDepth >= 0 && p != "." && depth(p) >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		base := path.Base(p)
		for i, k := range kinds {
			if k.match(base) {
				cands[i] = append(cands[i], p)
			}
		}
		return nil
	})

	var l Layout
	for i, k := range kinds {
		if best := pick(cands[i], k); best != "" {
			if k.dir {
				best = path.Dir(best)
			}
			k.set(&l, best)
		}
	}
	return l
}

func pick(paths []string, k kind) string {
	if len(paths) == 0 {
		return ""
	}
	// For rotated kinds, compare the stem ("…/apt/history.log") so that
	// history.log.1.gz counts as being in the conventional place too.
	conventional := func(p string) bool {
		if k.dir {
			p = path.Join(path.Dir(p), path.Base(k.suffix))
		}
		return p == k.suffix || strings.HasSuffix(p, "/"+k.suffix)
	}
	slices.SortFunc(paths, func(a, b string) int {
		ca, cb := conventional(a), conventional(b)
		if ca != cb {
			if ca {
				return -1
			}
			return 1
		}
		if da, db := depth(a), depth(b); da != db {
			return da - db
		}
		return strings.Compare(a, b)
	})
	return paths[0]
}

func depth(p string) int {
	if p == "." {
		return 0
	}
	return strings.Count(p, "/") + 1
}

func exists(fsys fs.FS, p string) bool {
	info, err := fs.Stat(fsys, p)
	return err == nil && info.Mode().IsRegular()
}

func hasGlob(fsys fs.FS, dir, pattern string) bool {
	m, err := fs.Glob(fsys, path.Join(dir, pattern))
	return err == nil && len(m) > 0
}

// --- tar reading ---

func readTar(p string, warn *diag.List) (fs.FS, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	br := bufio.NewReader(f)
	var r io.Reader = br
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}

	tr := tar.NewReader(r)
	mfs := fstest.MapFS{}
	members := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if members == 0 {
				return nil, err
			}
			warn.Add(p, 0, "archive truncated or corrupt after %d members: %v", members, err)
			break
		}
		members++
		if hdr.Typeflag != tar.TypeReg {
			continue // symlinks, hard links, devices, directories: never followed
		}
		name := cleanMember(hdr.Name)
		if name == "" {
			warn.Add(p, 0, "skipped archive member with unsafe path %q", hdr.Name)
			continue
		}
		if !isKnown(path.Base(name)) {
			continue
		}
		if hdr.Size > MaxMemberSize {
			warn.Add(p, 0, "skipped archive member %q: %d bytes exceeds the %d byte limit", hdr.Name, hdr.Size, MaxMemberSize)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, MaxMemberSize+1))
		if err != nil {
			warn.Add(p, 0, "reading archive member %q: %v", hdr.Name, err)
			continue
		}
		mfs[name] = &fstest.MapFile{Data: data, Mode: 0o444, ModTime: hdr.ModTime}
	}
	if members == 0 {
		return nil, errors.New("archive is empty")
	}
	return mfs, nil
}

// cleanMember turns an archive member name into a valid, unrooted fs.FS path,
// or "" if it would escape the archive root.
func cleanMember(name string) string {
	name = strings.TrimLeft(name, "/")
	name = path.Clean(name)
	if name == "." || !fs.ValidPath(name) {
		return ""
	}
	return name
}
