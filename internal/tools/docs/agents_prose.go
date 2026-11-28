package docs

import (
	"regexp"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// Prose as Markdown, for the agent export: the prose's own Markdown is
// kept as written, and its links, inserts, elements and code blocks are
// written in Markdown, each link to the file of the export that holds its
// target.

// mdLink writes a link to a node: a relative link to the file of the
// export that holds it, with its stable id as the fragment, or, for a node
// the export doesn't hold, its text and its id in code.
func (s *Site) mdLink(t *resolve.Node, text string) string {
	if t == nil {
		return text
	}
	text = strings.TrimSpace(text)
	if text == "" {
		text = displayName(s, t)
	}
	if s.mdHref != nil {
		if h := s.mdHref(t); h != "" {
			return "[" + mdLinkText(text) + "](" + h + ")"
		}
	}
	id := stableID(t)
	if strings.Contains(text, "`"+id+"`") || text == id {
		return text
	}
	return text + " (`" + id + "`)"
}

// mdLinkText keeps a link's text from closing the link early.
func mdLinkText(t string) string { return strings.NewReplacer("[", `\[`, "]", `\]`).Replace(t) }

// stableID is a node's id in the agent export: its path inside its
// package, or its path in the tree.
func stableID(n *resolve.Node) string {
	if n == nil {
		return ""
	}
	if n.Decl.File != nil && n.Decl.File.ID.IsStd() {
		return n.Path()
	}
	return relPathOf(n)
}

func (p *proseBuf) mdInlineNode(n *syntax.Node) {
	switch n.Kind {
	case syntax.Link:
		p.place(p.mdLinkNode(ast.Link{N: n}), false)
	case syntax.ShortLink:
		p.place(p.mdShortLink(ast.ShortLink{N: n}), false)
	case syntax.Insert:
		p.place(p.mdInsert(ast.Insert{N: n}), false)
	case syntax.Tag:
		if p.s.Tree.ElementNode(n) != nil {
			return
		}
		if n != p.site.N {
			if r := lens.ElementRestriction(p.s.Checker, resolve.Site{File: p.site.File, N: n, Scope: p.site.Scope}); r != nil {
				if !p.s.explore() && !p.s.Opts.Lens.Allows(r) {
					return
				}
			}
		}
		p.place(p.mdTag(ast.Tag{N: n}), false)
	case syntax.Fence:
		p.place(p.mdFence(n), true)
	}
}

func (p *proseBuf) mdLinkNode(l ast.Link) string {
	var inner []syntax.Elem
	in := false
	for _, ch := range l.N.Children {
		if t, ok := ch.(*syntax.Token); ok {
			if t.Kind == syntax.LinkOpen {
				in = true
				continue
			}
			if t.Kind == syntax.LinkMid {
				break
			}
		}
		if in {
			inner = append(inner, ch)
		}
	}
	text := p.sub(inner)
	if u := l.Url(); u != nil {
		if text == "" {
			text = u.Text
		}
		return "[" + text + "](" + u.Text + ")"
	}
	t := p.s.refTarget(l.Path().N, p.site)
	if t == nil {
		if text == "" {
			text = strings.Join(resolve.PathSegs(l.Path().N), ".")
		}
		return text
	}
	return p.s.mdLink(t, text)
}

func (p *proseBuf) mdShortLink(l ast.ShortLink) string {
	path := l.Path().N
	if path == nil {
		return ""
	}
	name := strings.Join(resolve.PathSegs(path), ".")
	t := p.s.refTarget(path, p.site)
	text := name
	switch {
	case len(l.LinkTicks()) > 0:
		text = "`" + name + "`"
	case t != nil:
		text = sentenceWords(p.s, t)
	}
	if t == nil {
		return text
	}
	return p.s.mdLink(t, text)
}

func (p *proseBuf) mdInsert(in ast.Insert) string {
	path := in.Path().N
	if path == nil {
		if pe, ok := in.Expr().(ast.PathExpr); ok {
			path = pe.Path().N
		}
	}
	if path != nil {
		t := p.s.refTarget(path, p.site)
		if t != nil && t.Value != nil {
			if sn := proseValue(t.Value.Syntax()); sn != nil {
				return p.s.inlineMD(t)
			}
			return "`" + plainOf(p.s.factHTML(t.Value, t.ValueSite)) + "`"
		}
		if t != nil {
			return p.s.mdLink(t, "`"+strings.Join(resolve.PathSegs(path), ".")+"`")
		}
		if v := p.s.factValue(resolve.PathSegs(path), p.site); v != "" {
			return "`" + plainOf(v) + "`"
		}
	}
	return "`" + strings.TrimPrefix(in.N.Text(), "$") + "`"
}

// plainOf is rendered HTML as its text.
func plainOf(h string) string { return strings.TrimSpace(stripTagsTight(h)) }

func stripTagsTight(h string) string { return htmlUnescape(tagRe.ReplaceAllString(h, "")) }

// mdTag writes a markup element as its content: the element form of a
// link as the link, and any other element as the Markdown of its text.
func (p *proseBuf) mdTag(t ast.Tag) string {
	segs := resolve.PathSegs(t.Path().N)
	kind := p.s.refTarget(t.Path().N, p.site)
	name := strings.Join(segs, ".")
	if kind != nil {
		name = kind.Path()
	}
	inner := tagContent(t.N)
	if name == "doc.ref" {
		for _, a := range t.TagAttrs() {
			if a.Ident() == nil || a.Ident().Text != "to" || a.StringLit().N == nil {
				continue
			}
			v, _ := resolve.StringValue(a.StringLit().N)
			target := p.s.Checker.Refs[a.StringLit().N]
			text := v
			if inner := p.sub(inner); strings.TrimSpace(inner) != "" {
				text = inner
			} else if target != nil {
				text = sentenceWords(p.s, target)
			}
			return p.s.mdLink(target, text)
		}
	}
	return p.sub(inner)
}

func (p *proseBuf) mdFence(n *syntax.Node) string {
	lines := syntax.FenceLines(n)
	if len(lines) == 0 {
		return ""
	}
	return "\n" + strings.Join(fenceBody(lines), "\n") + "\n"
}

// fenceBody is a fence's lines, its opening and closing lines included,
// with their common indentation removed.
func fenceBody(lines []string) []string {
	indent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		k := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent < 0 || k < indent {
			indent = k
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if indent > 0 && len(l) >= indent {
			l = l[indent:]
		}
		out[i] = strings.TrimRight(l, " \t")
	}
	if len(out) > 0 && !strings.HasPrefix(strings.TrimSpace(out[len(out)-1]), "```") && !strings.HasPrefix(strings.TrimSpace(out[len(out)-1]), "~~~") {
		out = append(out, "```")
	}
	return out
}

var blankRuns = regexp.MustCompile(`\n{3,}`)

// mdBlocks is the linearised text with its placeholders written, its
// lines' trailing space and runs of blank lines removed.
func (p *proseBuf) mdBlocks() string {
	t := p.replace(p.b.String())
	lines := strings.Split(t, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	t = strings.Join(lines, "\n")
	t = blankRuns.ReplaceAllString(t, "\n\n")
	return strings.TrimSpace(t)
}

// proseMD is a node's `///` docs, its prose and its prose value, as
// Markdown blocks.
func (s *Site) proseMD(n *resolve.Node) string {
	var out []string
	add := func(site resolve.Site) {
		if site.N == nil {
			return
		}
		p := &proseBuf{s: s, site: site, md: true}
		p.container(site.N)
		if t := p.mdBlocks(); t != "" {
			out = append(out, t)
		}
	}
	if s.kept(n) {
		for _, d := range n.Docs {
			add(d)
		}
	}
	for _, r := range n.Prose {
		if s.runShown(n, r) {
			add(r)
		}
	}
	if s.kept(n) && n.Value != nil {
		sn := proseValue(n.Value.Syntax())
		for _, p := range n.Prose {
			if p.N == sn {
				sn = nil
			}
		}
		if sn != nil {
			add(resolve.Site{File: n.ValueSite.File, N: sn, Scope: n.ValueSite.Scope})
		}
	}
	return strings.Join(out, "\n\n")
}

// inlineMD is a node's prose as one line of Markdown, for a condition
// that joins a sentence.
func (s *Site) inlineMD(n *resolve.Node) string {
	var parts []string
	add := func(site resolve.Site) {
		p := &proseBuf{s: s, site: site, md: true}
		p.container(site.N)
		if t := p.inline(strings.Join(strings.Fields(p.b.String()), " ")); t != "" {
			parts = append(parts, t)
		}
	}
	for _, site := range n.Prose {
		if site.N != nil && s.runShown(n, site) {
			add(site)
		}
	}
	if n.Value != nil {
		sn := proseValue(n.Value.Syntax())
		for _, p := range n.Prose {
			if p.N == sn {
				sn = nil
			}
		}
		if sn != nil {
			add(resolve.Site{File: n.ValueSite.File, N: sn, Scope: n.ValueSite.Scope})
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// rangeMD is the part of a prose site between two offsets as one line of
// Markdown.
func (s *Site) rangeMD(site resolve.Site, start, end int) string {
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
	p := &proseBuf{s: s, site: site, md: true}
	return p.sub(elems)
}
