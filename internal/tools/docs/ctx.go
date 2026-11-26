package docs

import (
	"fmt"
	"html"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
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
	if !c.Site.kept(n) {
		err = renderContainer(c)
	} else if c.Site.summarized(n) && kind != "spec.rule" && kind != "spec.quality" && !c.isPage() {
		err = c.summary()
	} else if r := c.Site.renderer(kind); r != nil {
		err = r(c)
	} else if t := c.Site.tagRenderer(kind); t != nil && n.Name == "" && strings.HasPrefix(kind, "doc.") {
		// An anonymous std/doc component, as a callout written as a node
		// or an element on lines of its own, renders as its tag does.
		h, _ := t(firstBlockless(c.Site.proseHTML(n)), c.Site.nodeAttrs(n))
		c.W(h)
	} else {
		err = renderGeneric(c)
	}
	c.Slot("after")
	return err
}

// renderContainer writes a node the lens leaves out but that holds nodes
// it keeps: its heading, as their place, and those nodes, without its own
// prose but for runs marked for the lens.
func renderContainer(c *Ctx) error {
	n := c.Node
	c.W(`<section class="section" id="`, attr(Anchor(n)), `">`)
	if n.Name != "" {
		c.title("")
	}
	c.Prose(n)
	if err := c.childrenOrLinks(); err != nil {
		return err
	}
	c.W(`</section>`)
	return nil
}

// inOrder is a node's children as a lens other than explore renders
// them: the nodes that are none of the others, such as examples, APIs and
// diagrams; then its rules; then its terms; then its sections; then its
// diagnostics last; each group in the tree's order. Explore keeps the
// tree's order.
func (s *Site) inOrder(children []*resolve.Node) []*resolve.Node {
	if s.explore() {
		return children
	}
	var groups [5][]*resolve.Node
	for _, ch := range children {
		g := 0
		switch k := s.kindPath(ch); {
		case k == "spec.rule" || k == "spec.quality" || k == "spec.guidance" || s.summarized(ch):
			g = 1
		case k == "doc.term":
			g = 2
		case k == "tech.diagnostic" || s.isContainer(ch) && s.onlyDiagnostics(ch):
			g = 4
		case s.isContainer(ch):
			g = 3
		}
		groups[g] = append(groups[g], ch)
	}
	var out []*resolve.Node
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// errorsGathered reports whether the lens shows diagnostics on its
// diagnostics page alone, as the user lens does.
func (s *Site) errorsGathered() bool {
	return s.Opts.Lens != nil && s.Opts.Lens.Name == "user"
}

// gatheredDiagnostic reports whether a node is a diagnostic, or a section
// of diagnostics only, that the lens shows on its diagnostics page alone.
func (s *Site) gatheredDiagnostic(n *resolve.Node) bool {
	if !s.errorsGathered() {
		return false
	}
	return s.kindPath(n) == "tech.diagnostic" || s.isContainer(n) && s.onlyDiagnostics(n)
}

// onlyDiagnostics reports whether a section holds diagnostics and nothing
// else the lens shows.
func (s *Site) onlyDiagnostics(n *resolve.Node) bool {
	any := false
	for _, ch := range n.Children {
		if ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue || !s.placed(ch) {
			continue
		}
		if s.kindPath(ch) != "tech.diagnostic" {
			return false
		}
		any = true
	}
	return any
}

// summary writes a node a lens sums up: its title and first sentence,
// with the node in full behind them.
func (c *Ctx) summary() error {
	n := c.Node
	s := c.Site
	head := esc(displayName(s, n))
	if t := firstSentence(inlineParagraph(s.proseHTML(n))); t != "" {
		head = `<strong>` + head + `</strong> ` + t
	}
	c.W(`<details class="node-summary" id="`, attr(Anchor(n)), `"><summary>`, head, `</summary>`)
	d := c.at(n, c.Level)
	var err error
	if r := s.renderer(s.kindPath(n)); r != nil {
		err = r(d)
	} else {
		err = renderGeneric(d)
	}
	c.W(`</details>`)
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
	for _, ch := range c.Site.inOrder(c.Node.Children) {
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
	if n == s.Config.Node && n != nil {
		// The docs block configures the site; it isn't content of the
		// page it's written on.
		return false
	}
	if !own(n) && !s.inConfig(n) {
		return false
	}
	if s.hidden(n) || s.gatheredDiagnostic(n) {
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
	return s.link(n, inner, class, false)
}

// proseLinkHTML is a link written in prose: one that leads to another
// lens names that lens after its text.
func (s *Site) proseLinkHTML(n *resolve.Node, inner string) string {
	return s.link(n, inner, "", true)
}

func (s *Site) link(n *resolve.Node, inner, class string, tagged bool) string {
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
	if tagged {
		if t := s.lensTag(n); t != "" && s.specURL(n) == "" {
			inner += ` <span class="lens-tag">` + esc(t) + `</span>`
		}
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

// firstBlockless is rendered prose without its outer paragraph, as a tag's
// body is.
func firstBlockless(h string) string {
	h = strings.TrimSpace(h)
	if strings.HasPrefix(h, "<p>") && strings.HasSuffix(h, "</p>") && strings.Count(h, "<p>") == 1 {
		return h[3 : len(h)-4]
	}
	return h
}

// nodeAttrs are a node's string arguments by name, as a tag's attributes.
func (s *Site) nodeAttrs(n *resolve.Node) map[string]string {
	out := map[string]string{}
	args := kindArgs(n)
	if args.N == nil {
		return out
	}
	for _, a := range args.Args() {
		if na, ok := a.(ast.NamedArg); ok && na.Ident() != nil {
			if v, ok := resolve.StringValue(na.Expr().Syntax()); ok {
				out[na.Ident().Text] = v
			} else {
				out[na.Ident().Text] = na.Expr().Syntax().Text()
			}
		}
	}
	return out
}
