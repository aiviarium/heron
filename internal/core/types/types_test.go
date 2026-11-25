package types

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// check resolves and checks one file and returns its messages.
func check(t *testing.T, src string) []string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "index.heron")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	id, _ := resolve.Canonical(p)
	tr := resolve.New(resolve.NewLoader()).Resolve(id)
	_, ds := Check(tr)
	var out []string
	for _, d := range append(append(diag.List{}, tr.Diags...), ds...) {
		out = append(out, d.Code+": "+d.Message)
	}
	return out
}

func TestCleanFile(t *testing.T) {
	got := check(t, `import = ["std/doc@1", "std/types@1", "std/spec@1"]

Refund: type {
  id: String
  amount: Money
  window: Duration = "30d"
}

Status: enum {
  OPEN: Self
  DONE: Self
}

let limit = Duration(days = 30)

overview: section {
  | See the [refund](#Refund) and $limit.
  |
  | <note kind="tip">Tags are kinds in scope.</note>
}

late: rule(overview) {
  if {{ it arrives late }}
  must {{ reject it }}
  example {{ day 31: rejected }}
}
`)
	if len(got) > 0 {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{"import = [\"std/types@1\"]\nR: type {\n  id: String\n}\nr: R = R(idd = \"x\")\n", "R has no member idd; did you mean id?"},
		{"import = [\"std/types@1\"]\nR: type {\n  d: Duration = \"thirty days\"\n}\n", "is not a duration"},
		{"import = [\"std/types@1\"]\nR: type {\n  m: Money = \"40 euros\"\n}\n", "is not an amount of money"},
		{"import = [\"std/types@1\"]\nR: type {\n  t: Timestamp = \"2026-02-30\"\n}\n", "is not a timestamp"},
		{"import = [\"std/types@1\"]\nR: type {\n  d: Duration = 30\n}\n", "expected Duration, got a number"},
		{"import = [\"std/types@1\"]\nR: type {\n  n: Int = \"x\"\n}\n", "expected Int, got a string"},
		{"import = [\"std/doc@1\"]\nx: section<lage>\n", "unknown flag lage"},
		{"import = [\"std/doc@1\"]\nx: sektion\n", "unknown kind or type sektion; did you mean section?"},
		{"import = [\"std/doc@1\"]\nx: note(knd = \"tip\")\n", "kind note takes no argument knd; did you mean kind?"},
		{"import = [\"std/doc@1\"]\nx: note(kind = \"hint\")\n", `"hint" is not a value of NoteKind`},
		{"import = [\"std/doc@1\"]\nx: abbr\n", "kind abbr needs the argument title"},
		{"import = [\"std/doc@1\"]\n@displey(\"X\")\nx: section\n", "unknown annotation @displey"},
		{"import = [\"std/doc@1\"]\nx: section {\n  | A [link](#nowhere).\n}\n", "the link to #nowhere doesn't resolve"},
		{"import = [\"std/doc@1\"]\nx: section {\n  | <warnin>x</warnin>\n}\n", "unknown tag <warnin>"},
		{"import = [\"std/spec@1\"]\np: project(path = \".\") {\n  implemnts = []\n}\n", "has no field implemnts; did you mean implements?"},
		{"import = [\"std/spec@1\"]\np: project(path = \".\")\np {\n  versioning {\n    shceme = \"x\"\n  }\n}\n", "has no field shceme"},
		{"import = [\"std/doc@1\"]\nx: section\nx {\n  nope {\n    a = 1\n  }\n}\n", "x.nope doesn't exist"},
		{"t: task do {\n  log(missing)\n}\n", "unknown name missing"},
		{"t: task do {\n  let a = 1\n  log(\"$a $b\")\n}\n", "unknown name b"},
		{"a = later\nlet later = 1\n", "unknown name later"},
		{"import = [\"std/doc@1\"]\nx: section {\n  | See [the issue](https://example.com/issues/$nope).\n}\n", "unknown name nope"},
		{"import = [\"std/doc@1\"]\nx: section {\n  | <toc depth={true} />\n}\n", "expected Int"},
		{"import = [\"std/doc@1\"]\nx: section {\n  | <toc depth={\"two\"} />\n}\n", "expected Int, got a string"},
		{"import = [\"std/doc@1\"]\nx: section {\n  | <note knd=\"tip\">x</note>\n}\n", "<note> takes no attribute knd; did you mean kind?"},
	}
	for _, c := range cases {
		got := check(t, c.src)
		found := false
		for _, g := range got {
			if strings.Contains(g, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: want %q, got %v", c.src, c.want, got)
		}
	}
}

// The standard packages check clean as packages.
func TestStdPackagesCheckClean(t *testing.T) {
	r := resolve.New(resolve.NewLoader())
	ents, err := os.ReadDir(filepath.Join("..", "..", "..", "std"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p, ds := r.Package("std/" + e.Name() + "@1")
		if p == nil {
			t.Errorf("std/%s: %v", e.Name(), ds)
			continue
		}
		_, cds := Check(p.Tree)
		for _, d := range append(append(ds, p.Tree.Diags...), cds...) {
			t.Errorf("std/%s: %s", e.Name(), d.Message)
		}
	}
}

func TestBodyAccepts(t *testing.T) {
	cases := []struct{ src, want string }{
		{"import = [\"std/doc@1\"]\nx: divider {{ text }}\n", "divider takes no prose"},
		{"import = [\"std/doc@1\"]\nx: section do {\n  log(\"x\")\n}\n", "section takes no do block"},
		{"import = [\"std/doc@1\"]\nx: list {\n  note {{ x }}\n}\n", "list doesn't accept note; it accepts item"},
		{"import = [\"std/doc@1\"]\nx: figure {\n  caption {{ a }}\n  caption {{ b }}\n}\n", "figure accepts at most 1 caption"},
		{"import = [\"std/spec@1\"]\nr: rule {\n  must {{ a }}\n  mustNot {{ b }}\n}\n", "rule accepts at most 1 strength node"},
		{"import = [\"std/spec@1\"]\nr: rule {\n  must {{ a }}\n  when {{ b }}\n  when {{ c }}\n}\n", "rule accepts at most 1 when"},
		{"import = [\"std/spec@1\"]\nr: rule {\n  mustNot {{ a }}\n  if {{ b }}\n  if {{ c }}\n}\n", "rule accepts at most 1 if"},
		{"import = [\"std/spec@1\"]\nr: rule {\n  must {{ a }}\n  instead {{ b }}\n}\n", "instead stands only under a negative strength, and must is positive"},
		{"import = [\"std/spec@1\"]\nr: rule {\n  when {{ a }}\n  instead {{ b }}\n}\n", "instead stands only under a negative strength, mustNot or shouldNot"},
	}
	// Examples repeat, and instead under a negative strength is fine.
	if got := check(t, "import = [\"std/spec@1\"]\nr: rule {\n  shouldNot {{ a }}\n  instead {{ b }}\n  example {{ c }}\n  example {{ d }}\n}\n"); len(got) > 0 {
		t.Errorf("a valid rule: %v", got)
	}
	for _, c := range cases {
		got := check(t, c.src)
		found := false
		for _, g := range got {
			found = found || strings.Contains(g, c.want)
		}
		if !found {
			t.Errorf("%q: want %q, got %v", c.src, c.want, got)
		}
	}
}

// A call takes the first <overload> whose parameters accept its arguments.
func TestOverloads(t *testing.T) {
	base := "import = [\"std/doc@1\"]\noverview: section\n"
	for _, src := range []string{
		"a: link(\"https://example.com\")\n",
		"a: link(uri = \"https://example.com\")\n",
		"a: link(target = overview)\n",
		"a: link(overview)\n",
		"s: section {\n  link(overview)\n  link(\"https://example.com\")\n}\n",
	} {
		if got := check(t, base+src); len(got) > 0 {
			t.Errorf("%q: unexpected %v", src, got)
		}
	}
	got := check(t, base+"a: link(href = \"x\")\n")
	if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), "takes no argument href") {
		t.Errorf("no overload fits: got %v", got)
	}
	got = check(t, "import = [\"std/schema@1\"]\n"+
		"w<overload>(n: Int): kind\n"+
		"w<overload>(s: String): kind\n"+
		"a: w(1)\nb: w(\"x\")\nc: w(s = \"x\")\nd: w(n = 1)\n")
	if len(got) > 0 {
		t.Errorf("local overloads: unexpected %v", got)
	}
	got = check(t, "import = [\"std/schema@1\"]\n"+
		"w<overload>(n: Int): kind\n"+
		"w<overload>(s: String): kind\n"+
		"a: w(true)\n")
	if len(got) != 1 {
		t.Errorf("no overload fits: want one error, got %v", got)
	}
}

// Members of a generic type are read through its arguments, and Patch(T)
// makes every member of T optional.
func TestGenericsAndPatch(t *testing.T) {
	base := "import = [\"std/types@1\"]\n" +
		"Refund: type {\n  id: String\n  amount: Int\n}\n" +
		"Page(T: Any): type {\n  items: List(T)\n  first: T\n  cursor?: String\n}\n"
	cases := []struct{ src, want string }{
		{"p: Page(Refund) = { items = [{ id = \"a\", amount = 1 }], first = { id = \"b\", amount = 2 } }\n", ""},
		{"p: Page(Refund) = { first = { id = \"b\", amont = 2 } }\n", "Refund has no member amont; did you mean amount?"},
		{"p: Page(Refund) = { items = [{ id = \"a\", amount = \"x\" }] }\n", "expected Int, got a string"},
		{"p: Page(Int) = { items = [1, \"x\"] }\n", "expected Int, got a string"},
		{"p: Patch(Refund) = { amount = 3 }\n", ""},
		{"p: Patch(Refund) = { id = null }\n", ""},
		{"p: Patch(Refund) = { amount = \"x\" }\n", "expected Int, got a string"},
		{"p: Patch(Refund) = { amout = 3 }\n", "Refund has no member amout"},
		{"p: Patch(Refund) = 3\n", "expected Patch, got a number"},
	}
	for _, c := range cases {
		got := strings.Join(check(t, base+c.src), "\n")
		if c.want == "" && got != "" || !strings.Contains(got, c.want) {
			t.Errorf("%q: want %q, got %q", c.src, c.want, got)
		}
	}
}

// An api holds ops; an op takes prose, its params and named examples; an
// example sets the op's params and states one outcome.
func TestApisAndOps(t *testing.T) {
	base := "import = [\"std/types@1\"]\nThing: type { id: String }\nGone: type(Error) { message: String }\n"
	valid := base + `
@throws(Gone)
store: api {
  | Things.

  get(id: String): op(Thing) =| Returns one thing.
  put(thing: Thing): op(returns = Thing) {
    | Stores a thing.

    stored: example {
      thing = Thing(id = "a")
      returns = Thing(id = "a")
    }
    missing: example {
      thing = Thing(id = "b")
      throws = Gone
    }
    told: example {{ Storing a thing twice keeps one. }}
  }
}

loose(id: String): op(Thing)
ping(): op
`
	if got := check(t, valid); len(got) > 0 {
		t.Errorf("a valid api: %v", got)
	}
	for src, want := range map[string]string{
		"import = [\"std/http@1\"]\nstore: api {\n  get(@header(name = 1) id: String): op(Thing)\n}\n":                            "expected String, got a number",
		"store: api {\n  put(thing: Thing): op(Thing) {\n    e: example {\n      thing = 1\n    }\n  }\n}\n":                      "expected Thing, got a number",
		"store: api {\n  t: type { a: Int }\n}\n":                                                                                 "api doesn't accept type; it accepts op",
		"store: api {\n  put(thing: Thing): op(Thing) {\n    e: example {\n      other = 1\n    }\n  }\n}\n":                      "store.put.e (a example) has no field other",
		"store: api {\n  put(thing: Thing): op(Thing) {\n    e: example {\n      returns = 1\n      returns = 2\n    }\n  }\n}\n": "store.put.e.returns is set twice in one body",
	} {
		got := check(t, base+src)
		found := false
		for _, g := range got {
			found = found || strings.Contains(g, want)
		}
		if !found {
			t.Errorf("%q: want %q, got %v", src, want, got)
		}
	}
}

// The kinds of std/spec, std/product and std/tech check their bodies,
// their fields and the variants a lifecycle names.
func TestSpecKinds(t *testing.T) {
	base := "import = [\"std/doc@1\", \"std/types@1\", \"std/spec@1\", \"std/product@1\", \"std/tech@1\"]\nStatus: enum {\n  OPEN: Self\n  SHUT: Self\n}\n"
	for src, want := range map[string]string{
		"life: states(Status) {\n  t: transition(Status.OPEN, Status.GONE)\n}\n":                          "Status has no member GONE",
		"life: states(Status) {\n  t: transition(Status.OPEN, Status.SHUT) {\n    must {{ x }}\n  }\n}\n": "transition doesn't accept must",
		"d: decision<accepted> {\n  choice =| x\n}\n":                                                     "unknown kind or type decision",
		"q: quality {\n  must {{ a }}\n  should {{ b }}\n}\n":                                             "quality accepts at most 1 strength node",
		"c: criterion {\n  when {{ a }}\n  when {{ b }}\n}\n":                                             "criterion accepts at most 1 when",
		"e: epic {\n  a: done\n  b: done\n}\n":                                                            "epic accepts at most 1 done",
		"a: actor(kind = ActorKind.ROBOT) {{ x }}\n":                                                      "ActorKind has no member ROBOT",
		"s: story(as = nobody) {\n  want =| w\n}\n":                                                       "unknown name nobody",
		"p: project(path = \".\") {\n  depends_on = [nobody]\n}\n":                                        "unknown name nobody",
		"p: project(path = \".\") {\n  owner = \"x\"\n}\n":                                                "p (a project) has no field owner",
		"p: project(path = \".\") {\n  t: task do {\n    log(\"x\")\n  }\n}\n":                            "",
		"r: rule(Status) {{ The system must keep a status. }}\n":                                          "",
		"g: grammar {\n  r: rule {\n    must {{ x }}\n  }\n}\n":                                           "",
	} {
		got := check(t, base+src)
		found := want == "" && len(got) == 0
		for _, g := range got {
			found = found || want != "" && strings.Contains(g, want)
		}
		if !found {
			t.Errorf("%q: want %q, got %v", src, want, got)
		}
	}
}
