package types

import (
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// messages are a check's diagnostics as "code: message".
func messages(t *testing.T, files map[string]string) []string {
	t.Helper()
	_, ds := checkDir(t, files)
	var out []string
	for _, d := range ds {
		out = append(out, d.Code+": "+d.Message)
	}
	return out
}

func wantMessages(t *testing.T, got []string, want ...string) {
	t.Helper()
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || strings.Contains(g, w)
		}
		if !found {
			t.Errorf("no %q in\n%s", w, strings.Join(got, "\n"))
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d diagnostics, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
}

// A flag resolves among the flags that attach to the kind it stands on, so
// one name can be a flag of two kinds and never meets a node's name; a flag
// on a kind it doesn't attach to is unknown, with where it attaches; two
// imported packages declaring one for the same kind make it ambiguous, and
// the qualified form picks one.
func TestFlagsByKind(t *testing.T) {
	kinds := "import = [\"std/schema@1\"]\ncase: kind\nsuite: kind\n"
	a := "import = [\"std/schema@1\", \"./kinds.heron\"]\na: package(version = \"1.0\") {\n  slow: flag(on = [case])\n  quick: flag(on = [suite])\n}\n"
	b := "import = [\"std/schema@1\", \"./kinds.heron\"]\nb: package(version = \"1.0\") {\n  slow: flag(on = [case])\n  slow_suite: flag(on = [suite])\n}\n"
	got := messages(t, map[string]string{
		"index.heron": `import = ["std/schema@1", "std/types@1", "./kinds.heron", "./a.heron", "./b.heron"]

slow: suite
one: case<a.slow>
two: case<slow>
three: suite<quick>
four: suite<slow>
five: case<b.quick>
system: suite
who: actor<system> {{ A program. }}
level: usecase<user>(who) {{
  1. [who] calls.
}}
e: error<warning>(400) {
  title = "x"
}
f: actor<warning> {{ x }}
`,
		"kinds.heron": kinds,
		"a.heron":     a,
		"b.heron":     b,
	})
	wantMessages(t, got,
		"ambiguous: <slow> is declared for case by more than one imported package: write <a.slow> or <b.slow>",
		"unresolved: unknown flag slow for suite; <slow> attaches to case, case",
		"unresolved: unknown flag b.quick",
		"unresolved: unknown flag warning for actor; <warning> attaches to error",
	)
}

// An error extends one parent: it inherits its status, title, severity,
// members and overrides, keeps its own id, and is covered by a @throws of
// its parent. An error that extends itself and a retyped member are
// reported.
func TestErrorKind(t *testing.T) {
	c, ds := checkDir(t, map[string]string{"index.heron": `import = ["std/types@1", "std/proto@1"]

@proto.status(INVALID_ARGUMENT)
Base: error<warning>(422, status_text = "Unprocessable") {
  title = "refused {id}"
  id: String
}

Child: error(Base, id = "shop.CHILD") {
  reason: String
}

Grandchild: error(extends = Child, status = 404) {
  title = "gone {id} {reason}"
}

Retyped: error(Base) {
  id: Int
}

Loop1: error(Loop2) {}
Loop2: error(Loop1) {}

Holder: type {
  last?: Base
  all: List(Child)
}
`})
	var got []string
	for _, d := range ds {
		got = append(got, d.Code+": "+d.Message)
	}
	wantMessages(t, got,
		"retyped-member: Retyped.id is Int, but Base declares it String; an error keeps the type of a member it inherits",
		"extends: error Loop1 extends itself: Loop1 extends Loop2 extends Loop1",
		"extends: error Loop2 extends itself: Loop2 extends Loop1 extends Loop2",
	)
	base, child, grand := c.Tree.Find("Base"), c.Tree.Find("Child"), c.Tree.Find("Grandchild")
	if !c.IsType(child) || c.ErrorParent(grand) != child || c.ErrorParent(base) != nil {
		t.Fatalf("parents: %v %v", c.ErrorParent(grand), c.ErrorParent(base))
	}
	if s := c.ErrorStatus(child); s != "422" {
		t.Errorf("Child's status %q, want the inherited 422", s)
	}
	if s := c.ErrorStatus(grand); s != "404" {
		t.Errorf("Grandchild's status %q, want its own 404", s)
	}
	if id, text := c.ErrorID(child), c.ErrorStatusText(child); id != "shop.CHILD" || text != "shop.CHILD" {
		t.Errorf("Child's id %q and text %q: neither is inherited", id, text)
	}
	if id, text := c.ErrorID(base), c.ErrorStatusText(base); id != "Base" || text != "Unprocessable" {
		t.Errorf("Base's id %q, its name, and text %q", id, text)
	}
	if sev := c.ErrorSeverity(grand); sev != "warning" {
		t.Errorf("Grandchild's severity %q, want the inherited warning", sev)
	}
	if f := c.ErrorField(child, "title"); f == nil || f.Parent != base {
		t.Errorf("Child's title comes from Base")
	}
	if f := c.ErrorField(grand, "title"); f == nil || f.Parent != grand {
		t.Errorf("Grandchild sets its own title")
	}
	if !c.ErrorCovers(base, grand) || c.ErrorCovers(grand, base) {
		t.Errorf("@throws(Base) covers Grandchild, not the other way")
	}
	if ds := c.ErrorDescendants(base); len(ds) != 3 {
		t.Errorf("Base's descendants: %d, want Child, Grandchild and Retyped", len(ds))
	}
	for name, from := range map[string]*resolve.Node{"reason": child, "id": base} {
		if f, ok := c.typeMember(grand, name, map[*resolve.Node]bool{}); !ok || f.Decl == nil || f.Decl.Parent != from {
			t.Errorf("Grandchild's member %s is inherited from %s", name, from.Name)
		}
	}
}

// An error that extends none needs its status; one that extends another
// inherits it.
func TestErrorRequiredStatus(t *testing.T) {
	got := messages(t, map[string]string{"index.heron": `import = ["std/types@1"]

Root: error(id = "x") {
  title = "x"
}
Base: error(400) {
  title = "base"
}
Child: error(Base) {}
`})
	wantMessages(t, got, "missing-argument: kind error needs the argument status")
}

// A union's variants are written positionally, their tags the snake_case
// form of the type's name, or in the body; mixed, the tags stay unique. A
// tagged union reports a variant with no tag to derive and a duplicate tag;
// any union reports a type written twice; an untagged one needs no tags.
func TestUnionVariants(t *testing.T) {
	c, ds := checkDir(t, map[string]string{"index.heron": `import = ["std/types@1"]

Card: type {}
BankTransfer: type {}
Wallet: type {}

Method: union(Card, BankTransfer) {
  wallet: Wallet
}
Bad: union(List(String), Card) {
  card: Wallet
}
Twice: union<untagged>(String, String)
Loose: union<untagged>(String, List(Int)) {
  card: Card
}
Tagged: union<tagged>(Card)
`})
	var got []string
	for _, d := range ds {
		got = append(got, d.Code+": "+d.Message)
	}
	wantMessages(t, got,
		"union: List(String) has no name to derive its tag from; give it one in the body, as name: List(String)",
		"union: two variants of Bad have the tag card",
		"union: String is a variant of Twice twice",
	)
	var tags []string
	for _, v := range c.UnionVariants(c.Tree.Find("Method")) {
		tags = append(tags, v.Tag)
	}
	if strings.Join(tags, " ") != "card bank_transfer wallet" {
		t.Errorf("Method's tags %v", tags)
	}
	if c.UnionTagged(c.Tree.Find("Loose")) || !c.UnionTagged(c.Tree.Find("Method")) || !c.UnionTagged(c.Tree.Find("Tagged")) {
		t.Error("a union is tagged unless <untagged>")
	}
}

// A flag whose `on` names an annotation resolves only on it, and a status
// flag only on the kinds it attaches to: elsewhere each is unknown.
func TestFlagsOnAnnotationsAndStatus(t *testing.T) {
	got := messages(t, map[string]string{"index.heron": `import = ["std/schema@1", "std/doc@1", "std/project@1"]

@insert<raw>
x: section
@highlight<raw>(token = "keyword")
quiet: flag
y: feature<security>
z: section<security>
r: rule<delivered> {
  must {{ x }}
}
`})
	wantMessages(t, got,
		"unresolved: unknown flag raw for highlight; <raw> attaches to insert",
		"unresolved: unknown flag security for section; <security> attaches to feature",
		"unresolved: unknown flag delivered for rule; <delivered> attaches to story, increment, milestone",
	)
}

// An unqualified annotation the core declares is the core's, even where an
// imported package declares one of that name, which is written qualified.
func TestCoreAnnotationsWin(t *testing.T) {
	c, ds := checkDir(t, map[string]string{
		"index.heron": `import = ["std/types@1", "./shell.heron"]

@exit(3)
E: error(500) {
  title = "x"
}

@shell.exit(4)
F: error(500) {
  title = "y"
}
`,
		"shell.heron": "import = [\"std/schema@1\", \"std/types@1\"]\nshell: package(version = \"1.0\") {\n  exit(code: Int): annotation(on = [types.error])\n}\n",
	})
	if len(ds) > 0 {
		t.Fatalf("%v", ds)
	}
	for name, want := range map[string]resolve.FileID{"E": stdFile("spec"), "F": ""} {
		n := c.Tree.Find(name)
		d := c.Refs[ast.Annotation{N: n.Annotations[0].N}.Path().N]
		if d == nil || want != "" && d.Decl.File.ID != want || want == "" && d.Decl.File.ID.IsStd() {
			t.Errorf("%s's @exit resolved to %v", name, d)
		}
	}
}
