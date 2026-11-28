package docs

import (
	"github.com/aiviarium/heron/internal/core/project"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// The renderers of the intent kinds and of std/project's delivery: goals,
// actors and personas, parts, scenarios, stories, increments, milestones
// and done criteria.

func addIntentRenderers() {
	for k, f := range map[string]RenderFunc{
		"spec.actor":        renderActor,
		"spec.persona":      renderPersona,
		"spec.goal":         renderGoal,
		"spec.quality":      renderGoal,
		"spec.nongoal":      renderNote,
		"spec.assumption":   renderNote,
		"spec.part":         renderPart,
		"spec.scenario":     renderScenario,
		"spec.usecase":      renderUsecase,
		"spec.given":        renderStepAlone,
		"project.story":     renderStory,
		"project.increment": renderIncrement,
		"project.milestone": renderIncrement,
		"project.done":      renderDone,
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
	exprs := []ast.Expr{e}
	if arr, ok := e.(ast.ArrayExpr); ok {
		exprs = arr.Exprs()
	}
	var out []*resolve.Node
	for _, x := range exprs {
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
	if k := c.Site.Checker.GroupFlag(n, "role"); k != "" && k != "person" {
		c.W(`<span class="aliases">`, esc(kindWords(k)), `</span>`)
	}
	c.W(`</dt><dd>`)
	c.Prose(n)
	c.W(`</dd></div>`)
	return nil
}

func renderPersona(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<article class="card-block persona" id="`, attr(Anchor(n)), `">`)
	c.W(`<div class="card-head"><a class="anchor card-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(s, n)), `</a>`)
	for _, a := range project.HardTargets(s.Checker, n) {
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

// renderWork is the view of an increment or a milestone: its heading
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

func renderIncrement(c *Ctx) error {
	n := c.Node
	key := "docs.label.delivers"
	if c.Site.kindPath(n) == "project.milestone" {
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
	serves := project.Served(s.Checker, n)
	c.factList([][2]string{
		{label("docs.label.serves"), c.refLinks(serves)},
		{label("docs.label.adds"), c.refLinks(s.argRefs(n, "adds"))},
		{label("docs.label.changes"), c.refLinks(s.argRefs(n, "changes"))},
	})
	c.Prose(n)
	if err := c.childrenOrLinks(); err != nil {
		return err
	}
	c.W(`</article>`)
	return nil
}

// gwtLine is one line of a scenario: its keyword and its text.
func gwtLine(key, h string) string {
	return `<p class="gwt"><span class="gwt-key">` + esc(label(key)) + `</span> ` + h + `</p>`
}

// renderScenario shows an outcome scenario as its given, when and then,
// with the rule or path it illustrates, or a quality scenario as its
// stimulus, environment, response and measure with the quality it serves.
func renderScenario(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<div class="scenario" id="`, attr(Anchor(n)), `">`)
	if n.Name != "" {
		c.W(`<p class="scenario-head">`, s.gutterLink(n, "#"+Anchor(n)), `<a class="anchor scenario-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(s, n)), `</a></p>`)
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
	lines("spec.given", "docs.gwt.given")
	lines("spec.when", "docs.gwt.when")
	lines("spec.then", "docs.gwt.then")
	var rows [][2]string
	for _, f := range []string{"stimulus", "environment", "response", "measure"} {
		if h := s.fieldInline(n, f); h != "" {
			rows = append(rows, [2]string{label("docs.label." + f), h})
		}
	}
	if t := project.Described(s.Checker, n); t != nil {
		rows = append(rows, [2]string{label("docs.label.illustrates"), s.linkHTML(t, esc(displayName(s, t)), "")})
	}
	rows = append(rows, [2]string{label("docs.label.measures"), c.refLinks(project.ReferenceTargets(s.Checker, n, "spec.quality"))})
	c.factList(rows)
	c.W(c.verifyList(n))
	c.W(c.derivedLists(n))
	c.W(`</div>`)
	return nil
}

// renderGoal shows a goal or a quality: its text, its measure, the actors
// or personas who asked for it, and what serves it, derived from the
// references below it.
func renderGoal(c *Ctx) error {
	n := c.Node
	s := c.Site
	head := `<span class="label">` + esc(label("docs.label.goal")) + `</span> `
	if pre, _ := s.kindMark(n); pre != "" {
		// The kind's icon says it's a goal.
		head = pre
	}
	c.W(`<div class="goal-block" id="`, attr(Anchor(n)), `"><div class="goal-head">`, head, `<a class="anchor goal-name" href="#`, attr(Anchor(n)), `">`, esc(displayName(s, n)), `</a></div>`)
	c.Prose(n)
	var rows [][2]string
	if m, ok := argString(kindArgs(n), -1, "measure"); ok {
		rows = append(rows, [2]string{label("docs.label.measure"), esc(m)})
	}
	if by := project.AskedBy(s.Checker, n); len(by) > 0 {
		rows = append(rows, [2]string{label("docs.label.asked_by"), c.refLinks(by)})
	}
	c.factList(rows)
	c.W(c.derivedLists(n))
	c.W(`</div>`)
	return nil
}

// renderNote shows a nongoal or an assumption as a callout.
func renderNote(c *Ctx) error {
	n := c.Node
	key := "docs.label.nongoal"
	if c.Site.kindPath(n) == "spec.assumption" {
		key = "docs.label.assumption"
	}
	c.W(`<aside class="callout callout-note" id="`, attr(Anchor(n)), `"><div class="label">`, esc(label(key)), `</div>`)
	c.Prose(n)
	c.W(c.derivedLists(n))
	c.W(`</aside>`)
	return nil
}

// renderPart shows a part with its prose and the rules that bind it.
func renderPart(c *Ctx) error {
	n := c.Node
	if c.isPage() || len(n.Children) > 0 {
		c.W(`<section class="node part" id="`, attr(Anchor(n)), `">`)
		c.title("")
		c.Prose(n)
		c.W(c.derivedLists(n))
		if err := c.childrenOrLinks(); err != nil {
			return err
		}
		c.W(`</section>`)
		return nil
	}
	return renderActor(c)
}

func renderDone(c *Ctx) error {
	n := c.Node
	c.W(`<aside class="callout callout-done" id="`, attr(Anchor(n)), `"><div class="label">`, esc(label("docs.label.done")), `</div>`)
	c.Prose(n)
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
