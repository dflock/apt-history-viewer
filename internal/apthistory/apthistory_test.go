package apthistory

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/testutil"
)

var london = func() *time.Location {
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		panic(err)
	}
	return loc
}()

func parseFile(t *testing.T, elem ...string) ([]Stanza, diag.List) {
	t.Helper()
	f, err := os.Open(testutil.Testdata(t, elem...))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var warn diag.List
	return Parse(f, "test", london, &warn), warn
}

func TestRealHistory(t *testing.T) {
	st, warn := parseFile(t, "real", "var", "log", "apt", "history.log")
	if len(warn) != 0 {
		t.Errorf("warnings: %v", warn)
	}
	if len(st) != 10 {
		t.Fatalf("got %d stanzas, want 10", len(st))
	}

	// 2026-05-10 09:02:18 synaptic: BST, automatic deps.
	syn := st[2]
	if syn.Commandline != "/usr/sbin/synaptic" {
		t.Errorf("stanza 2 commandline = %q", syn.Commandline)
	}
	if _, off := syn.Start.Zone(); off != 3600 {
		t.Errorf("May stanza offset = %d, want BST (+3600)", off)
	}
	if !syn.Start.Equal(time.Date(2026, 5, 10, 8, 2, 18, 0, time.UTC)) {
		t.Errorf("start = %v", syn.Start)
	}
	auto := 0
	for _, it := range syn.Items {
		if it.Automatic {
			auto++
		}
	}
	if auto != 2 || len(syn.Items) != 3 {
		t.Errorf("synaptic items = %+v", syn.Items)
	}

	// 2026-05-10 09:03:29 synaptic install + purge.
	if got := st[3].Items; len(got) != 2 || got[1] != (Item{Action: Purge, Name: "gcolor3", Arch: "amd64", Old: "2.4.0-2build2"}) {
		t.Errorf("purge stanza items = %+v", got)
	}

	// 2026-11-13 11:58:30 aptitude: Requested-By, no Commandline.
	apt := st[8]
	if apt.HasCommandline() || apt.RequestedBy != "testuser" || apt.UID != 1000 || !apt.HasUID {
		t.Errorf("aptitude stanza = cmd %q, user %q, uid %d", apt.Commandline, apt.RequestedBy, apt.UID)
	}
	if !apt.End.Equal(time.Date(2026, 11, 13, 12, 0, 6, 0, time.UTC)) {
		t.Errorf("aptitude end = %v", apt.End)
	}
	if len(apt.Items) != 57 {
		t.Errorf("aptitude items = %d, want 57", len(apt.Items))
	}

	// Unattended upgrades: no Requested-By.
	for _, i := range []int{5, 6, 7} {
		if st[i].Commandline != "/usr/bin/unattended-upgrade" || st[i].RequestedBy != "" || st[i].HasUID {
			t.Errorf("stanza %d not unattended-shaped: %+v", i, st[i])
		}
	}

	// Epochs survive.
	if it := st[0].Items[0]; it.Old != "1:1.2.77.358.g4339a634" || it.New != "1:1.2.82.428.g0ac8be2b" {
		t.Errorf("epoch versions = %q -> %q", it.Old, it.New)
	}
}

func TestSynthetic(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		st, warn := parseFile(t, "synthetic", "apthistory", "error.log")
		if len(warn) != 0 || len(st) != 1 {
			t.Fatalf("st=%d warn=%v", len(st), warn)
		}
		if st[0].Error != "Sub-process /usr/bin/dpkg returned an error code (1)" {
			t.Errorf("error = %q", st[0].Error)
		}
	})

	t.Run("downgrade-reinstall", func(t *testing.T) {
		st, warn := parseFile(t, "synthetic", "apthistory", "downgrade-reinstall.log")
		if len(warn) != 0 || len(st) != 2 {
			t.Fatalf("st=%d warn=%v", len(st), warn)
		}
		if it := st[0].Items[0]; it != (Item{Action: Downgrade, Name: "foo", Arch: "amd64", Old: "2.0-1", New: "1.9-1"}) {
			t.Errorf("downgrade = %+v", it)
		}
		if it := st[1].Items[0]; it != (Item{Action: Reinstall, Name: "bar", Arch: "all", New: "3.1-2"}) {
			t.Errorf("reinstall = %+v", it)
		}
	})

	t.Run("missing-end", func(t *testing.T) {
		st, warn := parseFile(t, "synthetic", "apthistory", "missing-end.log")
		if len(warn) != 0 || len(st) != 2 {
			t.Fatalf("st=%d warn=%v", len(st), warn)
		}
		if !st[0].End.IsZero() {
			t.Errorf("first stanza End = %v, want zero", st[0].End)
		}
		if st[1].End.IsZero() {
			t.Error("second stanza lost its End-Date")
		}
	})

	t.Run("truncated", func(t *testing.T) {
		st, warn := parseFile(t, "synthetic", "apthistory", "truncated.log")
		if len(st) != 2 {
			t.Fatalf("st=%d", len(st))
		}
		if !st[1].End.IsZero() || len(st[1].Items) != 1 || st[1].Items[0].Name != "delta" {
			t.Errorf("truncated stanza = %+v", st[1])
		}
		if len(warn) != 1 || !strings.Contains(warn[0].Msg, "libdel") {
			t.Errorf("want one warning about the cut-off item, got %v", warn)
		}
	})

	t.Run("origins", func(t *testing.T) {
		st, warn := parseFile(t, "synthetic", "apthistory", "origins.log")
		if len(warn) != 0 || len(st) != 3 {
			t.Fatalf("st=%d warn=%v", len(st), warn)
		}
		if st[0].HasCommandline() || st[0].RequestedBy != "testuser" {
			t.Errorf("GUI-shaped stanza = %+v", st[0])
		}
		if st[1].Commandline != "apt-get -y install curl" || st[1].RequestedBy != "" {
			t.Errorf("root-shaped stanza = %+v", st[1])
		}
		if it := st[2].Items[0]; it.Arch != "all" {
			t.Errorf("tzdata arch = %q", it.Arch)
		}
	})

	t.Run("odd-versions", func(t *testing.T) {
		st, warn := parseFile(t, "synthetic", "apthistory", "odd-versions.log")
		if len(warn) != 0 || len(st) != 1 {
			t.Fatalf("st=%d warn=%v", len(st), warn)
		}
		want := []Item{
			{Action: Install, Name: "g++-13", Arch: "amd64", New: "13.3.0-6ubuntu2~24.04"},
			{Action: Install, Name: "libstdc++6", Arch: "i386", New: "14.2.0-4ubuntu2~24.04", Automatic: true},
			{Action: Install, Name: "python3.12", Arch: "amd64", New: "3.12.3-1ubuntu0.8"},
			{Action: Install, Name: "lib.dotted+name", Arch: "amd64", New: "1:2.3~rc1+dfsg-0.1~bpo12+1", Automatic: true},
			{Action: Upgrade, Name: "libstdc++6", Arch: "amd64", Old: "14.2.0-4ubuntu2~24.04", New: "14.2.0-4ubuntu2~24.04.1"},
			{Action: Remove, Name: "oldthing", Arch: "amd64", Old: "0.9"},
			{Action: Purge, Name: "oldconf", Arch: "all", Old: "2:0.1-1"},
		}
		if len(st[0].Items) != len(want) {
			t.Fatalf("items = %+v", st[0].Items)
		}
		for i := range want {
			if st[0].Items[i] != want[i] {
				t.Errorf("item %d = %+v, want %+v", i, st[0].Items[i], want[i])
			}
		}
	})

	t.Run("garbage", func(t *testing.T) {
		st, warn := parseFile(t, "synthetic", "garbage", "history-garbage.log")
		if len(st) != 0 {
			t.Errorf("parsed stanzas from garbage: %+v", st)
		}
		if len(warn) == 0 {
			t.Error("garbage produced no warnings")
		}
	})
}

func TestNoBlankLineBetweenStanzas(t *testing.T) {
	in := "Start-Date: 2026-01-01  10:00:00\nCommandline: apt install a\nEnd-Date: 2026-01-01  10:00:01\n" +
		"Start-Date: 2026-01-01  11:00:00\nCommandline: apt install b\nEnd-Date: 2026-01-01  11:00:01\n"
	var warn diag.List
	st := Parse(strings.NewReader(in), "x", time.UTC, &warn)
	if len(st) != 2 || st[1].Commandline != "apt install b" {
		t.Errorf("stanzas = %+v", st)
	}
}

func TestFirstTime(t *testing.T) {
	f, err := os.Open(testutil.Testdata(t, "real", "var", "log", "apt", "history.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, ok := FirstTime(f)
	if !ok || !got.Equal(time.Date(2026, 3, 16, 12, 48, 52, 0, time.UTC)) {
		t.Errorf("FirstTime = %v, %v", got, ok)
	}
}

func FuzzParse(f *testing.F) {
	for _, p := range [][]string{
		{"real", "var", "log", "apt", "history.log"},
		{"synthetic", "apthistory", "odd-versions.log"},
		{"synthetic", "apthistory", "truncated.log"},
		{"synthetic", "garbage", "history-garbage.log"},
	} {
		b, err := os.ReadFile(testutil.Testdata(f, p...))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(b))
	}
	f.Fuzz(func(t *testing.T, in string) {
		var warn diag.List
		for _, s := range Parse(strings.NewReader(in), "fuzz", time.UTC, &warn) {
			if s.Start.IsZero() {
				t.Errorf("stanza with zero Start: %+v", s)
			}
			for _, it := range s.Items {
				if it.Name == "" {
					t.Errorf("item with empty name: %+v", it)
				}
			}
		}
	})
}
