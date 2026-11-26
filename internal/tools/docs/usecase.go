package docs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/specmodel"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/usecase"
)

// rangeHTML renders the part of a prose site between two offsets as inline
// text, its links resolved as in the whole text.
func (s *Site) rangeHTML(site resolve.Site, start, end int) string {
	var elems []syntax.Elem
	var walk func(n *syntax.Node)
	walk = func(n *syntax.Node) {
		for _, ch := range n.Children {
			cs, ce := ch.Span()
			if ce <= start || cs >= end {
				continue
			}
			switch x := ch.(type) {
			case *syntax.Token:
				a, b := max(cs, start), min(ce, end)
				if a < b {
					elems = append(elems, &syntax.Token{Kind: x.Kind, Start: a, Text: x.Text[a-cs : b-cs]})
				}
			case *syntax.Node:
				switch x.Kind {
				case syntax.ProseLine, syntax.ProseLines, syntax.ProseBlock, syntax.Tag:
					if x.Kind == syntax.Tag && cs >= start && ce <= end {
						elems = append(elems, x)
						continue
					}
					walk(x)
				default:
					if cs >= start && ce <= end {
						elems = append(elems, x)
					}
				}
			}
		}
	}
	if site.N != nil {
		walk(site.N)
	}
	p := &proseBuf{s: s, site: site}
	return p.sub(elems)
}

// renderUsecase shows a use case as Cockburn writes one: its actor, level,
// goals and guarantees, its main path numbered, its extensions as 2a, 3a
// with their condition, steps and outcome, and what realizes it.
func renderUsecase(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<section class="node usecase" id="`, attr(Anchor(n)), `">`)
	c.title(statusBadge(n))
	var rows [][2]string
	if a := s.argRef(n, 0, "actor"); a != nil {
		rows = append(rows, [2]string{label("docs.label.actor"), s.linkHTML(a, esc(displayName(s, a)), "")})
	}
	level := "USER"
	if e := namedArg(kindArgs(n), "level"); e != nil {
		level = variantName(e)
	}
	rows = append(rows, [2]string{label("docs.label.level"), esc(kindWords(strings.ToLower(level)))})
	rows = append(rows, [2]string{label("docs.label.serves"), c.refLinks(s.argRefs(n, "serves"))})
	if v, ok := argString(kindArgs(n), -1, "success"); ok {
		rows = append(rows, [2]string{label("docs.label.success"), esc(v)})
	}
	if v, ok := argString(kindArgs(n), -1, "minimal"); ok {
		rows = append(rows, [2]string{label("docs.label.minimal"), esc(v)})
	}
	c.factList(rows)
	p, ok := specmodel.ParseUseCase(s.Checker, n)
	if !ok || len(p.Steps) == 0 {
		c.Prose(n)
	} else {
		body := p.Site
		bs, _ := body.N.Span()
		if first := p.Steps[0].Start; first > bs {
			// The text before the steps: the use case's summary.
			intro := s.rangeHTML(body, bs, lineStart(body.File.Src, first))
			intro = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(stripBraces(intro)), "{{"))
			if intro != "" {
				c.W(`<p>`, intro, `</p>`)
			}
		}
		c.W(`<ol class="uc-steps">`)
		for _, st := range p.Steps {
			c.W(`<li id="`, attr(Anchor(n)+"."+st.Num), `">`, s.rangeHTML(body, st.Start, st.End), `</li>`)
		}
		c.W(`</ol>`)
		if len(p.Exts) > 0 {
			c.W(`<div class="label">`, esc(label("docs.label.extensions")), `</div><dl class="uc-ext">`)
			for _, x := range p.Exts {
				c.W(`<div class="uc-ext-item" id="`, attr(Anchor(n)+"."+x.Num), `"><dt><span class="uc-num">`, esc(x.Num), `</span> `, esc(x.Cond), `</dt><dd>`)
				if len(x.Steps) > 0 {
					c.W(`<ol class="uc-steps">`)
					for _, st := range x.Steps {
						c.W(`<li>`, s.rangeHTML(body, st.Start, st.End), `</li>`)
					}
					c.W(`</ol>`)
				}
				switch x.Outcome {
				case usecase.Fails:
					c.W(`<p class="mark uc-outcome">`, esc(label("docs.usecase.fails")), `</p>`)
				case usecase.Succeeds:
					c.W(`<p class="mark uc-outcome">`, esc(label("docs.usecase.succeeds")), `</p>`)
				case usecase.BackTo:
					c.W(`<p class="mark uc-outcome">`, esc(label("docs.usecase.back_to", "step", fmt.Sprint(x.BackTo))), `</p>`)
				}
				c.W(`</dd></div>`)
			}
			c.W(`</dl>`)
			if len(p.Exts) >= 2 && s.Opts.Lens != nil && s.Opts.Lens.Name != "user" && s.Opts.Lens.Name != "agent" {
				c.W(flowSVG(p.UseCase))
			}
		}
	}
	c.W(c.verifyList(n))
	c.W(c.derivedLists(n))
	c.W(`</section>`)
	return nil
}

func lineStart(src string, at int) int { return strings.LastIndexByte(src[:at], '\n') + 1 }

func stripBraces(h string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(h), "{{"))
}

// flowSVG draws a use case's main path down the left and each extension
// to the right of the step it branches off, with where it goes.
func flowSVG(uc usecase.UseCase) string {
	const w, h, gap = 150, 28, 14
	rowY := func(i int) int { return 10 + i*(h+gap) }
	height := rowY(len(uc.Steps)) + 10
	var b strings.Builder
	fmt.Fprintf(&b, `<figure class="uc-flow"><svg viewBox="0 0 420 %d" role="img" aria-label="%s">`, height, attr(label("docs.label.flow")))
	for i, st := range uc.Steps {
		y := rowY(i)
		fmt.Fprintf(&b, `<rect x="10" y="%d" width="%d" height="%d" rx="4" class="uc-box"/><text x="20" y="%d" class="uc-label">%s</text>`, y, w, h, y+18, esc(st.Num))
		if i > 0 {
			fmt.Fprintf(&b, `<line x1="85" y1="%d" x2="85" y2="%d" class="uc-edge"/>`, y-gap, y)
		}
	}
	col := map[int]int{}
	for _, x := range uc.Exts {
		if x.At < 1 || x.At > len(uc.Steps) {
			continue
		}
		y := rowY(x.At-1) + col[x.At]*6
		col[x.At]++
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="250" y2="%d" class="uc-edge"/>`, 10+w, y+h/2, y+h/2)
		fmt.Fprintf(&b, `<rect x="250" y="%d" width="%d" height="%d" rx="4" class="uc-box uc-ext-box"/>`, y, w, h)
		out := ""
		switch x.Outcome {
		case usecase.Fails:
			out = label("docs.usecase.fails")
		case usecase.Succeeds:
			out = label("docs.usecase.succeeds")
		case usecase.BackTo:
			out = label("docs.usecase.back_to", "step", fmt.Sprint(x.BackTo))
		}
		fmt.Fprintf(&b, `<text x="258" y="%d" class="uc-label">%s · %s</text>`, y+18, esc(x.Num), esc(out))
	}
	b.WriteString(`</svg></figure>`)
	return b.String()
}

// backlinks are the links written on lower nodes, walked in reverse: for
// each target, the nodes that realize, illustrate, serve, refine, rely on
// or derive from it.
func (s *Site) specLinks() map[*resolve.Node]map[string][]*resolve.Node {
	if s.back != nil {
		return s.back
	}
	s.back = map[*resolve.Node]map[string][]*resolve.Node{}
	add := func(t *resolve.Node, key string, from *resolve.Node) {
		if t == nil || from == nil {
			return
		}
		if s.back[t] == nil {
			s.back[t] = map[string][]*resolve.Node{}
		}
		for _, x := range s.back[t][key] {
			if x == from {
				return
			}
		}
		s.back[t][key] = append(s.back[t][key], from)
	}
	s.Tree.Walk(func(n *resolve.Node) {
		for _, pair := range [][2]string{{"serves", "served_by"}, {"illustrates", "illustrated_by"}, {"refines", "refined_by"}, {"relies", "relied_on_by"}, {"derives", "derived_by"}} {
			for _, t := range s.argRefs(n, pair[0]) {
				add(t, pair[1], n)
			}
		}
		for _, a := range n.Annotations {
			an := ast.Annotation{N: a.N}
			if an.N == nil || an.Path().N == nil {
				continue
			}
			if segs := resolve.PathSegs(an.Path().N); len(segs) == 0 || segs[len(segs)-1] != "realizes" {
				continue
			}
			for _, e := range project.ArgExprs(an.Args()) {
				if t := project.RefTarget(s.Checker, e.Syntax(), a); t != nil {
					add(t, "realized_by", n)
					if t.Shape == resolve.ShapeStep && t.Parent != nil {
						add(t.Parent, "realized_by", n)
					}
				}
			}
		}
	})
	return s.back
}

// derivedLists are a node's derived lists, each marked as derived.
func (c *Ctx) derivedLists(n *resolve.Node) string {
	s := c.Site
	m := s.specLinks()[n]
	if len(m) == 0 {
		return ""
	}
	var b strings.Builder
	for _, key := range []string{"realized_by", "illustrated_by", "served_by", "refined_by", "relied_on_by", "derived_by"} {
		var links []string
		ns := m[key]
		sort.SliceStable(ns, func(i, j int) bool { return ns[i].Path() < ns[j].Path() })
		for _, x := range ns {
			if !s.kept(x) && s.anchorOf[x] == nil {
				continue
			}
			links = append(links, s.linkHTML(x, esc(displayName(s, x)), ""))
		}
		if len(links) == 0 {
			continue
		}
		b.WriteString(`<div class="derived"><span class="derived-label">` + esc(label("docs.label."+key)) + ` <span class="derived-mark">` + esc(label("docs.label.derived")) + `</span></span> ` + strings.Join(links, ", ") + `</div>`)
	}
	return b.String()
}

// realizes reports whether a contract node names a rule with @realizes.
func (s *Site) entryRealizes(entry, rule *resolve.Node) bool {
	for _, x := range s.specLinks()[rule]["realized_by"] {
		if x == entry {
			return true
		}
	}
	return false
}
