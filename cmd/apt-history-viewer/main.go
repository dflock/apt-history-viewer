// Command apt-history-viewer reads apt and dpkg logs and writes a report of a
// machine's package history. See DESIGN.md.
//
// --json writes the correlated Report (Milestone 2); --raw writes the
// uncorrelated parse of every log. The HTML report arrives in Milestone 3.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	_ "time/tzdata" // logs from other machines may name zones this host lacks

	"github.com/dflock/apt-history-viewer/internal/collect"
	"github.com/dflock/apt-history-viewer/internal/diag"
	"github.com/dflock/apt-history-viewer/internal/model"
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
	fl.StringVar(&o.output, "output", "", `output path ("-" = stdout; default stdout for --json)`)
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
	if !o.json && !o.raw {
		fmt.Fprintln(stderr, "the HTML report is not implemented yet (Milestone 3); use --json")
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
