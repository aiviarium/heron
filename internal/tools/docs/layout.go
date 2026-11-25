package docs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/resolve"
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
	case "conformance":
		err = s.conformancePage(c)
	case "releases":
		err = s.releasesPage(c)
	case "spec":
		err = s.specPage(c)
	case "home":
		err = s.home(c)
	case "changelog":
		err = s.changelog(c)
	case "landing":
		err = s.landing(c)
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
		s.homeTiles(c)
		// The nodes the root holds that have no page of their own still
		// have their place here.
		d := c.at(n, c.Level)
		for _, ch := range s.inOrder(n.Children) {
			if !s.shown(ch) || s.pageOf[ch] != nil {
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
		name := termName(s, t)
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
	b.WriteString(`<title>` + esc(title) + `</title>`)
	b.WriteString(`<script>try{var t=localStorage.getItem("heron-theme");if(t)document.documentElement.dataset.theme=t}catch(e){}</script>`)
	b.WriteString(`<link rel="stylesheet" href="` + a + `docs.css">`)
	if s.Theme != nil {
		b.WriteString(`<link rel="stylesheet" href="` + a + `theme/theme.css">`)
	}
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
	b.WriteString(`<div class="` + cls + `">`)
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
	b.WriteString(`<h1 class="page-title">` + esc(p.Title) + s.kindTag(p.Node) + `</h1>`)
	b.WriteString(s.slotHTML(nil, "before", ""))
	b.WriteString(f.HTML)
	b.WriteString(s.slotHTML(nil, "after", ""))
	s.prevNext(&b, p)
	b.WriteString(`</main>`)
	b.WriteString(`<aside class="aside">`)
	if f.Aside != "" {
		b.WriteString(f.Aside)
	}
	if len(f.Outline) > 1 || f.Aside == "" && len(f.Outline) > 0 {
		b.WriteString(`<nav class="outline" aria-label="` + attr(label("docs.label.outline")) + `"><div class="label">` + esc(label("docs.label.on_this_page")) + `</div><ul>`)
		for _, h := range f.Outline {
			b.WriteString(fmt.Sprintf(`<li class="l%d"><a href="#%s">%s</a></li>`, h.Level, attr(h.ID), esc(h.Title)))
		}
		b.WriteString(`</ul></nav>`)
	}
	b.WriteString(`</aside></div>`)
	b.WriteString(`<script src="` + attr(s.Opts.Data) + `" defer></script>`)
	b.WriteString(`<script src="` + a + `docs.js" defer></script>`)
	b.WriteString(`</body></html>`)
	return b.String()
}

func (s *Site) topbar(b *strings.Builder, p *Page) {
	b.WriteString(`<header class="topbar"><div class="topbar-in">`)
	b.WriteString(`<button type="button" class="icon-btn nav-toggle" aria-label="` + attr(label("docs.action.menu")) + `" aria-expanded="false" data-island="nav">` + iconMenu + `</button>`)
	mark := logoMark
	if s.Theme != nil && s.Theme.Logo != "" {
		mark = `<img class="mark" src="` + attr(s.Opts.Assets+s.Theme.Logo) + `" width="18" height="18" alt="">`
	}
	b.WriteString(`<a class="logo" href="` + attr(s.href(s.Home.File)) + `">` + mark + `<span>` + esc(s.title()) + `</span></a>`)
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
	s.lensSwitch(b, p)
	b.WriteString(`<button type="button" class="search-btn" data-island="search" aria-label="` + attr(label("docs.action.search")) + `">` + iconSearch + `<span>` + esc(label("docs.action.search")) + `</span><kbd>⌘K</kbd></button>`)
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
	b.WriteString(`<nav class="lens-switch" aria-label="` + attr(label("docs.label.lens")) + `">`)
	for _, o := range s.lenses.sites {
		cur := ""
		if o == s {
			cur = ` aria-current="true"`
		}
		b.WriteString(`<a href="` + attr(s.lensHref(p, o)) + `" data-lens="` + attr(o.Opts.Lens.Name) + `"` + cur + `>` + esc(o.Opts.Lens.Title) + `</a>`)
	}
	b.WriteString(`</nav>`)
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
		b.WriteString(`<li><a href="` + attr(s.href(q.File)) + `"` + cur + `>`)
		if q.Layout == "op" {
			b.WriteString(`<span class="m m-` + strings.ToLower(attr(q.Method)) + `">` + esc(q.Method) + `</span>`)
		}
		b.WriteString(`<span class="t">` + esc(q.Title) + `</span></a>`)
		if q == p {
			s.pageNodes(b, p)
		}
		if len(q.Children) > 0 && q.Layout != "api" && (depth > 0 || s.within(p, q)) {
			b.WriteString(`<ul>`)
			for _, c := range q.Children {
				item(c, depth+1)
			}
			b.WriteString(`</ul>`)
		}
		b.WriteString(`</li>`)
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
	for _, g := range a.Groups {
		b.WriteString(`<div class="nav-group">`)
		switch {
		case g.Head != nil:
			cur := ""
			if g.Head == p {
				cur = ` aria-current="page"`
			}
			b.WriteString(`<a class="label" href="` + attr(s.href(g.Head.File)) + `"` + cur + `>` + esc(g.Title) + `</a>`)
		case g.Title != "":
			b.WriteString(`<div class="label">` + esc(g.Title) + `</div>`)
		}
		b.WriteString(`<ul class="nav">`)
		for _, q := range g.Pages {
			if q.Layout == "api" {
				continue
			}
			item(q, 0)
		}
		b.WriteString(`</ul></div>`)
	}
	b.WriteString(s.slotHTML(nil, "sidebar", ""))
	b.WriteString(`</div></nav>`)
}

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
	logoMark   = `<svg class="mark" viewBox="0 0 20 20" width="18" height="18" aria-hidden="true"><path d="M5 3.5v13M15 3.5v13M5 10h10" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" fill="none"/></svg>`
)
