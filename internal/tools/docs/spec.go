package docs

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
)

var (
	strengthKinds = map[string]bool{"spec.must": true, "spec.should": true, "spec.may": true, "spec.mustNot": true, "spec.shouldNot": true}
	// Conditions that open the sentence, in the order std/spec declares
	// them; unless closes it.
	leadConditions = []string{"spec.when", "spec.while", "spec.where", "spec.if"}
)

// ruleStrength returns the kind name of a rule's or quality's strength
// node, such as "mustNot", or "".
func (s *Site) ruleStrength(n *resolve.Node) string {
	switch s.kindPath(n) {
	case "spec.rule", "spec.quality":
	default:
		return ""
	}
	for _, ch := range n.Children {
		if k := s.kindPath(ch); strengthKinds[k] {
			return strings.TrimPrefix(k, "spec.")
		}
	}
	return ""
}

func (s *Site) childOfKind(n *resolve.Node, kind string) *resolve.Node {
	for _, ch := range n.Children {
		if s.kindPath(ch) == kind {
			return ch
		}
	}
	return nil
}

func (s *Site) childrenOfKind(n *resolve.Node, kind string) []*resolve.Node {
	var out []*resolve.Node
	for _, ch := range n.Children {
		if s.kindPath(ch) == kind {
			out = append(out, ch)
		}
	}
	return out
}

// subject is who a requirement binds: the node its `by` names, as "the
// parser", the type a rule on a type is about, or the system.
func (s *Site) subject(n *resolve.Node) string {
	if by := s.ruleBy(n); by != nil {
		if s.kindPath(by) == "tech.component" {
			return s.linkHTML(by, `<code>`+esc(by.Name)+`</code>`, "subject")
		}
		return strings.Replace(esc(label("docs.rule.subject_named", "name", "\x00")), "\x00", s.linkHTML(by, esc(subjectWords(s, by)), "subject"), 1)
	}
	for _, t := range s.targets(n) {
		if s.isTypeDecl(t) {
			words := kindWords(t.Name)
			key := "docs.rule.subject_type_a"
			if strings.ContainsRune("aeiou", rune(words[0])) {
				key = "docs.rule.subject_type_an"
			}
			return esc(label(key, "type", words))
		}
	}
	return esc(label("docs.rule.subject"))
}

// ruleBy is the node a rule's `by` names, or nil.
func (s *Site) ruleBy(n *resolve.Node) *resolve.Node {
	if e := namedArg(kindArgs(n), "by"); e != nil {
		return s.refTarget(e.Syntax(), n.Decl)
	}
	return nil
}

// personSubject reports whether a requirement's subject is an actor or a
// persona: a person, whose behaviour no test proves.
func (s *Site) personSubject(n *resolve.Node) bool {
	by := s.ruleBy(n)
	if by == nil {
		return false
	}
	k := s.kindPath(by)
	return k == "product.actor" || k == "product.persona"
}

// subjectWords is how a subject reads inside a sentence: an actor or a
// component by its display name in lower case, as "language server".
func subjectWords(s *Site, n *resolve.Node) string {
	if a := annotation(s, n, "display"); a != nil {
		return displayName(s, n)
	}
	if s.kindPath(n) == "doc.term" {
		return termName(s, n)
	}
	return kindWords(n.Name)
}

// sentence joins a requirement's strength and conditions into one EARS
// sentence: the conditions' kinds open it, the subject and the strength's
// words carry the behaviour, and unless closes it.
func (s *Site) sentence(n *resolve.Node) string {
	strength := ""
	var behaviour string
	for _, ch := range n.Children {
		if k := s.kindPath(ch); strengthKinds[k] {
			strength = strings.TrimPrefix(k, "spec.")
			behaviour = s.inlineHTML(ch)
		}
	}
	if strength == "" {
		return ""
	}
	var lead []string
	for _, k := range leadConditions {
		if ch := s.childOfKind(n, k); ch != nil {
			lead = append(lead, esc(kindWords(strings.TrimPrefix(k, "spec.")))+" "+s.inlineHTML(ch))
		}
	}
	var b strings.Builder
	subj := s.subject(n)
	if len(lead) > 0 {
		b.WriteString(capitalise(strings.Join(lead, ", ")))
		b.WriteString(", ")
		b.WriteString(subj)
	} else {
		b.WriteString(capitalise(subj))
	}
	b.WriteString(` <strong class="strength">` + esc(kindWords(strength)) + `</strong> `)
	b.WriteString(behaviour)
	if u := s.childOfKind(n, "spec.unless"); u != nil {
		b.WriteString(", " + esc(kindWords("unless")) + " " + s.inlineHTML(u))
	}
	out := strings.TrimRight(b.String(), " ")
	if !strings.HasSuffix(stripTags(out), ".") {
		out += "."
	}
	return out
}

func capitalise(h string) string {
	// Capitalise the first letter outside a tag, unless it's code, which
	// keeps its case.
	in := false
	for i, r := range h {
		switch {
		case r == '<':
			if strings.HasPrefix(h[i:], "<code") {
				return h
			}
			in = true
		case r == '>':
			in = false
		case !in && r != ' ':
			if r >= 'a' && r <= 'z' {
				return h[:i] + string(r-32) + h[i+1:]
			}
			return h
		}
	}
	return h
}

func renderRule(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<div class="rule" id="`, attr(Anchor(n)), `">`)
	c.W(`<div class="rule-head">`)
	if n.Name != "" {
		c.W(`<a class="anchor rule-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(s, n)), `</a>`)
	}
	c.W(statusBadge(n))
	if ts := s.targets(n); len(ts) > 0 {
		c.targetsLine(ts)
	}
	c.W(`</div>`)
	if sent := s.sentence(n); sent != "" {
		c.W(`<p class="sentence">`, sent, `</p>`)
	}
	c.Prose(n)
	c.consequences(n)
	c.W(`</div>`)
	return nil
}

// consequences writes a requirement's then, instead and examples.
func (c *Ctx) consequences(n *resolve.Node) {
	s := c.Site
	var rows []string
	for _, k := range []string{"spec.then", "spec.instead"} {
		if ch := s.childOfKind(n, k); ch != nil {
			rows = append(rows, `<dt>`+esc(sentenceCase(strings.TrimPrefix(k, "spec.")))+`</dt><dd>`+s.inlineHTML(ch)+`</dd>`)
		}
	}
	if ap := s.childOfKind(n, "spec.applies"); ap != nil {
		rows = append(rows, `<dt>`+esc(sentenceCase("applies"))+`</dt><dd>`+s.inlineHTML(ap)+`</dd>`)
	}
	if exs := s.childrenOfKind(n, "spec.example"); len(exs) > 0 {
		var li strings.Builder
		for _, e := range exs {
			li.WriteString(`<li>` + s.inlineHTML(e) + `</li>`)
		}
		rows = append(rows, `<dt>`+esc(label("docs.label.examples"))+`</dt><dd><ul>`+li.String()+`</ul></dd>`)
	}
	if v := c.verifyList(n); v != "" {
		rows = append(rows, `<dt>`+esc(label("docs.label.verified_by"))+`</dt><dd>`+v+`</dd>`)
	}
	// Conditions other packages add render with their kind's name.
	for _, ch := range n.Children {
		k := s.kindPath(ch)
		if ch.Name != "" || k == "" || strengthKinds[k] || strings.HasPrefix(k, "spec.") {
			continue
		}
		rows = append(rows, `<dt>`+esc(sentenceCase(s.Checker.KindOf(ch).Name))+`</dt><dd>`+s.inlineHTML(ch)+`</dd>`)
	}
	if len(rows) > 0 {
		c.W(`<dl class="rule-more">`, strings.Join(rows, ""), `</dl>`)
	}
}

func renderQuality(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<div class="rule quality" id="`, attr(Anchor(n)), `">`)
	c.W(`<div class="rule-head"><a class="anchor rule-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(s, n)), `</a>`)
	if ts := s.targets(n); len(ts) > 0 {
		c.targetsLine(ts)
	}
	c.W(`</div>`)
	if sent := s.sentence(n); sent != "" {
		c.W(`<p class="sentence">`, sent, `</p>`)
	}
	c.Prose(n)
	var facts []string
	for _, f := range []string{"metric", "at_least", "at_most", "percentile"} {
		ch := n.Child(f)
		if ch == nil || ch.Value == nil {
			continue
		}
		facts = append(facts, `<dt>`+esc(sentenceCase(f))+`</dt><dd><code>`+s.exprHTML(ch.Value, ch.ValueSite)+`</code></dd>`)
	}
	if len(facts) > 0 {
		c.W(`<dl class="facts">`, strings.Join(facts, ""), `</dl>`)
	}
	c.consequences(n)
	c.W(`</div>`)
	return nil
}

func renderMetric(c *Ctx) error {
	n := c.Node
	extra := ""
	if es := argExprs(kindArgs(n)); len(es) > 0 {
		extra = ` <code class="type">` + c.Site.exprHTML(es[0], n.Decl) + `</code>`
	}
	c.W(`<section class="node metric" id="`, attr(Anchor(n)), `">`)
	c.title(extra)
	c.Prose(n)
	c.W(`</section>`)
	return nil
}

// --- lifecycles ---

// lifecycle is a states node read for drawing: its states, the initial
// and terminal ones, and its transitions with the ops that perform them.
type lifecycle struct {
	node        *resolve.Node
	field       *resolve.Node
	states      []string
	initial     string
	terminal    map[string]bool
	transitions []transition
}

type transition struct {
	node     *resolve.Node
	name     string
	from, to string
	ops      []*resolve.Node
}

func variantName(e ast.Expr) string {
	if e == nil {
		return ""
	}
	t := strings.TrimSpace(e.Syntax().Text())
	if i := strings.LastIndex(t, "."); i >= 0 {
		return t[i+1:]
	}
	return t
}

func (s *Site) readLifecycle(n *resolve.Node) *lifecycle {
	l := &lifecycle{node: n, terminal: map[string]bool{}}
	seen := map[string]bool{}
	addState := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			l.states = append(l.states, v)
		}
	}
	if es := argExprs(kindArgs(n)); len(es) > 0 {
		l.field = s.refTarget(es[0].Syntax(), n.Decl)
	}
	if in := n.Child("initial"); in != nil {
		l.initial = variantName(in.Value)
	}
	if l.field != nil {
		if et := s.Checker.TypeOf(l.field); et != nil && s.Checker.IsEnum(et) {
			if l.initial != "" {
				addState(l.initial)
			}
			for _, v := range et.Children {
				addState(v.Name)
			}
		}
	}
	addState(l.initial)
	if te := n.Child("terminal"); te != nil && te.Value != nil {
		if arr, ok := te.Value.(ast.ArrayExpr); ok {
			for _, e := range arr.Exprs() {
				l.terminal[variantName(e)] = true
			}
		}
	}
	performed := s.performers()
	for _, ch := range n.Children {
		if s.kindPath(ch) != "spec.transition" {
			continue
		}
		es := argExprs(kindArgs(ch))
		t := transition{node: ch, name: ch.Name, ops: performed[ch]}
		if len(es) > 0 {
			t.from = variantName(es[0])
		}
		if len(es) > 1 {
			t.to = variantName(es[1])
		}
		addState(t.from)
		addState(t.to)
		l.transitions = append(l.transitions, t)
	}
	return l
}

// performers maps each transition to the ops whose @performs names it.
func (s *Site) performers() map[*resolve.Node][]*resolve.Node {
	out := map[*resolve.Node][]*resolve.Node{}
	s.Tree.Walk(func(n *resolve.Node) {
		if !own(n) || s.kindPath(n) != "types.op" {
			return
		}
		for _, a := range annotationsNamed(n, "performs") {
			for _, e := range argExprs(a.Args()) {
				if t := s.refTarget(e.Syntax(), n.Decl); t != nil {
					out[t] = append(out[t], n)
				}
			}
		}
	})
	return out
}

func renderStates(c *Ctx) error {
	n := c.Node
	s := c.Site
	l := s.readLifecycle(n)
	extra := ""
	if es := argExprs(kindArgs(n)); len(es) > 0 {
		extra = ` <code class="field">` + s.exprHTML(es[0], n.Decl) + `</code>`
	}
	c.W(`<section class="node lifecycle" id="`, attr(Anchor(n)), `">`)
	c.title(extra)
	c.Prose(n)
	c.lifecycleView(l)
	for _, t := range l.transitions {
		if len(t.node.Children) > 0 || len(t.node.Prose) > 0 {
			if err := c.Render(t.node); err != nil {
				return err
			}
		}
	}
	c.W(`</section>`)
	return nil
}

// lifecycleView writes a lifecycle's diagram with its table, and the
// switch between them.
func (c *Ctx) lifecycleView(l *lifecycle) {
	s := c.Site
	c.Island("diagram")
	id := Anchor(l.node)
	c.W(`<div class="diagram-view" data-island="diagram-view">`)
	c.W(`<div class="seg" role="tablist" aria-label="`, attr(label("docs.label.view")), `">`)
	c.W(`<button type="button" role="tab" aria-selected="true" data-view="diagram">`, esc(label("docs.action.diagram")), `</button>`)
	c.W(`<button type="button" role="tab" aria-selected="false" data-view="table">`, esc(label("docs.action.table")), `</button></div>`)
	c.W(`<div class="diagram" data-pane="diagram" data-island="diagram">`, s.lifecycleSVG(l, id), `</div>`)
	c.W(`<div class="table-wrap" data-pane="table" hidden><table><thead><tr><th>`, esc(label("docs.col.transition")), `</th><th>`, esc(label("docs.col.from")), `</th><th>`, esc(label("docs.col.to")), `</th><th>`, esc(label("docs.col.performed_by")), `</th></tr></thead><tbody>`)
	for _, t := range l.transitions {
		c.W(`<tr id="`, attr(Anchor(t.node)), `"><td><code>`, esc(t.name), `</code></td><td><code>`, esc(t.from), `</code></td><td><code>`, esc(t.to), `</code></td><td>`)
		if len(t.ops) == 0 {
			c.W(`<span class="warn">`, esc(label("docs.label.no_op")), `</span>`)
		}
		for i, op := range t.ops {
			if i > 0 {
				c.W(", ")
			}
			c.W(s.linkHTML(op, `<code>`+esc(op.Name)+`</code>`, ""))
		}
		c.W(`</td></tr>`)
	}
	c.W(`</tbody></table></div></div>`)
}

func renderTransition(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<div class="rule transition" id="`, attr(Anchor(n)), `-detail">`)
	c.W(`<div class="rule-head"><a class="anchor rule-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(s, n)), `</a></div>`)
	c.Prose(n)
	var rows []string
	for _, k := range append(append([]string(nil), leadConditions...), "spec.unless") {
		if ch := s.childOfKind(n, k); ch != nil {
			rows = append(rows, `<dt>`+esc(sentenceCase(strings.TrimPrefix(k, "spec.")))+`</dt><dd>`+s.inlineHTML(ch)+`</dd>`)
		}
	}
	if len(rows) > 0 {
		c.W(`<dl class="rule-more">`, strings.Join(rows, ""), `</dl>`)
	}
	c.consequences(n)
	c.W(`</div>`)
	return nil
}

// verifyList is the list of a node's verify children: each test or script
// by its file and name, with its prose, or "".
func (c *Ctx) verifyList(n *resolve.Node) string {
	vs := c.Site.childrenOfKind(n, "project.verify")
	if len(vs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<ul class="verify">`)
	for _, v := range vs {
		b.WriteString(`<li id="` + attr(Anchor(v)) + `">` + verifyRef(v))
		if p := c.Site.inlineHTML(v); strings.TrimSpace(p) != "" {
			b.WriteString(` <span class="verify-note">` + p + `</span>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>`)
	return b.String()
}

// verifyRef is a verify node's file and test, as `internal/core/syntax/parse_test.go`
// `TestLineStarts`.
func verifyRef(v *resolve.Node) string {
	path, _ := argString(kindArgs(v), 0, "path")
	out := `<code class="path">` + esc(path) + `</code>`
	if t, ok := argString(kindArgs(v), 1, "test"); ok {
		out += ` <code class="test">` + esc(t) + `</code>`
	}
	return out
}
