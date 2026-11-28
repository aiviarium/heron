// Package types checks the resolved tree against the schemas in scope:
// which kind or type each node has, the kind arguments and fields it may
// take, flags and annotations, typed strings and constructors, and that
// every name, link, tag and insert resolves.
package types

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// stdFile is the identity of a standard package's entry file.
func stdFile(pkg string) resolve.FileID { return resolve.FileID("std:" + pkg + "/package.heron") }

// IsStd reports whether n is the declaration at path in the standard
// package pkg, such as IsStd(n, "schema", "schema.kind").
func IsStd(n *resolve.Node, pkg, path string) bool {
	return n != nil && n.Decl.File != nil && n.Decl.File.ID == stdFile(pkg) && n.Path() == path
}

// meta is what a node's kind slot names, resolved once.
type meta struct {
	busy, done bool
	decl       *resolve.Node // the kind or type declaration, or nil
	t          ty            // the full type, with arguments, for typed nodes
}

// Meta returns the declaration a node's `:` slot, callee or heading names:
// a kind for an instance, a type for a typed node, or nil for open nodes
// and nodes whose slot doesn't resolve.
func (c *Checker) Meta(n *resolve.Node) *resolve.Node { return c.metaOf(n).decl }

func (c *Checker) metaOf(n *resolve.Node) *meta {
	m := c.metas[n]
	if m == nil {
		m = &meta{}
		c.metas[n] = m
	}
	if m.done || m.busy {
		return m
	}
	m.busy = true
	defer func() { m.busy, m.done = false, true }()
	switch n.Shape {
	case resolve.ShapeMounted:
		if n.Origin != nil {
			om := c.metaOf(n.Origin)
			m.decl, m.t = om.decl, om.t
		}
	case resolve.ShapeDeclared, resolve.ShapeValue:
		var tt ast.Type
		switch n.Decl.N.Kind {
		case syntax.Declare:
			tt = ast.Declare{N: n.Decl.N}.Type()
		case syntax.Value:
			tt = ast.Value{N: n.Decl.N}.Type()
		}
		if tt != nil {
			m.t = c.typeOf(tt, n.Decl.Scope, n.Decl.File)
			if nt, ok := tt.(ast.NamedType); ok && m.t.decl != nil && c.IsKind(m.t.decl) && n.Parent != nil && c.KindOf(n.Parent) != nil && !c.Accepts(n.Parent, m.t.decl) {
				// Of the kinds the name could mean, the one the enclosing
				// kind accepts wins, as for a call: std/docs's step in a
				// how-to, where another package's step is in scope too.
				pred := func(x *resolve.Node) bool { return c.IsKind(x) && c.Accepts(n.Parent, x) }
				if k := c.lookupPath(n.Decl.Scope, n.Decl.File, off(nt.N), resolve.PathSegs(nt.Path().N), pred); k != nil && pred(k) {
					m.t.decl = k
				}
			}
			if nt, ok := tt.(ast.NamedType); ok && m.t.decl != nil && c.IsKind(m.t.decl) {
				m.t.decl = c.pick(m.t.decl, nt.Args(), n, n.Decl.Scope, n.Decl.File)
			}
			m.decl = m.t.decl
		}
	case resolve.ShapeAnon:
		if p := calleePath(n); p != nil {
			segs := resolve.PathSegs(p)
			// Of the kinds the name could mean, the one the enclosing
			// kind accepts wins, so `if {{ … }}` in a rule is the rule's
			// condition even where std/doc's <if> is in scope too.
			pred := func(x *resolve.Node) bool { return c.IsKind(x) && c.Accepts(n.Parent, x) }
			m.decl = c.lookupPath(n.Decl.Scope, n.Decl.File, off(p), segs, pred)
			if m.decl != nil && len(m.decl.Overloads) > 0 && n.Decl.N.Kind == syntax.Invoke {
				m.decl = c.pick(m.decl, ast.Invoke{N: n.Decl.N}.Args(), n, n.Decl.Scope, n.Decl.File)
			}
			m.t = ty{decl: m.decl}
		}
	case resolve.ShapeHeading:
		m.decl = c.headingKind(n.Decl.Scope)
		m.t = ty{decl: m.decl}
	case resolve.ShapeElement:
		m.decl = c.elementKind(n)
		m.t = ty{decl: m.decl}
	case resolve.ShapeSet, resolve.ShapeImplicit:
		if n.Parent != nil {
			if f, ok := c.Field(n.Parent, n.Name); ok && f.Type.decl != nil {
				m.t = f.Type
				m.decl = f.Type.decl
			}
		}
	}
	return m
}

func off(n *syntax.Node) int {
	if n == nil {
		return -1
	}
	s, _ := n.Span()
	return s
}

// Macro returns the macro an invocation in structure calls, as
// `resource_ops(name = "category")` in an api body, or nil when the
// invocation calls a kind, a type or anything else.
func (c *Checker) Macro(n *resolve.Node) *resolve.Node {
	if n.Decl.N == nil || n.Decl.N.Kind != syntax.Invoke {
		return nil
	}
	p := calleePath(n)
	if p == nil {
		return nil
	}
	b := c.lookupBinding(n.Decl.Scope, n.Decl.File, off(p), resolve.PathSegs(p))
	if b.Node == nil || !IsStd(c.KindOf(b.Node), "core", "core.macro") {
		return nil
	}
	return b.Node
}

// calleePath returns the path an anonymous node's member calls.
func calleePath(n *resolve.Node) *syntax.Node {
	if n.Decl.N == nil {
		return nil
	}
	switch n.Decl.N.Kind {
	case syntax.Invoke, syntax.TextNode:
		for _, ch := range n.Decl.N.Children {
			if ch, ok := ch.(*syntax.Node); ok && ch.Kind == syntax.Path {
				return ch
			}
		}
	}
	return nil
}

// headingKind is the kind a heading in a prose file becomes: the kind in
// scope with heading = true.
func (c *Checker) headingKind(sc *resolve.Scope) *resolve.Node {
	var out *resolve.Node
	sc.Names(func(_ string, b resolve.Binding) {
		if out == nil && b.Node != nil && c.IsKind(b.Node) && c.kindArgBool(b.Node, "heading") {
			out = b.Node
		}
	})
	return out
}

// --- what a declaration is ---

func (c *Checker) metaIs(n *resolve.Node, pkg, path string) bool {
	return n != nil && IsStd(c.metaOf(n).decl, pkg, path)
}

// IsKind reports whether n declares a kind.
func (c *Checker) IsKind(n *resolve.Node) bool { return c.metaIs(n, "schema", "schema.kind") }

// IsFlag reports whether n declares a flag.
func (c *Checker) IsFlag(n *resolve.Node) bool { return c.metaIs(n, "schema", "schema.flag") }

// IsAnnotation reports whether n declares an annotation.
func (c *Checker) IsAnnotation(n *resolve.Node) bool {
	return c.metaIs(n, "schema", "schema.annotation")
}

// IsCategory reports whether n declares a category.
func (c *Checker) IsCategory(n *resolve.Node) bool { return c.metaIs(n, "schema", "schema.category") }

// IsPackage reports whether n is a package node.
func (c *Checker) IsPackage(n *resolve.Node) bool { return c.metaIs(n, "schema", "schema.package") }

// typeKinds are the std/types kinds whose instances are types.
var typeKinds = []string{"types.type", "types.enum", "types.union", "types.interface", "types.error"}

// IsType reports whether n is a type: a primitive, an instance of a std/types
// type kind, or an alias bound to a type.
func (c *Checker) IsType(n *resolve.Node) bool {
	if n == nil {
		return false
	}
	if resolve.IsBuiltin(n) {
		return true
	}
	d := c.metaOf(n).decl
	for _, k := range typeKinds {
		if IsStd(d, "types", k) {
			return true
		}
	}
	return c.aliasOf(n) != nil
}

// IsEnum reports whether n declares an enum.
func (c *Checker) IsEnum(n *resolve.Node) bool { return c.metaIs(n, "types", "types.enum") }

// IsError reports whether n declares an error.
func (c *Checker) IsError(n *resolve.Node) bool { return c.metaIs(n, "types", "types.error") }

// ErrorParent is the error an error extends, or nil for a root error.
func (c *Checker) ErrorParent(n *resolve.Node) *resolve.Node {
	if !c.IsError(n) {
		return nil
	}
	var e ast.Expr
	for i, a := range declArgs(n) {
		switch a := a.(type) {
		case ast.PositionalArg:
			if i == 0 {
				e = a.Expr()
			}
		case ast.NamedArg:
			if a.Ident() != nil && a.Ident().Text == "extends" {
				e = a.Expr()
			}
		}
	}
	p, ok := e.(ast.PathExpr)
	if !ok {
		return nil
	}
	t := c.lookupPath(n.Decl.Scope, n.Decl.File, off(p.N), resolve.PathSegs(p.Path().N), c.IsError)
	if t == nil || !c.IsError(t) {
		return nil
	}
	return t
}

// ErrorAncestors are the errors an error extends, its parent first, up to
// a root or a cycle.
func (c *Checker) ErrorAncestors(n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	seen := map[*resolve.Node]bool{n: true}
	for p := c.ErrorParent(n); p != nil && !seen[p]; p = c.ErrorParent(p) {
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// ErrorCovers reports whether an error named in `@throws` covers err:
// err is it, or extends it.
func (c *Checker) ErrorCovers(named, err *resolve.Node) bool {
	if named == err {
		return true
	}
	for _, a := range c.ErrorAncestors(err) {
		if a == named {
			return true
		}
	}
	return false
}

// ErrorSeverity is how serious an error is as a diagnostic: its severity
// flag, or the one it inherits, or "error".
func (c *Checker) ErrorSeverity(n *resolve.Node) string {
	for _, x := range append([]*resolve.Node{n}, c.ErrorAncestors(n)...) {
		if f := c.GroupFlag(x, "severity"); f != "" {
			return f
		}
	}
	return "error"
}

// IsInterface reports whether n declares an interface.
func (c *Checker) IsInterface(n *resolve.Node) bool { return c.metaIs(n, "types", "types.interface") }

// aliasOf returns the type an alias such as `u8 = Int(bits = 8)` names.
func (c *Checker) aliasOf(n *resolve.Node) *resolve.Node {
	if n.Shape != resolve.ShapeSet || n.Value == nil || c.aliasBusy[n] {
		return nil
	}
	c.aliasBusy[n] = true
	defer delete(c.aliasBusy, n)
	t := c.typeOfExpr(n.Value, n.ValueSite.Scope, n.ValueSite.File, false)
	if t.decl != nil && c.IsType(t.decl) {
		return t.decl
	}
	return nil
}

// KindOf returns the kind a node is an instance of, or nil.
func (c *Checker) KindOf(n *resolve.Node) *resolve.Node {
	d := c.metaOf(n).decl
	if d != nil && c.IsKind(d) {
		return d
	}
	return nil
}

// TypeOf returns the type of a typed node, or nil.
func (c *Checker) TypeOf(n *resolve.Node) *resolve.Node {
	d := c.metaOf(n).decl
	if d != nil && c.IsType(d) {
		return d
	}
	return nil
}

// --- kind declarations ---

// declArgs returns the arguments a declaration's kind slot passes, as in
// the `(in = [block])` of `section: kind(in = [block])`.
func declArgs(n *resolve.Node) []ast.Arg {
	if n == nil || n.Decl.N == nil {
		return nil
	}
	var tt ast.Type
	switch n.Decl.N.Kind {
	case syntax.Declare:
		tt = ast.Declare{N: n.Decl.N}.Type()
	case syntax.Value:
		tt = ast.Value{N: n.Decl.N}.Type()
	case syntax.Invoke:
		return ast.Invoke{N: n.Decl.N}.Args().Args()
	case syntax.Tag:
		return n.ElementArgs.Args()
	}
	if nt, ok := tt.(ast.NamedType); ok {
		return nt.Args().Args()
	}
	return nil
}

// declFlags returns the flags on a declaration's kind slot.
func declFlags(n *resolve.Node) []*syntax.Token {
	if n == nil || n.Decl.N == nil {
		return nil
	}
	var tt ast.Type
	switch n.Decl.N.Kind {
	case syntax.Declare:
		tt = ast.Declare{N: n.Decl.N}.Type()
	case syntax.Value:
		tt = ast.Value{N: n.Decl.N}.Type()
	case syntax.Invoke:
		return ast.Invoke{N: n.Decl.N}.Flags().Idents()
	}
	if nt, ok := tt.(ast.NamedType); ok {
		return nt.Flags().Idents()
	}
	return nil
}

// namedArg returns the expression of a named argument of a declaration.
func namedArg(n *resolve.Node, name string) ast.Expr {
	for _, a := range declArgs(n) {
		if na, ok := a.(ast.NamedArg); ok && na.Ident() != nil && na.Ident().Text == name {
			return na.Expr()
		}
	}
	return nil
}

func (c *Checker) kindArgBool(k *resolve.Node, name string) bool {
	e := namedArg(k, name)
	if e == nil {
		return false
	}
	l, ok := e.(ast.Literal)
	return ok && l.KwTrue() != nil
}

// refList resolves an argument such as `on = [case, types.op]` to nodes.
func (c *Checker) refList(decl *resolve.Node, name string) []*resolve.Node {
	e := namedArg(decl, name)
	if e == nil {
		return nil
	}
	var exprs []ast.Expr
	if arr, ok := e.(ast.ArrayExpr); ok {
		exprs = arr.Exprs()
	} else {
		exprs = []ast.Expr{e}
	}
	// Each argument wants what its position names, whatever the import order.
	var want func(*resolve.Node) bool
	switch name {
	case "in":
		want = c.IsCategory
	case "on":
		want = c.isKindOrCategory
	case "args_from", "fields_from", "extends":
		want = c.IsKind
	case "references":
		want = c.isKindOrCategory
	}
	var out []*resolve.Node
	for _, x := range exprs {
		if p, ok := x.(ast.PathExpr); ok {
			if n := c.lookupPath(decl.Decl.Scope, decl.Decl.File, off(p.N), resolve.PathSegs(p.Path().N), want); n != nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func (c *Checker) refArg(decl *resolve.Node, name string) *resolve.Node {
	l := c.refList(decl, name)
	if len(l) == 1 {
		return l[0]
	}
	return nil
}

// Categories returns the categories a kind joins with `in`.
func (c *Checker) Categories(k *resolve.Node) []*resolve.Node { return c.refList(k, "in") }

// Fits reports whether a kind is one of, or in a category of, the targets
// an `on` or `accept` list names.
func (c *Checker) Fits(k *resolve.Node, targets []*resolve.Node) bool {
	for _, t := range targets {
		if t == k {
			return true
		}
		for _, o := range t.Overloads {
			// Another signature of an overloaded kind is the same kind.
			if o == k {
				return true
			}
		}
		for _, cat := range c.Categories(k) {
			if cat == t {
				return true
			}
		}
	}
	return false
}

// --- parameters and fields ---

// Param is one parameter of a kind, type, func, op or annotation.
type Param struct {
	Name     string
	Type     ty
	Optional bool // `name?:` or a default
	Variadic bool
	N        *syntax.Node
	std      string // the standard package declaring it, or ""
}

// Params returns the parameters a declaration takes in its own `( … )`.
func (c *Checker) Params(n *resolve.Node) []Param {
	if n == nil || n.Decl.N == nil {
		return nil
	}
	var ps ast.Params
	switch n.Decl.N.Kind {
	case syntax.Declare:
		ps = ast.Declare{N: n.Decl.N}.Params()
	case syntax.Value:
		ps = ast.Value{N: n.Decl.N}.Params()
	default:
		return nil
	}
	sc := n.Body
	if sc == nil {
		sc = n.Decl.Scope
	}
	var out []Param
	for _, p := range ps.Params() {
		id := p.Ident()
		if id == nil {
			continue
		}
		pp := Param{Name: id.Text, N: p.N, Optional: p.Question() != nil || p.Default() != nil}
		if n.Decl.File != nil && n.Decl.File.ID.IsStd() {
			pp.std = strings.TrimSuffix(strings.TrimPrefix(string(n.Decl.File.ID), "std:"), "/package.heron")
		}
		for _, f := range p.Flags().Idents() {
			if f.Text == "variadic" {
				pp.Variadic = true
			}
		}
		if t := p.Type(); t != nil {
			pp.Type = c.typeOf(t, sc, n.Decl.File)
		}
		out = append(out, pp)
	}
	return out
}

// KindParams returns the arguments an instance of kind k takes; for a kind
// with `args_from`, those are the parameters of the enclosing node of that
// kind, as a case takes its scenario's.
func (c *Checker) KindParams(k *resolve.Node, instance *resolve.Node) []Param {
	if from := c.refArg(k, "args_from"); from != nil {
		for a := instance.Parent; a != nil; a = a.Parent {
			if c.KindOf(a) == from {
				return c.Params(a)
			}
		}
		return nil
	}
	ps := c.Params(k)
	if len(ps) == 0 && k != nil && k.Decl.N != nil && k.Decl.N.Kind == syntax.Declare && (ast.Declare{N: k.Decl.N}).Params().N == nil {
		// A kind that declares no parameters takes those of the kind it
		// extends.
		for x, i := c.Extends(k), 0; x != nil && i < 8; x, i = c.Extends(x), i+1 {
			if xs := c.Params(x); len(xs) > 0 {
				return xs
			}
		}
	}
	return ps
}

// Field is a field a node may set or enter.
type Field struct {
	Name string
	Type ty
	Flag bool // a flag of the node's kind, set like a boolean
	Decl *resolve.Node
	Open bool // the node is open: any name is a field
}

// Open reports whether a node accepts members it doesn't declare: the file
// root, a package node, a scope nobody typed, maps, untyped objects and
// `type<open>`.
func (c *Checker) Open(n *resolve.Node) bool {
	switch n.Shape {
	case resolve.ShapeRoot:
		return true
	case resolve.ShapeScoped:
		return c.metaOf(n).decl == nil
	case resolve.ShapeMounted:
		if n.Origin != nil {
			return c.Open(n.Origin)
		}
	}
	m := c.metaOf(n)
	if m.decl == nil {
		// Nothing types it: an untyped set or a node whose slot didn't
		// resolve (reported where it's written).
		return true
	}
	if c.IsPackage(n) {
		return true
	}
	if t := m.decl; c.IsType(t) {
		return c.typeOpen(t)
	}
	return false
}

func (c *Checker) typeOpen(t *resolve.Node) bool {
	if a := c.aliasOf(t); a != nil {
		t = a
	}
	if resolve.IsBuiltin(t) {
		switch t.Name {
		case "Any", "Object", "Map":
			return true
		}
		return false
	}
	for _, f := range declFlags(t) {
		if f.Text == "open" {
			return true
		}
	}
	return false
}

// Field looks a name up among the fields a node may set: its kind's
// parameters, fields and flags, or its type's members.
func (c *Checker) Field(n *resolve.Node, name string) (Field, bool) {
	if c.fieldBusy[n] {
		return Field{Open: true}, true
	}
	c.fieldBusy[n] = true
	defer delete(c.fieldBusy, n)
	m := c.metaOf(n)
	if k := m.decl; k != nil && c.IsKind(k) {
		for _, p := range c.KindParams(k, n) {
			if p.Name == name {
				return Field{Name: name, Type: p.Type}, true
			}
		}
		if f, ok := c.memberField(k, name); ok {
			return f, true
		}
		// A kind takes the member fields of the kind it extends.
		for x, i := c.Extends(k), 0; x != nil && i < 8; x, i = c.Extends(x), i+1 {
			if f, ok := c.memberField(x, name); ok {
				return f, true
			}
		}
		if from := c.refArg(k, "fields_from"); from != nil {
			for a := n.Parent; a != nil; a = a.Parent {
				if c.KindOf(a) == from {
					for _, p := range c.Params(a) {
						if p.Name == name {
							return Field{Name: name, Type: p.Type}, true
						}
					}
					break
				}
			}
			for _, b := range c.bindings(k) {
				if b == name {
					return Field{Name: name}, true
				}
			}
		}
		if fl := c.kindFlag(n, k, name); fl != nil {
			return Field{Name: name, Flag: true, Decl: fl}, true
		}
		if c.IsPackage(n) {
			return Field{Name: name, Open: true}, true
		}
		return Field{}, false
	}
	if t := m.decl; t != nil && c.IsType(t) {
		if f, ok := c.typeMember(t, name, map[*resolve.Node]bool{}); ok {
			return f, true
		}
		if c.typeOpen(t) {
			return Field{Name: name, Open: true}, true
		}
		return Field{}, false
	}
	if c.Open(n) {
		return Field{Name: name, Open: true}, true
	}
	return Field{}, false
}

// Fields lists the named fields a node may set or enter, as Field finds
// them: its kind's parameters and member fields, or its type's members and
// those of the interfaces it implements. Flags aren't listed. An open node
// lists only what it declares. It's the completion candidates of a body.
func (c *Checker) Fields(n *resolve.Node) []Field {
	var names []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	m := c.metaOf(n)
	if k := m.decl; k != nil && c.IsKind(k) {
		for _, p := range c.KindParams(k, n) {
			add(p.Name)
		}
		for x, i := k, 0; x != nil && i < 8; x, i = c.Extends(x), i+1 {
			for _, ch := range x.AllChildren() {
				add(ch.Name)
			}
		}
	} else if t := m.decl; t != nil && c.IsType(t) {
		var walk func(t *resolve.Node, depth int)
		walk = func(t *resolve.Node, depth int) {
			if a := c.aliasOf(t); a != nil {
				t = a
			}
			if depth > 8 || resolve.IsBuiltin(t) {
				return
			}
			for _, ch := range t.AllChildren() {
				add(ch.Name)
			}
			for _, i := range c.Implements(t) {
				walk(i, depth+1)
			}
		}
		walk(t, 0)
	}
	var out []Field
	for _, name := range names {
		if f, ok := c.Field(n, name); ok && !f.Open {
			out = append(out, f)
		}
	}
	return out
}

// TypeName renders a field's type as Heron writes it, or "" when unknown.
func (f Field) TypeName() string { return f.Type.name() }

// TypeName renders a parameter's type as Heron writes it, or "".
func (p Param) TypeName() string { return p.Type.name() }

func (t ty) name() string {
	if t.decl == nil {
		return t.param
	}
	s := t.decl.Name
	if t.optional {
		s += "?"
	}
	return s
}

// memberField finds a typed member declared in a kind's or type's body.
func (c *Checker) memberField(d *resolve.Node, name string) (Field, bool) {
	ch := d.Child(name)
	if ch == nil || (ch.Shape != resolve.ShapeDeclared && ch.Shape != resolve.ShapeValue) {
		return Field{}, false
	}
	m := c.metaOf(ch)
	if m.decl != nil && c.IsKind(m.decl) && !c.isMemberKind(m.decl) {
		return Field{}, false
	}
	return Field{Name: name, Type: m.t, Decl: ch}, true
}

// isMemberKind reports whether a kind is a typed member kind, such as
// types.member, whose instances are fields.
func (c *Checker) isMemberKind(k *resolve.Node) bool { return c.kindArgBool(k, "typed") }

// typeMember finds a member of a type or of an interface it implements.
func (c *Checker) typeMember(t *resolve.Node, name string, seen map[*resolve.Node]bool) (Field, bool) {
	if a := c.aliasOf(t); a != nil {
		t = a
	}
	if seen[t] || resolve.IsBuiltin(t) {
		return Field{}, false
	}
	seen[t] = true
	if f, ok := c.memberField(t, name); ok {
		return f, true
	}
	for _, i := range c.Implements(t) {
		if f, ok := c.typeMember(i, name, seen); ok {
			return f, true
		}
	}
	return Field{}, false
}

// Implements returns the interfaces a type, enum or interface names as its
// positional arguments or in `implements = […]`, or the error an error
// extends.
func (c *Checker) Implements(t *resolve.Node) []*resolve.Node {
	if c.metaIs(t, "types", "types.union") {
		return nil // a union's arguments are its variants
	}
	if c.IsError(t) {
		// An error's parent is the one type it extends.
		if p := c.ErrorParent(t); p != nil {
			return []*resolve.Node{p}
		}
		return nil
	}
	var out []*resolve.Node
	for _, a := range declArgs(t) {
		var e ast.Expr
		switch a := a.(type) {
		case ast.PositionalArg:
			e = a.Expr()
		case ast.NamedArg:
			if a.Ident() != nil && a.Ident().Text == "implements" {
				e = a.Expr()
			}
		}
		var exprs []ast.Expr
		if arr, ok := e.(ast.ArrayExpr); ok {
			exprs = arr.Exprs()
		} else if e != nil {
			exprs = []ast.Expr{e}
		}
		for _, x := range exprs {
			if p, ok := x.(ast.PathExpr); ok {
				if n := c.lookupPath(t.Decl.Scope, t.Decl.File, off(p.N), resolve.PathSegs(p.Path().N), c.IsType); n != nil {
					out = append(out, n)
				}
			}
		}
	}
	return out
}

// bindings returns the names a kind's `accept(binding = …)` entries admit.
func (c *Checker) bindings(k *resolve.Node) []string {
	var out []string
	for _, ch := range k.Children {
		if ch.Shape != resolve.ShapeAnon || calleePath(ch) == nil || resolve.PathText(calleePath(ch)) != "accept" {
			continue
		}
		for _, a := range (ast.Invoke{N: ch.Decl.N}).Args().Args() {
			if na, ok := a.(ast.NamedArg); ok && na.Ident() != nil && na.Ident().Text == "binding" {
				out = append(out, strings.TrimSpace(na.Expr().Syntax().Text()))
			}
		}
	}
	return out
}

// kindFlag finds a flag in scope that attaches to kind k, so a node may set
// it like a boolean.
func (c *Checker) kindFlag(n, k *resolve.Node, name string) *resolve.Node {
	sc := n.Body
	if sc == nil {
		sc = n.Decl.Scope
	}
	if sc == nil {
		return nil
	}
	out, _ := c.ResolveFlag(name, k, sc, nil, -1)
	return out
}

// Variant returns the named variant of an enum or union, or nil.
func (c *Checker) Variant(e *resolve.Node, name string) *resolve.Node {
	if v := e.Child(name); v != nil {
		return v
	}
	for _, ch := range e.Children {
		if strings.EqualFold(ch.Name, name) {
			return ch
		}
	}
	return nil
}

// Accepts reports whether a node's kind takes children of kind k, through
// `accept(kind = …)` or `accept(category = …)`.
func (c *Checker) Accepts(parent, k *resolve.Node) bool {
	if parent == nil {
		return false
	}
	pk := c.KindOf(parent)
	if pk == nil {
		return false
	}
	for _, a := range c.acceptList(pk) {
		if a.kind == k || (a.category != nil && c.Fits(k, []*resolve.Node{a.category})) {
			return true
		}
	}
	return false
}

// VerbatimText reports whether a node's kind takes its text as Verbatim,
// as `accept(text = Verbatim)` says: text kept as written, such as a
// grammar's productions, rather than prose.
func (c *Checker) VerbatimText(n *resolve.Node) bool {
	k := c.KindOf(n)
	if k == nil {
		return false
	}
	for _, a := range c.acceptList(k) {
		if a.text == "Verbatim" {
			return true
		}
	}
	return false
}

type acceptEntry struct {
	kind, category *resolve.Node
	mode           string // "prose", "code" or "structure"
	text           string // the type of the text, as accept(text = Verbatim) names it
	max            int    // 0 for no bound
	site           *resolve.Node
}

func (c *Checker) acceptList(k *resolve.Node) []acceptEntry {
	if l, ok := c.accepts[k]; ok {
		return l
	}
	c.accepts[k] = nil
	var out []acceptEntry
	for _, ch := range k.Children {
		p := calleePath(ch)
		if ch.Shape != resolve.ShapeAnon || p == nil || resolve.PathText(p) != "accept" {
			continue
		}
		e := acceptEntry{site: ch}
		for _, a := range (ast.Invoke{N: ch.Decl.N}).Args().Args() {
			var name string
			var x ast.Expr
			switch a := a.(type) {
			case ast.PositionalArg:
				name, x = "mode", a.Expr()
			case ast.NamedArg:
				if a.Ident() == nil {
					continue
				}
				name, x = a.Ident().Text, a.Expr()
			}
			text := strings.TrimSpace(x.Syntax().Text())
			switch name {
			case "mode":
				e.mode = text
			case "text":
				e.mode = "prose"
				e.text = text
			case "max":
				fmt.Sscan(text, &e.max)
			case "kind", "category":
				pe, ok := x.(ast.PathExpr)
				if !ok {
					continue
				}
				want := c.IsKind
				if name == "category" {
					want = c.IsCategory
				}
				n := c.lookupPath(ch.Decl.Scope, ch.Decl.File, off(pe.N), resolve.PathSegs(pe.Path().N), want)
				if name == "kind" {
					e.kind = n
				} else {
					e.category = n
				}
			}
		}
		out = append(out, e)
	}
	if len(out) == 0 && c.IsKind(k) {
		// A kind with no body of its own takes the body of the kind it
		// extends.
		if x := c.Extends(k); x != nil && x != k {
			out = c.acceptList(x)
		}
	}
	c.accepts[k] = out
	return out
}

// On returns the kinds and categories a flag or annotation attaches to;
// none means anywhere.
func (c *Checker) On(decl *resolve.Node) []*resolve.Node { return c.refList(decl, "on") }

// ResolveFlag resolves a flag written on a node of kind k, an annotation's
// declaration for a flag on an annotation, or nil where no kind applies. A
// qualified name, as `types.warning`, names the flag by its path. A plain
// name resolves among the flags in scope whose `on` lists k or a category
// of k, so flags of one name on different kinds never clash, and else among
// the flags in scope without `on`, as `variadic`. It returns the flag, nil
// when none fits, and the qualified forms of the flags it's ambiguous
// with: flags of one name that two imported packages attach to one kind.
func (c *Checker) ResolveFlag(name string, k *resolve.Node, sc *resolve.Scope, file *resolve.File, at int) (*resolve.Node, []string) {
	if sc == nil || name == "" {
		return nil, nil
	}
	fits := func(n *resolve.Node) bool {
		if !c.IsFlag(n) {
			return false
		}
		on := c.On(n)
		return len(on) > 0 && c.Fits(k, on)
	}
	if segs := strings.Split(name, "."); len(segs) > 1 {
		n := c.lookupPath(sc, file, at, segs, c.IsFlag)
		if n == nil || !c.IsFlag(n) {
			return nil, nil
		}
		if on := c.On(n); k != nil && len(on) > 0 && !c.Fits(k, on) {
			return nil, nil
		}
		return n, nil
	}
	if k != nil {
		if n, forms := c.flagAmong(sc, file, at, name, fits); n != nil {
			return n, forms
		}
	}
	scoped := func(n *resolve.Node) bool { return c.IsFlag(n) && (k == nil || len(c.On(n)) == 0) }
	if n := c.lookupPath(sc, file, at, []string{name}, scoped); n != nil && scoped(n) {
		return n, nil
	}
	return nil, nil
}

// flagAmong finds the flag of a name that fits, innermost scope first: a
// flag the tree declares shadows imported ones, and two imported packages
// declaring one at the same scope make it ambiguous.
func (c *Checker) flagAmong(sc *resolve.Scope, file *resolve.File, at int, name string, fits func(*resolve.Node) bool) (*resolve.Node, []string) {
	seen := map[*resolve.Scope]bool{}
	var found *resolve.Node
	var forms []string
	var walk func(sc *resolve.Scope) bool
	walk = func(sc *resolve.Scope) bool {
		for ; sc != nil; sc = sc.Parent {
			if seen[sc] {
				return false
			}
			seen[sc] = true
			if sc.Node != nil {
				if n := sc.Node.Child(name); n != nil && fits(n) {
					found = n
					return true
				}
			}
			pkgs := map[*resolve.Node]bool{}
			for _, p := range sc.Imports {
				p.Each(name, func(n *resolve.Node) bool {
					if fits(n) && n.Parent != nil && !pkgs[n.Parent] {
						pkgs[n.Parent] = true
						if found == nil {
							found = n
						}
						forms = append(forms, "<"+n.Parent.Name+"."+name+">")
					}
					return true
				})
			}
			if found != nil {
				return true
			}
			for _, a := range sc.Also {
				if walk(a) {
					return true
				}
			}
		}
		return false
	}
	walk(sc)
	if len(forms) < 2 {
		forms = nil
	}
	sort.Strings(forms)
	return found, forms
}

// FlagsNamed returns the flags of a name in scope, whatever they attach
// to, for the suggestion an unknown flag is reported with.
func (c *Checker) FlagsNamed(sc *resolve.Scope, name string) []*resolve.Node {
	var out []*resolve.Node
	seen := map[*resolve.Node]bool{}
	if sc == nil {
		return nil
	}
	sc.Each(name, nil, -1, func(b resolve.Binding) bool {
		if b.Node != nil && c.IsFlag(b.Node) && !seen[b.Node] {
			seen[b.Node] = true
			out = append(out, b.Node)
		}
		return true
	})
	return out
}

// EffectiveKind is the kind a node counts as for what attaches to it: the
// kind it's an instance of, or for a typed member, the member kind its
// parent's kind infers, such as types.member in a type's body.
func (c *Checker) EffectiveKind(n *resolve.Node) *resolve.Node {
	if k := c.KindOf(n); k != nil {
		return k
	}
	if c.TypeOf(n) == nil || n.Parent == nil {
		return nil
	}
	if pk := c.KindOf(n.Parent); pk != nil {
		for _, a := range c.acceptList(pk) {
			if a.kind != nil && c.isMemberKind(a.kind) {
				return a.kind
			}
		}
	}
	return nil
}

// StructureOnly reports whether a kind says `modes = [structure]`, so it
// can't stand as a tag.
func (c *Checker) StructureOnly(k *resolve.Node) bool {
	e := namedArg(k, "modes")
	arr, ok := e.(ast.ArrayExpr)
	if !ok {
		return false
	}
	for _, x := range arr.Exprs() {
		if strings.TrimSpace(x.Syntax().Text()) != "structure" {
			return false
		}
	}
	return len(arr.Exprs()) > 0
}

// HasKindFlag reports whether a declaration's kind slot carries a flag, as
// `annotation<repeatable>` does, plain or qualified.
func HasKindFlag(n *resolve.Node, flag string) bool {
	for _, f := range declFlags(n) {
		if f.Text == flag || strings.HasSuffix(f.Text, "."+flag) {
			return true
		}
	}
	return false
}

// GroupFlag is the name of the flag of a group, as "severity", that a node
// carries on its kind slot or its name, or "" when it carries none: the
// value a flag group stands for, as `usecase<user>` sets the level.
func (c *Checker) GroupFlag(n *resolve.Node, group string) string {
	if n == nil || n.Decl.N == nil {
		return ""
	}
	toks := declFlags(n)
	if fl := childOf(n.Decl.N, syntax.Flags); fl != nil {
		toks = append(toks, (ast.Flags{N: fl}).Idents()...)
	}
	k := c.EffectiveKind(n)
	for _, t := range toks {
		f := c.FlagRefs[t]
		if f == nil {
			f, _ = c.ResolveFlag(t.Text, k, n.Decl.Scope, n.Decl.File, t.Start)
		}
		if f == nil {
			continue
		}
		if g, ok := resolve.StringValue(namedArgSyntax(f, "group")); ok && g == group {
			return f.Name
		}
	}
	return ""
}

func namedArgSyntax(n *resolve.Node, name string) *syntax.Node {
	if e := namedArg(n, name); e != nil {
		return e.Syntax()
	}
	return nil
}

// acceptsMode reports whether a kind's body takes prose or code.
func (c *Checker) acceptsMode(k *resolve.Node, mode string) bool {
	for _, a := range c.acceptList(k) {
		if a.mode == mode {
			return true
		}
	}
	return false
}

// DeclArgs are the arguments a declaration's kind slot passes.
func DeclArgs(n *resolve.Node) []ast.Arg { return declArgs(n) }

// DeclNamedArg is the expression of a named argument of a declaration's
// kind slot, or nil.
func DeclNamedArg(n *resolve.Node, name string) ast.Expr { return namedArg(n, name) }

// Extends is the kind a kind extends with `extends`, or nil.
func (c *Checker) Extends(k *resolve.Node) *resolve.Node {
	if k == nil {
		return nil
	}
	return c.refArg(k, "extends")
}

// ReferenceKinds are the kinds a kind's references may name, from its
// `references`, or else from the kind it extends; ok is false when neither
// declares any, and the kind's arguments are no references.
func (c *Checker) ReferenceKinds(k *resolve.Node) (kinds []*resolve.Node, ok bool) {
	for i := 0; k != nil && i < 8; i++ {
		if namedArg(k, "references") != nil {
			return c.refList(k, "references"), true
		}
		k = c.Extends(k)
	}
	return nil, false
}

// IsKindOrExtends reports whether kind k is want or extends it.
func (c *Checker) IsKindOrExtends(k, want *resolve.Node) bool {
	for i := 0; k != nil && i < 8; i++ {
		if k == want {
			return true
		}
		k = c.Extends(k)
	}
	return false
}
