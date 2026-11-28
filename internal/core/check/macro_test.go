package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/resolve"
)

func checkSrc(t *testing.T, files map[string]string) *Result {
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
	return NewSession().Check(id)
}

// The spec's resource_ops example: what the macro produces joins the api
// body, so a reference to a produced op resolves.
func TestMacroExpansion(t *testing.T) {
	r := checkSrc(t, map[string]string{"index.heron": `import = ["std/types@1", "std/http@1", "std/doc@1"]

// Adds a list and a get op for a resource, wherever an api body invokes it.
resource_ops(name: String, path: String): macro = (heron)"""
  @http<get>("$path")
  list_${name}s(@query cursor?: String): op(List(${name.pascal}))

  @http<get>("$path/:id")
  get_$name(@path id: Int): op(${name.pascal})
"""

Category: type {
  id: Int
}

store: api {
  resource_ops(name = "category", path = "/categories")
}

notes: section {{ [store.get_category] and [store.list_categorys] are produced. }}
`})
	if len(r.Diags) != 0 {
		t.Fatalf("diagnostics: %v", r.Diags)
	}
	store := r.Tree.Find("store")
	var names []string
	for _, c := range store.Children {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, " "); got != "list_categorys get_category" {
		t.Fatalf("store holds %q", got)
	}
}

// A macro exported by a sealed package expands into the importer's body:
// the nodes are the importer's, at the invocation's place, with references
// bound from the arguments, and an error in what it produced is reported at
// the invocation with the line it came from.
func TestMacroFromSealedPackage(t *testing.T) {
	pkg := `import = ["std/schema@1", "std/types@1", "std/doc@1"]

guides: package(version = "1.0", sealed = true) {
  rules(author: Ref(actor)): macro = (heron)"""
    one_behaviour: rule($author) {
      while {{ a rule states two behaviours }}
      should {{ split it }}
      rationale {{ Each is checked alone. }}
    }

    | Text the guide adds.
    """

  broken(author: Ref(actor)): macro = (heron)"""
    ok: rule($author) {
      must {{ do one thing }}
    }
    bad: rule($author) {
      must {{ see [missing] }}
    }
    """
}
`
	r := checkSrc(t, map[string]string{
		"guides/index.heron": pkg,
		"index.heron": `import = ["std/schema@1", "std/types@1", "std/doc@1", "./guides/"]

writer: actor {{ Writes the spec. }}

guide: section {
  | Before.
  guides.rules(author = writer)
  | After.
}

see: section {{ As [guide.one_behaviour] says. }}
`})
	if len(r.Diags) != 0 {
		t.Fatalf("diagnostics: %v", r.Diags)
	}
	g := r.Tree.Find("guide")
	if len(g.Children) != 1 || g.Children[0].Name != "one_behaviour" {
		t.Fatalf("guide holds %v", g.Children)
	}
	var text []string
	for _, p := range g.Prose {
		text = append(text, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p.N.Text()), "|")))
	}
	if got := strings.Join(text, " / "); got != "Before. / Text the guide adds. / After." {
		t.Errorf("prose in order %q", got)
	}

	r = checkSrc(t, map[string]string{
		"guides/index.heron": pkg,
		"index.heron": `import = ["std/schema@1", "std/types@1", "std/doc@1", "./guides/"]

writer: actor {{ Writes the spec. }}

guide: section {
  guides.broken(author = writer)
}
`})
	if len(r.Diags) != 1 {
		t.Fatalf("diagnostics: %v", r.Diags)
	}
	d := r.Diags[0]
	if !strings.HasSuffix(d.File, "index.heron") || strings.Contains(d.File, "guides") || !strings.Contains(d.Message, "line 5 of what broken produced") {
		t.Fatalf("diagnostic %+v", d)
	}
}

// A macro's arguments are checked against its parameters, and a macro that
// fails to expand is reported at the invocation.
func TestMacroInvocationErrors(t *testing.T) {
	r := checkSrc(t, map[string]string{"index.heron": `import = ["std/types@1", "std/doc@1"]

fields(name: String): macro = (heron)"""
  ${name}_count: Int
"""

counts: type {
  fields(name = 3)
}
`})
	if !r.Diags.HasErrors() {
		t.Fatal("a wrong argument type passed")
	}
	if !strings.Contains(fmt.Sprint(r.Diags), "expected String, got a number") {
		t.Fatalf("diagnostics: %v", r.Diags)
	}
}
