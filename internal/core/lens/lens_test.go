package lens_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/resolve"
)

func checkSrc(t *testing.T, src string) *check.Result {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "index.heron")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := resolve.Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	return check.NewSession().Check(id)
}

func messages(res *check.Result) string {
	var out []string
	for _, d := range res.Diags {
		out = append(out, d.Code+": "+d.Message)
	}
	return strings.Join(out, "\n")
}

const tree = `import = ["std/doc@1", "std/spec@1", "std/docs@1"]

intro: section {
  | Everyone reads this.

  @lens(user)
  | Only users read this.

  @targets(intro)
  r: rule {
    must {{ do things }}
  }

  @lens(developer)
  devnote: section {
    | For spec writers.
  }

  start: guide {
    | Read [the rule](#intro.r).

    inner: section {
      | A part of the guide.
    }

    ok: example {
      | ` + "```heron" + `
      | import = ["std/doc@1"]
      | a: section {}
      | ` + "```" + `
    }
  }
}

@lens(user, developer)
both: section {
  kept: section {
    | For users and developers.
  }

  @lens(developer)
  narrowed: section {
    | For developers only.
  }
}

agent: lens {
  title = "Agent"
  nav = LensNav.TREE
  prefix = "agent"
  hide = [doc.section]
  show = [guide]
}

docs {
  LENSES
}
`

func withLenses(s string) string { return strings.Replace(tree, "LENSES", s, 1) }

// The standard lenses, a lens of the tree's own and @lens on a node and
// on a prose block check clean, and the docs block's choice reads back in
// order with the default at the root.
func TestLensesCheck(t *testing.T) {
	res := checkSrc(t, withLenses("lenses = [user, developer, agent, explore]"))
	if res.Diags.HasErrors() {
		t.Fatal(messages(res))
	}
	s := lens.SiteOf(res.Tree, res.Checker)
	var got []string
	for _, l := range s.Lenses {
		got = append(got, l.Name+":"+l.Prefix+":"+string(l.Nav))
	}
	if want := "user::TASK developer:dev:TOPIC agent:agent:TREE explore:explore:TREE"; strings.Join(got, " ") != want {
		t.Fatalf("lenses = %q, want %q", got, want)
	}
	if s.Default == nil || s.Default.Name != "user" {
		t.Fatalf("default = %v", s.Default)
	}
}

// Without a choice the site is explore alone, at the root.
func TestDefaultSiteIsExplore(t *testing.T) {
	res := checkSrc(t, withLenses("depth = 2"))
	if res.Diags.HasErrors() {
		t.Fatal(messages(res))
	}
	s := lens.SiteOf(res.Tree, res.Checker)
	if len(s.Lenses) != 1 || !s.Lenses[0].Explore() || s.Lenses[0].Prefix != "" {
		t.Fatalf("lenses = %+v", s.Lenses)
	}
}

// An unknown lens name is an error, on a node and on a prose block, and so
// is a name that resolves to something other than a lens.
func TestUnknownLens(t *testing.T) {
	for _, c := range []struct{ from, to, want string }{
		{"@lens(user)\n  |", "@lens(usr)\n  |", "unknown lens usr; the lenses in scope are agent, developer, explore, user"},
		{"@lens(developer)\n  devnote", "@lens(developr)\n  devnote", "unknown lens developr; the lenses in scope are"},
		{"@lens(developer)\n  devnote", "@lens(intro)\n  devnote", "intro is a section, not a lens"},
		{"lenses = [user, explore]", "lenses = [user, intro]", "intro is a section, not a lens"},
		{"lenses = [user, explore]", "lenses = [user, nobody]", "unknown lens nobody"},
		// `@lens` on an element that declares no node is a mark too.
		{"| Everyone reads this.", `| Everyone <em @lens="usr">reads</em> this.`, "unknown lens usr; the lenses in scope are"},
		{"| Everyone reads this.", `| Everyone <em @lens="intro">reads</em> this.`, "intro is a section, not a lens"},
	} {
		res := checkSrc(t, strings.Replace(withLenses("lenses = [user, explore]"), c.from, c.to, 1))
		if !strings.Contains(messages(res), c.want) {
			t.Errorf("%s: diagnostics %q, want %q", c.to, messages(res), c.want)
		}
		if strings.Contains(messages(res), "unknown name") {
			t.Errorf("%s: the generic error stays: %q", c.to, messages(res))
		}
	}
}

// A site has exactly one default lens, and every other lens a prefix of
// its own.
func TestOneDefaultLens(t *testing.T) {
	for _, c := range []struct{ lenses, want string }{
		{"lenses = [developer, explore]", "a site has exactly one default lens; 0 of developer, explore set default = true"},
		{"lenses = [user, other]\n  other: lens {\n    title = \"Other\"\n    default = true\n    nav = LensNav.TOPIC\n    prefix = \"o\"\n  }", "a site has exactly one default lens; 2 of user, other set default = true"},
		{"lenses = [user, developer, again]\n  again: lens {\n    title = \"Again\"\n    nav = LensNav.TOPIC\n    prefix = \"dev\"\n  }", "developer and again are both served under dev/"},
		{"lenses = [user, root]\n  root: lens {\n    title = \"Root\"\n    nav = LensNav.TOPIC\n    prefix = \"\"\n  }", "root: only the default lens is served at the site's root"},
		{"lenses = [user, slash]\n  slash: lens {\n    title = \"Slash\"\n    nav = LensNav.TOPIC\n    prefix = \"/dev/\"\n  }", `slash: the prefix "/dev/" isn't one path segment`},
		{"lenses = []", "the site builds no lens"},
		{"lenses = [user]\n  odd: lens {\n    title = \"Odd\"\n    nav = LensNav.TOPIC\n    prefix = \"odd\"\n    show = [intro]\n    entry = [intro]\n  }", "intro is a section; a lens's show lists kinds and categories"},
		{"lenses = [user]\n  odd: lens {\n    title = \"Odd\"\n    nav = LensNav.TOPIC\n    prefix = \"odd\"\n    entry = [intro]\n  }", "intro is a section; a lens's entry lists guides, how-tos, concepts and tutorials"},
	} {
		res := checkSrc(t, withLenses(c.lenses))
		if !strings.Contains(messages(res), c.want) {
			t.Errorf("%s: diagnostics %q, want %q", c.lenses, messages(res), c.want)
		}
	}
}

// Each lens shows, sums up or hides a node by its kind or category and by
// @lens; explore shows everything.
func TestModes(t *testing.T) {
	res := checkSrc(t, withLenses("lenses = [user, developer, agent, explore]"))
	if res.Diags.HasErrors() {
		t.Fatal(messages(res))
	}
	s := lens.SiteOf(res.Tree, res.Checker)
	byName := map[string]*lens.Lens{}
	for _, l := range s.Lenses {
		byName[l.Name] = l
	}
	intro := res.Tree.Root.Child("intro")
	start := intro.Child("start")
	both := res.Tree.Root.Child("both")
	nodes := map[string]*resolve.Node{"rule": intro.Child("r"), "devnote": intro.Child("devnote"), "guide": start, "intro": intro,
		"example": start.Child("ok"), "inner": start.Child("inner"), "narrowed": both.Child("narrowed"), "kept": both.Child("kept")}
	modes := map[lens.Mode]string{lens.Show: "show", lens.Summarize: "summarize", lens.Hide: "hide"}
	for _, c := range []struct{ lens, node, want string }{
		{"user", "rule", "hide"},
		{"developer", "rule", "summarize"},
		{"explore", "rule", "show"},
		// Marked for the developer lens: out of user, in full in developer.
		{"user", "devnote", "hide"},
		{"developer", "devnote", "show"},
		{"explore", "devnote", "show"},
		// agent hides sections by kind and shows guides.
		{"agent", "intro", "hide"},
		{"agent", "guide", "show"},
		// user shows authored pages; a section is unlisted, so it takes its
		// parent's treatment, and a root child is out when show is set.
		{"user", "guide", "show"},
		{"user", "intro", "hide"},
		{"developer", "intro", "show"},
		// An example is listed; a section inside a guide follows the guide.
		{"user", "example", "show"},
		{"user", "inner", "show"},
		// An inner mark narrows: user and developer above, developer below.
		{"user", "narrowed", "hide"},
		{"developer", "narrowed", "show"},
		{"user", "kept", "show"},
	} {
		if got := modes[byName[c.lens].Mode(res.Checker, nodes[c.node])]; got != c.want {
			t.Errorf("%s in %s: %s, want %s", c.node, c.lens, got, c.want)
		}
	}
	if !lens.IsAuthored(res.Checker, nodes["guide"]) || lens.IsAuthored(res.Checker, nodes["intro"]) {
		t.Error("IsAuthored")
	}
	// The prose block keeps to the user lens.
	r := lens.ProseRestriction(res.Checker, intro.Prose[1])
	if len(r) != 1 || r[0].Name != "user" || byName["developer"].Allows(r) || !byName["user"].Allows(r) || !byName["explore"].Allows(r) {
		t.Errorf("prose restriction = %v", r)
	}
}

// An authored page's Heron example is checked as a tree of its own, where
// only the standard packages resolve.
func TestAuthoredExamplesCheck(t *testing.T) {
	for _, c := range []struct{ code, want string }{
		{"a: section {", "the example intro.start.ok doesn't check: expected `}` (line 3)"},
		{"a: rule {}", "the example intro.start.ok doesn't check: unknown kind or type rule"},
	} {
		res := checkSrc(t, strings.Replace(withLenses("lenses = [user, explore]"), "a: section {}", c.code, 1))
		if !strings.Contains(messages(res), c.want) {
			t.Errorf("%s: diagnostics %q", c.code, messages(res))
		}
	}
}

// std/docs's step is the kind of a how-to's child, with the packages
// imported in either order.
func TestStepResolvesByParent(t *testing.T) {
	for _, imports := range []string{
		`["std/doc@1", "std/spec@1", "std/docs@1"]`,
		`["std/docs@1", "std/doc@1", "std/spec@1"]`,
	} {
		src := "import = " + imports + `
shopper: actor {{ Someone who buys. }}

pay: howto {
  | Pay for an order.
  open: step {{ Open the cart. }}
}

buy: usecase(actor = shopper, success = "An order paid for.") {{
  1. [shopper] opens the cart.
}}
`
		res := checkSrc(t, src)
		if res.Diags.HasErrors() {
			t.Fatalf("%s: %s", imports, messages(res))
		}
		k1 := res.Checker.KindOf(res.Tree.Root.Child("pay").Child("open"))
		if k1 == nil || k1.Path() != "docs.step" {
			t.Errorf("%s: how-to step is %v", imports, k1)
		}
		if st := res.Tree.Root.Child("buy").Child("1"); st == nil || st.Shape != resolve.ShapeStep {
			t.Errorf("%s: use-case step is %v", imports, st)
		}
	}
}

// A lens's fields are checked against std/docs's declaration, and a tree's
// own lens of a standard name is the one its docs block and marks use.
func TestLensFieldsAndOverride(t *testing.T) {
	res := checkSrc(t, strings.Replace(withLenses("lenses = [user, explore]"), "nav = LensNav.TREE", "nav = LensNav.PAGES", 1))
	if !strings.Contains(messages(res), "LensNav has no member PAGES") {
		t.Errorf("diagnostics %q", messages(res))
	}
	src := withLenses("lenses = [user, explore]") + `
user: lens {
  title = "Customers"
  default = true
  nav = LensNav.TOPIC
  prefix = ""
}
`
	res = checkSrc(t, src)
	if res.Diags.HasErrors() {
		t.Fatal(messages(res))
	}
	s := lens.SiteOf(res.Tree, res.Checker)
	if s.Default == nil || s.Default.Title != "Customers" || s.Default.Node.Decl.File.ID.IsStd() {
		t.Fatalf("default = %+v", s.Default)
	}
	// The mark on the prose run names the tree's own user lens.
	r := lens.ProseRestriction(res.Checker, res.Tree.Root.Child("intro").Prose[1])
	if len(r) != 1 || r[0] != s.Default.Node {
		t.Errorf("mark resolves to %v", r)
	}
}

// Without an override, user, developer and explore are std/docs's.
func TestStandardLensesInStd(t *testing.T) {
	res := checkSrc(t, withLenses("lenses = [user, developer, explore]"))
	for _, l := range lens.SiteOf(res.Tree, res.Checker).Lenses {
		if !l.Node.Decl.File.ID.IsStd() || l.Node.Parent == nil || l.Node.Parent.Name != "docs" {
			t.Errorf("%s is declared at %s", l.Name, l.Node.Decl.File.ID)
		}
	}
	// @lens is std/doc's annotation.
	intro := res.Tree.Root.Child("intro")
	if r := lens.Restriction(res.Checker, intro.Child("devnote")); len(r) != 1 || r[0].Name != "developer" {
		t.Errorf("restriction %v", r)
	}
}

// Two marked runs in a row are two runs, each with its own mark.
func TestMarkSplitsRun(t *testing.T) {
	src := `import = ["std/doc@1", "std/docs@1"]

refunds: section {
  @lens(user)
  | Open the payment and press Refund.

  @lens(developer, explore)
  | A refund moves the payment to refunded.
  | The ledger records it.
}
`
	res := checkSrc(t, src)
	if res.Diags.HasErrors() {
		t.Fatal(messages(res))
	}
	n := res.Tree.Root.Child("refunds")
	if len(n.Prose) != 2 {
		t.Fatalf("%d runs", len(n.Prose))
	}
	a, b := lens.ProseRestriction(res.Checker, n.Prose[0]), lens.ProseRestriction(res.Checker, n.Prose[1])
	if len(a) != 1 || a[0].Name != "user" || len(b) != 2 || b[0].Name != "developer" {
		t.Errorf("marks %v, %v", a, b)
	}
}

// std/docs's step stands only in a how-to or a tutorial.
func TestStepOnlyInTasks(t *testing.T) {
	res := checkSrc(t, `import = ["std/doc@1", "std/docs@1"]

s: section {
  one: step {{ Do it. }}
}
`)
	if !strings.Contains(messages(res), "section doesn't accept step") {
		t.Errorf("diagnostics %q", messages(res))
	}
}

// Only heron code blocks of examples on authored pages are checked; an
// insertion of a fact that's gone and a link that resolves nowhere are
// errors.
func TestAuthoredPageChecks(t *testing.T) {
	base := `import = ["std/doc@1", "std/spec@1", "std/docs@1"]

cli: section {
  sync: command("kite sync") {
    dry_run: option("--dry-run", type = Bool, default = false)
  }
}

page: guide {
  | Run ${cli.sync.name}; --dry-run is ${cli.sync.dry_run.default} unless set. See [sync](#cli.sync).

  | ` + "```heron" + `
  | not: checked {
  | ` + "```" + `

  shell: example {
    | ` + "```sh" + `
    | kite sync {
    | ` + "```" + `
  }
}
`
	res := checkSrc(t, base)
	if res.Diags.HasErrors() {
		t.Fatal(messages(res))
	}
	for _, c := range []struct{ from, to, want string }{
		{"${cli.sync.dry_run.default}", "${cli.sync.dry_run.defualt}", "unknown name cli.sync.dry_run.defualt"},
		{"${cli.sync.name}", "${cli.snyc.name}", "unknown name"},
		{"(#cli.sync)", "(#cli.snyc)", "the link to #cli.snyc doesn't resolve"},
	} {
		res := checkSrc(t, strings.Replace(base, c.from, c.to, 1))
		if !strings.Contains(messages(res), c.want) {
			t.Errorf("%s: diagnostics %q, want %q", c.to, messages(res), c.want)
		}
	}
}

const mdTree = `import = ["std/doc@1", "std/spec@1", "std/docs@1"]

@lens(user)
for_users: section {
  | Only for users.

  pick: rule {
    must {{ pick }}
  }
}

LENSES

docs {
  SITE
}
`

func mdSrc(lenses, site string) string {
	return strings.Replace(strings.Replace(mdTree, "LENSES", lenses, 1), "SITE", site, 1)
}

// std/docs's agent lens is written as Markdown, reads no lens marks and
// has the default budget; a docs block can't list a Markdown lens, no lens
// of the site may take the agent lens's prefix, and a budget is at least
// 500 tokens.
func TestMarkdownLens(t *testing.T) {
	res := checkSrc(t, mdSrc("", "lenses = [user, developer, explore]"))
	if res.Diags.HasErrors() {
		t.Fatal(messages(res))
	}
	a := lens.Agent(res.Tree, res.Checker)
	if a.Name != "agent" || a.Format != lens.Markdown || !a.NoMarks || a.Budget != lens.DefaultBudget || a.Prefix != "agents" {
		t.Fatalf("agent = %+v", a)
	}
	rule := res.Tree.Root.Child("for_users").Child("pick")
	if a.Mode(res.Checker, rule) != lens.Show {
		t.Error("the agent lens leaves out a node @lens(user) marks")
	}
	for _, l := range lens.SiteOf(res.Tree, res.Checker).Lenses {
		if l.Name == "developer" && l.Mode(res.Checker, rule) != lens.Hide {
			t.Error("developer shows a node @lens(user) marks")
		}
	}
	for _, c := range []struct{ lenses, site, want string }{
		{"notes: lens {\n  title = \"Notes\"\n  nav = LensNav.TREE\n  format = LensFormat.MARKDOWN\n  prefix = \"notes\"\n}", "lenses = [user, notes]", "notes is written as Markdown; a site's lenses are written as HTML"},
		{"taken: lens {\n  title = \"Taken\"\n  nav = LensNav.TOPIC\n  prefix = \"agents\"\n}", "lenses = [user, taken]", "agent and taken are both served under agents/"},
		{"small: lens {\n  title = \"Small\"\n  nav = LensNav.TREE\n  format = LensFormat.MARKDOWN\n  budget = 100\n  prefix = \"small\"\n}", "lenses = [user]", "small: a budget is at least 500 tokens, not 100"},
		{"odd: lens {\n  title = \"Odd\"\n  nav = LensNav.TREE\n  format = LensFormat.PDF\n  prefix = \"odd\"\n}", "lenses = [user]", "PDF"},
	} {
		res := checkSrc(t, mdSrc(c.lenses, c.site))
		if !strings.Contains(messages(res), c.want) {
			t.Errorf("%s: diagnostics %q, want %q", c.lenses, messages(res), c.want)
		}
	}
	// A tree's own agent lens is the one the export is written through.
	res = checkSrc(t, mdSrc("agent: lens {\n  title = \"Mine\"\n  nav = LensNav.TREE\n  format = LensFormat.MARKDOWN\n  budget = 900\n  prefix = \"ai\"\n}", "lenses = [user]"))
	if res.Diags.HasErrors() {
		t.Fatal(messages(res))
	}
	if a := lens.Agent(res.Tree, res.Checker); a.Title != "Mine" || a.Budget != 900 || a.Prefix != "ai" || a.NoMarks {
		t.Errorf("own agent = %+v", a)
	}
}
