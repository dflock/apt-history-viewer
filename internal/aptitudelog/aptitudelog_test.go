package aptitudelog

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/testutil"
)

func parseFile(t *testing.T, elem ...string) ([]Report, diag.List) {
	t.Helper()
	f, err := os.Open(testutil.Testdata(t, elem...))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var warn diag.List
	return Parse(f, "test", &warn), warn
}

func TestRealAptitude(t *testing.T) {
	reps, warn := parseFile(t, "real", "var", "log", "aptitude")
	if len(warn) != 0 || len(reps) != 1 {
		t.Fatalf("reports=%d warn=%v", len(reps), warn)
	}
	// 1 s before the matching history.log Start-Date (11:58:30).
	if want := time.Date(2026, 11, 13, 11, 58, 29, 0, time.UTC); !reps[0].Time.Equal(want) {
		t.Errorf("time = %v, want %v", reps[0].Time, want)
	}
	if reps[0].Actions != 57 || reps[0].Line != 1 {
		t.Errorf("report = %+v", reps[0])
	}
}

func TestSyntheticActions(t *testing.T) {
	reps, warn := parseFile(t, "synthetic", "aptitude", "actions.log")
	if len(warn) != 0 || len(reps) != 1 {
		t.Fatalf("reports=%d warn=%v", len(reps), warn)
	}
	if want := time.Date(2026, 2, 9, 18, 0, 0, 0, time.UTC); !reps[0].Time.Equal(want) {
		t.Errorf("time = %v (double-space day)", reps[0].Time)
	}
	if reps[0].Actions != 5 {
		t.Errorf("actions = %d, want 5", reps[0].Actions)
	}
}

func TestMultipleReportsAndOffsets(t *testing.T) {
	in := "Aptitude 0.8.13: log report\nTue, Jul 14 2026 10:00:00 +0100\n[INSTALL] a:amd64 1\n\nLog complete.\n" +
		"Aptitude 0.8.13: log report\nnot a date\n[INSTALL] b:amd64 1\n" +
		"Aptitude 0.8.13: log report\nWed, Jul 15 2026 11:00:00 +0100\n[UPGRADE] c:amd64 1 -> 2\n[REMOVE] d:amd64 1\n"
	var warn diag.List
	reps := Parse(strings.NewReader(in), "x", &warn)
	if len(reps) != 2 || len(warn) != 1 {
		t.Fatalf("reports=%+v warn=%v", reps, warn)
	}
	if !reps[0].Time.Equal(time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)) || reps[0].Actions != 1 {
		t.Errorf("first = %+v", reps[0])
	}
	if reps[1].Actions != 2 {
		t.Errorf("second actions = %d (the skipped report's lines must not count)", reps[1].Actions)
	}
}

func FuzzParse(f *testing.F) {
	for _, p := range [][]string{{"synthetic", "aptitude", "actions.log"}, {"real", "var", "log", "aptitude"}} {
		b, err := os.ReadFile(testutil.Testdata(f, p...))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(b))
	}
	f.Fuzz(func(t *testing.T, in string) {
		var warn diag.List
		for _, r := range Parse(strings.NewReader(in), "fuzz", &warn) {
			if r.Time.IsZero() {
				t.Errorf("report with zero time: %+v", r)
			}
		}
	})
}
