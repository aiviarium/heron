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

const iconTree = `import = ["std/doc@1", "std/spec@1", "std/docs@1"]

@icon("compass")
area: section {
  | An area with an icon of its own, ready to launch :rocket:.

  part: section {
    | A part.

    holds: rule(area) {
      must {{ hold }}
    }

    | <note kind="warning">Mind it.</note>
  }
}

@icon("star")
starred: section {
  | A section with the tree's own icon.
}

star: svg_icon { file = "./star.svg" }
`

func writeIconTree(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.heron"), []byte(src), 0o644)
	os.WriteFile(filepath.Join(dir, "star.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M12 2l3 7h7l-6 4 2 7-6-4-6 4 2-7-6-4h7z"/></svg>`), 0o644)
	return filepath.Join(dir, "index.heron")
}

// An icon mark names an icon a package declares: std/docs's set, or the
// tree's own, which the checker reads from its SVG file.
func TestIconsCheck(t *testing.T) {
	entry := writeIconTree(t, strings.Replace(iconTree, `@icon("compass")`, `@icon("compas")`, 1)+"\nbroken: svg_icon { file = \"./nope.svg\" }\n")
	id, _ := resolve.Canonical(entry)
	res := check.NewSession().Check(id)
	var msgs []string
	for _, d := range res.Diags {
		msgs = append(msgs, d.Message)
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{"no icon compas; did you mean compass?", "broken: its file ./nope.svg can't be read"} {
		if !strings.Contains(all, want) {
			t.Errorf("diagnostics lack %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "star") {
		t.Errorf("the tree's own icon is reported:\n%s", all)
	}
}

// Icons in the docs: a node's own in the navigation, its page's title and
// the home's cards; its kind's, muted, in the outline and search; the
// theme's on callouts; inline, in currentColor, hidden from assistive
// technology; and data.js holds the icons search shows and no other.
func TestIconsRender(t *testing.T) {
	_, f := lensFiles(t, writeIconTree(t, iconTree))
	page := f["area.part.html"]
	svg := regexp.MustCompile(`<svg class="icon[^"]*" viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor"[^>]*aria-hidden="true"`)
	if !svg.MatchString(page) {
		t.Fatalf("no inline icon in currentColor, hidden:\n%.600s", page)
	}
	nav := regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`).FindString(page)
	if !regexp.MustCompile(`<a href="area.html"><svg class="icon"[^>]*>.*?</svg><span class="t">Area</span>`).MatchString(nav) {
		t.Errorf("the navigation's entry lacks its section's icon:\n%s", nav)
	}
	if regexp.MustCompile(`<a href="area.part.html"[^>]*><svg`).MatchString(nav) {
		t.Errorf("an entry with no icon of its own shows one:\n%s", nav)
	}
	if !strings.Contains(f["area.html"], `<h1 class="page-title"><svg class="icon title-icon"`) {
		t.Error("the page's title lacks its icon")
	}
	if strings.Contains(f["area.part.html"], `<h1 class="page-title"><svg`) {
		t.Error("a section with no icon shows one in its title")
	}
	outline := regexp.MustCompile(`(?s)<nav class="outline".*?</nav>`).FindString(page)
	if !strings.Contains(outline, `<svg class="icon kind-icon"`) {
		t.Errorf("the outline lacks the rule's kind icon:\n%s", outline)
	}
	// A rule carries its mark in the gutter: the link to it, its kind and
	// name as its tooltip.
	if !strings.Contains(page, `<a class="rule-link gutter-mark" href="#area.part.holds" title="rule · Holds" aria-label="rule · Holds"><svg class="icon"`) {
		t.Error("the rule lacks its gutter mark")
	}
	if !regexp.MustCompile(`<aside class="callout callout-warning"><svg class="icon"`).MatchString(page) {
		t.Error("the warning callout lacks the theme's icon")
	}
	if !strings.Contains(f["starred.html"], `M12 2l3 7h7`) {
		t.Error("the tree's own icon isn't drawn from its SVG")
	}
	// A shortcode in prose is text, never an icon or an emoji.
	if !strings.Contains(f["area.html"], "ready to launch :rocket:.") {
		t.Error("a shortcode in prose isn't kept as text")
	}
	data := f["data.js"]
	// Search's text, a plain-text output, carries no icon.
	if regexp.MustCompile(`"x":"[^"]*svg`).MatchString(data) {
		t.Error("search's text holds an icon")
	}
	if !strings.Contains(data, `"i":"scale"`) || !strings.Contains(data, `"scale":"\u003csvg`) {
		t.Errorf("search's icons aren't in data.js: %.300s", data[strings.Index(data, "icons:"):])
	}
	if strings.Contains(data, `"zap":`) || strings.Contains(data, `"palette":`) {
		t.Error("data.js ships icons no result shows")
	}
}

// The navigation is a tree on every page: every branch is written, as a
// disclosure whose label is a link, open where the page is below it and
// closed elsewhere, so every page is a link away without a script.
func TestNavTree(t *testing.T) {
	_, f := lensFiles(t, writeIconTree(t, iconTree))
	nav := regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`)
	on := nav.FindString(f["starred.html"])
	if !strings.Contains(on, `<details class="nav-branch" data-key="area.html"><summary><a href="area.html">`) || !strings.Contains(on, `href="area.part.html"`) {
		t.Errorf("a branch the page isn't in isn't written closed with its children:\n%s", on)
	}
	in := nav.FindString(f["area.part.html"])
	if !strings.Contains(in, `<details class="nav-branch" data-key="area.html" open>`) {
		t.Errorf("the page's ancestor isn't open:\n%s", in)
	}
	// The inline script opens the branches the reader left open, before
	// the page paints.
	if !strings.Contains(f["starred.html"], `localStorage.getItem("heron-tree:"`) {
		t.Error("the open branches aren't put back before paint")
	}
}
