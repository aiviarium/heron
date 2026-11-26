package docs

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// RenderFunc renders the node a context is positioned on.
type RenderFunc func(c *Ctx) error

// builtins are the renderers of the standard kinds, registered under the
// kinds' paths by Register and used directly when a site has no registry.
var builtins map[string]RenderFunc

func init() {
	builtins = map[string]RenderFunc{
		"doc.section":     renderSection,
		"doc.term":        renderTerm,
		"spec.rule":       renderRule,
		"spec.states":     renderStates,
		"spec.quality":    renderQuality,
		"spec.metric":     renderMetric,
		"spec.guidance":   renderLabelled("docs.label.guidance"),
		"spec.outOfScope": renderLabelled("docs.label.out_of_scope"),
		"spec.open":       renderOpen,
		"spec.project":    renderProject,
		"types.api":       renderAPI,
		"types.op":        renderOp,
		"types.type":      renderType,
		"types.enum":      renderType,
		"types.union":     renderType,
		"types.interface": renderType,
		"core.task":       renderTask,
		"spec.transition": renderTransition,
	}
	addProductRenderers()
	addTechRenderers()
	addProjectRenderers()
	addAuthoredRenderers()
}

// BuiltinKinds lists the kinds with a built-in renderer.
func BuiltinKinds() []string {
	var out []string
	for k := range builtins {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Builtin returns a built-in renderer for registering it under another
// kind, or nil.
func Builtin(kind string) RenderFunc { return builtins[kind] }

func (s *Site) renderer(kind string) RenderFunc {
	if kind == "" {
		return nil
	}
	if r := s.Opts.Registry; r != nil {
		if x := r.Renderer(kind); x != nil {
			if x.Render == nil {
				return s.plainRenderer(x)
			}
			return func(c *Ctx) error { return x.Render(c) }
		}
	}
	return builtins[kind]
}

// registerRenderers registers the built-in renderers on a registry.
func registerRenderers(r *registry.Registry) {
	for _, k := range BuiltinKinds() {
		f := builtins[k]
		if r.Renderer(k) != nil {
			continue
		}
		if err := r.SetRenderer(registry.Renderer{Kind: k, Name: "docs", Render: func(ctx any) error { return f(ctx.(*Ctx)) }}); err != nil {
			panic(err)
		}
	}
}

// title writes the node's heading unless it is the page's own node, whose
// title the layout writes.
func (c *Ctx) title(extra string) {
	if c.Page != nil && c.Page.Node == c.Node {
		return
	}
	if !strings.Contains(extra, `class="kind"`) {
		extra += c.Site.kindTag(c.Node)
	}
	c.Heading(c.Level, Anchor(c.Node), displayName(c.Site, c.Node), extra)
}

// isPage reports whether the context renders its page's own node.
func (c *Ctx) isPage() bool { return c.Page != nil && c.Page.Node == c.Node }

// renderGeneric is the view of a kind without a renderer: its title, its
// prose, its fields and its children.
func renderGeneric(c *Ctx) error {
	n := c.Node
	kind := c.Site.kindPath(n)
	if n.Name == "" && len(n.Children) == 0 && len(n.Prose) > 0 {
		c.W(`<div class="node node-anon" data-kind="`, attr(kind), `">`)
		c.Prose(n)
		c.W(`</div>`)
		return nil
	}
	c.W(`<section class="node" id="`, attr(Anchor(n)), `" data-kind="`, attr(kind), `">`)
	extra := ""
	if k := c.Site.Checker.KindOf(n); k != nil && n.Name != "" {
		extra = ` <span class="kind">` + esc(k.Name) + `</span>`
	}
	if n.Name != "" {
		c.title(extra)
	}
	c.Prose(n)
	c.fields(n)
	c.dependencyGraph(n)
	if err := c.childrenOrLinks(); err != nil {
		return err
	}
	c.W(`</section>`)
	return nil
}

// fields writes the values set on a node as a two-column list.
func (c *Ctx) fields(n *resolve.Node) {
	var rows []*resolve.Node
	for _, ch := range n.Children {
		if (ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue) && ch.Value != nil && proseValue(ch.Value.Syntax()) == nil {
			rows = append(rows, ch)
		}
	}
	if len(rows) == 0 {
		return
	}
	c.W(`<dl class="facts">`)
	for _, r := range rows {
		c.W(`<div class="fact"><dt>`, esc(sentenceCase(r.Name)), `</dt><dd><code>`, c.Site.exprHTML(r.Value, r.ValueSite), `</code></dd></div>`)
	}
	c.W(`</dl>`)
	for _, ch := range n.Children {
		if (ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue) && ch.Value != nil && proseValue(ch.Value.Syntax()) != nil {
			c.W(`<div class="field-prose"><div class="label">`, esc(sentenceCase(ch.Name)), `</div>`)
			c.Prose(ch)
			c.W(`</div>`)
		}
	}
}

// childrenOrLinks renders the children, with those that have pages of
// their own as links.
func (c *Ctx) childrenOrLinks() error {
	var links []*resolve.Node
	flushLinks := func() {
		if len(links) == 0 {
			return
		}
		// Each page below, by its title and the first sentence of its
		// prose, as the home page lists its groups.
		c.W(`<ul class="page-links">`)
		for _, l := range links {
			p := c.Site.pageOf[l]
			c.W(`<li><a href="`, attr(c.Site.href(p.File)), `"><span class="home-link-title">`, esc(p.Title), `</span>`)
			if t := c.Site.pageSentence(p); t != "" {
				c.W(`<span class="home-link-text">`, t, `</span>`)
			}
			c.W(`</a></li>`)
		}
		c.W(`</ul>`)
		links = nil
	}
	headed, summing := false, false
	for _, ch := range c.Site.inOrder(c.Node.Children) {
		if !c.Site.shown(ch) {
			continue
		}
		if p := c.Site.pageOf[ch]; p != nil && p != c.Page {
			links = append(links, ch)
			continue
		}
		flushLinks()
		if c.Site.summarized(ch) && !summing {
			summing = true
			if headed && c.Node != nil {
				// The summary lines follow a part with a heading of its
				// own; their heading says they're this node's again.
				c.Heading(c.Level+1, Anchor(c.Node)+".rules", label("docs.heading.rules"), "")
			}
		}
		if k := c.Site.kindPath(ch); ch.Name != "" && (k == "doc.section" || k == "" && ch.Shape == resolve.ShapeScoped) {
			headed = true
		}
		if err := c.Render(ch); err != nil {
			return err
		}
	}
	flushLinks()
	return nil
}

func renderSection(c *Ctx) error {
	n := c.Node
	c.W(`<section class="section" id="`, attr(Anchor(n)), `">`)
	c.title("")
	c.Prose(n)
	if err := c.childrenOrLinks(); err != nil {
		return err
	}
	c.W(`</section>`)
	return nil
}

func renderTerm(c *Ctx) error {
	n := c.Node
	c.W(`<div class="term" id="`, attr(Anchor(n)), `">`)
	c.W(`<dt class="term-name"><a class="anchor" href="#`, attr(Anchor(n)), `">`, esc(termName(c.Site, n)), `</a>`, c.Site.kindTag(n))
	if al := termAliases(n); len(al) > 0 {
		c.W(`<span class="aliases">`, esc(strings.Join(al, ", ")), `</span>`)
	}
	c.W(`</dt><dd>`)
	c.Prose(n)
	c.W(`</dd></div>`)
	return nil
}

// termName is the word a term defines: its first alias when its name is
// an identifier only, as `api_kind` defines "api".
func termName(s *Site, n *resolve.Node) string {
	if a := annotation(s, n, "display"); a != nil {
		return displayName(s, n)
	}
	al := rawAliases(n)
	if strings.Contains(n.Name, "_") && len(al) > 0 && !strings.Contains(al[0], "(") {
		return al[0]
	}
	return strings.ReplaceAll(n.Name, "_", " ")
}

// termAliases are a term's aliases other than the word it's shown as.
func termAliases(n *resolve.Node) []string {
	raw := rawAliases(n)
	name := strings.ReplaceAll(n.Name, "_", " ")
	var out []string
	for i, a := range raw {
		if a == name || strings.Contains(n.Name, "_") && i == 0 && !strings.Contains(a, "(") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func rawAliases(n *resolve.Node) []string {
	e := namedArg(kindArgs(n), "aliases")
	if e == nil {
		return nil
	}
	var out []string
	e.Syntax().Walk(func(x *syntax.Node) bool {
		if v, ok := resolve.StringValue(x); ok && x.Kind == syntax.StringLit {
			out = append(out, v)
			return false
		}
		return true
	})
	return out
}

func renderLabelled(key string) RenderFunc {
	return func(c *Ctx) error {
		n := c.Node
		c.W(`<aside class="callout" id="`, attr(Anchor(n)), `"><div class="label">`, esc(label(key)), `</div>`)
		if ts := c.Site.targets(n); len(ts) > 0 {
			c.targetsLine(ts)
		}
		c.Prose(n)
		if err := c.Children(); err != nil {
			return err
		}
		c.W(`</aside>`)
		return nil
	}
}

// targetsLine writes the nodes a requirement or note is about.
func (c *Ctx) targetsLine(ts []*resolve.Node) {
	c.W(`<div class="targets">`)
	for i, t := range ts {
		if i > 0 {
			c.W(`<span class="sep">, </span>`)
		}
		c.W(c.Site.linkHTML(t, `<code>`+esc(c.refText(t))+`</code>`, ""))
	}
	c.W(`</div>`)
}

// refText is how a reference to a node reads in code: its path from the
// nearest ancestor that has a page, such as `store.place_order`.
func refText(n *resolve.Node) string {
	return n.Path()
}

// refText is a reference as read on the current page: a term by its word,
// anything else by its path below the page's node.
func (c *Ctx) refText(n *resolve.Node) string {
	if c.Site.kindPath(n) == "doc.term" {
		return termName(c.Site, n)
	}
	if n.Name == "" {
		return displayName(c.Site, n)
	}
	p := n.Path()
	if c.Page != nil && c.Page.Node != nil && c.Page.Node.Parent != nil {
		if pre := c.Page.Node.Path() + "."; strings.HasPrefix(p, pre) {
			return strings.TrimPrefix(p, pre)
		}
	}
	return p
}

func renderOpen(c *Ctx) error {
	n := c.Node
	c.W(`<aside class="callout callout-open" id="`, attr(Anchor(n)), `"><div class="label">`, esc(label("docs.label.open")))
	if o, ok := argString(kindArgs(n), -1, "owner"); ok {
		c.W(` <span class="owner">`, esc(o), `</span>`)
	}
	c.W(`</div>`)
	if ts := c.Site.targets(n); len(ts) > 0 {
		c.targetsLine(ts)
	}
	c.Prose(n)
	c.W(`</aside>`)
	return nil
}

func renderProject(c *Ctx) error {
	n := c.Node
	c.W(`<section class="node project" id="`, attr(Anchor(n)), `">`)
	extra := ""
	if p := projectDir(c.Site.Tree, n); p != "" {
		extra = ` <code class="path">` + esc(p) + `</code>`
	}
	c.title(extra)
	c.Prose(n)
	c.dependencyGraph(n)
	for _, ch := range n.Children {
		if c.Site.kindPath(ch) == "core.task" {
			if err := c.Render(ch); err != nil {
				return err
			}
		}
	}
	c.W(`</section>`)
	return nil
}

// projectDir is a project's path relative to the tree's entry point, or ""
// for a project at the tree's root or above it, whose path says nothing a
// reader needs, such as "." or "..".
func projectDir(t *resolve.Tree, n *resolve.Node) string {
	p := resolve.KindArg(n, "path")
	if p == "" {
		return ""
	}
	rel := filepath.ToSlash(filepath.Clean(p))
	if n.Decl.File != nil && t != nil && t.Entry != nil && !n.Decl.File.ID.IsStd() {
		if r, err := filepath.Rel(t.Entry.ID.Dir(), filepath.Join(n.Decl.File.ID.Dir(), p)); err == nil {
			rel = filepath.ToSlash(r)
		}
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	return rel
}

func renderTask(c *Ctx) error {
	n := c.Node
	c.W(`<section class="node task" id="`, attr(Anchor(n)), `">`)
	c.title("")
	if n.Decl.N != nil {
		c.W(`<pre class="sig"><code>`, c.Site.signature(n), `</code></pre>`)
	}
	c.Prose(n)
	c.W(`</section>`)
	return nil
}
