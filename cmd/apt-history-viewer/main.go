// Command apt-history-viewer reads apt and dpkg logs and writes a report of a
// machine's package history. See DESIGN.md.
//
// By default it writes a self-contained HTML report per input. --json writes
// the correlated Report instead, and --raw the uncorrelated parse of every log.
package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // logs from other machines may name zones this host lacks

	"github.com/dflock/apt-history-viewer/internal/collect"
	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/model"
	"github.com/dflock/apt-history-viewer/internal/render"
	"github.com/dflock/apt-history-viewer/internal/source"
)

var version = "dev" // set with -ldflags "-X main.version=…"

// now is the report's generation time; tests pin it.
var now = time.Now

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type options struct {
	inputs  stringList
	output  string
	json    bool
	raw     bool
	host    string
	ov      collect.Overrides
	version bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	var o options
	fl := flag.NewFlagSet("apt-history-viewer", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Var(&o.inputs, "input", "input root: directory or .tar/.tar.gz/.tgz (default /; repeatable)")
	fl.Var(&o.inputs, "i", "shorthand for --input")
	fl.StringVar(&o.output, "output", "", `output file ("-" = stdout); a directory with several --input.
Default: ./apt-history-<host>.html, or stdout for --json/--raw`)
	fl.StringVar(&o.output, "o", "", "shorthand for --output")
	fl.BoolVar(&o.json, "json", false, "write the report as JSON")
	fl.BoolVar(&o.raw, "raw", false, "write the uncorrelated parse of every log as JSON (debugging)")
	fl.StringVar(&o.host, "host", "", "override the host name shown in the report")
	fl.StringVar(&o.ov.AptDir, "apt-dir", "", "override: directory holding history.log*")
	fl.StringVar(&o.ov.DpkgLog, "dpkg-log", "", "override: path of dpkg.log (rotations found alongside)")
	fl.StringVar(&o.ov.DpkgStatus, "dpkg-status", "", "override: path of dpkg's status file")
	fl.BoolVar(&o.version, "version", false, "print version and exit")
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fl.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fl.Args(), " "))
		return 2
	}
	if o.version {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if o.json && o.raw {
		fmt.Fprintln(stderr, "--json and --raw can't be used together")
		return 2
	}
	if len(o.inputs) == 0 {
		o.inputs = stringList{"/"}
	}
	if o.ov != (collect.Overrides{}) && len(o.inputs) > 1 {
		fmt.Fprintln(stderr, "--apt-dir, --dpkg-log and --dpkg-status can only be used with a single --input")
		return 2
	}

	var results []any
	anyData := false
	for _, in := range o.inputs {
		var warn diag.List
		src, err := source.Open(in, &warn)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		res := collect.Collect(src, o.ov, &warn)
		if o.host != "" {
			res.Host = o.host
		}
		for _, w := range res.Warnings {
			fmt.Fprintf(stderr, "warning: %s\n", w)
		}
		anyData = anyData || !res.Empty()
		if o.raw {
			results = append(results, res)
		} else {
			results = append(results, model.Build(res, now()))
		}
	}
	if !anyData {
		fmt.Fprintln(stderr, "error: no apt or dpkg logs could be read")
		return 1
	}

	if !o.json && !o.raw {
		return writeHTML(o.output, stdout, stderr, results)
	}
	var payload any = results
	if len(results) == 1 {
		payload = results[0]
	}
	if err := writeJSON(o.output, stdout, payload); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// writeHTML writes one report per input: to out (or ./apt-history-<host>.html)
// for a single input, or into the directory out (default ".") for several.
func writeHTML(out string, stdout, stderr io.Writer, results []any) int {
	if len(results) > 1 && out == "-" {
		fmt.Fprintln(stderr, "--output - (stdout) only works with a single --input")
		return 2
	}
	dir := ""
	if len(results) > 1 {
		dir = cmp.Or(out, ".")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}
	used := map[string]bool{}
	for _, x := range results {
		r := x.(*model.Report)
		if len(results) == 1 && out == "-" {
			if err := render.Render(stdout, r, version); err != nil {
				fmt.Fprintf(stderr, "error: %v\n", err)
				return 1
			}
			continue
		}
		path := out
		if dir != "" || path == "" {
			path = filepath.Join(dir, uniqueName(fileName(r.Host), used))
		}
		if err := writeFile(path, func(w io.Writer) error { return render.Render(w, r, version) }); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "wrote %s (%d transactions)\n", path, len(r.Transactions))
	}
	return 0
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func fileName(host string) string {
	h := strings.Trim(unsafeChars.ReplaceAllString(host, "_"), "._")
	return "apt-history-" + cmp.Or(h, "unknown")
}

// uniqueName returns base.html, or base-2.html etc. if already used (two
// inputs from the same host).
func uniqueName(base string, used map[string]bool) string {
	name := base + ".html"
	for i := 2; used[name]; i++ {
		name = fmt.Sprintf("%s-%d.html", base, i)
	}
	used[name] = true
	return name
}

// writeFile writes via a temporary file and renames it into place, so a
// failed run never leaves a truncated report behind.
func writeFile(path string, write func(io.Writer) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".apt-history-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func writeJSON(out string, stdout io.Writer, v any) error {
	w := stdout
	if out != "" && out != "-" {
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
