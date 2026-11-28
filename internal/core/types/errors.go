package types

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// ErrorDescendants are the errors that extend an error, directly or
// through others, in the tree and the context it's checked against.
func (c *Checker) ErrorDescendants(n *resolve.Node) []*resolve.Node {
	if c.errKids == nil {
		c.errKids = map[*resolve.Node][]*resolve.Node{}
		seen := map[*resolve.Node]bool{}
		var walk func(x *resolve.Node)
		walk = func(x *resolve.Node) {
			if x == nil || seen[x] {
				return
			}
			seen[x] = true
			for _, ch := range x.Children {
				if c.IsError(ch) {
					if p := c.ErrorParent(ch); p != nil {
						c.errKids[p] = append(c.errKids[p], ch)
					}
				}
				walk(ch)
			}
		}
		if c.Tree != nil {
			walk(c.Tree.Root)
			for sc := c.Tree.Context; sc != nil; sc = sc.Parent {
				walk(sc.Node)
			}
		}
	}
	var out []*resolve.Node
	seen := map[*resolve.Node]bool{n: true}
	var add func(x *resolve.Node)
	add = func(x *resolve.Node) {
		for _, k := range c.errKids[x] {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
				add(k)
			}
		}
	}
	add(n)
	return out
}

// ErrorID is the id an error is reported with: its `id`, or its name.
func (c *Checker) ErrorID(n *resolve.Node) string {
	if s, ok := resolve.StringValue(namedArgSyntax(n, "id")); ok {
		return s
	}
	return n.Name
}

// ErrorStatusText is the text shown beside an error's status: its
// `status_text`, or its id. Neither is inherited.
func (c *Checker) ErrorStatusText(n *resolve.Node) string {
	if s, ok := resolve.StringValue(namedArgSyntax(n, "status_text")); ok {
		return s
	}
	return c.ErrorID(n)
}

// ErrorStatus is an error's HTTP status as written, its own or the one it
// inherits, or "" when neither gives one.
func (c *Checker) ErrorStatus(n *resolve.Node) string {
	for _, x := range append([]*resolve.Node{n}, c.ErrorAncestors(n)...) {
		if e := c.errorStatusExpr(x); e != nil {
			return strings.TrimSpace(e.Syntax().Text())
		}
	}
	return ""
}

func (c *Checker) errorStatusExpr(n *resolve.Node) ast.Expr {
	if e := namedArg(n, "status"); e != nil {
		return e
	}
	// The root signature takes the status first; the extending one, second.
	pos := 0
	if c.ErrorParent(n) != nil {
		pos = 1
	}
	i := 0
	for _, a := range declArgs(n) {
		if pa, ok := a.(ast.PositionalArg); ok {
			if i == pos {
				return pa.Expr()
			}
			i++
		}
	}
	return nil
}

// ErrorField is a field an error sets, its own or the nearest ancestor's,
// as its `title`: the node that sets it, or nil.
func (c *Checker) ErrorField(n *resolve.Node, name string) *resolve.Node {
	for _, x := range append([]*resolve.Node{n}, c.ErrorAncestors(n)...) {
		if ch := x.Child(name); ch != nil && ch.Shape == resolve.ShapeSet {
			return ch
		}
	}
	return nil
}

// ErrorAnnotated is the error an annotation is inherited from, as an
// override such as `@exit`: the error itself, or the nearest ancestor
// that carries one, as `has` tells.
func (c *Checker) ErrorAnnotated(n *resolve.Node, has func(*resolve.Node) bool) *resolve.Node {
	for _, x := range append([]*resolve.Node{n}, c.ErrorAncestors(n)...) {
		if has(x) {
			return x
		}
	}
	return nil
}

// checkError reports an error that extends itself, and a member it
// declares again with another type than the one it inherits.
func (c *Checker) checkError(n *resolve.Node) {
	p := c.ErrorParent(n)
	if p == nil || n.Decl.File == nil {
		return
	}
	path := []string{n.Name}
	seen := map[*resolve.Node]bool{n: true}
	for x := p; x != nil; x = c.ErrorParent(x) {
		path = append(path, x.Name)
		if x == n {
			c.errorf(n.Decl.File, pathNode(n.Decl.N), "extends", "error %s extends itself: %s", n.Name, strings.Join(path, " extends "))
			return
		}
		if seen[x] {
			return
		}
		seen[x] = true
	}
	for _, ch := range n.Children {
		if ch.Shape != resolve.ShapeDeclared && ch.Shape != resolve.ShapeValue || ch.Decl.N == nil {
			continue
		}
		own := memberType(ch)
		if own == "" {
			continue
		}
		for _, a := range c.ErrorAncestors(n) {
			if in := a.Child(ch.Name); in != nil && (in.Shape == resolve.ShapeDeclared || in.Shape == resolve.ShapeValue) {
				if t := memberType(in); t != "" && t != own {
					c.errorf(ch.Decl.File, pathNode(ch.Decl.N), "retyped-member", "%s.%s is %s, but %s declares it %s; an error keeps the type of a member it inherits", n.Name, ch.Name, own, a.Name, t)
				}
				break
			}
		}
	}
}

// memberType is a typed member's type as written, with `?` for an
// optional one, or "".
func memberType(n *resolve.Node) string {
	switch n.Decl.N.Kind {
	case syntax.Declare:
		d := ast.Declare{N: n.Decl.N}
		if d.Type() == nil {
			return ""
		}
		t := strings.Join(strings.Fields(d.Type().Syntax().Text()), " ")
		if d.Question() != nil {
			t += "?"
		}
		return t
	case syntax.Value:
		v := ast.Value{N: n.Decl.N}
		if v.Type() == nil {
			return ""
		}
		t := strings.Join(strings.Fields(v.Type().Syntax().Text()), " ")
		if v.Question() != nil {
			t += "?"
		}
		return t
	}
	return ""
}

// UnionVariant is one variant of a union: written positionally, as the
// `Card` of `union(Card)`, with its tag the type's name in snake_case, or
// in the body as `name: Type`, with its tag the name.
type UnionVariant struct {
	Tag string // "" when a positional variant's tag can't be derived
	// Node is a body variant, or for a positional one a node of the union's
	// that stands for it, named by its tag, outside the union's children.
	Node *resolve.Node
	Expr ast.Expr // a positional variant's type, or nil
}

// UnionVariants are a union's variants: its positional ones, then its
// body's.
func (c *Checker) UnionVariants(u *resolve.Node) []UnionVariant {
	if l, ok := c.variants[u]; ok {
		return l
	}
	var out []UnionVariant
	one := func(x ast.Expr) {
		tag := derivedTag(x)
		name := tag
		if name == "" {
			name = "variant_" + strconv.Itoa(len(out)+1)
		}
		n := &resolve.Node{Name: name, Shape: resolve.ShapeDeclared, Parent: u,
			Decl: resolve.Site{File: u.Decl.File, N: x.Syntax(), Scope: u.Decl.Scope}}
		out = append(out, UnionVariant{Tag: tag, Expr: x, Node: n})
	}
	add := func(e ast.Expr) {
		if arr, ok := e.(ast.ArrayExpr); ok {
			for _, x := range arr.Exprs() {
				one(x)
			}
			return
		}
		one(e)
	}
	for _, a := range declArgs(u) {
		switch a := a.(type) {
		case ast.PositionalArg:
			add(a.Expr())
		case ast.NamedArg:
			if a.Ident() != nil && a.Ident().Text == "variants" {
				add(a.Expr())
			}
		}
	}
	for _, ch := range u.Children {
		if ch.Name != "" && ch.Decl.N != nil && ch.Decl.N.Kind == syntax.Declare {
			out = append(out, UnionVariant{Tag: ch.Name, Node: ch})
		}
	}
	c.variants[u] = out
	return out
}

// UnionTagged reports whether a union's values carry their variant's tag:
// unless it's `<untagged>`.
func (c *Checker) UnionTagged(u *resolve.Node) bool { return c.GroupFlag(u, "tagging") != "untagged" }

// derivedTag is the tag of a positional variant: the snake_case form of
// the type's plain name, or "" for a type with arguments or no name.
func derivedTag(e ast.Expr) string {
	p, ok := e.(ast.PathExpr)
	if !ok {
		return ""
	}
	segs := resolve.PathSegs(p.Path().N)
	if len(segs) == 0 {
		return ""
	}
	return Snake(segs[len(segs)-1])
}

// checkUnion reports, for a tagged union, a positional variant whose tag
// can't be derived and two variants of one tag, and for any union, one
// type written twice positionally.
func (c *Checker) checkUnion(u *resolve.Node) {
	if u.Decl.File == nil {
		return
	}
	tagged := c.UnionTagged(u)
	tags := map[string]bool{}
	types := map[string]bool{}
	for _, v := range c.UnionVariants(u) {
		at, file := u.Decl.N, u.Decl.File
		if v.Expr != nil {
			at = v.Expr.Syntax()
			text := strings.Join(strings.Fields(at.Text()), " ")
			if types[text] {
				c.errorf(file, at, "union", "%s is a variant of %s twice", text, u.Name)
				continue
			}
			types[text] = true
		} else {
			at, file = pathNode(v.Node.Decl.N), v.Node.Decl.File
		}
		if !tagged {
			continue
		}
		if v.Tag == "" {
			c.errorf(file, at, "union", "%s has no name to derive its tag from; give it one in the body, as name: %s", strings.TrimSpace(at.Text()), strings.TrimSpace(at.Text()))
			continue
		}
		if tags[v.Tag] {
			c.errorf(file, at, "union", "two variants of %s have the tag %s", u.Name, v.Tag)
			continue
		}
		tags[v.Tag] = true
	}
}

// Snake writes a name in snake case: ParcelStatus is parcel_status.
func Snake(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		if r >= 'A' && r <= 'Z' {
			if i > 0 && rs[i-1] != '_' && (rs[i-1] < 'A' || rs[i-1] > 'Z' || i+1 < len(rs) && rs[i+1] >= 'a' && rs[i+1] <= 'z') {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
