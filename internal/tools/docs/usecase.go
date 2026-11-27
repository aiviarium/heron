package docs

import (
	"fmt"
	"regexp"
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
	rows = append(rows, [2]string{label("docs.label.serves"), c.refLinks(s.annRefs(n, "serves"))})
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
		lv := 2
		if !c.isPage() {
			lv = c.Level + 1
		}
		src := body.File.Src
		c.Heading(lv, Anchor(n)+"-main", label("docs.label.main_path"), "")
		c.W(`<ol class="uc-steps">`)
		for _, st := range p.Steps {
			c.W(`<li id="`, attr(Anchor(n)+"."+st.Num), `"><span class="uc-num">`, esc(st.Num), `</span><span class="uc-text">`, asSubject(src, st.Start, s.rangeHTML(body, st.Start, st.End)), `</span></li>`)
		}
		c.W(`</ol>`)
		if len(p.Exts) > 0 {
			c.Heading(lv, Anchor(n)+"-extensions", label("docs.label.extensions"), "")
			c.W(`<dl class="uc-ext">`)
			for _, x := range p.Exts {
				if lv < 3 {
					*c.outline = append(*c.outline, Heading{ID: Anchor(n) + "." + x.Num, Title: x.Num + " " + x.Cond, Level: lv + 1})
				}
				c.W(`<div class="uc-ext-item" id="`, attr(Anchor(n)+"."+x.Num), `"><dt><span class="uc-num">`, esc(x.Num), `</span><span class="uc-text">`, esc(x.Cond), `</span></dt><dd><ol class="uc-steps">`)
				for _, st := range x.Steps {
					c.W(`<li><span class="uc-num">`, esc(strings.ReplaceAll(st.Num, ".", "")), `</span><span class="uc-text">`, asSubject(src, st.Start, s.rangeHTML(body, st.Start, st.End)), `</span></li>`)
				}
				if out := outcomeLabel(x); out != "" {
					c.W(`<li class="uc-outcome"><span class="uc-num">→</span><span class="uc-text mark">`, esc(out), `</span></li>`)
				}
				c.W(`</ol></dd></div>`)
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

// asSubject reads a step that opens with its party's link as a sentence
// with that party as its subject: "The editor underlines…", where the
// link's text is a common noun, starting lowercase.
func asSubject(src string, start int, h string) string {
	for start < len(src) && (src[start] == ' ' || src[start] == '\t' || src[start] == '\n' || src[start] == '|') {
		start++
	}
	h = strings.TrimSpace(h)
	if start >= len(src) || src[start] != '[' || !strings.HasPrefix(h, "<a ") {
		return h
	}
	i := strings.IndexByte(h, '>')
	if i < 0 || i+1 >= len(h) {
		return h
	}
	if r := h[i+1]; r < 'a' || r > 'z' {
		return h
	}
	return label("docs.usecase.the") + " " + h
}

func lineStart(src string, at int) int { return strings.LastIndexByte(src[:at], '\n') + 1 }

func stripBraces(h string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(h), "{{"))
}

// flowSVG draws a use case's main path down the left and each extension
// to the right of the step it branches off, with where it goes.
func flowSVG(uc usecase.UseCase) string {
	const w, xw, h, gap = 190, 140, 28, 14
	const xx = 10 + w + 50
	rowY := func(i int) int { return 10 + i*(h+gap) }
	height := rowY(len(uc.Steps)) + 10
	var b strings.Builder
	fmt.Fprintf(&b, `<figure class="uc-flow"><svg viewBox="0 0 %d %d" role="img" aria-label="%s">`, xx+xw+10, height, attr(label("docs.label.flow")))
	for i, st := range uc.Steps {
		y := rowY(i)
		fmt.Fprintf(&b, `<rect x="10" y="%d" width="%d" height="%d" rx="4" class="uc-box"/><text x="20" y="%d" class="uc-label"><tspan class="uc-label-num">%s</tspan> %s</text>`, y, w, h, y+18, esc(st.Num), esc(clip(st.Text, 22)))
		if i > 0 {
			fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="uc-edge"/>`, 10+w/2, y-gap, 10+w/2, y)
		}
	}
	col := map[int]int{}
	for _, x := range uc.Exts {
		if x.At < 1 || x.At > len(uc.Steps) {
			continue
		}
		y := rowY(x.At-1) + col[x.At]*6
		col[x.At]++
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="uc-edge"/>`, 10+w, y+h/2, xx, y+h/2)
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="4" class="uc-box uc-ext-box"/>`, xx, y, xw, h)
		out := outcomeLabel(x)
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="uc-label">%s · %s</text>`, xx+8, y+18, esc(x.Num), esc(out))
	}
	b.WriteString(`</svg></figure>`)
	return b.String()
}

// outcomeLabel is how an extension ends, in the catalogue's words.
func outcomeLabel(x usecase.Ext) string {
	switch x.Outcome {
	case usecase.Fails:
		return label("docs.usecase.fails")
	case usecase.Succeeds:
		return label("docs.usecase.succeeds")
	case usecase.BackTo:
		return label("docs.usecase.back_to", "step", fmt.Sprint(x.BackTo))
	}
	return ""
}

// backlinks are the links written on lower nodes, walked in reverse: for
// each target, the nodes that realize, illustrate, serve, refine, rely on
// or derive from it, and the errors whose prose links it.
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
		// An error's prose links the rules that say when it's reported.
		if s.kindPath(n) == "spec.error" {
			for _, t := range project.ErrorRules(s.Checker, n) {
				add(t, "reported_as", n)
				if t.Shape == resolve.ShapeStep && t.Parent != nil {
					add(t.Parent, "reported_as", n)
				}
			}
		}
		for _, pair := range [][2]string{{"serves", "served_by"}, {"illustrates", "illustrated_by"}, {"refines", "refined_by"}, {"relies", "relied_on_by"}, {"derives", "derived_by"}} {
			for _, t := range s.annRefs(n, pair[0]) {
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
	for _, key := range []string{"realized_by", "reported_as", "illustrated_by", "served_by", "refined_by", "relied_on_by", "derived_by"} {
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

// clip shortens plain text to at most n runes, with an ellipsis, its
// links' brackets and targets dropped.
func clip(t string, n int) string {
	t0 := t
	t = linkTargets.ReplaceAllString(t, "$1")
	t = shortLinks.ReplaceAllStringFunc(t, func(m string) string {
		// A short link reads as its target's last name.
		m = strings.Trim(m, "[]")
		return strings.ReplaceAll(m[strings.LastIndexByte(m, '.')+1:], "_", " ")
	})
	if strings.HasPrefix(strings.TrimSpace(t0), "[") {
		if r := []rune(strings.TrimSpace(t)); len(r) > 0 && r[0] >= 'a' && r[0] <= 'z' {
			t = label("docs.usecase.the") + " " + strings.TrimSpace(t)
		}
	}
	t = strings.NewReplacer("[", "", "]", "", "`", "").Replace(strings.TrimSpace(t))
	if r := []rune(t); len(r) > n {
		return strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return t
}

var (
	linkTargets = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	shortLinks  = regexp.MustCompile(`\[[A-Za-z_][\w.]*\]`)
)
