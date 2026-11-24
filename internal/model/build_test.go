package model

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dflock/apt-history-viewer/internal/apthistory"
	"github.com/dflock/apt-history-viewer/internal/aptitudelog"
	"github.com/dflock/apt-history-viewer/internal/collect"
	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/dpkglog"
	"github.com/dflock/apt-history-viewer/internal/source"
	"github.com/dflock/apt-history-viewer/internal/testutil"
)

var genTime = time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)

// fromText builds a Result from log text, all in UTC.
func fromText(t *testing.T, history, dpkg, aptitude string) *collect.Result {
	t.Helper()
	var warn diag.List
	res := &collect.Result{
		AptHistory:      apthistory.Parse(strings.NewReader(history), "history.log", time.UTC, &warn),
		DpkgRuns:        dpkglog.Parse(strings.NewReader(dpkg), "dpkg.log", time.UTC, &warn),
		AptitudeReports: aptitudelog.Parse(strings.NewReader(aptitude), "aptitude", &warn),
	}
	if len(warn) != 0 {
		t.Fatalf("fixture warnings: %v", warn)
	}
	return res
}

func read(t testing.TB, elem ...string) string {
	t.Helper()
	b, err := os.ReadFile(testutil.Testdata(t, elem...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func at(s string) time.Time {
	t, err := time.Parse(time.DateTime, s)
	if err != nil {
		panic(err)
	}
	return t
}

func realReport(t *testing.T) *Report {
	t.Helper()
	var warn diag.List
	src, err := source.Open(testutil.Testdata(t, "real"), &warn)
	if err != nil {
		t.Fatal(err)
	}
	r := Build(collect.Collect(src, collect.Overrides{}, &warn), genTime)
	if len(r.Warnings) != 0 {
		t.Fatalf("warnings: %v", r.Warnings)
	}
	return r
}

func findChange(t *testing.T, tx Transaction, name string) Change {
	t.Helper()
	for _, c := range tx.Changes {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("tx %d has no change for %s", tx.ID, name)
	return Change{}
}

func TestRealTransactions(t *testing.T) {
	r := realReport(t)
	want := []struct {
		origin Origin
		tool   string
	}{
		{OriginUserCLI, "apt"}, {OriginUserCLI, "apt"},
		{OriginUserGUI, "synaptic"}, {OriginUserGUI, "synaptic"},
		{OriginUserCLI, "apt"},
		{OriginUnattended, "unattended-upgrade"}, {OriginUnattended, "unattended-upgrade"}, {OriginUnattended, "unattended-upgrade"},
		{OriginUserCLI, "aptitude"}, // no Commandline; aptitude report 1 s before
		{OriginUserCLI, "apt"},
	}
	if len(r.Transactions) != len(want) {
		t.Fatalf("%d transactions, want %d", len(r.Transactions), len(want))
	}
	for i, tx := range r.Transactions {
		if tx.ID != i || tx.Origin != want[i].origin || tx.Tool != want[i].tool {
			t.Errorf("tx %d: id %d origin %s tool %q, want %s %q", i, tx.ID, tx.Origin, tx.Tool, want[i].origin, want[i].tool)
		}
		if tx.Manual != (tx.Origin != OriginUnattended) {
			t.Errorf("tx %d: manual = %v", i, tx.Manual)
		}
		if strings.Join(tx.Sources, "+") != "apt+dpkg" || tx.Incomplete {
			t.Errorf("tx %d: sources %v incomplete %v", i, tx.Sources, tx.Incomplete)
		}
	}
	if !r.HasBaseline || !r.AptFrom.Equal(at("2026-03-16 12:48:52")) || r.Generated != genTime {
		t.Errorf("header: baseline %v aptFrom %v generated %v", r.HasBaseline, r.AptFrom, r.Generated)
	}

	// apt install xdotool xvfb: libxdo3 is a dependency, the others are named.
	x := r.Transactions[4]
	if c := findChange(t, x, "libxdo3"); !c.Dependency || c.Requested || c.Named {
		t.Errorf("libxdo3 = %+v", c)
	}
	if c := findChange(t, x, "xvfb"); c.Dependency || !c.Requested || !c.Named {
		t.Errorf("xvfb = %+v", c)
	}

	// aptitude run: history says lintian:amd64, dpkg says lintian:all; the change
	// takes the normalised arch and dpkg's timestamp, 25 s after Start.
	c := findChange(t, r.Transactions[8], "lintian")
	if c.Arch != "all" || !c.At.Equal(at("2026-11-13 11:58:55")) {
		t.Errorf("lintian = %+v", c)
	}

	// BST: dpkg times carry the same zone as apt's.
	g := findChange(t, r.Transactions[2], "gcolor3")
	if _, off := g.At.Zone(); off != 3600 {
		t.Errorf("gcolor3 at %v, want +01:00", g.At)
	}
	if p := findChange(t, r.Transactions[3], "gcolor3"); p.Action != Purge {
		t.Errorf("purge = %+v", p)
	}
}

func TestRealPackages(t *testing.T) {
	r := realReport(t)
	by := map[string]Package{}
	for _, p := range r.Packages {
		by[p.Name+":"+p.Arch] = p
	}
	cases := []struct {
		key              string
		installed, state string
		auto, pre        bool
		events           int
	}{
		{"bash:amd64", "5.2.21-2ubuntu4", "", true, true, 0},            // never in the logs
		{"libxdo3:amd64", "1:3.20160805.1-5build1", "", true, false, 1}, // installed in the window
		{"lintian:all", "2.117.0ubuntu1.6", "", true, true, 1},          // upgraded: existed before; extended_states says amd64
		{"libdistro-info-perl:all", "1.7ubuntu0.1", "", true, false, 1},
		{"jellyfin-ffmpeg7:amd64", "", "config-files", false, true, 0},
		{"gcolor3:amd64", "", "", false, false, 2}, // installed then purged
		{"libc6:i386", "2.39-0ubuntu8.12", "", false, true, 1},
	}
	for _, c := range cases {
		p, ok := by[c.key]
		if !ok {
			t.Errorf("%s missing", c.key)
			continue
		}
		if p.Installed != c.installed || p.State != c.state || p.AutoNow != c.auto || p.PreHistory != c.pre || len(p.Events) != c.events {
			t.Errorf("%s = %+v", c.key, p)
		}
	}
	if _, ok := by["lintian:amd64"]; ok {
		t.Error("lintian listed under amd64 too")
	}
	// Events point at the right change.
	g := by["gcolor3:amd64"]
	if ch := r.Transactions[g.Events[1].Tx].Changes[g.Events[1].Change]; ch.Name != "gcolor3" || ch.Action != Purge {
		t.Errorf("gcolor3 second event -> %+v", ch)
	}
}

func TestNoCommandlineOrigin(t *testing.T) {
	hist := read(t, "synthetic", "apthistory", "origins.log")
	report := func(when string) string {
		return "Aptitude 0.8.13: log report\n" + when + "\n[UPGRADE] firefox:amd64 140.0-1 -> 141.0-1\n"
	}
	for _, c := range []struct {
		name, aptitude string
		origin         Origin
		tool           string
	}{
		{"no report", "", OriginUserGUI, ""},
		{"report 1s before", report("Thu, Feb  5 2026 11:59:59 +0000"), OriginUserCLI, "aptitude"},
		{"report same second", report("Thu, Feb  5 2026 12:00:00 +0000"), OriginUserCLI, "aptitude"},
		{"report 3s before", report("Thu, Feb  5 2026 11:59:57 +0000"), OriginUserGUI, ""},
		{"report after", report("Thu, Feb  5 2026 12:00:01 +0000"), OriginUserGUI, ""},
	} {
		r := Build(fromText(t, hist, "", c.aptitude), genTime)
		tx := r.Transactions[0]
		if tx.Origin != c.origin || tx.Tool != c.tool {
			t.Errorf("%s: %s %q, want %s %q", c.name, tx.Origin, tx.Tool, c.origin, c.tool)
		}
	}

	r := Build(fromText(t, hist, "", ""), genTime)
	if tx := r.Transactions[1]; tx.Origin != OriginRoot || tx.Tool != "apt-get" || tx.UID != nil {
		t.Errorf("root: %+v", tx)
	} else if !findChange(t, tx, "curl").Named || findChange(t, tx, "libcurl4t64").Named {
		t.Errorf("named: %+v", tx.Changes)
	}
	if tx := r.Transactions[2]; tx.Origin != OriginUnattended || tx.Manual || tx.Changes[0].Arch != "all" {
		t.Errorf("unattended: %+v", tx)
	}
	// No dpkg log: changes are timed at Start, sources are apt only.
	if tx := r.Transactions[0]; len(tx.Sources) != 1 || !tx.Changes[0].At.Equal(tx.Start) {
		t.Errorf("apt-only: %+v", tx)
	}
}

func TestMissingEndDate(t *testing.T) {
	dpkg := "2026-02-03 08:00:01 startup archives unpack\n" +
		"2026-02-03 08:00:01 upgrade alpha:amd64 1.0-1 1.0-2\n" +
		"2026-02-03 08:00:04 startup packages configure\n" +
		"2026-02-03 08:00:04 status half-configured alpha:amd64 1.0-2\n" +
		"2026-02-03 08:10:01 startup archives unpack\n" +
		"2026-02-03 08:10:01 upgrade alpha:amd64 1.0-1 1.0-2\n" +
		"2026-02-03 08:10:01 status installed alpha:amd64 1.0-2\n"
	r := Build(fromText(t, read(t, "synthetic", "apthistory", "missing-end.log"), dpkg, ""), genTime)
	if len(r.Transactions) != 2 {
		t.Fatalf("%d transactions", len(r.Transactions))
	}
	first, second := r.Transactions[0], r.Transactions[1]
	// The open-ended stanza owns runs up to the next stanza; its End comes from them.
	if !first.Incomplete || !first.End.Equal(at("2026-02-03 08:00:04")) || len(first.Sources) != 2 {
		t.Errorf("first = %+v", first)
	}
	if second.Incomplete || !second.Changes[0].At.Equal(at("2026-02-03 08:10:01")) {
		t.Errorf("second = %+v", second)
	}

	// Without any dpkg runs, End falls back to Start.
	r = Build(fromText(t, read(t, "synthetic", "apthistory", "missing-end.log"), "", ""), genTime)
	if tx := r.Transactions[0]; !tx.Incomplete || !tx.End.Equal(tx.Start) {
		t.Errorf("no dpkg: %+v", tx)
	}
}

const twoTx = `Start-Date: 2026-04-01  10:00:00
Commandline: apt install a
Requested-By: testuser (1000)
Install: a:amd64 (1)
End-Date: 2026-04-01  10:00:05

Start-Date: 2026-04-01  10:00:06
Commandline: apt install b
Requested-By: testuser (1000)
Install: b:amd64 (1)
End-Date: 2026-04-01  10:00:10
`

func run(when, verb, pkg string) string {
	return "2026-04-01 " + when + " startup archives unpack\n" +
		"2026-04-01 " + when + " " + verb + " " + pkg + " <none> 1\n" +
		"2026-04-01 " + when + " status installed " + pkg + " 1\n"
}

func TestRunMatchingEdges(t *testing.T) {
	cases := []struct {
		name, dpkg string
		dpkgTxs    int
	}{
		// 10:00:06 is within slack of the first and inside the second: inside wins.
		{"adjacent: inside wins", run("10:00:01", "install", "a:amd64") + run("10:00:06", "install", "b:amd64"), 0},
		// 2 s before Start and 2 s after End are still in.
		{"slack edges", run("09:59:58", "install", "a:amd64") + run("10:00:12", "install", "b:amd64"), 0},
		// 3 s after the last End: a separate dpkg transaction.
		{"outside slack", run("10:00:13", "install", "c:amd64"), 1},
		{"well before", run("09:00:00", "install", "c:amd64"), 1},
	}
	for _, c := range cases {
		r := Build(fromText(t, twoTx, c.dpkg, ""), genTime)
		var apt, dp int
		for _, tx := range r.Transactions {
			if tx.Origin == OriginDpkg {
				dp++
				if tx.Tool != "dpkg" || !tx.Manual || tx.Changes[0].Name != "c" {
					t.Errorf("%s: dpkg tx %+v", c.name, tx)
				}
				continue
			}
			apt++
			// Each apt transaction got exactly its own package's run.
			if got := len(tx.Sources) == 2; got != (c.dpkgTxs == 0) {
				t.Errorf("%s: tx %s sources %v", c.name, tx.Changes[0].Name, tx.Sources)
			}
		}
		if dp != c.dpkgTxs || apt != 2 {
			t.Errorf("%s: %d apt, %d dpkg transactions", c.name, apt, dp)
		}
	}

	// Check exact placement for the adjacent case.
	r := Build(fromText(t, twoTx, run("10:00:05", "install", "a:amd64")+run("10:00:06", "install", "b:amd64"), ""), genTime)
	if a := r.Transactions[0].Changes[0]; !a.At.Equal(at("2026-04-01 10:00:05")) {
		t.Errorf("a at %v", a.At)
	}
	if b := r.Transactions[1].Changes[0]; !b.At.Equal(at("2026-04-01 10:00:06")) {
		t.Errorf("b at %v", b.At)
	}
}

func TestDpkgOnlyTransactions(t *testing.T) {
	r := Build(fromText(t, "", read(t, "synthetic", "dpkglog", "dpkg-direct.log"), ""), genTime)
	// Unpack and configure runs 0 s apart: one dpkg -i invocation.
	if len(r.Transactions) != 1 {
		t.Fatalf("%d transactions", len(r.Transactions))
	}
	tx := r.Transactions[0]
	if tx.Origin != OriginDpkg || tx.Incomplete || len(tx.Changes) != 1 || !tx.End.Equal(at("2026-02-07 15:00:01")) {
		t.Errorf("dpkg -i: %+v", tx)
	}
	if c := tx.Changes[0]; c.Action != Install || c.New != "0.4.2" || !c.Requested {
		t.Errorf("change = %+v", c)
	}

	r = Build(fromText(t, "", read(t, "synthetic", "dpkglog", "half-configured.log"), ""), genTime)
	if tx := r.Transactions[0]; !tx.Incomplete || tx.Changes[0].Action != Upgrade || tx.Changes[0].Old != "1.0-1" {
		t.Errorf("half-configured: %+v", tx)
	}

	r = Build(fromText(t, "", read(t, "synthetic", "dpkglog", "remove-purge.log"), ""), genTime)
	if tx := r.Transactions[0]; len(tx.Changes) != 1 || tx.Changes[0].Action != Purge || tx.Changes[0].Arch != "all" || tx.Changes[0].Old != "2:0.1-1" {
		t.Errorf("remove+purge: %+v", tx)
	}

	// Trigger-only runs and same-version reinstalls.
	dpkg := "2026-04-02 10:00:00 startup packages configure\n" +
		"2026-04-02 10:00:00 trigproc man-db:amd64 2.12 <none>\n" +
		"2026-04-02 11:00:00 startup archives unpack\n" +
		"2026-04-02 11:00:00 upgrade x:amd64 1 1\n"
	r = Build(fromText(t, "", dpkg, ""), genTime)
	if len(r.Transactions) != 1 || r.Transactions[0].Changes[0].Action != Reinstall {
		t.Errorf("trigger-only/reinstall: %+v", r.Transactions)
	}
}

func TestArchSwitchIsNotIncomplete(t *testing.T) {
	// linux-firmware went from amd64 to all: dpkg leaves the old instance
	// half-installed while the new one is unpacked and configured.
	hist := "Start-Date: 2026-09-04  16:27:27\nCommandline: apt upgrade\nRequested-By: testuser (1000)\n" +
		"Upgrade: linux-firmware:amd64 (20240318.git3b128b60-0ubuntu2.16, 20240318.git3b128b60-0ubuntu2.17)\n" +
		"End-Date: 2026-09-04  16:28:39\n"
	dpkg := "2026-09-04 16:27:39 startup archives unpack\n" +
		"2026-09-04 16:27:40 upgrade linux-firmware:all 20240318.git3b128b60-0ubuntu2.16 20240318.git3b128b60-0ubuntu2.17\n" +
		"2026-09-04 16:27:40 status half-installed linux-firmware:amd64 20240318.git3b128b60-0ubuntu2.16\n" +
		"2026-09-04 16:27:50 status unpacked linux-firmware:all 20240318.git3b128b60-0ubuntu2.17\n" +
		"2026-09-04 16:28:02 startup packages configure\n" +
		"2026-09-04 16:28:30 status installed linux-firmware:all 20240318.git3b128b60-0ubuntu2.17\n"
	r := Build(fromText(t, hist, dpkg, ""), genTime)
	if tx := r.Transactions[0]; tx.Incomplete || tx.Changes[0].Arch != "all" || !tx.Changes[0].At.Equal(at("2026-09-04 16:27:40")) {
		t.Errorf("tx = %+v", tx)
	}
}

func TestToolOf(t *testing.T) {
	for cmd, want := range map[string]string{
		"apt install xdotool":                                    "apt",
		"/usr/bin/unattended-upgrade":                            "unattended-upgrade",
		"/usr/bin/python3 /usr/bin/unattended-upgrade":           "unattended-upgrade",
		"/usr/bin/python3 -u /usr/bin/unattended-upgrade -v":     "unattended-upgrade",
		"/usr/sbin/synaptic":                                     "synaptic",
		"sudo DEBIAN_FRONTEND=noninteractive apt-get -y upgrade": "apt-get",
		"":        "",
		"python3": "python3",
	} {
		if got := toolOf(cmd); got != want {
			t.Errorf("toolOf(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestNamedPackages(t *testing.T) {
	got := namedPackages("apt", "apt install -y foo bar=1.2 baz/noble qux:i386 -t backports")
	for _, n := range []string{"foo", "bar", "baz", "qux"} {
		if !got[n] {
			t.Errorf("%s not named in %v", n, got)
		}
	}
	if got["install"] || got["-y"] {
		t.Errorf("verb or option counted as a package: %v", got)
	}
	if got := namedPackages("aptitude", "aptitude install g++ libfoo-"); !got["g++"] || !got["libfoo"] {
		t.Errorf("aptitude: %v", got)
	}
	if namedPackages("synaptic", "/usr/sbin/synaptic") != nil {
		t.Error("synaptic has no named packages")
	}
}

func TestRunBetweenTransactions(t *testing.T) {
	// Equally close (1 s) to the end of the first and the start of the second:
	// apt starts dpkg after Start-Date, so the later transaction wins.
	hist := strings.Replace(twoTx, "10:00:06", "10:00:07", 1)
	r := Build(fromText(t, hist, run("10:00:06", "install", "b:amd64"), ""), genTime)
	if a, b := r.Transactions[0], r.Transactions[1]; len(a.Sources) != 1 || len(b.Sources) != 2 {
		t.Errorf("run went to %v / %v", a.Sources, b.Sources)
	}
}

func TestDpkgGrouping(t *testing.T) {
	// Runs 2 s apart are one dpkg invocation; 3 s apart are two.
	dpkg := run("10:00:00", "install", "a:amd64") + run("10:00:02", "install", "b:amd64") + run("10:00:05", "install", "c:amd64")
	r := Build(fromText(t, "", strings.ReplaceAll(dpkg, "2026-04-01", "2026-04-03"), ""), genTime)
	if len(r.Transactions) != 2 || len(r.Transactions[0].Changes) != 2 || len(r.Transactions[1].Changes) != 1 {
		t.Errorf("groups: %+v", r.Transactions)
	}
}

func TestArchSwitchWithinOneRun(t *testing.T) {
	// The only run that mentions the package ends with the old arch half-installed
	// and the new one installed; map order must not matter.
	dpkg := "2026-09-04 16:27:39 startup archives install\n" +
		"2026-09-04 16:27:40 upgrade linux-firmware:all 1 2\n" +
		"2026-09-04 16:27:40 status half-installed linux-firmware:amd64 1\n" +
		"2026-09-04 16:27:50 status installed linux-firmware:all 2\n"
	for range 50 {
		r := Build(fromText(t, "", dpkg, ""), genTime)
		if r.Transactions[0].Incomplete {
			t.Fatal("arch switch flagged incomplete")
		}
	}
}

func TestBrokenFinalStates(t *testing.T) {
	for state, broken := range map[string]bool{
		"unpacked": true, "half-installed": true, "half-configured": true,
		"installed": false, "config-files": false, "triggers-pending": false,
	} {
		// apt killed after unpacking: no configure run follows.
		dpkg := "2026-04-04 10:00:00 startup archives unpack\n" +
			"2026-04-04 10:00:00 install a:amd64 <none> 1\n" +
			"2026-04-04 10:00:01 status " + state + " a:amd64 1\n"
		if got := Build(fromText(t, "", dpkg, ""), genTime).Transactions[0].Incomplete; got != broken {
			t.Errorf("final %s: incomplete = %v", state, got)
		}
	}
}

// FuzzBuild checks Build's invariants on arbitrary logs.
func FuzzBuild(f *testing.F) {
	f.Add(twoTx, run("10:00:06", "install", "b:amd64"))
	f.Add(read(f, "synthetic", "apthistory", "missing-end.log"), read(f, "synthetic", "dpkglog", "half-configured.log"))
	f.Add(read(f, "real", "var", "log", "apt", "history.log"), read(f, "real", "var", "log", "dpkg.log"))
	f.Fuzz(func(t *testing.T, hist, dpkg string) {
		var warn diag.List
		res := &collect.Result{
			AptHistory: apthistory.Parse(strings.NewReader(hist), "h", time.UTC, &warn),
			DpkgRuns:   dpkglog.Parse(strings.NewReader(dpkg), "d", time.UTC, &warn),
		}
		r := Build(res, genTime)
		if len(r.Transactions) < len(res.AptHistory) {
			t.Fatalf("lost apt transactions: %d < %d", len(r.Transactions), len(res.AptHistory))
		}
		for i, tx := range r.Transactions {
			if tx.ID != i || tx.End.Before(tx.Start) || tx.Changes == nil {
				t.Fatalf("tx %d: %+v", i, tx)
			}
			if i > 0 && tx.Start.Before(r.Transactions[i-1].Start) {
				t.Fatalf("tx %d out of order", i)
			}
		}
		for _, p := range r.Packages {
			for _, e := range p.Events {
				if c := r.Transactions[e.Tx].Changes[e.Change]; c.Name != p.Name || c.Arch != p.Arch {
					t.Fatalf("package %s:%s -> %+v", p.Name, p.Arch, c)
				}
			}
		}
	})
}
