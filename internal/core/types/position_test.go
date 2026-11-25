package types

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// The packages of TestNamesResolveByPosition: each declares a name another
// declares as something else, so only the position a name is written in
// says which it means.
var positionPkgs = map[string]string{
	// widget is a kind, a flag and an annotation; zone is a category and a
	// flag; shape and Shaped are types and flags.
	"k.heron": "import = [\"std/schema@1\"]\n\nwidget: kind {\n  accept(prose)\n}\n",
	"f.heron": "import = [\"std/schema@1\"]\n\nwidget: flag\nzone: flag\nshape: flag\nShaped: flag\n",
	"a.heron": "import = [\"std/schema@1\"]\n\nwidget: annotation\n",
	"c.heron": "import = [\"std/schema@1\"]\n\nzone: category\n",
	"t.heron": "import = [\"std/types@1\"]\n\nshape: type {\n  a: Int\n}\n\nShaped: interface {\n  a: Int\n}\n",
}

const positionUses = `
holder: kind(in = [zone]) {
  accept(kind = widget)
  accept(category = zone)
}

pin: flag(on = [widget])

note: annotation(on = [widget])

Box: type(implements = [Shaped]) {
  a: Int
  s: shape
  l: List(shape)
  r?: Ref(widget)
}

measure(s: shape): func(Int)

@widget
w<widget, pin>: widget {{ Some text. }}

h: holder {
  widget {{ An anonymous one. }}
}
`

// TestNamesResolveByPosition: when packages declare one name as a kind, a
// flag, an annotation, a category and a type, each position takes the
// binding it wants, whatever the import order: a `:` slot, a parameter's
// type and a type argument a kind or a type, `<…>` a flag, `@…` an
// annotation, an anonymous member a kind its parent accepts, accept(kind =
// …) a kind, accept(category = …) and in = […] a category, on = […] a kind
// or category, and implements = […] a type.
func TestNamesResolveByPosition(t *testing.T) {
	orders := [][]string{
		{"./k.heron", "./f.heron", "./a.heron", "./c.heron", "./t.heron"},
		{"./f.heron", "./a.heron", "./t.heron", "./c.heron", "./k.heron"},
		{"./a.heron", "./f.heron", "./k.heron", "./t.heron", "./c.heron"},
	}
	for _, order := range orders {
		dir := t.TempDir()
		for name, src := range positionPkgs {
			os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644)
		}
		imports := `import = ["std/schema@1", "std/types@1", "` + strings.Join(order, `", "`) + `"]` + "\n"
		p := filepath.Join(dir, "index.heron")
		os.WriteFile(p, []byte(imports+positionUses), 0o644)
		id, _ := resolve.Canonical(p)
		tr := resolve.New(resolve.NewLoader()).Resolve(id)
		c, ds := Check(tr)
		var msgs []string
		for _, d := range append(append(diag.List{}, tr.Diags...), ds...) {
			msgs = append(msgs, d.Code+": "+d.Message)
		}
		if len(msgs) > 0 {
			t.Errorf("%v: %v", order, msgs)
			continue
		}
		from := func(n *resolve.Node) string {
			if n == nil || n.Decl.File == nil {
				return "<nil>"
			}
			return filepath.Base(string(n.Decl.File.ID))
		}
		w := tr.Find("w")
		if k := c.KindOf(w); from(k) != "k.heron" {
			t.Errorf("%v: the : slot took widget from %s", order, from(k))
		}
		if f := c.FlagDecl("widget", w.Decl.Scope, w.Decl.File, -1); from(f) != "f.heron" {
			t.Errorf("%v: <widget> took %s", order, from(f))
		}
		holder := tr.Find("holder")
		if cs := c.Categories(holder); len(cs) != 1 || from(cs[0]) != "c.heron" {
			t.Errorf("%v: in = [zone] took %v", order, froms(cs, from))
		}
		acc := c.acceptList(holder)
		if len(acc) != 2 || from(acc[0].kind) != "k.heron" || from(acc[1].category) != "c.heron" {
			t.Errorf("%v: accept took %d entries", order, len(acc))
		}
		if on := c.On(tr.Find("pin")); len(on) != 1 || from(on[0]) != "k.heron" {
			t.Errorf("%v: on = [widget] took %v", order, froms(on, from))
		}
		if is := c.Implements(tr.Find("Box")); len(is) != 1 || from(is[0]) != "t.heron" {
			t.Errorf("%v: implements took %v", order, froms(is, from))
		}
		for _, f := range []string{"s", "l"} {
			ft, ok := c.typeMember(tr.Find("Box"), f, map[*resolve.Node]bool{})
			d := ft.Type.decl
			if f == "l" {
				d = c.argType(ft.Type, 0).decl
			}
			if !ok || from(d) != "t.heron" {
				t.Errorf("%v: Box.%s's type took shape from %s", order, f, from(d))
			}
		}
		if ps := c.Params(tr.Find("measure")); len(ps) != 1 || from(ps[0].Type.decl) != "t.heron" {
			t.Errorf("%v: a parameter's type took shape from %v", order, ps)
		}
		// What each written name refers to, as go-to-definition sees it.
		src := imports + positionUses
		wantRef := map[string]string{
			"accept(kind = widget)": "k.heron", "accept(category = zone)": "c.heron", "in = [zone]": "c.heron",
			"implements = [Shaped]": "t.heron", "s: shape": "t.heron", "List(shape)": "t.heron", "Ref(widget)": "k.heron",
			"measure(s: shape)": "t.heron", "@widget": "a.heron", "w<widget, pin>: widget": "k.heron", "widget {{ An": "k.heron",
		}
		seen := map[string]bool{}
		for p, n := range c.Refs {
			if p == nil || tr.Entry == nil {
				continue
			}
			start, _ := p.Span()
			if start >= len(src) || !strings.Contains("widget zone shape Shaped", resolve.PathText(p)) {
				continue
			}
			lineStart := strings.LastIndexByte(src[:start], '\n') + 1
			line := src[lineStart:]
			if i := strings.IndexByte(line, '\n'); i >= 0 {
				line = line[:i]
			}
			for frag, file := range wantRef {
				if strings.Contains(line, frag) {
					seen[frag] = true
					if from(n) != file {
						t.Errorf("%v: %s refers to %s's, not %s's", order, frag, from(n), file)
					}
				}
			}
		}
		for frag := range wantRef {
			if !seen[frag] {
				t.Errorf("%v: no reference recorded for %s", order, frag)
			}
		}
	}
}

// TestStdOpenKindAndFlag: std/spec's open kind and std/types' <open> flag
// each resolve where they're written, in either import order.
func TestStdOpenKindAndFlag(t *testing.T) {
	const uses = `
board: kind {
  accept(kind = open)
}

b: board {
  q2: open {{ Who decides? }}
}

Shape<open>: type {
  a: Int
}

Pointer: type {
  to: Ref(open)
}

intro: section {
  q1: open {{ What next? }}

  open {{ And this? }}
}
`
	for _, imports := range []string{
		`import = ["std/schema@1", "std/types@1", "std/spec@1", "std/doc@1"]`,
		`import = ["std/schema@1", "std/spec@1", "std/types@1", "std/doc@1"]`,
	} {
		if got := check(t, imports+"\n"+uses); len(got) > 0 {
			t.Errorf("%s: %v", imports, got)
		}
	}
}

func froms(ns []*resolve.Node, from func(*resolve.Node) string) []string {
	var out []string
	for _, n := range ns {
		out = append(out, from(n))
	}
	return out
}
