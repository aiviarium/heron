package check_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
	"github.com/aiviarium/heron/std"
)

// Every standard package is read and checked like any other: a tree that
// imports all of them resolves with no diagnostics, and each package's own
// declarations type-check clean.
func TestEveryStdPackageChecks(t *testing.T) {
	dirs, err := fs.ReadDir(std.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var imports []string
	for _, d := range dirs {
		if d.IsDir() {
			imports = append(imports, `"std/`+d.Name()+`@1"`)
		}
	}
	if len(imports) < 18 {
		t.Fatalf("only %d standard packages: %v", len(imports), imports)
	}
	p := filepath.Join(t.TempDir(), "index.heron")
	src := "import = [" + strings.Join(imports, ", ") + "]\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	id, _ := resolve.Canonical(p)
	res := check.NewSession().Check(id)
	for _, d := range res.Diags {
		t.Errorf("%s: %s", d.Code, d.Message)
	}
	// Each package's own declarations type-check as an entry point's do.
	for _, pkg := range res.Tree.Imports {
		c := types.New(pkg.Tree)
		for _, n := range append(append([]*resolve.Node(nil), pkg.Tree.Root.Children...), pkg.Tree.Root) {
			for _, d := range c.CheckNode(n) {
				t.Errorf("%s: %s: %s", pkg.Name, d.Code, d.Message)
			}
		}
	}
}
