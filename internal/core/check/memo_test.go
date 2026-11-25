package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/helightdev/specs-v0/internal/core/resolve"
)

// Results are memoized per file and per resolved node by content hash: an
// edit to one node's prose rechecks that node and reparses that file only,
// and an edit to structure rechecks the tree.
func TestIncrementalRecomputation(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644)
	}
	write("index.heron", "import = [\"std/doc@1\"]\ninclude = [\"./a.heron\", \"./b.heron\"]\n")
	write("a.heron", "one: section {{ One. }}\ntwo: section {{ Two. }}\n")
	write("b.heron", "three: section {{ Three, after [one](#one). }}\n")
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	s := NewSession()

	r := s.Check(id)
	if len(r.Diags) != 0 {
		t.Fatalf("diagnostics: %v", r.Diags)
	}
	first := s.Recomputed
	if first != 4 { // three nodes and the root
		t.Fatalf("first check computed %d nodes, want 4", first)
	}
	parses := s.Loader.Parses

	// Nothing changed: nothing is recomputed or reparsed.
	s.Check(id)
	if s.Recomputed != first || s.Loader.Parses != parses {
		t.Fatalf("an unchanged tree recomputed %d nodes and %d parses", s.Recomputed-first, s.Loader.Parses-parses)
	}

	// Prose in the last node of a.heron: one parse, one node.
	write("a.heron", "one: section {{ One. }}\ntwo: section {{ Two, changed. }}\n")
	s.Check(id)
	if got := s.Recomputed - first; got != 1 {
		t.Errorf("a prose edit recomputed %d nodes, want 1", got)
	}
	if got := s.Loader.Parses - parses; got != 1 {
		t.Errorf("a prose edit reparsed %d files, want 1", got)
	}

	// A broken link in b.heron is found by rechecking b's node alone.
	before := s.Recomputed
	write("b.heron", "three: section {{ Three, after [one](#onee). }}\n")
	r = s.Check(id)
	if got := s.Recomputed - before; got != 1 || len(r.Diags) != 1 {
		t.Errorf("recomputed %d nodes with %v", got, r.Diags)
	}

	// Structure changes: renaming a node rechecks every node.
	before = s.Recomputed
	write("a.heron", "uno: section {{ One. }}\ntwo: section {{ Two, changed. }}\n")
	r = s.Check(id)
	if got := s.Recomputed - before; got != 4 {
		t.Errorf("a structural edit recomputed %d nodes, want 4", got)
	}
	if len(r.Diags) != 1 {
		t.Errorf("want the broken link only: %v", r.Diags)
	}
}
