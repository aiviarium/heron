package setup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/eval"
	"github.com/aiviarium/heron/internal/core/gen"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/setup"
)

// procs answers `<tool> --version` from a table and records every command.
type procs struct {
	versions map[string]string
	ran      []string
}

func (p *procs) Run(_ context.Context, c eval.Command) (eval.Exit, error) {
	line := strings.TrimSpace(c.Path + " " + strings.Join(c.Args, " "))
	p.ran = append(p.ran, line)
	if len(c.Args) == 1 && c.Args[0] == "--version" {
		if v, ok := p.versions[c.Path]; ok {
			return eval.Exit{Stdout: []byte("v" + v + "\n")}, nil
		}
		return eval.Exit{Code: 127, Stderr: []byte("not found")}, nil
	}
	if c.Path == "npm" && len(c.Args) == 3 && c.Args[0] == "install" {
		tool, v, _ := strings.Cut(c.Args[2], "@")
		p.versions[tool] = v + ".0.0"
	}
	return eval.Exit{}, nil
}

const src = `import = ["std/impl@1"]

repo: project(".") {
  toolchain = { node = "22", pnpm = "9" }

  check_tools(install: Bool = false): task(List(Tool)) do {
    return ensure_toolchain(repo.toolchain, install = install)
  }

  safe: task(String) do {
    let r = attempt(ensure_toolchain(repo.toolchain))
    return r.error.message
  }
}
`

func machine(t *testing.T, p *procs) (*eval.Machine, *resolve.Tree) {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.heron"), []byte(src), 0o644)
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatalf("doesn't check: %v", res.Diags)
	}
	r := registry.New()
	setup.Register(r, setup.Options{})
	m := eval.New(res.Tree, res.Checker, r, eval.Caps{Processes: p})
	m.Dir = dir
	return m, res.Tree
}

func TestToolchain(t *testing.T) {
	p := &procs{versions: map[string]string{"node": "22.23.5", "pnpm": "10.28.0"}}
	m, tree := machine(t, p)
	_, err := m.Call(context.Background(), tree.Find("repo.check_tools"), eval.Args{})
	if err == nil || !strings.Contains(err.Error(), "wants pnpm 9, but found 10.28.0") {
		t.Fatalf("mismatch without install: %v", err)
	}
	v, err := m.Call(context.Background(), tree.Find("repo.safe"), eval.Args{})
	if err != nil || !strings.Contains(eval.Str(v), "--install_toolchain") {
		t.Errorf("attempt didn't catch the SetupError: %v %v", v, err)
	}
	p.ran = nil
	v, err = m.Call(context.Background(), tree.Find("repo.check_tools"), eval.Args{Named: map[string]eval.Value{"install": true}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.ran, "; "); got != "node --version; pnpm --version; npm install --global pnpm@9; pnpm --version" {
		t.Errorf("ran: %s", got)
	}
	tools := eval.Plain(v).([]any)
	if pn := tools[1].(map[string]any); pn["found"] != "9.0.0" || pn["installed"] != true {
		t.Errorf("pnpm: %v", pn)
	}
	delete(p.versions, "node")
	_, err = m.Call(context.Background(), tree.Find("repo.check_tools"), eval.Args{Named: map[string]eval.Value{"install": true}})
	if err == nil || !strings.Contains(err.Error(), "heron can't install node") {
		t.Errorf("node: %v", err)
	}
}

func TestIgnoreBlock(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ".gitignore")
	os.WriteFile(file, []byte("node_modules/\n*.log"), 0o644)
	w := gen.IgnoreSection{Owner: gen.OwnerSetup}
	if ch, err := w.WriteIgnore(dir, []string{"/.heron/"}); !ch || err != nil {
		t.Fatalf("first write: %v %v", ch, err)
	}
	if ch, _ := w.WriteIgnore(dir, []string{"/.heron/"}); ch {
		t.Error("an unchanged block is rewritten")
	}
	b, _ := os.ReadFile(file)
	os.WriteFile(file, append(b, []byte("dist/\n")...), 0o644)
	if ch, _ := w.WriteIgnore(dir, []string{"/.heron/", "/vendor/x/"}); !ch {
		t.Error("a changed block isn't written")
	}
	b, _ = os.ReadFile(file)
	want := "node_modules/\n*.log\n\n" + gen.BlockBegin + "\n# from heron run setup\n/.heron/\n/vendor/x/\n" + gen.BlockEnd + "\ndist/\n"
	if string(b) != want {
		t.Errorf("got %q", b)
	}
}

func TestLockRoundTrip(t *testing.T) {
	l := &setup.Lock{
		Packages: map[string]setup.Pin{"std/spec": {Version: "1.0", Checksum: "sha256:ab"}, "acme_x": {Version: "2.1", Checksum: "sha256:cd"},
			"github.com/acme/specs/heron": {Version: "1.2.0", Checksum: "sha256:12", URL: "https://github.com/acme/specs", Path: "heron", Ref: "v1.2.0", Commit: "9a1b"}},
		Sources: map[string]setup.SourcePin{"mailer": {URL: "https://example.com/m", Ref: "v1", Commit: "3c8e"}},
		Tools:   map[string]registry.ToolPin{"vale": {Version: "v3.28.1", Checksum: "sha256:ef"}},
	}
	file := filepath.Join(t.TempDir(), setup.LockFile)
	os.WriteFile(file, []byte(l.Format()), 0o644)
	got, err := setup.ReadLock(file)
	if err != nil {
		t.Fatal(err)
	}
	if got.Format() != l.Format() {
		t.Errorf("round trip:\n%s\nvs\n%s", got.Format(), l.Format())
	}
	if g := got.Packages["github.com/acme/specs/heron"]; !g.Git() || g.Path != "heron" || g.Ref != "v1.2.0" || g.Commit != "9a1b" {
		t.Errorf("git pin: %+v", g)
	}
	if got.Packages["std/spec"].Checksum != "sha256:ab" || got.Sources["mailer"].Commit != "3c8e" || got.Tools["vale"].Version != "v3.28.1" {
		t.Errorf("read: %+v", got)
	}
	// The Example tab's lock reads too.
	ex := "packages.{\n  \"std/ci\" = { version = \"1.4.2\", checksum = \"sha256:9f2c41\" }\n}\n"
	os.WriteFile(file, []byte(ex), 0o644)
	if got, err := setup.ReadLock(file); err != nil || got.Packages["std/ci"].Version != "1.4.2" {
		t.Errorf("example lock: %+v %v", got, err)
	}
}

// TestGenerateRunsEveryTarget: impl.generate writes every target's files,
// as `heron generate` without targets does, and both sections of the
// managed ignore block, and a second call writes nothing.
func TestGenerateRunsEveryTarget(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.heron"), []byte("import = [\"std/impl@1\"]\n\nrepo: project(\".\") {\n  gen: task(List(String)) do {\n    return generate()\n  }\n}\n"), 0o644)
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatalf("doesn't check: %v", res.Diags)
	}
	r := registry.New()
	setup.Register(r, setup.Options{})
	r.SetGenerator(registry.Generator{Path: "sample.out", Target: "sample", Generate: func(_ context.Context, in any) ([]registry.GeneratedFile, error) {
		return []registry.GeneratedFile{{Path: "out/x.txt", Content: []byte("x\n"), Ignored: true}}, nil
	}})
	m := eval.New(res.Tree, res.Checker, r, eval.Caps{})
	m.Dir = dir
	v, err := m.Call(context.Background(), res.Tree.Find("repo.gen"), eval.Args{})
	if err != nil {
		t.Fatal(err)
	}
	if got := eval.Str(v); !strings.Contains(got, "out/x.txt") || !strings.Contains(got, ".gitignore") {
		t.Errorf("wrote %s", got)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "out", "x.txt")); err != nil || string(b) != "x\n" {
		t.Errorf("out/x.txt: %q %v", b, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	for _, w := range []string{"# from heron generate\n/.heron/\n/out/x.txt\n", "# from heron run setup\n/.heron/\n"} {
		if !strings.Contains(string(b), w) {
			t.Errorf(".gitignore has no %q: %s", w, b)
		}
	}
	v, err = m.Call(context.Background(), res.Tree.Find("repo.gen"), eval.Args{})
	if err != nil || eval.Str(v) != "[]" {
		t.Errorf("a second run wrote %s, %v", eval.Str(v), err)
	}
}
