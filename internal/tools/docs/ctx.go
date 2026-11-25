package docs

import (
	"fmt"
	"html"
	"strings"

	"github.com/aiviarium/heron/internal/core/resolve"
)

// Ctx is what a renderer receives: the site, the node it renders and the
// page it's on, and the output. Renderers write HTML through it and use its
// helpers so links, headings and the outline stay consistent.
type Ctx struct {
	Site *Site
	Node *resolve.Node
	Page *Page
	// Level is the heading level the node's title takes (2 on a page whose
	// node is its parent, deeper below).
	Level int

	// Tag is set while a markup tag renders through a registered renderer.
	Tag *TagCall

	anchorSuffix string
	out          *strings.Builder
	outline      *[]Heading
	islands      map[string]bool
	aside        *strings.Builder
	seen         map[*resolve.Node]bool
}

// Heading is an entry of a page's outline.
type Heading struct {
	ID    string
	Title string
	Level int
}

// Fragment is rendered HTML with what it needs: the islands to load and
// the outline of its headings.
type Fragment struct {
	HTML    string
	Aside   string // the right column (an op's examples), if any
	Outline []Heading
	Islands []string
}

func (s *Site) newCtx(n *resolve.Node, p *Page, level int) *Ctx {
	var ol []Heading
	return &Ctx{Site: s, Node: n, Page: p, Level: level, out: &strings.Builder{}, outline: &ol, islands: map[string]bool{}, aside: &strings.Builder{}, seen: map[*resolve.Node]bool{}}
}

func (c *Ctx) fragment() *Fragment {
	f := &Fragment{HTML: c.out.String(), Aside: c.aside.String(), Outline: *c.outline}
	for k := range c.islands {
		f.Islands = append(f.Islands, k)
	}
	return f
}

// W writes raw HTML.
func (c *Ctx) W(parts ...string) {
	for _, p := range parts {
		c.out.WriteString(p)
	}
}

// Wf writes formatted raw HTML.
func (c *Ctx) Wf(format string, args ...any) { fmt.Fprintf(c.out, format, args...) }

// Text writes text from the spec, escaped.
func (c *Ctx) Text(s string) { c.out.WriteString(esc(s)) }

// Label writes a catalogue string, escaped.
func (c *Ctx) Label(key string, args ...string) { c.out.WriteString(esc(label(key, args...))) }

// Island marks an island the page needs.
func (c *Ctx) Island(name string) { c.islands[name] = true }

// Aside writes HTML into the right column.
func (c *Ctx) Aside(h string) { c.aside.WriteString(h) }

// Render renders a node through the renderer of its kind, one level below
// the current one.
func (c *Ctx) Render(n *resolve.Node) error {
	return c.at(n, c.Level+1).render()
}

// at returns a context for another node on the same page.
func (c *Ctx) at(n *resolve.Node, level int) *Ctx {
	d := *c
	d.Node, d.Level = n, level
	return &d
}

func (c *Ctx) render() error {
	n := c.Node
	if c.seen[n] {
		return nil
	}
	c.seen[n] = true
	defer delete(c.seen, n)
	kind := c.Site.kindPath(n)
	c.Slot("before")
	var err error
	if r := c.Site.renderer(kind); r != nil {
		err = r(c)
	} else {
		err = renderGeneric(c)
	}
	c.Slot("after")
	return err
}

// Slot writes the components a docs block's `slot(name, of = <kind>)`
// gives the current node's kind. Every node has "before" and "after",
// which render places around it; a renderer places any other name.
func (c *Ctx) Slot(name string) {
	if c.Node == nil {
		return
	}
	c.W(c.Site.slotHTML(c, name, c.Site.kindPath(c.Node)))
}

// Children renders every child of the current node that the docs show,
// in order.
func (c *Ctx) Children() error {
	for _, ch := range c.Node.Children {
		if !c.Site.shown(ch) {
			continue
		}
		if err := c.Render(ch); err != nil {
			return err
		}
	}
	return nil
}

// shown reports whether a child renders as a node of its own.
func (s *Site) shown(n *resolve.Node) bool {
	if !own(n) && !s.inConfig(n) {
		return false
	}
	switch n.Shape {
	case resolve.ShapeSet, resolve.ShapeValue:
		return false
	}
	return true
}

// Heading writes a heading with an anchor and adds it to the outline.
func (c *Ctx) Heading(level int, id, title string, extra string) {
	if level < 2 {
		level = 2
	}
	if level > 6 {
		level = 6
	}
	if level <= 3 {
		*c.outline = append(*c.outline, Heading{ID: id, Title: title, Level: level})
	}
	idAttr := ` id="` + attr(id) + `"`
	if c.Node != nil && id == Anchor(c.Node) {
		// The node's wrapper carries the id.
		idAttr = ""
	}
	c.Wf(`<h%d%s class="h"><a class="anchor" href="#%s">%s</a>%s</h%d>`, level, idAttr, attr(id), esc(title), extra, level)
}

// Link writes a link to a node with the given text (escaped), carrying a
// hover card where the node has one.
func (c *Ctx) Link(n *resolve.Node, text string) {
	c.W(c.Site.linkHTML(n, esc(text), ""))
}

func (s *Site) linkHTML(n *resolve.Node, inner, class string) string {
	u := s.URL(n)
	cls := "ref"
	if class != "" {
		cls += " " + class
	}
	switch {
	case s.kindPath(n) == "doc.term":
		cls += " ref-term"
	case s.isTypeDecl(n):
		cls += " ref-type"
	}
	card := ""
	if s.hasCard(n) {
		card = ` data-card="` + attr(n.Path()) + `"`
	}
	if u == "" {
		return `<span class="` + cls + `"` + card + `>` + inner + `</span>`
	}
	return `<a class="` + cls + `" href="` + attr(u) + `"` + card + `>` + inner + `</a>`
}

// Prose writes a node's own documentation and prose as HTML.
func (c *Ctx) Prose(n *resolve.Node) bool {
	h := c.Site.proseHTML(n)
	if h == "" {
		return false
	}
	c.W(`<div class="prose">`, h, `</div>`)
	return true
}

func esc(s string) string { return html.EscapeString(s) }

func attr(s string) string { return html.EscapeString(s) }
