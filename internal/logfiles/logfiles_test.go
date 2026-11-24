package logfiles_test

import (
	"io"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/dflock/apt-history-viewer/internal/apthistory"
	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/logfiles"
	"github.com/dflock/apt-history-viewer/internal/testutil"
)

func names(files []logfiles.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = path.Base(f.Path)
	}
	return out
}

func find(t *testing.T, dir string) ([]string, diag.List) {
	t.Helper()
	var warn diag.List
	files := logfiles.Find(os.DirFS(dir), ".", "history.log", apthistory.FirstTime, &warn)
	return names(files), warn
}

func equal(a, b []string) bool { return strings.Join(a, "|") == strings.Join(b, "|") }

func TestNumericRotationOrder(t *testing.T) {
	got, warn := find(t, testutil.Testdata(t, "synthetic", "rotation-numeric"))
	want := []string{"history.log.10", "history.log.2", "history.log.1", "history.log"}
	if !equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if len(warn) != 0 {
		t.Errorf("warnings: %v", warn)
	}
}

func TestDateextRotationOrder(t *testing.T) {
	got, _ := find(t, testutil.Testdata(t, "synthetic", "rotation-dateext"))
	want := []string{"history.log-20251203", "history.log-20260103", "history.log"}
	if !equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// Content order must win over names: here the names claim the opposite of
// the timestamps inside.
func TestTimestampsBeatNames(t *testing.T) {
	src := testutil.ReadTree(t, testutil.Testdata(t, "synthetic", "rotation-numeric"))
	dir := testutil.WriteTree(t, map[string][]byte{
		"history.log.1": src["history.log.10"], // oldest content, "newest" rotation number
		"history.log.2": src["history.log"],    // newest content
		"history.log":   src["history.log.1"],
	})
	got, _ := find(t, dir)
	want := []string{"history.log.1", "history.log", "history.log.2"}
	if !equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestGzipAndUnsupported(t *testing.T) {
	src := testutil.ReadTree(t, testutil.Testdata(t, "synthetic", "rotation-numeric"))
	dir := testutil.WriteTree(t, map[string][]byte{
		"history.log.10.gz": testutil.Gzip(t, src["history.log.10"]),
		"history.log.2.gz":  testutil.Gzip(t, src["history.log.2"]),
		"history.log.1":     src["history.log.1"],
		"history.log":       src["history.log"],
		"history.log.11.xz": []byte("\xfd7zXZ"),
		"history.log.bak":   []byte("ignored: unknown suffix"),
		"history.logger":    []byte("ignored: different stem"),
		"eipp.log.xz":       []byte("ignored: different stem"),
	})
	got, warn := find(t, dir)
	want := []string{"history.log.10.gz", "history.log.2.gz", "history.log.1", "history.log"}
	if !equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if len(warn) != 1 || !strings.Contains(warn[0].String(), "history.log.11.xz") {
		t.Errorf("want one warning about the .xz file, got %v", warn)
	}

	rc, err := logfiles.Open(os.DirFS(dir), "history.log.10.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != string(src["history.log.10"]) {
		t.Error("gzip content not decompressed transparently")
	}
}

func TestMissingDir(t *testing.T) {
	var warn diag.List
	files := logfiles.Find(os.DirFS(t.TempDir()), "nope", "history.log", apthistory.FirstTime, &warn)
	if len(files) != 0 || len(warn) != 1 {
		t.Errorf("files=%v warn=%v", files, warn)
	}
}
