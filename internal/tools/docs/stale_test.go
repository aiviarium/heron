package docs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/gen"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// A run removes the pages the previous run wrote and this one doesn't,
// and keeps the files no manifest lists.
func TestStalePagesRemoved(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "index.heron")
	run := func(src string) {
		t.Helper()
		if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		id, err := resolve.Canonical(entry)
		if err != nil {
			t.Fatal(err)
		}
		res := check.NewSession().Check(id)
		if res.Diags.HasErrors() {
			t.Fatalf("doesn't check: %v", res.Diags)
		}
		r := registry.New()
		Register(r)
		if _, err := gen.Run(context.Background(), r, &gen.Input{Tree: res.Tree, Checker: res.Checker, Dir: dir}, []string{"docs"}); err != nil {
			t.Fatal(err)
		}
	}
	const head = "import = [\"std/doc@1\"]\n\n"
	run(head + "a: section {\n  | A.\n}\n\nb: section {\n  | B.\n}\n")
	out := filepath.Join(dir, OutDir)
	if _, err := os.Stat(filepath.Join(out, "b.html")); err != nil {
		t.Fatal("no b.html after the first run")
	}
	mine := filepath.Join(out, "notes.txt")
	os.WriteFile(mine, []byte("mine"), 0o644)
	run(head + "a: section {\n  | A.\n}\n")
	if _, err := os.Stat(filepath.Join(out, "b.html")); !os.IsNotExist(err) {
		t.Error("b.html stays after its section is gone")
	}
	if _, err := os.Stat(filepath.Join(out, "a.html")); err != nil {
		t.Error("a.html is gone")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Error("a file no manifest lists was removed")
	}
}
