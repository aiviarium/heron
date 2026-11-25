package eval

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/ast"
	"github.com/helightdev/specs-v0/internal/core/resolve"
	"github.com/helightdev/specs-v0/internal/core/syntax"
	"github.com/helightdev/specs-v0/internal/core/types"
)

// expr evaluates an expression.
func (m *Machine) expr(ctx context.Context, f *frame, e ast.Expr) (Value, error) {
	switch e := e.(type) {
	case nil:
		return nil, nil
	case ast.Literal:
		return m.literal(f, e)
	case ast.StringLit:
		return m.str(ctx, f, e, nil)
	case ast.PathExpr:
		return m.path(ctx, f, resolve.PathSegs(e.Path().N), e.N)
	case ast.CallExpr:
		return m.call(ctx, f, e)
	case ast.FieldExpr:
		v, err := m.expr(ctx, f, e.Expr())
		if err != nil {
			return nil, err
		}
		id := e.Ident()
		if id == nil {
			return v, nil
		}
		return m.field(f, v, id.Text, e.N)
	case ast.ArrayExpr:
		out := []Value{}
		for _, x := range e.Exprs() {
			v, err := m.expr(ctx, f, x)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case ast.MapExpr:
		out := map[string]Value{}
		for _, ent := range e.MapEntries() {
			v, err := m.expr(ctx, f, ent.Expr())
			if err != nil {
				return nil, err
			}
			out[resolve.PathText(ent.Path().N)] = v
		}
		return out, nil
	case ast.ParenExpr:
		return m.expr(ctx, f, e.Expr())
	case ast.CastExpr:
		return m.cast(ctx, f, e)
	case ast.BinaryExpr:
		return m.binary(ctx, f, e)
	case ast.UnaryExpr:
		v, err := m.expr(ctx, f, e.Expr())
		if err != nil {
			return nil, err
		}
		if e.KwNot() != nil {
			b, ok := v.(bool)
			if !ok {
				return nil, m.errAt(f, e.N, "not needs a Bool, got %s", TypeName(v))
			}
			return !b, nil
		}
		switch x := v.(type) {
		case int64:
			if x == math.MinInt64 {
				return nil, m.errAt(f, e.N, "-(%d) overflows Int, a 64-bit integer", x)
			}
			return -x, nil
		case float64:
			return -x, nil
		}
		return nil, m.errAt(f, e.N, "- needs a number, got %s", TypeName(v))
	case ast.IfExpr, ast.ForExpr:
		v, err := m.flow(ctx, f, e)
		if _, ok := v.(returned); ok {
			return nil, m.errAt(f, e.Syntax(), "return can't stand inside an expression")
		}
		return v, err
	case ast.ProseLines, ast.ProseBlock:
		return proseText(e.Syntax()), nil
	case ast.ErrorNode:
		return nil, m.errAt(f, e.N, "this expression doesn't parse")
	default:
		return nil, m.errAt(f, e.Syntax(), "unsupported expression")
	}
}

func (m *Machine) literal(f *frame, e ast.Literal) (Value, error) {
	switch {
	case e.KwTrue() != nil:
		return true, nil
	case e.KwFalse() != nil:
		return false, nil
	case e.Raw() != nil:
		return dedentRaw(resolve.RawValue(e.Raw().Text), e.Raw().Text), nil
	case e.Number() != nil:
		text := e.Number().Text
		v, err := parseNumber(text)
		switch {
		case errors.Is(err, strconv.ErrRange):
			return nil, m.errAt(f, e.N, "%s overflows Int, a 64-bit integer", text)
		case err != nil:
			return nil, m.errAt(f, e.N, "%s is no number", text)
		}
		return v, nil
	}
	return nil, nil
}

// parseNumber reads a number literal. A whole number is an Int, and one
// that doesn't fit fails with strconv.ErrRange rather than become a Float
// that has lost its last digits.
func parseNumber(text string) (Value, error) {
	t := strings.ReplaceAll(text, "_", "")
	var i int64
	var err error
	switch {
	case strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0X"):
		i, err = strconv.ParseInt(t[2:], 16, 64)
	case !strings.ContainsAny(t, ".eE"):
		i, err = strconv.ParseInt(t, 10, 64)
	default:
		x, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, errors.New("not a number")
		}
		return x, nil
	}
	if err != nil {
		return nil, err
	}
	return i, nil
}

// --- paths ---

// path evaluates a dotted path: a local and its fields, or a node of the
// tree and what follows it.
func (m *Machine) path(ctx context.Context, f *frame, segs []string, at *syntax.Node) (Value, error) {
	if len(segs) == 0 {
		return nil, nil
	}
	if len(segs) == 1 && segs[0] == "null" {
		if _, ok := f.get("null"); !ok {
			return nil, nil
		}
	}
	v, n, rest, err := m.head(ctx, f, segs, at)
	if err != nil {
		return nil, err
	}
	if n != nil {
		if len(rest) > 0 {
			if fv, ok := m.State[n][rest[0]]; ok {
				v, rest = fv, rest[1:]
			} else if v, err = m.nodeValue(ctx, n); err != nil {
				return nil, err
			}
		} else {
			return m.nodeValue(ctx, n)
		}
	}
	for _, s := range rest {
		if v, err = m.field(f, v, s, at); err != nil {
			return nil, err
		}
	}
	return v, nil
}

// head resolves the start of a path: a local (returned as a value), or the
// deepest node of the tree its segments name. rest is what's left.
func (m *Machine) head(ctx context.Context, f *frame, segs []string, at *syntax.Node) (Value, *resolve.Node, []string, error) {
	if v, ok := f.get(segs[0]); ok {
		return v, nil, segs[1:], nil
	}
	b := f.scope.Lookup(segs[0], f.file, off(at))
	switch {
	case b.Local != nil:
		v, err := m.letValue(ctx, b.Local)
		return v, nil, segs[1:], err
	case b.Node != nil:
		n, i := b.Node, 1
		for i < len(segs) {
			if v, ok := m.State[n][segs[i]]; ok {
				// A field code set wins over what the tree says.
				return v, nil, segs[i+1:], nil
			}
			c := n.Child(segs[i])
			if c == nil {
				break
			}
			n, i = c, i+1
		}
		return nil, n, segs[i:], nil
	}
	return nil, nil, nil, m.errAt(f, at, "unknown name %s", segs[0])
}

// letValue evaluates a `let` outside code, once.
func (m *Machine) letValue(ctx context.Context, l *resolve.Local) (Value, error) {
	if v, ok := m.lets[l]; ok {
		return v, nil
	}
	if l.Value == nil {
		return nil, m.errorf(l.Site, nil, "%s has no value here", l.Name)
	}
	f := &frame{scope: l.Site.Scope, file: l.Site.File, vars: map[string]Value{}}
	v, err := m.expr(ctx, f, l.Value)
	if err != nil {
		return nil, err
	}
	m.lets[l] = v
	return v, nil
}

// nodeValue is a node used as a value: its value if it has one, an enum
// variant's name, the clock's reading, a type, or a reference.
func (m *Machine) nodeValue(ctx context.Context, n *resolve.Node) (Value, error) {
	if v, ok := m.values[n]; ok {
		return v, nil
	}
	if n.Parent != nil && m.Types.IsEnum(n.Parent) {
		return n.Name, nil
	}
	if qualified(n) == "core.clock" {
		if m.Caps.Clock == nil {
			return nil, m.errorf(n.Decl, nil, "no clock is available to this run")
		}
		return &Record{Type: "Clock", Fields: map[string]Value{"now": m.Caps.Clock.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")}}, nil
	}
	if m.isTypeLike(n) {
		return TypeValue{Node: n}, nil
	}
	if n.Value != nil && len(m.Types.Params(n)) == 0 && m.kindName(n) == "" {
		site := n.ValueSite
		f := &frame{node: n, scope: site.Scope, file: site.File, vars: map[string]Value{}}
		if f.scope == nil {
			f.scope, f.file = n.Decl.Scope, n.Decl.File
		}
		m.evaluating++
		v, err := m.expr(ctx, f, n.Value)
		m.evaluating--
		if err != nil {
			return nil, err
		}
		m.values[n] = v
		return v, nil
	}
	return Ref{Node: n}, nil
}

// field reads a member of a value.
func (m *Machine) field(f *frame, v Value, name string, at *syntax.Node) (Value, error) {
	switch x := v.(type) {
	case *Record:
		return x.Fields[name], nil
	case map[string]Value:
		return x[name], nil
	case Ref:
		if fv, ok := m.State[x.Node][name]; ok {
			return fv, nil
		}
		if c := x.Node.Child(name); c != nil {
			return m.nodeValue(context.Background(), c)
		}
		return nil, nil
	case nil:
		return nil, m.errAt(f, at, "null has no member %s", name)
	}
	if p, ok := property(v, name); ok {
		return p, nil
	}
	return nil, m.errAt(f, at, "%s has no member %s", TypeName(v), name)
}

// --- calls ---

func (m *Machine) args(ctx context.Context, f *frame, args ast.Args) (callArgs, error) {
	out := callArgs{named: map[string]Value{}}
	for _, a := range args.Args() {
		switch a := a.(type) {
		case ast.PositionalArg:
			v, err := m.expr(ctx, f, a.Expr())
			if err != nil {
				return out, err
			}
			out.pos = append(out.pos, v)
		case ast.NamedArg:
			id := a.Ident()
			if id == nil {
				continue
			}
			v, err := m.expr(ctx, f, a.Expr())
			if err != nil {
				return out, err
			}
			out.named[id.Text] = v
		}
	}
	return out, nil
}

func (m *Machine) call(ctx context.Context, f *frame, e ast.CallExpr) (Value, error) {
	switch callee := e.Callee().(type) {
	case ast.PathExpr:
		segs := resolve.PathSegs(callee.Path().N)
		if len(segs) == 0 {
			return nil, nil
		}
		v, n, rest, err := m.head(ctx, f, segs, callee.N)
		if err != nil {
			return nil, err
		}
		if n != nil && len(rest) == 0 {
			if qualified(n) == "core.attempt" {
				return m.attempt(ctx, f, e.Args())
			}
			args, err := m.args(ctx, f, e.Args())
			if err != nil {
				return nil, err
			}
			return m.callNode(ctx, n, args, nil, f)
		}
		if n != nil {
			// A method on a node's value, such as items.contains(x).
			if v, err = m.nodeValue(ctx, n); err != nil {
				return nil, err
			}
		}
		for _, s := range rest[:len(rest)-1] {
			if v, err = m.field(f, v, s, callee.N); err != nil {
				return nil, err
			}
		}
		return m.method(ctx, f, v, rest[len(rest)-1], e.Args(), callee.N)
	case ast.FieldExpr:
		v, err := m.expr(ctx, f, callee.Expr())
		if err != nil {
			return nil, err
		}
		id := callee.Ident()
		if id == nil {
			return nil, m.errAt(f, e.N, "a call needs a name")
		}
		return m.method(ctx, f, v, id.Text, e.Args(), callee.N)
	default:
		return nil, m.errAt(f, e.N, "only funcs, tasks and types can be called")
	}
}

// method calls a method on a value: a built-in one, or a func in scope
// with the value as its first argument, as list.contains(x) is
// contains(list, x).
func (m *Machine) method(ctx context.Context, f *frame, recv Value, name string, a ast.Args, at *syntax.Node) (Value, error) {
	args, err := m.args(ctx, f, a)
	if err != nil {
		return nil, err
	}
	if v, ok, err := builtinMethod(recv, name, args.pos); ok {
		if err != nil {
			return nil, m.errAt(f, at, "%v", err)
		}
		return v, nil
	}
	if b := f.scope.Lookup(name, f.file, off(at)); b.Node != nil {
		args.pos = append([]Value{recv}, args.pos...)
		return m.callNode(ctx, b.Node, args, nil, f)
	}
	return nil, m.errAt(f, at, "%s has no method %s", TypeName(recv), name)
}

// attempt runs a call that may throw and keeps the outcome as a Result.
func (m *Machine) attempt(ctx context.Context, f *frame, a ast.Args) (Value, error) {
	list := a.Args()
	if len(list) != 1 {
		return nil, m.errAt(f, a.N, "attempt takes one call")
	}
	var e ast.Expr
	switch x := list[0].(type) {
	case ast.PositionalArg:
		e = x.Expr()
	case ast.NamedArg:
		e = x.Expr()
	}
	v, err := m.expr(ctx, f, e)
	if th, ok := err.(*Throw); ok {
		return &Record{Type: "Result", Fields: map[string]Value{"error": th.Value}}, nil
	}
	if err != nil {
		return nil, err
	}
	return &Record{Type: "Result", Fields: map[string]Value{"value": v}}, nil
}

// --- strings and embeds ---

// str evaluates a quoted string, inserting $name and ${expr}; quote makes
// inserted values safe for an embed's language. A multi-line string loses
// its first and last line breaks and its common indentation.
func (m *Machine) str(ctx context.Context, f *frame, e ast.StringLit, quote func(string) string) (Value, error) {
	var tmpl strings.Builder
	var inserts []string
	multi := false
	for _, c := range e.N.Children {
		switch c := c.(type) {
		case *syntax.Token:
			switch c.Kind {
			case syntax.Quote:
				if len(c.Text) >= 3 {
					multi = true
				}
			case syntax.StringText:
				tmpl.WriteString(c.Text)
			case syntax.Escape:
				tmpl.WriteString(unescape(c.Text))
			}
		case *syntax.Node:
			if c.Kind != syntax.Insert {
				continue
			}
			v, err := m.insert(ctx, f, ast.Insert{N: c})
			if err != nil {
				return nil, err
			}
			s := Str(v)
			if quote != nil {
				s = quote(s)
			}
			inserts = append(inserts, s)
			tmpl.WriteByte(0)
		}
	}
	t := tmpl.String()
	if multi {
		t = dedent(t)
	}
	var out strings.Builder
	k := 0
	for i := 0; i < len(t); i++ {
		if t[i] == 0 && k < len(inserts) {
			out.WriteString(inserts[k])
			k++
			continue
		}
		out.WriteByte(t[i])
	}
	return out.String(), nil
}

func (m *Machine) insert(ctx context.Context, f *frame, in ast.Insert) (Value, error) {
	if e := in.Expr(); e != nil {
		return m.expr(ctx, f, e)
	}
	p := in.Path()
	if p.N == nil {
		return "", nil
	}
	segs := resolve.PathSegs(p.N)
	if in.Args().N != nil {
		v, n, rest, err := m.head(ctx, f, segs, p.N)
		if err != nil {
			return nil, err
		}
		if n != nil && len(rest) == 0 {
			args, err := m.args(ctx, f, in.Args())
			if err != nil {
				return nil, err
			}
			return m.callNode(ctx, n, args, nil, f)
		}
		return v, nil
	}
	return m.path(ctx, f, segs, p.N)
}

func unescape(e string) string {
	if len(e) < 2 {
		return e
	}
	switch e[1] {
	case 'n':
		return "\n"
	case 't':
		return "\t"
	case 'r':
		return "\r"
	case '0':
		return "\x00"
	default:
		return e[1:]
	}
}

// dedent trims a multi-line string: the line break after the opening
// quotes, the last line when it holds only indentation, and the
// indentation every other non-blank line shares. Inserted values (the
// zero bytes of the template) count as text.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) < 2 {
		return s
	}
	if strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if n := len(lines); n > 0 && strings.TrimLeft(lines[n-1], " \t") == "" {
		lines = lines[:n-1]
	}
	common := -1
	for _, l := range lines {
		if strings.TrimLeft(l, " \t") == "" {
			continue
		}
		ind := len(l) - len(strings.TrimLeft(l, " \t"))
		if common < 0 || ind < common {
			common = ind
		}
	}
	for i, l := range lines {
		if len(l) >= common && common > 0 {
			lines[i] = l[common:]
		} else if strings.TrimLeft(l, " \t") == "" {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// dedentRaw trims a multi-line raw string the same way.
func dedentRaw(v, text string) string {
	if strings.HasPrefix(text, "```") && strings.Contains(v, "\n") {
		return dedent(v)
	}
	return v
}

// cast evaluates a value with a type in front: an embed of a language
// becomes Text with its inserts made safe; any other type tags the value.
func (m *Machine) cast(ctx context.Context, f *frame, e ast.CastExpr) (Value, error) {
	lang := m.language(f, e.Type())
	if lang == nil {
		return m.expr(ctx, f, e.Expr())
	}
	var quote func(string) string
	if mode := insertMode(lang); mode == "QUOTE" && !rawInserts(f.node) {
		quote = quoterFor(lang.Name)
	}
	var v Value
	var err error
	switch x := e.Expr().(type) {
	case ast.StringLit:
		v, err = m.str(ctx, f, x, quote)
	default:
		v, err = m.expr(ctx, f, e.Expr())
	}
	if err != nil {
		return nil, err
	}
	return Text{Lang: lang.Name, Text: Str(v)}, nil
}

// language returns the language node a cast's type names, or nil.
func (m *Machine) language(f *frame, t ast.Type) *resolve.Node {
	nt, ok := t.(ast.NamedType)
	if !ok {
		return nil
	}
	n := m.lookupNode(f, resolve.PathSegs(nt.Path().N), nt.N)
	if n == nil {
		return nil
	}
	if k := m.Types.KindOf(n); k != nil && types.IsStd(k, "schema", "schema.language") {
		return n
	}
	return nil
}

// insertMode reads @insert(Insert.MODE) above a language.
func insertMode(lang *resolve.Node) string {
	for _, s := range lang.Annotations {
		a := ast.Annotation{N: s.N}
		if resolve.PathText(a.Path().N) != "insert" {
			continue
		}
		for _, arg := range a.Args().Args() {
			if pa, ok := arg.(ast.PositionalArg); ok {
				if pe, ok := pa.Expr().(ast.PathExpr); ok {
					segs := resolve.PathSegs(pe.Path().N)
					return segs[len(segs)-1]
				}
			}
		}
	}
	return ""
}

// rawInserts reports whether @insert<raw> above the node opts out.
func rawInserts(n *resolve.Node) bool {
	for ; n != nil; n = n.Parent {
		for _, s := range n.Annotations {
			a := ast.Annotation{N: s.N}
			if resolve.PathText(a.Path().N) != "insert" {
				continue
			}
			for _, fl := range a.Flags().Idents() {
				if fl.Text == "raw" {
					return true
				}
			}
		}
	}
	return false
}

// quoterFor returns how a language quotes an inserted value.
func quoterFor(lang string) func(string) string {
	switch lang {
	case "sh", "bash", "zsh":
		return ShellQuote
	}
	return nil
}

// ShellQuote quotes a value as one sh word.
func ShellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=@%+,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// proseText is the text of a prose value, without its markers.
func proseText(n *syntax.Node) string {
	var b strings.Builder
	n.Tokens(func(t *syntax.Token) {
		switch t.Kind {
		case syntax.Pipe, syntax.LBrace2, syntax.RBrace2:
		default:
			b.WriteString(t.Text)
		}
	})
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(strings.TrimRight(l, " \t"), " ")
	}
	return strings.TrimSpace(dedent(strings.Join(lines, "\n")))
}

// --- operators ---

func (m *Machine) binary(ctx context.Context, f *frame, e ast.BinaryExpr) (Value, error) {
	op := opOf(e.N)
	l, err := m.expr(ctx, f, e.Lhs())
	if err != nil {
		return nil, err
	}
	switch op {
	case syntax.KwAnd, syntax.KwOr:
		lb, ok := l.(bool)
		if !ok {
			return nil, m.errAt(f, e.Lhs().Syntax(), "%s needs Bools, got %s", opText(op), TypeName(l))
		}
		if op == syntax.KwAnd && !lb || op == syntax.KwOr && lb {
			return lb, nil
		}
		r, err := m.expr(ctx, f, e.Rhs())
		if err != nil {
			return nil, err
		}
		rb, ok := r.(bool)
		if !ok {
			return nil, m.errAt(f, e.Rhs().Syntax(), "%s needs Bools, got %s", opText(op), TypeName(r))
		}
		return rb, nil
	case syntax.KwIs:
		return m.is(f, l, e.Rhs())
	}
	r, err := m.expr(ctx, f, e.Rhs())
	if err != nil {
		return nil, err
	}
	v, err := operate(op, l, r)
	if err != nil {
		return nil, m.errAt(f, e.N, "%v", err)
	}
	return v, nil
}

func opOf(n *syntax.Node) syntax.Kind {
	for _, c := range n.Children {
		if t, ok := c.(*syntax.Token); ok {
			switch t.Kind {
			case syntax.EqEq, syntax.NotEq, syntax.Lt, syntax.LtEq, syntax.Gt, syntax.GtEq,
				syntax.Plus, syntax.Minus, syntax.Star, syntax.Slash, syntax.Percent,
				syntax.KwAnd, syntax.KwOr, syntax.KwIn, syntax.KwIs:
				return t.Kind
			}
		}
	}
	return syntax.KindNone
}

var opTexts = map[syntax.Kind]string{
	syntax.EqEq: "==", syntax.NotEq: "!=", syntax.Lt: "<", syntax.LtEq: "<=", syntax.Gt: ">", syntax.GtEq: ">=",
	syntax.Plus: "+", syntax.Minus: "-", syntax.Star: "*", syntax.Slash: "/", syntax.Percent: "%",
	syntax.KwAnd: "and", syntax.KwOr: "or", syntax.KwIn: "in", syntax.KwIs: "is",
}

func opText(k syntax.Kind) string { return opTexts[k] }

type opError string

// overflow is the fault of Int arithmetic whose result doesn't fit in an
// Int: it fails rather than wrap around.
func overflow(l int64, op syntax.Kind, r int64) error {
	return opError(strconv.FormatInt(l, 10) + " " + opText(op) + " " + strconv.FormatInt(r, 10) + " overflows Int, a 64-bit integer")
}

func (e opError) Error() string { return string(e) }

func operate(op syntax.Kind, l, r Value) (Value, error) {
	bad := func() (Value, error) {
		return nil, opError(TypeName(l) + " " + opText(op) + " " + TypeName(r) + " isn't defined")
	}
	if t, ok := l.(Text); ok {
		l = t.Text
	}
	if t, ok := r.(Text); ok {
		r = t.Text
	}
	switch op {
	case syntax.EqEq:
		return Equal(l, r), nil
	case syntax.NotEq:
		return !Equal(l, r), nil
	case syntax.KwIn:
		return contains(r, l)
	}
	li, lInt := l.(int64)
	ri, rInt := r.(int64)
	lf, lNum := num(l)
	rf, rNum := num(r)
	switch op {
	case syntax.Lt, syntax.LtEq, syntax.Gt, syntax.GtEq:
		var c int
		switch {
		case lNum && rNum:
			c = cmpFloat(lf, rf)
		default:
			ls, ok1 := l.(string)
			rs, ok2 := r.(string)
			if !ok1 || !ok2 {
				return bad()
			}
			c = strings.Compare(ls, rs)
		}
		switch op {
		case syntax.Lt:
			return c < 0, nil
		case syntax.LtEq:
			return c <= 0, nil
		case syntax.Gt:
			return c > 0, nil
		default:
			return c >= 0, nil
		}
	case syntax.Plus:
		switch {
		case lInt && rInt:
			s := li + ri
			if (li > 0 && ri > 0 && s < 0) || (li < 0 && ri < 0 && s >= 0) {
				return nil, overflow(li, op, ri)
			}
			return s, nil
		case lNum && rNum:
			return lf + rf, nil
		}
		if ls, ok := l.(string); ok {
			if rs, ok := r.(string); ok {
				return ls + rs, nil
			}
		}
		if ll, ok := l.([]Value); ok {
			if rl, ok := r.([]Value); ok {
				return append(append([]Value{}, ll...), rl...), nil
			}
		}
		return bad()
	case syntax.Minus, syntax.Star:
		switch {
		case lInt && rInt && op == syntax.Minus:
			d := li - ri
			if (li^ri)&(li^d) < 0 {
				return nil, overflow(li, op, ri)
			}
			return d, nil
		case lInt && rInt:
			p := li * ri
			if li != 0 && (p/li != ri || (li == -1 && ri == math.MinInt64) || (ri == -1 && li == math.MinInt64)) {
				return nil, overflow(li, op, ri)
			}
			return p, nil
		case lNum && rNum && op == syntax.Minus:
			return lf - rf, nil
		case lNum && rNum:
			return lf * rf, nil
		}
		return bad()
	case syntax.Slash, syntax.Percent:
		if !lNum || !rNum {
			return bad()
		}
		if rf == 0 {
			return nil, opError("division by zero")
		}
		if lInt && rInt {
			if op == syntax.Slash {
				if li == math.MinInt64 && ri == -1 {
					return nil, overflow(li, op, ri)
				}
				return li / ri, nil
			}
			return li % ri, nil
		}
		if op == syntax.Slash {
			return lf / rf, nil
		}
		return math.Mod(lf, rf), nil
	}
	return bad()
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// contains is `x in coll`: an item of a list, a key of a map or record, or
// a substring.
func contains(coll, x Value) (Value, error) {
	switch c := coll.(type) {
	case []Value:
		for _, it := range c {
			if Equal(it, x) {
				return true, nil
			}
		}
		return false, nil
	case map[string]Value:
		k, ok := x.(string)
		if !ok {
			return false, nil
		}
		_, has := c[k]
		return has, nil
	case *Record:
		k, ok := x.(string)
		if !ok {
			return false, nil
		}
		_, has := c.Fields[k]
		return has, nil
	case string:
		s, ok := x.(string)
		if !ok {
			return nil, opError("in on a String needs a String, got " + TypeName(x))
		}
		return strings.Contains(c, s), nil
	case nil:
		return false, nil
	}
	return nil, opError("in needs a list, a map or a String, got " + TypeName(coll))
}

// is tests a value's type: `result.error is ProcessError`.
func (m *Machine) is(f *frame, v Value, t ast.Expr) (Value, error) {
	pe, ok := t.(ast.PathExpr)
	if !ok {
		return nil, m.errAt(f, t.Syntax(), "is needs a type name")
	}
	segs := resolve.PathSegs(pe.Path().N)
	name := segs[len(segs)-1]
	if name == "Any" {
		return v != nil, nil
	}
	got := TypeName(v)
	if got == name || name == "Float" && got == "Int" || name == "Set" && got == "List" {
		return true, nil
	}
	if r, ok := v.(*Record); ok {
		// A type is also each interface it implements, such as Error.
		if n := m.lookupNode(f, []string{r.Type}, pe.N); n != nil {
			for _, i := range m.Types.Implements(n) {
				if i.Name == name {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
