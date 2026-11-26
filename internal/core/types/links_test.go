package types

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// checkDir resolves and checks the index.heron of a folder of files.
func checkDir(t *testing.T, files map[string]string) (*Checker, diag.List) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	tr := resolve.New(resolve.NewLoader()).Resolve(id)
	c, ds := Check(tr)
	return c, append(append(diag.List{}, tr.Diags...), ds...)
}

// A link's path resolves lexically first, then by suffix, name by name;
// a short link that names nothing is an error with the closest name, and
// one that matches two nodes is an ambiguity with a suggested fix per
// candidate, which `heron lint -fix` doesn't apply.
func TestLinkLookup(t *testing.T) {
	c, ds := checkDir(t, map[string]string{"index.heron": `import = ["std/doc@1"]

a: section {
  thing: term {{ One. }}
  other: term {{ Only here. }}
}

b: section {
  | Here [thing] is b's own, by lexical lookup.

  thing: term {{ Two. }}
  my_thing: term {{ Three. }}
}

c: section {
  | By suffix: [other], [` + "`a.other`" + `], [b.my_thing] and [its words](#a.other).
  | Ambiguous: [thing]. Unknown: [nothng], [hing].
}
`})
	var msgs []string
	var amb diag.Diagnostic
	for _, d := range ds {
		msgs = append(msgs, d.Code+": "+d.Message)
		if d.Code == "ambiguous" {
			amb = d
		}
	}
	want := []string{
		"ambiguous: thing names 2 nodes: a.thing, b.thing; write a longer path",
		"unresolved: [nothng] names no node",
		"unresolved: [hing] names no node; did you mean thing?",
	}
	if strings.Join(msgs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(msgs, "\n"), strings.Join(want, "\n"))
	}
	if len(amb.Fixes) != 2 || !amb.Fixes[0].Suggestion || amb.Fixes[0].Edits[0].New != "a.thing" || amb.Fixes[1].Edits[0].New != "b.thing" {
		t.Errorf("fixes: %+v", amb.Fixes)
	}
	found := map[string]bool{}
	for p, n := range c.Refs {
		found[strings.Join(resolve.PathSegs(p), ".")+"→"+n.Path()] = true
	}
	for _, w := range []string{"thing→b.thing", "other→a.other", "a.other→a.other", "b.my_thing→b.my_thing"} {
		if !found[w] {
			t.Errorf("no ref %s in %v", w, found)
		}
	}
}

// A suffix match in the tree's own nodes wins over one in an imported
// package; where the tree has none, the package's is used.
func TestLinkLookupOwnTreeFirst(t *testing.T) {
	c, ds := checkDir(t, map[string]string{
		"lib/index.heron": `import = ["std/doc@1"]

lib: section {
  window: term {{ The package's. }}
  limit: term {{ Only the package's. }}
}
`,
		"index.heron": `import = ["std/doc@1", "./lib/"]

refunds: section {
  window: term {{ The tree's own. }}
}

notes: section {
  | The [window] and the [limit].
}
`})
	if len(ds) > 0 {
		t.Fatalf("unexpected: %v", ds)
	}
	got := map[string]string{}
	for p, n := range c.Refs {
		got[strings.Join(resolve.PathSegs(p), ".")] = n.Path()
	}
	if got["window"] != "refunds.window" || got["limit"] != "lib.limit" {
		t.Errorf("refs: %v", got)
	}
}
