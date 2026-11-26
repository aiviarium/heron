package docs

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// Prose is linearised into text with placeholders for the inline forms
// (links, inserts, tags, fences, code spans and escapes), parsed into
// Markdown's blocks, and the placeholders replaced by their HTML.

const ph = '\x00'

type proseBuf struct {
	s     *Site
	site  resolve.Site
	b     strings.Builder
	parts []string // the HTML of each placeholder
	block map[int]bool
}

func (p *proseBuf) place(h string, block bool) {
	i := len(p.parts)
	p.parts = append(p.parts, h)
	if block {
		if p.block == nil {
			p.block = map[int]bool{}
		}
		p.block[i] = true
	}
	p.b.WriteRune(ph)
	p.b.WriteString(strconv.Itoa(i))
	p.b.WriteRune(ph)
}

// proseHTML renders a node's `///` docs, its prose and a prose value.
func (s *Site) proseHTML(n *resolve.Node) string {
	var out []string
	if s.kept(n) {
		for _, d := range n.Docs {
			if h := s.renderText([]resolve.Site{d}); h != "" {
				out = append(out, h)
			}
		}
	}
	var runs []resolve.Site
	for _, p := range n.Prose {
		if s.runShown(n, p) {
			runs = append(runs, p)
		}
	}
	for _, r := range runs {
		h := s.renderText([]resolve.Site{r})
		if h == "" {
			continue
		}
		if t := s.markTags(lens.ProseRestriction(s.Checker, r)); t != "" {
			h = `<div class="marked">` + t + h + `</div>`
		}
		out = append(out, h)
	}
	if !s.kept(n) {
		return strings.Join(out, "")
	}
	if n.Value != nil {
		sn := proseValue(n.Value.Syntax())
		for _, p := range n.Prose {
			if p.N == sn {
				// `kind(…) {{ … }}` is the node's prose, not a value too.
				sn = nil
			}
		}
		if sn != nil {
			out = append(out, s.renderText([]resolve.Site{{File: n.ValueSite.File, N: sn, Scope: n.ValueSite.Scope}}))
		}
	}
	return strings.Join(out, "")
}

// proseValue returns the ProseLines or ProseBlock a value is written as.
func proseValue(n *syntax.Node) *syntax.Node {
	if n == nil {
		return nil
	}
	if n.Kind == syntax.ProseLines || n.Kind == syntax.ProseBlock {
		return n
	}
	var found *syntax.Node
	n.Walk(func(x *syntax.Node) bool {
		if found == nil && (x.Kind == syntax.ProseLines || x.Kind == syntax.ProseBlock) {
			found = x
		}
		return found == nil
	})
	return found
}

// plainText returns the text of a node's prose without markup, for search
// and hover cards.
func (s *Site) plainText(n *resolve.Node) string {
	h := s.proseHTML(n)
	return strings.Join(strings.Fields(stripTags(h)), " ")
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(h string) string {
	return htmlUnescape(tagRe.ReplaceAllString(h, " "))
}

// renderText renders prose sites as HTML blocks.
func (s *Site) renderText(sites []resolve.Site) string {
	var out strings.Builder
	for _, site := range sites {
		if site.N == nil {
			continue
		}
		p := &proseBuf{s: s, site: site}
		p.container(site.N)
		out.WriteString(p.blocks())
	}
	return out.String()
}

// inlineHTML renders prose as one run of inline HTML, for a requirement's
// condition that joins a sentence.
func (s *Site) inlineHTML(n *resolve.Node) string {
	var b strings.Builder
	for _, site := range n.Prose {
		if !s.runShown(n, site) {
			continue
		}
		p := &proseBuf{s: s, site: site}
		p.container(site.N)
		text := strings.Join(strings.Fields(p.b.String()), " ")
		b.WriteString(p.inline(text))
	}
	if n.Value != nil {
		sn := proseValue(n.Value.Syntax())
		for _, p := range n.Prose {
			if p.N == sn {
				// `kind(…) {{ … }}` is the node's prose, not a value too.
				sn = nil
			}
		}
		if sn != nil {
			p := &proseBuf{s: s, site: resolve.Site{File: n.ValueSite.File, N: sn, Scope: n.ValueSite.Scope}}
			p.container(sn)
			b.WriteString(p.inline(strings.Join(strings.Fields(p.b.String()), " ")))
		}
	}
	return strings.TrimSpace(b.String())
}

// container linearises a prose container's children.
func (p *proseBuf) container(n *syntax.Node) {
	switch n.Kind {
	case syntax.Link, syntax.Insert, syntax.Tag, syntax.Fence:
		p.inlineNode(n)
		return
	}
	lineStart := true
	for _, ch := range n.Children {
		switch ch := ch.(type) {
		case *syntax.Token:
			switch ch.Kind {
			case syntax.Text:
				t := ch.Text
				if lineStart {
					t = strings.TrimPrefix(t, " ")
				}
				p.b.WriteString(t)
				lineStart = false
			case syntax.Whitespace:
				if !lineStart {
					p.b.WriteString(ch.Text)
				}
			case syntax.Newline:
				// Between the lines of a `///` doc, each DocLine has
				// ended its own line already; another newline would make
				// every doc line a paragraph of its own.
				if n.Kind != syntax.Doc {
					p.b.WriteString("\n")
				}
				lineStart = true
			case syntax.CodeSpan:
				code := strings.Trim(ch.Text, "`")
				p.place(`<code>`+p.s.highlightInline(code)+`</code>`, false)
				lineStart = false
			case syntax.Escape:
				p.place(esc(strings.TrimPrefix(ch.Text, `\`)), false)
				lineStart = false
			case syntax.HeadingMark, syntax.HeadingId:
				// Headings in prose files declare sections.
			}
		case *syntax.Node:
			switch ch.Kind {
			case syntax.ProseLine, syntax.DocLine, syntax.ProseLines, syntax.ProseBlock, syntax.Doc:
				p.container(ch)
				if ch.Kind == syntax.DocLine {
					p.b.WriteString("\n")
				}
				lineStart = true
			case syntax.Heading:
			default:
				p.inlineNode(ch)
				lineStart = false
			}
		}
	}
}

// inlineNode renders a link, insert, tag or fence into a placeholder.
func (p *proseBuf) inlineNode(n *syntax.Node) {
	switch n.Kind {
	case syntax.Link:
		p.place(p.link(ast.Link{N: n}), false)
	case syntax.Insert:
		p.place(p.insert(ast.Insert{N: n}), false)
	case syntax.Tag:
		h, block := p.tag(ast.Tag{N: n})
		p.place(h, block)
	case syntax.Fence:
		p.place(p.fence(n), true)
	}
}

func (p *proseBuf) sub(children []syntax.Elem) string {
	q := &proseBuf{s: p.s, site: p.site}
	q.container(&syntax.Node{Kind: syntax.ProseLine, Children: children})
	return q.inline(strings.Join(strings.Fields(q.b.String()), " "))
}

func (p *proseBuf) link(l ast.Link) string {
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
			text = esc(u.Text)
		}
		return `<a class="ext" href="` + attr(u.Text) + `">` + text + `</a>`
	}
	t := p.s.refTarget(l.Path().N, p.site)
	if t == nil {
		if text == "" {
			text = esc(strings.Join(resolve.PathSegs(l.Path().N), "."))
		}
		return `<span class="ref ref-broken">` + text + `</span>`
	}
	if text == "" {
		text = esc(displayName(p.s, t))
	}
	if strength := p.s.ruleStrength(t); strength != "" {
		text += ` <span class="lvl">` + esc(kindWords(strength)) + `</span>`
	}
	return p.s.proseLinkHTML(t, text)
}

func (p *proseBuf) insert(in ast.Insert) string {
	path := in.Path().N
	if path == nil {
		// `${path.to.node.field}`: an expression that's a path names a
		// spec fact, shown as its value.
		if pe, ok := in.Expr().(ast.PathExpr); ok {
			path = pe.Path().N
		}
	}
	if path != nil {
		t := p.s.refTarget(path, p.site)
		if t != nil && t.Value != nil {
			if sn := proseValue(t.Value.Syntax()); sn != nil {
				return p.s.inlineHTML(t)
			}
			return `<code class="value">` + p.s.factHTML(t.Value, t.ValueSite) + `</code>`
		}
		if t != nil {
			return p.s.linkHTML(t, `<code>`+esc(strings.Join(resolve.PathSegs(path), "."))+`</code>`, "")
		}
		if v := p.s.factValue(resolve.PathSegs(path), p.site); v != "" {
			return `<code class="value">` + v + `</code>`
		}
	}
	return `<code class="value">` + esc(strings.TrimPrefix(in.N.Text(), "$")) + `</code>`
}

// tag renders a markup component through the renderer of its kind.
func (p *proseBuf) tag(t ast.Tag) (string, bool) {
	segs := resolve.PathSegs(t.Path().N)
	kind := p.s.refTarget(t.Path().N, p.site)
	name := strings.Join(segs, ".")
	if kind != nil {
		name = kind.Path()
	}
	var inner []syntax.Elem
	started := false
	for _, ch := range t.N.Children {
		if tok, ok := ch.(*syntax.Token); ok && (tok.Kind == syntax.TagEnd) && !started {
			started = true
			continue
		}
		if n, ok := ch.(*syntax.Node); ok && n.Kind == syntax.TagClose {
			break
		}
		if started {
			inner = append(inner, ch)
		}
	}
	attrs := map[string]string{}
	for _, a := range t.TagAttrs() {
		var key, val string
		for _, ch := range a.N.Children {
			switch ch := ch.(type) {
			case *syntax.Token:
				if ch.Kind == syntax.Ident && key == "" {
					key = ch.Text
				}
			case *syntax.Node:
				if v, ok := resolve.StringValue(ch); ok {
					val = v
				} else {
					val = ch.Text()
				}
			}
		}
		attrs[key] = val
	}
	body := p.sub(inner)
	if r := p.s.tagRenderer(name); r != nil {
		return r(body, attrs)
	}
	return `<span class="tag tag-` + attr(segs[len(segs)-1]) + `">` + body + `</span>`, false
}

func (p *proseBuf) fence(n *syntax.Node) string {
	lines := syntax.FenceLines(n)
	if len(lines) == 0 {
		return ""
	}
	// Remove the common indentation, including the space after `|`.
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
	for i, l := range lines {
		if len(l) >= indent && indent > 0 {
			lines[i] = l[indent:]
		}
	}
	open := strings.TrimSpace(lines[0])
	lang := strings.TrimSpace(strings.TrimLeft(open, "`~"))
	body := lines[1:]
	if len(body) > 0 && strings.HasPrefix(strings.TrimSpace(body[len(body)-1]), "```") {
		body = body[:len(body)-1]
	}
	code := strings.Join(body, "\n")
	title := p.s.fenceTitle
	p.s.fenceTitle = ""
	return p.s.codeBlock(lang, title, code)
}

// --- blocks ---

var (
	orderedRe = regexp.MustCompile(`^(\d+)[.)]\s+`)
	tableSep  = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`)
)

// blocks parses the linearised text into Markdown's blocks.
func (p *proseBuf) blocks() string {
	lines := strings.Split(p.b.String(), "\n")
	var out strings.Builder
	var para []string
	flush := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + p.inline(strings.Join(para, " ")) + "</p>")
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], " \t")
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			flush()
		case p.isBlockPlaceholder(t):
			flush()
			out.WriteString(p.replace(t))
		case strings.HasPrefix(t, "|") && i+1 < len(lines) && tableSep.MatchString(strings.TrimSpace(lines[i+1])):
			flush()
			var rows []string
			rows = append(rows, t)
			i += 2
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				rows = append(rows, strings.TrimSpace(lines[i]))
			}
			i--
			out.WriteString(p.table(rows))
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || orderedRe.MatchString(t):
			flush()
			ordered := orderedRe.MatchString(t)
			var items []string
			for ; i < len(lines); i++ {
				x := strings.TrimSpace(lines[i])
				switch {
				case !ordered && (strings.HasPrefix(x, "- ") || strings.HasPrefix(x, "* ")):
					items = append(items, x[2:])
				case ordered && orderedRe.MatchString(x):
					items = append(items, orderedRe.ReplaceAllString(x, ""))
				case x != "" && len(items) > 0 && lines[i] != strings.TrimLeft(lines[i], " "):
					items[len(items)-1] += " " + x
				default:
					goto done
				}
			}
		done:
			i--
			tag := "ul"
			if ordered {
				tag = "ol"
			}
			out.WriteString("<" + tag + ">")
			for _, it := range items {
				out.WriteString("<li>" + p.inline(it) + "</li>")
			}
			out.WriteString("</" + tag + ">")
		case strings.HasPrefix(t, ">"):
			flush()
			var q []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				q = append(q, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
			}
			i--
			out.WriteString("<blockquote><p>" + p.inline(strings.Join(q, " ")) + "</p></blockquote>")
		default:
			para = append(para, t)
		}
	}
	flush()
	return out.String()
}

func (p *proseBuf) isBlockPlaceholder(t string) bool {
	if len(t) < 3 || t[0] != ph || t[len(t)-1] != ph {
		return false
	}
	i, err := strconv.Atoi(t[1 : len(t)-1])
	return err == nil && p.block[i]
}

func (p *proseBuf) table(rows []string) string {
	// A row splits at each pipe not escaped as `\|`; as in GitHub's tables,
	// `\|` is a pipe in the cell, inside a code span too.
	cells := func(r string) []string {
		r = strings.TrimSpace(r)
		r = strings.TrimPrefix(r, "|")
		if strings.HasSuffix(r, "|") && !strings.HasSuffix(r, `\|`) {
			r = r[:len(r)-1]
		}
		for _, m := range phRe.FindAllStringSubmatch(r, -1) {
			if i, _ := strconv.Atoi(m[1]); i < len(p.parts) {
				p.parts[i] = strings.ReplaceAll(p.parts[i], `\|`, "|")
			}
		}
		var parts []string
		cur := strings.Builder{}
		for i := 0; i < len(r); i++ {
			switch {
			case r[i] == '\\' && i+1 < len(r) && r[i+1] == '|':
				cur.WriteByte('|')
				i++
			case r[i] == '|':
				parts = append(parts, strings.TrimSpace(cur.String()))
				cur.Reset()
			default:
				cur.WriteByte(r[i])
			}
		}
		return append(parts, strings.TrimSpace(cur.String()))
	}
	var b strings.Builder
	b.WriteString(`<div class="table-wrap"><table><thead><tr>`)
	for _, c := range cells(rows[0]) {
		b.WriteString("<th>" + p.inline(c) + "</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, r := range rows[1:] {
		b.WriteString("<tr>")
		for _, c := range cells(r) {
			b.WriteString("<td>" + p.inline(c) + "</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></div>")
	return b.String()
}

var (
	strongRe = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	emRe     = regexp.MustCompile(`(^|[^\w*])[*_]([^*_\s][^*_]*)[*_]([^\w*]|$)`)
	phRe     = regexp.MustCompile("\x00(\\d+)\x00")
)

// inline renders a run of text: escaping, emphasis, then placeholders.
func (p *proseBuf) inline(t string) string {
	t = esc(t)
	t = strongRe.ReplaceAllString(t, "<strong>$1</strong>")
	t = emRe.ReplaceAllString(t, "$1<em>$2</em>$3")
	return p.replace(t)
}

func (p *proseBuf) replace(t string) string {
	return phRe.ReplaceAllStringFunc(t, func(m string) string {
		i, _ := strconv.Atoi(m[1 : len(m)-1])
		if i < len(p.parts) {
			return p.parts[i]
		}
		return ""
	})
}

func htmlUnescape(s string) string {
	r := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "&#34;", `"`, "&#39;", "'", "&quot;", `"`)
	return r.Replace(s)
}

// factValue is the value of a spec fact a path names past its last node:
// a field or a kind argument of that node, as `sync.dry_run.default`.
func (s *Site) factValue(segs []string, site resolve.Site) string {
	if len(segs) < 2 {
		return ""
	}
	n := s.lookup(site, segs[:len(segs)-1])
	if n == nil {
		return ""
	}
	name := segs[len(segs)-1]
	if ch := n.Child(name); ch != nil && ch.Value != nil {
		return s.factHTML(ch.Value, ch.ValueSite)
	}
	args := kindArgs(n)
	if e := namedArg(args, name); e != nil {
		return s.factHTML(e, n.Decl)
	}
	names := s.paramNames(s.Checker.KindOf(n))
	for i, e := range argExprs(args) {
		if i < len(names) && names[i] == name {
			return s.factHTML(e, n.Decl)
		}
	}
	// The parameter's default, when the node passes none.
	if k := s.Checker.KindOf(n); k != nil {
		for _, p := range s.Checker.KindParams(k, n) {
			if p.Name == name && p.N != nil {
				if d := (ast.Param{N: p.N}).Default(); d != nil {
					return s.factHTML(d, k.Decl)
				}
			}
		}
	}
	return ""
}

// factHTML is an inserted value: a string as its text, without quotes,
// anything else as written.
func (s *Site) factHTML(e ast.Expr, site resolve.Site) string {
	if v, ok := resolve.StringValue(e.Syntax()); ok {
		return esc(v)
	}
	return s.exprHTML(e, site)
}
