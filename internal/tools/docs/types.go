package docs

import (
	"github.com/aiviarium/heron/internal/core/jsonnames"
	"path/filepath"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// texpr is a type as written, resolved: its declaration, its arguments,
// and the variants of a union.
type texpr struct {
	decl     *resolve.Node
	name     string
	args     []texpr
	optional bool // T?, nullable
	union    []texpr
	text     string // as written
	html     string // as written, names linked
}

func (s *Site) readType(t ast.Type, site resolve.Site, env map[string]texpr) texpr {
	switch t := t.(type) {
	case ast.NamedType:
		segs := resolve.PathSegs(t.Path().N)
		if len(segs) == 1 && env != nil {
			if b, ok := env[segs[0]]; ok {
				return b
			}
		}
		x := texpr{name: strings.Join(segs, "."), text: strings.TrimSpace(t.N.Text()), html: s.typeHTML(t, site)}
		x.decl = s.refTarget(t.Path().N, site)
		for _, a := range t.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				x.args = append(x.args, s.readTypeExpr(pa.Expr(), site, env))
			}
		}
		return x
	case ast.OptionalType:
		x := s.readType(t.Type(), site, env)
		x.optional = true
		x.text += "?"
		x.html += `<span class="tk-punct">?</span>`
		return x
	case ast.UnionType:
		l, r := s.readType(t.Lhs(), site, env), s.readType(t.Rhs(), site, env)
		x := texpr{text: strings.TrimSpace(t.N.Text()), html: s.typeHTML(t, site)}
		for _, v := range []texpr{l, r} {
			if len(v.union) > 0 {
				x.union = append(x.union, v.union...)
			} else {
				x.union = append(x.union, v)
			}
		}
		return x
	case nil:
		return texpr{}
	default:
		return texpr{text: strings.TrimSpace(t.Syntax().Text()), html: s.typeHTML(t, site)}
	}
}

// readTypeExpr reads a type written as an argument, such as the Pet of
// List(Pet) or the Page(Pet) of op(Page(Pet)).
func (s *Site) readTypeExpr(e ast.Expr, site resolve.Site, env map[string]texpr) texpr {
	switch e := e.(type) {
	case ast.PathExpr:
		segs := resolve.PathSegs(e.Path().N)
		if len(segs) == 1 && env != nil {
			if b, ok := env[segs[0]]; ok {
				return b
			}
		}
		return texpr{name: strings.Join(segs, "."), decl: s.refTarget(e.Path().N, site), text: strings.TrimSpace(e.N.Text()), html: s.exprHTML(e, site)}
	case ast.CallExpr:
		x := s.readTypeExpr(e.Callee(), site, env)
		x.text = strings.TrimSpace(e.N.Text())
		x.html = s.exprHTML(e, site)
		for _, a := range e.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				x.args = append(x.args, s.readTypeExpr(pa.Expr(), site, env))
			}
		}
		if env != nil && len(x.args) > 0 {
			x.html = s.substHTML(x)
		}
		return x
	case nil:
		return texpr{}
	default:
		return texpr{text: strings.TrimSpace(e.Syntax().Text()), html: s.exprHTML(e, site)}
	}
}

// substHTML writes a type with its arguments after substitution.
func (s *Site) substHTML(x texpr) string {
	h := esc(x.name)
	if x.decl != nil && own(x.decl) {
		h = s.linkHTML(x.decl, `<span class="tk-type">`+esc(x.name)+`</span>`, "code-ref")
	} else {
		h = `<span class="tk-type">` + h + `</span>`
	}
	if len(x.args) > 0 {
		var as []string
		for _, a := range x.args {
			as = append(as, a.html)
		}
		h += `<span class="tk-punct">(</span>` + strings.Join(as, `<span class="tk-punct">, </span>`) + `<span class="tk-punct">)</span>`
	}
	return h
}

// member is a field of a type, read from its declaration.
type member struct {
	node     *resolve.Node
	name     string
	typ      texpr
	optional bool
	def      ast.Expr
	site     resolve.Site
	from     *resolve.Node // the interface it comes from, if inherited
}

// members returns a type's fields, its own first, then those of the
// interfaces it implements that it doesn't redeclare.
func (s *Site) members(t *resolve.Node, env map[string]texpr) []member {
	if s.kindPath(t) == "types.union" {
		// A union's variants, positional or in its body, by their tags.
		var out []member
		for _, v := range s.Checker.UnionVariants(t) {
			m := member{node: v.Node, name: v.Node.Name, site: s.bodySite(t, v.Node.Decl)}
			if v.Expr != nil {
				m.typ = s.readTypeExpr(v.Expr, t.Decl, env)
			} else {
				m.typ = s.readType(ast.Declare{N: v.Node.Decl.N}.Type(), m.site, env)
			}
			out = append(out, m)
		}
		return out
	}
	var out []member
	seen := map[string]bool{}
	var collect func(t *resolve.Node, from *resolve.Node, depth int)
	collect = func(t *resolve.Node, from *resolve.Node, depth int) {
		if depth > 8 {
			return
		}
		for _, ch := range t.Children {
			if ch.Name == "" || seen[ch.Name] || ch.Decl.N == nil {
				continue
			}
			if k := s.kindPath(ch); k != "" && k != "types.member" {
				continue
			}
			m := member{node: ch, name: ch.Name, site: ch.Decl, from: from}
			switch ch.Decl.N.Kind {
			case syntax.Declare:
				d := ast.Declare{N: ch.Decl.N}
				m.typ = s.readType(d.Type(), s.bodySite(t, ch.Decl), env)
				m.optional = d.Question() != nil
			case syntax.Value:
				v := ast.Value{N: ch.Decl.N}
				m.typ = s.readType(v.Type(), s.bodySite(t, ch.Decl), env)
				m.optional = v.Question() != nil
				m.def = v.Expr()
			default:
				continue
			}
			seen[ch.Name] = true
			out = append(out, m)
		}
		for _, i := range s.Checker.Implements(t) {
			collect(i, i, depth+1)
		}
	}
	collect(t, nil, 0)
	return out
}

// bodySite is the site a member's type is read in.
func (s *Site) bodySite(owner *resolve.Node, site resolve.Site) resolve.Site {
	if owner.Body != nil && site.Scope == nil {
		site.Scope = owner.Body
	}
	return site
}

// typeParams returns the parameters of a generic type.
func (s *Site) typeParams(t *resolve.Node) []ast.Param {
	if t.Decl.N == nil || t.Decl.N.Kind != syntax.Declare {
		return nil
	}
	return ast.Declare{N: t.Decl.N}.Params().Params()
}

// envFor binds a generic type's parameters to the arguments it's used with.
func (s *Site) envFor(x texpr) map[string]texpr {
	if x.decl == nil || len(x.args) == 0 {
		return nil
	}
	ps := s.typeParams(x.decl)
	env := map[string]texpr{}
	for i, p := range ps {
		if i < len(x.args) && p.Ident() != nil {
			env[p.Ident().Text] = x.args[i]
		}
	}
	return env
}

// signature writes a declaration as Heron, without its docs, names linked.
func (s *Site) signature(n *resolve.Node) string {
	return s.highlightDecl(n.Decl, true)
}

// relFile is a file's path relative to the tree's entry directory.
func (s *Site) relFile(f *resolve.File) string {
	if f == nil {
		return ""
	}
	id := string(f.ID)
	if s.Tree.Entry != nil {
		base := filepath.Dir(string(s.Tree.Entry.ID))
		if r, err := filepath.Rel(base, id); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
	}
	return filepath.Base(id)
}

func renderType(c *Ctx) error {
	n := c.Node
	s := c.Site
	kind := s.kindPath(n)
	c.W(`<section class="node type-page" id="`, attr(Anchor(n)), `">`)
	c.title("")
	frame := s.codeFrame("heron", s.relFile(n.Decl.File), s.signature(n), dedent(strings.TrimSpace(n.Decl.N.Text()), declIndent(n.Decl)))
	if !c.protoTabs(n, frame) {
		c.W(frame)
	}
	c.Prose(n)

	// Facts: an error's id, status and the error it extends, a type's
	// interfaces.
	var facts []string
	if s.Checker.IsError(n) {
		facts = append(facts, `<dt>`+esc(label("docs.label.id"))+`</dt><dd><code class="err-code">`+esc(s.Checker.ErrorID(n))+`</code></dd>`)
		if st := s.Checker.ErrorStatus(n); st != "" {
			facts = append(facts, `<dt>`+esc(label("docs.label.status"))+`</dt><dd><code class="status-code">`+esc(st)+`</code> `+esc(s.Checker.ErrorStatusText(n))+`</dd>`)
		}
		if p := s.Checker.ErrorParent(n); p != nil {
			facts = append(facts, `<dt>`+esc(label("docs.label.extends"))+`</dt><dd>`+s.linkHTML(p, `<code>`+esc(p.Name)+`</code>`, "")+`</dd>`)
		}
		if t := s.errorText(n, "title"); t != "" {
			facts = append(facts, `<dt>`+esc(label("docs.label.message"))+`</dt><dd>`+messageHTML(t)+`</dd>`)
		}
	} else if impl := s.Checker.Implements(n); len(impl) > 0 {
		var ls []string
		for _, i := range impl {
			ls = append(ls, s.linkHTML(i, `<code>`+esc(i.Name)+`</code>`, ""))
		}
		facts = append(facts, `<dt>`+esc(label("docs.label.implements"))+`</dt><dd>`+strings.Join(ls, ", ")+`</dd>`)
	}
	if len(facts) > 0 {
		c.W(`<dl class="facts">`, strings.Join(facts, ""), `</dl>`)
	}

	if ps := s.typeParams(n); len(ps) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".params", label("docs.heading.type_params"), "")
		c.W(`<div class="table-wrap"><table class="fields"><thead><tr><th>`, esc(label("docs.col.name")), `</th><th>`, esc(label("docs.col.constraint")), `</th></tr></thead><tbody>`)
		for _, p := range ps {
			name := ""
			if p.Ident() != nil {
				name = p.Ident().Text
			}
			c.W(`<tr><td><code>`, esc(name), `</code></td><td><code>`, s.typeHTML(p.Type(), n.Decl), `</code></td></tr>`)
		}
		c.W(`</tbody></table></div>`)
	}

	switch kind {
	case "types.enum":
		c.Heading(c.Level+1, Anchor(n)+".values", label("docs.heading.values"), "")
		c.W(`<div class="table-wrap"><table class="fields"><thead><tr><th>`, esc(label("docs.col.value")), `</th>`)
		hasDocs := false
		for _, v := range n.Children {
			if s.proseHTML(v) != "" {
				hasDocs = true
			}
		}
		if hasDocs {
			c.W(`<th>`, esc(label("docs.col.description")), `</th>`)
		}
		c.W(`</tr></thead><tbody>`)
		for _, v := range n.Children {
			if v.Name == "" {
				continue
			}
			c.W(`<tr id="`, attr(Anchor(v)), `"><td><code class="value">`, esc(v.Name), `</code></td>`)
			if hasDocs {
				c.W(`<td>`, s.proseHTML(v), `</td>`)
			}
			c.W(`</tr>`)
		}
		c.W(`</tbody></table></div>`)
	case "types.union":
		c.Heading(c.Level+1, Anchor(n)+".variants", label("docs.heading.variants"), "")
		c.fieldsTable(n, s.members(n, nil))
	default:
		if ms := s.members(n, nil); len(ms) > 0 {
			c.Heading(c.Level+1, Anchor(n)+".fields", label("docs.heading.fields"), "")
			c.typeViews(n, ms, frame)
		}
	}

	if kind == "types.interface" {
		c.interfaceMethods(n)
		if impl := s.implementors[n]; len(impl) > 0 {
			c.Heading(c.Level+1, Anchor(n)+".implementors", label("docs.heading.implemented_by"), "")
			c.W(`<ul class="refs">`)
			for _, i := range impl {
				c.W(`<li>`, s.linkHTML(i, `<code>`+esc(i.Name)+`</code>`, ""), `</li>`)
			}
			c.W(`</ul>`)
		}
	}

	// Rules on the type, as its state properties, and its lifecycles.
	var rules, lifecycles []*resolve.Node
	s.Tree.Walk(func(x *resolve.Node) {
		if !own(x) {
			return
		}
		switch s.kindPath(x) {
		case "spec.rule", "spec.quality":
			for _, t := range s.targets(x) {
				if t == n || t.Parent == n {
					rules = append(rules, x)
					break
				}
			}
		case "spec.states":
			if l := s.readLifecycle(x); l.field != nil && l.field.Parent == n {
				lifecycles = append(lifecycles, x)
			}
		}
	})
	if len(rules) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".rules", label("docs.heading.rules"), "")
		for _, r := range rules {
			d := c.at(r, c.Level+2)
			d.anchorSuffix = "--" + Anchor(n)
			if err := d.renderRef(); err != nil {
				return err
			}
		}
	}
	for _, l := range lifecycles {
		c.Heading(c.Level+1, Anchor(l)+"--"+Anchor(n), displayName(s, l), "")
		c.lifecycleView(s.readLifecycle(l))
	}

	c.usedBy(n)
	c.W(`</section>`)
	return nil
}

// renderRef renders a node shown away from its own place, as the rules on
// a type page: same view, anchors suffixed so ids stay unique, and its
// name linking to its home.
func (c *Ctx) renderRef() error {
	n := c.Node
	s := c.Site
	c.W(`<div class="rule" id="`, attr(Anchor(n)+c.anchorSuffix), `">`)
	sent := s.fullSentence(n)
	if sent == "" {
		sent = esc(displayName(s, n))
	}
	c.W(`<p class="sentence">`, s.gutterLink(n, s.URL(n)), sent, `</p>`)
	c.Prose(n)
	c.W(`</div>`)
	return nil
}

// fieldsTable writes a type's fields with type, required, default,
// constraints and docs; empty columns are left out.
func (c *Ctx) fieldsTable(owner *resolve.Node, ms []member) {
	s := c.Site
	hasDef, hasDoc, hasCons := false, false, false
	for _, m := range ms {
		if m.def != nil {
			hasDef = true
		}
		if s.proseHTML(m.node) != "" {
			hasDoc = true
		}
		if len(constraints(m.node)) > 0 {
			hasCons = true
		}
	}
	c.W(`<div class="table-wrap"><table class="fields"><thead><tr><th>`, esc(label("docs.col.name")), `</th><th>`, esc(label("docs.col.type")), `</th>`)
	if hasDef {
		c.W(`<th>`, esc(label("docs.col.default")), `</th>`)
	}
	if hasCons {
		c.W(`<th>`, esc(label("docs.col.constraints")), `</th>`)
	}
	if hasDoc {
		c.W(`<th>`, esc(label("docs.col.description")), `</th>`)
	}
	c.W(`</tr></thead><tbody>`)
	for _, m := range ms {
		id := Anchor(m.node)
		if m.from != nil {
			id = Anchor(owner) + "." + m.name
		}
		c.W(`<tr id="`, attr(id), `"><td><code class="fname">`, esc(m.name), `</code>`)
		if !m.optional && m.def == nil {
			c.W(` <span class="badge req">`, esc(label("docs.badge.required")), `</span>`)
		}
		c.W(`</td><td><code>`, m.typ.html, `</code>`)
		if m.from != nil {
			c.W(` <span class="from">`, s.linkHTML(m.from, esc(m.from.Name), ""), `</span>`)
		}
		if c.nest {
			c.W(c.nestedFields(owner, m))
		}
		c.W(`</td>`)
		if hasDef {
			c.W(`<td>`)
			if m.def != nil {
				c.W(`<code>`, s.exprHTML(m.def, m.site), `</code>`)
			}
			c.W(`</td>`)
		}
		if hasCons {
			c.W(`<td>`)
			for _, a := range constraints(m.node) {
				c.W(`<code>`, s.highlightDecl(resolve.Site{File: m.site.File, N: a.N, Scope: m.site.Scope}, false), `</code> `)
			}
			c.W(`</td>`)
		}
		if hasDoc {
			c.W(`<td>`, s.proseHTML(m.node), `</td>`)
		}
		c.W(`</tr>`)
	}
	c.W(`</tbody></table></div>`)
}

// constraints are a member's annotations other than docs and display.
func constraints(n *resolve.Node) []ast.Annotation {
	var out []ast.Annotation
	for _, a := range annotations(n) {
		switch annName(a) {
		case "doc", "display", "lint":
			continue
		}
		out = append(out, a)
	}
	return out
}

// interfaceMethods writes an interface's funcs as a table, each row
// anchored by the func's path so links to a method land on it: its name,
// its signature as written and its documentation.
func (c *Ctx) interfaceMethods(n *resolve.Node) {
	s := c.Site
	var fns []*resolve.Node
	for _, ch := range n.Children {
		if ch.Name != "" && ch.Decl.N != nil && s.kindPath(ch) == "core.func" {
			fns = append(fns, ch)
		}
	}
	if len(fns) == 0 {
		return
	}
	hasDoc := false
	for _, f := range fns {
		if s.proseHTML(f) != "" {
			hasDoc = true
		}
	}
	c.Heading(c.Level+1, Anchor(n)+".methods", label("docs.heading.methods"), "")
	c.W(`<div class="table-wrap"><table class="fields"><thead><tr><th>`, esc(label("docs.col.name")), `</th><th>`, esc(label("docs.col.signature")), `</th>`)
	if hasDoc {
		c.W(`<th>`, esc(label("docs.col.description")), `</th>`)
	}
	c.W(`</tr></thead><tbody>`)
	for _, f := range fns {
		sig := strings.TrimSpace(f.Decl.N.Text())
		if i := strings.Index(sig, f.Name+"("); i >= 0 {
			sig = sig[i+len(f.Name):]
		}
		c.W(`<tr id="`, attr(Anchor(f)), `"><td><code class="fname">`, esc(f.Name), `</code></td><td><code>`, esc(sig), `</code></td>`)
		if hasDoc {
			c.W(`<td>`, s.proseHTML(f), `</td>`)
		}
		c.W(`</tr>`)
	}
	c.W(`</tbody></table></div>`)
}

// usedBy writes the backlinks of a node: the ops, fields and types that
// refer to it.
func (c *Ctx) usedBy(n *resolve.Node) {
	s := c.Site
	refs := s.backlinks[n]
	if len(refs) == 0 {
		return
	}
	// An anonymous node, such as a rule's clause, is listed as the named
	// node that holds it, once.
	var named []*resolve.Node
	seen := map[*resolve.Node]bool{}
	for _, r := range refs {
		for r.Name == "" && r.Parent != nil {
			r = r.Parent
		}
		if !seen[r] && r != n {
			seen[r] = true
			named = append(named, r)
		}
	}
	if len(named) == 0 {
		return
	}
	c.Heading(c.Level+1, Anchor(n)+".used_by", label("docs.heading.used_by"), "")
	c.W(`<ul class="refs">`)
	for _, r := range named {
		pre, post := s.kindMark(r)
		c.W(`<li>`, pre, s.linkHTML(r, esc(displayName(s, r)), ""), post, `</li>`)
	}
	c.W(`</ul>`)
}

// schemaTree writes a type as an expandable tree: nested types open in
// place, unions show their variants as tabs, a cycle is a link.
func (c *Ctx) schemaTree(x texpr, stack []*resolve.Node, depth int) {
	s := c.Site
	if len(x.union) > 0 {
		c.Island("tabs")
		c.W(`<div class="union" data-island="tabs"><div class="seg" role="tablist">`)
		for i, v := range x.union {
			c.W(`<button type="button" role="tab" aria-selected="`, map[bool]string{true: "true", false: "false"}[i == 0], `">`, esc(v.name), `</button>`)
		}
		c.W(`</div>`)
		for i, v := range x.union {
			hidden := ""
			if i > 0 {
				hidden = " hidden"
			}
			c.W(`<div role="tabpanel"`, hidden, `>`)
			c.schemaTree(v, stack, depth)
			c.W(`</div>`)
		}
		c.W(`</div>`)
		return
	}
	// List(T) and Stream(T) show T's tree.
	if (x.name == "List" || x.name == "Stream" || x.name == "Set") && len(x.args) == 1 {
		c.schemaTree(x.args[0], stack, depth)
		return
	}
	t := x.decl
	if t == nil || !own(t) || !s.isTypeDecl(t) || depth > 6 {
		return
	}
	for _, st := range stack {
		if st == t {
			return
		}
	}
	if s.kindPath(t) == "types.enum" {
		return
	}
	ms := s.members(t, s.envFor(x))
	if len(ms) == 0 {
		return
	}
	stack = append(stack, t)
	c.W(`<ul class="schema">`)
	for _, m := range ms {
		inner := m.typ
		for (inner.name == "List" || inner.name == "Stream") && len(inner.args) == 1 {
			inner = inner.args[0]
		}
		cyclic := false
		for _, st := range stack {
			if inner.decl == st {
				cyclic = true
			}
		}
		expandable := !cyclic && inner.decl != nil && own(inner.decl) && s.isTypeDecl(inner.decl) && s.kindPath(inner.decl) != "types.enum" && len(s.members(inner.decl, nil)) > 0 || len(inner.union) > 0
		row := c.schemaRow(m)
		if expandable {
			c.W(`<li><details><summary>`, row, `</summary>`)
			c.schemaTree(m.typ, stack, depth+1)
			c.W(`</details></li>`)
		} else {
			c.W(`<li><div class="row">`, row, `</div></li>`)
		}
	}
	c.W(`</ul>`)
}

func (c *Ctx) schemaRow(m member) string {
	s := c.Site
	var b strings.Builder
	name := m.name
	if m.node != nil && m.node.Parent != nil {
		name = jsonnames.Member(m.node, m.node.Parent, s.jsonCase)
	}
	b.WriteString(`<span class="row-main"><code class="fname">` + esc(name) + `</code> <code class="t">` + m.typ.html + `</code>`)
	switch {
	case !m.optional && m.def == nil:
		b.WriteString(` <span class="badge req">` + esc(label("docs.badge.required")) + `</span>`)
	case m.typ.optional:
		b.WriteString(` <span class="mark">` + esc(label("docs.label.nullable")) + `</span>`)
	}
	if m.def != nil {
		b.WriteString(` <span class="mark">` + esc(label("docs.label.default")) + ` <code>` + s.exprHTML(m.def, m.site) + `</code></span>`)
	}
	if t := m.typ.decl; t != nil && s.kindPath(t) == "types.enum" {
		var vs []string
		for _, v := range t.Children {
			if v.Name != "" {
				vs = append(vs, `<code>`+esc(v.Name)+`</code>`)
			}
		}
		b.WriteString(` <span class="mark values">` + strings.Join(vs, " ") + `</span>`)
	}
	b.WriteString(`</span>`)
	if d := s.proseHTML(m.node); d != "" {
		b.WriteString(`<div class="row-doc">` + d + `</div>`)
	}
	return b.String()
}
