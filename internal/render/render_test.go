package render

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/dflock/apt-history-viewer/internal/model"
)

func sample() *model.Report {
	at := time.Date(2026, 5, 10, 9, 2, 18, 0, time.FixedZone("BST", 3600))
	return &model.Report{
		Host: `evil<b>"host"</b>`,
		TZ:   "Europe/London",
		Transactions: []model.Transaction{{
			Start: at, End: at.Add(2 * time.Second), Origin: model.OriginUserCLI, Manual: true,
			Tool:        "apt",
			Commandline: `apt install x </script><script>alert(1)</script> <!-- & ` + " ",
			Changes:     []model.Change{{Name: "x", Arch: "amd64", Action: model.Install, New: "1", Requested: true, At: at}},
			Sources:     []string{"apt"},
		}},
		Packages: []model.Package{},
		Warnings: nil,
	}
}

var dataRe = regexp.MustCompile(`(?s)<script type="application/json" id="data">(.*?)</script>`)

func TestRenderEmbedsReportSafely(t *testing.T) {
	var buf bytes.Buffer
	r := sample()
	if err := Render(&buf, r, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	html := buf.String()

	// Exactly two <script> elements: data and code. Log text must not add any.
	if n := strings.Count(strings.ToLower(html), "<script"); n != 2 {
		t.Errorf("%d <script> tags, want 2 (data + code)", n)
	}
	if strings.Contains(html, "alert(1)</script>") || strings.Contains(html, "<b>") {
		t.Error("log text was not escaped")
	}
	if !strings.Contains(html, "<title>Package history: evil&lt;b&gt;&#34;host&#34;&lt;/b&gt;</title>") {
		t.Errorf("title not escaped: %s", regexp.MustCompile(`<title>.*</title>`).FindString(html))
	}

	m := dataRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatal("data element not found")
	}
	var got struct {
		Version string       `json:"version"`
		Report  model.Report `json:"report"`
	}
	if err := json.Unmarshal([]byte(m[1]), &got); err != nil {
		t.Fatalf("embedded JSON: %v", err)
	}
	if got.Version != "v1.2.3" || got.Report.Transactions[0].Commandline != r.Transactions[0].Commandline {
		t.Errorf("round trip: %q %q", got.Version, got.Report.Transactions[0].Commandline)
	}
	// The page shows the machine's wall clock, which JS reads from the offset form.
	if !strings.Contains(m[1], `"start":"2026-05-10T09:02:18+01:00"`) {
		t.Error("times must keep their local offset")
	}
	for _, need := range []string{`id="timeline"`, `id="overview"`, `id="detail"`, `id="pkg-q"`, `id="tx-table"`, `id="f-text"`, "window.AHV", "prefers-color-scheme"} {
		if !strings.Contains(html, need) {
			t.Errorf("page lacks %s", need)
		}
	}
}

func TestEmptyReportRenders(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, &model.Report{Host: "h", Transactions: []model.Transaction{}, Packages: []model.Package{}}, "dev"); err != nil {
		t.Fatal(err)
	}
}

// TestJavaScript checks the page's script parses and runs the core.js unit
// tests. Skipped when node isn't installed; the Go build doesn't need it.
func TestJavaScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	f := filepath.Join(t.TempDir(), "page.js")
	if err := os.WriteFile(f, []byte(string(js)), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil {
		t.Fatalf("embedded JS has a syntax error:\n%s", out)
	}
	if out, err := exec.Command(node, "--test", "assets/core.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("core.js tests failed:\n%s", out)
	}
}
