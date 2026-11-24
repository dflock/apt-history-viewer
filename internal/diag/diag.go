// Package diag holds the non-fatal warnings produced while reading logs.
//
// Parsers never fail on bad input. They skip what they cannot understand and
// record a Warning so the problem is visible in the report and on stderr.
package diag

import "fmt"

// Warning is a non-fatal problem found in an input file.
type Warning struct {
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"` // 1-based; 0 when not tied to a line
	Msg  string `json:"msg"`
}

func (w Warning) String() string {
	switch {
	case w.File != "" && w.Line > 0:
		return fmt.Sprintf("%s:%d: %s", w.File, w.Line, w.Msg)
	case w.File != "":
		return fmt.Sprintf("%s: %s", w.File, w.Msg)
	default:
		return w.Msg
	}
}

// List accumulates warnings.
type List []Warning

// Add records a warning.
func (l *List) Add(file string, line int, format string, args ...any) {
	*l = append(*l, Warning{File: file, Line: line, Msg: fmt.Sprintf(format, args...)})
}
