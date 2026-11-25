// Package diag holds the diagnostics every stage reports: syntax errors,
// resolution and type errors, and lint findings share one shape, so the CLI,
// `--json` and the language server print them the same way.
package diag

import (
	"fmt"
	"sort"
	"strings"
)

// Severity orders diagnostics; errors fail `heron check`.
type Severity int

const (
	Error Severity = iota
	Warning
	Hint
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	default:
		return "hint"
	}
}

// MarshalText makes severities read as words in JSON.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Related points at a second place a diagnostic concerns, such as the other
// side of a conflict.
type Related struct {
	File    string `json:"file"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Message string `json:"message"`
}

// Diagnostic is one finding with the byte range it concerns.
type Diagnostic struct {
	File     string    `json:"file"`
	Start    int       `json:"start"`
	End      int       `json:"end"`
	Severity Severity  `json:"severity"`
	Code     string    `json:"code"` // a stable name, such as "unresolved" or a lint's name
	Message  string    `json:"message"`
	Related  []Related `json:"related,omitempty"`
	Fixes    []Fix     `json:"fixes,omitempty"`
}

// Fix is a change that resolves a diagnostic: edits applied together. A
// suggestion is one guess among others, such as a spelling correction: an
// editor offers it, and `heron lint -fix` never applies it.
type Fix struct {
	Title      string `json:"title"`
	Edits      []Edit `json:"edits"`
	Suggestion bool   `json:"suggestion,omitempty"`
}

// Edit replaces a file's byte range with new text.
type Edit struct {
	File  string `json:"file"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	New   string `json:"new"`
}

// List collects diagnostics.
type List []Diagnostic

// Add appends a diagnostic.
func (l *List) Add(d Diagnostic) { *l = append(*l, d) }

// Errorf appends an error.
func (l *List) Errorf(file string, start, end int, code, format string, args ...any) {
	l.Add(Diagnostic{File: file, Start: start, End: end, Severity: Error, Code: code, Message: fmt.Sprintf(format, args...)})
}

// HasErrors reports whether any diagnostic is an error.
func (l List) HasErrors() bool {
	for _, d := range l {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// Sort orders diagnostics by file, offset and message, and drops exact
// duplicates, so output is deterministic.
func (l List) Sort() List {
	sort.SliceStable(l, func(i, j int) bool {
		a, b := l[i], l[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.Message < b.Message
	})
	out := l[:0]
	for i, d := range l {
		if i > 0 && sameDiag(d, out[len(out)-1]) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func sameDiag(a, b Diagnostic) bool {
	return a.File == b.File && a.Start == b.Start && a.End == b.End && a.Code == b.Code && a.Message == b.Message
}

// Format prints a diagnostic as file:line:col: severity: message [code],
// given a function that maps a file and offset to a line and column.
func Format(d Diagnostic, pos func(file string, off int) (int, int)) string {
	line, col := pos(d.File, d.Start)
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d:%d: %s: %s [%s]", d.File, line, col, d.Severity, d.Message, d.Code)
	for _, r := range d.Related {
		l, c := pos(r.File, r.Start)
		fmt.Fprintf(&b, "\n\t%s:%d:%d: %s", r.File, l, c, r.Message)
	}
	return b.String()
}
