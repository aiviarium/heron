package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The petstore's docs block: the orders scope as a landing page, the store
// api in the reference, a sidebar slot on every page and a slot after
// every rule.
func TestLayoutsAndSlots(t *testing.T) {
	s := site(t, entries["petstore"])
	if s.Config.Node == nil {
		t.Fatal("the petstore's docs block isn't read")
	}
	orders := s.PageByFile("orders.html")
	if orders == nil || orders.Layout != "landing" {
		t.Fatalf("orders: %+v", orders)
	}
	h, err := s.RenderPage(orders)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-layout="landing"`, `class="layout no-sidebar"`, `<a class="tile" href="orders.ordering.html"><span class="tile-title">Ordering</span><span class="tile-text">Customers order a pet`} {
		if !strings.Contains(h, want) {
			t.Errorf("landing page lacks %q", want)
		}
	}
	if strings.Contains(h, `class="sidebar"`) {
		t.Error("a landing page has no sidebar")
	}
	if p := s.PageByFile("store.html"); p == nil || p.Layout != "api" || p.Area.Key != "reference" {
		t.Errorf("store: %+v", p)
	}
	h, err = s.RenderPage(s.PageByFile("orders.ordering.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h, `<div class="slot slot-sidebar"><div class="prose"><p>Questions about the store go to the store team.</p></div></div></div></nav>`) {
		t.Error("the sidebar slot isn't at the end of the sidebar")
	}
	if n := strings.Count(h, `<div class="slot slot-after" data-of="spec.rule">`); n != 4 {
		t.Errorf("want the rule slot after each of the 4 rules, got %d", n)
	}
	// The docs block itself is never content.
	for _, p := range s.Pages {
		if p.Node == s.Config.Node {
			t.Error("the docs block has a page")
		}
	}
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A theme package found in node_modules by its major version: its tokens,
// font, logo and CSS in the theme layer, written into _assets/theme/.
func TestTheme(t *testing.T) {
	sample, _ := filepath.Abs("../../../web/samples/docs-theme")
	src := "import = [\"std/doc@1\", \"std/docs@1\"]\n\nguide: section {\n  | Read me.\n}\n\ndocs {\n  theme = \"npm:@heron/sample-docs-theme@1\"\n}\n"
	dir := writeTree(t, map[string]string{"index.heron": src})
	os.MkdirAll(filepath.Join(dir, "node_modules", "@heron"), 0o755)
	if err := os.Symlink(sample, filepath.Join(dir, "node_modules", "@heron", "sample-docs-theme")); err != nil {
		t.Fatal(err)
	}
	s := site(t, filepath.Join(dir, "index.heron"))
	if s.ThemeErr != nil {
		t.Fatal(s.ThemeErr)
	}
	files, err := s.Files()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Content)
	}
	css := got["_assets/theme/theme.css"]
	for _, want := range []string{
		"@layer heron, theme;\n@layer theme {",
		`@font-face { font-family: "Fraunces"; src: url("fonts/fraunces-latin-600-normal.woff2") format("woff2"); font-weight: 600;`,
		"--h-accent: #5fd4c4;", "--h-font-display: Fraunces", "--h-radius-card: 12px;",
		"[data-theme=\"light\"] {\n  --h-accent: #0b6e64;",
		"font-family: var(--h-font-display)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("theme.css lacks %q:\n%s", want, css)
		}
	}
	light := css[strings.Index(css, "[data-theme=\"light\"]"):]
	if strings.Contains(light[:strings.Index(light, "}")], "--h-font-display") {
		t.Error("theme-independent tokens are repeated for light")
	}
	for _, f := range []string{"_assets/theme/logo.svg", "_assets/theme/fonts/fraunces-latin-600-normal.woff2"} {
		if got[f] == "" {
			t.Errorf("%s isn't written", f)
		}
	}
	page := got["guide.html"]
	for _, want := range []string{`<link rel="stylesheet" href="_assets/docs.css"><link rel="stylesheet" href="_assets/theme/theme.css">`, `<img class="mark" src="_assets/theme/logo.svg"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(got["_assets/docs.css"], "@layer heron{") {
		t.Error("the built-in theme isn't in the heron layer, so a theme layer can't win over it")
	}

	// Another major version, or a missing package, is an error.
	for spec, want := range map[string]string{
		"npm:@heron/sample-docs-theme@2": "node_modules has @heron/sample-docs-theme@1.0.0",
		"npm:@acme/heron-theme@2":        "isn't installed",
		"./nope":                         "no folder",
	} {
		os.WriteFile(filepath.Join(dir, "index.heron"), []byte(strings.Replace(src, "npm:@heron/sample-docs-theme@1", spec, 1)), 0o644)
		s := site(t, filepath.Join(dir, "index.heron"))
		if _, err := s.Files(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q, got %v", spec, want, err)
		}
	}
}

// A changelog lists its sections as entries on its own page.
func TestChangelogLayout(t *testing.T) {
	src := "import = [\"std/doc@1\", \"std/docs@1\"]\n\nchanges: section {\n  v1_1: section {\n    | Adds search.\n  }\n  v1_0: section {\n    | The first release.\n  }\n}\n\ndocs {\n  layout(\"changelog\") {\n    sections = [changes]\n  }\n}\n"
	dir := writeTree(t, map[string]string{"index.heron": src})
	s := site(t, filepath.Join(dir, "index.heron"))
	p := s.PageByFile("changes.html")
	if p == nil || p.Layout != "changelog" || len(p.Children) != 0 || s.PageByFile("changes.v1_1.html") != nil {
		t.Fatalf("changes: %+v", p)
	}
	f, err := s.PageFragment(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.HTML, `<article class="entry" id="changes.v1_1"><div class="entry-head"><h2 class="h">`) || strings.Index(f.HTML, "Adds search") > strings.Index(f.HTML, "The first release") {
		t.Errorf("changelog:\n%s", f.HTML)
	}
}
