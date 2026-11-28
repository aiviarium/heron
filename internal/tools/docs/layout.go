package docs

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/web/tokens"
)

// RenderPage renders a page as a whole HTML document: the top bar, the
// navigation, the page and the right column.
func (s *Site) RenderPage(p *Page) (string, error) {
	f, err := s.PageFragment(p)
	if err != nil {
		return "", err
	}
	return s.document(p, f), nil
}

// PageFragment renders a page's content without the layout around it.
func (s *Site) PageFragment(p *Page) (*Fragment, error) {
	c := s.newCtx(p.Node, p, 1)
	var err error
	switch p.Layout {
	case "glossary":
		err = s.glossary(c)
	case "diagnostics":
		err = s.diagnosticsPage(c)
	case "features":
		err = s.featuresPage(c)
	case "home":
		err = s.home(c)
	case "changelog":
		err = s.changelog(c)
	case "landing":
		err = s.landing(c)
	case "schema":
		err = s.schemaLanding(c)
	case "kind":
		err = s.kindPage(c)
	case "annotation":
		err = s.annotationPage(c)
	default:
		err = c.render()
	}
	if err != nil {
		return nil, err
	}
	return c.fragment(), nil
}

// RenderNode renders one node as the docs show it, for the live preview:
// its page's content when it has a page, its own view otherwise.
func (s *Site) RenderNode(n *resolve.Node) (*Fragment, error) {
	if p := s.pageOf[n]; p != nil {
		f, err := s.PageFragment(p)
		if err != nil {
			return nil, err
		}
		f.HTML = `<h1 class="page-title">` + esc(p.Title) + `</h1>` + f.HTML
		return f, nil
	}
	c := s.newCtx(n, s.anchorOf[n], 1)
	if err := c.render(); err != nil {
		return nil, err
	}
	return c.fragment(), nil
}

// RenderFile renders every top-level node of one file, for previewing a
// file as a whole.
func (s *Site) RenderFile(file resolve.FileID) (*Fragment, error) {
	out := &Fragment{}
	islands := map[string]bool{}
	var nodes []*resolve.Node
	s.Tree.Walk(func(n *resolve.Node) {
		if n.Decl.File == nil || n.Decl.File.ID != file || !own(n) {
			return
		}
		for a := n.Parent; a != nil; a = a.Parent {
			if a.Decl.File != nil && a.Decl.File.ID == file {
				return
			}
		}
		nodes = append(nodes, n)
	})
	var b strings.Builder
	for _, n := range nodes {
		f, err := s.RenderNode(n)
		if err != nil {
			return nil, err
		}
		b.WriteString(f.HTML)
		out.Outline = append(out.Outline, f.Outline...)
		for _, i := range f.Islands {
			islands[i] = true
		}
		out.Aside += f.Aside
	}
	out.HTML = b.String()
	for k := range islands {
		out.Islands = append(out.Islands, k)
	}
	sort.Strings(out.Islands)
	return out, nil
}

func (s *Site) home(c *Ctx) error {
	n := s.homeNode()
	if s.nav() != lens.NavTree {
		if h := s.proseHTML(n); h != "" {
			c.W(`<div class="lead prose">`, h, `</div>`)
		}
		// The package's goals open its home page, before the tiles.
		d := c.at(n, c.Level)
		for _, ch := range n.Children {
			if s.isIntent(ch) && s.shown(ch) && s.pageOf[ch] == nil {
				if err := d.Render(ch); err != nil {
					return err
				}
			}
		}
		s.homeTiles(c)
		// The nodes the root holds that have no page of their own still
		// have their place here.
		for _, ch := range s.inOrder(n.Children) {
			if !s.shown(ch) || s.pageOf[ch] != nil || s.isIntent(ch) {
				continue
			}
			if err := d.Render(ch); err != nil {
				return err
			}
		}
		return nil
	}
	if n != s.Tree.Root {
		if h := s.proseHTML(n); h != "" {
			c.W(`<div class="lead prose">`, h, `</div>`)
		}
		return c.at(n, c.Level).childrenOrLinks()
	}
	c.Prose(n)
	return c.childrenOrLinks()
}

func (s *Site) glossary(c *Ctx) error {
	terms := append([]*resolve.Node(nil), s.terms...)
	sort.SliceStable(terms, func(i, j int) bool {
		return strings.ToLower(termName(s, terms[i])) < strings.ToLower(termName(s, terms[j]))
	})
	c.W(`<dl class="glossary">`)
	letter := ""
	for _, t := range terms {
		name := termHeading(s, t)
		if l := strings.ToUpper(name[:1]); l != letter {
			letter = l
			c.W(`<div class="letter" id="letter-`, attr(l), `">`, esc(l), `</div>`)
			*c.outline = append(*c.outline, Heading{ID: "letter-" + l, Title: l, Level: 2})
		}
		c.W(`<div class="term" id="`, attr(Anchor(t)), `"><dt class="term-name">`, s.linkHTML(t, esc(name), ""))
		if al := termAliases(t); len(al) > 0 {
			c.W(`<span class="aliases">`, esc(strings.Join(al, ", ")), `</span>`)
		}
		c.W(`</dt><dd><div class="prose">`, s.proseHTML(t), `</div></dd></div>`)
	}
	c.W(`</dl>`)
	return nil
}

// breadcrumb is the path of pages and sections down to a page.
func (s *Site) breadcrumb(p *Page) []*Page {
	var out []*Page
	for x := p.Parent; x != nil; x = x.Parent {
		out = append([]*Page{x}, out...)
	}
	return out
}

func (s *Site) document(p *Page, f *Fragment) string {
	var b strings.Builder
	a := s.Opts.Assets
	title := p.Title
	if p != s.Home {
		title += " · " + s.title()
	}
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">`)
	// The canvas takes the theme's scheme before the stylesheet arrives.
	b.WriteString(`<meta name="color-scheme" content="dark light">`)
	b.WriteString(`<title>` + esc(title) + `</title>`)
	b.WriteString(s.favicon())
	b.WriteString(canvasStyle())
	b.WriteString(themeScript)
	b.WriteString(`<link rel="stylesheet" href="` + a + `docs.css?v=` + assetVersion("docs.css") + `">`)
	if s.Theme != nil {
		b.WriteString(`<link rel="stylesheet" href="` + a + `theme/theme.css?v=` + contentVersion(s.Theme.CSS) + `">`)
	}
	// The page paints whole: never before its frame, the navigation and
	// the right column, are parsed, so nothing pops in after first paint.
	b.WriteString(`<link rel="expect" href="#` + layoutID + `" blocking="render">`)
	lensName := ""
	if s.Opts.Lens != nil {
		lensName = s.Opts.Lens.Name
	}
	b.WriteString(`</head><body data-layout="` + attr(p.Layout) + `" data-page="` + attr(p.File) + `" data-lens="` + attr(lensName) + `">`)
	b.WriteString(`<a class="skip" href="#main">` + esc(label("docs.action.skip")) + `</a>`)
	s.topbar(&b, p)
	cls := "layout"
	if f.Aside != "" {
		cls += " has-examples"
	}
	if p.Layout == "landing" {
		cls += " no-sidebar"
	}
	b.WriteString(`<div class="` + cls + `" id="` + layoutID + `">`)
	if p.Layout != "landing" {
		s.sidebar(&b, p)
	}
	b.WriteString(`<main id="main" class="page">`)
	if crumbs := s.breadcrumb(p); len(crumbs) > 0 {
		b.WriteString(`<nav class="crumbs" aria-label="` + attr(label("docs.label.breadcrumb")) + `"><ol>`)
		for _, c := range crumbs {
			b.WriteString(`<li><a href="` + attr(s.href(c.File)) + `">` + esc(c.Title) + `</a></li>`)
		}
		b.WriteString(`</ol></nav>`)
	} else if p.Area != nil && p != s.Home && len(s.Areas) > 1 && p.Layout != "glossary" && p.Area.Key != "docs" {
		b.WriteString(`<nav class="crumbs" aria-label="` + attr(label("docs.label.breadcrumb")) + `"><ol><li>` + esc(p.Area.Title) + `</li></ol></nav>`)
	}
	b.WriteString(`<h1 class="page-title">` + s.nodeIcon(p.Node, "icon title-icon") + strings.Replace(p.icon, `class="icon"`, `class="icon title-icon"`, 1) + esc(p.Title) + s.kindTag(p.Node) + `</h1>`)
	b.WriteString(s.slotHTML(nil, "before", ""))
	b.WriteString(f.HTML)
	b.WriteString(s.slotHTML(nil, "after", ""))
	s.prevNext(&b, p)
	b.WriteString(`</main>`)
	b.WriteString(`<aside class="aside">`)
	if f.Aside != "" {
		b.WriteString(f.Aside)
	}
	ol := s.outline(p, f)
	if len(ol) > 1 || f.Aside == "" && len(ol) > 0 {
		b.WriteString(`<nav class="outline" aria-label="` + attr(label("docs.label.outline")) + `"><div class="label">` + esc(label("docs.label.on_this_page")) + `</div><ul>`)
		for _, h := range ol {
			bind := ""
			if h.Binding != "" {
				bind = ` data-binding="` + attr(h.Binding) + `"`
				if h.Hidden {
					bind += " hidden"
				}
			}
			b.WriteString(fmt.Sprintf(`<li class="l%d"%s><a href="#%s">%s%s</a></li>`, h.Level, bind, attr(h.ID), s.nodeIcon(s.nodeByAnchor(h.ID), "icon kind-icon"), esc(h.Title)))
		}
		b.WriteString(`</ul></nav>`)
	}
	b.WriteString(`</aside>`)
	if s.hasBindings() {
		// The binding the reader picked last, applied before the page paints.
		b.WriteString(bindingScript)
	}
	b.WriteString(`</div>`)
	b.WriteString(`<script src="` + attr(s.Opts.Data) + `" defer></script>`)
	b.WriteString(`<script src="` + a + `docs.js?v=` + assetVersion("docs.js") + `" defer></script>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

func (s *Site) topbar(b *strings.Builder, p *Page) {
	b.WriteString(`<header class="topbar"><div class="topbar-in">`)
	b.WriteString(`<button type="button" class="icon-btn nav-toggle" aria-label="` + attr(label("docs.action.menu")) + `" aria-expanded="false" data-island="nav">` + iconMenu + `</button>`)
	mark, cls := s.logoMark()
	b.WriteString(`<a class="` + cls + `" href="` + attr(s.href(s.Home.File)) + `">` + mark + `<span>` + esc(s.title()) + `</span></a>`)
	if len(s.Areas) > 1 {
		b.WriteString(`<nav class="tabs" aria-label="` + attr(label("docs.label.sections")) + `">`)
		for _, a := range s.Areas {
			first := s.firstPage(a)
			if first == nil {
				continue
			}
			cur := ""
			if p.Area == a {
				cur = ` aria-current="true"`
			}
			b.WriteString(`<a href="` + attr(s.href(first.File)) + `"` + cur + `>` + esc(a.Title) + `</a>`)
		}
		b.WriteString(`</nav>`)
	}
	b.WriteString(s.slotHTML(nil, "header", ""))
	b.WriteString(`<div class="topbar-end">`)
	b.WriteString(`<button type="button" class="search-btn" data-island="search" aria-label="` + attr(label("docs.action.search")) + `">` + iconSearch + `<span>` + esc(label("docs.action.search")) + `</span><kbd>⌘K</kbd></button>`)
	s.lensSwitch(b, p)
	b.WriteString(`<div class="version" data-island="version"><select aria-label="` + attr(label("docs.label.version")) + `">`)
	cur := s.Opts.Version
	if cur == "" {
		cur = label("docs.version.current")
	}
	b.WriteString(`<option value="" selected>` + esc(cur) + `</option>`)
	for _, v := range s.Opts.Versions {
		if v == s.Opts.Version {
			continue
		}
		b.WriteString(`<option value="` + attr(v) + `">` + esc(v) + `</option>`)
	}
	b.WriteString(`</select></div>`)
	b.WriteString(`<button type="button" class="icon-btn theme" data-island="theme" aria-label="` + attr(label("docs.action.theme")) + `">` + iconTheme + `</button>`)
	b.WriteString(`</div></div></header>`)
}

// lensSwitch writes the switch between the site's lenses, each a link to
// the same place in that lens.
func (s *Site) lensSwitch(b *strings.Builder, p *Page) {
	if s.lenses == nil || len(s.lenses.sites) < 2 {
		return
	}
	// A dropdown of plain links, which works without a script; the lens
	// island draws it as the version switch's select.
	b.WriteString(`<div class="lens-switch" data-island="lens"><details class="lens-menu"><summary aria-label="` + attr(label("docs.label.lens")) + `"><span class="caption">` + esc(label("docs.label.view")) + `</span><span class="v">` + esc(s.Opts.Lens.Title) + `</span></summary><nav aria-label="` + attr(label("docs.label.lens")) + `"><ul>`)
	for _, o := range s.lenses.sites {
		cur := ""
		if o == s {
			cur = ` aria-current="true"`
		}
		b.WriteString(`<li><a href="` + attr(s.lensHref(p, o)) + `" data-lens="` + attr(o.Opts.Lens.Name) + `"` + cur + `>` + esc(o.Opts.Lens.Title) + `</a></li>`)
	}
	b.WriteString(`</ul></nav></details></div>`)
}

func (s *Site) firstPage(a *Area) *Page {
	if len(a.Pages) > 0 {
		return a.Pages[0]
	}
	for _, g := range a.Groups {
		if len(g.Pages) > 0 {
			return g.Pages[0]
		}
	}
	return nil
}

func (s *Site) sidebar(b *strings.Builder, p *Page) {
	b.WriteString(`<nav class="sidebar" aria-label="` + attr(label("docs.label.navigation")) + `"><div class="sidebar-in">`)
	a := p.Area
	if a == nil {
		a = s.Areas[0]
	}
	if len(s.Areas) > 1 {
		// On narrow screens the tabs move into the sidebar.
		b.WriteString(`<div class="nav-areas">`)
		for _, x := range s.Areas {
			if f := s.firstPage(x); f != nil {
				cur := ""
				if x == a {
					cur = ` aria-current="true"`
				}
				b.WriteString(`<a href="` + attr(s.href(f.File)) + `"` + cur + `>` + esc(x.Title) + `</a>`)
			}
		}
		b.WriteString(`</div>`)
	}
	var item func(q *Page, depth int)
	item = func(q *Page, depth int) {
		cur := ""
		if q == p {
			cur = ` aria-current="page"`
		}
		link := &strings.Builder{}
		bb := b
		b = link
		b.WriteString(`<a href="` + attr(s.href(q.File)) + `"` + cur + `>`)
		if q.Layout == "op" {
			b.WriteString(`<span class="m m-` + strings.ToLower(attr(q.Method)) + `"`)
			for _, bm := range q.BindMethods {
				b.WriteString(` data-m-` + bm[0] + `="` + attr(bm[1]) + `"`)
			}
			b.WriteString(`>` + esc(q.Method) + `</span>`)
		}
		if q.Layout != "op" {
			b.WriteString(s.nodeIcon(q.Node, "icon") + q.icon)
		}
		b.WriteString(`<span class="t">` + esc(q.Title) + `</span>`)
		if q.Layout == "op" && q.Node != nil && deprecatedBadge(q.Node) != "" {
			b.WriteString(`<span class="nav-deprecated" title="` + attr(label("docs.label.deprecated")) + `">` + esc(label("docs.label.deprecated")) + `</span>`)
		}
		b.WriteString(`</a>`)
		b = bb
		if len(q.Children) == 0 || q.Layout == "api" {
			b.WriteString(`<li>` + link.String() + `</li>`)
			return
		}
		// A branch: the whole tree is on the page, each branch a disclosure
		// that opens in place, the current page's ancestors open. Its label
		// navigates; the chevron beside it opens and closes it.
		open := ""
		if s.within(p, q) {
			open = " open"
		}
		b.WriteString(`<li><details class="nav-branch" data-key="` + attr(q.File) + `"` + open + `><summary>` + link.String() + `</summary><ul>`)
		for _, c := range q.Children {
			item(c, depth+1)
		}
		b.WriteString(`</ul></details></li>`)
	}
	if len(a.Pages) > 0 {
		b.WriteString(`<ul class="nav">`)
		for _, q := range a.Pages {
			if q == s.Home {
				continue
			}
			item(q, 0)
		}
		b.WriteString(`</ul>`)
	}
	part := ""
	for _, g := range a.Groups {
		if g.Part != "" && g.Part != part {
			// A part of the area, as the Reference's API, Schema and CLI.
			part = g.Part
			b.WriteString(`<div class="nav-part">` + esc(part) + `</div>`)
		}
		if g.Fold {
			// A group that opens, closed unless the page is one of its own.
			open := ""
			for _, q := range g.Pages {
				if q == p {
					open = " open"
				}
			}
			b.WriteString(`<div class="nav-group"><details class="nav-types nav-fold" data-key="fold:` + attr(g.Title) + `"` + open + `><summary>` + esc(g.Title) + `<span class="count">` + fmt.Sprint(len(g.Pages)) + `</span></summary><ul class="nav">`)
			for _, q := range g.Pages {
				item(q, 0)
			}
			b.WriteString(`</ul></details></div>`)
			continue
		}
		b.WriteString(`<div class="nav-group">`)
		switch {
		case g.Head != nil:
			cur := ""
			if g.Head == p {
				cur = ` aria-current="page"`
			}
			b.WriteString(`<a class="label" href="` + attr(s.href(g.Head.File)) + `"` + cur + `>` + s.ownIcon(g.Head.Node) + esc(g.Title) + `</a>`)
		case g.Title != "":
			var gn *resolve.Node
			if len(g.Pages) > 0 && g.Pages[0].Layout == "api" {
				gn = g.Pages[0].Node
			}
			b.WriteString(`<div class="label">` + s.ownIcon(gn) + esc(g.Title) + `</div>`)
		}
		b.WriteString(`<ul class="nav">`)
		for _, q := range g.Pages {
			if q.Layout == "api" {
				continue
			}
			item(q, 0)
		}
		b.WriteString(`</ul>`)
		if len(g.Types) > 0 {
			// An api's types, after its ops: closed unless the page is one.
			open := ""
			for _, q := range g.Types {
				if q == p {
					open = " open"
				}
			}
			b.WriteString(`<details class="nav-types" data-key="types:` + attr(g.Types[0].File) + `"` + open + `><summary>` + esc(label("docs.nav.types")) + `<span class="count">` + fmt.Sprint(len(g.Types)) + `</span></summary><ul class="nav">`)
			for _, q := range g.Types {
				item(q, 0)
			}
			b.WriteString(`</ul></details>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(s.slotHTML(nil, "sidebar", ""))
	b.WriteString(`</div></nav>`)
	b.WriteString(navScrollScript)
}

// navScrollScript puts the navigation back as the reader left it, before
// the page paints. First the tree: the branches open on the last page of
// the same lens's site and site area open again (kept in localStorage, so
// a reload keeps them too), beside the current page's ancestors, which the
// page opens itself. Then the scroll: the entry that was followed stays
// where it was on screen, and otherwise the scroll position is kept. The
// current page's entry is then scrolled into view if it isn't. docs.js
// saves the open branches as they change, and the position and the entry
// followed as a page is left.
const navScrollScript = `<script>(function(){var n=document.currentScript.previousElementSibling,l=document.querySelector(".logo"),t=document.querySelector('.tabs [aria-current]'),a=n.querySelector('[aria-current="page"]'),k=l?l.href+(t?" "+t.href:""):"",v=null,o=null;` +
	`try{o=JSON.parse(localStorage.getItem("heron-tree:"+k))}catch(e){}` +
	`if(o&&o.forEach)o.forEach(function(x){var d=n.querySelector('details[data-key="'+String(x).replace(/["\\]/g,"")+'"]');if(d)d.open=true});` +
	`try{v=l&&JSON.parse(sessionStorage.getItem("heron-nav:"+k))}catch(e){}` +
	`if(typeof v==="number")v={t:v};` +
	`if(v&&typeof v==="object"){if(a&&v.h&&v.h===a.getAttribute("href")&&typeof v.y==="number")n.scrollTop+=a.getBoundingClientRect().top-n.getBoundingClientRect().top-v.y;else if(typeof v.t==="number")n.scrollTop=v.t}` +
	`if(a){var r=a.getBoundingClientRect(),b=n.getBoundingClientRect();if(r.top<b.top||r.bottom>b.bottom)a.scrollIntoView({block:"nearest"})}})()</script>`

// themeScript applies the reader's chosen theme before the stylesheet
// and the first paint; without one the system's preference holds.
// themeScript also settles the promises of a page transition the browser
// skips (as it does for pages opened from file://), so a skipped
// transition is never an unhandled rejection, including one the browser
// rejects before the page's pageswap or pagereveal handler sees it.
const themeScript = `<script>try{var t=localStorage.getItem("heron-theme");if(t==="dark"||t==="light")document.documentElement.dataset.theme=t}catch(e){}` +
	`["pageswap","pagereveal"].forEach(function(k){addEventListener(k,function(e){var v=e.viewTransition;if(v){[v.ready,v.finished,v.updateCallbackDone].forEach(function(q){q&&q.catch(function(){})})}})});` +
	`addEventListener("unhandledrejection",function(e){var r=e.reason;if(r&&(r.name==="InvalidStateError"||r.name==="AbortError")&&/transition/i.test(r.message))e.preventDefault()})</script>`

// canvasStyle colours the canvas in the page's theme from the first
// frame any engine paints, before the stylesheet has loaded: the page
// background of each theme, keyed off the theme the reader chose
// (data-theme, set by themeScript) or else the system's preference. It's
// in the heron layer, so docs.css and a theme package's layer win over it
// once they load.
var canvasStyle = sync.OnceValue(func() string {
	dark, light := "#111111", "#ffffff"
	if set, err := tokens.Load(); err == nil {
		for _, c := range set.Colors {
			if c.Name == "bg-page" {
				dark, light = c.Dark, c.Light
			}
		}
	}
	d := "color-scheme:dark;background:" + dark
	l := "color-scheme:light;background:" + light
	return `<style>@layer heron{html{` + d + `}html[data-theme=light]{` + l + `}@media (prefers-color-scheme: light){html:not([data-theme=dark]){` + l + `}}}</style>`
})

// layoutID is the id of a page's frame, which the page's render-blocking
// link waits for.
const layoutID = "h-layout"

// within reports whether page p is q or below it.
func (s *Site) within(p, q *Page) bool {
	for x := p; x != nil; x = x.Parent {
		if x == q {
			return true
		}
	}
	return false
}

// methodShort is a method as the navigation's fixed-width column shows it.
func methodShort(m string) string {
	switch m {
	case "DELETE":
		return "DEL"
	case "PATCH":
		return "PATCH"
	}
	return m
}

func (s *Site) prevNext(b *strings.Builder, p *Page) {
	if p.prev == nil && p.next == nil {
		return
	}
	b.WriteString(`<nav class="prevnext" aria-label="` + attr(label("docs.label.pages")) + `">`)
	if p.prev != nil {
		b.WriteString(`<a class="prev" href="` + attr(s.href(p.prev.File)) + `"><span class="label">` + esc(label("docs.label.previous")) + `</span><span class="t">` + esc(p.prev.Title) + `</span></a>`)
	} else {
		b.WriteString(`<span></span>`)
	}
	if p.next != nil {
		b.WriteString(`<a class="next" href="` + attr(s.href(p.next.File)) + `"><span class="label">` + esc(label("docs.label.next")) + `</span><span class="t">` + esc(p.next.Title) + `</span></a>`)
	}
	b.WriteString(`</nav>`)
}

const (
	iconSearch = `<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" stroke-width="1.4"/><path d="M10.5 10.5 14 14" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"/></svg>`
	iconTheme  = `<svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true"><circle cx="8" cy="8" r="5.5" fill="none" stroke="currentColor" stroke-width="1.3"/><path d="M8 2.5a5.5 5.5 0 0 1 0 11z" fill="currentColor"/></svg>`
	iconMenu   = `<svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true"><path d="M2.5 4.5h11M2.5 8h11M2.5 11.5h11" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>`
)

// outline is a page's outline: its headings and, under the heading of a
// node with no subheadings of its own, the nodes the page shows inside it
// that have an anchor, two levels deep in all. What's on a page is here,
// and the navigation lists pages alone.
func (s *Site) outline(p *Page, f *Fragment) []Heading {
	if p.Node == nil {
		return f.Outline
	}
	byAnchor := map[string]*resolve.Node{}
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range n.Children {
			if s.anchorOf[ch] == p && s.pageOf[ch] == nil {
				byAnchor[Anchor(ch)] = ch
				walk(ch)
			}
		}
	}
	walk(p.Node)
	shown := func(n *resolve.Node) []*resolve.Node {
		var out []*resolve.Node
		for _, ch := range n.Children {
			if ch.Name == "" || !own(ch) || ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue || ch.Shape == resolve.ShapeStep || s.anchorOf[ch] != p || s.pageOf[ch] != nil {
				continue
			}
			if strings.Contains(f.HTML, `id="`+attr(Anchor(ch))+`"`) {
				out = append(out, ch)
			}
		}
		// In the order the page shows them, which is not the tree's.
		at := func(n *resolve.Node) int { return strings.Index(f.HTML, `id="`+attr(Anchor(n))+`"`) }
		sort.SliceStable(out, func(i, j int) bool { return at(out[i]) < at(out[j]) })
		return out
	}
	var out []Heading
	if len(f.Outline) == 0 {
		// A page without headings: the nodes it shows, and theirs.
		for _, ch := range shown(p.Node) {
			out = append(out, Heading{ID: Anchor(ch), Title: displayName(s, ch), Level: 2})
			for _, g := range shown(ch) {
				out = append(out, Heading{ID: Anchor(g), Title: displayName(s, g), Level: 3})
			}
		}
		return out
	}
	for i, h := range f.Outline {
		out = append(out, h)
		if h.Level != 2 || i+1 < len(f.Outline) && f.Outline[i+1].Level > 2 {
			continue
		}
		n := byAnchor[h.ID]
		if n == nil {
			continue
		}
		for _, ch := range shown(n) {
			out = append(out, Heading{ID: Anchor(ch), Title: displayName(s, ch), Level: 3})
		}
	}
	return out
}
