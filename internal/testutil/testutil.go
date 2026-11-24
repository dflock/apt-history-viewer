// Package testutil has helpers shared by tests: locating testdata and
// building archive inputs at run time (DESIGN.md §10: no binary fixtures).
package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Testdata returns the absolute path of testdata/<elem...> at the repo root.
func Testdata(t testing.TB, elem ...string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(append([]string{dir, "testdata"}, elem...)...)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above working directory")
		}
		dir = parent
	}
}

// ReadTree returns every regular file under dir, keyed by slash path.
func ReadTree(t testing.TB, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// Member is one archive entry for MakeTar. Typeflag defaults to a regular file.
type Member struct {
	Name     string
	Data     []byte
	Typeflag byte
	Linkname string
}

// MakeTar writes a tar (gzip-compressed if gz) to a temp file and returns its path.
func MakeTar(t testing.TB, name string, gz bool, members []Member) string {
	t.Helper()
	var buf bytes.Buffer
	var tw *tar.Writer
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(&buf)
		tw = tar.NewWriter(zw)
	} else {
		tw = tar.NewWriter(&buf)
	}
	for _, m := range members {
		h := &tar.Header{Name: m.Name, Mode: 0o644, Typeflag: m.Typeflag, Linkname: m.Linkname}
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(m.Data))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(m.Data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Members turns a ReadTree map into tar members under prefix, in a stable order.
func Members(files map[string][]byte, prefix string) []Member {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Member, 0, len(names))
	for _, n := range names {
		out = append(out, Member{Name: prefix + n, Data: files[n]})
	}
	return out
}

// Gzip compresses b.
func Gzip(t testing.TB, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// WriteTree writes files (slash paths) under a new temp dir and returns it.
func WriteTree(t testing.TB, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for n, b := range files {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
