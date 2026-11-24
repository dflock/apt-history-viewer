package dpkglog

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/testutil"
)

func parseFile(t *testing.T, elem ...string) ([]Run, diag.List) {
	t.Helper()
	f, err := os.Open(testutil.Testdata(t, elem...))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var warn diag.List
	return Parse(f, "test", time.UTC, &warn), warn
}

func TestRealDpkgLog(t *testing.T) {
	runs, warn := parseFile(t, "real", "var", "log", "dpkg.log")
	if len(warn) != 0 {
		t.Errorf("warnings: %v", warn)
	}
	if len(runs) != 24 {
		t.Fatalf("runs = %d, want 24 (one per startup line)", len(runs))
	}
	first := runs[0]
	if first.Phase != "archives unpack" || len(first.Events) != 1 {
		t.Fatalf("first run = %+v", first)
	}
	want := Event{
		Time: time.Date(2026, 3, 16, 12, 48, 52, 0, time.UTC), Line: 2, Action: "upgrade",
		Name: "spotify-client", Arch: "amd64", Old: "1:1.2.77.358.g4339a634", New: "1:1.2.82.428.g0ac8be2b",
	}
	if first.Events[0] != want {
		t.Errorf("event = %+v\nwant  %+v", first.Events[0], want)
	}
	// Unpack phase leaves the package unpacked; the configure run that follows installs it.
	if got := first.FinalStatus["spotify-client:amd64"]; got != "unpacked" {
		t.Errorf("unpack-run final status = %q", got)
	}
	if got := runs[1].FinalStatus["spotify-client:amd64"]; runs[1].Phase != "packages configure" || got != "installed" {
		t.Errorf("configure run: phase %q, final status %q", runs[1].Phase, got)
	}

	// Install events use <none> for the old version.
	for _, r := range runs {
		for _, e := range r.Events {
			if e.Name == "xdotool" && e.Action == "install" && (e.Old != "" || e.New == "") {
				t.Errorf("xdotool install = %+v", e)
			}
			if e.Arch == "" {
				t.Errorf("event without arch: %+v", e)
			}
		}
	}
}

func TestSynthetic(t *testing.T) {
	t.Run("dpkg-direct", func(t *testing.T) {
		runs, warn := parseFile(t, "synthetic", "dpkglog", "dpkg-direct.log")
		if len(warn) != 0 || len(runs) != 2 {
			t.Fatalf("runs=%d warn=%v", len(runs), warn)
		}
		if e := runs[0].Events; len(e) != 1 || e[0].Action != "install" || e[0].Name != "localtool" || e[0].New != "0.4.2" {
			t.Errorf("events = %+v", e)
		}
		if got := runs[1].FinalStatus["localtool:amd64"]; got != "installed" {
			t.Errorf("final status = %q", got)
		}
	})

	t.Run("half-configured", func(t *testing.T) {
		runs, warn := parseFile(t, "synthetic", "dpkglog", "half-configured.log")
		if len(warn) != 0 || len(runs) != 2 {
			t.Fatalf("runs=%d warn=%v", len(runs), warn)
		}
		if got := runs[1].FinalStatus["broken-pkg:amd64"]; got != "half-configured" {
			t.Errorf("final status = %q, want half-configured", got)
		}
		if !runs[1].End.Equal(time.Date(2026, 2, 8, 16, 0, 2, 0, time.UTC)) {
			t.Errorf("run end = %v", runs[1].End)
		}
	})

	t.Run("remove-purge", func(t *testing.T) {
		runs, warn := parseFile(t, "synthetic", "dpkglog", "remove-purge.log")
		if len(warn) != 0 || len(runs) != 1 {
			t.Fatalf("runs=%d warn=%v", len(runs), warn)
		}
		ev := runs[0].Events
		if len(ev) != 2 || ev[0].Action != "remove" || ev[1].Action != "purge" || ev[1].Old != "2:0.1-1" || ev[1].New != "" {
			t.Errorf("events = %+v", ev)
		}
		if got := runs[0].FinalStatus["oldconf:all"]; got != "not-installed" {
			t.Errorf("final status = %q", got)
		}
	})

	t.Run("garbage", func(t *testing.T) {
		runs, warn := parseFile(t, "synthetic", "garbage", "dpkg-garbage.log")
		for _, r := range runs {
			if len(r.Events) != 0 {
				t.Errorf("events from garbage: %+v", r.Events)
			}
		}
		if len(warn) < 4 {
			t.Errorf("want a warning per bad line, got %v", warn)
		}
	})
}

func TestLinesBeforeFirstStartup(t *testing.T) {
	in := "2026-01-01 10:00:00 status installed a:amd64 1.0\n2026-01-01 10:00:01 startup archives unpack\n"
	var warn diag.List
	runs := Parse(strings.NewReader(in), "x", time.UTC, &warn)
	if len(runs) != 2 || runs[0].Phase != "" || runs[1].Phase != "archives unpack" {
		t.Errorf("runs = %+v", runs)
	}
}

func FuzzParse(f *testing.F) {
	for _, p := range [][]string{
		{"synthetic", "dpkglog", "dpkg-direct.log"},
		{"synthetic", "dpkglog", "remove-purge.log"},
		{"synthetic", "garbage", "dpkg-garbage.log"},
	} {
		b, err := os.ReadFile(testutil.Testdata(f, p...))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(b))
	}
	f.Fuzz(func(t *testing.T, in string) {
		var warn diag.List
		for _, r := range Parse(strings.NewReader(in), "fuzz", time.UTC, &warn) {
			if r.End.Before(r.Start) {
				t.Errorf("run ends before it starts: %+v", r)
			}
		}
	})
}
