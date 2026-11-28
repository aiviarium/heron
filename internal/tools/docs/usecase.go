package docs

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/specmodel"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/usecase"
	uistrings "github.com/aiviarium/heron/web/strings"
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
	actors := specmodel.UseCaseActors(s.Checker, n)
	if len(actors) > 0 {
		rows = append(rows, [2]string{label("docs.label.actor"), s.linkHTML(actors[0], esc(displayName(s, actors[0])), "")})
	}
	if len(actors) > 1 {
		rows = append(rows, [2]string{label("docs.label.supporting_actors"), c.refLinks(actors[1:])})
	}
	rows = append(rows, [2]string{label("docs.label.level"), esc(kindWords(useCaseLevel(s, n)))})
	rows = append(rows, [2]string{label("docs.label.serves"), c.refLinks(project.Served(s.Checker, n))})
	for _, g := range []string{"success", "minimal"} {
		for _, ch := range s.childrenOfKind(n, "spec."+g) {
			rows = append(rows, [2]string{label("docs.label." + g), s.inlineHTML(ch)})
		}
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

// useCaseLevel is a use case's goal level, from its level flag: "user"
// without one.
func useCaseLevel(s *Site, n *resolve.Node) string {
	if l := s.Checker.GroupFlag(n, "level"); l != "" {
		return l
	}
	return "user"
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

// specLinks are the references and links written on lower nodes, walked
// in reverse: for each target, under "hard", the nodes whose hard
// references name it, a step's use case included; under "reported_as",
// the errors whose prose links it; and under "see_also", the nodes whose
// @see names it.
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
		if s.kindPath(n) == "types.error" {
			for _, t := range project.ErrorRules(s.Checker, n) {
				add(t, "reported_as", n)
				if t.Shape == resolve.ShapeStep && t.Parent != nil {
					add(t.Parent, "reported_as", n)
				}
			}
		}
		// A hard reference's meaning comes from the kind it names; the
		// docs list it by the kind that writes it.
		// A rule's subject isn't listed: the rule's sentence names it, and
		// a part such as the checker is the subject of hundreds.
		subject := project.Subject(s.Checker, n)
		for _, t := range project.HardTargets(s.Checker, n) {
			if t == subject && s.kindPath(n) == "spec.rule" {
				continue
			}
			add(t, "hard", n)
			if t.Shape == resolve.ShapeStep && t.Parent != nil {
				add(t.Parent, "hard", n)
			}
		}
		for _, t := range project.SeeAlso(s.Checker, n) {
			add(t, "see_also", n)
		}
	})
	return s.back
}

// backlinkGroup is one derived list: the nodes of one kind that reference
// a node, or the nodes that see it.
type backlinkGroup struct {
	key   string // the referencing kind's name, or "see_also"
	label string
	nodes []*resolve.Node
}

// backlinkOrder is the order the derived lists of the core kinds come in;
// a custom kind's come after, by name.
var backlinkOrder = []string{"op", "api", "command", "argument", "option", "screen", "region", "grammar", "production", "token", "type", "enum", "union", "interface", "error", "component", "scenario", "example", "usecase", "rule", "constraint", "goal", "story"}

// backlinkGroups are a node's derived lists: the nodes that reference it,
// grouped by their kind, the errors whose prose links it among them, then
// the nodes that see it.
func (s *Site) backlinkGroups(n *resolve.Node) []backlinkGroup {
	m := s.specLinks()[n]
	if len(m) == 0 {
		return nil
	}
	by := map[string][]*resolve.Node{}
	for _, key := range []string{"hard", "reported_as"} {
		for _, x := range m[key] {
			k := s.Checker.KindOf(x)
			if k == nil {
				continue
			}
			dup := false
			for _, y := range by[k.Name] {
				dup = dup || y == x
			}
			if !dup {
				by[k.Name] = append(by[k.Name], x)
			}
		}
	}
	var keys []string
	rank := map[string]int{}
	for i, k := range backlinkOrder {
		rank[k] = i + 1
	}
	for k := range by {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := rank[keys[i]], rank[keys[j]]
		if ri == 0 {
			ri = len(backlinkOrder) + 1
		}
		if rj == 0 {
			rj = len(backlinkOrder) + 1
		}
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	var out []backlinkGroup
	for _, k := range keys {
		out = append(out, backlinkGroup{key: k, label: kindPlural(k), nodes: by[k]})
	}
	if len(m["see_also"]) > 0 {
		out = append(out, backlinkGroup{key: "see_also", label: label("docs.label.see_also"), nodes: m["see_also"]})
	}
	return out
}

// kindPlural is how a derived list names the kind of the nodes in it, as
// "Ops" or "Use cases"; a custom kind's name in sentence case, with an s.
func kindPlural(k string) string {
	if uistrings.Has("docs.referenced_by." + k) {
		return label("docs.referenced_by." + k)
	}
	w := kindWords(k)
	if w == "" {
		return k
	}
	return strings.ToUpper(w[:1]) + w[1:] + "s"
}

// derivedLists are a node's derived lists, each marked as derived.
func (c *Ctx) derivedLists(n *resolve.Node) string {
	s := c.Site
	var b strings.Builder
	for _, g := range s.backlinkGroups(n) {
		var links []string
		ns := append([]*resolve.Node(nil), g.nodes...)
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
		b.WriteString(`<div class="derived"><span class="derived-label">` + esc(g.label) + ` <span class="derived-mark">` + esc(label("docs.label.derived")) + `</span></span> ` + strings.Join(links, ", ") + `</div>`)
	}
	return b.String()
}

// entryRealizes reports whether a node's hard references name a rule.
func (s *Site) entryRealizes(entry, rule *resolve.Node) bool {
	for _, x := range s.specLinks()[rule]["hard"] {
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
