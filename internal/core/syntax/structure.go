package syntax

// Structure: bodies of members.

type stop int

const (
	stopEOF   stop = iota // the file root
	stopBrace             // a `{ … }` body
	stopFence             // a heron fence in a prose file
)

// members parses members until the stop: EOF, a `}`, or a closing fence.
func (p *parser) members(s stop) {
	for {
		start := p.pos
		// Layout between members: spaces and blank lines.
		for {
			p.ws()
			if !p.newline() {
				break
			}
		}
		if p.eof() {
			return
		}
		switch s {
		case stopBrace:
			if p.peek(0) == '}' {
				return
			}
		case stopFence:
			if p.atFenceClose() {
				return
			}
		}
		if p.atComment() {
			m := p.mark()
			p.comment()
			p.wrap(m, CommentLine)
			continue
		}
		p.member()
		p.endLine("}")
		if s == stopEOF && p.peek(0) == '}' {
			p.err("unexpected `}`")
			m := p.mark()
			p.tok(Error, 1)
			p.wrap(m, ErrorNode)
		}
		if p.pos == start {
			// No progress: consume one character as an error.
			_, w := p.runeAt(p.pos)
			m := p.mark()
			p.tok(Error, w)
			p.wrap(m, ErrorNode)
		}
	}
}

// member parses one member, with its docs and annotations.
func (p *parser) member() {
	m := p.mark()
	if p.at("///") {
		p.doc()
		// The member follows on the next line; a blank line ends the docs,
		// which then stand alone.
		save := p.save()
		p.ws()
		if !p.newline() {
			p.restore(save)
			return
		}
		p.ws()
		if p.atLineEnd() || p.peek(0) == '}' || p.atComment() {
			p.restore(save)
			return
		}
	}
	p.annotations(true)
	if !p.enter() {
		p.leave()
		p.errorToLineEnd()
		p.wrap(m, ErrorNode)
		return
	}
	defer p.leave()

	switch c := p.peek(0); {
	case c == '|':
		p.proseLines()
		p.wrap(m, ProseLines)
		return
	case p.at("{{"):
		p.proseBlock()
		p.wrap(m, ProseBlock)
		return
	case p.at(".{"):
		p.tok(Dot, 1)
		p.structBody()
		p.wrap(m, Scope)
		return
	case p.word("do") && p.nextNonSpace(2) == '{':
		p.doBlock()
		p.wrap(m, Action)
		return
	case p.word("let") && p.identLenAt(p.pos+3+p.spacesAt(p.pos+3)) > 0 && p.spacesAt(p.pos+3) > 0:
		p.letBinding()
		p.wrap(m, Let)
		return
	case p.identLen() > 0 || c == '"':
	default:
		p.err("expected a member")
		p.errorToLineEnd()
		p.wrap(m, ErrorNode)
		return
	}

	nameStart := p.pos
	p.path(true)
	kindName := lastSegment(p.src[nameStart:p.pos])
	hasFlags, hasArgs := false, false
	if p.peek(0) == '<' && p.flagsAhead() {
		p.flags()
		hasFlags = true
	}
	params := false
	if p.peek(0) == '(' {
		params = p.argList()
		hasArgs = true
	}
	if p.peek(0) == '?' && p.peekAfterSpaces(1) == ':' {
		p.tok(Question, 1)
		params = true
	}
	if p.at(":") {
		if hasArgs && !params {
			p.retag(Params)
		}
		p.tok(Colon, 1)
		p.ws()
		typeName := lastSegment(p.src[p.pos : p.pos+p.pathLenAt(p.pos)])
		p.typ()
		p.ws()
		if p.atAssign() {
			p.valueAfterEq()
			p.wrap(m, Value)
			return
		}
		p.optBody(verbatimKinds[typeName])
		p.wrap(m, Declare)
		return
	}
	p.ws()
	if hasFlags || hasArgs {
		if p.atAssign() {
			p.valueAfterEq()
		} else {
			p.optBody(verbatimKinds[kindName])
		}
		p.wrap(m, Invoke)
		return
	}
	switch {
	case p.at(".{"):
		p.tok(Dot, 1)
		p.structBody()
		p.wrap(m, Scope)
	case p.atAssign():
		p.valueAfterEq()
		p.wrap(m, Set)
	case p.at("{{"):
		bm := p.mark()
		p.body(verbatimKinds[kindName])
		p.wrap(bm, ProseBlock)
		p.wrap(m, TextNode)
	case p.peek(0) == '{':
		p.structBody()
		p.wrap(m, Enter)
	case p.word("do") && p.nextNonSpace(2) == '{':
		p.doBlock()
		p.wrap(m, Enter)
	default:
		p.wrap(m, Invoke)
	}
}

// retag changes the kind of the last Args node emitted at this level.
func (p *parser) retag(k Kind) {
	for i := len(p.elems) - 1; i >= 0; i-- {
		if n, ok := p.elems[i].(*Node); ok && (n.Kind == Args || n.Kind == Params) {
			n.Kind = k
			return
		}
	}
}

func (p *parser) spacesAt(off int) int {
	n := 0
	for off+n < len(p.src) && (p.src[off+n] == ' ' || p.src[off+n] == '\t') {
		n++
	}
	return n
}

// nextNonSpace returns the byte after off bytes and any spaces.
func (p *parser) nextNonSpace(off int) byte {
	o := p.pos + off
	o += p.spacesAt(o)
	if o < len(p.src) {
		return p.src[o]
	}
	return 0
}

func (p *parser) peekAfterSpaces(off int) byte { return p.nextNonSpace(off) }

// atAssign reports whether the cursor is at `=` that isn't `==`.
func (p *parser) atAssign() bool { return p.peek(0) == '=' && p.peek(1) != '=' }

// valueAfterEq parses `=` and the value after it, which may start on a
// later line.
func (p *parser) valueAfterEq() {
	p.tok(Eq, 1)
	p.trivia()
	if p.eof() || p.peek(0) == '}' || p.peek(0) == ')' {
		p.err("expected a value after `=`")
		m := p.mark()
		p.wrap(m, ErrorNode)
		return
	}
	p.expr()
}

// optBody parses the body that may follow a declaration or call; a `{{ }}`
// body of a verbatim kind is text.
func (p *parser) optBody(verbatim bool) {
	switch {
	case p.at("{{"):
		m := p.mark()
		p.body(verbatim)
		p.wrap(m, ProseBlock)
	case p.peek(0) == '{':
		p.structBody()
	case p.word("do") && p.nextNonSpace(2) == '{':
		p.doBlock()
	}
}

func (p *parser) structBody() {
	m := p.mark()
	p.tok(LBrace, 1)
	p.members(stopBrace)
	if p.peek(0) == '}' {
		p.tok(RBrace, 1)
	} else {
		p.err("expected `}`")
	}
	p.wrap(m, StructBody)
}

func (p *parser) doBlock() {
	m := p.mark()
	p.tok(KwDo, 2)
	p.ws()
	p.code()
	p.wrap(m, DoBlock)
}

// letBinding parses `let name (: Type)? = expr` without wrapping it.
func (p *parser) letBinding() {
	p.tok(KwLet, 3)
	p.ws()
	p.tok(Ident, p.identLen())
	p.ws()
	if p.peek(0) == ':' {
		p.tok(Colon, 1)
		p.ws()
		p.typ()
		p.ws()
	}
	if p.atAssign() {
		p.valueAfterEq()
	} else {
		p.err("expected `=`")
	}
}

// annotations parses `@name(args)` annotations. In structure they may stand
// on lines of their own above the member.
func (p *parser) annotations(lines bool) {
	for p.peek(0) == '@' && p.identLenAt(p.pos+1) > 0 {
		m := p.mark()
		p.tok(At, 1)
		p.path(false)
		if p.peek(0) == '<' && p.flagsAhead() {
			p.flags()
		}
		if p.peek(0) == '(' {
			p.argList()
		}
		p.wrap(m, Annotation)
		p.ws()
		if lines {
			save := p.save()
			p.lineTrivia()
			if p.newline() {
				p.ws()
				if p.atLineEnd() {
					p.restore(save)
					return
				}
				for p.atComment() {
					p.comment()
					p.newline()
					p.ws()
				}
			} else if !p.eof() {
				p.restore(save)
				p.ws()
			}
		}
	}
}

// doc parses consecutive `///` lines.
func (p *parser) doc() {
	m := p.mark()
	for {
		lm := p.mark()
		p.tok(DocPrefix, 3)
		p.inlineLine("///")
		p.wrap(lm, DocLine)
		save := p.save()
		if p.newline() {
			p.ws()
			if p.at("///") {
				continue
			}
		}
		p.restore(save)
		break
	}
	p.wrap(m, Doc)
}

// path parses `a.b.c`; a segment may be a string in structure. It stops
// before `.{` and before a `.` that isn't followed by a name.
func (p *parser) path(strs bool) {
	m := p.mark()
	first := true
	for {
		if n := p.identLen(); n > 0 {
			p.tok(Ident, n)
		} else if n := p.segLenAt(p.pos); !first && n > 0 {
			p.tok(Ident, n)
		} else if strs && p.peek(0) == '"' {
			p.stringLit()
		} else {
			p.err("expected a name")
			break
		}
		if p.peek(0) == '.' && (p.segLenAt(p.pos+1) > 0 || (strs && p.peek(1) == '"')) {
			p.tok(Dot, 1)
			first = false
			continue
		}
		break
	}
	p.wrap(m, Path)
}

// flagLenAt is the length of a flag's name at an offset: an identifier, or
// a qualified one, as `types.warning`, which is one token.
func (p *parser) flagLenAt(off int) int {
	n := p.identLenAt(off)
	for n > 0 && off+n < len(p.src) && p.src[off+n] == '.' {
		m := p.identLenAt(off + n + 1)
		if m == 0 {
			break
		}
		n += 1 + m
	}
	return n
}

// flagsAhead reports whether `<…>` at the cursor is a flag list.
func (p *parser) flagsAhead() bool {
	o := p.pos + 1
	for {
		o += p.spacesAt(o)
		n := p.flagLenAt(o)
		if n == 0 {
			return false
		}
		o += n
		o += p.spacesAt(o)
		if o >= len(p.src) {
			return false
		}
		switch p.src[o] {
		case '>':
			return true
		case ',':
			o++
		default:
			return false
		}
	}
}

func (p *parser) flags() {
	m := p.mark()
	p.tok(FlagOpen, 1)
	for !p.eof() {
		p.ws()
		if p.peek(0) == '>' {
			break
		}
		if n := p.flagLenAt(p.pos); n > 0 {
			p.tok(Ident, n)
		}
		p.ws()
		if p.peek(0) == ',' {
			p.tok(Comma, 1)
			continue
		}
		break
	}
	if p.peek(0) == '>' {
		p.tok(FlagClose, 1)
	} else {
		p.err("expected `>`")
	}
	p.wrap(m, Flags)
}

// argList parses `( … )` after a name or kind, as arguments or parameters.
// It reports whether the list holds parameters.
func (p *parser) argList() bool {
	m := p.mark()
	p.tok(LParen, 1)
	params := false
	for {
		p.trivia()
		if p.eof() || p.peek(0) == ')' {
			break
		}
		start := p.pos
		if p.item() {
			params = true
		}
		p.trivia()
		if p.peek(0) == ',' {
			p.tok(Comma, 1)
			continue
		}
		if p.peek(0) == ')' || p.eof() {
			break
		}
		p.err("expected `,` or `)`")
		p.skipToCommaOrClose()
		if p.pos == start {
			break
		}
	}
	if p.peek(0) == ')' {
		p.tok(RParen, 1)
	} else {
		p.err("expected `)`")
	}
	k := Args
	if params {
		k = Params
	}
	p.wrap(m, k)
	return params
}

// skipToCommaOrClose recovers inside a list by skipping to the next `,`
// or the closing bracket, keeping nested brackets balanced.
func (p *parser) skipToCommaOrClose() {
	m := p.mark()
	depth := 0
	for !p.eof() {
		c := p.peek(0)
		if depth == 0 && (c == ',' || c == ')' || c == ']' || c == '}') {
			break
		}
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '"', '`':
			p.expr()
			continue
		}
		_, w := p.runeAt(p.pos)
		if c == '\n' {
			p.tok(Newline, 1)
		} else {
			p.tok(Error, w)
		}
	}
	p.wrap(m, ErrorNode)
}

// item parses one entry of an argument or parameter list. It reports
// whether the entry is a parameter.
func (p *parser) item() bool {
	m := p.mark()
	if p.at("///") || p.peek(0) == '@' || p.paramAhead() {
		if p.at("///") {
			p.doc()
			p.trivia()
		}
		for p.peek(0) == '@' && p.identLenAt(p.pos+1) > 0 {
			p.annotations(false)
			p.trivia()
		}
		if n := p.identLen(); n > 0 {
			p.tok(Ident, n)
		} else {
			p.err("expected a parameter name")
		}
		if p.peek(0) == '<' && p.flagsAhead() {
			p.flags()
		}
		if p.peek(0) == '?' {
			p.tok(Question, 1)
		}
		p.ws()
		if p.peek(0) == ':' {
			p.tok(Colon, 1)
			p.ws()
			p.typ()
		} else {
			p.err("expected `:` and a type")
		}
		p.ws()
		if p.atAssign() {
			p.valueAfterEq()
		}
		p.wrap(m, Param)
		return true
	}
	if n := p.identLen(); n > 0 && p.nextNonSpace(n) == '=' && p.nextNonSpaceIs(n, "==") == false {
		p.tok(Ident, n)
		p.ws()
		p.valueAfterEq()
		p.wrap(m, NamedArg)
		return false
	}
	p.expr()
	p.wrap(m, PositionalArg)
	return false
}

func (p *parser) nextNonSpaceIs(off int, s string) bool {
	o := p.pos + off
	o += p.spacesAt(o)
	return len(p.src)-o >= len(s) && p.src[o:o+len(s)] == s
}

// paramAhead reports whether `name<flags>? ?: Type` starts at the cursor.
func (p *parser) paramAhead() bool {
	n := p.identLen()
	if n == 0 {
		return false
	}
	o := p.pos + n
	if o < len(p.src) && p.src[o] == '<' {
		save := p.pos
		p.pos = o
		ok := p.flagsAhead()
		p.pos = save
		if !ok {
			return false
		}
		for o < len(p.src) && p.src[o] != '>' {
			o++
		}
		o++
	}
	if o < len(p.src) && p.src[o] == '?' {
		o++
	}
	o += p.spacesAt(o)
	return o < len(p.src) && p.src[o] == ':'
}
