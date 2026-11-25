package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const homeSrc = `import = ["std/doc@1", "std/docs@1"]

/// The first line of a doc comment
/// and its second line.
///
/// A paragraph of its own.
overview: section {
  | Hello.
}

docs {
  depth = 3
}
`

// A docs block at the root configures the site and isn't a card on the
// home page; consecutive /// lines join into one paragraph, and a blank
// /// line starts the next.
func TestHomeAndDocComments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte(homeSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s := site(t, filepath.Join(dir, "index.heron"))
	if s.Config.Depth != 3 {
		t.Fatalf("depth %d, want 3", s.Config.Depth)
	}
	home, err := s.PageFragment(s.Home)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`data-kind="docs.site"`, "Depth"} {
		if strings.Contains(home.HTML, bad) {
			t.Errorf("the home page shows the docs block (%s):\n%s", bad, home.HTML)
		}
	}
	f, err := s.RenderNode(s.Tree.Find("overview"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.HTML, "<p>The first line of a doc comment and its second line.</p>") ||
		!strings.Contains(f.HTML, "<p>A paragraph of its own.</p>") {
		t.Errorf("doc lines don't join into paragraphs:\n%s", f.HTML)
	}
}
