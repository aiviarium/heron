package types

import (
	"strings"
	"testing"
)

// A kind element's name resolves to a kind, by suffix too; its quoted
// attributes are literals of their parameters' types and braced ones
// expressions; a kind with a heading or a term needs an id; a kind whose
// body is code has no element form; a missing attribute, an unknown one
// and a quoted reference that names nothing are reported; and an
// unquoted value is a syntax error that keeps the element.
func TestElementsCheck(t *testing.T) {
	_, ds := checkDir(t, map[string]string{"index.heron": `import = ["std/doc@1", "std/docs@1"]

people: section {
  desk: actor<system> {{ The console. }}
}

ok: section {
  | <rule id="late" refs="desk"><must>refuse it</must></rule>
  | <toc depth="2" /> <toc depth={1 + 1} /> <ref to="desk"/> <ref to="desk">the desk</ref> <tip>A tip.</tip>
}

bad: section {
  | <howto id="h">
  |   <step>No id.</step>
  | </howto>
  |
  | <term>Unnamed.</term> <toc depth="two" /> <toc depth=2 /> <ref to="nobody"/> <ref/> <toc colour="red" />
}
`})
	var got []string
	for _, d := range ds {
		got = append(got, d.Code+": "+d.Message)
	}
	want := []string{
		"syntax: the value of depth needs quotes or braces",
		"unnamed: <step> declares a node and needs an id",
		"unnamed: <term> declares a node and needs an id",
		"type: expected Int, got a string",
		"unresolved: [nobody] names no node",
		"missing-argument: <ref> needs the attribute to",
		"unknown-argument: <toc> takes no attribute colour",
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if strings.HasPrefix(g, w) {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q in\n%s", w, strings.Join(got, "\n"))
		}
	}
	for _, g := range got {
		if strings.Contains(g, "late") || strings.Contains(g, "kind") && strings.Contains(g, "TIP") {
			t.Errorf("a clean element reported: %s", g)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d diagnostics, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
}

// A kind whose body is code has no element form.
func TestElementCode(t *testing.T) {
	_, ds := checkDir(t, map[string]string{"index.heron": `import = ["std/doc@1"]

s: section {
  | <task id="t">x</task>
}
`})
	for _, d := range ds {
		if d.Code == "element-code" {
			return
		}
	}
	t.Errorf("no element-code diagnostic: %v", ds)
}

// A variadic parameter's attribute gives its arguments in order, before
// the named ones: `delivers="ok"` is `increment(ok)` and
// `delivers={[ok, ok.a]}` is `increment(ok, ok.a)`. An `@name` attribute
// is the annotation, as `@display="Three"` is `@display("Three")`, and
// `refs="desk"` reaches desk as a link would.
func TestElementVariadicAttribute(t *testing.T) {
	c, ds := checkDir(t, map[string]string{"index.heron": `import = ["std/doc@1", "std/project@1"]

people: section {
  desk: actor<system> {{ The console. }}
}

ok: section {
  a: term {{ A word. }}

  | <increment id="one" delivers="ok">The first slice.</increment>
  |
  | <increment id="two" delivers={[ok, ok.a]}>The second slice.</increment>
  |
  | <rule id="three" refs="desk" @display="Three"><must>refuse it</must></rule>
}
`})
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	for name, want := range map[string]string{"one": "x(ok)", "two": "x(ok, ok.a)", "three": "x(people.desk)"} {
		var got string
		for _, n := range c.Tree.Root.Child("ok").Children {
			if n.Name == name {
				got = "x" + n.ElementArgs.N.Text()
			}
		}
		if got != want {
			t.Errorf("%s: args %q, want %q", name, got, want)
		}
	}
	var anns []string
	for _, n := range c.Tree.Root.Child("ok").Children {
		if n.Name == "three" {
			for _, a := range n.Annotations {
				anns = append(anns, a.N.Text())
			}
		}
	}
	if got := strings.Join(anns, " "); got != `@display("Three")` {
		t.Errorf("three: annotations %q", got)
	}
}
