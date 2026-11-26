// Package prose is the prose view: it projects the prose of a checked
// tree's own files into regions a text linter can read, keeping headings,
// lists and paragraphs, masking code, inserts, links to nodes and tag
// attributes, and mapping every projected character back to its bytes.
package prose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// Region is a projected block of prose.
type Region = registry.ProseRegion

// Project returns the regions of every file the tree reads itself (not its
// packages), in file order and then source order.
func Project(t *resolve.Tree, c *types.Checker) []*Region {
	owners := map[*syntax.Node]*resolve.Node{}
	t.Walk(func(n *resolve.Node) {
		if n.Shape == resolve.ShapeMounted {
			return
		}
		for _, s := range append(append([]resolve.Site{n.Decl}, n.Sites...), n.Prose...) {
			if s.N != nil && owners[s.N] == nil {
				owners[s.N] = n
			}
		}
	})
	p := &projector{c: c, owners: owners}
	for _, f := range t.Files {
		p.file = f
		if f.Tree == nil || f.Tree.Root == nil {
			continue
		}
		if f.Tree.Root.Kind == syntax.ProseFile {
			p.proseFile(f.Tree.Root, t.Root)
		} else {
			p.walk(f.Tree.Root, t.Root)
		}
	}
	return p.out
}

type projector struct {
	c      *types.Checker
	owners map[*syntax.Node]*resolve.Node
	file   *resolve.File
	out    []*Region
	group  int

	// the open block and its context
	cur    *Region
	owner  *resolve.Node
	source string
	// a pending join: the source range between two lines of one block
	gapFrom, gapTo int
	gap            bool
}

func (p *projector) walk(n *syntax.Node, owner *resolve.Node) {
	if o := p.owners[n]; o != nil {
		owner = o
		if p.c.VerbatimText(o) {
			// Verbatim text, such as a production, isn't prose; its
			// docs are.
			for _, e := range n.Children {
				if c, ok := e.(*syntax.Node); ok && c.Kind == syntax.Doc {
					p.walk(c, owner)
				}
			}
			return
		}
	}
	switch n.Kind {
	case syntax.DoBlock, syntax.Code:
		return
	case syntax.Doc:
		p.begin(owner, "doc")
		p.prefixed(nodes(n, syntax.DocLine), syntax.DocPrefix)
		p.end()
		return
	case syntax.ProseLines:
		src := "prose"
		if isValue(n) {
			src = "value"
		}
		p.begin(owner, src)
		p.prefixed(nodes(n, syntax.ProseLine), syntax.Pipe)
		p.end()
		return
	case syntax.ProseBlock:
		src := "block"
		if isValue(n) {
			src = "value"
		}
		p.begin(owner, src)
		var inner []syntax.Elem
		for _, e := range n.Children {
			if k := e.ElemKind(); k != syntax.LBrace2 && k != syntax.RBrace2 {
				inner = append(inner, e)
			}
		}
		p.lines(inner)
		p.end()
		return
	case syntax.StringLit:
		if p.markupString(n, owner) {
			p.begin(owner, "string")
			p.newBlock("paragraph", 0, false)
			for _, e := range n.Children {
				if e.ElemKind() == syntax.Quote {
					continue
				}
				if t, ok := e.(*syntax.Token); ok && t.Kind == syntax.StringText {
					p.markupText(t.Text, t.Start)
					continue
				}
				p.inline(e)
			}
			p.end()
		}
		return
	}
	for _, e := range n.Children {
		if c, ok := e.(*syntax.Node); ok {
			p.walk(c, owner)
		}
	}
}

// markupString reports whether a string literal is the value of a node
// typed as prose.
func (p *projector) markupString(n *syntax.Node, owner *resolve.Node) bool {
	if owner == nil || n.Parent == nil || p.owners[n.Parent] != owner {
		return false
	}
	if k := n.Parent.Kind; k != syntax.Value && k != syntax.Set {
		return false
	}
	d := p.c.Meta(owner)
	return resolve.IsBuiltin(d) && d.Name == "Prose"
}

func isValue(n *syntax.Node) bool {
	if n.Parent == nil {
		return false
	}
	switch n.Parent.Kind {
	case syntax.Set, syntax.Value, syntax.NamedArg, syntax.PositionalArg, syntax.Invoke:
		return true
	}
	return false
}

// proseFile projects a `.heron.md` file: headings open the sections they
// declare, and text belongs to the section it's under.
func (p *projector) proseFile(root *syntax.Node, owner *resolve.Node) {
	var run []syntax.Elem
	flush := func() {
		if len(run) > 0 {
			p.lines(run)
			run = nil
		}
	}
	p.begin(owner, "file")
	for _, e := range root.Children {
		n, isNode := e.(*syntax.Node)
		switch {
		case isNode && n.Kind == syntax.Heading:
			flush()
			p.end()
			if o := p.owners[n]; o != nil {
				owner = o
			}
			p.begin(owner, "file")
			level := strings.Count(strings.TrimSpace(n.Children[0].(*syntax.Token).Text), "#")
			p.newBlock("heading", level, false)
			first := true
			for _, c := range n.Children[1:] {
				if c.ElemKind() == syntax.HeadingId || c.ElemKind().IsTrivia() {
					continue
				}
				if first {
					c = trimLeft(c)
					first = false
				}
				if c != nil {
					p.inline(c)
				}
			}
			p.close()
		case isNode && n.Kind == syntax.HeronFence:
			flush()
			p.close()
			p.walk(n, owner)
		default:
			run = append(run, e)
		}
	}
	flush()
	p.end()
}

// prefixed projects `|` or `///` lines: consecutive lines form one
// paragraph and an empty line separates paragraphs.
func (p *projector) prefixed(lines []*syntax.Node, prefix syntax.Kind) {
	para := true
	prevEnd := -1
	for _, l := range lines {
		var es []syntax.Elem
		for _, e := range l.Children {
			if k := e.ElemKind(); k != prefix && !k.IsTrivia() {
				es = append(es, e)
			}
		}
		if len(es) == 1 {
			if t, ok := es[0].(*syntax.Token); ok && strings.TrimSpace(t.Text) == "" {
				es = nil
			}
		}
		if len(es) == 0 {
			p.close()
			para, prevEnd = true, -1
			continue
		}
		if prevEnd >= 0 {
			first := es[0]
			if t := trimLeft(first); t != nil {
				first = t
			}
			s, _ := first.Span()
			p.setGap(prevEnd, s)
		}
		p.line(es, para)
		para = false
		_, prevEnd = es[len(es)-1].Span()
	}
}

// begin starts a run of text belonging to a node.
func (p *projector) begin(owner *resolve.Node, source string) {
	p.close()
	p.owner, p.source = owner, source
	p.group++
}

func (p *projector) end() { p.close() }

// lines projects elements of free-flowing text, where a newline ends a line
// and a blank line ends a paragraph.
func (p *projector) lines(elems []syntax.Elem) {
	var line []syntax.Elem
	prevEnd := -1
	para := true
	for _, e := range elems {
		if e.ElemKind().IsTrivia() {
			continue
		}
		s, _ := e.Span()
		if prevEnd >= 0 {
			between := p.file.Src[prevEnd:s]
			if nl := strings.Count(between, "\n"); nl > 0 {
				p.line(line, para)
				line = nil
				para = nl > 1
				p.setGap(prevEnd, s)
			}
		}
		line = append(line, e)
		_, prevEnd = e.Span()
	}
	if len(line) > 0 {
		p.line(line, para)
	}
}

var (
	itemRe    = regexp.MustCompile(`^([-*+]|\d+[.)])( \[[ xX]\])? +`)
	quoteRe   = regexp.MustCompile(`^> ?`)
	dividerRe = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})\s*$`)
)

// line projects one line of text. An empty line ends the paragraph; a line
// that starts with a list or quote marker starts a block.
func (p *projector) line(elems []syntax.Elem, para bool) {
	var es []syntax.Elem
	for _, e := range elems {
		if !e.ElemKind().IsTrivia() {
			es = append(es, e)
		}
	}
	if len(es) > 0 {
		es[0] = trimLeft(es[0])
		if es[0] == nil {
			es = es[1:]
		}
	}
	if len(es) == 0 {
		p.close()
		return
	}
	if t, ok := es[0].(*syntax.Token); ok && t.Kind == syntax.Text {
		switch {
		case len(es) == 1 && dividerRe.MatchString(t.Text):
			p.close()
			return
		case itemRe.MatchString(t.Text):
			m := itemRe.FindString(t.Text)
			p.close()
			p.newBlock("item", 0, m[0] >= '0' && m[0] <= '9')
			es[0] = cut(t, len(m))
		case quoteRe.MatchString(t.Text):
			m := quoteRe.FindString(t.Text)
			if p.cur == nil || p.cur.Block != "quote" {
				p.close()
				p.newBlock("quote", 0, false)
			}
			es[0] = cut(t, len(m))
		case para:
			p.close()
		}
	} else if para {
		p.close()
	}
	prevEnd := -1
	for _, e := range es {
		if e == nil {
			continue
		}
		s, end := e.Span()
		if prevEnd >= 0 && s > prevEnd {
			p.setGap(prevEnd, s)
		}
		p.inline(e)
		prevEnd = end
	}
}

// inline projects one element of a line.
func (p *projector) inline(e syntax.Elem) {
	if t, ok := e.(*syntax.Token); ok {
		switch t.Kind {
		case syntax.Text, syntax.StringText:
			p.text(t.Text, t.Start)
		case syntax.CodeSpan, syntax.Escape:
			p.mask(t.Text, t.Start, "code")
		case syntax.Newline:
			// A line break inside an element's content reads as a space,
			// mapped to the break.
			p.setGap(t.Start, t.Start+len(t.Text))
		case syntax.FenceLine:
			p.mask(t.Text, t.Start, "code")
		}
		return
	}
	n := e.(*syntax.Node)
	s, end := n.Span()
	switch n.Kind {
	case syntax.Insert:
		p.mask(p.file.Src[s:end], s, "insert")
	case syntax.ShortLink:
		p.mask(p.file.Src[s:end], s, "link")
	case syntax.Link:
		url := false
		for _, c := range n.Children {
			if c.ElemKind() == syntax.Url {
				url = true
			}
		}
		if !url {
			p.mask(p.file.Src[s:end], s, "link")
			return
		}
		for _, c := range n.Children {
			if c.ElemKind() == syntax.LinkMid {
				break
			}
			if c.ElemKind() != syntax.LinkOpen && !c.ElemKind().IsTrivia() {
				p.inline(c)
			}
		}
	case syntax.Tag:
		open := s
		i := 0
		for ; i < len(n.Children); i++ {
			k := n.Children[i].ElemKind()
			if k == syntax.TagEnd || k == syntax.TagSelfEnd {
				_, open = n.Children[i].Span()
				i++
				break
			}
		}
		if open == s {
			p.mask(p.file.Src[s:end], s, "tag")
			return
		}
		p.mask(p.file.Src[s:open], s, "tag")
		for ; i < len(n.Children); i++ {
			c := n.Children[i]
			if c.ElemKind() == syntax.Newline {
				// A line break inside an element's content reads as a space.
				p.inline(c)
				continue
			}
			if c.ElemKind().IsTrivia() {
				continue
			}
			if c.ElemKind() == syntax.TagClose {
				cs, ce := c.Span()
				p.mask(p.file.Src[cs:ce], cs, "tag")
				continue
			}
			p.inline(c)
		}
	case syntax.Fence:
		p.close()
		p.newBlock("code", 0, false)
		p.mask(p.file.Src[s:end], s, "code")
		p.close()
	default:
		p.mask(p.file.Src[s:end], s, "code")
	}
}

func (p *projector) newBlock(kind string, level int, ordered bool) {
	p.close()
	path, k := "", ""
	if p.owner != nil {
		path = p.owner.Path()
		if kd := p.c.KindOf(p.owner); kd != nil {
			k = kd.Name
		} else if td := p.c.Meta(p.owner); td != nil {
			k = td.Name
		}
	}
	p.cur = &Region{File: string(p.file.ID), Node: path, Kind: k, Source: p.source, Block: kind, Level: level, Ordered: ordered, Group: p.group, Owner: p.owner, Start: -1}
	p.gap = false
}

func (p *projector) setGap(from, to int) {
	if p.cur != nil && len(p.cur.Text) > 0 {
		p.gapFrom, p.gapTo, p.gap = from, to, true
	}
}

func (p *projector) add(text string, at int, mask string) {
	if p.cur == nil {
		p.newBlock("paragraph", 0, false)
	}
	r := p.cur
	if p.gap && len(r.Text) > 0 {
		gapText := p.file.Src[p.gapFrom:p.gapTo]
		if strings.Trim(gapText, " \t") == "" {
			r.Map = append(r.Map, registry.ProseSeg{At: len(r.Text), Len: len(gapText), Src: p.gapFrom, SrcLen: len(gapText), Exact: true})
			r.Text += gapText
		} else {
			r.Map = append(r.Map, registry.ProseSeg{At: len(r.Text), Len: 1, Src: p.gapFrom, SrcLen: p.gapTo - p.gapFrom})
			r.Text += " "
		}
	}
	p.gap = false
	if r.Start < 0 {
		r.Start = at
	}
	r.End = at + len(text)
	if mask != "" {
		r.Masks = append(r.Masks, registry.ProseMask{Start: len(r.Text), End: len(r.Text) + len(text), Kind: mask})
	}
	r.Map = append(r.Map, registry.ProseSeg{At: len(r.Text), Len: len(text), Src: at, SrcLen: len(text), Exact: true})
	r.Text += text
}

func (p *projector) text(s string, at int) {
	if s == "" {
		return
	}
	if p.cur == nil || len(p.cur.Text) == 0 {
		t := strings.TrimLeft(s, " \t")
		at += len(s) - len(t)
		s = t
		if s == "" {
			return
		}
	}
	p.add(s, at, "")
}

func (p *projector) mask(s string, at int, kind string) { p.add(s, at, kind) }

// markupText projects the text of a markup-typed string, which the parser
// leaves as plain string text, masking its inline code.
func (p *projector) markupText(s string, at int) {
	for s != "" {
		i := strings.IndexByte(s, '`')
		if i < 0 {
			p.text(s, at)
			return
		}
		j := strings.IndexByte(s[i+1:], '`')
		if j < 0 {
			p.text(s, at)
			return
		}
		p.text(s[:i], at)
		p.mask(s[i:i+j+2], at+i, "code")
		s, at = s[i+j+2:], at+i+j+2
	}
}

// close finishes the open block, trimming trailing space, and keeps it if
// it holds any text.
func (p *projector) close() {
	r := p.cur
	p.cur, p.gap = nil, false
	if r == nil {
		return
	}
	t := strings.TrimRight(r.Text, " \t")
	if t == "" {
		return
	}
	if len(t) < len(r.Text) {
		n := len(t)
		var m []registry.ProseSeg
		for _, s := range r.Map {
			if s.At >= n {
				continue
			}
			if s.At+s.Len > n {
				cutBy := s.At + s.Len - n
				s.Len -= cutBy
				if s.Exact {
					s.SrcLen -= cutBy
				}
			}
			m = append(m, s)
		}
		r.Map, r.Text = m, t
		if last := m[len(m)-1]; last.Exact {
			r.End = last.Src + last.SrcLen
		}
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%t\x00%v\x00%s", r.Node, r.Kind, r.Source, r.Block, r.Level, r.Ordered, r.Masks, r.Text)))
	r.Hash = hex.EncodeToString(h[:16])
	p.out = append(p.out, r)
}

func nodes(n *syntax.Node, k syntax.Kind) []*syntax.Node {
	var out []*syntax.Node
	for _, c := range n.Children {
		if c, ok := c.(*syntax.Node); ok && c.Kind == k {
			out = append(out, c)
		}
	}
	return out
}

// trimLeft drops the leading space of a text token, returning nil for a
// token of only space.
func trimLeft(e syntax.Elem) syntax.Elem {
	t, ok := e.(*syntax.Token)
	if !ok || t.Kind != syntax.Text {
		return e
	}
	s := strings.TrimLeft(t.Text, " \t")
	if s == "" {
		return nil
	}
	return &syntax.Token{Kind: t.Kind, Text: s, Start: t.Start + len(t.Text) - len(s)}
}

func cut(t *syntax.Token, n int) syntax.Elem {
	if n >= len(t.Text) {
		return nil
	}
	return &syntax.Token{Kind: t.Kind, Text: t.Text[n:], Start: t.Start + n}
}
