package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// schemaTree is a package of interface elements with kinds, annotations,
// flags and categories of its own, and types with nested fields.
const schemaTree = "testdata/schema/index.heron"

// siteFiles renders a site and returns its files by path.
func siteFiles(t *testing.T, s *Site) map[string]string {
	t.Helper()
	files, err := s.Files()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[f.Path] = string(f.Content)
	}
	return out
}

// mainOf is a page's main column.
func mainOf(h string) string {
	i := strings.Index(h, `<main id="main"`)
	j := strings.Index(h, `</main>`)
	if i < 0 || j < i {
		return ""
	}
	return h[i:j]
}

// The Reference area gathers the API reference, the schema reference and
// the commands, each part under its heading where more than one has a
// page; the API pages keep their place and look; commands leave the Docs
// area, and the section that holds them links them.
func TestReferenceParts(t *testing.T) {
	t.Parallel()
	f := siteFiles(t, site(t, schemaTree))
	nav := regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`).FindString(f["Server.html"])
	mustHave(t, "reference nav", nav, `<div class="nav-part">Schema</div>`, `<details class="nav-types nav-fold" data-key="fold:ui"`, `href="_schema.ui.panel.html"`, `href="_schema.ui.shortcut.html"`, `<div class="nav-part">API</div>`)
	if i, j := strings.Index(nav, `nav-part">API`), strings.Index(nav, `nav-part">Schema`); i < 0 || j < i {
		t.Error("the API part doesn't come before the Schema part")
	}
	mustHave(t, "top bar", f["Server.html"], `<a href="Mode.html" aria-current="true">Reference</a>`)

	// A tree without std/schema has no Schema part, and its API reference
	// no part headings.
	pet := siteFiles(t, site(t, entries["petstore"]))
	for p, h := range pet {
		if strings.HasPrefix(p, "_schema") || strings.Contains(h, `class="nav-part"`) {
			t.Errorf("petstore: %s has the schema reference or a part heading", p)
		}
	}

	// The commands of the lenses' tree are in the Reference's CLI part.
	_, lf := lensFiles(t, kite)
	cmd := lf["explore/kite.cli.sync.html"]
	mustHave(t, "command page", cmd, `aria-current="true">Reference</a>`)
	mustHave(t, "command's section", lf["explore/kite.cli.html"], `href="kite.cli.sync.html"`)
}

// The landing page shows what goes inside what, then each package's
// kinds, annotations, flags by group and categories, with their anchors.
func TestSchemaLanding(t *testing.T) {
	t.Parallel()
	s := site(t, schemaTree)
	f := siteFiles(t, s)
	h := mainOf(f["_schema.html"])
	mustHave(t, "landing", h,
		`id="schema-tree"`, `What goes inside what`,
		// The tree: block at a file's top, panel in it, its controls.
		`<span class="tree-cat"><a class="ref" href="_schema.html#doc.block">block</a></span>`,
		`href="_schema.ui.panel.html"><code>panel</code></a>`,
		`<span class="tree-cat"><a class="ref" href="_schema.html#ui.control">control</a></span>`,
		// The package's parts.
		`id="ui"`, `id="ui--kinds"`, `id="ui--annotations"`, `id="ui--flags"`, `id="ui--categories"`,
		`<code>@shortcut(keys: String)</code>`,
		`<tr id="ui.primary">`, `<code>&#34;variant&#34;</code>`, `<tr id="ui.hidden">`,
		`<li id="ui.control">`,
		`A screen made of controls.`,
	)
	// A group's flags before the flags of no group.
	if strings.Index(h, `id="ui.secondary"`) > strings.Index(h, `id="ui.hidden"`) {
		t.Error("the flags of no group come before a group's")
	}
	// The core packages are listed too, each once.
	if strings.Count(h, `id="spec"`) != 1 || strings.Count(h, `id="doc"`) != 1 {
		t.Error("std/spec or std/doc isn't listed once")
	}
}

// A kind's page: its icon and docs, both forms, the summary rows, its
// parameters and fields, its example in tabs, the rules that check it with
// their codes, and its uses.
func TestKindPage(t *testing.T) {
	t.Parallel()
	s := site(t, schemaTree)
	f := siteFiles(t, s)
	panel := f["_schema.ui.panel.html"]
	mustHave(t, "panel title", panel, `<h1 class="page-title"><svg class="icon title-icon"`)
	h := mainOf(panel)
	mustHave(t, "panel", h,
		`<p class="lead">A screen made of controls.</p>`,
		`<dt>.heron</dt>`, `<dt>.heron.md</dt>`, `&lt;panel id=&#34;name&#34; title=&#34;…&#34;&gt;…&lt;/panel&gt;`,
		`<dt>Layer</dt><dd>Contract</dd>`,
		`<dt>Belongs to</dt><dd><a class="ref" href="_schema.html#doc.block"><code>block</code></a></dd>`,
		`<dt>Goes inside</dt><dd>any kind that accepts <a class="ref" href="_schema.html#doc.block"><code>block</code></a></dd>`,
		`<span class="hold-group"><a class="ref" href="_schema.html#ui.control"><code>control</code></a>: <a class="ref" href="_schema.ui.button.html"><code>button</code></a>, <a class="ref" href="_schema.ui.input.html"><code>input</code></a></span>`,
		`other: <a class="ref" href="_schema.ui.footer.html"><code>footer</code></a> <span class="mark">at most 1</span>`,
		`id="ui.panel.params"`, `<code class="fname">title</code>`,
		`id="ui.panel.example"`, `role="tablist" aria-label="Example forms"`, `>.heron</button>`, `>.heron.md</button>`, `>Rendered</button>`,
		`id="ui.panel.checked_by"`, `<code class="code-id">footer</code>`,
		`id="ui.panel.used_in"`, `1 node of the tree`,
	)
	button := mainOf(f["_schema.ui.button.html"])
	mustHave(t, "button", button,
		`<dt>Annotations</dt><dd><a class="ref" href="_schema.ui.shortcut.html"><code>@shortcut</code></a>`,
		`<span class="any-node">any node: `,
		`<dt>Flags</dt><dd><span class="hold-group"><code>&#34;variant&#34;</code>: <a class="ref" href="_schema.html#ui.primary"><code>&lt;primary&gt;</code></a>`,
		`<code>&lt;hidden&gt;</code>`,
	)
	// A footer is held by a panel, and its example stands in one.
	footer := mainOf(f["_schema.ui.footer.html"])
	mustHave(t, "footer", footer, `<dt>Goes inside</dt><dd><a class="ref" href="_schema.ui.panel.html"><code>panel</code></a></dd>`, `panel_example`)
	// The fields a kind's body declares.
	mustHave(t, "input", mainOf(f["_schema.ui.input.html"]), `id="ui.input.fields"`, `<code class="fname">placeholder</code>`)
	// References name categories by their names, never expanded.
	rule := mainOf(f["_schema.spec.rule.html"])
	mustHave(t, "rule", rule, `<dt>References</dt><dd><a class="ref" href="_schema.spec.actor.html"><code>actor</code></a>`)
	cmd := mainOf(f["_schema.spec.command.html"])
	mustHave(t, "command", cmd, `<dt>References</dt><dd><a class="ref" href="_schema.html#spec.touchable"><code>touchable</code></a></dd>`)
}

// Every example on a kind page checks clean in each form it shows, and
// the rendered tab is the renderer's own view, inert.
func TestGeneratedExamples(t *testing.T) {
	t.Parallel()
	s := site(t, entries["spec"])
	shown := 0
	for _, p := range s.schema.pkgs {
		for _, k := range p.kinds {
			ex := s.example(k)
			if ex == nil {
				continue
			}
			shown++
			dir := t.TempDir()
			for _, form := range []struct{ file, src string }{{"index.heron", ex.heron}, {"example.heron.md", ex.md}} {
				if form.src == "" {
					continue
				}
				d := filepath.Join(dir, strings.ReplaceAll(form.file, ".", "_"))
				os.MkdirAll(d, 0o755)
				idx := "import = [\"std/doc@1\", \"std/docs@1\", \"std/grammar@1\", \"std/types@1\", \"std/schema@1\"]\n\n"
				if form.file == "index.heron" {
					idx += form.src + "\n"
				} else {
					idx += "include = [\"./example.heron.md\"]\n"
					os.WriteFile(filepath.Join(d, form.file), []byte(form.src+"\n"), 0o644)
				}
				os.WriteFile(filepath.Join(d, "index.heron"), []byte(idx), 0o644)
				id, _ := resolve.Canonical(filepath.Join(d, "index.heron"))
				if res := check.NewSession().Check(id); res.Diags.HasErrors() {
					t.Errorf("%s: the %s example doesn't check: %v", k.Path(), form.file, res.Diags)
				}
			}
			if strings.Contains(ex.rendered, ` id="`) || strings.Contains(ex.rendered, `<a `) {
				t.Errorf("%s: the rendered example has ids or links", k.Path())
			}
		}
	}
	if shown < 40 {
		t.Errorf("only %d kinds of the spec's packages have an example", shown)
	}
}

// An annotation's page: its signature, what it attaches to, whether it's
// repeatable, its parameters and the rules that check it.
func TestAnnotationPage(t *testing.T) {
	t.Parallel()
	f := siteFiles(t, site(t, schemaTree))
	h := mainOf(f["_schema.ui.shortcut.html"])
	mustHave(t, "shortcut", h, `<figcaption class="code-title"><span>index.heron</span>`,
		`<dt>Attaches to</dt><dd><a class="ref" href="_schema.html#ui.control"><code>control</code></a></dd>`,
		`<dt>Repeatable</dt><dd>No</dd>`, `<code class="fname">keys</code>`, `id="ui.shortcut.checked_by"`, `id="rules.one_shortcut--ui.shortcut"`)
	mustHave(t, "@shortcut title", f["_schema.ui.shortcut.html"], `<h1 class="page-title">@shortcut</h1>`)
	touch := mainOf(f["_schema.spec.touch.html"])
	mustHave(t, "touch", touch, `<dt>Attaches to</dt><dd>any node</dd>`, `std/spec/package.heron`)
	cov := mainOf(f["_schema.schema.highlight.html"])
	mustHave(t, "highlight", cov, `<dt>Repeatable</dt><dd>No</dd>`)
}

// A type page shows its fields in three tabs: the field table with nested
// types opening in place, every field by its dotted path with its own
// anchor, and the declaration beside a value of it that checks.
func TestTypeViews(t *testing.T) {
	t.Parallel()
	s := site(t, schemaTree)
	f := siteFiles(t, s)
	h := mainOf(f["Server.html"])
	mustHave(t, "server", h,
		`role="tablist" aria-label="Field views"`,
		`<button type="button" role="tab" id="views-Server-fields-tab" aria-controls="views-Server-fields" aria-selected="true">Fields</button>`,
		`>Flat</button>`, `>Example</button>`,
		`<details class="nested-fields"><summary>Fields</summary>`,
		`<tr id="Server:tls.cert">`, `<code class="fname">tls.cert</code>`,
		`<tr id="Server:ports[].number">`,
		`<code>STRICT</code> <code>LAX</code>`,
		`<div class="type-example">`,
	)
	// The fields of fields are in the search index, by their paths.
	found := false
	for _, e := range s.Search() {
		if e.Title == "Server.tls.cert" && strings.HasSuffix(e.URL, "#Server:tls.cert") {
			found = true
		}
	}
	if !found {
		t.Error("Server.tls.cert isn't in the search index")
	}
	// The value checks, in a copy of the tree.
	dir := t.TempDir()
	src, _ := os.ReadFile(schemaTree)
	var tr *resolve.Node
	for _, x := range s.types {
		if x.Name == "Server" {
			tr = x
		}
	}
	v := s.typeValue(tr)
	if !strings.Contains(v, "tls = Tls(") || !strings.Contains(v, "ports = [Port(number = 0)]") {
		t.Errorf("the example value is %q", v)
	}
	os.WriteFile(filepath.Join(dir, "index.heron"), append(src, []byte("\n"+v+"\n")...), 0o644)
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	if res := check.NewSession().Check(id); res.Diags.HasErrors() {
		t.Errorf("the example value doesn't check: %v", res.Diags)
	}
}

// The search index holds the kinds, annotations, flags and categories of
// the standard packages, by their names as they're written.
func TestSchemaSearch(t *testing.T) {
	t.Parallel()
	s := site(t, schemaTree)
	want := map[string]string{"rule": "_schema.spec.rule.html", "@touch": "_schema.spec.touch.html", "<external>": "_schema.html#spec.external", "block": "_schema.html#doc.block"}
	for _, e := range s.Search() {
		if u, ok := want[e.Title]; ok && e.URL == u {
			delete(want, e.Title)
		}
	}
	for k := range want {
		t.Errorf("%s isn't in the search index", k)
	}
}

// The agent export writes a file per schema package, each kind as its
// signature and its summary's rows as key: value lines.
func TestSchemaAgentExport(t *testing.T) {
	t.Parallel()
	id, _ := resolve.Canonical(schemaTree)
	res := check.NewSession().Check(id)
	files, err := Agents(res.Tree, res.Checker, AgentOptions{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]string{}
	for _, f := range files {
		by[f.Path] = string(f.Content)
	}
	ui := by["_schema.ui.md"]
	mustHave(t, "ui export", ui, "### panel(title: String)\n", "layer: contract\n", "belongs to: `block`\n", "goes inside: any kind that accepts `block`\n",
		"holds: `control` (`button`, `input`); other (`footer` (at most 1)); prose\n", "### @shortcut(keys: String)\n", "attaches to: `control`\n", "### <primary>\n", "group: \"variant\"\n")
	mustHave(t, "spec export", by["_schema.spec.md"], "### rule(refs<variadic>?: Ref(Any))\n", "references: `actor`, `part`, `goal`, `quality`, `assumption`, `feature`\n")
	mustHave(t, "index", by[AgentIndex], "## Schema", "(_schema.ui.md)")
}

// Without a theme the top bar shows Heron's icon and pages link its 16 px
// icon, each in its version for light backgrounds; a theme's logo and its
// light version replace the mark, the title kept for screen readers where
// the logo holds the name.
func TestBrandMarks(t *testing.T) {
	t.Parallel()
	f := siteFiles(t, site(t, schemaTree))
	mustHave(t, "mark", f["Server.html"],
		`<img class="mark mark-dark" src="_assets/heron-icon.svg"`, `<img class="mark mark-light" src="_assets/heron-icon-light.svg"`,
		`<link rel="icon" type="image/svg+xml" href="_assets/heron-16.svg">`, `href="_assets/heron-16-light.svg" media="(prefers-color-scheme: light)"`)
	for _, a := range []string{"_assets/heron-icon.svg", "_assets/heron-16-light.svg"} {
		if f[a] == "" {
			t.Errorf("%s isn't written", a)
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ThemeFile), []byte(`{"logo": "a.svg", "logoLight": "b.svg", "wordmark": true}`), 0o644)
	os.WriteFile(filepath.Join(dir, "a.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o644)
	os.WriteFile(filepath.Join(dir, "b.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), 0o644)
	th, err := LoadTheme(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	s := site(t, schemaTree)
	s.Theme = th
	mark, cls := s.logoMark()
	if cls != "logo wordmark" || !strings.Contains(mark, `src="_assets/theme/a.svg"`) || !strings.Contains(mark, `class="mark mark-light" src="_assets/theme/b.svg"`) {
		t.Errorf("the theme's logo shows as %q %q", cls, mark)
	}
}
