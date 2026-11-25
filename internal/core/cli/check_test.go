package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/helightdev/specs-v0/internal/core/cli"
	"github.com/helightdev/specs-v0/internal/core/diag"
	"github.com/helightdev/specs-v0/internal/core/lint"
	"github.com/helightdev/specs-v0/internal/core/registry"
	"github.com/helightdev/specs-v0/internal/core/resolve"
)

func repo(t *testing.T, files map[string]string) string {
	dir := t.TempDir()
	for n, s := range files {
		p := filepath.Join(dir, n)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	return dir
}

func TestCheckCommand(t *testing.T) {
	r := registry.New()
	cli.Register(r)
	dir := repo(t, map[string]string{
		"index.heron": "import = [\"std/doc@1\"]\nx: sektion\n",
	})
	e, out, _ := env(dir)
	if code := cli.Main(r, []string{"check"}, e); code != 1 {
		t.Fatalf("code %d, out %s", code, out)
	}
	if !strings.Contains(out.String(), "index.heron:2:4: error: unknown kind or type sektion; did you mean section? [unresolved]") {
		t.Errorf("out %q", out)
	}
	os.WriteFile(filepath.Join(dir, "index.heron"), []byte("import = [\"std/doc@1\"]\nx: section {\n  title_ok = 1\n}\n"), 0o644)
	e, out, _ = env(dir)
	if code := cli.Main(r, []string{"check", "--json", "index.heron"}, e); code != 1 {
		t.Fatalf("json: code %d", code)
	}
	var docs []struct {
		Entry       string
		Diagnostics []struct {
			Code string
			Line int
		}
		Tree struct {
			Children []struct {
				Name, Kind string
			}
		}
	}
	if err := json.Unmarshal(out.Bytes(), &docs); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(docs) != 1 || docs[0].Entry != "index.heron" || len(docs[0].Diagnostics) != 1 || docs[0].Diagnostics[0].Code != "unknown-field" || docs[0].Diagnostics[0].Line != 3 {
		t.Errorf("json: %s", out)
	}
	if len(docs[0].Tree.Children) != 1 || docs[0].Tree.Children[0].Kind != "doc.section" {
		t.Errorf("json tree: %s", out)
	}
}

// TestSampleLint proves the lint extension point: a lint registered from
// outside the unit runs through `heron lint` like the core ones.
func TestSampleLint(t *testing.T) {
	r := registry.New()
	cli.Register(r)
	r.MustAddLint(registry.Lint{Name: "sample-no-todo", Summary: "flags nodes named todo", Run: func(in any) {
		li := in.(*lint.Input)
		li.Tree.Walk(func(n *resolve.Node) {
			if n.Name == "todo" {
				li.Report(n, n.Decl, diag.Warning, "a node named todo")
			}
		})
	}})
	dir := repo(t, map[string]string{
		"index.heron": "import = [\"std/doc@1\"]\ntodo: section\n@lint(allow = [\"sample-no-todo\"])\nx: section {\n  todo: section\n}\n",
	})
	e, out, _ := env(dir)
	if code := cli.Main(r, []string{"lint"}, e); code != 1 {
		t.Fatalf("code %d: %s", code, out)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "index.heron:2:1: warning: a node named todo [sample-no-todo]") {
		t.Errorf("out %q", out)
	}
	e, out, _ = env(dir)
	if code := cli.Main(r, []string{"lint", "--json"}, e); code != 1 || !strings.Contains(out.String(), `"code": "sample-no-todo"`) {
		t.Errorf("json: %d %s", code, out)
	}
}
