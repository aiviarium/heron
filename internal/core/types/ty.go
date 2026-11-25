package types

import (
	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// ty is a type as written: its declaration and its arguments, read lazily.
type ty struct {
	decl     *resolve.Node
	args     []ast.Arg
	scope    *resolve.Scope
	file     *resolve.File
	optional bool
	// any is set for what this stage doesn't narrow: unions, intersections,
	// type parameters and names that didn't resolve.
	any bool
	// param names the type parameter a type is written as, such as T in
	// a member `items: List(T)`; env binds type parameters to the
	// arguments of the generic type the member was read through.
	param string
	env   map[string]ty
}

// subst reads a member's type through the generic type that holds it, so
// `items: List(T)` of `Page(Refund)` is a List(Refund).
func (c *Checker) subst(member, owner ty) ty {
	env := c.bindParams(owner)
	if len(env) == 0 {
		return member
	}
	if member.param != "" {
		if t, ok := env[member.param]; ok {
			t.optional = t.optional || member.optional
			return t
		}
		return member
	}
	member.env = env
	return member
}

// bindParams maps a generic type's parameters to the arguments it is
// written with.
func (c *Checker) bindParams(owner ty) map[string]ty {
	if owner.decl == nil || len(owner.args) == 0 {
		return nil
	}
	ps := c.Params(owner.decl)
	if len(ps) == 0 {
		return nil
	}
	env := map[string]ty{}
	pos := 0
	for _, a := range owner.args {
		switch a := a.(type) {
		case ast.PositionalArg:
			if pos < len(ps) {
				env[ps[pos].Name] = c.withEnv(c.typeOfExpr(a.Expr(), owner.scope, owner.file, false), owner.env)
				pos++
			}
		case ast.NamedArg:
			if id := a.Ident(); id != nil {
				env[id.Text] = c.withEnv(c.typeOfExpr(a.Expr(), owner.scope, owner.file, false), owner.env)
			}
		}
	}
	return env
}

// withEnv resolves a type parameter through env, or carries env along.
func (c *Checker) withEnv(t ty, env map[string]ty) ty {
	if len(env) == 0 {
		return t
	}
	if t.param != "" {
		if b, ok := env[t.param]; ok {
			b.optional = b.optional || t.optional
			return b
		}
	}
	if t.env == nil {
		t.env = env
	}
	return t
}

// typeOf reads a type in the `:` slot or a parameter.
func (c *Checker) typeOf(t ast.Type, sc *resolve.Scope, file *resolve.File) ty {
	switch t := t.(type) {
	case ast.NamedType:
		segs := resolve.PathSegs(t.Path().N)
		b := c.lookupSlot(sc, file, off(t.N), segs)
		if b.Local != nil && len(segs) == 1 {
			return ty{any: true, param: segs[0]}
		}
		if b.Local != nil || b.Node == nil {
			return ty{any: true}
		}
		d := b.Node
		if resolve.IsBuiltin(d) && d.Name == "Self" {
			d = enclosingType(sc)
		}
		return ty{decl: d, args: t.Args().Args(), scope: sc, file: file}
	case ast.OptionalType:
		in := c.typeOf(t.Type(), sc, file)
		in.optional = true
		return in
	default:
	}
	return ty{any: true}
}

// typeOfExpr reads a type written where an expression stands: a type
// argument, a cast, or an alias's value.
func (c *Checker) typeOfExpr(e ast.Expr, sc *resolve.Scope, file *resolve.File, _ bool) ty {
	switch e := e.(type) {
	case ast.PathExpr:
		segs := resolve.PathSegs(e.Path().N)
		b := c.lookupSlot(sc, file, off(e.N), segs)
		if b.Local != nil && len(segs) == 1 {
			return ty{any: true, param: segs[0]}
		}
		if b.Node == nil {
			return ty{any: true}
		}
		return ty{decl: b.Node, scope: sc, file: file}
	case ast.CallExpr:
		if p, ok := e.Callee().(ast.PathExpr); ok {
			b := c.lookupSlot(sc, file, off(p.N), resolve.PathSegs(p.Path().N))
			if b.Node == nil {
				return ty{any: true}
			}
			return ty{decl: b.Node, args: e.Args().Args(), scope: sc, file: file}
		}
	default:
	}
	return ty{any: true}
}

// enclosingType is what `Self` means: the nearest type, enum or union whose
// body the scope is in.
func enclosingType(sc *resolve.Scope) *resolve.Node {
	for s := sc; s != nil; s = s.Parent {
		if s.Node != nil && s.Node.Shape != resolve.ShapeRoot {
			return s.Node
		}
	}
	return nil
}

// argType returns the i-th positional type argument, such as T in List(T).
func (c *Checker) argType(t ty, i int) ty {
	k := 0
	for _, a := range t.args {
		if pa, ok := a.(ast.PositionalArg); ok {
			if k == i {
				return c.withEnv(c.typeOfExpr(pa.Expr(), t.scope, t.file, false), t.env)
			}
			k++
		}
	}
	return ty{any: true}
}

// lookupBinding resolves a dotted path: its first segment through the scope
// and the rest through children and fields.
func (c *Checker) lookupBinding(sc *resolve.Scope, file *resolve.File, at int, segs []string) resolve.Binding {
	if sc == nil || len(segs) == 0 {
		return resolve.Binding{}
	}
	var out resolve.Binding
	sc.Each(segs[0], file, at, func(b resolve.Binding) bool {
		if b.Local != nil {
			out = b
			return false
		}
		if n := c.walkSegs(b.Node, segs[1:]); n != nil {
			out = resolve.Binding{Node: n}
			return false
		}
		return true
	})
	return out
}

// Names resolve by the position they're written in, not by import order:
// when packages bind one name to a kind, a flag, an annotation, a category
// or a type, each position takes the first binding of what it wants, and
// only when none is that, the first binding, so the caller can say what
// it is. These are what the positions want.

// isKindOrType is what a `:` slot, a parameter's type, a type argument and
// args_from or fields_from want.
func (c *Checker) isKindOrType(n *resolve.Node) bool {
	return c.IsKind(n) || c.IsType(n) || resolve.IsBuiltin(n)
}

// isKindOrCategory is what a flag's or an annotation's on = […] wants.
func (c *Checker) isKindOrCategory(n *resolve.Node) bool { return c.IsKind(n) || c.IsCategory(n) }

// lookupWanted resolves a dotted path written where pred says what is
// wanted: the first binding that is, a local first of all, and else the
// first binding.
func (c *Checker) lookupWanted(sc *resolve.Scope, file *resolve.File, at int, segs []string, pred func(*resolve.Node) bool) resolve.Binding {
	if sc == nil || len(segs) == 0 {
		return resolve.Binding{}
	}
	var first, match resolve.Binding
	sc.Each(segs[0], file, at, func(b resolve.Binding) bool {
		if b.Local != nil {
			if first.Node == nil && first.Local == nil {
				match = b
				return false
			}
			return true
		}
		n := c.walkSegs(b.Node, segs[1:])
		if n == nil {
			return true
		}
		if first.Node == nil {
			first = resolve.Binding{Node: n}
		}
		if pred(n) {
			match = resolve.Binding{Node: n}
			return false
		}
		return true
	})
	if match.Node != nil || match.Local != nil {
		return match
	}
	return first
}

// lookupSlot resolves a name where a kind or a type is wanted: a `:` slot,
// a parameter's type or a type argument.
func (c *Checker) lookupSlot(sc *resolve.Scope, file *resolve.File, at int, segs []string) resolve.Binding {
	return c.lookupWanted(sc, file, at, segs, c.isKindOrType)
}

// lookupPath resolves a path to a node, taking the first that pred accepts
// (or the first found when none does, so callers can say what it is).
func (c *Checker) lookupPath(sc *resolve.Scope, file *resolve.File, at int, segs []string, pred func(*resolve.Node) bool) *resolve.Node {
	if sc == nil || len(segs) == 0 {
		return nil
	}
	var first, match *resolve.Node
	sc.Each(segs[0], file, at, func(b resolve.Binding) bool {
		if b.Local != nil {
			return true
		}
		n := c.walkSegs(b.Node, segs[1:])
		if n == nil {
			return true
		}
		if first == nil {
			first = n
		}
		if pred == nil || pred(n) {
			match = n
			return false
		}
		return true
	})
	if match != nil {
		return match
	}
	return first
}

func (c *Checker) walkSegs(n *resolve.Node, segs []string) *resolve.Node {
	for _, s := range segs {
		if n == nil {
			return nil
		}
		n = n.Child(s)
	}
	return n
}

// spanOf returns a syntax node's range.
func spanOf(n *syntax.Node) (int, int) {
	if n == nil {
		return 0, 0
	}
	return n.Span()
}
