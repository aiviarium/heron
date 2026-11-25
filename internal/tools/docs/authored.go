package docs

import (
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// The authored kinds of std/docs (guide, howto, concept and tutorial)
// are pages written for a lens's readers. Each renders as a page: its
// prose, its examples as code blocks titled by their names, and a how-to's
// or a tutorial's steps as one numbered list.

func addAuthoredRenderers() {
	for _, k := range []string{"docs.guide", "docs.howto", "docs.concept", "docs.tutorial"} {
		builtins[k] = renderAuthored
	}
	builtins["docs.step"] = renderStep
	builtins["spec.example"] = renderExample
}

func renderAuthored(c *Ctx) error {
	n := c.Node
	c.W(`<article class="authored" id="`, attr(Anchor(n)), `" data-kind="`, attr(c.Site.kindPath(n)), `">`)
	c.title("")
	if err := c.stepsAndChildren(); err != nil {
		return err
	}
	c.W(`</article>`)
	return nil
}

// stepsAndChildren renders the prose blocks and the children in the
// order they're written, with each run of steps as one numbered list.
func (c *Ctx) stepsAndChildren() error {
	s := c.Site
	n := c.Node
	type item struct {
		at    int
		prose *resolve.Site
		node  *resolve.Node
	}
	var items []item
	for _, d := range n.Docs {
		d := d
		if s.kept(n) {
			items = append(items, item{at: -1, prose: &d})
		}
	}
	for i := range n.Prose {
		p := n.Prose[i]
		if !s.runShown(n, p) {
			continue
		}
		start, _ := p.Span()
		items = append(items, item{at: start, prose: &p})
	}
	for _, ch := range n.Children {
		if !s.shown(ch) {
			continue
		}
		start, _ := ch.Decl.Span()
		if ch.Decl.File != nil && len(n.Prose) > 0 && n.Prose[0].File != ch.Decl.File {
			start = 1 << 30
		}
		items = append(items, item{at: start, node: ch})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at < items[j].at })
	open := false
	for _, it := range items {
		isStep := it.node != nil && s.kindPath(it.node) == "docs.step"
		if isStep && !open {
			c.W(`<ol class="steps">`)
			open = true
		}
		if !isStep && open {
			c.W(`</ol>`)
			open = false
		}
		if it.prose != nil {
			if h := s.renderText([]resolve.Site{*it.prose}); h != "" {
				c.W(`<div class="prose">`, s.markTags(lens.ProseRestriction(s.Checker, *it.prose)), h, `</div>`)
			}
			continue
		}
		ch := it.node
		if p := s.pageOf[ch]; p != nil && p != c.Page {
			c.W(`<ul class="page-links"><li>`)
			c.Link(ch, displayName(s, ch))
			c.W(`</li></ul>`)
			continue
		}
		if err := c.Render(ch); err != nil {
			return err
		}
	}
	if open {
		c.W(`</ol>`)
	}
	return nil
}

func renderStep(c *Ctx) error {
	n := c.Node
	c.W(`<li class="step" id="`, attr(Anchor(n)), `">`)
	c.Heading(c.Level, Anchor(n), displayName(c.Site, n), "")
	if err := c.stepsAndChildren(); err != nil {
		return err
	}
	c.W(`</li>`)
	return nil
}

// renderExample writes an example as its code, the first code block
// titled with the example's name; prose around the code stays prose.
func renderExample(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<div class="example" id="`, attr(Anchor(n)), `">`)
	title := ""
	if n.Name != "" {
		title = displayName(s, n)
	}
	h := s.proseHTMLTitled(n, title)
	if !strings.Contains(h, `class="code"`) && n.Name != "" {
		// An example in words: its name leads it.
		c.W(`<p class="example-name">`, esc(title), `</p>`)
	}
	if h != "" {
		c.W(`<div class="prose">`, h, `</div>`)
	}
	if err := c.Children(); err != nil {
		return err
	}
	c.W(`</div>`)
	return nil
}

// proseHTMLTitled renders a node's prose with its first code block titled.
func (s *Site) proseHTMLTitled(n *resolve.Node, title string) string {
	s.fenceTitle = title
	defer func() { s.fenceTitle = "" }()
	return s.proseHTML(n)
}
