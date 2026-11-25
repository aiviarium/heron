package docs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/extension"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

const docsSite = "../../../web/samples/docs-site"

// The sample site's components and markup tag render through its TS
// module in the JS host; kinds it doesn't render keep theirs.
func TestTSRenderers(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node isn't installed")
	}
	if _, err := os.Stat(filepath.Join(docsSite, "node_modules/@heron/runtime/dist/host-main.js")); err != nil {
		t.Skip("web/ isn't installed and built; scripts/ci.sh does that first")
	}
	id, err := resolve.Canonical(filepath.Join(docsSite, "index.heron"))
	if err != nil {
		t.Fatal(err)
	}
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatalf("the sample doesn't check: %v", res.Diags)
	}
	r := registry.New()
	m := extension.Register(r)
	m.Warn = func(s string) { t.Log(s) }
	Register(r)
	t.Cleanup(r.Close)
	if err := r.LoadTree(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	s := New(res.Tree, res.Checker, Options{Registry: r})
	if s.RenderersErr != nil {
		t.Fatal(s.RenderersErr)
	}
	for _, k := range []string{"components.steps", "components.step", "components.status"} {
		if x := r.Renderer(k); x == nil || x.Plain == nil {
			t.Errorf("%s isn't registered from the TS module", k)
		}
	}
	if x := r.Renderer("doc.section"); x == nil || x.Name != "docs" {
		t.Error("a kind the module doesn't render keeps its built-in renderer")
	}
	h, err := s.RenderPage(s.PageByFile("guide.setup.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<ol class="steps" id="guide.setup.install" start="1"><li class="step"><p>Install the web workspace with pnpm.</p></li>`,
		`<span class="status-badge" data-level="2">ready</span>`, // the tag, its Int attribute typed
		`<div class="slot slot-header"><span class="status-badge" data-level="1">Sample</span></div>`,
		`</ol><div class="slot slot-after" data-of="components.steps"><div class="prose"><p>Each step runs in the sample&#39;s folder.</p></div></div>`,
		`<section class="section" id="guide.setup">`, // the built-in section renderer
	} {
		if !strings.Contains(h, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(h, `<span class="tag tag-status">`) {
		t.Error("the status tag fell back to a plain tag")
	}
	checkVisibleText(t, "docs-site", s)
}
