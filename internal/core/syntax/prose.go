package syntax

import "strings"

// Strings and prose.

// stringLit parses a double-quoted string: one quote, or a run of three or
// more that closes at a run of the same length. `$name` and `${expr}`
// interpolate; `\x` is an escape.
func (p *parser) stringLit() {
	m := p.mark()
	k := p.runOf('"')
	if k == 2 {
		p.tok(Quote, 1)
		p.tok(Quote, 1)
		p.wrap(m, StringLit)
		return
	}
	p.tok(Quote, k)
	for {
		if p.eof() {
			p.err("unterminated string")
			break
		}
		c := p.peek(0)
		if c == '"' {
			run := p.runOf('"')
			if k == 1 {
				p.tok(Quote, 1)
				break
			}
			if run >= k {
				p.tok(StringText, run-k)
				p.tok(Quote, k)
				break
			}
			p.tok(StringText, run)
			continue
		}
		if k == 1 && p.atNewline() {
			p.err("unterminated string")
			break
		}
		if c == '\\' && p.pos+1 < len(p.src) {
			_, w := p.runeAt(p.pos + 1)
			p.tok(Escape, 1+w)
			continue
		}
		if c == '$' && p.insertAhead() {
			p.insert()
			continue
		}
		n := 0
		for p.pos+n < len(p.src) {
			c := p.src[p.pos+n]
			if c == '"' || c == '\\' || c == '$' || (k == 1 && (c == '\n' || c == '\r')) {
				break
			}
			n++
		}
		if n == 0 {
			n = 1
		}
		p.tok(StringText, n)
	}
	p.wrap(m, StringLit)
}

func (p *parser) runOf(c byte) int {
	n := 0
	for p.pos+n < len(p.src) && p.src[p.pos+n] == c {
		n++
	}
	return n
}

// raw parses a backtick string, which closes at a run of the same length.
func (p *parser) raw() {
	k := p.runOf('`')
	if k == 2 {
		p.tok(Raw, 2)
		return
	}
	o := p.pos + k
	for o < len(p.src) {
		if p.src[o] == '`' {
			n := 0
			for o+n < len(p.src) && p.src[o+n] == '`' {
				n++
			}
			if n == k {
				p.tok(Raw, o+n-p.pos)
				return
			}
			o += n
			continue
		}
		if k == 1 && p.src[o] == '\n' {
			break
		}
		o++
	}
	p.err("unterminated raw string")
	if o > len(p.src) {
		o = len(p.src)
	}
	if k == 1 && o < len(p.src) && o > 0 && p.src[o-1] == '\r' {
		o--
	}
	p.tok(Raw, o-p.pos)
}

func (p *parser) insertAhead() bool {
	return p.peek(1) == '{' || p.identLenAt(p.pos+1) > 0
}

// insert parses `$path(args)?` or `${expr}`. Arguments that don't parse
// are left as text, so `get_$name(` in a template keeps its parenthesis.
func (p *parser) insert() {
	m := p.mark()
	if p.peek(1) == '{' {
		p.tok(DollarBrace, 2)
		p.trivia()
		p.expr()
		p.trivia()
		if p.peek(0) == '}' {
			p.tok(RBrace, 1)
		} else {
			p.err("expected `}`")
		}
		p.wrap(m, Insert)
		return
	}
	p.tok(Dollar, 1)
	p.path(false)
	if p.peek(0) == '(' && !p.failedAt('(') {
		s := p.save()
		if p.argList() || len(p.diags) > s.diags {
			p.restore(s)
			p.markFailed(s.pos, '(')
		}
	}
	p.wrap(m, Insert)
}

// --- prose ---

type inlineCtx struct {
	prefix string // "|" or "///" in line mode; "" in block and file mode
	block  bool   // stop at an unescaped `}}`
	file   bool   // a prose file: headings and heron fences at line starts
	single bool   // stay on one line (link text, headings)
	tags   int    // open tags: stop at `</`
	link   int    // open link text: stop at `]`
	stopAt int    // stop at this offset when > 0 (a heading's {#id})
}

// proseLines parses consecutive `|` lines without wrapping them.
func (p *parser) proseLines() {
	for {
		lm := p.mark()
		p.tok(Pipe, 1)
		p.inlineLine("|")
		p.wrap(lm, ProseLine)
		save := p.save()
		if p.newline() {
			p.ws()
			if p.peek(0) == '|' {
				continue
			}
		}
		p.restore(save)
		return
	}
}

// inlineLine parses the content of one `|` or `///` line; tags and fences
// may continue onto following lines with the same prefix.
func (p *parser) inlineLine(prefix string) {
	o := p.pos + p.spacesAt(p.pos)
	if fenceLen(p.src[o:]) > 0 {
		p.lineFence(prefix)
		return
	}
	p.inlines(&inlineCtx{prefix: prefix})
}

// contLine reports whether the next line continues prose with prefix.
func (p *parser) contLine(prefix string) bool {
	o := p.pos
	if strings.HasPrefix(p.src[o:], "\r\n") {
		o += 2
	} else if strings.HasPrefix(p.src[o:], "\n") {
		o++
	} else {
		return false
	}
	o += p.spacesAt(o)
	if !strings.HasPrefix(p.src[o:], prefix) {
		return false
	}
	return prefix != "|" || !strings.HasPrefix(p.src[o:], "||")
}

func (p *parser) prefixTok(prefix string) {
	if prefix == "|" {
		p.tok(Pipe, 1)
	} else {
		p.tok(DocPrefix, 3)
	}
}

// fenceLen returns the length of a ``` or ~~~ marker at the start of s.
func fenceLen(s string) int {
	if len(s) < 3 || (s[0] != '`' && s[0] != '~') {
		return 0
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 {
		return 0
	}
	return n
}

func restOfLine(s string, o int) int {
	n := strings.IndexAny(s[o:], "\r\n")
	if n < 0 {
		return len(s) - o
	}
	return n
}

// isFenceClose reports whether line closes a fence opened with marker.
func isFenceClose(line, marker string) bool {
	t := strings.TrimSpace(line)
	return len(t) >= len(marker) && strings.Trim(t, marker[:1]) == "" && t[0] == marker[0]
}

// lineFence parses a fenced block written in `|` or `///` lines.
func (p *parser) lineFence(prefix string) {
	m := p.mark()
	o := p.pos + p.spacesAt(p.pos)
	marker := p.src[o : o+fenceLen(p.src[o:])]
	p.tok(FenceLine, restOfLine(p.src, p.pos))
	for p.contLine(prefix) {
		p.newline()
		p.ws()
		p.prefixTok(prefix)
		n := restOfLine(p.src, p.pos)
		line := p.src[p.pos : p.pos+n]
		p.tok(FenceLine, n)
		if isFenceClose(line, marker) {
			break
		}
	}
	p.wrap(m, Fence)
}

// blockFence parses a fence inside a `{{ }}` body or a prose file. The
// cursor is at the marker, after the line's indentation.
func (p *parser) blockFence(cx *inlineCtx) {
	m := p.mark()
	marker := p.src[p.pos : p.pos+fenceLen(p.src[p.pos:])]
	p.tok(FenceLine, restOfLine(p.src, p.pos))
	for !p.eof() {
		save := p.save()
		if !p.newline() {
			break
		}
		p.ws()
		n := restOfLine(p.src, p.pos)
		line := p.src[p.pos : p.pos+n]
		if cx.block && strings.TrimSpace(line) == "}}" {
			p.restore(save)
			break
		}
		p.tok(FenceLine, n)
		if isFenceClose(line, marker) {
			break
		}
	}
	p.wrap(m, Fence)
}

// proseBlock parses `{{ … }}` without wrapping it.
func (p *parser) proseBlock() {
	p.tok(LBrace2, 2)
	cx := &inlineCtx{block: true}
	p.inlines(cx)
	if p.at("}}") {
		p.tok(RBrace2, 2)
	} else {
		p.err("expected `}}`")
	}
}

// verbatimKinds are the standard kinds that take their text as Verbatim:
// std/project's exports, std/grammar's production and token, and std/doc's code and math.
// The parser knows no kinds, so it knows them by name: a `{{ }}` body of a
// node whose `:` slot or callee names one, by its last segment, is read
// as text, with no inserts, links, tags, code spans or fences in it. The
// tree-sitter grammar reads the same names the same way.
var verbatimKinds = map[string]bool{"exports": true, "surface": true, "production": true, "token": true, "code": true, "math": true}

// lastSegment is the last name of a dotted path.
func lastSegment(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// pathLenAt is the length of the dotted path of names at off.
func (p *parser) pathLenAt(off int) int {
	o := off
	for {
		n := p.identLenAt(o)
		if n == 0 {
			break
		}
		o += n
		if o < len(p.src) && p.src[o] == '.' && p.segLenAt(o+1) > 0 {
			o++
			for p.identLenAt(o) == 0 && p.segLenAt(o) > 0 {
				o += p.segLenAt(o)
				if !(o < len(p.src) && p.src[o] == '.' && p.segLenAt(o+1) > 0) {
					break
				}
				o++
			}
			continue
		}
		break
	}
	return o - off
}

// body parses a `{{ }}` body: prose, or text when verbatim.
func (p *parser) body(verbatim bool) {
	if verbatim {
		p.verbatimBlock()
		return
	}
	p.proseBlock()
}

// verbatimBlock parses `{{ … }}` as text exactly as written; only `\}}`
// is an escape, so the text can hold `}}`.
func (p *parser) verbatimBlock() {
	p.tok(LBrace2, 2)
	for !p.eof() && !p.at("}}") {
		switch {
		case p.newline():
			// A line's indentation is whitespace, as in prose, so the
			// formatter indents the text as it does a prose body's.
			p.ws()
		case p.at("\\}}"):
			p.tok(Escape, 3)
		default:
			n := 0
			for o := p.pos; o < len(p.src); o++ {
				c := p.src[o]
				if c == '\n' || c == '\r' && o+1 < len(p.src) && p.src[o+1] == '\n' ||
					c == '}' && o+1 < len(p.src) && p.src[o+1] == '}' ||
					c == '\\' && strings.HasPrefix(p.src[o:], "\\}}") {
					break
				}
				n++
			}
			if n == 0 {
				n = 1
			}
			p.tok(Text, n)
		}
	}
	if p.at("}}") {
		p.tok(RBrace2, 2)
	} else {
		p.err("expected `}}`")
	}
}

// proseFile parses a .heron.md file.
func (p *parser) proseFile() {
	m := p.mark()
	cx := &inlineCtx{file: true}
	p.lineStart(cx)
	p.inlines(cx)
	p.wrap(m, ProseFile)
}

// lineStart handles what may begin a line in block and file mode: fences
// and, in prose files, headings and heron fences.
func (p *parser) lineStart(cx *inlineCtx) {
	p.ws()
	if n := fenceLen(p.src[p.pos:]); n > 0 {
		info := strings.TrimSpace(p.src[p.pos+n : p.pos+restOfLine(p.src, p.pos)])
		if cx.file && info == "heron" && cx.tags == 0 {
			// Inside an element a heron fence is code, as an example's.
			p.heronFence()
			return
		}
		p.blockFence(cx)
		return
	}
	if cx.file && p.peek(0) == '#' {
		n := p.runOf('#')
		if n <= 6 && (p.peek(n) == ' ' || p.peek(n) == '\t') {
			p.heading(n)
		}
	}
}

func (p *parser) heading(n int) {
	m := p.mark()
	p.tok(HeadingMark, n)
	start := p.pos
	end := p.pos + restOfLine(p.src, p.pos)
	line := strings.TrimRight(p.src[p.pos:end], " \t")
	id := -1
	if strings.HasSuffix(line, "}") {
		if i := strings.LastIndex(line, "{#"); i >= 0 {
			id = p.pos + i
		}
	}
	cx := &inlineCtx{single: true}
	if id >= 0 {
		cx.stopAt = id
	}
	p.inlines(cx)
	if id >= 0 && p.pos == id {
		p.tok(HeadingId, start+len(line)-id)
	}
	p.wrap(m, Heading)
}

func (p *parser) heronFence() {
	m := p.mark()
	marker := p.src[p.pos : p.pos+fenceLen(p.src[p.pos:])]
	p.tok(FenceLine, restOfLine(p.src, p.pos))
	outer := p.fence
	p.fence = marker
	p.members(stopFence)
	if p.atFenceClose() {
		p.ws()
		p.tok(FenceLine, restOfLine(p.src, p.pos))
	} else {
		p.err("expected the closing fence")
	}
	p.fence = outer
	p.wrap(m, HeronFence)
}

func (p *parser) atFenceClose() bool {
	if p.fence == "" {
		return false
	}
	o := p.pos + p.spacesAt(p.pos)
	line := p.src[o : o+restOfLine(p.src, o)]
	return isFenceClose(line, p.fence) && p.atLineStart()
}

// inlines parses inline prose until the context says to stop.
func (p *parser) inlines(cx *inlineCtx) {
	for !p.eof() {
		if cx.stopAt > 0 && p.pos >= cx.stopAt {
			return
		}
		if cx.tags > 0 && p.at("</") {
			return
		}
		if cx.link > 0 && p.peek(0) == ']' {
			return
		}
		if cx.block && p.at("}}") {
			return
		}
		if p.atNewline() {
			if cx.single {
				return
			}
			if cx.prefix != "" {
				if cx.tags > 0 && p.contLine(cx.prefix) {
					p.newline()
					p.ws()
					p.prefixTok(cx.prefix)
					continue
				}
				return
			}
			p.newline()
			p.lineStart(cx)
			continue
		}
		switch c := p.peek(0); {
		case c == '\\':
			switch {
			case p.at("\\}}"):
				p.tok(Escape, 3)
			case strings.IndexByte("$<[|]`\\*_#", p.peek(1)) >= 0 && p.peek(1) != 0:
				p.tok(Escape, 2)
			default:
				p.tok(Text, 1)
			}
		case c == '`':
			p.codeSpan()
		case c == '[':
			if !p.link(cx) && (cx.link > 0 || !p.shortLink()) {
				p.tok(Text, 1)
			}
		case c == '$' && p.insertAhead():
			p.insert()
		case c == '<' && p.identLenAt(p.pos+1) > 0:
			if !p.tag(cx) {
				p.tok(Text, 1)
			}
		default:
			n := 0
			for p.pos+n < len(p.src) {
				c := p.src[p.pos+n]
				if strings.IndexByte("\\`[]$<}\r\n", c) >= 0 {
					break
				}
				if cx.stopAt > 0 && p.pos+n >= cx.stopAt {
					break
				}
				n++
			}
			if n == 0 {
				_, n = p.runeAt(p.pos)
			}
			p.tok(Text, n)
		}
	}
}

// codeSpan parses inline code, which closes at a run of the same number of
// backticks on the same line; without one, the backticks are text.
func (p *parser) codeSpan() {
	k := p.runOf('`')
	end := p.pos + restOfLine(p.src, p.pos)
	for o := p.pos + k; o < end; {
		if p.src[o] != '`' {
			o++
			continue
		}
		n := 0
		for o+n < end && p.src[o+n] == '`' {
			n++
		}
		if n == k {
			p.tok(CodeSpan, o+n-p.pos)
			return
		}
		o += n
	}
	p.tok(Text, k)
}

// link parses `[text](#path)` or `[text](url)`, and reports false (with
// nothing consumed) when the brackets aren't a link.
func (p *parser) link(cx *inlineCtx) bool {
	if p.failedAt('[') || !p.linkAhead() {
		return false
	}
	s := p.save()
	defer func() {
		if p.pos == s.pos {
			p.markFailed(s.pos, '[')
		}
	}()
	m := p.mark()
	p.tok(LinkOpen, 1)
	inner := *cx
	inner.link++
	inner.single = true
	p.inlines(&inner)
	if !p.at("](") {
		p.restore(s)
		return false
	}
	p.tok(LinkMid, 2)
	if p.peek(0) == '#' {
		p.tok(Hash, 1)
		if p.identLen() > 0 {
			p.path(false)
		}
	} else {
		for !p.eof() && p.peek(0) != ')' {
			c := p.peek(0)
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				break
			}
			if c == '$' && p.insertAhead() {
				p.insert()
				continue
			}
			_, w := p.runeAt(p.pos)
			p.tok(Url, w)
		}
	}
	if p.peek(0) != ')' || len(p.diags) > s.diags {
		p.restore(s)
		return false
	}
	p.tok(LinkClose, 1)
	p.wrap(m, Link)
	return true
}

// byteAt is the byte at an offset of the source, or 0 past its end.
func (p *parser) byteAt(o int) byte {
	if o >= 0 && o < len(p.src) {
		return p.src[o]
	}
	return 0
}

// shortLinkLen reports the length of a short link at the cursor, `[name]`
// or “[`name`]“ with a name or a dotted path of names, not followed by
// `(`, and whether its path is in backticks; 0 when the brackets aren't one.
func (p *parser) shortLinkLen() (int, bool) {
	o := p.pos + 1
	tick := p.byteAt(o) == '`' && p.byteAt(o+1) != '`'
	if tick {
		o++
	}
	for {
		n := p.identLenAt(o)
		if n == 0 {
			return 0, false
		}
		o += n
		if p.byteAt(o) == '.' && p.segLenAt(o+1) > 0 {
			o++
			if p.identLenAt(o) == 0 {
				o += p.segLenAt(o)
				break
			}
			continue
		}
		break
	}
	if tick {
		if p.byteAt(o) != '`' {
			return 0, false
		}
		o++
	}
	if p.byteAt(o) != ']' || p.byteAt(o+1) == '(' {
		return 0, false
	}
	return o + 1 - p.pos, tick
}

// shortLink parses `[name]` or “[`name`]“, and reports false (with
// nothing consumed) when the brackets aren't one.
func (p *parser) shortLink() bool {
	n, tick := p.shortLinkLen()
	if n == 0 {
		return false
	}
	m := p.mark()
	p.tok(LinkOpen, 1)
	if tick {
		p.tok(LinkTick, 1)
	}
	p.path(false)
	if tick {
		p.tok(LinkTick, 1)
	}
	p.tok(ShortLinkEnd, 1)
	p.wrap(m, ShortLink)
	return true
}

// linkAhead is a quick check that the `[` at the cursor has a matching `]`
// followed by `(` on the same line.
func (p *parser) linkAhead() bool {
	depth := 0
	for o := p.pos; o < len(p.src); o++ {
		switch p.src[o] {
		case '\\':
			o++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return o+1 < len(p.src) && p.src[o+1] == '('
			}
		case '\n':
			return false
		}
	}
	return false
}

// tag parses a component tag and reports false (with nothing consumed)
// when the `<` doesn't start one.
func (p *parser) tag(cx *inlineCtx) bool {
	if p.failedAt('<') {
		return false
	}
	s := p.save()
	defer func() {
		if p.pos == s.pos {
			p.markFailed(s.pos, '<')
		}
	}()
	m := p.mark()
	p.tok(TagOpen, 1)
	nameStart := p.pos
	p.path(false)
	name := p.src[nameStart:p.pos]
	base := s.diags // the diagnostics before this element's own
	for {
		p.ws()
		if p.at("/>") {
			p.tok(TagSelfEnd, 2)
			p.wrap(m, Tag)
			return true
		}
		if p.peek(0) == '>' {
			p.tok(TagEnd, 1)
			break
		}
		at := 0
		if p.peek(0) == '@' {
			// `@name="args"`: an annotation on the element's node.
			at = 1
		}
		n := p.identLenAt(p.pos + at)
		if n == 0 {
			p.restore(s)
			return false
		}
		am := p.mark()
		if at == 1 {
			p.tok(At, 1)
		}
		attr := p.src[p.pos : p.pos+n]
		p.tok(Ident, n)
		if p.peek(0) == '=' {
			p.tok(Eq, 1)
			switch p.peek(0) {
			case '"':
				p.stringLit()
			case '{':
				p.tok(LBrace, 1)
				p.trivia()
				p.expr()
				p.trivia()
				if p.peek(0) != '}' {
					p.restore(s)
					return false
				}
				p.tok(RBrace, 1)
			default:
				// An element is never repaired: an unquoted value is an
				// error, and the element stays in the tree.
				k := 0
				for p.pos+k < len(p.src) {
					c := p.src[p.pos+k]
					if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '>' || c == '"' || c == '{' || strings.HasPrefix(p.src[p.pos+k:], "/>") {
						break
					}
					k++
				}
				if k == 0 {
					p.restore(s)
					return false
				}
				p.errAt(p.pos, p.pos+k, "the value of "+attr+" needs quotes or braces")
				base = len(p.diags)
				p.tok(Error, k)
				p.wrap(am, TagAttr)
				continue
			}
		}
		p.wrap(am, TagAttr)
		if len(p.diags) > base {
			p.restore(s)
			return false
		}
	}
	inner := *cx
	inner.tags++
	inner.link = 0
	if !p.enter() {
		p.leave()
		p.restore(s)
		return false
	}
	p.inlines(&inner)
	p.leave()
	if p.at("</") {
		cm := p.mark()
		p.tok(TagCloseOpen, 2)
		closeStart := p.pos
		if p.identLen() > 0 {
			p.path(false)
		}
		if got := p.src[closeStart:p.pos]; got != name {
			p.errAt(closeStart, p.pos, "`</"+got+">` closes `<"+name+">`")
		}
		p.ws()
		if p.peek(0) == '>' {
			p.tok(TagEnd, 1)
		} else {
			p.err("expected `>`")
		}
		p.wrap(cm, TagClose)
	} else {
		p.errAt(nameStart-1, nameStart+len(name), "`<"+name+">` is never closed")
	}
	p.wrap(m, Tag)
	return true
}
