package types

import (
	"regexp"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// A kind element that declares a node is checked as a declaration: its
// name resolves to a kind as a name in code does, then by suffix; its
// attributes are the kind's arguments, a quoted one a literal of its
// parameter's type and a braced one an expression; its `@name` attributes
// are annotations; and its content is the kind's body.

var pathText = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// elementKind resolves an element's name to a kind: the one the enclosing
// kind accepts first, then any kind in scope, then by suffix.
func (c *Checker) elementKind(n *resolve.Node) *resolve.Node {
	p := ast.Tag{N: n.Decl.N}.Path().N
	segs := resolve.PathSegs(p)
	if len(segs) == 0 {
		return nil
	}
	sc, file := n.Decl.Scope, n.Decl.File
	if n.Parent != nil && c.KindOf(n.Parent) != nil {
		pred := func(x *resolve.Node) bool { return c.IsKind(x) && c.Accepts(n.Parent, x) }
		if k := c.lookupPath(sc, file, -1, segs, pred); k != nil && pred(k) {
			return k
		}
	}
	if k := c.lookupPath(sc, file, -1, segs, c.IsKind); k != nil {
		return k
	}
	var kinds []*resolve.Node
	for _, m := range c.suffixMatches(segs) {
		if c.IsKind(m) {
			kinds = append(kinds, m)
		}
	}
	if len(kinds) == 1 {
		return kinds[0]
	}
	return nil
}

// element checks an element that declares a node.
func (c *Checker) element(n *resolve.Node) {
	tag := ast.Tag{N: n.Decl.N}
	file, sc := n.Decl.File, n.Decl.Scope
	p := tag.Path().N
	segs := resolve.PathSegs(p)
	k := c.metaOf(n).decl
	if k == nil {
		c.errorf(file, p, "unresolved", "unknown tag <%s>: a tag names a kind in scope%s", strings.Join(segs, "."), c.closestWhere(sc, segs[len(segs)-1], c.IsKind))
		return
	}
	c.Refs[p] = k
	if n.Name == "" && (c.kindArgBool(k, "heading") || IsStd(k, "doc", "doc.term")) {
		c.errorf(file, p, "unnamed", "<%s> declares a node and needs an id", strings.Join(segs, "."))
	}
	if c.acceptsMode(k, "code") && !c.acceptsMode(k, "prose") {
		c.errorf(file, p, "element-code", "<%s>'s body is code; write it in a .heron file", strings.Join(segs, "."))
	}
	// `@name="…"` reads as written where the annotation takes no string.
	n.Annotations = resolve.ElementAnnotations(file, n.Decl.N, sc, func(name string) bool {
		d := c.lookupPath(sc, file, -1, []string{name}, c.IsAnnotation)
		if d == nil {
			return false
		}
		ps := c.Params(d)
		if len(ps) == 0 {
			return true
		}
		t := ps[0].Type.decl
		return !(resolve.IsBuiltin(t) && (t.Name == "String" || t.Name == "Prose"))
	})
	params := c.KindParams(k, n)
	n.ElementArgs = c.elementArgs(tag, params, sc, file)
	c.args(n.ElementArgs, params, "<"+k.Name+">", sc, file, true)
	// A quoted attribute that names a node links from its text too, for
	// go-to-definition and hover.
	for _, a := range tag.TagAttrs() {
		if a.At() != nil || a.Ident() == nil || a.StringLit().N == nil {
			continue
		}
		if p := findParam(params, a.Ident().Text); p != nil {
			raw, _ := resolve.StringValue(a.StringLit().N)
			if v, ok := c.literal(raw, p.Type); ok && pathText.MatchString(v) && !c.IsEnum(p.Type.decl) {
				if t, _ := c.linkTarget(sc, file, strings.Split(v, ".")); t != nil {
					c.Refs[a.StringLit().N] = t
				}
			}
		}
	}
}

// elementArgs writes an element's attributes as the arguments of its kind:
// `id` and `@name` attributes aside, a quoted value as a literal of its
// parameter's type and a braced one as the expression it holds, parsed
// apart and placed at the attributes' text.
func (c *Checker) elementArgs(tag ast.Tag, params []Param, sc *resolve.Scope, file *resolve.File) ast.Args {
	type piece struct {
		text       string
		from, at   int // the value's offset in the text written, and in the file
		attrOffset int
	}
	var src strings.Builder
	src.WriteString("x(")
	var pieces []piece
	// A variadic parameter's attribute is written first, as positional
	// arguments: `targets="a"` is `x(a)`, and `targets={[a, b]}` is `x(a, b)`.
	variadic := func(a ast.TagAttr) bool {
		p := findParam(params, a.Ident().Text)
		return p != nil && p.Variadic
	}
	var attrs []ast.TagAttr
	for _, pass := range []bool{true, false} {
		for _, a := range tag.TagAttrs() {
			if a.At() == nil && a.Ident() != nil && variadic(a) == pass {
				attrs = append(attrs, a)
			}
		}
	}
	for _, a := range attrs {
		if a.At() != nil || a.Ident() == nil || a.Ident().Text == "id" {
			continue
		}
		name := a.Ident().Text
		as, _ := a.N.Span()
		val, at := "true", as
		switch {
		case a.LBrace() != nil && a.Expr() != nil:
			val = a.Expr().Syntax().Text()
			at, _ = a.Expr().Syntax().Span()
		case a.StringLit().N != nil:
			lit := a.StringLit().N
			at, _ = lit.Span()
			raw := strings.Trim(lit.Text(), `"`)
			val = lit.Text()
			if p := findParam(params, name); p != nil {
				if v, ok := c.literal(raw, p.Type); ok {
					val, at = v, at+1
					if c.refParam(p.Type) {
						val = c.reachable(v, sc, file)
					}
				}
			}
		}
		if len(pieces) > 0 {
			src.WriteString(", ")
		}
		if variadic(a) {
			if a.LBrace() != nil && strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
				val, at = val[1:len(val)-1], at+1
			}
		} else {
			src.WriteString(name + " = ")
		}
		pieces = append(pieces, piece{text: val, from: src.Len(), at: at, attrOffset: as})
		src.WriteString(val)
	}
	src.WriteString(")\n")
	t := syntax.Parse("element.heron", src.String())
	var args *syntax.Node
	t.Root.Walk(func(x *syntax.Node) bool {
		if args == nil && x.Kind == syntax.Args {
			args = x
		}
		return args == nil
	})
	if args == nil {
		return ast.Args{}
	}
	if len(pieces) > 0 {
		syntax.Shift(args, func(o int) int {
			for _, p := range pieces {
				if o >= p.from && o < p.from+len(p.text) {
					return min(p.at+(o-p.from), p.at+len(p.text))
				}
			}
			// A name, `=`, a comma or a parenthesis: at the attribute it
			// leads to.
			for _, p := range pieces {
				if p.from > o {
					return p.attrOffset
				}
			}
			return pieces[len(pieces)-1].attrOffset
		})
	}
	return ast.Args{N: args}
}

// literal reads a quoted attribute as a literal of a parameter's type: a
// number or a boolean as written, an enum member by its name, and a
// reference as the path it names; ok is false where it stays a string.
func (c *Checker) literal(raw string, t ty) (string, bool) {
	d := t.decl
	if d == nil {
		return "", false
	}
	if resolve.IsBuiltin(d) {
		switch d.Name {
		case "Int", "Float", "Decimal":
			return raw, raw != "" && strings.Trim(raw, "0123456789.-_") == ""
		case "Bool":
			return raw, raw == "true" || raw == "false"
		}
		return "", false
	}
	if !pathText.MatchString(raw) {
		return "", false
	}
	if c.IsEnum(d) && !strings.Contains(raw, ".") {
		return d.Name + "." + raw, true
	}
	if c.IsType(d) && !c.IsEnum(d) && d.Name != "Ref" {
		// A record or a typed string is written as a string.
		return "", false
	}
	return raw, true
}

// refParam reports whether a parameter takes a reference to a node.
func (c *Checker) refParam(t ty) bool {
	return t.decl != nil && !resolve.IsBuiltin(t.decl) && t.decl.Name == "Ref"
}

// attrRef resolves a quoted attribute that names a node, as `<ref
// to="refund_window"/>` does, as a link's path is resolved.
func (c *Checker) attrRef(lit *syntax.Node, sc *resolve.Scope, file *resolve.File) {
	raw, _ := resolve.StringValue(lit)
	if !pathText.MatchString(raw) {
		c.errorf(file, lit, "unresolved", "[%s] names no node", raw)
		return
	}
	segs := strings.Split(raw, ".")
	t, cands := c.linkTarget(sc, file, segs)
	switch {
	case t != nil:
		c.Refs[lit] = t
	case len(cands) > 1:
		c.ambiguousLink(lit, file, segs, cands)
	default:
		var names []string
		for k := range c.links().own {
			names = append(names, k)
		}
		c.errorf(file, lit, "unresolved", "[%s] names no node%s", raw, suggest(segs[len(segs)-1], names))
	}
}

// reachable writes a reference resolved as a link's path is as a path the
// element's scope reaches it by, so `by="desk"` names the actor `desk`
// wherever it's declared.
func (c *Checker) reachable(v string, sc *resolve.Scope, file *resolve.File) string {
	segs := strings.Split(v, ".")
	if c.lookupPath(sc, file, -1, segs, nil) != nil {
		return v
	}
	t, _ := c.linkTarget(sc, file, segs)
	if t == nil {
		return v
	}
	if p := t.Path(); c.lookupPath(sc, file, -1, strings.Split(p, "."), nil) == t {
		return p
	}
	return v
}
