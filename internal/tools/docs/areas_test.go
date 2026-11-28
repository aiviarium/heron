package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const suiteEntry = "../../../examples/suite/index.heron"

var (
	tabsRe    = regexp.MustCompile(`(?s)<nav class="tabs".*?</nav>`)
	sidebarRe = regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`)
	switchRe  = regexp.MustCompile(`(?s)<details class="area-switch">.*?</details>`)
	treeRe    = regexp.MustCompile(`(?s)<div class="sidebar-in">.*?</nav>`)
	narrowRe  = regexp.MustCompile(`(?s)<div class="nav-areas">.*?</div>`)
)

// navTree is a page's navigation tree, without the tabs it shows on narrow
// screens.
func navTree(h string) string { return narrowRe.ReplaceAllString(treeRe.FindString(h), "") }

// The docs block's areas: a `<menu>` area is an entry of the switcher at
// the top of the sidebar, and the switcher's areas share one tab, titled
// as the one the page is in; a `<tab>` area, the default, is a tab of its
// own. The home page is the first area's, and each area's navigation
// holds its node's page first and the pages below it.
func TestSiteAreas(t *testing.T) {
	_, f := lensFiles(t, suiteEntry)
	notes := f["notes.syncing.html"]
	mustHave(t, "notes tabs", tabsRe.FindString(notes), `<a href="notes.html" aria-current="true">Notes</a>`, `<a href="help.html">Help</a>`)
	mustNotHave(t, "notes tabs", tabsRe.FindString(notes), ">Calendar<", ">Docs<", ">Glossary<")
	sw := switchRe.FindString(notes)
	mustHave(t, "notes switcher", sw, `<span class="t">Notes</span>`,
		`<a href="notes.html" aria-current="true"><span class="area-switch-title">`, `Notes</span><span class="area-switch-text">Notes keeps a folder of notes in sync across devices.</span>`,
		`<a href="calendar.html"><span class="area-switch-title">`)
	if strings.Index(sw, `href="notes.html"`) > strings.Index(sw, `href="calendar.html"`) {
		t.Error("the switcher doesn't keep the docs block's order")
	}
	nav := navTree(notes)
	mustHave(t, "notes nav", nav, `href="notes.html"`, `href="notes.start.html"`, `href="notes.syncing.html" aria-current="page"`)
	mustNotHave(t, "notes nav", nav, `href="calendar.`, `href="help.`)
	if strings.Contains(nav, `class="area-switch"`) || !strings.Contains(sidebarRe.FindString(notes), `<nav class="sidebar" aria-label="Navigation"><details class="area-switch">`) {
		t.Error("the switcher isn't at the top of the sidebar, before the navigation's tree")
	}
	// The switcher's tab is titled as the area the page is in.
	cal := f["calendar.events.html"]
	mustHave(t, "calendar tabs", tabsRe.FindString(cal), `<a href="calendar.html" aria-current="true">Calendar</a>`)
	mustHave(t, "calendar switcher", switchRe.FindString(cal), `<span class="t">Calendar</span>`)
	// A tab area has no switcher.
	help := f["help.accounts.html"]
	mustHave(t, "help tabs", tabsRe.FindString(help), `<a href="notes.html">Notes</a>`, `<a href="help.html" aria-current="true">Help</a>`)
	mustNotHave(t, "help", help, `class="area-switch"`)
	// The home page is the first area's.
	mustHave(t, "home tabs", tabsRe.FindString(f["index.html"]), `<a href="notes.html" aria-current="true">Notes</a>`)
	// The site's pages close every area's navigation.
	for _, p := range []string{"notes.html", "help.html", "contributor/calendar.html"} {
		mustHave(t, p+" site pages", sidebarRe.FindString(f[p]), `<div class="nav-group nav-site">`, `href="_glossary.html"`)
	}
}

// apiScopeTree has a folder that describes one API, its errors in a
// section of their own, beside a section a reader reads through.
const apiScopeTree = `import = ["std/doc@1", "std/types@1", "std/docs@1"]

include = ["./store_api/"]

guide: section {
  | How to use the store.

  @icon("compass")
  basics: section {
    | The basics, with an icon its author chose.
  }

  more: section {
    | More, with none.
  }
}

docs {
  lenses = [user, contributor, explore]
}
`

const apiScopeFolder = `| The store's API.

store: api {
  get(id: String): op(String) {{ Returns an item. }}
}

errors: section {
  gone: error(404) {
    title = "no item {id}"
    id: String
  }
}
`

// A folder that holds an api and no page but its errors' is listed in
// the Reference alone; the navigation shows an icon an entry's node sets,
// at any depth, and a site page's own, and never a kind's; and the
// gathered pages are never listed twice.
func TestAPIScopesAndNavIcons(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte(apiScopeTree), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "store_api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "store_api", "index.heron"), []byte(apiScopeFolder), 0o644); err != nil {
		t.Fatal(err)
	}
	_, f := lensFiles(t, filepath.Join(dir, "index.heron"))
	for _, p := range []string{"contributor/guide.html", "explore/guide.html"} {
		nav := navTree(f[p])
		mustNotHave(t, p+" nav", nav, `href="store_api.html"`, `href="store_api.errors.html"`)
		if !regexp.MustCompile(`<a href="guide.basics.html"[^>]*><svg`).MatchString(nav) {
			t.Errorf("%s: an entry below the top level doesn't show the icon its node sets:\n%s", p, nav)
		}
		if !regexp.MustCompile(`<a href="guide.more.html"[^>]*><span class="icon nav-blank" aria-hidden="true"></span><span class="t">`).MatchString(nav) {
			t.Errorf("%s: an entry without an icon beside one with an icon keeps no icon's space:\n%s", p, nav)
		}
		if !regexp.MustCompile(`<a href="guide.html"[^>]*><span class="t">`).MatchString(nav) {
			t.Errorf("%s: a section shows its kind's icon:\n%s", p, nav)
		}
		if !regexp.MustCompile(`<a href="_diagnostics.html"[^>]*><svg class="icon"`).MatchString(nav) {
			t.Errorf("%s: the Errors page shows no icon:\n%s", p, nav)
		}
		if n := strings.Count(nav, `href="_glossary.html"`) + strings.Count(nav, `href="_diagnostics.html"`); n > 1 {
			t.Errorf("%s: a gathered page is listed twice", p)
		}
	}
	scope := f["explore/store_api.html"]
	mustHave(t, "api scope tabs", tabsRe.FindString(scope), `aria-current="true">Reference</a>`)
	mustHave(t, "reference nav", sidebarRe.FindString(scope), `href="store_api.store.html"`)
}

// gapsTree has an intent gap, a goal nothing serves, and a feature with
// two rules about a part, one an error carries out.
const gapsTree = `import = ["std/doc@1", "std/types@1", "std/lint@1", "std/docs@1"]

writer: actor {{ Someone who keeps notes. }}

syncer: part {{ Copies notes to the store. }}

kept: goal {{ A note is never lost. }}

lonely: goal {{ Nobody serves this. }}

sync: feature(kept, writer) {
  | How a sync copies notes.

  copies: rule(syncer) {
    must {{ copy every changed note }}
  }

  reports: rule(syncer) {
    must {{ report a note it can't read }}
  }

  unreadable: error(422, id = "unreadable") {
    title = "can't read {file}"
    file: String
    | Reported as [sync.reports] says.
  }
}

docs {
  lenses = [user, contributor, explore]
}
`

// The gaps page gathers the intent gaps heron lint reports, by feature,
// at the bottom of every area's navigation, in the lenses that show the
// parts; contract coverage is a figure on a feature's page and a column
// of the Features page there; the user lens has neither, and a tree with
// no intent gap has no gaps page.
func TestGapsAndContractCoverage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte(gapsTree), 0o644); err != nil {
		t.Fatal(err)
	}
	_, f := lensFiles(t, filepath.Join(dir, "index.heron"))
	for _, l := range []string{"contributor/", "explore/"} {
		gaps := f[l+"_gaps.html"]
		mustHave(t, l+"gaps", gaps, `<details class="gap-group"><summary><code>lonely</code> <span class="gap-count">1</span>`, `href="index.html#lonely"`, `nothing serves lonely`)
		mustNotHave(t, l+"gaps", gaps, "copies")
		mustHave(t, l+"nav", sidebarRe.FindString(f[l+"sync.html"]), `<div class="nav-group nav-site">`, `href="_gaps.html"`)
		mustHave(t, l+"feature", f[l+"sync.html"], `<p class="feature-contract"><span class="feature-contract-label">Contract coverage</span> <span class="feature-figure">1 of 2</span>`)
		mustHave(t, l+"features", f[l+"_features.html"], `<p class="features-head"><span>Feature</span><span>Contract coverage</span></p>`, `<span class="feature-figure">1 of 2</span>`)
	}
	if _, ok := f["_gaps.html"]; ok {
		t.Error("the user lens has a gaps page")
	}
	mustNotHave(t, "user nav", sidebarRe.FindString(f["sync.html"]), `href="_gaps.html"`)
	mustNotHave(t, "user feature", f["sync.html"], "feature-contract", "Contract coverage")
	mustNotHave(t, "user features", f["_features.html"], "feature-figure", "Contract coverage")
	// Without an intent gap there's no gaps page.
	_, k := lensFiles(t, kite)
	for p := range k {
		if strings.HasSuffix(p, "_gaps.html") {
			t.Errorf("%s: a gaps page with no gap in it", p)
		}
	}
}
