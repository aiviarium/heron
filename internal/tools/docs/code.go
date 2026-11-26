package docs

import (
	"strings"
	"unicode"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// codeBlock writes a code block with a title bar and a copy button.
func (s *Site) codeBlock(lang, title, code string) string {
	var body string
	switch lang {
	case "heron", "":
		if lang == "heron" || looksHeron(code) {
			body = s.highlightSource(code)
		} else {
			body = esc(code)
		}
	default:
		body = esc(code)
	}
	return s.codeFrame(lang, title, body, code)
}

// codeFrame wraps highlighted code in the block's frame.
func (s *Site) codeFrame(lang, title, body, raw string) string {
	var b strings.Builder
	b.WriteString(`<figure class="code">`)
	head := title
	if head == "" {
		head = lang
	}
	b.WriteString(`<figcaption class="code-title"><span>` + esc(head) + `</span>`)
	b.WriteString(`<button type="button" class="icon-btn copy" data-island="copy" aria-label="` + attr(label("docs.action.copy")) + `">` + iconCopy + `</button></figcaption>`)
	b.WriteString(`<pre><code>` + body + `</code></pre></figure>`)
	return b.String()
}

func looksHeron(code string) bool {
	return strings.Contains(code, ": ") && (strings.Contains(code, "{") || strings.Contains(code, "("))
}

// highlightInline highlights inline code that reads as a Heron path or
// signature, and escapes anything else.
func (s *Site) highlightInline(code string) string {
	return esc(code)
}

// highlightSource highlights Heron source by its tokens.
func (s *Site) highlightSource(code string) string {
	t := syntax.Parse("snippet.heron", code)
	if t == nil || t.Root == nil {
		return esc(code)
	}
	var b strings.Builder
	s.highlight(&b, t.Root, resolve.Site{}, nil)
	return b.String()
}

// highlightDecl highlights a declaration of the tree, linking every name
// that resolves to a documented node.
func (s *Site) highlightDecl(site resolve.Site, skipDoc bool) string {
	var b strings.Builder
	skip := func(n *syntax.Node) bool { return skipDoc && n.Kind == syntax.Doc }
	s.highlight(&b, site.N, site, skip)
	return strings.TrimSpace(dedent(b.String(), declIndent(site)))
}

// declIndent is the indentation of the line a declaration starts on, which
// its later lines carry too.
func declIndent(site resolve.Site) int {
	if site.File == nil || site.N == nil {
		return 0
	}
	start, _ := site.Span()
	src := site.File.Src
	if start > len(src) {
		return 0
	}
	line := strings.LastIndexByte(src[:start], '\n') + 1
	n := 0
	for line+n < len(src) && src[line+n] == ' ' {
		n++
	}
	return n
}

// dedent removes indent spaces from the start of every line after the
// first, where a line has them, so a nested declaration reads as written
// at the left margin.
func dedent(text string, indent int) string {
	if indent == 0 {
		return text
	}
	pad := strings.Repeat(" ", indent)
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.TrimPrefix(lines[i], pad)
	}
	return strings.Join(lines, "\n")
}

func (s *Site) highlight(b *strings.Builder, n *syntax.Node, site resolve.Site, skip func(*syntax.Node) bool) {
	var prev *syntax.Token
	var walk func(n *syntax.Node)
	walk = func(n *syntax.Node) {
		if skip != nil && skip(n) {
			return
		}
		for _, ch := range n.Children {
			switch ch := ch.(type) {
			case *syntax.Node:
				if ch.Kind == syntax.Path && site.N != nil {
					if target := s.Checker.Refs[ch]; target != nil && own(target) && s.URL(target) != "" {
						cls := tokenClass(ch.Children[0].(interface{ ElemKind() syntax.Kind }), prev, ch)
						b.WriteString(s.linkHTML(target, `<span class="`+cls+`">`+esc(ch.Text())+`</span>`, "code-ref"))
						continue
					}
				}
				walk(ch)
			case *syntax.Token:
				cls := tokenClassTok(ch, prev, n)
				if cls == "" {
					b.WriteString(esc(ch.Text))
				} else {
					b.WriteString(`<span class="` + cls + `">` + esc(ch.Text) + `</span>`)
				}
				if !ch.Kind.IsTrivia() {
					prev = ch
				}
			}
		}
	}
	walk(n)
}

func tokenClass(_ interface{ ElemKind() syntax.Kind }, prev *syntax.Token, path *syntax.Node) string {
	if prev != nil && prev.Kind == syntax.At {
		return "tk-ann"
	}
	t := path.Text()
	if t != "" && unicode.IsUpper([]rune(t)[0]) {
		return "tk-type"
	}
	return "tk-name"
}

func tokenClassTok(t *syntax.Token, prev *syntax.Token, parent *syntax.Node) string {
	switch t.Kind {
	case syntax.Comment:
		return "tk-comment"
	case syntax.KwLet, syntax.KwDo, syntax.KwIf, syntax.KwElse, syntax.KwFor, syntax.KwIn, syntax.KwReturn, syntax.KwNot, syntax.KwAnd, syntax.KwOr, syntax.KwIs, syntax.KwTrue, syntax.KwFalse:
		return "tk-kw"
	case syntax.Quote, syntax.StringText, syntax.Raw:
		return "tk-str"
	case syntax.Escape:
		return "tk-str"
	case syntax.Number:
		return "tk-num"
	case syntax.At:
		return "tk-ann"
	case syntax.DocPrefix:
		return "tk-comment"
	case syntax.Pipe:
		// A prose line's marker is punctuation; its text is prose, which
		// reads at the text colour, as prose does outside code.
		return "tk-punct"
	case syntax.Text, syntax.CodeSpan:
		if parent != nil && (parent.Kind == syntax.DocLine) {
			return "tk-comment"
		}
		return "tk-prose"
	case syntax.Ident:
		if prev != nil && prev.Kind == syntax.At {
			return "tk-ann"
		}
		if parent != nil && parent.Kind == syntax.Flags {
			return "tk-kw"
		}
		if unicode.IsUpper([]rune(t.Text)[0]) {
			return "tk-type"
		}
		if parent != nil && parent.Kind == syntax.Path && parent.Parent != nil {
			switch parent.Parent.Kind {
			case syntax.NamedType:
				return "tk-kw"
			case syntax.Annotation:
				return "tk-ann"
			}
		}
		return "tk-name"
	case syntax.LBrace, syntax.RBrace, syntax.LParen, syntax.RParen, syntax.LBracket, syntax.RBracket, syntax.Comma, syntax.Colon, syntax.Dot, syntax.Question, syntax.Eq, syntax.FlagOpen, syntax.FlagClose, syntax.LBrace2, syntax.RBrace2:
		return "tk-punct"
	}
	return ""
}

// exprHTML renders a value expression as written, linking paths.
func (s *Site) exprHTML(e ast.Expr, site resolve.Site) string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	s.highlight(&b, e.Syntax(), site, nil)
	return strings.Join(strings.Fields(b.String()), " ")
}

// typeHTML renders a type as written, every name linked.
func (s *Site) typeHTML(t ast.Type, site resolve.Site) string {
	if t == nil {
		return ""
	}
	var b strings.Builder
	s.highlight(&b, t.Syntax(), site, nil)
	return strings.Join(strings.Fields(b.String()), " ")
}

const iconCopy = `<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><rect x="5.5" y="5.5" width="8" height="8" rx="1.5" fill="none" stroke="currentColor"/><path d="M10.5 3.5v-.5a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 3v5A1.5 1.5 0 0 0 4 9.5h.5" fill="none" stroke="currentColor"/></svg>`
