package lint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/helightdev/specs-v0/internal/core/check"
	"github.com/helightdev/specs-v0/internal/core/diag"
	"github.com/helightdev/specs-v0/internal/core/registry"
	"github.com/helightdev/specs-v0/internal/core/resolve"
)

// The fixtures that check clean also lint clean: a design example that a
// core lint flags is either a lint bug or an example to fix. A block whose
// finding is intended goes in fixtures/lint-allowed.txt; HERON_LINT=1 logs
// every finding, allowed ones included.
func TestCleanFixturesLintClean(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	allowed := map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(root, "fixtures", "lint-allowed.txt")); err == nil {
		for _, k := range fields(string(b)) {
			allowed[k] = true
		}
	}
	r := registry.New()
	Register(r)
	linted := map[*check.Result]diag.List{}
	for _, run := range check.CheckFixtures(t, root) {
		if run.Pending != "" || len(run.Errs) > 0 || run.Result == nil {
			continue
		}
		ds, ok := linted[run.Result]
		if !ok {
			ds = Run(r, run.Result)
			linted[run.Result] = ds
		}
		for _, d := range ds {
			if resolve.FileID(d.File) != run.File {
				continue
			}
			msg := run.Block.Key() + ": " + diag.Format(d, run.Session.Pos)
			switch {
			case os.Getenv("HERON_LINT") != "":
				t.Log(msg)
			case !allowed[run.Block.Key()]:
				t.Error(msg)
			}
		}
	}
}

// fields returns the first word of each line, without # comments.
func fields(s string) []string {
	var out []string
	for _, line := range splitLines(s) {
		for i, c := range line {
			if c == '#' {
				line = line[:i]
				break
			}
		}
		var w []byte
		for i := 0; i < len(line); i++ {
			if line[i] == ' ' || line[i] == '\t' {
				if len(w) > 0 {
					break
				}
				continue
			}
			w = append(w, line[i])
		}
		if len(w) > 0 {
			out = append(out, string(w))
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
