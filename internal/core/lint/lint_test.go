package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
)

func lintFiles(t *testing.T, files map[string]string) []string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(src), 0o644)
	}
	r := registry.New()
	Register(r)
	s := check.NewSession()
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	res := s.Check(id)
	var out []string
	for _, d := range res.Diags {
		out = append(out, "check "+d.Code+": "+d.Message)
	}
	for _, d := range Run(r, res) {
		out = append(out, d.Code+": "+d.Message)
	}
	return out
}

func wantOnly(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if !strings.Contains(got[i], want[i]) {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A second check in one session serves unchanged nodes from the memo; the
// lints still see every reference, so a used let and an allowed finding
// stay quiet.
func TestLintAfterMemoizedCheck(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.heron"), []byte("import = [\"std/doc@1\"]\nlet used = \"a\"\nx: section {\n  | Uses $used.\n}\n@lint(allow = [\"unused-let\"])\ny: section {\n  let quiet = 1\n}\n"), 0o644)
	r := registry.New()
	Register(r)
	s := check.NewSession()
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	for i := 0; i < 2; i++ {
		res := s.Check(id)
		if ds := Run(r, res); len(ds) != 0 {
			t.Fatalf("check %d: %v", i+1, ds)
		}
	}
}

func TestUnusedLet(t *testing.T) {
	wantOnly(t, lintFiles(t, map[string]string{"index.heron": `import = ["std/doc@1"]
let used = "a"
let unused = "b"
x: section {
  | Uses $used.
}
`}), "unused-let: let unused is never used")
}

func TestFlagPlacement(t *testing.T) {
	wantOnly(t, lintFiles(t, map[string]string{"index.heron": `import = ["std/doc@1"]
x: feature<security>
y: section<security>
`}), "flag-placement: <security> doesn't attach to section; it attaches to feature")
}

func TestFlagOnAnnotation(t *testing.T) {
	wantOnly(t, lintFiles(t, map[string]string{"index.heron": `import = ["std/schema@1", "std/doc@1"]
@insert<raw>
x: section
@highlight<raw>("keyword")
quiet: flag
`}), "flag-placement: <raw> doesn't attach to @highlight; it attaches to insert")
}

func TestAnnotationPlacement(t *testing.T) {
	wantOnly(t, lintFiles(t, map[string]string{"index.heron": `import = ["std/types@1", "std/http@1", "std/doc@1"]
@status(404)
NotFound: type(Error) {
  message: String
}

@status(500)
overview: section

a: api {
  get_one(@path id: String): op(NotFound)
  @status(1)
  @http("/x")
  @http("/y")
  other(@status(2) id: String): op(NotFound)
}
`}),
		"annotation-placement: @status doesn't attach to section; it attaches to error, type",
		"annotation-placement: @status doesn't attach to op",
		"annotation-placement: @http stands twice on one node",
		"annotation-placement: @status doesn't attach to a parameter",
	)
}

func TestTagKind(t *testing.T) {
	wantOnly(t, lintFiles(t, map[string]string{"index.heron": `import = ["std/schema@1", "std/doc@1", "std/types@1"]
listing: kind(modes = [structure]) {
  accept(prose)
}
x: section {
  | A <ActorRole>tip</ActorRole> and a <listing>x</listing>.
}
`}),
		"tag-kind: <ActorRole> names ActorRole, which isn't a kind",
		"tag-kind: <listing> is a kind that stands only in structure",
	)
}

func TestExampleParams(t *testing.T) {
	wantOnly(t, lintFiles(t, map[string]string{"index.heron": `import = ["std/types@1"]
a: api {
  get(id: String, verbose?: Bool): op(String) {
    good: example {
      id = "1"
      returns = "one"
    }
    bad: example {
      returns = "two"
    }
    text_only: example {{ Any id works. }}
  }
}
`}), "example-params: the example leaves out id, which get requires")
}

func TestRepeatedImportAndAllow(t *testing.T) {
	wantOnly(t, lintFiles(t, map[string]string{
		"index.heron": "import = [\"std/doc@1\"]\ninclude = [\"./a.heron\"]\n",
		"a.heron":     "import = [\"std/doc@1\"]\n@lint(allow = [\"unused-let\"])\nx: section {\n  let quiet = 1\n}\n",
	}), "repeated-import: std/doc is already imported")
}

// The standard packages lint clean, as packages.
func TestStdLintsClean(t *testing.T) {
	r := registry.New()
	Register(r)
	res := resolve.New(resolve.NewLoader())
	ents, _ := os.ReadDir(filepath.Join("..", "..", "..", "std"))
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		p, _ := res.Package("std/" + e.Name() + "@1")
		if p == nil {
			continue
		}
		c, _ := types.Check(p.Tree)
		for _, d := range Run(r, &check.Result{Tree: p.Tree, Checker: c}) {
			t.Errorf("std/%s: %s: %s", e.Name(), d.Code, d.Message)
		}
	}
}

// An override of an error's code that sets the code its @status derives
// is reported, with a fix that removes the annotation; one that differs
// is kept.
func TestRedundantCode(t *testing.T) {
	dir := t.TempDir()
	src := `import = ["std/types@1", "std/proto@1", "std/jsonrpc@1"]
@status(404)
@proto.status(NOT_FOUND)
@jsonrpc.code(-32004)
Gone: type(Error) { message: String }

@status(422)
@proto.status(INVALID_ARGUMENT)
@jsonrpc.code(-32602)
Odd: type(Error) { message: String }

@status(400)
@exit(2)
usage: error("x exit 2") {
  title = "a usage error"
}

@status(500)
@exit(3)
crash: error("x exit 3") {
  title = "a crash"
}
`
	p := filepath.Join(dir, "index.heron")
	os.WriteFile(p, []byte(src), 0o644)
	r := registry.New()
	Register(r)
	id, _ := resolve.Canonical(p)
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatalf("%v", res.Diags)
	}
	var got []string
	for _, d := range Run(r, res) {
		got = append(got, d.Code+": "+d.Message)
		if len(d.Fixes) != 1 || len(d.Fixes[0].Edits) != 1 || d.Fixes[0].Edits[0].New != "" {
			t.Errorf("%s: fixes %+v", d.Message, d.Fixes)
			continue
		}
		e := d.Fixes[0].Edits[0]
		fixed := src[:e.Start] + src[e.End:]
		if strings.Contains(fixed, src[d.Start:d.End]) {
			t.Errorf("the fix leaves %s", src[d.Start:d.End])
		}
	}
	wantOnly(t, got,
		"redundant-code: @proto.status sets NOT_FOUND, the code Gone's @status derives; leave it out",
		"redundant-code: @jsonrpc.code sets -32004, the code Gone's @status derives; leave it out",
		"redundant-code: @exit sets 2, the code usage's @status derives; leave it out",
	)
}
