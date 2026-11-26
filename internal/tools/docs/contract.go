package docs

import (
	"fmt"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// The renderers of the contract kinds: component pages with their contract
// and dependency links, command pages laid out as a CLI reference, and
// grammars as EBNF blocks whose names link production to production.

func addContractRenderers() {
	for k, f := range map[string]RenderFunc{
		"project.component":      renderComponent,
		"spec.command":           renderCommand,
		"grammar.grammar":        renderGrammar,
		"grammar.production":     renderGrammarPart,
		"grammar.token":          renderGrammarPart,
		"project.property":       renderProperty,
		"project.responsibility": renderStepAlone,
		"project.exports":        renderSurface,
		"spec.argument":          renderStepAlone,
		"spec.option":            renderStepAlone,
		"spec.screen":            renderScreen,
		"spec.error":             renderDiagnostic,
		"spec.region":            renderRegion,
	} {
		builtins[k] = f
	}
}

// verbatim is the text of a node whose kind takes text as written, such as
// a production's expression or a surface's code, without its braces and
// with its common indent removed.
func verbatim(n *resolve.Node) string {
	var parts []string
	for _, p := range n.Prose {
		if p.N == nil {
			continue
		}
		t := p.N.Text()
		t = strings.TrimSpace(t)
		t = strings.TrimPrefix(t, "{{")
		t = strings.TrimSuffix(t, "}}")
		t = strings.TrimPrefix(t, "=|")
		parts = append(parts, dedentBlock(t))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// dedentBlock trims blank edge lines and the indent every line shares.
func dedentBlock(t string) string {
	lines := strings.Split(t, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	min := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		ind := len(l) - len(strings.TrimLeft(l, " \t"))
		if min < 0 || ind < min {
			min = ind
		}
	}
	for i, l := range lines {
		if len(l) >= min && min > 0 {
			lines[i] = l[min:]
		}
	}
	return strings.Join(lines, "\n")
}

// arrayItems are the items of an array expression.
func arrayItems(e ast.Expr) []ast.Expr {
	if arr, ok := e.(ast.ArrayExpr); ok {
		return arr.Exprs()
	}
	return nil
}

// --- components ---

// components are the tree's component nodes, and usedBy maps each to the
// components whose uses name it.
func (s *Site) usedBy(n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	s.Tree.Walk(func(x *resolve.Node) {
		if !own(x) || s.kindPath(x) != "project.component" {
			return
		}
		for _, u := range s.componentUses(x) {
			if u == n {
				out = append(out, x)
			}
		}
	})
	return out
}

// componentUses are the components a component's uses field names.
func (s *Site) componentUses(n *resolve.Node) []*resolve.Node {
	u := n.Child("uses")
	if u == nil || u.Value == nil {
		return nil
	}
	var out []*resolve.Node
	for _, e := range arrayItems(u.Value) {
		if t := s.refTarget(e.Syntax(), u.ValueSite); t != nil {
			out = append(out, t)
		}
	}
	return out
}

func renderComponent(c *Ctx) error {
	n := c.Node
	s := c.Site
	path, _ := argString(kindArgs(n), 0, "path")
	lang, _ := argString(kindArgs(n), 1, "lang")
	c.W(`<section class="node component" id="`, attr(Anchor(n)), `">`)
	c.title(` <code class="path">` + esc(path) + `</code>` + statusBadge(n))
	var facts [][2]string
	if c.isPage() && path != "" {
		facts = append(facts, [2]string{label("docs.label.path"), `<code class="path">` + esc(path) + `</code>`})
	}
	if lang != "" {
		facts = append(facts, [2]string{label("docs.label.language"), `<code>` + esc(lang) + `</code>`})
	}
	facts = append(facts,
		[2]string{label("docs.project.realizes"), c.specLinks(s.realizes(n))},
		[2]string{label("docs.label.uses"), c.codeLinks(s.componentUses(n))},
		[2]string{label("docs.heading.used_by"), c.codeLinks(s.usedBy(n))},
	)
	c.factList(facts)
	c.Prose(n)
	if rs := s.childrenOfKind(n, "project.responsibility"); len(rs) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".responsibilities", label("docs.heading.responsibilities"), "")
		c.W(`<ul class="responsibilities">`)
		for _, r := range rs {
			c.W(`<li id="`, attr(Anchor(r)), `">`)
			if r.Name != "" {
				c.W(`<a class="anchor resp-name" href="#`, attr(Anchor(r)), `">`, esc(displayName(s, r)), `</a> `)
			}
			c.W(s.inlineHTML(r), `</li>`)
		}
		c.W(`</ul>`)
	}
	if ss := s.childrenOfKind(n, "project.exports"); len(ss) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".surface", label("docs.heading.surface"), "")
		for _, sf := range ss {
			c.renderSurfaceOf(sf, lang)
		}
	}
	if is := s.childrenOfKind(n, "project.property"); len(is) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".properties", label("docs.heading.properties"), "")
		for _, i := range is {
			if err := c.Render(i); err != nil {
				return err
			}
		}
	}
	if v := c.verifyList(n); v != "" {
		c.Heading(c.Level+1, Anchor(n)+".tests", label("docs.heading.tests"), "")
		c.W(v)
	}
	if groups := s.implementedBy(n); len(groups) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".implements", label("docs.heading.implements"), "")
		c.W(`<ul class="implements">`)
		for _, g := range groups {
			count := label("docs.label.rule_one")
			if g.rules != 1 {
				count = label("docs.label.rule_count", "count", fmt.Sprint(g.rules))
			}
			name := esc(displayName(s, g.section))
			// The parent's name tells apart sections of one name, such as
			// the Lines of prose and of strings.
			if p := g.section.Parent; p != nil && p.Parent != nil && p.Name != "" {
				name = esc(displayName(s, p)) + ` › ` + name
			}
			c.W(`<li>`, s.linkHTML(g.section, name, ""), ` <span class="count">`, esc(count), `</span></li>`)
		}
		c.W(`</ul>`)
	}
	if err := c.otherChildren(n, "project."); err != nil {
		return err
	}
	c.W(`</section>`)
	return nil
}

// ruleGroup is a section and how many of its rules a component implements.
type ruleGroup struct {
	section *resolve.Node
	rules   int
}

// implementedBy groups the rules outside a component whose `by` names it
// by the node each rule sits in, in tree order.
func (s *Site) implementedBy(n *resolve.Node) []ruleGroup {
	var out []ruleGroup
	at := map[*resolve.Node]int{}
	s.Tree.Walk(func(x *resolve.Node) {
		if !own(x) || x.Parent == nil || s.kindPath(x) != "spec.rule" || s.ruleBy(x) != n || inside(x, n) {
			return
		}
		i, ok := at[x.Parent]
		if !ok {
			i = len(out)
			at[x.Parent] = i
			out = append(out, ruleGroup{section: x.Parent})
		}
		out[i].rules++
	})
	return out
}

// inside reports whether x is n or a node below it.
func inside(x, n *resolve.Node) bool {
	for ; x != nil; x = x.Parent {
		if x == n {
			return true
		}
	}
	return false
}

// otherChildren renders the children not of the package a renderer lays
// out itself, such as rules and sections inside a component.
func (c *Ctx) otherChildren(n *resolve.Node, prefix string, skip ...string) error {
	for _, ch := range n.Children {
		k := c.Site.kindPath(ch)
		if strings.HasPrefix(k, prefix) || k == "project.verify" || k == "" || !c.Site.shown(ch) || contains(skip, k) {
			continue
		}
		if ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue {
			continue
		}
		if p := c.Site.pageOf[ch]; p != nil && p != c.Page {
			c.W(`<ul class="page-links"><li>`)
			c.Link(ch, displayName(c.Site, ch))
			c.W(`</li></ul>`)
			continue
		}
		if err := c.Render(ch); err != nil {
			return err
		}
	}
	return nil
}

// codeLinks writes links to nodes by their names in mono.
func (c *Ctx) codeLinks(ns []*resolve.Node) string {
	var parts []string
	for _, n := range ns {
		parts = append(parts, c.Site.linkHTML(n, `<code>`+esc(n.Name)+`</code>`, ""))
	}
	return strings.Join(parts, `<span class="sep">, </span>`)
}

func renderSurface(c *Ctx) error {
	lang := ""
	if p := c.Node.Parent; p != nil {
		lang, _ = argString(kindArgs(p), 1, "lang")
	}
	c.renderSurfaceOf(c.Node, lang)
	return nil
}

func (c *Ctx) renderSurfaceOf(n *resolve.Node, lang string) {
	if l, ok := argString(kindArgs(n), 0, "lang"); ok {
		lang = l
	}
	title, _ := argString(kindArgs(n), 1, "title")
	c.W(`<div class="surface" id="`, attr(Anchor(n)), `">`)
	for _, d := range n.Docs {
		c.W(c.Site.renderText([]resolve.Site{d}))
	}
	c.W(c.Site.codeBlock(lang, title, verbatim(n)))
	c.W(`</div>`)
}

func renderProperty(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<div class="rule property" id="`, attr(Anchor(n)), `">`)
	for _, ch := range n.Children {
		k := s.kindPath(ch)
		if !strengthKinds[k] {
			continue
		}
		subj := esc(label("docs.rule.subject"))
		if p := n.Parent; p != nil && s.kindPath(p) == "project.component" {
			subj = `<code>` + esc(p.Name) + `</code>`
		}
		out := capitalise(subj) + ` <strong class="strength">` + esc(kindWords(strings.TrimPrefix(k, "spec."))) + `</strong> ` + s.inlineHTML(ch)
		if !strings.HasSuffix(stripTags(out), ".") {
			out += "."
		}
		if n.Name != "" {
			out += ` <a class="rule-link" href="#` + attr(Anchor(n)) + `" aria-label="` + attr(displayName(s, n)) + `">#</a>`
		}
		c.W(`<p class="sentence">`, out, `</p>`)
	}
	c.Prose(n)
	c.W(c.verifyList(n))
	c.W(`</div>`)
	return nil
}

// --- commands ---

func renderCommand(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<section class="node command" id="`, attr(Anchor(n)), `">`)
	c.title("")
	if u := n.Child("usage"); u != nil && u.Value != nil {
		var lines []string
		for _, e := range arrayItems(u.Value) {
			if v, ok := resolve.StringValue(e.Syntax()); ok {
				lines = append(lines, v)
			}
		}
		if len(lines) > 0 {
			c.W(s.codeFrame("sh", label("docs.label.usage"), esc(strings.Join(lines, "\n")), strings.Join(lines, "\n")))
		}
	}
	c.Prose(n)
	if as := s.childrenOfKind(n, "spec.argument"); len(as) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".arguments", label("docs.heading.arguments"), "")
		small := len(as) < 3
		if small {
			c.W(`<ul class="lines">`)
		} else {
			c.W(`<div class="table-wrap"><table class="opts"><thead><tr><th>`, esc(label("docs.col.name")), `</th><th>`, esc(label("docs.col.type")), `</th><th>`, esc(label("docs.col.description")), `</th></tr></thead><tbody>`)
		}
		for _, a := range as {
			args := kindArgs(a)
			an, _ := argString(args, 0, "name")
			shown := "<" + an + ">"
			if boolArg(a, "variadic") {
				shown += "…"
			}
			if boolArg(a, "optional") {
				shown = "[" + shown + "]"
			}
			if small {
				c.W(`<li id="`, attr(Anchor(a)), `"><p class="line-head"><code>`, esc(shown), `</code> `, c.argTypeHTML(a, "type", 1), `</p>`, lineDoc(s.proseHTML(a)), `</li>`)
				continue
			}
			c.W(`<tr id="`, attr(Anchor(a)), `"><td><code>`, esc(shown), `</code></td><td>`, c.argTypeHTML(a, "type", 1), `</td><td>`, s.proseHTML(a), `</td></tr>`)
		}
		if small {
			c.W(`</ul>`)
		} else {
			c.W(`</tbody></table></div>`)
		}
	}
	if os := s.childrenOfKind(n, "spec.option"); len(os) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".options", label("docs.heading.options"), "")
		small := len(os) < 3
		if small {
			c.W(`<ul class="lines">`)
		} else {
			c.W(`<div class="table-wrap"><table class="opts"><thead><tr><th>`, esc(label("docs.col.option")), `</th><th>`, esc(label("docs.col.type")), `</th><th>`, esc(label("docs.col.default")), `</th><th>`, esc(label("docs.col.description")), `</th></tr></thead><tbody>`)
		}
		for _, o := range os {
			args := kindArgs(o)
			on, _ := argString(args, 0, "name")
			shown := `<code>` + esc(on) + `</code>`
			if sh, ok := argString(args, -1, "short"); ok {
				shown = `<code>` + esc(sh) + `</code><span class="sep">, </span>` + shown
			}
			doc := s.proseHTML(o)
			env, hasEnv := argString(args, -1, "env")
			if small {
				head := shown + ` ` + c.argTypeHTML(o, "type", 1)
				if d := c.argTypeHTML(o, "default", 2); d != "" {
					head += `<span class="sep">, </span>` + esc(label("docs.label.default")) + ` ` + d
				}
				if hasEnv {
					head += `<span class="sep">, </span>` + esc(label("docs.label.env")) + ` <code>` + esc(env) + `</code>`
				}
				c.W(`<li id="`, attr(Anchor(o)), `"><p class="line-head">`, head, `</p>`, lineDoc(doc), `</li>`)
				continue
			}
			if hasEnv {
				doc += `<div class="mark">` + esc(label("docs.label.env")) + ` <code>` + esc(env) + `</code></div>`
			}
			c.W(`<tr id="`, attr(Anchor(o)), `"><td>`, shown, `</td><td>`, c.argTypeHTML(o, "type", 1), `</td><td>`, c.argTypeHTML(o, "default", 2), `</td><td>`, doc, `</td></tr>`)
		}
		if small {
			c.W(`</ul>`)
		} else {
			c.W(`</tbody></table></div>`)
		}
	}
	errs := s.childrenOfKind(n, "spec.error")
	errs = append(errs, s.argRefList(n, "errors")...)
	if len(errs) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".exit", label("docs.heading.exit_codes"), "")
		c.W(`<ul class="lines">`)
		for _, e := range errs {
			code := ""
			if x := namedArg(kindArgs(e), "exit"); x != nil {
				code = strings.TrimSpace(x.Syntax().Text())
			}
			title := ""
			if t := e.Child("title"); t != nil && t.Value != nil {
				title, _ = resolve.StringValue(t.Value.Syntax())
			}
			c.W(`<li id="`, attr(Anchor(e)), `"><p class="line-head"><code>`, esc(code), `</code> `, esc(title), `</p></li>`)
		}
		c.W(`</ul>`)
	}
	if exs := s.childrenOfKind(n, "spec.example"); len(exs) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".examples", label("docs.label.examples"), "")
		for _, e := range exs {
			c.W(`<div class="example" id="`, attr(Anchor(e)), `">`, s.proseHTML(e), `</div>`)
		}
	}
	if v := c.verifyList(n); v != "" {
		c.Heading(c.Level+1, Anchor(n)+".tests", label("docs.heading.tests"), "")
		c.W(v)
	}
	subs := s.childrenOfKind(n, "spec.command")
	if len(subs) > 0 {
		c.Heading(c.Level+1, Anchor(n)+".commands", label("docs.heading.subcommands"), "")
		for _, sc := range subs {
			if p := s.pageOf[sc]; p != nil && p != c.Page {
				continue
			}
			if err := c.Render(sc); err != nil {
				return err
			}
		}
		var links []*resolve.Node
		for _, sc := range subs {
			if p := s.pageOf[sc]; p != nil && p != c.Page {
				links = append(links, sc)
			}
		}
		if len(links) > 0 {
			c.W(`<ul class="page-links">`)
			for _, l := range links {
				c.W(`<li>`)
				c.Link(l, displayName(s, l))
				c.W(`</li>`)
			}
			c.W(`</ul>`)
		}
	}
	if err := c.otherChildren(n, "spec.argument", "spec.option", "spec.error", "spec.command", "spec.example"); err != nil {
		return err
	}
	c.W(`</section>`)
	return nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// argTypeHTML is a kind argument's expression as code, or "".
func (c *Ctx) argTypeHTML(n *resolve.Node, name string, index int) string {
	args := kindArgs(n)
	e := namedArg(args, name)
	if e == nil {
		if es := argExprs(args); index < len(es) {
			e = es[index]
		}
	}
	if e == nil {
		return ""
	}
	return `<code>` + c.Site.exprHTML(e, n.Decl) + `</code>`
}

func boolArg(n *resolve.Node, name string) bool {
	e := namedArg(kindArgs(n), name)
	return e != nil && strings.TrimSpace(e.Syntax().Text()) == "true"
}

// --- grammars ---

// grammarNames maps the names of a grammar's productions and tokens to
// their nodes. Grammars that name the same source are one grammar split by
// concern, so their names link across them; the grammar's own names win.
func (s *Site) grammarNames(g *resolve.Node) map[string]*resolve.Node {
	out := map[string]*resolve.Node{}
	add := func(g *resolve.Node) {
		for _, ch := range g.Children {
			switch s.kindPath(ch) {
			case "grammar.production", "grammar.token":
				if out[ch.Name] == nil {
					out[ch.Name] = ch
				}
			}
		}
	}
	add(g)
	if src, ok := argString(kindArgs(g), 1, "source"); ok && src != "" {
		for _, o := range s.grammarsBySource()[src] {
			if o != g {
				add(o)
			}
		}
	}
	return out
}

// grammarsBySource lists the tree's grammars by the source file they name.
func (s *Site) grammarsBySource() map[string][]*resolve.Node {
	if s.grammarSources == nil {
		s.grammarSources = map[string][]*resolve.Node{}
		s.Tree.Walk(func(n *resolve.Node) {
			if s.kindPath(n) != "grammar.grammar" {
				return
			}
			if src, ok := argString(kindArgs(n), 1, "source"); ok && src != "" {
				s.grammarSources[src] = append(s.grammarSources[src], n)
			}
		})
	}
	return s.grammarSources
}

// ebnfTokens splits a grammar expression into its names, literals,
// character classes, comments and operators.
func ebnfTokens(src string) []ebnfTok {
	var out []ebnfTok
	i := 0
	for i < len(src) {
		ch := src[i]
		switch {
		case ch == '"' || ch == '\'':
			j := i + 1
			for j < len(src) && src[j] != ch {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(src) {
				j++
			}
			out = append(out, ebnfTok{"str", src[i:j]})
			i = j
		case ch == '[':
			j := i + 1
			for j < len(src) && src[j] != ']' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(src) {
				j++
			}
			out = append(out, ebnfTok{"str", src[i:j]})
			i = j
		case ch == '/' && i+1 < len(src) && src[i+1] == '/':
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				j = len(src) - i
			}
			out = append(out, ebnfTok{"comment", src[i : i+j]})
			i += j
		case ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z':
			j := i
			for j < len(src) && (src[j] == '_' || src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z' || src[j] >= '0' && src[j] <= '9') {
				j++
			}
			out = append(out, ebnfTok{"name", src[i:j]})
			i = j
		default:
			out = append(out, ebnfTok{"op", src[i : i+1]})
			i++
		}
	}
	return out
}

type ebnfTok struct{ kind, text string }

// ebnfHTML highlights a grammar expression, linking each name to its
// production or token.
func (s *Site) ebnfHTML(src string, names map[string]*resolve.Node, from *resolve.Node) string {
	var b strings.Builder
	for _, t := range ebnfTokens(src) {
		switch t.kind {
		case "str":
			b.WriteString(`<span class="tk-str">` + esc(t.text) + `</span>`)
		case "comment":
			b.WriteString(`<span class="tk-comment">` + esc(t.text) + `</span>`)
		case "name":
			if n := names[t.text]; n != nil {
				u := "#" + Anchor(n)
				if from != nil && s.anchorOf[n] != nil && s.anchorOf[n] != s.anchorOf[from] {
					// A name of a sibling grammar, on another page.
					u = s.URL(n)
				}
				b.WriteString(`<a class="code-ref tk-name" href="` + attr(u) + `">` + esc(t.text) + `</a>`)
			} else {
				b.WriteString(`<span class="tk-name">` + esc(t.text) + `</span>`)
			}
		default:
			if strings.TrimSpace(t.text) == "" {
				b.WriteString(esc(t.text))
			} else {
				b.WriteString(`<span class="tk-punct">` + esc(t.text) + `</span>`)
			}
		}
	}
	return b.String()
}

func renderGrammar(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<section class="node grammar" id="`, attr(Anchor(n)), `">`)
	c.title("")
	var start string
	if st := n.Child("start"); st != nil && st.Value != nil {
		if t := s.refTarget(st.Value.Syntax(), st.ValueSite); t != nil {
			start = s.linkHTML(t, `<code>`+esc(t.Name)+`</code>`, "")
		}
	}
	notation, _ := argString(kindArgs(n), 0, "notation")
	source, _ := argString(kindArgs(n), 1, "source")
	var facts [][2]string
	if notation != "" {
		facts = append(facts, [2]string{label("docs.label.notation"), `<code>` + esc(notation) + `</code>`})
	}
	if source != "" {
		facts = append(facts, [2]string{label("docs.label.source"), `<code class="path">` + esc(source) + `</code>`})
	}
	facts = append(facts, [2]string{label("docs.label.start"), start})
	c.factList(facts)
	c.Prose(n)
	names := s.grammarNames(n)
	usedBy := map[*resolve.Node][]*resolve.Node{}
	for _, ch := range n.Children {
		if s.kindPath(ch) != "grammar.production" {
			continue
		}
		seen := map[*resolve.Node]bool{}
		for _, t := range ebnfTokens(verbatim(ch)) {
			if x := names[t.text]; t.kind == "name" && x != nil && x != ch && !seen[x] {
				seen[x] = true
				usedBy[x] = append(usedBy[x], ch)
			}
		}
	}
	groups := []struct{ kind, key string }{{"grammar.production", "docs.heading.productions"}, {"grammar.token", "docs.heading.tokens"}}
	both := len(s.childrenOfKind(n, "grammar.production")) > 0 && len(s.childrenOfKind(n, "grammar.token")) > 0
	for _, group := range groups {
		parts := s.childrenOfKind(n, group.kind)
		if len(parts) == 0 {
			continue
		}
		// A grammar of one group needs no heading under its own.
		if both {
			c.Heading(c.Level+1, Anchor(n)+"."+strings.TrimPrefix(group.kind, "grammar.")+"s", label(group.key), "")
		}
		width := 0
		for _, p := range parts {
			if len(p.Name) > width {
				width = len(p.Name)
			}
		}
		c.W(`<div class="ebnf">`)
		for _, p := range parts {
			c.W(`<div class="prod" id="`, attr(Anchor(p)), `">`)
			// Every line after the first, written or wrapped, hangs under
			// the expression's start.
			c.W(`<pre class="prod-def"><code style="--hang:`, fmt.Sprint(width+3), `ch"><a class="anchor prod-name" href="#`, attr(Anchor(p)), `">`, esc(p.Name), `</a>`, strings.Repeat(" ", width-len(p.Name)), ` <span class="tk-punct">=</span> `)
			expr := verbatim(p)
			c.W(s.ebnfHTML(expr, names, n), `</code></pre>`)
			if len(p.Docs) > 0 || len(usedBy[p]) > 0 {
				c.W(`<div class="prod-doc">`)
				for _, d := range p.Docs {
					c.W(s.renderText([]resolve.Site{d}))
				}
				if us := usedBy[p]; len(us) > 0 {
					c.W(`<div class="mark">`, esc(label("docs.heading.used_by")), ` `)
					for i, u := range us {
						if i > 0 {
							c.W(`<span class="sep">, </span>`)
						}
						c.W(`<a class="code-ref" href="#`, attr(Anchor(u)), `"><code>`, esc(u.Name), `</code></a>`)
					}
					c.W(`</div>`)
				}
				c.W(`</div>`)
			}
			c.W(`</div>`)
		}
		c.W(`</div>`)
	}
	if err := c.otherChildren(n, "grammar."); err != nil {
		return err
	}
	c.W(`</section>`)
	return nil
}

// renderGrammarPart shows a production or token outside its grammar.
func renderGrammarPart(c *Ctx) error {
	n := c.Node
	names := map[string]*resolve.Node{}
	if p := n.Parent; p != nil {
		names = c.Site.grammarNames(p)
	}
	c.W(`<div class="ebnf"><div class="prod" id="`, attr(Anchor(n)), `"><pre class="prod-def"><code>`, esc(n.Name), ` <span class="tk-punct">=</span> `, c.Site.ebnfHTML(verbatim(n), names, n), `</code></pre></div></div>`)
	return nil
}

// --- screens ---

func renderScreen(c *Ctx) error {
	n := c.Node
	c.W(`<section class="node screen" id="`, attr(Anchor(n)), `">`)
	c.title("")
	if r, ok := argString(kindArgs(n), 0, "route"); ok {
		c.factList([][2]string{{label("docs.label.route"), `<code class="path">` + esc(r) + `</code>`}})
	}
	c.Prose(n)
	if rs := c.Site.childrenOfKind(n, "spec.region"); len(rs) > 0 {
		c.W(`<div class="regions">`)
		for _, r := range rs {
			if err := c.Render(r); err != nil {
				return err
			}
		}
		c.W(`</div>`)
	}
	if err := c.otherChildren(n, "spec.region"); err != nil {
		return err
	}
	c.W(`</section>`)
	return nil
}

// renderRegion draws a region as a bordered box holding its prose and the
// regions inside it.
func renderRegion(c *Ctx) error {
	n := c.Node
	c.W(`<div class="region" id="`, attr(Anchor(n)), `"><a class="anchor region-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(c.Site, n)), `</a>`)
	c.Prose(n)
	for _, ch := range n.Children {
		if !c.Site.shown(ch) || ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue {
			continue
		}
		if err := c.Render(ch); err != nil {
			return err
		}
	}
	c.W(`</div>`)
	return nil
}

// lineDoc is a row's description under its line, as prose.
func lineDoc(h string) string {
	if strings.TrimSpace(h) == "" {
		return ""
	}
	return `<div class="prose line-doc">` + h + `</div>`
}

// stripP is one paragraph of rendered prose without its <p>, to join a
// line.
func stripP(h string) string {
	h = strings.TrimSpace(h)
	if strings.HasPrefix(h, "<p>") && strings.HasSuffix(h, "</p>") && strings.Count(h, "<p>") == 1 {
		return h[3 : len(h)-4]
	}
	return h
}
