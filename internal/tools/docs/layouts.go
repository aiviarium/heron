package docs

import (
	"github.com/aiviarium/heron/internal/core/resolve"
)

// changelog renders a section in the changelog layout: each child section
// is an entry, its title in the margin and its notes beside it, in the
// order written.
func (s *Site) changelog(c *Ctx) error {
	n := c.Node
	c.Prose(n)
	c.W(`<div class="changelog">`)
	for _, ch := range n.Children {
		if !s.shown(ch) {
			continue
		}
		if s.kindPath(ch) != "doc.section" {
			if err := c.Render(ch); err != nil {
				return err
			}
			continue
		}
		d := c.at(ch, 2)
		d.W(`<article class="entry" id="`, attr(Anchor(ch)), `"><div class="entry-head">`)
		d.Heading(2, Anchor(ch), displayName(s, ch), "")
		d.W(`</div><div class="entry-body">`)
		d.Prose(ch)
		if err := d.childrenOrLinks(); err != nil {
			return err
		}
		d.W(`</div></article>`)
	}
	c.W(`</div>`)
	return nil
}

// landing renders a section in the landing layout: its prose as the lead,
// then a tile for each page below it.
func (s *Site) landing(c *Ctx) error {
	n := c.Node
	if h := s.proseHTML(n); h != "" {
		c.W(`<div class="lead prose">`, h, `</div>`)
	}
	p := c.Page
	var rest []*resolve.Node
	if p != nil && len(p.Children) > 0 {
		c.W(`<div class="tiles">`)
		for _, q := range p.Children {
			c.W(`<a class="tile" href="`, attr(s.href(q.File)), `"><span class="tile-title">`, s.nodeIcon(q.Node, "icon"), esc(q.Title), `</span>`)
			if q.Node != nil {
				if t := inlineParagraph(s.proseHTML(q.Node)); t != "" {
					c.W(`<span class="tile-text">`, t, `</span>`)
				}
			}
			c.W(`</a>`)
		}
		c.W(`</div>`)
	}
	for _, ch := range n.Children {
		if s.shown(ch) && (p == nil || s.pageOf[ch] == nil) {
			rest = append(rest, ch)
		}
	}
	for _, ch := range rest {
		if err := c.Render(ch); err != nil {
			return err
		}
	}
	return nil
}
