package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dflock/apt-history-viewer/internal/collect"
	"github.com/dflock/apt-history-viewer/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite golden files")

// realRel is relative so golden file paths don't depend on the checkout location.
const realRel = "../../testdata/real"

func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e)
	return o.String(), e.String(), code
}

func TestGoldenJSON(t *testing.T) {
	out, errOut, code := runCLI(t, "--json", "-i", realRel)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if errOut != "" {
		t.Errorf("unexpected stderr: %s", errOut)
	}
	golden := testutil.Testdata(t, "golden", "real.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run `go test ./cmd/... -update` to create it)", err)
	}
	if out != string(want) {
		t.Errorf("output differs from %s; rerun with -update if the change is intended", golden)
	}
}

// parsed is the part of a Result that must not depend on how the input was packaged.
type parsed struct {
	Host, TZ string
	Hist     []any
	Runs     []any
	Reps     []any
}

func normalise(t *testing.T, js string) parsed {
	t.Helper()
	var r collect.Result
	if err := json.Unmarshal([]byte(js), &r); err != nil {
		t.Fatal(err)
	}
	var p parsed
	p.Host, p.TZ = r.Host, r.TZ
	// File names and line numbers legitimately differ (rotation restarts line numbers).
	for _, s := range r.AptHistory {
		s.File, s.Line = "", 0
		p.Hist = append(p.Hist, s)
	}
	for _, x := range r.DpkgRuns {
		x.File, x.Line = "", 0
		for i := range x.Events {
			x.Events[i].Line = 0
		}
		p.Runs = append(p.Runs, x)
	}
	for _, x := range r.AptitudeReports {
		x.File, x.Line = "", 0
		p.Reps = append(p.Reps, x)
	}
	return p
}

func TestAllInputFormsAgree(t *testing.T) {
	realDir := testutil.Testdata(t, "real")
	tree := testutil.ReadTree(t, realDir)

	// Rotate the logs: split dpkg.log in two, gzip the older half, as logrotate would.
	dpkg := strings.SplitAfter(string(tree["var/log/dpkg.log"]), "\n")
	half := len(dpkg) / 2
	for strings.Contains(dpkg[half], " status ") || !strings.Contains(dpkg[half], " startup ") {
		half++ // split at a run boundary so both halves parse the same as the whole
	}
	rotated := map[string][]byte{}
	for k, v := range tree {
		rotated[k] = v
	}
	rotated["var/log/dpkg.log.1.gz"] = testutil.Gzip(t, []byte(strings.Join(dpkg[:half], "")))
	rotated["var/log/dpkg.log"] = []byte(strings.Join(dpkg[half:], ""))

	flat := map[string][]byte{}
	for k, v := range tree {
		flat[filepath.Base(k)] = v
	}

	inputs := map[string]string{
		"dir":         realDir,
		"rotated dir": testutil.WriteTree(t, rotated),
		"flat dir":    testutil.WriteTree(t, flat),
		"tar":         testutil.MakeTar(t, "in.tar", false, testutil.Members(tree, "")),
		"tgz nested":  testutil.MakeTar(t, "in.tgz", true, testutil.Members(rotated, "host/")),
		"flat tar.gz": testutil.MakeTar(t, "flat.tar.gz", true, testutil.Members(flat, "")),
	}

	var want parsed
	for _, name := range []string{"dir", "rotated dir", "flat dir", "tar", "tgz nested", "flat tar.gz"} {
		out, errOut, code := runCLI(t, "--json", "-i", inputs[name])
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", name, code, errOut)
		}
		got := normalise(t, out)
		if name == "dir" {
			want = got
			if len(want.Hist) != 10 || len(want.Runs) != 24 || len(want.Reps) != 1 {
				t.Fatalf("baseline counts: %d stanzas, %d runs, %d reports", len(want.Hist), len(want.Runs), len(want.Reps))
			}
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: parsed content differs from the plain directory", name)
		}
	}
}

func TestMultipleInputsGiveArray(t *testing.T) {
	out, errOut, code := runCLI(t, "--json", "-i", realRel, "-i", testutil.Testdata(t, "synthetic", "root-packagekit"))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var rs []collect.Result
	if err := json.Unmarshal([]byte(out), &rs); err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[1].Host != "pkhost" || len(rs[1].AptHistory) != 3 {
		t.Errorf("results = %d, second host %q", len(rs), rs[1].Host)
	}
	// root-packagekit has no dpkg log and no timezone: warn, don't fail.
	for _, want := range []string{"no dpkg logs found", "timezone unknown"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
	}
}

func TestExitCodes(t *testing.T) {
	if _, _, code := runCLI(t, "-i", realRel); code != 2 {
		t.Errorf("without --json: exit %d, want 2 (HTML not implemented yet)", code)
	}
	if _, errOut, code := runCLI(t, "--json", "-i", t.TempDir()); code != 1 || !strings.Contains(errOut, "no apt or dpkg logs") {
		t.Errorf("empty dir: exit %d, %s", code, errOut)
	}
	if _, _, code := runCLI(t, "--json", "-i", filepath.Join(t.TempDir(), "missing")); code != 1 {
		t.Errorf("missing input: exit %d, want 1", code)
	}
	if _, _, code := runCLI(t, "--json", "stray"); code != 2 {
		t.Errorf("stray argument: exit %d, want 2", code)
	}
	if out, _, code := runCLI(t, "--version"); code != 0 || strings.TrimSpace(out) != "dev" {
		t.Errorf("--version: %d %q", code, out)
	}
}

func TestOverridesAndHost(t *testing.T) {
	dir := testutil.WriteTree(t, map[string][]byte{"etc/timezone": []byte("Etc/UTC\n")})
	aptDir := testutil.Testdata(t, "real", "var", "log", "apt")
	dpkgLog := testutil.Testdata(t, "real", "var", "log", "dpkg.log")
	out, errOut, code := runCLI(t, "--json", "-i", dir, "--apt-dir", aptDir, "--dpkg-log", dpkgLog, "--host", "renamed")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var r collect.Result
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.Host != "renamed" || len(r.AptHistory) != 10 || len(r.DpkgRuns) != 24 {
		t.Errorf("host %q, %d stanzas, %d runs", r.Host, len(r.AptHistory), len(r.DpkgRuns))
	}
	if r.AptHistory[0].File != filepath.Join(aptDir, "history.log") {
		t.Errorf("override file name = %q", r.AptHistory[0].File)
	}
}
