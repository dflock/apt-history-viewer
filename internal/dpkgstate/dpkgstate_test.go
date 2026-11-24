package dpkgstate

import (
	"os"
	"strings"
	"testing"

	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/testutil"
)

func open(t *testing.T, elem ...string) *os.File {
	t.Helper()
	f, err := os.Open(testutil.Testdata(t, elem...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestRealStatus(t *testing.T) {
	var warn diag.List
	es := ParseStatus(open(t, "real", "var", "lib", "dpkg", "status"), "status", &warn)
	if len(warn) != 0 || len(es) != 16 {
		t.Fatalf("entries=%d warn=%v", len(es), warn)
	}
	by := map[string]Entry{}
	for _, e := range es {
		by[e.Name+":"+e.Arch] = e
	}
	if e := by["jellyfin-ffmpeg7:amd64"]; e.State != "config-files" || e.Installed() {
		t.Errorf("jellyfin = %+v", e)
	}
	if e := by["lintian:all"]; e.Version != "2.117.0ubuntu1.6" || !e.Installed() {
		t.Errorf("lintian = %+v", e)
	}
	if _, ok := by["libc6:i386"]; !ok {
		t.Error("libc6:i386 missing (multiarch)")
	}
	if e := by["spotify-client:amd64"]; e.Version != "1:1.3.4.258.gc60b10b0946a" {
		t.Errorf("spotify = %+v (epoch)", e)
	}
}

func TestStatusOddities(t *testing.T) {
	in := "Package: gone\nStatus: purge ok not-installed\n\n" +
		"Status: install ok installed\nVersion: 1\n\n" + // no Package
		"Package: bad\nStatus: weird\n\n" +
		"Package: ok\r\nStatus: install ok half-configured\r\nArchitecture: amd64\r\nVersion: 2\r\n" +
		"Description: x\r\n multi\r\n .\r\n" +
		"garbage line\n"
	var warn diag.List
	es := ParseStatus(strings.NewReader(in), "s", &warn)
	if len(es) != 1 || es[0] != (Entry{Name: "ok", Arch: "amd64", Version: "2", State: "half-configured"}) {
		t.Errorf("entries = %+v", es)
	}
	if len(warn) != 3 {
		t.Errorf("warnings = %v, want 3", warn)
	}
}

func TestRealExtendedStates(t *testing.T) {
	var warn diag.List
	as := ParseExtendedStates(open(t, "real", "var", "lib", "apt", "extended_states"), "es", &warn)
	if len(warn) != 0 || len(as) != 8 {
		t.Fatalf("auto=%v warn=%v", as, warn)
	}
	// extended_states records arch-all packages under the native arch.
	if as[2] != (Auto{Name: "lintian", Arch: "amd64"}) {
		t.Errorf("as[2] = %+v", as[2])
	}
}

func TestExtendedStatesManual(t *testing.T) {
	in := "Package: a\nArchitecture: amd64\nAuto-Installed: 0\n\nPackage: b\nAuto-Installed: 1\n"
	var warn diag.List
	as := ParseExtendedStates(strings.NewReader(in), "es", &warn)
	if len(as) != 1 || as[0].Name != "b" || len(warn) != 0 {
		t.Errorf("auto=%v warn=%v", as, warn)
	}
}

func FuzzParseStatus(f *testing.F) {
	f.Add("Package: a\nStatus: install ok installed\n\n")
	f.Fuzz(func(t *testing.T, in string) {
		var warn diag.List
		for _, e := range ParseStatus(strings.NewReader(in), "fuzz", &warn) {
			if e.Name == "" || e.State == "" || e.State == "not-installed" {
				t.Errorf("bad entry %+v", e)
			}
		}
		ParseExtendedStates(strings.NewReader(in), "fuzz", &warn)
	})
}
