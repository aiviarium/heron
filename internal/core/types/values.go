package types

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// TypedStrings parse the string form of a type, keyed by the type's path in
// its standard package, such as "types.Duration". A unit value is a string
// that the expected type parses, so "30d" is a Duration and "40.00 EUR" is
// Money. Go backs only the parsing; the types are declared in Heron.
var TypedStrings = map[string]func(string) error{
	"types.Duration":  parseDuration,
	"types.Money":     parseMoney,
	"types.Timestamp": parseTimestamp,
}

var durationRe = regexp.MustCompile(`^(\d+(\.\d+)?(ms|s|m|h|d|w))+$`)

func parseDuration(s string) error {
	if !durationRe.MatchString(s) {
		return fmt.Errorf("%q is not a duration; write a number and a unit, as in \"30d\", \"20m\" or \"200ms\"", s)
	}
	return nil
}

var moneyRe = regexp.MustCompile(`^-?\d+(\.\d+)? [A-Z]{3}$`)

func parseMoney(s string) error {
	if !moneyRe.MatchString(s) {
		return fmt.Errorf("%q is not an amount of money; write the amount and an ISO 4217 currency, as in \"40.00 EUR\"", s)
	}
	return nil
}

func parseTimestamp(s string) error {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if _, err := time.Parse(layout, s); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%q is not a timestamp; write an RFC 3339 time or a date, as in \"2026-03-01\"", s)
}

// stdKey returns a standard declaration's path, such as "types.Duration".
func stdKey(n *resolve.Node) string {
	if n == nil || n.Decl.File == nil || !n.Decl.File.ID.IsStd() {
		return ""
	}
	return n.Path()
}

// literalKind says what a literal is, for messages: "a number", "a string".
func literalKind(e ast.Expr) string {
	switch e := e.(type) {
	case ast.Literal:
		switch {
		case e.Number() != nil:
			return "a number"
		case e.KwTrue() != nil, e.KwFalse() != nil:
			return "a boolean"
		case e.Raw() != nil:
			return "a string"
		}
	case ast.StringLit:
		return "a string"
	case ast.ProseLines, ast.ProseBlock:
		return "prose"
	case ast.ArrayExpr:
		return "a list"
	case ast.MapExpr:
		return "a map"
	default:
	}
	return ""
}

// value checks an expression against the type it must have. What this
// stage can't narrow, such as a call's result, is only checked for names.
func (c *Checker) value(e ast.Expr, want ty, sc *resolve.Scope, file *resolve.File, p *Param) {
	if e == nil {
		return
	}
	if p != nil && p.Name == "binding" && p.std == "schema" {
		// accept(binding = returns) names the field it admits; it
		// declares the name rather than referring to one.
		return
	}
	d := want.decl
	if want.any || d == nil {
		c.expr(e, sc, file, nil)
		return
	}
	if a := c.aliasOf(d); a != nil {
		d = a
	}
	if want.optional {
		if pe, ok := e.(ast.PathExpr); ok && resolve.PathText(pe.Path().N) == "null" {
			return
		}
	}
	lit := literalKind(e)
	mismatch := func() {
		c.errorf(file, e.Syntax(), "type", "expected %s, got %s", typeName(d), lit)
	}
	if c.IsEnum(d) {
		switch e := e.(type) {
		case ast.PathExpr:
			segs := resolve.PathSegs(e.Path().N)
			if len(segs) == 1 && c.Variant(d, segs[0]) != nil {
				// A bare name where an enum is expected is its variant,
				// as in accept(prose) or actor(role = AGENT).
				return
			}
		case ast.StringLit:
			if s, ok := resolve.StringValue(e.N); ok {
				if c.Variant(d, s) == nil {
					c.errorf(file, e.N, "type", "%q is not a value of %s%s", s, d.Name, c.closestMember(d, s))
				}
				return
			}
		default:
		}
		if lit != "" {
			mismatch()
			return
		}
		c.expr(e, sc, file, nil)
		return
	}
	if parse := TypedStrings[stdKey(d)]; parse != nil {
		switch x := e.(type) {
		case ast.StringLit:
			if s, ok := resolve.StringValue(x.N); ok {
				if err := parse(s); err != nil {
					c.errorf(file, x.N, "typed-string", "%v", err)
				}
				return
			}
		case ast.CastExpr:
			if s, ok := resolve.StringValue(x.Expr().Syntax()); ok {
				if err := parse(s); err != nil {
					c.errorf(file, x.Expr().Syntax(), "typed-string", "%v", err)
				}
			}
			c.typeNames(x.Type(), sc, file)
			return
		default:
		}
		if lit != "" && lit != "a string" {
			mismatch()
			return
		}
		c.expr(e, sc, file, nil)
		return
	}
	if resolve.IsBuiltin(d) {
		if c.strict && c.namesNode(e, sc, file) && d.Name != "Any" {
			// While overloads are tried, a path to a node is a reference,
			// not a scalar, so badge(overview) picks a Ref signature.
			mismatch()
			return
		}
		switch d.Name {
		case "String", "Prose":
			if lit == "a number" || lit == "a boolean" || lit == "a list" || lit == "a map" {
				mismatch()
				return
			}
		case "Int":
			if l, ok := e.(ast.Literal); ok && l.Number() != nil && strings.ContainsAny(l.Number().Text, ".eE") && !strings.HasPrefix(l.Number().Text, "0x") {
				c.errorf(file, e.Syntax(), "type", "expected Int, got %s", l.Number().Text)
				return
			}
			if lit != "" && lit != "a number" {
				mismatch()
				return
			}
		case "Float", "Decimal":
			if lit != "" && lit != "a number" {
				mismatch()
				return
			}
		case "Bool":
			if lit != "" && lit != "a boolean" {
				mismatch()
				return
			}
		case "List", "Set":
			if arr, ok := e.(ast.ArrayExpr); ok {
				el := c.argType(want, 0)
				for _, x := range arr.Exprs() {
					c.value(x, el, sc, file, nil)
				}
				return
			}
			if lit != "" {
				mismatch()
				return
			}
		case "Map":
			if mp, ok := e.(ast.MapExpr); ok {
				v := c.argType(want, 1)
				for _, ent := range mp.MapEntries() {
					c.value(ent.Expr(), v, sc, file, nil)
				}
				return
			}
			if lit != "" && lit != "a map" {
				mismatch()
				return
			}
		}
		c.expr(e, sc, file, nil)
		return
	}
	if IsStd(d, "types", "types.Patch") {
		// Every member of T, each optional, and null clears one.
		inner := c.argType(want, 0)
		if mp, ok := e.(ast.MapExpr); ok && inner.decl != nil && c.IsType(inner.decl) && !c.typeOpen(inner.decl) {
			for _, ent := range mp.MapEntries() {
				key := resolve.PathSegs(ent.Path().N)
				if len(key) == 1 {
					if f, ok := c.typeMember(inner.decl, key[0], map[*resolve.Node]bool{}); ok {
						ft := c.subst(f.Type, inner)
						ft.optional = true
						c.value(ent.Expr(), ft, sc, file, nil)
						continue
					}
					c.errorf(file, ent.Path().N, "unknown-field", "%s has no member %s%s", inner.decl.Name, key[0], c.closestMember(inner.decl, key[0]))
				}
				c.expr(ent.Expr(), sc, file, nil)
			}
			return
		}
		if lit != "" && lit != "a map" {
			mismatch()
			return
		}
		c.expr(e, sc, file, nil)
		return
	}
	if IsStd(d, "types", "types.Stream") {
		// An example's stream: the messages in the order they're sent.
		if arr, ok := e.(ast.ArrayExpr); ok {
			el := c.argType(want, 0)
			for _, x := range arr.Exprs() {
				c.value(x, el, sc, file, nil)
			}
			return
		}
		c.errorf(file, e.Syntax(), "type", "expected a list of the stream's messages, in the order they're sent")
		return
	}
	if IsStd(d, "schema", "schema.Ref") {
		if lit != "" {
			mismatch()
			return
		}
		c.expr(e, sc, file, nil)
		c.refTo(e, c.refKind(want), sc, file)
		return
	}
	if c.IsType(d) {
		switch e := e.(type) {
		case ast.MapExpr:
			// An untyped record written as a map: its keys are members.
			if !c.typeOpen(d) {
				for _, ent := range e.MapEntries() {
					key := resolve.PathSegs(ent.Path().N)
					if len(key) == 1 {
						if f, ok := c.typeMember(d, key[0], map[*resolve.Node]bool{}); ok {
							c.value(ent.Expr(), c.subst(f.Type, want), sc, file, nil)
							continue
						}
						c.errorf(file, ent.Path().N, "unknown-field", "%s has no member %s%s", d.Name, key[0], c.closestMember(d, key[0]))
					}
					c.expr(ent.Expr(), sc, file, nil)
				}
				return
			}
		case ast.Literal, ast.StringLit, ast.ArrayExpr:
			if len(d.Children) > 0 || c.IsInterface(d) {
				mismatch()
				return
			}
		default:
		}
	}
	c.expr(e, sc, file, nil)
}

// namesNode reports whether an expression is a path that resolves to a
// declared node rather than a local or a value.
func (c *Checker) namesNode(e ast.Expr, sc *resolve.Scope, file *resolve.File) bool {
	pe, ok := e.(ast.PathExpr)
	if !ok {
		return false
	}
	b := c.lookupBinding(sc, file, off(pe.N), resolve.PathSegs(pe.Path().N))
	if b.Node == nil || resolve.IsBuiltin(b.Node) {
		return false
	}
	switch b.Node.Shape {
	case resolve.ShapeSet, resolve.ShapeValue:
		return false
	}
	return true
}

func typeName(d *resolve.Node) string {
	if d == nil {
		return "a value"
	}
	return d.Name
}

// --- names in expressions, prose and code ---

// locals are the names a `do` block binds, innermost first.
type locals struct {
	parent *locals
	names  map[string]bool
}

func (l *locals) has(n string) bool {
	for x := l; x != nil; x = x.parent {
		if x.names[n] {
			return true
		}
	}
	return false
}

func (l *locals) child() *locals { return &locals{parent: l, names: map[string]bool{}} }

// expr checks that every name in an expression resolves.
func (c *Checker) expr(e ast.Expr, sc *resolve.Scope, file *resolve.File, loc *locals) {
	switch e := e.(type) {
	case ast.PathExpr:
		c.pathRef(e.Path().N, sc, file, loc)
	case ast.CallExpr:
		switch callee := e.Callee().(type) {
		case ast.PathExpr:
			d := c.pathRef(callee.Path().N, sc, file, loc)
			switch {
			case d == nil:
				c.argExprsIn(e.Args(), sc, file, loc)
			case c.IsKind(d):
				c.args(e.Args(), c.Params(d), "kind "+d.Name, sc, file, false)
			case c.IsType(d):
				c.construct(d, e.Args(), sc, file)
			default:
				if loc != nil {
					c.argExprsIn(e.Args(), sc, file, loc)
				} else {
					c.call(d, e.Args(), sc, file)
				}
			}
		default:
			c.expr(callee, sc, file, loc)
			c.argExprsIn(e.Args(), sc, file, loc)
		}
	case ast.FieldExpr:
		c.expr(e.Expr(), sc, file, loc)
	case ast.BinaryExpr:
		c.expr(e.Lhs(), sc, file, loc)
		c.expr(e.Rhs(), sc, file, loc)
	case ast.UnaryExpr:
		c.expr(e.Expr(), sc, file, loc)
	case ast.ParenExpr:
		c.expr(e.Expr(), sc, file, loc)
	case ast.ArrayExpr:
		for _, x := range e.Exprs() {
			c.expr(x, sc, file, loc)
		}
	case ast.MapExpr:
		for _, ent := range e.MapEntries() {
			c.expr(ent.Expr(), sc, file, loc)
		}
	case ast.CastExpr:
		c.typeNames(e.Type(), sc, file)
		c.expr(e.Expr(), sc, file, loc)
	case ast.IfExpr:
		c.ifExpr(e, sc, file, loc)
	case ast.ForExpr:
		c.expr(e.Expr(), sc, file, loc)
		inner := loc.child()
		if id := e.Ident(); id != nil {
			inner.names[id.Text] = true
		}
		c.block(e.Code(), sc, file, inner)
	case ast.StringLit:
		// The parser puts a string's inserts directly under it.
		for _, ins := range childrenOf(e.N, syntax.Insert) {
			c.insert(ast.Insert{N: ins}, sc, file, loc)
		}
	case ast.ProseLines, ast.ProseBlock:
		c.prose(e.Syntax(), sc, file)
	default:
	}
}

func (c *Checker) ifExpr(e ast.IfExpr, sc *resolve.Scope, file *resolve.File, loc *locals) {
	c.expr(e.Cond(), sc, file, loc)
	c.block(e.Then(), sc, file, loc.child())
	if e.Else().N != nil {
		c.block(e.Else(), sc, file, loc.child())
	}
	if e.ElseIf().N != nil {
		c.ifExpr(e.ElseIf(), sc, file, loc)
	}
}

func (c *Checker) argExprsIn(args ast.Args, sc *resolve.Scope, file *resolve.File, loc *locals) {
	for _, a := range args.Args() {
		switch a := a.(type) {
		case ast.PositionalArg:
			c.expr(a.Expr(), sc, file, loc)
		case ast.NamedArg:
			c.expr(a.Expr(), sc, file, loc)
		}
	}
}

// refKind is the kind K of a Ref(K), or nil. K is read as a declared kind,
// past the parameters in scope, since in std/schema's `accept(kind?:
// Ref(kind))` the parameter has the kind's name.
func (c *Checker) refKind(want ty) *resolve.Node {
	for _, a := range want.args {
		pa, ok := a.(ast.PositionalArg)
		if !ok {
			continue
		}
		pe, ok := pa.Expr().(ast.PathExpr)
		if !ok {
			return nil
		}
		n := c.lookupPath(want.scope, want.file, off(pe.N), resolve.PathSegs(pe.Path().N), c.IsKind)
		if n != nil && c.IsKind(n) {
			return n
		}
		return nil
	}
	return nil
}

// refTo resolves a path written where a Ref(K) is wanted to the first
// binding that is an instance of K, so `accept(kind = open)` names the kind
// open even where a flag open is imported first.
func (c *Checker) refTo(e ast.Expr, k *resolve.Node, sc *resolve.Scope, file *resolve.File) {
	pe, ok := e.(ast.PathExpr)
	if !ok || k == nil || resolve.IsBuiltin(k) || !c.IsKind(k) {
		return
	}
	b := c.lookupWanted(sc, file, off(pe.N), resolve.PathSegs(pe.Path().N), func(n *resolve.Node) bool {
		kind := c.KindOf(n)
		return kind != nil && c.Fits(kind, []*resolve.Node{k})
	})
	if b.Node != nil {
		c.Refs[pe.Path().N] = b.Node
	}
}

// pathRef checks a path in an expression: its first name resolves, and the
// rest are children or fields of what it names. It returns the node the
// whole path names, when it names one.
func (c *Checker) pathRef(p *syntax.Node, sc *resolve.Scope, file *resolve.File, loc *locals) *resolve.Node {
	segs := resolve.PathSegs(p)
	if len(segs) == 0 || sc == nil {
		return nil
	}
	if loc.has(segs[0]) {
		return nil
	}
	switch segs[0] {
	case "true", "false", "null":
		return nil
	}
	at := off(p)
	var heads []resolve.Binding
	sc.Each(segs[0], file, at, func(b resolve.Binding) bool {
		heads = append(heads, b)
		return true
	})
	if len(heads) == 0 {
		c.errorf(file, p, "unresolved", "unknown name %s%s", segs[0], c.closest(sc, segs[0]))
		return nil
	}
	for _, h := range heads {
		if h.Local != nil {
			c.LocalUses[h.Local]++
			return nil
		}
		if n := c.walkSegs(h.Node, segs[1:]); n != nil {
			c.Refs[p] = n
			return n
		}
	}
	// No candidate has every segment as a child: the rest may be fields.
	n := heads[0].Node
	for i, s := range segs[1:] {
		if ch := n.Child(s); ch != nil {
			n = ch
			continue
		}
		if _, ok := c.Field(n, s); ok {
			return nil
		}
		if c.IsType(n) || c.IsKind(n) || n.Shape == resolve.ShapeAnon {
			c.errorf(file, p, "unresolved", "%s has no member %s", strings.Join(segs[:i+1], "."), s)
		}
		return nil
	}
	return n
}

func (c *Checker) insert(ins ast.Insert, sc *resolve.Scope, file *resolve.File, loc *locals) {
	if ins.N == nil {
		return
	}
	if p := ins.Path(); p.N != nil {
		c.pathRef(p.N, sc, file, loc)
		c.factPath(p.N, sc, file, loc)
		c.argExprsIn(ins.Args(), sc, file, loc)
	}
	if e := ins.Expr(); e != nil {
		c.expr(e, sc, file, loc)
		if pe, ok := e.(ast.PathExpr); ok {
			c.factPath(pe.Path().N, sc, file, loc)
		}
	}
}

// factPath checks an insertion's path strictly: past the nodes it names,
// only its last name may be a field or a parameter of the node's kind, so
// a page stops checking once the fact it inserts is gone.
func (c *Checker) factPath(p *syntax.Node, sc *resolve.Scope, file *resolve.File, loc *locals) {
	segs := resolve.PathSegs(p)
	if len(segs) < 2 || sc == nil || loc.has(segs[0]) {
		return
	}
	var heads []*resolve.Node
	sc.Each(segs[0], file, off(p), func(b resolve.Binding) bool {
		if b.Local != nil {
			heads = nil
			return false
		}
		heads = append(heads, b.Node)
		return true
	})
	if len(heads) == 0 {
		return
	}
	best, bestAt := heads[0], 0
	for _, h := range heads {
		// i is the first name past the nodes this candidate names.
		n, i := h, 1
		for ; i < len(segs); i++ {
			ch := n.Child(segs[i])
			if ch == nil {
				break
			}
			n = ch
		}
		if i == len(segs) {
			return
		}
		if i > bestAt {
			best, bestAt = n, i
		}
	}
	n := best
	if bestAt == 0 {
		bestAt = 1
	}
	if bestAt == len(segs)-1 {
		if _, ok := c.Field(n, segs[bestAt]); ok {
			return
		}
		// A parameter the node declares itself, with a default to show.
		for _, pr := range c.Params(n) {
			if pr.Name == segs[bestAt] && pr.N != nil && (ast.Param{N: pr.N}).Default() != nil {
				return
			}
		}
	}
	c.errorf(file, p, "unresolved", "unknown name %s%s", strings.Join(segs[:bestAt+1], "."), c.closestField(n, segs[bestAt]))
}

// prose checks the links, inserts and tags of a text.
func (c *Checker) prose(n *syntax.Node, sc *resolve.Scope, file *resolve.File) {
	if n == nil {
		return
	}
	if n.Kind == syntax.Tag && c.Tree.ElementNode(n) != nil {
		// A node of its own, checked as one.
		return
	}
	switch n.Kind {
	case syntax.Link, syntax.ShortLink, syntax.Insert, syntax.Tag:
		// A prose file's items are inline forms themselves.
		c.proseChildren(&syntax.Node{Kind: syntax.ProseLine, Children: []syntax.Elem{n}}, sc, file)
		return
	}
	c.proseChildren(n, sc, file)
}

func (c *Checker) proseChildren(n *syntax.Node, sc *resolve.Scope, file *resolve.File) {
	for _, ch := range n.Children {
		ch, ok := ch.(*syntax.Node)
		if !ok {
			continue
		}
		switch ch.Kind {
		case syntax.Annotation:
			// An annotation on a prose block, such as `@lens(user)`.
			c.annotation(resolve.Site{File: file, N: ch, Scope: sc})
		case syntax.Link:
			l := ast.Link{N: ch}
			if l.Hash() != nil && l.Path().N != nil {
				p := l.Path().N
				c.checkLinkPath(p, sc, file, func(segs []string, hint string) {
					c.errorf(file, p, "unresolved", "the link to #%s doesn't resolve%s", strings.Join(segs, "."), hint)
				})
			}
			for _, ins := range l.Inserts() {
				c.insert(ins, sc, file, nil)
			}
			c.proseChildren(ch, sc, file)
		case syntax.ShortLink:
			c.shortLink(ast.ShortLink{N: ch}, sc, file)
		case syntax.Insert:
			c.insert(ast.Insert{N: ch}, sc, file, nil)
		case syntax.Tag:
			if c.Tree.ElementNode(ch) != nil {
				// A node of its own, checked as one.
				continue
			}
			c.tag(ast.Tag{N: ch}, sc, file)
			c.proseChildren(ch, sc, file)
		case syntax.Fence:
		default:
			c.proseChildren(ch, sc, file)
		}
	}
}

// tag checks a component: its name is a kind in scope, and its attributes
// are the kind's arguments.
func (c *Checker) tag(t ast.Tag, sc *resolve.Scope, file *resolve.File) {
	segs := resolve.PathSegs(t.Path().N)
	if len(segs) == 0 {
		return
	}
	d := c.lookupPath(sc, file, -1, segs, c.IsKind)
	if d == nil {
		c.errorf(file, t.Path().N, "unresolved", "unknown tag <%s>: a tag names a kind in scope%s", strings.Join(segs, "."), c.closestWhere(sc, segs[0], c.IsKind))
		return
	}
	c.Refs[t.Path().N] = d
	if !c.IsKind(d) {
		return // the tag-kind lint reports it
	}
	if c.kindArgBool(d, "heading") || IsStd(d, "spec", "spec.term") {
		// A section, a step or a term is a node: it needs an id.
		c.errorf(file, t.Path().N, "unnamed", "<%s> declares a node and needs an id", strings.Join(segs, "."))
	}
	params := c.Params(d)
	given := map[string]bool{}
	for _, a := range t.TagAttrs() {
		id := a.Ident()
		if id == nil || a.At() != nil {
			continue
		}
		given[id.Text] = true
		p := findParam(params, id.Text)
		if p == nil {
			c.errorf(file, a.N, "unknown-argument", "<%s> takes no attribute %s%s", d.Name, id.Text, closestParam(params, id.Text))
			continue
		}
		switch {
		case a.LBrace() != nil:
			// A value in braces, `depth={"two"}`, is an expression, even
			// when it's a string, and is checked against the type.
			if e := a.Expr(); e != nil {
				c.value(e, p.Type, sc, file, p)
			}
		case a.StringLit().N != nil && c.refParam(p.Type):
			// A reference, resolved as a link's path is.
			c.attrRef(a.StringLit().N, sc, file)
		case a.StringLit().N != nil:
			want := p.Type
			if want.decl != nil && resolve.IsBuiltin(want.decl) && want.decl.Name != "String" && want.decl.Name != "Prose" {
				// A quoted attribute is a literal of its parameter's type.
				raw, _ := resolve.StringValue(a.StringLit().N)
				if _, ok := c.literal(raw, want); !ok && (want.decl.Name == "Int" || want.decl.Name == "Float" || want.decl.Name == "Decimal" || want.decl.Name == "Bool") {
					c.errorf(file, a.StringLit().N, "type", "expected %s, got a string", want.decl.Name)
				}
				c.expr(a.StringLit(), sc, file, nil)
				continue
			}
			c.value(a.StringLit(), want, sc, file, p)
		case a.Expr() != nil:
			c.value(a.Expr(), p.Type, sc, file, p)
		}
	}
	for _, p := range params {
		if !p.Optional && !p.Variadic && !given[p.Name] {
			c.errorf(file, t.Path().N, "missing-argument", "<%s> needs the attribute %s", d.Name, p.Name)
		}
	}
}

// code checks a `do` block: names resolve, and a local is usable only
// below the line that declares it.
func (c *Checker) code(n *syntax.Node, sc *resolve.Scope, file *resolve.File) {
	if n.Kind == syntax.Action {
		n = childOf(n, syntax.DoBlock)
	}
	if n != nil && n.Kind == syntax.DoBlock {
		n = childOf(n, syntax.Code)
	}
	if n == nil {
		return
	}
	c.block(ast.Code{N: n}, sc, file, (*locals)(nil).child())
}

func (c *Checker) block(code ast.Code, sc *resolve.Scope, file *resolve.File, loc *locals) {
	if code.N == nil {
		return
	}
	for _, st := range code.Stmts() {
		switch st := st.(type) {
		case ast.LetStmt:
			c.expr(st.Expr(), sc, file, loc)
			if t := st.Type(); t != nil {
				c.typeNames(t, sc, file)
			}
			if id := st.Ident(); id != nil {
				loc.names[id.Text] = true
			}
		case ast.AssignStmt:
			c.pathRef(st.Path().N, sc, file, loc)
			c.expr(st.Expr(), sc, file, loc)
		case ast.ReturnStmt:
			c.expr(st.Expr(), sc, file, loc)
		case ast.ExprStmt:
			c.expr(st.Expr(), sc, file, loc)
			if st.Code().N != nil {
				c.block(st.Code(), sc, file, loc.child())
			}
		default:
		}
	}
}
