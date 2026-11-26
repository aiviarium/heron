package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/fixtures"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/std"
	uistrings "github.com/aiviarium/heron/web/strings"
)

func site(t *testing.T, entry string) *Site {
	t.Helper()
	if entry == "" && specMissing != nil {
		t.Fatal(specMissing)
	}
	id, err := resolve.Canonical(entry)
	if err != nil {
		t.Fatal(err)
	}
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatalf("%s doesn't check", entry)
	}
	r := registry.New()
	Register(r)
	return New(res.Tree, res.Checker, Options{Registry: r})
}

var entries = map[string]string{
	"petstore":  "../../../examples/petstore/index.heron",
	"kinds":     "testdata/kinds/index.heron",
	"shop":      "../../../examples/shop/index.heron",
	"shop-spec": "../../../examples/shop-spec/index.heron",
}

// The spec package's entry, as the repository imports it: from
// $HERON_SPECS_DIR or .heron/packages/ (scripts/spec-setup.sh puts it there).
func init() {
	if e, err := fixtures.SpecEntry(filepath.Join("..", "..", "..")); err == nil {
		entries["spec"] = e
	} else {
		specMissing = err
	}
}

var specMissing error

var (
	scriptRe = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>|<title>.*?</title>`)
	attrRe   = regexp.MustCompile(`(?:aria-label|title|placeholder|alt)="([^"]*)"`)
	wordRe   = regexp.MustCompile(`[\p{L}][\p{L}'’]*`)
)

// visibleText returns the text a reader sees or hears on a page: text
// nodes and the labels of controls.
func visibleText(h string) []string {
	var out []string
	for _, m := range attrRe.FindAllStringSubmatch(h, -1) {
		out = append(out, htmlUnescape(m[1]))
	}
	body := h
	if i := strings.Index(h, "<body"); i >= 0 {
		body = h[i:]
	}
	body = scriptRe.ReplaceAllString(body, " ")
	out = append(out, stripTags(body))
	return out
}

// Every word a page shows comes from the spec (its files, or the standard
// packages its kinds are declared in) or from the catalogue: the renderer
// adds headings, labels, navigation and values, never sentences of its own.
func TestVisibleTextComesFromSpecOrCatalogue(t *testing.T) {
	for name, entry := range entries {
		checkVisibleText(t, name, site(t, entry))
	}
}

// checkVisibleText fails on any word a site's pages show that is neither
// in its files, the standard packages nor the catalogue.
func checkVisibleText(t *testing.T, name string, s *Site) {
	t.Helper()
	allowed := map[string]bool{}
	addWords := func(s string) {
		for _, w := range wordRe.FindAllString(s, -1) {
			allowed[strings.ToLower(w)] = true
			for _, part := range splitName(w) {
				allowed[strings.ToLower(part)] = true
			}
		}
		// Names split at underscores and case changes, as display names are.
		for _, w := range regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`).FindAllString(s, -1) {
			for _, part := range splitName(w) {
				allowed[strings.ToLower(part)] = true
			}
		}
	}
	for _, v := range uistrings.All() {
		addWords(v)
	}
	fs.WalkDir(std.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := std.FS.ReadFile(p)
			addWords(string(b))
		}
		return nil
	})
	// The values the renderer computes: HTTP statuses and request forms.
	addWords("curl HTTP Content Type application json BASE_URL const result await")
	{
		local := map[string]bool{}
		src := append([]*resolve.File(nil), s.Tree.Files...)
		for _, imp := range s.Tree.Imports {
			// A project shows the names of the spec package it implements.
			if imp.Tree != nil && imp.Tree.Entry != nil && !imp.Tree.Entry.ID.IsStd() {
				src = append(src, imp.Tree.Files...)
			}
		}
		for _, f := range src {
			for _, w := range wordRe.FindAllString(f.Src, -1) {
				local[strings.ToLower(w)] = true
			}
			for _, w := range regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`).FindAllString(f.Src, -1) {
				for _, part := range splitName(w) {
					local[strings.ToLower(part)] = true
				}
			}
		}
		files, err := s.Files()
		if err != nil {
			t.Fatal(err)
		}
		bad := map[string]string{}
		for _, f := range files {
			if !strings.HasSuffix(f.Path, ".html") {
				continue
			}
			for _, text := range visibleText(string(f.Content)) {
				for _, w := range wordRe.FindAllString(text, -1) {
					lw := strings.ToLower(strings.TrimRight(w, "'’"))
					lw = strings.TrimSuffix(strings.TrimSuffix(lw, "'s"), "’s")
					if len([]rune(lw)) > 1 && !allowed[lw] && !local[lw] {
						if _, ok := bad[w]; !ok {
							bad[w] = f.Path
						}
					}
				}
			}
		}
		for w, p := range bad {
			t.Errorf("%s: %q on %s comes neither from the spec nor from the catalogue", name, w, p)
		}
	}
}

// The catalogue has none of the generated-UI tells.
func TestCatalogueWording(t *testing.T) {
	banned := regexp.MustCompile(`(?i)seamless|powerful|effortless|let's|note that|learn more|powered by|auto-generated|\bnew\b|!|\?|[\x{1F300}-\x{1FAFF}\x{2728}]`)
	for k, v := range uistrings.All() {
		if banned.MatchString(v) {
			t.Errorf("%s = %q reads like generated UI", k, v)
		}
		if strings.HasSuffix(v, ".") {
			t.Errorf("%s = %q: labels don't end with a full stop", k, v)
		}
	}
}

// Every page renders, every id is unique, and every link within the site
// resolves to a page and an anchor that exist.
func TestLinksResolve(t *testing.T) {
	idRe := regexp.MustCompile(` id="([^"]+)"`)
	hrefRe := regexp.MustCompile(` href="([^"]+)"`)
	for name, entry := range entries {
		s := site(t, entry)
		files, err := s.Files()
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]map[string]bool{}
		for _, f := range files {
			if !strings.HasSuffix(f.Path, ".html") {
				continue
			}
			ids[f.Path] = map[string]bool{}
			for _, m := range idRe.FindAllStringSubmatch(string(f.Content), -1) {
				id := htmlUnescape(m[1])
				if ids[f.Path][id] {
					t.Errorf("%s: %s: id %q appears twice", name, f.Path, id)
				}
				ids[f.Path][id] = true
			}
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Path, ".html") {
				continue
			}
			for _, m := range hrefRe.FindAllStringSubmatch(string(f.Content), -1) {
				href := htmlUnescape(m[1])
				if strings.Contains(href, "://") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "data:") {
					continue
				}
				file, frag, _ := strings.Cut(href, "#")
				frag = strings.ReplaceAll(frag, "%23", "#")
				if file == "" {
					file = f.Path
				}
				file = filepath.ToSlash(filepath.Clean(file))
				if strings.HasPrefix(file, "_assets/") {
					continue
				}
				if ids[file] == nil {
					t.Errorf("%s: %s links to %s, which doesn't exist", name, f.Path, href)
					continue
				}
				if frag != "" && !ids[file][frag] {
					t.Errorf("%s: %s links to %s, whose anchor doesn't exist", name, f.Path, href)
				}
			}
		}
	}
}

// A kind without a renderer falls back to its fields and children, and a
// registered renderer replaces a built-in one.
func TestRendererRegistry(t *testing.T) {
	s := site(t, entries["petstore"])
	n := s.Tree.Find("orders.single_sale")
	if n == nil {
		t.Fatal("no orders.single_sale")
	}
	f, err := s.RenderNode(n)
	if err != nil || !strings.Contains(f.HTML, `<strong class="strength">must</strong>`) || !strings.Contains(f.HTML, "A pet ") {
		t.Fatalf("rule on a type: %v\n%s", err, f.HTML)
	}
	r := s.Opts.Registry
	r.SetRenderer(registry.Renderer{Kind: "spec.rule", Name: "test", Render: func(ctx any) error {
		c := ctx.(*Ctx)
		c.W(`<p class="custom">`)
		c.Text(c.Node.Name)
		c.W(`</p>`)
		return nil
	}})
	f, _ = s.RenderNode(n)
	if !strings.Contains(f.HTML, `<p class="custom">single_sale</p>`) {
		t.Fatalf("registered renderer not used:\n%s", f.HTML)
	}
	// A project has a built-in renderer; a task's fields fall back.
	if p := s.Tree.Find("petstore"); p != nil {
		if f, err := s.RenderNode(p); err != nil || !strings.Contains(f.HTML, "Petstore") {
			t.Fatalf("project: %v %s", err, f.HTML)
		}
	}
}

// Tasks and their @needs render as a dependency graph with a table.
func TestDependencyGraph(t *testing.T) {
	dir := t.TempDir()
	src := "import = [\"std/doc@1\", \"std/process@1\"]\n\nbuild.{\n  compile: task do {\n    log(\"compile\")\n  }\n\n  @needs(compile)\n  test: task do {\n    log(\"test\")\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s := site(t, filepath.Join(dir, "index.heron"))
	f, err := s.RenderNode(s.Tree.Find("build"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`class="graph"`, `data-from="build.compile" data-to="build.test"`, `<th>Needs</th>`} {
		if !strings.Contains(f.HTML, want) {
			t.Errorf("dependency graph lacks %q", want)
		}
	}
}

// The op page carries everything the reference screen lists.
func TestOpPage(t *testing.T) {
	s := site(t, entries["petstore"])
	p := s.PageByFile("store.place_order.html")
	if p == nil {
		t.Fatal("no op page")
	}
	h, err := s.RenderPage(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`class="method m-post"`, `<span class="base">/v2</span>/store/orders`, // method and path, base dimmed
		`pet_lifecycle.held`,                                                                                         // the transition it performs
		`<code class="status err">409</code>`,                                                                        // errors by status
		`<p class="cause sentence">`,                                                                                 // with the rule behind it
		`-X POST &#34;$BASE_URL/v2/store/orders&#34;`, `store.place_order(`, `POST</span> /v2/store/orders HTTP/1.1`, // three forms
		`data-tryit`, `class="tryit-data"`, // Try it
		`class="prevnext"`, `class="crumbs"`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("op page lacks %q", want)
		}
	}
}

// A project's heading shows its path from the tree's root, and nothing for
// the root project, whose path "." says nothing.
func TestProjectHeadingPath(t *testing.T) {
	s := site(t, "../../../examples/petstore/index.heron")
	for name, want := range map[string]string{"petstore": "", "store_spec": "specs/store"} {
		f, err := s.RenderNode(s.Tree.Find(name))
		if err != nil {
			t.Fatal(err)
		}
		h := f.HTML[:strings.Index(f.HTML, "</h")]
		got := ""
		if i := strings.Index(h, `<code class="path">`); i >= 0 {
			got = strings.TrimSuffix(h[i+len(`<code class="path">`):], "</code>")
		}
		if got != want {
			t.Errorf("%s: heading path %q, want %q (%s)", name, got, want, h)
		}
	}
}

// A nested declaration's code reads as written at the left margin.
func TestNestedSignatureDedented(t *testing.T) {
	dir := t.TempDir()
	src := "import = [\"std/doc@1\", \"std/process@1\"]\n\nbuild.{\n  compile: task do {\n    log(\"compile\")\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s := site(t, filepath.Join(dir, "index.heron"))
	sig := regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s.signature(s.Tree.Find("build.compile")), "")
	lines := strings.Split(sig, "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "  ") || strings.HasPrefix(lines[1], "    ") || lines[2] != "}" {
		t.Fatalf("signature isn't dedented:\n%s", sig)
	}
}

// The layers' kinds render as the spec says: goals with their measure and
// what serves them, use cases as Cockburn writes them, errors with their
// projections, rules with their rationale and derived lists.
func TestLayeredKinds(t *testing.T) {
	s := site(t, entries["kinds"])
	files, err := s.Files()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"editing.html": {
			`<span class="gwt-key">And</span> the language server running`,
			`<dt>Illustrates</dt><dd><a class="ref" href="guarantees.html#guarantees.parse_all">`,
			`<span class="diag-message">can&#39;t read <span class="ph">‹path›</span></span>`,
			`exit <code>2</code>`,
		},
		"editing.check_tree.html": {
			`<li id="editing.check_tree.2"><span class="uc-num">2</span><span class="uc-text">The <a class="ref" href="index.html#checker">checker</a> parses every file.</span></li>`, `<h2 id="editing.check_tree-main" class="h">`, `<h2 id="editing.check_tree-extensions" class="h">`,
			`<span class="uc-num">2a</span><span class="uc-text">A file can&#39;t be read</span></dt>`,
			`<li class="uc-outcome"><span class="uc-num">→</span><span class="uc-text mark">Back to 1</span></li>`,
			`<span class="uc-text mark">Fails</span>`, `<span class="uc-num">2a1</span>`,
		},
		"cli.check.html": {
			`<h1 class="page-title">heron check</h1>`,
			`<p class="line-head"><code>[&lt;path&gt;…]</code>`,
			`<p class="line-head"><code>--json</code> <code><span class="tk-type">Bool</span></code><span class="sep">, </span>default <code><span class="tk-kw">false</span></code></p>`,
			`<code>2</code> can&#39;t read {path}`,
		},
		"runtime.syntax.html": {
			`<dt>Used by</dt><dd><a class="ref" href="runtime.resolve.html"><code>resolve</code></a></dd>`,
		},
		"lang.html": {
			`<a class="code-ref tk-name" href="#lang.core.member">member</a><span class="tk-punct">*</span>`,
			`Used by <a class="code-ref" href="#lang.core.file"><code>file</code></a>`,
		},
		"op_page.html": {
			`<dt>Route</dt><dd><code class="path">/docs/&lt;op&gt;</code></dd>`,
			`<div class="region" id="op_page.body.facts"><a class="anchor region-name" href="#op_page.body.facts">Facts</a>`,
		},
		"index.html": {
			`<dt>Adds</dt><dd><a class="ref" href="guarantees.html#guarantees.parse_all">Parse all</a></dd>`,
		},
		"people.html": {
			`<a class="anchor" href="#people.ci">Ci</a><span class="aliases">system</span>`,
			`<article class="card-block persona" id="people.maya">`,
		},
		"guarantees.html": {
			`<a class="ref subject" href="index.html#checker">checker</a> <strong class="strength">must</strong> read every file.`,
			`<p class="rationale"><span class="field-label">Rationale</span> A file left unread hides its errors.</p>`,
			`<span class="derived-label">Illustrated by <span class="derived-mark">derived</span></span> <a class="ref" href="editing.html#editing.saved">Saved</a>`,
			`The <a class="ref subject" href="people.html#people.writer">writer</a> <strong class="strength">should</strong>`,
		},
		"index.html#goal": {
			`<div class="goal-block" id="trust"><div class="goal-head"><span class="label">Goal</span>`,
			`<dt>Measure</dt><dd>Nine trees in ten check clean on the first CI run.</dd>`,
			`<span class="derived-label">Served by <span class="derived-mark">derived</span></span>`,
		},
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Content)
	}
	for file, snippets := range want {
		h, ok := got[strings.Split(file, "#")[0]]
		if !ok {
			t.Errorf("no page %s", file)
			continue
		}
		for _, w := range snippets {
			if !strings.Contains(h, w) {
				t.Errorf("%s lacks %s", file, w)
			}
		}
	}
}
