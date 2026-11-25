package docs

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// Each lens orders its navigation its own way: TREE keeps the node tree,
// TOPIC makes each top-level section a labelled group of its pages, and
// TASK puts the authored pages first, entry pages before the rest, then
// the reference as TOPIC does.

// authoredGroups are the authored kinds in the order TASK lists them,
// with the catalogue key of each group's label.
var authoredGroups = []struct{ kind, key string }{
	{"docs.guide", "docs.nav.guides"},
	{"docs.tutorial", "docs.nav.tutorials"},
	{"docs.howto", "docs.nav.howtos"},
	{"docs.concept", "docs.nav.concepts"},
}

// nav is the site's navigation strategy.
func (s *Site) nav() lens.Nav {
	if s.Opts.Lens == nil {
		return lens.NavTree
	}
	return s.Opts.Lens.Nav
}

// applyNav reorders the docs area for the lens's strategy.
func (s *Site) applyNav(docs *Area) {
	switch s.nav() {
	case lens.NavTopic:
		docs.Groups = append(s.topicGroups(docs.Pages), docs.Groups...)
		docs.Pages = nil
	case lens.NavTask:
		groups := s.taskGroups(docs)
		var rest []*Page
		for _, p := range docs.Pages {
			if p != s.Home {
				rest = append(rest, p)
			}
		}
		if len(rest) > 0 {
			groups = append(groups, NavGroup{Title: label("docs.nav.reference"), Pages: rest})
		}
		docs.Groups = append(groups, docs.Groups...)
		docs.Pages = nil
	}
}

// topicGroups turns top-level pages into groups: a page with pages below
// it becomes a group labelled with its title, linking to it, and runs of
// pages without any become a group with no label.
func (s *Site) topicGroups(pages []*Page) []NavGroup {
	var out []NavGroup
	var loose []*Page
	flush := func() {
		if len(loose) > 0 {
			out = append(out, NavGroup{Pages: loose})
			loose = nil
		}
	}
	for _, p := range pages {
		if p == s.Home {
			continue
		}
		if len(p.Children) == 0 {
			loose = append(loose, p)
			continue
		}
		flush()
		out = append(out, NavGroup{Title: p.Title, Head: p, Pages: p.Children})
	}
	flush()
	return out
}

// taskGroups takes the authored pages out of the tree's order: the lens's
// entry pages first, then each authored kind's pages.
func (s *Site) taskGroups(docs *Area) []NavGroup {
	var authored []*Page
	var walk func(ps []*Page) []*Page
	walk = func(ps []*Page) []*Page {
		var keep []*Page
		for _, p := range ps {
			p.Children = walk(p.Children)
			if p.Node != nil && s.authored(p.Node) {
				authored = append(authored, p)
				continue
			}
			keep = append(keep, p)
		}
		return keep
	}
	docs.Pages = walk(docs.Pages)
	for _, p := range authored {
		p.Parent = nil
	}
	var groups []NavGroup
	taken := map[*Page]bool{}
	if l := s.Opts.Lens; l != nil && len(l.Entry) > 0 {
		g := NavGroup{Title: label("docs.nav.start")}
		for _, n := range l.Entry {
			if p := s.pageOf[n]; p != nil && !taken[p] {
				g.Pages = append(g.Pages, p)
				taken[p] = true
				if !s.authored(n) {
					// A section as an entry page leaves its place too.
					docs.Pages = removePage(docs.Pages, p)
				}
			}
		}
		if len(g.Pages) > 0 {
			groups = append(groups, g)
		}
	}
	for _, ag := range authoredGroups {
		g := NavGroup{Title: label(ag.key)}
		for _, p := range authored {
			if !taken[p] && s.kindPath(p.Node) == ag.kind {
				g.Pages = append(g.Pages, p)
				taken[p] = true
			}
		}
		if len(g.Pages) > 0 {
			groups = append(groups, g)
		}
	}
	for _, p := range authored {
		// An authored kind of another package's.
		if !taken[p] {
			groups = append(groups, NavGroup{Title: p.Title, Pages: []*Page{p}})
		}
	}
	return groups
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

// homeTiles writes a lens's home page below the root's prose: TASK shows
// a tile per entry page, or for its first three authored pages; TOPIC a
// tile per top-level section.
func (s *Site) homeTiles(c *Ctx) {
	var area *Area
	for _, a := range s.Areas {
		if a.Key == "docs" {
			area = a
		}
	}
	if area == nil {
		return
	}
	if s.nav() == lens.NavTask {
		var pages []*Page
		if l := s.Opts.Lens; l != nil {
			for _, n := range l.Entry {
				if p := s.pageOf[n]; p != nil {
					pages = append(pages, p)
				}
			}
		}
		if len(pages) == 0 {
			for _, p := range s.Pages {
				if s.isAuthoredPage(p) && len(pages) < 3 {
					pages = append(pages, p)
				}
			}
		}
		s.tiles(c, pages)
		return
	}
	var topics []*Page
	for _, g := range area.Groups {
		if g.Head != nil {
			topics = append(topics, g.Head)
		} else {
			topics = append(topics, g.Pages...)
		}
	}
	s.tiles(c, topics)
}

// tiles writes a grid of tiles, one per page, each with the first
// sentence of the page's prose.
func (s *Site) tiles(c *Ctx, pages []*Page) {
	if len(pages) == 0 {
		return
	}
	c.W(`<div class="tiles">`)
	for _, q := range pages {
		c.W(`<a class="tile" href="`, attr(s.href(q.File)), `"><span class="tile-title">`, esc(q.Title), `</span>`)
		if q.Node != nil {
			if t := firstSentence(firstParagraph(s.proseHTML(q.Node))); t != "" {
				c.W(`<span class="tile-text">`, t, `</span>`)
			}
		}
		c.W(`</a>`)
	}
	c.W(`</div>`)
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

// isAuthoredPage reports whether a page is an authored node's.
func (s *Site) isAuthoredPage(p *Page) bool { return p != nil && p.Node != nil && s.authored(p.Node) }

// pageNodes writes, for TREE, the nodes the open page shows that have an
// anchor, nested as the tree nests them.
func (s *Site) pageNodes(b *strings.Builder, p *Page) {
	if s.nav() != lens.NavTree || p.Node == nil || s.Opts.Lens == nil {
		return
	}
	var walk func(n *resolve.Node, depth int) string
	walk = func(n *resolve.Node, depth int) string {
		var out strings.Builder
		for _, ch := range n.Children {
			if ch.Name == "" || !own(ch) || ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue || s.anchorOf[ch] != p || s.pageOf[ch] != nil {
				continue
			}
			out.WriteString(`<li><a href="#` + attr(Anchor(ch)) + `"><span class="t">` + esc(displayName(s, ch)) + `</span></a>`)
			if depth < 2 {
				if inner := walk(ch, depth+1); inner != "" {
					out.WriteString(`<ul>` + inner + `</ul>`)
				}
			}
			out.WriteString(`</li>`)
		}
		return out.String()
	}
	if h := walk(p.Node, 0); h != "" {
		b.WriteString(`<ul class="nav-nodes">` + h + `</ul>`)
	}
}
