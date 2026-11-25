package syntax

// Expressions, types and code.

// expr parses an expression with the usual precedence.
func (p *parser) expr() { p.binary(1) }

type binop struct {
	kind Kind
	n    int
	prec int
}

// binopAt returns the binary operator at the cursor, if any. Word operators
// need a non-name character after them.
func (p *parser) binopAt() (binop, bool) {
	two := ""
	if p.pos+2 <= len(p.src) {
		two = p.src[p.pos : p.pos+2]
	}
	switch two {
	case "==":
		return binop{EqEq, 2, 3}, true
	case "!=":
		return binop{NotEq, 2, 3}, true
	case "<=":
		return binop{LtEq, 2, 3}, true
	case ">=":
		return binop{GtEq, 2, 3}, true
	case "//":
		return binop{}, false
	}
	switch p.peek(0) {
	case '<':
		return binop{Lt, 1, 3}, true
	case '>':
		return binop{Gt, 1, 3}, true
	case '+':
		return binop{Plus, 1, 4}, true
	case '-':
		return binop{Minus, 1, 4}, true
	case '*':
		return binop{Star, 1, 5}, true
	case '/':
		return binop{Slash, 1, 5}, true
	case '%':
		return binop{Percent, 1, 5}, true
	}
	switch {
	case p.word("or"):
		return binop{KwOr, 2, 1}, true
	case p.word("and"):
		return binop{KwAnd, 3, 2}, true
	case p.word("in"):
		return binop{KwIn, 2, 3}, true
	case p.word("is"):
		return binop{KwIs, 2, 3}, true
	}
	return binop{}, false
}

func (p *parser) binary(minPrec int) {
	m := p.mark()
	p.unary()
	for {
		save := p.save()
		p.ws()
		op, ok := p.binopAt()
		if !ok || op.prec < minPrec {
			p.restore(save)
			return
		}
		p.tok(op.kind, op.n)
		p.trivia() // an operator at the end of a line continues the expression
		if !p.enter() {
			p.leave()
			return
		}
		p.binary(op.prec + 1)
		p.leave()
		p.wrap(m, BinaryExpr)
	}
}

func (p *parser) unary() {
	switch {
	case p.word("not"):
		m := p.mark()
		p.tok(KwNot, 3)
		p.ws()
		p.binary(3)
		p.wrap(m, UnaryExpr)
	case p.peek(0) == '-' && p.peek(1) != '-':
		m := p.mark()
		p.tok(Minus, 1)
		p.unary()
		p.wrap(m, UnaryExpr)
	default:
		p.postfix()
	}
}

// postfix parses a primary followed by calls and field accesses, which
// attach with no space.
func (p *parser) postfix() {
	m := p.mark()
	callable := p.primary()
	for callable {
		switch {
		case p.peek(0) == '<' && p.flagsAhead():
			p.flags()
			if p.peek(0) == '(' {
				p.argList()
			}
			p.wrap(m, CallExpr)
		case p.peek(0) == '(':
			p.argList()
			p.wrap(m, CallExpr)
		case p.peek(0) == '.' && p.identLenAt(p.pos+1) > 0:
			p.tok(Dot, 1)
			p.tok(Ident, p.identLen())
			p.wrap(m, FieldExpr)
		default:
			return
		}
	}
}

// primary parses one primary expression and reports whether calls and
// field accesses may follow it.
func (p *parser) primary() bool {
	if !p.enter() {
		p.leave()
		m := p.mark()
		p.wrap(m, ErrorNode)
		return false
	}
	defer p.leave()
	m := p.mark()
	c := p.peek(0)
	switch {
	case c >= '0' && c <= '9':
		p.number()
		p.wrap(m, Literal)
		return false
	case p.word("true"):
		p.tok(KwTrue, 4)
		p.wrap(m, Literal)
		return false
	case p.word("false"):
		p.tok(KwFalse, 5)
		p.wrap(m, Literal)
		return false
	case c == '"':
		p.stringLit()
		return true
	case c == '`':
		p.raw()
		p.wrap(m, Literal)
		return true
	case c == '[':
		p.array()
		return true
	case p.at("{{"):
		p.proseBlock()
		p.wrap(m, ProseBlock)
		return false
	case c == '{':
		p.mapLit()
		return true
	case c == '|':
		p.proseLines()
		p.wrap(m, ProseLines)
		return false
	case c == '(':
		if p.castAhead() {
			p.tok(LParen, 1)
			p.trivia()
			p.typ()
			p.trivia()
			if p.peek(0) == ')' {
				p.tok(RParen, 1)
			} else {
				p.err("expected `)`")
			}
			p.postfix()
			p.wrap(m, CastExpr)
			return false
		}
		p.tok(LParen, 1)
		p.trivia()
		p.expr()
		p.trivia()
		if p.peek(0) == ')' {
			p.tok(RParen, 1)
		} else {
			p.err("expected `)`")
		}
		p.wrap(m, ParenExpr)
		return true
	case p.word("if"):
		p.ifExpr()
		return false
	case p.word("for"):
		p.forExpr()
		return false
	case p.identLen() > 0:
		p.path(false)
		p.wrap(m, PathExpr)
		return true
	}
	p.err("expected a value")
	p.wrap(m, ErrorNode)
	return false
}

func (p *parser) number() {
	n := 0
	digits := func() {
		for p.pos+n < len(p.src) {
			c := p.src[p.pos+n]
			if (c >= '0' && c <= '9') || c == '_' {
				n++
				continue
			}
			break
		}
	}
	digits()
	if p.pos+n+1 < len(p.src) && p.src[p.pos+n] == '.' && p.src[p.pos+n+1] >= '0' && p.src[p.pos+n+1] <= '9' {
		n++
		digits()
	}
	if p.pos+n < len(p.src) && (p.src[p.pos+n] == 'e' || p.src[p.pos+n] == 'E') {
		k := n + 1
		if p.pos+k < len(p.src) && (p.src[p.pos+k] == '+' || p.src[p.pos+k] == '-') {
			k++
		}
		if p.pos+k < len(p.src) && p.src[p.pos+k] >= '0' && p.src[p.pos+k] <= '9' {
			n = k
			digits()
		}
	}
	p.tok(Number, n)
}

// castAhead reports whether `(` starts a cast: a parenthesised type with a
// value directly after the `)`.
func (p *parser) castAhead() bool {
	depth := 0
	for o := p.pos; o < len(p.src); o++ {
		switch p.src[o] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
			if depth == 0 {
				if o+1 >= len(p.src) {
					return false
				}
				switch c := p.src[o+1]; {
				case c == '"' || c == '`' || c == '[' || c == '{' || c == '(' || (c >= '0' && c <= '9'):
					return true
				default:
					return p.identLenAt(o+1) > 0
				}
			}
		case '"', '`', '\n', '{', '}', '|', '=':
			return false
		}
	}
	return false
}

func (p *parser) array() {
	m := p.mark()
	p.tok(LBracket, 1)
	for {
		p.trivia()
		if p.eof() || p.peek(0) == ']' {
			break
		}
		start := p.pos
		p.expr()
		p.trivia()
		if p.peek(0) == ',' {
			p.tok(Comma, 1)
			continue
		}
		if p.peek(0) == ']' || p.eof() {
			break
		}
		p.err("expected `,` or `]`")
		p.skipToCommaOrClose()
		if p.pos == start || p.peek(0) == ')' || p.peek(0) == '}' {
			break
		}
	}
	if p.peek(0) == ']' {
		p.tok(RBracket, 1)
	} else {
		p.err("expected `]`")
	}
	p.wrap(m, ArrayExpr)
}

// mapLit parses `{ key = value, … }`; entries end at `,` or a new line.
func (p *parser) mapLit() {
	m := p.mark()
	p.tok(LBrace, 1)
	for {
		p.trivia()
		if p.eof() || p.peek(0) == '}' {
			break
		}
		start := p.pos
		em := p.mark()
		if p.identLen() > 0 || p.peek(0) == '"' {
			p.path(true)
			p.ws()
			if p.atAssign() {
				p.valueAfterEq()
			} else {
				p.err("expected `=` in a map entry")
			}
		} else {
			p.err("expected a map entry")
			p.skipToCommaOrClose()
		}
		p.wrap(em, MapEntry)
		p.lineTrivia()
		if p.peek(0) == ',' {
			p.tok(Comma, 1)
			continue
		}
		if p.peek(0) == '}' || p.atNewline() || p.eof() {
			continue
		}
		p.err("expected `,`, a new line or `}`")
		p.skipToCommaOrClose()
		if p.pos == start || p.peek(0) == ')' || p.peek(0) == ']' {
			break
		}
	}
	if p.peek(0) == '}' {
		p.tok(RBrace, 1)
	} else {
		p.err("expected `}`")
	}
	p.wrap(m, MapExpr)
}

func (p *parser) ifExpr() {
	m := p.mark()
	p.tok(KwIf, 2)
	p.ws()
	p.expr()
	p.ws()
	p.code()
	save := p.save()
	p.ws()
	if !p.word("else") {
		// `else` may start the next line.
		p.restore(save)
		save = p.save()
		p.lineTrivia()
		if p.newline() {
			p.ws()
		}
		if !p.word("else") {
			p.restore(save)
			p.wrap(m, IfExpr)
			return
		}
	}
	p.tok(KwElse, 4)
	p.ws()
	if p.word("if") {
		p.ifExpr()
	} else {
		p.code()
	}
	p.wrap(m, IfExpr)
}

func (p *parser) forExpr() {
	m := p.mark()
	p.tok(KwFor, 3)
	p.ws()
	if n := p.identLen(); n > 0 {
		p.tok(Ident, n)
	} else {
		p.err("expected a name")
	}
	p.ws()
	if p.word("in") {
		p.tok(KwIn, 2)
	} else {
		p.err("expected `in`")
	}
	p.ws()
	p.expr()
	p.ws()
	p.code()
	p.wrap(m, ForExpr)
}

// code parses a `{ … }` block of statements.
func (p *parser) code() {
	m := p.mark()
	if p.peek(0) != '{' {
		p.err("expected `{`")
		p.wrap(m, Code)
		return
	}
	p.tok(LBrace, 1)
	for {
		start := p.pos
		for {
			p.ws()
			if !p.newline() {
				break
			}
		}
		if p.eof() || p.peek(0) == '}' {
			break
		}
		if p.atComment() {
			cm := p.mark()
			p.comment()
			p.wrap(cm, CommentLine)
			continue
		}
		p.stmt()
		p.endLine("}")
		if p.pos == start {
			em := p.mark()
			_, w := p.runeAt(p.pos)
			p.tok(Error, w)
			p.wrap(em, ErrorNode)
		}
	}
	if p.peek(0) == '}' {
		p.tok(RBrace, 1)
	} else {
		p.err("expected `}`")
	}
	p.wrap(m, Code)
}

func (p *parser) stmt() {
	if !p.enter() {
		p.leave()
		p.errorToLineEnd()
		return
	}
	defer p.leave()
	m := p.mark()
	switch {
	case p.word("let"):
		p.letBinding()
		p.wrap(m, LetStmt)
		return
	case p.word("return"):
		p.tok(KwReturn, 6)
		p.ws()
		if !p.atLineEnd() && p.peek(0) != '}' && !p.atComment() {
			p.expr()
		}
		p.wrap(m, ReturnStmt)
		return
	}
	p.expr()
	save := p.save()
	p.ws()
	if p.atAssign() {
		// The target of an assignment is a path.
		if n, ok := p.elems[m].(*Node); ok && n.Kind == PathExpr {
			p.elems[m] = n.Children[0]
		}
		p.valueAfterEq()
		p.wrap(m, AssignStmt)
		return
	}
	if p.peek(0) == '{' && !p.at("{{") {
		p.code()
	} else {
		p.restore(save)
	}
	p.wrap(m, ExprStmt)
}

// typ parses a type: names with arguments, `T?`, `A | B` and `A & B`.
func (p *parser) typ() {
	m := p.mark()
	p.typPostfix()
	for {
		save := p.save()
		p.ws()
		var k Kind
		switch {
		case p.peek(0) == '|' && p.peek(1) == ' ':
			k = UnionType
			p.tok(Bar, 1)
		case p.peek(0) == '&':
			k = IntersectionType
			p.tok(Amp, 1)
		default:
			p.restore(save)
			return
		}
		p.trivia()
		p.typPostfix()
		p.wrap(m, k)
	}
}

func (p *parser) typPostfix() {
	m := p.mark()
	if p.identLen() == 0 {
		p.err("expected a type")
		p.wrap(m, ErrorNode)
		return
	}
	p.path(false)
	if p.peek(0) == '<' && p.flagsAhead() {
		p.flags()
	}
	if p.peek(0) == '(' {
		p.argList()
	}
	p.wrap(m, NamedType)
	for p.peek(0) == '?' && p.peekAfterSpaces(1) != ':' {
		p.tok(Question, 1)
		p.wrap(m, OptionalType)
	}
}
