// Package logfiles finds a log's rotated copies inside an fs.FS, opens them
// with transparent gzip decompression, and orders them oldest first.
//
// Ordering is by each file's first timestamp (supplied by the caller), not by
// name: numeric rotation sorts wrongly as text (".10" < ".2"), and logrotate's
// dateext names files by rotation day, not content day (DESIGN.md §3.4).
package logfiles

import (
	"bufio"
	"compress/gzip"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dflock/apt-history-viewer/internal/diag"
)

// File is one member of a rotated set.
type File struct {
	Path  string    // fs.FS path
	First time.Time // first timestamp found; zero if none
}

// FirstTimeFunc returns the first timestamp in a log, reading as little as it can.
type FirstTimeFunc func(r io.Reader) (time.Time, bool)

// unsupported compressions: recognised so they produce a warning instead of
// being silently ignored or parsed as garbage.
var unsupported = []string{".xz", ".zst", ".bz2", ".lz4"}

// Find returns the rotated set for stem (e.g. "history.log") in dir, oldest
// first. It matches stem, stem.N, stem.N.gz, stem-YYYYMMDD and
// stem-YYYYMMDD.gz. firstTime orders the files.
func Find(fsys fs.FS, dir, stem string, firstTime FirstTimeFunc, warn *diag.List) []File {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		warn.Add(dir, 0, "cannot read directory: %v", err)
		return nil
	}

	type cand struct {
		File
		fallback int // ordering when timestamps are missing: lower = older
	}
	var cands []cand
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() && e.Type()&fs.ModeSymlink == 0 {
			continue
		}
		rest, ok := strings.CutPrefix(name, stem)
		if !ok {
			continue
		}
		if ext := path.Ext(name); slices.Contains(unsupported, ext) {
			warn.Add(path.Join(dir, name), 0, "skipped: %s compression is not supported; decompress it first", ext)
			continue
		}
		fb, ok := fallbackOrder(rest)
		if !ok {
			continue
		}
		p := path.Join(dir, name)
		f := File{Path: p}
		if rc, err := Open(fsys, p); err != nil {
			warn.Add(p, 0, "cannot open: %v", err)
			continue
		} else {
			f.First, _ = firstTime(rc)
			rc.Close()
		}
		cands = append(cands, cand{f, fb})
	}

	// Fallback order first; then give files without a timestamp their
	// predecessor's, so a stable sort by time keeps them in place.
	slices.SortStableFunc(cands, func(a, b cand) int { return a.fallback - b.fallback })
	var last time.Time
	for i := range cands {
		if cands[i].First.IsZero() {
			cands[i].First = last
		} else {
			last = cands[i].First
		}
	}
	slices.SortStableFunc(cands, func(a, b cand) int { return a.First.Compare(b.First) })

	out := make([]File, len(cands))
	for i, c := range cands {
		out[i] = c.File
	}
	return out
}

var (
	numericRe = regexp.MustCompile(`^\.(\d+)(\.gz)?$`)
	dateextRe = regexp.MustCompile(`^-(\d{8})(\.gz)?$`)
)

// fallbackOrder ranks a suffix after the stem: older files get lower values.
// The current file ("") is newest. Unrecognised suffixes are rejected.
func fallbackOrder(rest string) (int, bool) {
	const current = 1 << 30
	switch {
	case rest == "", rest == ".gz":
		return current, true
	case numericRe.MatchString(rest):
		n, _ := strconv.Atoi(numericRe.FindStringSubmatch(rest)[1])
		return -n, true // .10 is older than .2
	case dateextRe.MatchString(rest):
		d, _ := strconv.Atoi(dateextRe.FindStringSubmatch(rest)[1])
		return d - 100000000, true // older dates first, all before current
	}
	return 0, false
}

// Open opens a log, decompressing gzip transparently (detected by magic bytes,
// not by name).
func Open(fsys fs.FS, p string) (io.ReadCloser, error) {
	f, err := fsys.Open(p)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(f)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &multiCloser{Reader: gz, closers: []io.Closer{gz, f}}, nil
	}
	return &multiCloser{Reader: br, closers: []io.Closer{f}}, nil
}

type multiCloser struct {
	io.Reader
	closers []io.Closer
}

func (m *multiCloser) Close() error {
	var first error
	for _, c := range m.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// NewScanner returns a line scanner that tolerates the very long lines apt
// writes for big transactions.
func NewScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	return sc
}
