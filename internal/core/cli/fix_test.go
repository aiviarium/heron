package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiviarium/heron/internal/core/diag"
)

// `heron lint -fix` applies a diagnostic's first fix that isn't a
// suggestion, and leaves a diagnostic whose fixes are all suggestions, such
// as a spelling correction, for the editor.
func TestFixSkipsSuggestions(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.heron")
	if err := os.WriteFile(f, []byte("| the subtree's utilize\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ds := diag.List{
		{File: f, Start: 6, End: 15, Code: "Vale.Vale.Spelling", Fixes: []diag.Fix{
			{Title: "Replace with sub tree's", Edits: []diag.Edit{{File: f, Start: 6, End: 15, New: "sub tree's"}}, Suggestion: true},
		}},
		{File: f, Start: 16, End: 23, Code: "Vale.Team.Simple", Fixes: []diag.Fix{
			{Title: "Replace with a guess", Edits: []diag.Edit{{File: f, Start: 16, End: 23, New: "guess"}}, Suggestion: true},
			{Title: "Replace with use", Edits: []diag.Edit{{File: f, Start: 16, End: 23, New: "use"}}},
		}},
	}
	rest, n, err := applyFixes(ds)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	if n != 1 || len(rest) != 1 || rest[0].Code != "Vale.Vale.Spelling" || string(b) != "| the subtree's use\n" {
		t.Fatalf("applied %d, left %v, file %q", n, rest, b)
	}
}

// TestFixSkipsOverlaps: of two fixes whose edits overlap, or start at one
// offset, the first is applied and the second's finding is left.
func TestFixSkipsOverlaps(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.heron")
	if err := os.WriteFile(f, []byte("| one two three\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fix := func(s, e int, new string) []diag.Fix {
		return []diag.Fix{{Title: new, Edits: []diag.Edit{{File: f, Start: s, End: e, New: new}}}}
	}
	ds := diag.List{
		{File: f, Start: 2, End: 9, Code: "a", Fixes: fix(2, 9, "1 2")},
		{File: f, Start: 6, End: 15, Code: "b", Fixes: fix(6, 15, "x")},
		{File: f, Start: 2, End: 2, Code: "c", Fixes: fix(2, 2, "zero ")},
		{File: f, Start: 10, End: 15, Code: "d", Fixes: fix(10, 15, "3")},
	}
	rest, n, err := applyFixes(ds)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	if n != 2 || len(rest) != 2 || rest[0].Code != "b" || rest[1].Code != "c" || string(b) != "| 1 2 3\n" {
		t.Fatalf("applied %d, left %v, file %q", n, rest, b)
	}
}
