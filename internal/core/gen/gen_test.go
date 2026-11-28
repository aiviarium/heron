package gen

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

func TestIgnoreBlockKeepsTheRest(t *testing.T) {
	in := "node_modules/\n"
	once, _ := IgnoreBlock(in, OwnerGenerate, []string{"/.heron/", "/out.txt"})
	if !strings.HasPrefix(once, "node_modules/\n\n"+BlockBegin+"\n# from heron generate\n") || !strings.Contains(once, "/out.txt\n"+BlockEnd) {
		t.Fatalf("%q", once)
	}
	twice, _ := IgnoreBlock(once+"dist/\n", OwnerGenerate, []string{"/.heron/"})
	if strings.Contains(twice, "/out.txt") || !strings.HasSuffix(twice, "dist/\n") || strings.Count(twice, BlockBegin) != 1 {
		t.Fatalf("%q", twice)
	}
	if again, _ := IgnoreBlock(twice, OwnerGenerate, []string{"/.heron/"}); again != twice {
		t.Fatal("not idempotent")
	}
	// Setup's section sits beside generate's, and neither erases the other.
	both, _ := IgnoreBlock(twice, OwnerSetup, []string{"/.heron/", "/vendor/x/"})
	both, _ = IgnoreBlock(both, OwnerGenerate, []string{"/.heron/", "/out.txt"})
	want := "node_modules/\n\n" + BlockBegin + "\n# from heron generate\n/.heron/\n/out.txt\n# from heron run setup\n/.heron/\n/vendor/x/\n" + BlockEnd + "\ndist/\n"
	if both != want {
		t.Fatalf("got %q", both)
	}
	if _, err := IgnoreBlock(BlockBegin+"\nx\n", OwnerSetup, nil); err == nil {
		t.Fatal("a block without its end was accepted")
	}
}

func TestPathsStayInside(t *testing.T) {
	for _, p := range []string{"../x", "/etc/x", "a/../../x", "."} {
		if _, err := inside(p); err == nil {
			t.Fatalf("%s was accepted", p)
		}
	}
	if p, err := inside("a/./b.txt"); err != nil || p != "a/b.txt" {
		t.Fatal(p, err)
	}
}

func checked(t *testing.T, files map[string]string) (*check.Result, string) {
	t.Helper()
	dir := t.TempDir()
	for n, s := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, n)), 0o755)
		os.WriteFile(filepath.Join(dir, n), []byte(s), 0o644)
	}
	id, err := resolve.Canonical(filepath.Join(dir, "index.heron"))
	if err != nil {
		t.Fatal(err)
	}
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatal(res.Diags)
	}
	return res, dir
}

func TestRunWritesTargetsAndTheBlock(t *testing.T) {
	res, dir := checked(t, map[string]string{"index.heron": "import = [\"std/types@1\"]\n\nPet: type {\n  name: String\n}\n"})
	r := registry.New()
	Register(r)
	calls := 0
	r.SetGenerator(registry.Generator{Path: "sample.names", Target: "sample", Generate: func(ctx context.Context, in any) ([]registry.GeneratedFile, error) {
		calls++
		var names []string
		for _, n := range in.(*Input).Tree.Root.Children {
			names = append(names, n.Name)
		}
		return []registry.GeneratedFile{{Path: "out/names.txt", Content: []byte(strings.Join(names, "\n") + "\n"), Ignored: true}}, nil
	}})
	in := &Input{Tree: res.Tree, Checker: res.Checker, Dir: dir}
	out, err := Run(context.Background(), r, in, []string{"sample"})
	if err != nil || len(out.Written) != 1 {
		t.Fatal(out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out/names.txt")); string(b) != "Pet\n" {
		t.Fatalf("%q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err == nil {
		t.Fatal("one target doesn't write the ignore block")
	}
	out, err = Run(context.Background(), r, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	ig, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !bytes.Contains(ig, []byte("/.heron/\n/out/names.txt\n")) {
		t.Fatalf("%s", ig)
	}
	again, err := Run(context.Background(), r, in, nil)
	if err != nil || len(again.Written) != 0 {
		t.Fatalf("a second run wrote %v (%v)", again.Written, err)
	}
	if _, err := Run(context.Background(), r, in, []string{"nope"}); err == nil {
		t.Fatal("an unknown target ran")
	}
}

func TestTSTypes(t *testing.T) {
	res, dir := checked(t, map[string]string{"index.heron": `import = ["std/types@1"]

Status: enum {
  open: Self
  closed: Self
}

/// An order.
Order: type {
  id: Int
  note: String?
  tags: List(String)
  status: Status
}
`})
	files, err := TS(&Input{Tree: res.Tree, Checker: res.Checker, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != ".heron/types.ts" {
		t.Fatalf("files: %v", files)
	}
	types := string(files[0].Content)
	for _, want := range []string{
		"export type Decimal = string;",
		`export type Status = "open" | "closed";`,
		"export interface Order {\n  id: number;\n  note?: string | null;\n  tags: Array<string>;\n  status: Status;\n}",
	} {
		if !strings.Contains(types, want) {
			t.Fatalf("types.ts lacks %q:\n%s", want, types)
		}
	}
	if strings.Contains(types, "import") {
		t.Fatalf("types.ts imports something:\n%s", types)
	}
}

// TestPackRunsAgentsAlone: --pack runs the agents target alone, and given
// with another target it's a usage error.
func TestPackRunsAgentsAlone(t *testing.T) {
	r := registry.New()
	Register(r)
	var out, errb strings.Builder
	env := &registry.Env{Dir: t.TempDir(), Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}
	if code := r.Command("generate").Run(env, []string{"--pack", "language.apis", "docs"}); code != 2 || !strings.Contains(errb.String(), `--pack runs the agents target alone, not "docs"`) {
		t.Errorf("--pack with docs: %d %q", code, errb.String())
	}
}
