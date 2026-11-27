package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// lensFiles renders every lens of a tree, by file path.
func lensFiles(t *testing.T, entry string) (*Lenses, map[string]string) {
	t.Helper()
	id, err := resolve.Canonical(entry)
	if err != nil {
		t.Fatal(err)
	}
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatalf("%s doesn't check: %v", entry, res.Diags)
	}
	r := registry.New()
	Register(r)
	ls := NewLenses(res.Tree, res.Checker, Options{Registry: r})
	files, err := ls.Files()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[f.Path] = string(f.Content)
	}
	return ls, out
}

const kite = "testdata/lenses/index.heron"

func mustHave(t *testing.T, what, h string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(h, p) {
			t.Errorf("%s lacks %q", what, p)
		}
	}
}

func mustNotHave(t *testing.T, what, h string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(h, p) {
			t.Errorf("%s has %q", what, p)
		}
	}
}

// One run builds every chosen lens: the default one at the root with the
// assets, each other under its prefix with its own data.js, and a node has
// the same file name and anchor in every lens that keeps it.
func TestLensSiteFiles(t *testing.T) {
	_, f := lensFiles(t, kite)
	for _, p := range []string{"index.html", "data.js", "_assets/docs.css", "dev/index.html", "dev/data.js", "explore/index.html", "explore/data.js",
		"kite.cli.sync.html", "dev/kite.cli.sync.html", "explore/kite.cli.sync.html", "kite.start.html", "dev/kite.start.html"} {
		if _, ok := f[p]; !ok {
			t.Errorf("no %s", p)
		}
	}
	for p := range f {
		if strings.HasPrefix(p, "dev/_assets/") || strings.HasPrefix(p, "explore/_assets/") {
			t.Errorf("assets written twice: %s", p)
		}
	}
	v := "?v=" + assetVersion("docs.css")
	mustHave(t, "dev page", f["dev/kite.cli.sync.html"], `href="../_assets/docs.css`+v+`"`, `src="data.js"`, `id="kite.cli.sync.dry_run"`)
	mustHave(t, "user page", f["kite.cli.sync.html"], `href="_assets/docs.css`+v+`"`, `id="kite.cli.sync.dry_run"`)
	// The frame every page has, for moving between pages without a flash:
	// the canvas's scheme, background and the reader's theme before the
	// stylesheet, assets cached until they change, the first paint held
	// until the frame is parsed, and the navigation put back before paint.
	page := f["kite.cli.sync.html"]
	mustHave(t, "page chrome", page,
		`<meta name="color-scheme" content="dark light">`,
		`localStorage.getItem("heron-theme")`,
		`<link rel="expect" href="#h-layout" blocking="render">`,
		`<div class="layout" id="h-layout">`,
		`<style>@layer heron{html{color-scheme:dark;background:#111111}html[data-theme=light]{color-scheme:light;background:#ffffff}`,
		`src="_assets/docs.js?v=`+assetVersion("docs.js")+`"`,
		`sessionStorage.getItem("heron-nav:"`)
	if strings.Index(page, `heron-theme`) > strings.Index(page, `rel="stylesheet"`) || strings.Index(page, `<style>@layer heron{html{`) > strings.Index(page, `rel="stylesheet"`) {
		t.Error("the canvas style or the theme script comes after the stylesheet link")
	}
	if strings.Index(page, `heron-nav:`) < strings.Index(page, `<nav class="sidebar"`) || strings.Index(page, `heron-nav:`) > strings.Index(page, `<main`) {
		t.Error("the navigation's scroll isn't put back right after the navigation, before the page")
	}
	// Each lens's search index holds what it keeps: the user lens has no rule.
	mustNotHave(t, "user data.js", f["data.js"], `"p":"kite.sync.copies_changed"`)
	mustHave(t, "explore data.js", f["explore/data.js"], `"p":"kite.sync.copies_changed"`)
}

// Without lenses the site is explore alone at the root, with no switcher.
func TestLensesUnset(t *testing.T) {
	ls, f := lensFiles(t, entries["kinds"])
	if len(ls.Sites()) != 1 || !ls.Sites()[0].explore() {
		t.Fatalf("%d sites", len(ls.Sites()))
	}
	mustNotHave(t, "home", f["index.html"], "lens-switch")
	for p := range f {
		if strings.Contains(p, "/") && !strings.HasPrefix(p, "_assets/") {
			t.Errorf("a lens folder: %s", p)
		}
	}
}

// The switcher lists each lens's title in the docs block's order, the
// current one marked, each a plain link to the same page there, else to
// the nearest page it keeps, else its home.
func TestLensSwitcher(t *testing.T) {
	_, f := lensFiles(t, kite)
	sw := regexp.MustCompile(`<nav class="lens-switch".*?</nav>`)
	got := sw.FindString(f["kite.cli.sync.html"])
	mustHave(t, "user switcher", got, `href="kite.cli.sync.html" data-lens="user" aria-current="true">User</a>`,
		`href="dev/kite.cli.sync.html" data-lens="developer">Developer</a>`, `href="explore/kite.cli.sync.html" data-lens="explore">Explore</a>`)
	if strings.Index(got, ">User<") > strings.Index(got, ">Developer<") || strings.Index(got, ">Developer<") > strings.Index(got, ">Explore<") {
		t.Errorf("order: %s", got)
	}
	got = sw.FindString(f["explore/kite.system.html"])
	mustHave(t, "explore switcher", got, `href="../index.html" data-lens="user">User</a>`, `href="../dev/kite.system.html"`)
	mustHave(t, "dev glossary switcher", sw.FindString(f["dev/_glossary.html"]), `href="../_glossary.html"`)
}

// The user lens opens on its tasks: the root's prose marked for it, its
// first tutorial as the primary link and a group per group of its
// navigation, which lists the authored pages by kind, then the reference
// with the errors and the glossary, and a page holding only authored pages
// once; it shows commands, terms, examples and diagnostics as errors with
// their fixes, and leaves out rules and the tree's section prose, keeping
// a section's heading where it holds what it shows.
func TestUserLens(t *testing.T) {
	_, f := lensFiles(t, kite)
	home := f["index.html"]
	mustHave(t, "user home", home, "Kite syncs a folder of notes",
		`<a class="home-primary" href="kite.first_sync.html"><span class="home-primary-title">First sync</span><span class="home-primary-text">Build a notes folder from nothing and keep it in sync.</span>`,
		`<h2 class="home-group-title">How-to guides</h2>`, `href="kite.start.html"`, `href="kite.set_up_store.html"`,
		`<h2 class="home-group-title">Reference</h2>`, `href="kite.cli.html"`, `href="_diagnostics.html"><span class="home-link-title">Errors</span>`, `href="_glossary.html"`,
		`<span class="home-link-text">Keep the folder small: Kite copies changed notes only, so a large folder syncs as fast as a small one.</span>`)
	mustNotHave(t, "user home", home, "The spec of Kite 1.4", `<h2 class="home-group-title">Tutorials</h2>`, `class="tile"`)
	nav := regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`).FindString(home)
	mustNotHave(t, "user nav", nav, ">More<")
	order := []string{">Guides<", ">Start<", ">Tutorials<", ">First sync<", ">How-to guides<", ">Set up store<", ">Concepts<", ">Tips<", ">Conflicts<", ">Reference<", ">CLI<", ">Errors<", ">Glossary<"}
	rest := nav[strings.Index(nav, `class="nav-group"`):]
	for _, o := range order {
		i := strings.Index(rest, o)
		if i < 0 {
			t.Errorf("navigation order at %s: %s", o, nav)
			break
		}
		rest = rest[i:]
	}
	sync := f["kite.sync.html"]
	mustHave(t, "user sync", sync, "conflict")
	// Its errors are on its Errors page alone, each at its anchor there.
	mustNotHave(t, "user sync", sync, `id="kite.sync.unreadable"`, "diag-code", "copy every note", "sev-error", "Reported by", "How a sync decides what to copy", "Spec writers:")
	if _, ok := f["kite.system.html"]; ok {
		t.Error("the actors' section has a page in the user lens")
	}
	mustHave(t, "user command", f["kite.cli.sync.html"], "--dry-run", "Lists what a sync would copy")
	mustNotHave(t, "user command", f["kite.cli.sync.html"], "exit with status 1 when any note")
	// A term's heading is its display name, in sentence case; inside a
	// sentence it keeps its case.
	mustHave(t, "user sync", sync, `href="#kite.sync.conflict">Conflict</a>`)
	mustHave(t, "user glossary", f["_glossary.html"], `>Conflict</a>`, `>Wait</a>`)
	// The user lens's depth, 2, folds a third level onto its parent's page,
	// which the docs block's depth, 3, gives a page in the other lenses.
	if _, ok := f["kite.sync.retries.backoff.html"]; ok {
		t.Error("a third-level section has a page in the user lens")
	}
	mustHave(t, "user retries", f["kite.sync.retries.html"], `id="kite.sync.retries.backoff"`, "The time a sync waits")
	mustHave(t, "dev backoff", f["dev/kite.sync.retries.backoff.html"], "double the wait")
	// Every authored page has a page in every lens that keeps it.
	for _, p := range []string{"kite.start.html", "dev/kite.start.html", "explore/kite.start.html"} {
		mustHave(t, p, f[p], `class="authored"`)
	}
}

// A mark keeps a node or a prose run to the lenses it names, and explore
// shows it with their titles.
func TestLensMarksRender(t *testing.T) {
	_, f := lensFiles(t, kite)
	mustHave(t, "user guide", f["kite.start.html"], "When the sync finishes")
	mustNotHave(t, "dev guide", f["dev/kite.start.html"], "When the sync finishes")
	mustHave(t, "dev sync", f["dev/kite.sync.html"], "Spec writers:")
	mustHave(t, "explore sync", f["explore/kite.sync.html"], "Spec writers:", `<span class="lens-mark">Developer</span>`)
	mustHave(t, "explore guide", f["explore/kite.start.html"], `<span class="lens-mark">User</span>`)
	mustHave(t, "dev home", f["dev/index.html"], "The spec of Kite 1.4")
	mustNotHave(t, "dev home", f["dev/index.html"], "Kite syncs a folder")
}

// `@lens` on an element that declares no node marks its content as a
// mark marks a prose run: a prose file's item that is such an element is a
// marked run, and one inside a sentence shows only in its lenses; explore
// shows every one with its lenses beside it.
func TestElementLensMarks(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.heron", `import = ["std/doc@1", "std/docs@1"]

include = ["./guide.heron.md"]

docs {
  lenses = [user, developer, explore]
}
`)
	write("guide.heron.md", `# Refunds {#refunds}

Everyone reads this.

<p @lens="user">Open the payment and press **Refund**.</p>

<p @lens="developer, explore">A refund moves the payment to `+"`refunded`"+`.</p>

Inline <em @lens="user">user-only words</em> in a sentence.

<term id="window" @lens="developer">The days a refund can be asked for.</term>
`)
	_, f := lensFiles(t, filepath.Join(dir, "index.heron"))
	dev := f["dev/refunds.html"]
	mustHave(t, "dev", dev, "A refund moves the payment", "Inline", "in a sentence")
	mustNotHave(t, "dev", dev, "Open the payment", "user-only words")
	ex := f["explore/refunds.html"]
	mustHave(t, "explore", ex, "Open the payment", "A refund moves the payment", "user-only words",
		`<div class="marked"> <span class="lens-mark">User</span>`, `<span class="marked"> <span class="lens-mark">User</span>`)
	// A named element's `@lens` is its node's mark, not a mark on its text.
	if strings.Contains(ex, `<span class="lens-mark">Developer</span><p>The days`) {
		t.Error("a named element's mark marks its text too")
	}
}

// The developer lens lists sections as topics and sums each rule up as
// its sentence, which opens in place; a page reads as its prose, its
// rules, its terms, its sections and its diagnostics last, while explore
// keeps the tree's order. Its home lists each topic's pages.
func TestDeveloperLens(t *testing.T) {
	_, f := lensFiles(t, kite)
	sync := f["dev/kite.sync.html"]
	mustHave(t, "dev sync", sync, `<div class="rule rule-summary" id="kite.sync.no_deletes"><p class="sentence">`)
	main := sync[strings.Index(sync, `id="main"`):]
	order := []string{`id="kite.sync.copies_changed"`, `id="kite.sync.no_deletes"`, `id="kite.sync.conflict"`, `href="kite.sync.retries.html"`, `id="kite.sync.unreadable"`}
	for i := 1; i < len(order); i++ {
		if a, b := strings.Index(main, order[i-1]), strings.Index(main, order[i]); a < 0 || b < 0 || a > b {
			t.Errorf("%s after %s", order[i-1], order[i])
		}
	}
	ex := f["explore/kite.sync.html"]
	if strings.Index(ex, `id="kite.sync.conflict"`) > strings.Index(ex, `id="kite.sync.copies_changed"`) {
		t.Error("explore doesn't keep the tree's order")
	}
	mustHave(t, "dev home", f["dev/index.html"], `<h2 class="home-group-title"><a href="kite.cli.html">CLI</a></h2>`, `<a href="kite.cli.sync.html"><span class="home-link-title">kite sync</span><span class="home-link-text">Syncs a folder with its store, once.</span>`)
	// A section whose only prose and pages are for users has no page here.
	if _, ok := f["dev/kite.more.html"]; ok {
		t.Error("an empty section has a page in the developer lens")
	}
	nav := regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`).FindString(sync)
	mustHave(t, "dev nav", nav, `<a class="label" href="kite.cli.html">CLI</a>`)
	mustNotHave(t, "dev nav", nav, "copies_changed", "Unreadable")
}

// An area's goals come first, before its rules and its errors, on its
// page and among its pages in the navigation, whatever order the tree
// writes them in; explore keeps the tree's order.
func TestIntentFirst(t *testing.T) {
	_, f := lensFiles(t, kite)
	sync := f["dev/kite.sync.html"]
	main := sync[strings.Index(sync, `id="main"`):]
	order := []string{`<div class="goal-block" id="kite.sync.same_everywhere">`, `id="kite.sync.copies_changed"`, `id="kite.sync.unreadable"`}
	for i := 1; i < len(order); i++ {
		if a, b := strings.Index(main, order[i-1]), strings.Index(main, order[i]); a < 0 || b < 0 || a > b {
			t.Errorf("%s after %s", order[i-1], order[i])
		}
	}
	mustHave(t, "goal block", main, `<span class="label">Goal</span>`)
	// A goal has no page and no place in the navigation.
	if _, ok := f["dev/kite.sync.same_everywhere.html"]; ok {
		t.Error("a goal has a page")
	}
	nav := regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`).FindString(sync)
	mustNotHave(t, "nav", nav, "same_everywhere")
	ex := f["explore/kite.sync.html"]
	ex = ex[strings.Index(ex, `id="main"`):]
	if strings.Index(ex, `kite.sync.same_everywhere`) < strings.Index(ex, `id="kite.sync.copies_changed"`) {
		t.Error("explore doesn't keep the tree's order")
	}
}

// Explore shows every node in full: each rule with a facts line of its
// path, kind, targets, subject and strength, each node's kind beside its
// name; the navigation lists pages alone, and the page's nodes are in its
// outline.
func TestExploreLens(t *testing.T) {
	_, f := lensFiles(t, kite)
	sync := f["explore/kite.sync.html"]
	mustHave(t, "explore sync", sync, `<span class="rule-meta"><code class="meta-name">sync.copies_changed</code>`,
		`<code class="meta-kind">rule</code>`, `<code class="meta-strength">must</code>`, `<span class="kind">section</span>`,
		`<span class="kind">term</span>`, `id="kite.sync.unreadable"`)
	mustNotHave(t, "explore sync", sync, `<details class="rule`)
	outline := regexp.MustCompile(`(?s)<nav class="outline".*?</nav>`).FindString(sync)
	mustHave(t, "explore sync outline", outline, `<li class="l2"><a href="#kite.sync.keeps_both">`)
}

// In every lens the navigation lists pages, never an anchor on a page;
// a page's outline holds its headings and, under a node's heading, the
// nodes inside it, two levels deep, or on a page without headings its
// nodes and theirs.
func TestNavPagesOutlineNested(t *testing.T) {
	_, f := lensFiles(t, kite)
	nav := regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`)
	outline := regexp.MustCompile(`(?s)<nav class="outline".*?</nav>`)
	for path, h := range f {
		if strings.HasSuffix(path, ".html") && strings.Contains(nav.FindString(h), `href="#`) {
			t.Errorf("%s: the navigation lists an anchor", path)
		}
	}
	mustHave(t, "user retries outline", outline.FindString(f["kite.sync.retries.html"]),
		`<li class="l2"><a href="#kite.sync.retries.backoff">Backoff</a></li><li class="l3"><a href="#kite.sync.retries.backoff.wait">Wait</a></li>`)
	mustHave(t, "explore backoff outline", outline.FindString(f["explore/kite.sync.retries.backoff.html"]),
		`<li class="l2"><a href="#kite.sync.retries.backoff.backoff_doubles">`, `<li class="l2"><a href="#kite.sync.retries.backoff.wait">`)
}

// A link to a node its lens leaves out opens it in the first lens that
// renders it, with that lens's title after the link text.
func TestLinksAcrossLenses(t *testing.T) {
	_, f := lensFiles(t, kite)
	mustHave(t, "user concept", f["kite.conflicts.html"], `href="dev/kite.sync.html#kite.sync.keeps_both">the conflict rule`, `<span class="lens-tag">Developer</span>`)
	mustNotHave(t, "dev concept", f["dev/kite.conflicts.html"], "lens-tag")
	// A link the generator builds, as a rule's subject, carries no title.
	mustHave(t, "dev sync", f["dev/kite.sync.html"], `<a class="ref subject" href="kite.system.html#kite.system.syncer">syncer</a>`)
}

// A rule reads as one paragraph: conditions, subject, strength, behaviour,
// unless, then after ", so", instead after ", and must instead", and its
// examples as sentences after it; no label column, and a chip only for a
// target the rule's place doesn't name.
func TestRuleAsProse(t *testing.T) {
	_, f := lensFiles(t, kite)
	sync := f["explore/kite.sync.html"]
	mustHave(t, "rules", sync,
		`While the two copies differ, when a note changed in the folder and in the store, the <a class="ref subject"`,
		`<strong class="strength">must</strong> keep both copies, the store&#39;s under a <code>.conflict</code> suffix.`,
		`copy every note whose content changed since the last sync, so a note that only moved`,
		`<strong class="strength">must not</strong> delete a note from the store, and must instead move it`,
		`<span class="rule-example">For example, removing <code>ideas.md</code> locally moves it to <code>trash/ideas.md</code>.</span>`)
	mustNotHave(t, "rules", sync, `class="rule-more"`, `class="rule-head"`, `<dt>`)
	dev := f["dev/kite.sync.html"]
	// A clause that holds the connective it would be joined with is the
	// next sentence.
	mustHave(t, "dev rules", dev, `for every note it copies. The log grows with each sync, so it&#39;s rotated once it reaches 1 MB.`)
	mustNotHave(t, "dev rules", dev, `copies, so the log grows`)
	// keeps_both targets the term conflict, on its own page: no chip, as
	// none for the rule's own section; a target elsewhere leads the
	// sentence as a tag.
	for _, id := range []string{"kite.sync.copies_changed", "kite.sync.keeps_both"} {
		if c := regexp.MustCompile(`id="` + regexp.QuoteMeta(id) + `".*?</(details|div)>`).FindString(dev); strings.Contains(c, "chip") {
			t.Errorf("a chip for a target on the rule's page: %s", c)
		}
	}
	// A rule the op realizes is listed on the op's page.
	mustHave(t, "dev op", f["dev/kite.store_api.get_note.html"], `When the store answers 404`)
	// The formal parts are in the markup for the hover card.
	mustHave(t, "dev rules", dev, `class="rule-meta"`)
}

// A how-to's steps are one numbered list, each step a heading one level
// below the page with an anchor at its path; an example's code block is
// titled with its name; an inserted fact renders its value, a string
// without quotes.
func TestAuthoredPagesRender(t *testing.T) {
	_, f := lensFiles(t, kite)
	h := f["kite.set_up_store.html"]
	mustHave(t, "how-to", h, `<ol class="steps"><li class="step" id="kite.set_up_store.create"><h2 class="h">`, `id="kite.set_up_store.check"`, `<span>Store line</span>`)
	if strings.Count(h, `<ol class="steps">`) != 1 {
		t.Error("steps split into several lists")
	}
	g := f["kite.start.html"]
	mustHave(t, "guide", g, `, <code class="value">kite sync</code>, does the work`, `defaults to <code class="value"><span class="tk-kw">false</span></code>`, `<span>First config</span>`)
}

// Data of fewer than three rows renders as lines, not a table.
func TestSmallTablesAsLines(t *testing.T) {
	_, f := lensFiles(t, kite)
	h := f["explore/kite.cli.sync.html"]
	mustHave(t, "command", h, `<ul class="lines"><li id="kite.cli.sync.dry_run"><p class="line-head"><code>--dry-run</code>`, `</span>default <code>`)
	mustNotHave(t, "command", h, `<table class="opts">`)
}

// The diagnostics page lists its diagnostics by code: a code with one as
// its code and message on one line with its fix under it, a code with two
// or more as the code once with each message and fix under it. A
// placeholder inside literal angle brackets reads without its own marks.
// In the user lens each entry is the diagnostic's anchor.
func TestDiagnosticsCompact(t *testing.T) {
	_, f := lensFiles(t, kite)
	h := f["explore/_diagnostics.html"]
	mustHave(t, "diagnostics", h, `<li id="code-store-unreachable"><div><p class="diag-line"><code class="diag-code">store-unreachable</code> <a class="ref diag-message"`,
		`<p class="diag-fix">The store didn&#39;t answer within the timeout; Kite retries at the next interval.</p>`,
		`<li id="code-unreadable"><p class="diag-line"><code class="diag-code">unreadable</code></p><ul class="diag-group"><li><p class="diag-line"><a class="ref diag-message"`,
		`can&#39;t list &lt;<span class="ph">folder</span>&gt;`, `can&#39;t read <span class="ph">‹file›</span>`)
	mustNotHave(t, "diagnostics", h, "<table", `&lt;<span class="ph">‹folder›`)
	if strings.Count(h, `<code class="diag-code">unreadable</code>`) != 1 {
		t.Error("a code listed more than once")
	}
	u := f["_diagnostics.html"]
	mustHave(t, "user errors", u, `<h1 class="page-title">Errors</h1>`, `<li id="kite.sync.unreadable"><p class="diag-line"><span class="diag-message">can&#39;t read`)
	mustHave(t, "user search", f["data.js"], `kite.sync.unreadable`)
}

// A lens a tree declares is built under its prefix and listed by its
// title, and a tree's own user lens replaces std/docs's.
func TestOwnLenses(t *testing.T) {
	dir := t.TempDir()
	src := `import = ["std/doc@1", "std/spec@1", "std/docs@1"]

cli: section {
  sync: command("kite sync") {{ Syncs a folder. }}
}

start: guide {
  | Run the sync.
}

support: lens {
  title = "Support"
  nav = LensNav.TASK
  show = [spec.command, spec.error]
  prefix = "support"
}

user: lens {
  title = "User"
  default = true
  nav = LensNav.TASK
  show = [spec.command]
  prefix = ""
}

docs {
  lenses = [user, support, explore]
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, f := lensFiles(t, filepath.Join(dir, "index.heron"))
	mustHave(t, "support", f["support/cli.sync.html"], "Syncs a folder")
	mustHave(t, "switcher", f["cli.sync.html"], `data-lens="support">Support</a>`)
	// The tree's user shows commands only: the guide has no page there.
	if _, ok := f["start.html"]; ok {
		t.Error("the guide has a page in the tree's own user lens")
	}
	if _, ok := f["explore/start.html"]; !ok {
		t.Error("no guide page in explore")
	}
}

// Prose inside Heron code reads as prose: a `|` line's marker is
// punctuation and its text, like a `{{ }}` body's, is prose at the text
// colour; only comments take the faint comment colour.
func TestProseInCode(t *testing.T) {
	ls, _ := lensFiles(t, kite)
	h := ls.Default().highlightSource("// a comment\nrefunds: section {\n  | How a refund is made.\n  window: term {{ The 30 days after capture. }}\n}\n")
	mustHave(t, "code", h, `<span class="tk-comment">// a comment</span>`, `<span class="tk-punct">|</span>`, `<span class="tk-prose">`)
	mustNotHave(t, "code", h, `<span class="tk-comment">|</span>`)
	css, err := assets.ReadFile("assets/docs.css")
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, "docs.css", string(css), `.tk-prose{color:var(--h-text)}`)
	mustNotHave(t, "docs.css", string(css), `.tk-prose{color:var(--h-syntax-comment)}`, `.tk-comment,.tk-prose`)
}

// A short link reads as its node does inside a sentence, a term by its
// word, and in backticks as its path in code, each linking to the node.
func TestShortLinksRender(t *testing.T) {
	_, f := lensFiles(t, kite)
	h := f["kite.more.tips.html"]
	mustHave(t, "tips", h, `A <a class="ref ref-term" href="kite.sync.html#kite.sync.conflict" data-card="kite.sync.conflict">conflict</a> needs you`,
		`<a class="ref" href="kite.cli.sync.html#kite.cli.sync.dry_run"`, `><code>dry_run</code></a> previews`, `>store line</a> step`)
}

// Kind elements: a named element is a node with its page or anchor, its
// content its body, an element on lines of its own in it a member, and its
// @name attributes its annotations; an element of a kind a Markdown form
// builds renders as the form does, and `<ref>` as a short link does.
func TestElementsRender(t *testing.T) {
	_, f := lensFiles(t, "testdata/elements/index.heron")
	// A prose file's text is its section's prose, its Markdown read as
	// Markdown, a list item continued on an indented line included.
	mustHave(t, "asking", f["explore/asking.html"], `<h1 class="page-title">Asking <span class="kind">section</span></h1>`, `<li>open the payment and read its state;</li>`)
	h := f["asking.do_it.html"]
	mustHave(t, "how-to", h, `<h1 class="page-title">Ask for a refund</h1>`, `<li class="step" id="asking.do_it.look"><h2 class="h">`,
		`id="asking.do_it.call"`, `<aside class="callout callout-warning"><div class="prose"><p>A refund can&#39;t be undone.</p></div></aside></li>`)
	if _, ok := f["dev/asking.do_it.html"]; ok {
		t.Error("@lens=\"user\" doesn't keep the how-to to the user lens")
	}
	ex := f["explore/refunds.html"]
	mustHave(t, "refunds", ex, `with <em>stress</em> and <em>stress</em>, <code>code</code> and <code>code</code>.`,
		`See <a class="ref ref-term" href="refunds.html#refunds.window" data-card="refunds.window">window</a>, <a class="ref ref-term" href="refunds.html#refunds.window" data-card="refunds.window">window</a>,`,
		`<a class="ref ref-term" href="refunds.html#refunds.window" data-card="refunds.window"><code>window</code></a> and <a class="ref ref-term" href="refunds.html#refunds.window" data-card="refunds.window"><code>window</code></a>.`,
		`Fish &amp; chips.`, `<div class="rule" id="refunds.late"><p class="sentence">The <a class="ref subject" href="people.html#people.desk">desk</a> <strong class="strength">must</strong> refuse a refund after the`)
	mustNotHave(t, "refunds", ex, "&amp;amp;", `tag-ref`)
}

// A use case with two extensions or more gets a flow diagram in the
// developer and explore lenses, never in the user lens.
func TestUseCaseFlowDiagram(t *testing.T) {
	_, f := lensFiles(t, kite)
	var dev, user string
	for path, h := range f {
		if strings.Contains(h, `id="kite.sync_notes"`) {
			switch {
			case strings.HasPrefix(path, "dev/"):
				dev = h
			case !strings.HasPrefix(path, "explore/"):
				user = h
			}
		}
	}
	mustHave(t, "dev use case", dev, `<figure class="uc-flow">`, `<span class="uc-num">2b</span>`)
	// The outline names the main path and each extension, never bare step
	// numbers.
	ol := regexp.MustCompile(`(?s)<nav class="outline".*?</nav>`).FindString(dev)
	mustHave(t, "use case outline", ol, `>Main path<`, `>Extensions<`, `2b `)
	if regexp.MustCompile(`>\d+<`).MatchString(ol) {
		t.Errorf("bare step numbers in the outline:\n%s", ol)
	}
	if strings.Contains(user, `uc-flow`) {
		t.Error("a flow diagram in the user lens")
	}
}
