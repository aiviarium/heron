package docs

import (
	"go/build"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/resolve"
)

const modulePath = "github.com/aiviarium/heron"

// TestSpecComponentUses keeps unit 1's components honest: every Go package
// of internal/core (outside an internal/ folder and other than a command)
// is a component of the project tree's architecture, and each such
// component's uses names exactly the components its package imports, by
// the code's own imports without tests.
func TestSpecComponentUses(t *testing.T) {
	// The repository's own tree, whose project tree holds the components.
	s := site(t, "../../../index.heron")
	root := filepath.Join("..", "..", "..")
	byPath := map[string]*resolve.Node{}
	var core []*resolve.Node
	s.Tree.Walk(func(x *resolve.Node) {
		if !own(x) || s.kindPath(x) != "tech.component" {
			return
		}
		p, _ := argString(kindArgs(x), 0, "path")
		lang, _ := argString(kindArgs(x), 1, "lang")
		if lang != "go" {
			return
		}
		byPath[p] = x
		if p == "std" || strings.HasPrefix(p, "internal/core/") {
			core = append(core, x)
		}
	})
	if len(core) == 0 {
		t.Skip("the project tree declares no components of internal/core yet")
	}
	// The component an import belongs to: the one at the longest path the
	// import starts with.
	owner := func(imp string) *resolve.Node {
		rel, ok := strings.CutPrefix(imp, modulePath+"/")
		if !ok {
			return nil
		}
		for p := rel; p != "." && p != ""; p = filepath.Dir(p) {
			if c := byPath[p]; c != nil {
				return c
			}
		}
		return nil
	}
	for _, c := range core {
		p, _ := argString(kindArgs(c), 0, "path")
		pkg, err := build.Default.ImportDir(filepath.Join(root, p), 0)
		if err != nil {
			t.Errorf("%s: %v", c.Path(), err)
			continue
		}
		want := map[string]bool{}
		for _, imp := range pkg.Imports {
			if o := owner(imp); o != nil && o != c {
				want[o.Path()] = true
			}
		}
		got := map[string]bool{}
		for _, u := range s.componentUses(c) {
			got[u.Path()] = true
		}
		if missing, extra := diffSets(want, got), diffSets(got, want); len(missing)+len(extra) > 0 {
			t.Errorf("%s: uses should add %v and drop %v, to match the imports of %s", c.Path(), missing, extra, p)
		}
	}
	err := filepath.WalkDir(filepath.Join(root, "internal", "core"), func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		if d.Name() == "internal" || d.Name() == "testdata" {
			return filepath.SkipDir
		}
		pkg, err := build.Default.ImportDir(path, 0)
		if err != nil || pkg.Name == "main" {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if byPath[filepath.ToSlash(rel)] == nil {
			t.Errorf("package %s has no component in the project tree", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func diffSets(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
