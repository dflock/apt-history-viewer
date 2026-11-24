package source

import (
	"archive/tar"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/testutil"
)

var wantRealLayout = Layout{
	AptDir:         "var/log/apt",
	DpkgLogDir:     "var/log",
	AptitudeDir:    "var/log",
	DpkgStatus:     "var/lib/dpkg/status",
	ExtendedStates: "var/lib/apt/extended_states",
	Hostname:       "etc/hostname",
	Timezone:       "etc/timezone",
}

func TestDirCanonicalLayout(t *testing.T) {
	var warn diag.List
	s, err := Open(testutil.Testdata(t, "real"), &warn)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tar {
		t.Error("directory reported as tar")
	}
	if s.Layout != wantRealLayout {
		t.Errorf("layout:\n got %+v\nwant %+v", s.Layout, wantRealLayout)
	}
	if h := s.Host(); h != "testhost" {
		t.Errorf("host = %q", h)
	}
	if loc, name := s.Location(&warn); loc.String() != "Europe/London" || name != "Europe/London" {
		t.Errorf("location = %v, %q", loc, name)
	}
	if len(warn) != 0 {
		t.Errorf("unexpected warnings: %v", warn)
	}
}

// flatten copies testdata/real into a single directory, as someone might scp them.
func flatten(t *testing.T) map[string][]byte {
	flat := map[string][]byte{}
	for p, b := range testutil.ReadTree(t, testutil.Testdata(t, "real")) {
		flat[filepath.Base(p)] = b
	}
	return flat
}

func TestFlatDir(t *testing.T) {
	var warn diag.List
	s, err := Open(testutil.WriteTree(t, flatten(t)), &warn)
	if err != nil {
		t.Fatal(err)
	}
	want := Layout{
		AptDir: ".", DpkgLogDir: ".", AptitudeDir: ".",
		DpkgStatus: "status", ExtendedStates: "extended_states",
		Hostname: "hostname", Timezone: "timezone",
	}
	if s.Layout != want {
		t.Errorf("layout:\n got %+v\nwant %+v", s.Layout, want)
	}
	if h := s.Host(); h != "testhost" {
		t.Errorf("host = %q", h)
	}
}

func TestTarInputs(t *testing.T) {
	real := testutil.ReadTree(t, testutil.Testdata(t, "real"))
	tests := []struct {
		name, file, prefix string
		gz                 bool
		flat               bool
	}{
		{"tar at root", "in.tar", "", false, false},
		{"tgz nested", "in.tgz", "myhost/", true, false},
		{"tar.gz with ./", "in.tar.gz", "./", true, false},
		{"tar absolute paths", "abs.tar", "/", false, false},
		{"flat tgz", "flat.tgz", "logs/", true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := real
			if tc.flat {
				files = flatten(t)
			}
			p := testutil.MakeTar(t, tc.file, tc.gz, testutil.Members(files, tc.prefix))
			var warn diag.List
			s, err := Open(p, &warn)
			if err != nil {
				t.Fatal(err)
			}
			if !s.Tar {
				t.Error("not reported as tar")
			}
			if len(warn) != 0 {
				t.Errorf("unexpected warnings: %v", warn)
			}
			if h := s.Host(); h != "testhost" {
				t.Errorf("host = %q", h)
			}
			// Every file the layout points at must have the same bytes as the original.
			got, err := fs.ReadFile(s.FS, s.Layout.DpkgStatus)
			if err != nil {
				t.Fatalf("status: %v (layout %+v)", err, s.Layout)
			}
			if string(got) != string(real["var/lib/dpkg/status"]) {
				t.Error("status content differs")
			}
			hist, err := fs.ReadFile(s.FS, s.Layout.AptDir+"/history.log")
			if err != nil || string(hist) != string(real["var/log/apt/history.log"]) {
				t.Errorf("history.log differs or missing: %v", err)
			}
		})
	}
}

func TestTarUnsafeMembers(t *testing.T) {
	old := MaxMemberSize
	MaxMemberSize = 1024
	t.Cleanup(func() { MaxMemberSize = old })

	p := testutil.MakeTar(t, "evil.tar", false, []testutil.Member{
		{Name: "../escape/history.log", Data: []byte("x")},
		{Name: "a/../../escape2/dpkg.log", Data: []byte("x")},
		{Name: "var/log/dpkg.log", Typeflag: tar.TypeSymlink, Linkname: "/etc/shadow"},
		{Name: "var/log/apt/history.log", Typeflag: tar.TypeLink, Linkname: "etc/passwd"},
		{Name: "var/lib/dpkg/status", Data: []byte(strings.Repeat("x", 2048))},
		{Name: "home/user/secret.txt", Data: []byte("not a log")},
		{Name: "etc/hostname", Data: []byte("evilhost\n")},
	})
	var warn diag.List
	s, err := Open(p, &warn)
	if err != nil {
		t.Fatal(err)
	}
	mfs := s.FS.(fstest.MapFS)
	if len(mfs) != 1 || mfs["etc/hostname"] == nil {
		names := []string{}
		for n := range mfs {
			names = append(names, n)
		}
		t.Errorf("kept members %v, want only etc/hostname", names)
	}
	joined := ""
	for _, w := range warn {
		joined += w.String() + "\n"
	}
	for _, want := range []string{"unsafe path \"../escape/history.log\"", "unsafe path \"a/../../escape2/dpkg.log\"", "exceeds the 1024 byte limit"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning containing %q in:\n%s", want, joined)
		}
	}
}

func TestNotAnArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(p, []byte("hello, this is not a tar file at all\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warn diag.List
	if _, err := Open(p, &warn); err == nil {
		t.Error("expected an error for a non-archive file")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing"), &warn); err == nil {
		t.Error("expected an error for a missing path")
	}
}

func TestPickPrefersConventionalThenShallowest(t *testing.T) {
	fsys := fstest.MapFS{
		"x/status":                          {Data: []byte("a")}, // shallower, but not conventional
		"deep/root/var/lib/dpkg/status":     {Data: []byte("b")},
		"deep/root/var/log/apt/history.log": {Data: []byte("c")},
		"other/history.log":                 {Data: []byte("d")},
		"hostname":                          {Data: []byte("h")},
		"deep/root/etc/hostname":            {Data: []byte("h2")},
	}
	l := detectWalk(fsys, -1)
	if l.DpkgStatus != "deep/root/var/lib/dpkg/status" {
		t.Errorf("status = %q", l.DpkgStatus)
	}
	if l.AptDir != "deep/root/var/log/apt" {
		t.Errorf("aptDir = %q", l.AptDir)
	}
	if l.Hostname != "deep/root/etc/hostname" {
		t.Errorf("hostname = %q", l.Hostname)
	}
}

func TestHostFallbackAndUnknownTimezone(t *testing.T) {
	dir := testutil.WriteTree(t, map[string][]byte{"var/log/apt/history.log": []byte("\n")})
	var warn diag.List
	s, err := Open(dir, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if h := s.Host(); h != filepath.Base(dir) {
		t.Errorf("host = %q, want input basename", h)
	}
	if loc, _ := s.Location(&warn); loc.String() != "UTC" {
		t.Errorf("location = %v, want UTC fallback", loc)
	}
	if len(warn) != 1 || !strings.Contains(warn[0].Msg, "timezone unknown") {
		t.Errorf("warnings = %v", warn)
	}

	p := testutil.MakeTar(t, "server01.tgz", true, []testutil.Member{{Name: "dpkg.log", Data: []byte("")}})
	s, err = Open(p, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if h := s.Host(); h != "server01" {
		t.Errorf("tar host = %q, want server01", h)
	}
}
