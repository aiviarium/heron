package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Prose renders its inline forms: an insert of a prose value as that prose,
// an insert of any other value as its expression in code, a node link as a
// link to the node, an insert of a node with no value as a link to it,
// inserted text as the characters it holds, and a tag through its kind's
// renderer.
func TestMarkupRenders(t *testing.T) {
	dir := t.TempDir()
	src := `import = ["std/doc@1"]

limit = 30
sym = "<b>$"
reminder: Prose =| Check the **window** first.

guide: section {
  | Refunds close after $limit days. $reminder See [the window](#window).
  |
  | <warning>A refund can't be undone.</warning>
  |
  | Press <kbd>R</kbd>. The window: $window. A symbol: $sym.
  |
  | - *first* and **second**
  | - second, \$literal
  |
  | ~~~json
  | {"limit": "$limit"}
  | ~~~

  window: section {{
    Thirty days.
  }}
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s := site(t, filepath.Join(dir, "index.heron"))
	n := s.Tree.Find("guide")
	if n == nil {
		t.Fatal("no guide")
	}
	f, err := s.RenderNode(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<code class="value"><span class="tk-num">30</span></code>`,
		`<strong>window</strong>`,
		`<a class="ref" href="guide.window.html">the window</a>`,
		`callout-warning`,
		`<kbd>R</kbd>`,
		`<a class="ref" href="guide.window.html"><code>window</code></a>`,
		`&lt;b&gt;$`,
		`<em>first</em>`,
		`$literal`,
		`{&#34;limit&#34;: &#34;$limit&#34;}`,
	} {
		if !strings.Contains(f.HTML, want) {
			t.Errorf("guide lacks %q:\n%s", want, f.HTML)
		}
	}
}

// A link may name a declaration of a package the file imports: it checks,
// and renders as a reference with no page, since the site has none for it.
func TestLinkToImportedDeclaration(t *testing.T) {
	dir := t.TempDir()
	src := "import = [\"std/doc@1\", \"std/spec@1\"]\n\nguide: section {\n  | Write a [rule](#spec.rule).\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s := site(t, filepath.Join(dir, "index.heron"))
	f, err := s.RenderNode(s.Tree.Find("guide"))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<span class="ref">rule</span>`; !strings.Contains(f.HTML, want) {
		t.Errorf("guide lacks %q:\n%s", want, f.HTML)
	}
}
