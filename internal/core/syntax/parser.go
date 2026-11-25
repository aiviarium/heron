package syntax

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Parse parses a Heron file. A name ending in ".heron.md" is a prose file.
func Parse(name, src string) *Tree {
	p := &parser{src: src}
	if strings.HasSuffix(name, ".heron.md") {
		p.proseFile()
	} else {
		m := p.mark()
		p.members(stopEOF)
		p.wrap(m, File)
	}
	root := p.elems[0].(*Node)
	finish(root, nil, 0)
	return &Tree{Name: name, Src: src, Root: root, Diags: p.diags}
}

const maxDepth = 256

type parser struct {
	src   string
	pos   int
	elems []Elem
	diags []Diagnostic
	depth int
	fence string // the marker of the heron fence being parsed

	// Offsets where a speculative parse already failed, so retries stay linear.
	failed map[failKey]bool
}

type failKey struct {
	pos  int
	what byte
}

func (p *parser) failedAt(what byte) bool { return p.failed[failKey{p.pos, what}] }

func (p *parser) markFailed(pos int, what byte) {
	if p.failed == nil {
		p.failed = map[failKey]bool{}
	}
	p.failed[failKey{pos, what}] = true
}

// --- builder ---

func (p *parser) mark() int { return len(p.elems) }

// wrap turns the elements since m into one node of kind k.
func (p *parser) wrap(m int, k Kind) *Node {
	ch := make([]Elem, len(p.elems)-m)
	copy(ch, p.elems[m:])
	n := &Node{Kind: k, Children: ch}
	p.elems = append(p.elems[:m], n)
	return n
}

// tok emits the next n bytes as a token of kind k. Adjacent text tokens merge.
func (p *parser) tok(k Kind, n int) {
	if n <= 0 {
		return
	}
	if k == Text || k == StringText || k == Url || k == Error {
		if l := len(p.elems); l > 0 {
			if t, ok := p.elems[l-1].(*Token); ok && t.Kind == k && t.End() == p.pos {
				t.Text = p.src[t.Start : p.pos+n]
				p.pos += n
				return
			}
		}
	}
	p.elems = append(p.elems, &Token{Kind: k, Text: p.src[p.pos : p.pos+n], Start: p.pos})
	p.pos += n
}

func (p *parser) errAt(start, end int, msg string) {
	if len(p.diags) > 0 {
		last := p.diags[len(p.diags)-1]
		if last.Start == start && last.Message == msg {
			return
		}
	}
	p.diags = append(p.diags, Diagnostic{Start: start, End: end, Message: msg})
}

func (p *parser) err(msg string) { p.errAt(p.pos, p.pos, msg) }

type state struct {
	pos, elems, diags int
}

func (p *parser) save() state { return state{p.pos, len(p.elems), len(p.diags)} }

// restore undoes everything since s. Tokens merged since s are unmerged by
// trimming the merged token back to the saved position.
func (p *parser) restore(s state) {
	p.pos = s.pos
	p.elems = p.elems[:s.elems]
	p.diags = p.diags[:s.diags]
	if l := len(p.elems); l > 0 {
		if t, ok := p.elems[l-1].(*Token); ok && t.End() > s.pos {
			t.Text = p.src[t.Start:s.pos]
		}
	}
}

// --- characters ---

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek(off int) byte {
	if p.pos+off < len(p.src) && p.pos+off >= 0 {
		return p.src[p.pos+off]
	}
	return 0
}

func (p *parser) at(s string) bool { return strings.HasPrefix(p.src[p.pos:], s) }

func (p *parser) atNewline() bool {
	c := p.peek(0)
	return c == '\n' || (c == '\r' && p.peek(1) == '\n')
}

func (p *parser) atLineEnd() bool { return p.eof() || p.atNewline() }

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isIdentChar(r rune) bool  { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func (p *parser) runeAt(off int) (rune, int) {
	if off >= len(p.src) {
		return 0, 0
	}
	return utf8.DecodeRuneInString(p.src[off:])
}

func (p *parser) identLenAt(off int) int {
	r, w := p.runeAt(off)
	if w == 0 || !isIdentStart(r) {
		return 0
	}
	n := w
	for {
		r, w := p.runeAt(off + n)
		if w == 0 || !isIdentChar(r) {
			return n
		}
		n += w
	}
}

func (p *parser) identLen() int { return p.identLenAt(p.pos) }

// word reports whether the identifier at the cursor is exactly w.
func (p *parser) word(w string) bool { return p.identLen() == len(w) && p.at(w) }

// atLineStart reports whether only spaces stand between the cursor and the
// start of its line.
func (p *parser) atLineStart() bool {
	for i := p.pos - 1; i >= 0; i-- {
		switch p.src[i] {
		case ' ', '\t':
			continue
		case '\n':
			return true
		}
		return false
	}
	return true
}

// --- trivia ---

// ws consumes spaces and tabs.
func (p *parser) ws() {
	n := 0
	for p.pos+n < len(p.src) && (p.src[p.pos+n] == ' ' || p.src[p.pos+n] == '\t') {
		n++
	}
	p.tok(Whitespace, n)
}

func (p *parser) newline() bool {
	switch {
	case p.at("\r\n"):
		p.tok(Newline, 2)
	case p.at("\n"):
		p.tok(Newline, 1)
	default:
		return false
	}
	return true
}

func (p *parser) atComment() bool { return p.at("//") && !p.at("///") }

func (p *parser) comment() {
	n := strings.IndexAny(p.src[p.pos:], "\r\n")
	if n < 0 {
		n = len(p.src) - p.pos
	}
	p.tok(Comment, n)
}

// trivia consumes spaces, newlines and comments: the layout inside brackets.
// It stops at `///`, which starts a parameter's documentation.
func (p *parser) trivia() {
	for !p.eof() {
		start := p.pos
		p.ws()
		if p.atComment() {
			p.comment()
		}
		p.newline()
		if p.pos == start {
			return
		}
	}
}

// lineTrivia consumes spaces and a trailing comment, but no newline.
func (p *parser) lineTrivia() {
	p.ws()
	if p.atComment() {
		p.comment()
	}
}

// endLine finishes a member or statement: the rest of its line must be
// empty. Anything left is wrapped in an error node.
func (p *parser) endLine(closers string) {
	p.lineTrivia()
	if p.atLineEnd() || strings.IndexByte(closers, p.peek(0)) >= 0 && p.peek(0) != 0 {
		return
	}
	p.err("expected the end of the line")
	p.errorToLineEnd()
}

// errorToLineEnd wraps the rest of the line in an ErrorNode, keeping
// brackets balanced so a stray `{` doesn't swallow the file.
func (p *parser) errorToLineEnd() {
	m := p.mark()
	for !p.atLineEnd() {
		_, w := p.runeAt(p.pos)
		p.tok(Error, w)
	}
	p.wrap(m, ErrorNode)
}

func (p *parser) enter() bool {
	p.depth++
	if p.depth > maxDepth {
		p.err("nesting is too deep")
		return false
	}
	return true
}

func (p *parser) leave() { p.depth-- }
