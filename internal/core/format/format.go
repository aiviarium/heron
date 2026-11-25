// Package format formats Heron files from their lossless syntax tree.
//
// Formatting normalizes layout only: each line's indentation follows from
// nesting (two spaces per level), trailing spaces are removed, runs of blank
// lines become one, and a file ends in exactly one newline. Spacing within a
// line is kept, so hand-aligned columns survive, and literal text (strings,
// raw strings, fenced code, and the relative indentation of `{{ }}` prose) is
// never changed. Formatting is idempotent.
package format

import (
	"strings"

	"github.com/helightdev/specs-v0/internal/core/syntax"
)

// Source formats src, a file called name. Files with syntax errors are
// formatted too; the tree is lossless, so nothing is lost.
func Source(name, src string) string {
	tr := syntax.Parse(name, src)
	return Tree(tr)
}

// Tree formats a parsed file.
func Tree(tr *syntax.Tree) string {
	f := &fmter{src: tr.Src}
	f.collect(tr.Root)
	if tr.Root.Kind == syntax.ProseFile {
		f.push(frame{kind: frameVerbatim})
	}
	f.node(tr.Root)
	return f.output()
}

type frameKind int

const (
	frameBracket  frameKind = iota // content of ( [ {: opener's line + 2
	frameSame                      // lines of a member or prose run: its first line's indent
	frameCont                      // a value continuing on later lines: start + 2
	frameRelative                  // {{ }} content: shifted, relative indentation kept
	frameVerbatim                  // prose files: keep indentation as written
	frameFence                     // a heron fence in a prose file: members at column 0
)

type frame struct {
	kind   frameKind
	indent int // the indentation lines in this frame get
	closer syntax.Kind
	min    int // frameRelative: the content's common indentation
}

type line struct {
	ws       *syntax.Token // leading whitespace, if any
	first    syntax.Elem   // first token after it (nil for a blank line at EOF)
	indent   int           // the decided indentation, or -1 to keep
	blank    bool
	keepWS   bool // verbatim: keep the original whitespace
	collapse bool // a blank line that may be removed when repeated
}

type fmter struct {
	src    string
	toks   []*syntax.Token
	starts map[*syntax.Token]int // first token of a line (after leading ws) -> line index
	lines  []*line
	frames []frame
	cur    int // indentation of the line being visited
}

// collect finds the lines: every token after a Newline (or the first token)
// starts one.
func (f *fmter) collect(root *syntax.Node) {
	root.Tokens(func(t *syntax.Token) { f.toks = append(f.toks, t) })
	f.starts = map[*syntax.Token]int{}
	atStart := true
	for i := 0; i < len(f.toks); i++ {
		if !atStart {
			if f.toks[i].Kind == syntax.Newline {
				atStart = true
			}
			continue
		}
		l := &line{indent: -1}
		if f.toks[i].Kind == syntax.Whitespace {
			l.ws = f.toks[i]
			i++
		}
		if i >= len(f.toks) || f.toks[i].Kind == syntax.Newline {
			l.blank = true
			l.collapse = true
			f.lines = append(f.lines, l)
			if i >= len(f.toks) {
				break
			}
			continue // the newline keeps atStart true
		}
		l.first = f.toks[i]
		f.starts[f.toks[i]] = len(f.lines)
		f.lines = append(f.lines, l)
		atStart = false
	}
}

func (f *fmter) push(fr frame) { f.frames = append(f.frames, fr) }
func (f *fmter) pop()          { f.frames = f.frames[:len(f.frames)-1] }

func (f *fmter) top() *frame {
	if len(f.frames) == 0 {
		return nil
	}
	return &f.frames[len(f.frames)-1]
}

func firstToken(n *syntax.Node) *syntax.Token {
	for _, c := range n.Children {
		switch c := c.(type) {
		case *syntax.Token:
			return c
		case *syntax.Node:
			if t := firstToken(c); t != nil {
				return t
			}
		}
	}
	return nil
}

// decide sets the indentation of the line that t starts, if it starts one.
func (f *fmter) decide(t *syntax.Token) {
	idx, ok := f.starts[t]
	if !ok {
		return
	}
	l := f.lines[idx]
	fr := f.top()
	orig := 0
	if l.ws != nil {
		orig = len(l.ws.Text)
	}
	switch {
	case fr == nil:
		l.indent = 0
	case fr.kind == frameVerbatim:
		l.keepWS = true
		l.collapse = false
		l.indent = orig
	case fr.kind == frameRelative:
		if t.Kind == syntax.RBrace2 {
			l.indent = fr.indent - 2
		} else {
			l.indent = fr.indent + max(0, orig-fr.min)
		}
	case fr.kind == frameBracket && t.Kind == fr.closer:
		l.indent = fr.indent - 2
	default:
		l.indent = fr.indent
	}
	f.cur = l.indent
	// Blank lines inside prose and fences are content.
	if fr != nil && (fr.kind == frameRelative || fr.kind == frameVerbatim) {
		for j := idx - 1; j >= 0 && f.lines[j].blank; j-- {
			f.lines[j].collapse = false
		}
	}
}

var openers = map[syntax.Kind]syntax.Kind{
	syntax.LBrace:      syntax.RBrace,
	syntax.LParen:      syntax.RParen,
	syntax.LBracket:    syntax.RBracket,
	syntax.DollarBrace: syntax.RBrace,
}

func isMember(k syntax.Kind) bool {
	switch k {
	case syntax.Declare, syntax.Value, syntax.Enter, syntax.TextNode, syntax.Scope, syntax.Invoke,
		syntax.Set, syntax.Let, syntax.Action, syntax.ProseLines, syntax.Doc, syntax.CommentLine,
		syntax.LetStmt, syntax.AssignStmt, syntax.ReturnStmt, syntax.ExprStmt, syntax.IfExpr,
		syntax.Param, syntax.NamedArg, syntax.MapEntry, syntax.Tag:
		return true
	}
	return false
}

func hasEq(k syntax.Kind) bool {
	switch k {
	case syntax.Set, syntax.Value, syntax.Let, syntax.LetStmt, syntax.AssignStmt, syntax.NamedArg,
		syntax.MapEntry, syntax.Param, syntax.Invoke:
		return true
	}
	return false
}

func (f *fmter) node(n *syntax.Node) {
	if t := firstToken(n); t != nil {
		f.decide(t)
	}
	pushed := 0
	switch {
	case n.Kind == syntax.HeronFence:
		f.push(frame{kind: frameFence, indent: 0})
		pushed++
	case n.Kind == syntax.ProseBlock:
		f.push(frame{kind: frameRelative, indent: f.cur + 2, min: f.minIndent(n)})
		pushed++
	case isMember(n.Kind):
		f.push(frame{kind: frameSame, indent: f.cur})
		pushed++
	}
	start := f.cur
	for i, c := range n.Children {
		switch c := c.(type) {
		case *syntax.Node:
			f.node(c)
		case *syntax.Token:
			f.token(n, c)
			if closer, ok := openers[c.Kind]; ok && n.Kind != syntax.ProseBlock {
				f.push(frame{kind: frameBracket, indent: f.cur + 2, closer: closer})
			}
			// A value that starts on a later line than its `=`, or an
			// operator at the end of a line, continues at start + 2.
			if (c.Kind == syntax.Eq && hasEq(n.Kind)) || (n.Kind == syntax.BinaryExpr && isOp(c.Kind)) {
				if f.endsLine(n, i) {
					f.push(frame{kind: frameCont, indent: start + 2})
					pushed++
				}
			}
		}
	}
	for ; pushed > 0; pushed-- {
		f.pop()
	}
}

func (f *fmter) token(n *syntax.Node, t *syntax.Token) {
	if fr := f.top(); fr != nil && fr.kind == frameBracket && t.Kind == fr.closer {
		f.decide(t)
		f.pop()
		return
	}
	f.decide(t)
}

func isOp(k syntax.Kind) bool {
	switch k {
	case syntax.EqEq, syntax.NotEq, syntax.Lt, syntax.LtEq, syntax.Gt, syntax.GtEq, syntax.Plus,
		syntax.Minus, syntax.Star, syntax.Slash, syntax.Percent, syntax.KwAnd, syntax.KwOr,
		syntax.KwIn, syntax.KwIs:
		return true
	}
	return false
}

// endsLine reports whether only trivia follows child i of n on its line.
func (f *fmter) endsLine(n *syntax.Node, i int) bool {
	for _, c := range n.Children[i+1:] {
		t, ok := c.(*syntax.Token)
		if !ok {
			if ft := firstToken(c.(*syntax.Node)); ft != nil {
				_, starts := f.starts[ft]
				return starts
			}
			continue
		}
		switch t.Kind {
		case syntax.Whitespace, syntax.Comment:
			continue
		case syntax.Newline:
			return true
		}
		return false
	}
	return false
}

// minIndent returns the common indentation of a {{ }} block's content
// lines, ignoring blank lines and the closing line.
func (f *fmter) minIndent(n *syntax.Node) int {
	min := -1
	afterNL, ws := false, 0
	n.Tokens(func(t *syntax.Token) {
		switch {
		case t.Kind == syntax.Newline:
			afterNL, ws = true, 0
		case afterNL && t.Kind == syntax.Whitespace:
			ws = len(t.Text)
		case afterNL:
			if t.Kind != syntax.RBrace2 && (min < 0 || ws < min) {
				min = ws
			}
			afterNL = false
		}
	})
	if min < 0 {
		return 0
	}
	return min
}

func (f *fmter) output() string {
	lineOf := map[*syntax.Token]*line{}
	for _, l := range f.lines {
		if l.ws != nil {
			lineOf[l.ws] = l
		}
		if l.first != nil {
			if _, ok := lineOf[l.first.(*syntax.Token)]; !ok {
				lineOf[l.first.(*syntax.Token)] = l
			}
		}
	}
	var res []string
	cur := strings.Builder{}
	flush := func() {
		res = append(res, cur.String())
		cur.Reset()
	}
	for _, t := range f.toks {
		if t.Kind == syntax.Newline {
			flush()
			continue
		}
		if l, ok := lineOf[t]; ok && t == l.ws {
			if l.blank {
				continue
			}
			if l.keepWS || l.indent < 0 {
				cur.WriteString(t.Text)
			} else {
				cur.WriteString(strings.Repeat(" ", l.indent))
			}
			continue
		}
		if l, ok := lineOf[t]; ok && l.ws == nil && !l.blank && l.indent > 0 && !l.keepWS {
			cur.WriteString(strings.Repeat(" ", l.indent))
		}
		cur.WriteString(t.Text)
	}
	flush()
	// Trailing spaces, outside verbatim lines, and blank-line runs.
	var lines []string
	collapsible := map[int]bool{}
	for i, s := range res {
		if i < len(f.lines) && f.lines[i].keepWS {
			lines = append(lines, s)
			continue
		}
		lines = append(lines, strings.TrimRight(s, " \t"))
		if i < len(f.lines) && f.lines[i].blank && f.lines[i].collapse {
			collapsible[len(lines)-1] = true
		}
	}
	var keep []string
	for i, s := range lines {
		if s == "" && collapsible[i] {
			if len(keep) == 0 || keep[len(keep)-1] == "" {
				continue
			}
		}
		keep = append(keep, s)
	}
	for len(keep) > 0 && keep[len(keep)-1] == "" {
		keep = keep[:len(keep)-1]
	}
	if len(keep) == 0 {
		return ""
	}
	return strings.Join(keep, "\n") + "\n"
}
