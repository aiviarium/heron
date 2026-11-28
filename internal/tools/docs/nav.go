package docs

import (
	"regexp"
	"strings"

	"github.com/aiviarium/heron/internal/core/resolve"
)

// Every lens has one navigation: the pages it keeps, nested as the tree
// nests them, in the tree's order, its entry pages first, split into the
// site's areas. The docs block's `area(…)` nodes each get an area of
// their own; the other top-level pages are the Docs area's; the API, the
// schema and the commands are the Reference's. The site's own pages, the
// features, errors, glossary and gaps, close every area's sidebar.

// arrangeAreas splits the top-level pages into the site's areas: the
// declared ones in the order the docs block writes them, then Docs, then
// Reference, each left out where the lens gives it no page.
func (s *Site) arrangeAreas(docs, ref *Area) {
	docs.Pages = s.dropAPIScopes(docs.Pages, ref)
	var declared []*Area
	for _, d := range s.Config.Areas {
		a := &Area{Key: "area:" + d.Node.Path(), Title: displayName(s, d.Node), Node: d.Node, Menu: d.Menu}
		declared = append(declared, a)
	}
	areaOf := func(n *resolve.Node) *Area {
		for x := n; x != nil; x = x.Parent {
			for _, a := range declared {
				if a.Node == x {
					return a
				}
			}
		}
		return docs
	}
	var rest []*Page
	for _, p := range docs.Pages {
		a := areaOf(p.Node)
		if a == docs {
			rest = append(rest, p)
			continue
		}
		var move func(q *Page)
		move = func(q *Page) {
			q.Area = a
			for _, c := range q.Children {
				move(c)
			}
		}
		move(p)
		a.Pages = append(a.Pages, p)
		if p.Node == a.Node {
			// The area's own page opens its navigation, and the pages
			// below it are the area's top-level entries.
			a.Pages = append(a.Pages, p.Children...)
			p.Children = nil
		}
	}
	docs.Pages = rest

	// The lens's entry pages come first in their area, out of their place.
	if l := s.Opts.Lens; l != nil {
		for i := len(l.Entry) - 1; i >= 0; i-- {
			p := s.pageOf[l.Entry[i]]
			if p == nil || p.Area == nil || p.Area == ref || p == s.Home {
				continue
			}
			a := p.Area
			a.Pages = removePage(a.Pages, p)
			p.Parent = nil
			a.Pages = append([]*Page{p}, a.Pages...)
		}
	}

	for _, a := range declared {
		if len(a.Pages) > 0 {
			s.Areas = append(s.Areas, a)
		}
	}
	if len(docs.Pages) > 0 || len(s.Areas) == 0 {
		s.Areas = append(s.Areas, docs)
	}
	if len(ref.Groups) > 0 {
		s.Areas = append(s.Areas, ref)
	}
	// The home page and the site's pages are the first area's.
	home := s.Areas[0]
	s.Home.Area = home
	for _, p := range s.sitePages {
		p.Area = home
	}
}

// dropAPIScopes moves the pages of the sections and scopes that hold an
// api and no page of their own but the api's and its errors' out of the
// navigation and into the Reference area, which lists the api.
func (s *Site) dropAPIScopes(ps []*Page, ref *Area) []*Page {
	var out []*Page
	for _, p := range ps {
		p.Children = s.dropAPIScopes(p.Children, ref)
		if s.apiScope(p) {
			var move func(q *Page)
			move = func(q *Page) {
				q.Area = ref
				for _, c := range q.Children {
					move(c)
				}
			}
			move(p)
			continue
		}
		out = append(out, p)
	}
	return out
}

// apiScope reports whether a page is a section's or a scope's that holds
// an api and no page of its own but the api's and its errors'.
func (s *Site) apiScope(p *Page) bool {
	if p.Node == nil || !s.isContainer(p.Node) || !s.holdsAPI(p.Node) {
		return false
	}
	for _, c := range p.Children {
		if c.Node == nil || len(c.Children) > 0 || !s.isContainer(c.Node) || !s.onlyDiagnostics(c.Node) {
			return false
		}
	}
	return true
}

// holdsAPI reports whether a node holds an api the Reference lists.
func (s *Site) holdsAPI(n *resolve.Node) bool {
	for _, ch := range n.Children {
		if s.kindPath(ch) == "types.api" && s.pageOf[ch] != nil {
			return true
		}
	}
	return false
}

func removePage(ps []*Page, p *Page) []*Page {
	var out []*Page
	for _, q := range ps {
		if q != p {
			q.Children = removePage(q.Children, p)
			out = append(out, q)
		}
	}
	return out
}

// homeTiles writes a lens's home page below the root's prose: its first
// entry page, or else its first authored page, as the primary link, then
// a card for each top-level entry of the home's area.
func (s *Site) homeTiles(c *Ctx) {
	area := s.Home.Area
	if area == nil {
		return
	}
	var primary *Page
	if l := s.Opts.Lens; l != nil {
		for _, n := range l.Entry {
			if p := s.pageOf[n]; p != nil && primary == nil {
				primary = p
			}
		}
	}
	if primary == nil {
		for _, p := range s.Pages {
			if primary == nil && p.Node != nil && s.authored(p.Node) {
				primary = p
			}
		}
	}
	if primary != nil {
		c.W(`<a class="home-primary" href="`, attr(s.href(primary.File)), `"><span class="home-primary-title">`, s.nodeIcon(primary.Node, "icon"), esc(primary.Title), `</span>`)
		if t := s.pageSentence(primary); t != "" {
			c.W(`<span class="home-primary-text">`, t, `</span>`)
		}
		c.W(`<span class="home-primary-arrow" aria-hidden="true">→</span></a>`)
	}
	var cards []*Page
	for _, p := range area.Pages {
		if p != primary && p != s.Home && (area.Node == nil || p.Node != area.Node) {
			cards = append(cards, p)
		}
	}
	if len(cards) == 0 {
		return
	}
	c.W(`<ul class="page-links home-cards">`)
	for _, p := range cards {
		c.W(`<li><a href="`, attr(s.href(p.File)), `"><span class="home-link-title">`, s.ownIcon(p.Node), esc(p.Title), `</span>`)
		if t := s.pageSentence(p); t != "" {
			c.W(`<span class="home-link-text">`, t, `</span>`)
		}
		c.W(`</a></li>`)
	}
	c.W(`</ul>`)
}

// pageSentence is the first sentence of a page's prose in the lens, as
// text without links.
func (s *Site) pageSentence(p *Page) string {
	if p.Node == nil {
		return ""
	}
	return firstSentence(inlineParagraph(s.proseHTML(p.Node)))
}

// linkTagRe matches what a summary inside a link drops: the tags of other
// links, the lens tags after them and hover-card spans.
var linkTagRe = regexp.MustCompile(`<span class="lens-tag">[^<]*</span>|</?a\b[^>]*>|</?span\b[^>]*>`)

// inlineParagraph is the first paragraph of rendered prose with its inline
// code kept and its links reduced to their text, to sit inside a link.
func inlineParagraph(h string) string {
	i := strings.Index(h, "<p>")
	if i < 0 {
		return ""
	}
	j := strings.Index(h[i:], "</p>")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(linkTagRe.ReplaceAllString(h[i+3:i+j], ""))
}

// firstSentence cuts rendered text after its first sentence.
func firstSentence(h string) string {
	depth := 0
	for i := 0; i < len(h); i++ {
		switch h[i] {
		case '<':
			depth++
		case '>':
			depth--
		case '.':
			if depth == 0 && (i+1 == len(h) || h[i+1] == ' ') {
				return h[:i+1]
			}
		}
	}
	return h
}
