// Package render writes the self-contained HTML report (DESIGN.md §6).
//
// The page is one file: CSS, JavaScript and the Report (as JSON) are all
// inlined, so it works offline and can be mailed around. The timeline is
// drawn client-side from the JSON by assets/app.js.
package render

import (
	"bytes"
	"embed"
	"encoding/json"
	"html/template"
	"io"

	"github.com/dflock/apt-history-viewer/internal/model"
)

//go:embed assets/report.tmpl assets/app.css assets/core.js assets/app.js
var assets embed.FS

var page = template.Must(template.ParseFS(assets, "assets/report.tmpl"))

func asset(name string) string {
	b, err := assets.ReadFile("assets/" + name)
	if err != nil {
		panic(err) // embedded at build time; can't happen
	}
	return string(b)
}

var (
	css = template.CSS(asset("app.css"))
	js  = template.JS(asset("core.js") + "\n" + asset("app.js"))
)

// payload is what the page's script reads from <script id="data">.
type payload struct {
	Version string        `json:"version"`
	Report  *model.Report `json:"report"`
}

// Render writes the HTML report for r.
func Render(w io.Writer, r *model.Report, version string) error {
	// encoding/json escapes <, > and & (and U+2028/9), so log text such as a
	// command line containing "</script>" can't end the data element early.
	data, err := json.Marshal(payload{Version: version, Report: r})
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	err = page.Execute(&buf, struct {
		Host, Version string
		CSS           template.CSS
		JS            template.JS
		Data          template.JS
	}{r.Host, version, css, js, template.JS(data)})
	if err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes())
	return err
}
