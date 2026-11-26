package docs

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// The renderers of std/product: actors and personas, epics of stories
// with their criteria, use cases with numbered flows, increments,
// milestones and done criteria.

func addProductRenderers() {
	for k, f := range map[string]RenderFunc{
		"product.actor":         renderActor,
		"product.persona":       renderPersona,
		"product.epic":          renderEpic,
		"product.story":         renderStory,
		"product.criterion":     renderCriterion,
		"product.usecase":       renderUsecase,
		"product.increment":     renderIncrement,
		"product.milestone":     renderIncrement,
		"product.done":          renderDone,
		"product.step":          renderStepAlone,
		"product.given":         renderStepAlone,
		"product.alternate":     renderStepAlone,
		"product.failure":       renderStepAlone,
		"product.precondition":  renderStepAlone,
		"product.postcondition": renderStepAlone,
	} {
		builtins[k] = f
	}
}

// statusBadge is the status flag of a piece of work, as "started", or "".
func statusBadge(n *resolve.Node) string {
	for _, f := range kindFlags(n) {
		switch f {
		case "planned", "started", "delivered", "deferred":
			return ` <span class="status status-` + attr(f) + `">` + esc(kindWords(f)) + `</span>`
		}
	}
	return ""
}

// argRef is the node a kind argument refers to, by position or name.
func (s *Site) argRef(n *resolve.Node, index int, name string) *resolve.Node {
	args := kindArgs(n)
	if e := namedArg(args, name); e != nil {
		return s.refTarget(e.Syntax(), n.Decl)
	}
	if es := argExprs(args); index >= 0 && index < len(es) {
		return s.refTarget(es[index].Syntax(), n.Decl)
	}
	return nil
}

// argRefs are the nodes a list argument refers to.
func (s *Site) argRefs(n *resolve.Node, name string) []*resolve.Node {
	e := namedArg(kindArgs(n), name)
	if e == nil {
		return nil
	}
	arr, ok := e.(ast.ArrayExpr)
	if !ok {
		return nil
	}
	var out []*resolve.Node
	for _, x := range arr.Exprs() {
		if t := s.refTarget(x.Syntax(), n.Decl); t != nil {
			out = append(out, t)
		}
	}
	return out
}

// fieldInline is a prose field of a node as inline HTML, or "".
func (s *Site) fieldInline(n *resolve.Node, name string) string {
	if ch := n.Child(name); ch != nil {
		return s.inlineHTML(ch)
	}
	return ""
}

// refLinks writes links to nodes, comma separated.
func (c *Ctx) refLinks(ns []*resolve.Node) string {
	var parts []string
	for _, n := range ns {
		parts = append(parts, c.Site.linkHTML(n, esc(displayName(c.Site, n)), ""))
	}
	return strings.Join(parts, `<span class="sep">, </span>`)
}

// facts writes rows of a facts list, skipping empty values.
func (c *Ctx) factList(rows [][2]string) {
	var b strings.Builder
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		b.WriteString(`<div class="fact"><dt>` + esc(r[0]) + `</dt><dd>` + r[1] + `</dd></div>`)
	}
	if b.Len() > 0 {
		c.W(`<dl class="facts">`, b.String(), `</dl>`)
	}
}

func renderActor(c *Ctx) error {
	n := c.Node
	c.W(`<div class="term actor" id="`, attr(Anchor(n)), `">`)
	c.W(`<dt class="term-name"><a class="anchor" href="#`, attr(Anchor(n)), `">`, esc(displayName(c.Site, n)), `</a>`)
	if k := actorKind(n); k != "" && k != "PERSON" {
		c.W(`<span class="aliases">`, esc(kindWords(strings.ToLower(k))), `</span>`)
	}
	c.W(`</dt><dd>`)
	c.Prose(n)
	c.W(`</dd></div>`)
	return nil
}

func actorKind(n *resolve.Node) string {
	args := kindArgs(n)
	if e := namedArg(args, "kind"); e != nil {
		return variantName(e)
	}
	if es := argExprs(args); len(es) > 0 {
		return variantName(es[0])
	}
	return ""
}

func renderPersona(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<article class="card-block persona" id="`, attr(Anchor(n)), `">`)
	c.W(`<div class="card-head"><a class="anchor card-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(s, n)), `</a>`)
	if a := s.argRef(n, 0, "of"); a != nil {
		c.W(`<span class="mark">`, s.linkHTML(a, esc(displayName(s, a)), ""), `</span>`)
	}
	c.W(`</div>`)
	c.Prose(n)
	var rows []string
	for _, f := range []string{"goals", "pains"} {
		if h := s.fieldInline(n, f); h != "" {
			rows = append(rows, `<p class="field-line"><span class="field-label">`+esc(sentenceCase(f))+`</span> `+h+`</p>`)
		}
	}
	c.W(strings.Join(rows, ""))
	c.W(`</article>`)
	return nil
}

// renderWork is the view of an epic, increment or milestone: its heading
// with its status, its facts, its prose and its children.
func renderWork(c *Ctx, class string, facts [][2]string) error {
	n := c.Node
	c.W(`<section class="node `, class, `" id="`, attr(Anchor(n)), `">`)
	c.title(statusBadge(n))
	if c.isPage() {
		if b := statusBadge(n); b != "" {
			c.W(`<div class="page-status">`, b, `</div>`)
		}
	}
	c.factList(facts)
	c.Prose(n)
	if err := c.childrenOrLinks(); err != nil {
		return err
	}
	c.W(`</section>`)
	return nil
}

func renderEpic(c *Ctx) error {
	var facts [][2]string
	if o, ok := argString(kindArgs(c.Node), 0, "owner"); ok {
		facts = append(facts, [2]string{label("docs.label.owner"), `<code>` + esc(o) + `</code>`})
	}
	return renderWork(c, "epic", facts)
}

func renderIncrement(c *Ctx) error {
	n := c.Node
	key := "docs.label.delivers"
	if c.Site.kindPath(n) == "product.milestone" {
		key = "docs.label.reached_by"
	}
	var ts []*resolve.Node
	for _, e := range argExprs(kindArgs(n)) {
		if t := c.Site.refTarget(e.Syntax(), n.Decl); t != nil {
			ts = append(ts, t)
		}
	}
	return renderWork(c, "increment", [][2]string{{label(key), c.refLinks(ts)}})
}

// articled is "a writer" or "an agent", from the catalogue.
func articled(key, name, words string) string {
	if words != "" && strings.ContainsRune("aeiou", rune(words[0])) {
		return label(key+"_an", name, "\x00")
	}
	return label(key+"_a", name, "\x00")
}

func renderStory(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<article class="card-block story" id="`, attr(Anchor(n)), `">`)
	c.W(`<div class="card-head"><a class="anchor card-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(s, n)), `</a>`, statusBadge(n), `</div>`)
	var parts []string
	if a := s.argRef(n, 0, "as"); a != nil {
		words := subjectWords(s, a)
		parts = append(parts, strings.Replace(esc(articled("docs.story.as", "actor", words)), "\x00", s.linkHTML(a, esc(words), ""), 1))
	}
	if w := s.fieldInline(n, "want"); w != "" {
		parts = append(parts, strings.Replace(esc(label("docs.story.want", "want", "\x00")), "\x00", w, 1))
	}
	if so := s.fieldInline(n, "so"); so != "" {
		if len(parts) > 0 {
			parts[len(parts)-1] += ","
		}
		parts = append(parts, strings.Replace(esc(label("docs.story.so", "so", "\x00")), "\x00", so, 1))
	}
	if len(parts) > 0 {
		out := strings.Join(parts, " ")
		if !strings.HasSuffix(stripTags(out), ".") {
			out += "."
		}
		c.W(`<p class="sentence">`, out, `</p>`)
	}
	c.Prose(n)
	if crit := s.childrenOfKind(n, "product.criterion"); len(crit) > 0 {
		c.W(`<div class="label">`, esc(label("docs.label.acceptance")), `</div>`)
		for _, cr := range crit {
			if err := c.Render(cr); err != nil {
				return err
			}
		}
	}
	c.W(`</article>`)
	return nil
}

// gwtLine is one line of a criterion: its keyword and its text.
func gwtLine(key, h string) string {
	return `<p class="gwt"><span class="gwt-key">` + esc(label(key)) + `</span> ` + h + `</p>`
}

func renderCriterion(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<div class="criterion" id="`, attr(Anchor(n)), `">`)
	if n.Name != "" {
		c.W(`<a class="anchor criterion-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(s, n)), `</a>`)
	}
	c.Prose(n)
	lines := func(kind, first string) {
		for i, ch := range s.childrenOfKind(n, kind) {
			key := first
			if i > 0 {
				key = "docs.gwt.and"
			}
			c.W(gwtLine(key, s.inlineHTML(ch)))
		}
	}
	lines("product.given", "docs.gwt.given")
	lines("spec.when", "docs.gwt.when")
	lines("spec.then", "docs.gwt.then")
	c.W(c.verifyList(n))
	c.W(`</div>`)
	return nil
}

func renderDone(c *Ctx) error {
	n := c.Node
	c.W(`<aside class="callout callout-done" id="`, attr(Anchor(n)), `"><div class="label">`, esc(label("docs.label.done")), `</div>`)
	c.Prose(n)
	for _, cr := range c.Site.childrenOfKind(n, "product.criterion") {
		if err := c.Render(cr); err != nil {
			return err
		}
	}
	c.W(`</aside>`)
	return nil
}

// renderStepAlone shows a flow's part outside the use case that numbers
// it: its prose only.
func renderStepAlone(c *Ctx) error {
	c.W(`<div class="node-anon" id="`, attr(Anchor(c.Node)), `">`)
	c.Prose(c.Node)
	c.W(`</div>`)
	return nil
}

// flowNumbers numbers a use case's steps: main steps 1, 2, 3; the flows
// that branch off step 3 3a, 3b, in order; their steps 3a1, 3a2.
func (s *Site) flowNumbers(uc *resolve.Node) (map[*resolve.Node]string, []*resolve.Node) {
	num := map[*resolve.Node]string{}
	var branches []*resolve.Node
	i := 0
	for _, ch := range uc.Children {
		if s.kindPath(ch) == "product.step" {
			i++
			num[ch] = strconv.Itoa(i)
		}
	}
	letters := map[string]int{}
	for _, ch := range uc.Children {
		k := s.kindPath(ch)
		if k != "product.alternate" && k != "product.failure" {
			continue
		}
		at := "0"
		if t := s.argRef(ch, 0, "at"); t != nil && num[t] != "" {
			at = num[t]
		}
		l := letters[at]
		letters[at]++
		id := at + string(rune('a'+l))
		num[ch] = id
		j := 0
		for _, st := range ch.Children {
			if s.kindPath(st) == "product.step" {
				j++
				num[st] = id + strconv.Itoa(j)
			}
		}
		branches = append(branches, ch)
	}
	return num, branches
}

func renderUsecase(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<section class="node usecase" id="`, attr(Anchor(n)), `">`)
	c.title(statusBadge(n))
	var actor string
	if a := s.argRef(n, 0, "actor"); a != nil {
		actor = s.linkHTML(a, esc(displayName(s, a)), "")
	}
	c.factList([][2]string{
		{label("docs.label.primary_actor"), actor},
		{label("docs.label.supporting_actors"), c.refLinks(s.argRefs(n, "supporting"))},
		{label("docs.label.goal"), s.fieldInline(n, "goal")},
	})
	c.Prose(n)
	conds := func(key string, onFailure bool) {
		var items []string
		for _, ch := range n.Children {
			k := s.kindPath(ch)
			if k == "product.precondition" && key == "docs.heading.preconditions" ||
				k == "product.postcondition" && key != "docs.heading.preconditions" && postOnFailure(ch) == onFailure {
				items = append(items, `<li id="`+attr(Anchor(ch))+`">`+s.inlineHTML(ch)+`</li>`)
			}
		}
		if len(items) > 0 {
			c.W(`<div class="label">`, esc(label(key)), `</div><ul class="conds">`, strings.Join(items, ""), `</ul>`)
		}
	}
	conds("docs.heading.preconditions", false)
	num, branches := s.flowNumbers(n)
	steps := func(parent *resolve.Node) {
		c.W(`<ol class="flow">`)
		for _, st := range parent.Children {
			if s.kindPath(st) != "product.step" {
				continue
			}
			c.W(`<li id="`, attr(Anchor(st)), `"><span class="step-no">`, esc(num[st]), `</span><span class="step-text">`, s.inlineHTML(st), `</span></li>`)
		}
		c.W(`</ol>`)
	}
	c.W(`<div class="label">`, esc(label("docs.heading.main_flow")), `</div>`)
	steps(n)
	for _, kind := range []string{"product.alternate", "product.failure"} {
		key := "docs.heading.alternate_flows"
		if kind == "product.failure" {
			key = "docs.heading.failure_flows"
		}
		first := true
		for _, b := range branches {
			if s.kindPath(b) != kind {
				continue
			}
			if first {
				c.W(`<div class="label">`, esc(label(key)), `</div>`)
				first = false
			}
			c.W(`<div class="branch" id="`, attr(Anchor(b)), `"><div class="branch-head"><span class="step-no">`, esc(num[b]), `</span>`)
			if h := s.inlineHTML(b); h != "" {
				c.W(`<span class="step-text">`, h, `</span>`)
			}
			c.W(`</div>`)
			steps(b)
			if r := s.argRef(b, -1, "rejoins"); r != nil && num[r] != "" {
				c.W(`<p class="mark">`, strings.Replace(esc(label("docs.usecase.rejoins", "step", "\x00")), "\x00", s.linkHTML(r, esc(num[r]), ""), 1), `</p>`)
			} else if kind == "product.alternate" {
				c.W(`<p class="mark">`, esc(label("docs.usecase.ends")), `</p>`)
			}
			c.W(`</div>`)
		}
	}
	conds("docs.heading.postconditions", false)
	conds("docs.heading.failure_postconditions", true)
	c.W(`</section>`)
	return nil
}

func postOnFailure(n *resolve.Node) bool {
	e := namedArg(kindArgs(n), "on_failure")
	return e != nil && strings.TrimSpace(e.Syntax().Text()) == "true"
}
