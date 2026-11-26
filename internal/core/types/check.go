package types

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// Checker type-checks one resolved tree. Its queries, such as Meta, Field
// and KindOf, are what lints and the language server read.
type Checker struct {
	Tree  *resolve.Tree
	diags diag.List
	metas map[*resolve.Node]*meta

	aliasBusy map[*resolve.Node]bool
	fieldBusy map[*resolve.Node]bool
	accepts   map[*resolve.Node][]acceptEntry
	// strict is set while overloads are tried: a path that names a node
	// doesn't pass for a scalar there.
	strict bool

	// LocalUses counts the references to each local and parameter.
	LocalUses map[*resolve.Local]int
	// Refs maps each path that resolved to the node it names, for
	// go-to-definition and the lints that follow references.
	Refs map[*syntax.Node]*resolve.Node
	// linkIdx indexes the nodes a link reaches by suffix, once built.
	linkIdx *linkIndex
}

// New returns a checker for a tree.
func New(t *resolve.Tree) *Checker {
	return &Checker{
		Tree:      t,
		metas:     map[*resolve.Node]*meta{},
		aliasBusy: map[*resolve.Node]bool{},
		fieldBusy: map[*resolve.Node]bool{},
		accepts:   map[*resolve.Node][]acceptEntry{},
		LocalUses: map[*resolve.Local]int{},
		Refs:      map[*syntax.Node]*resolve.Node{},
	}
}

// Check checks every node of the tree and returns the diagnostics.
func Check(t *resolve.Tree) (*Checker, diag.List) {
	c := New(t)
	var out diag.List
	for _, n := range t.Root.Children {
		out = append(out, c.CheckNode(n)...)
	}
	out = append(out, c.CheckNode(t.Root)...)
	return c, out
}

// CheckNode checks a node and its descendants; the root alone is checked
// without its children. Results depend only on the node's sites and the
// tree's declarations, so callers may memoize them per node.
func (c *Checker) CheckNode(n *resolve.Node) diag.List {
	saved := c.diags
	c.diags = nil
	if n.Shape == resolve.ShapeRoot {
		c.own(n)
	} else {
		c.walk(n)
	}
	out := c.diags
	c.diags = saved
	return out
}

func (c *Checker) walk(n *resolve.Node) {
	c.own(n)
	for _, ch := range n.Children {
		c.walk(ch)
	}
}

func (c *Checker) errorf(file *resolve.File, at *syntax.Node, code, format string, args ...any) {
	if file == nil {
		return
	}
	s, e := spanOf(at)
	c.diags.Errorf(string(file.ID), s, e, code, format, args...)
}

// own checks what a node's own sites say: its kind slot, arguments, flags,
// annotations, value, prose and code.
func (c *Checker) own(n *resolve.Node) {
	if n.Shape == resolve.ShapeMounted && n.Decl.N != nil && n.Origin != nil && n.Decl.N == n.Origin.Decl.N {
		// Checked in its package; only amends made here are checked here.
		c.sites(n, false)
		return
	}
	switch n.Shape {
	case resolve.ShapeDeclared, resolve.ShapeValue:
		c.declared(n)
	case resolve.ShapeAnon:
		c.anon(n)
	case resolve.ShapeSet:
		c.setValue(n)
	case resolve.ShapeImplicit:
		if n.Entered && n.Parent != nil && !c.Open(n.Parent) {
			if _, ok := c.Field(n.Parent, n.Name); !ok {
				c.errorf(n.Decl.File, pathNode(n.Decl.N), "unresolved",
					"%s doesn't exist, and entering never creates a node%s", qualified(n), c.closestField(n.Parent, n.Name))
			}
		}
	case resolve.ShapeElement:
		c.element(n)
	case resolve.ShapeHeading:
		if c.metaOf(n).decl == nil {
			c.errorf(n.Decl.File, n.Decl.N, "unresolved", "a heading needs a kind with heading = true in scope, such as section from std/doc")
		}
	}
	c.bodyContent(n)
	c.insteadStrength(n)
	c.sites(n, true)
}

// insteadStrength reports std/spec's `instead` outside a negative strength:
// the Specifications tab says it stands only under `mustNot` or
// `shouldNot`, as the outcome a decider expects.
func (c *Checker) insteadStrength(n *resolve.Node) {
	var instead, positive *resolve.Node
	for _, ch := range n.Children {
		k := c.KindOf(ch)
		switch {
		case k == nil:
		case IsStd(k, "spec", "spec.instead"):
			if instead == nil {
				instead = ch
			}
		case IsStd(k, "spec", "spec.mustNot"), IsStd(k, "spec", "spec.shouldNot"):
			return
		case IsStd(k, "spec", "spec.must"), IsStd(k, "spec", "spec.should"), IsStd(k, "spec", "spec.may"):
			positive = k
		}
	}
	if instead == nil || instead.Decl.File == nil {
		return
	}
	if positive != nil {
		c.errorf(instead.Decl.File, pathNode(instead.Decl.N), "instead", "instead stands only under a negative strength, and %s is positive", positive.Name)
		return
	}
	c.errorf(instead.Decl.File, pathNode(instead.Decl.N), "instead", "instead stands only under a negative strength, mustNot or shouldNot")
}

// bodyContent checks a body against what its kind accepts: prose, code,
// and child kinds directly or through categories, each up to its max.
func (c *Checker) bodyContent(n *resolve.Node) {
	k := c.KindOf(n)
	if k == nil || c.IsPackage(n) || n.Decl.File == nil {
		return
	}
	at := n.Decl
	prose := len(n.Prose) > 0
	if n.Shape == resolve.ShapeValue {
		switch n.Value.(type) {
		case ast.ProseLines, ast.ProseBlock:
			prose = true
		default:
		}
	}
	if prose && !c.acceptsMode(k, "prose") {
		c.errorf(at.File, pathNode(at.N), "accept", "%s takes no prose", k.Name)
	}
	if len(n.Code) > 0 && !c.acceptsMode(k, "code") {
		c.errorf(at.File, pathNode(at.N), "accept", "%s takes no do block", k.Name)
	}
	counts := map[*acceptEntry]int{}
	list := c.acceptList(k)
	for _, ch := range n.Children {
		if ch.Shape != resolve.ShapeAnon && ch.Shape != resolve.ShapeDeclared && ch.Shape != resolve.ShapeHeading && ch.Shape != resolve.ShapeElement {
			continue
		}
		x := c.KindOf(ch)
		if x == nil || c.isMemberKind(x) {
			continue
		}
		if ch.Name != "" {
			if f, ok := c.Field(n, ch.Name); ok && !f.Open {
				continue
			}
		}
		var hit *acceptEntry
		for i := range list {
			a := &list[i]
			if a.kind == x || (a.category != nil && c.Fits(x, []*resolve.Node{a.category})) {
				hit = a
				break
			}
		}
		if hit == nil {
			c.errorf(ch.Decl.File, pathNode(ch.Decl.N), "accept", "%s doesn't accept %s%s", k.Name, x.Name, c.acceptHint(list))
			continue
		}
		counts[hit]++
		if hit.max > 0 && counts[hit] == hit.max+1 {
			what := x.Name
			if hit.category != nil {
				// A bound on a category counts all its kinds together.
				what = hit.category.Name + " node"
				if hit.max != 1 {
					what += "s"
				}
			}
			c.errorf(ch.Decl.File, pathNode(ch.Decl.N), "accept", "%s accepts at most %d %s", k.Name, hit.max, what)
		}
	}
}

func (c *Checker) acceptHint(list []acceptEntry) string {
	var names []string
	for _, a := range list {
		switch {
		case a.kind != nil:
			names = append(names, a.kind.Name)
		case a.category != nil:
			names = append(names, a.category.Name)
		}
	}
	if len(names) == 0 {
		return "; it accepts no child kinds"
	}
	return "; it accepts " + strings.Join(names, ", ")
}

func qualified(n *resolve.Node) string {
	if p := n.Path(); p != "" {
		return p
	}
	return n.Name
}

func pathNode(n *syntax.Node) *syntax.Node {
	if p := childOf(n, syntax.Path); p != nil {
		return p
	}
	return n
}

func childOf(n *syntax.Node, k syntax.Kind) *syntax.Node {
	if n == nil {
		return nil
	}
	for _, ch := range n.Children {
		if ch, ok := ch.(*syntax.Node); ok && ch.Kind == k {
			return ch
		}
	}
	return nil
}

// sites checks annotations, docs, prose and code from every site.
func (c *Checker) sites(n *resolve.Node, annotations bool) {
	if annotations {
		for _, a := range n.Annotations {
			c.annotation(a)
		}
		for _, a := range n.GroupAnnotations {
			c.annotation(a)
		}
		for _, d := range n.Docs {
			c.prose(d.N, d.Scope, d.File)
		}
	}
	for _, p := range n.Prose {
		sc := p.Scope
		if n.Body != nil {
			sc = n.Body
		}
		if n.Shape == resolve.ShapeElement && p.N == n.Decl.N {
			// An element's own text: its content, its nested elements aside.
			c.proseChildren(p.N, sc, p.File)
			continue
		}
		c.prose(p.N, sc, p.File)
	}
	for _, cd := range n.Code {
		sc := cd.Scope
		if n.Body != nil {
			sc = n.Body
		}
		c.code(cd.N, sc, cd.File)
	}
}

// --- declarations ---

func (c *Checker) declared(n *resolve.Node) {
	d := n.Decl
	var tt ast.Type
	var params ast.Params
	if d.N.Kind == syntax.Declare {
		dn := ast.Declare{N: d.N}
		tt, params = dn.Type(), dn.Params()
	} else {
		vn := ast.Value{N: d.N}
		tt, params = vn.Type(), vn.Params()
	}
	c.nameFlags(n)
	c.params(params, n.Body, d.File)
	m := c.metaOf(n)
	nt, isNamed := tt.(ast.NamedType)
	if !isNamed {
		c.typeNames(tt, d.Scope, d.File)
	} else {
		c.slot(n, nt, m.decl, d.Scope, d.File)
	}
	if n.Shape == resolve.ShapeValue && n.Value != nil {
		sc := n.Body
		if sc == nil {
			sc = d.Scope
		}
		if m.decl != nil && c.IsKind(m.decl) {
			// `name: kind = value` gives a kind instance its text, as in
			// `tour_term: term =| …`, or binds a func to its body.
			c.expr(n.Value, sc, d.File, nil)
		} else {
			c.value(n.Value, m.t, sc, d.File, nil)
		}
	}
}

// slot checks a `:` slot: the name resolves to a kind or a type, and its
// flags and arguments fit.
func (c *Checker) slot(n *resolve.Node, nt ast.NamedType, decl *resolve.Node, sc *resolve.Scope, file *resolve.File) {
	segs := resolve.PathSegs(nt.Path().N)
	b := c.lookupSlot(sc, file, off(nt.N), segs)
	switch {
	case b.Local != nil:
		c.LocalUses[b.Local]++
		c.typeArgs(nt.Args(), nil, sc, file)
		return
	case b.Node == nil:
		c.errorf(file, nt.Path().N, "unresolved", "unknown kind or type %s%s", strings.Join(segs, "."), c.closest(sc, segs[0]))
		return
	}
	c.Refs[nt.Path().N] = b.Node
	if decl == nil {
		decl = b.Node
	} else if c.IsKind(decl) && decl != b.Node && len(b.Node.Overloads) == 0 && decl.Name == b.Node.Name {
		// The enclosing kind's choice among kinds of one name.
		c.Refs[nt.Path().N] = decl
	}
	switch {
	case c.IsKind(decl):
		decl = c.pick(decl, nt.Args(), n, sc, file)
		c.kindFlags(nt.Flags(), decl, sc, file)
		c.args(nt.Args(), c.KindParams(decl, n), "kind "+decl.Name, sc, file, true)
	case c.IsType(decl):
		c.kindFlags(nt.Flags(), nil, sc, file)
		c.typeArgs(nt.Args(), decl, sc, file)
	default:
		c.errorf(file, nt.Path().N, "not-a-kind", "%s is not a kind or a type", strings.Join(segs, "."))
	}
}

func (c *Checker) nameFlags(n *resolve.Node) {
	fl := childOf(n.Decl.N, syntax.Flags)
	if fl == nil {
		return
	}
	for _, t := range (ast.Flags{N: fl}).Idents() {
		c.flag(t, n.Decl.Scope, n.Decl.File)
	}
}

func (c *Checker) kindFlags(fl ast.Flags, kind *resolve.Node, sc *resolve.Scope, file *resolve.File) {
	for _, t := range fl.Idents() {
		c.flag(t, sc, file)
	}
}

// flag checks that a flag names a flag declaration in scope.
func (c *Checker) flag(t *syntax.Token, sc *resolve.Scope, file *resolve.File) {
	n := c.lookupPath(sc, file, t.Start, []string{t.Text}, c.IsFlag)
	if n == nil {
		c.diags.Errorf(string(file.ID), t.Start, t.End(), "unresolved", "unknown flag %s%s", t.Text, c.closestWhere(sc, t.Text, c.IsFlag))
		return
	}
	if !c.IsFlag(n) {
		c.diags.Errorf(string(file.ID), t.Start, t.End(), "not-a-flag", "%s is not a flag", t.Text)
	}
}

// params checks parameter types, defaults and annotations.
func (c *Checker) params(ps ast.Params, sc *resolve.Scope, file *resolve.File) {
	for _, p := range ps.Params() {
		for _, f := range p.Flags().Idents() {
			c.flag(f, sc, file)
		}
		for _, a := range childrenOf(p.N, syntax.Annotation) {
			c.annotation(resolve.Site{File: file, N: a, Scope: sc})
		}
		if t := p.Type(); t != nil {
			c.typeNames(t, sc, file)
			if d := p.Default(); d != nil {
				c.value(d, c.typeOf(t, sc, file), sc, file, nil)
			}
		}
	}
}

func childrenOf(n *syntax.Node, k syntax.Kind) []*syntax.Node {
	var out []*syntax.Node
	for _, ch := range n.Children {
		if ch, ok := ch.(*syntax.Node); ok && ch.Kind == k {
			out = append(out, ch)
		}
	}
	return out
}

// typeNames checks that every name in a type resolves to a type.
func (c *Checker) typeNames(t ast.Type, sc *resolve.Scope, file *resolve.File) {
	switch t := t.(type) {
	case ast.NamedType:
		segs := resolve.PathSegs(t.Path().N)
		b := c.lookupSlot(sc, file, off(t.N), segs)
		switch {
		case b.Local != nil:
			c.LocalUses[b.Local]++
		case b.Node == nil:
			c.errorf(file, t.Path().N, "unresolved", "unknown type %s%s", strings.Join(segs, "."), c.closest(sc, segs[0]))
			return
		default:
			c.Refs[t.Path().N] = b.Node
		}
		c.typeArgs(t.Args(), b.Node, sc, file)
	case ast.OptionalType:
		c.typeNames(t.Type(), sc, file)
	case ast.UnionType:
		c.typeNames(t.Lhs(), sc, file)
		c.typeNames(t.Rhs(), sc, file)
	case ast.IntersectionType:
		c.typeNames(t.Lhs(), sc, file)
		c.typeNames(t.Rhs(), sc, file)
	default:
	}
}

// typeArgs checks a type's arguments: positional ones are types, named
// ones fill its parameters.
func (c *Checker) typeArgs(args ast.Args, decl *resolve.Node, sc *resolve.Scope, file *resolve.File) {
	if args.N == nil {
		return
	}
	var params []Param
	if decl != nil && !resolve.IsBuiltin(decl) {
		params = c.Params(decl)
	}
	for _, a := range args.Args() {
		switch a := a.(type) {
		case ast.PositionalArg:
			c.expr(a.Expr(), sc, file, nil)
			c.typeArgRef(a.Expr(), sc, file)
		case ast.NamedArg:
			if params != nil && a.Ident() != nil && findParam(params, a.Ident().Text) == nil {
				c.errorf(file, a.N, "unknown-argument", "%s takes no argument %s%s", decl.Name, a.Ident().Text, closestParam(params, a.Ident().Text))
			}
			c.expr(a.Expr(), sc, file, nil)
		}
	}
}

// typeArgRef records what a positional type argument names, as List(T)'s
// T or Ref(K)'s K: a kind or a type, wherever another binding of the name
// comes first.
func (c *Checker) typeArgRef(e ast.Expr, sc *resolve.Scope, file *resolve.File) {
	var p ast.PathExpr
	switch e := e.(type) {
	case ast.PathExpr:
		p = e
	case ast.CallExpr:
		callee, ok := e.Callee().(ast.PathExpr)
		if !ok {
			return
		}
		p = callee
		for _, a := range e.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				c.typeArgRef(pa.Expr(), sc, file)
			}
		}
	default:
		return
	}
	if b := c.lookupSlot(sc, file, off(p.N), resolve.PathSegs(p.Path().N)); b.Node != nil {
		c.Refs[p.Path().N] = b.Node
	}
}

func findParam(ps []Param, name string) *Param {
	for i := range ps {
		if ps[i].Name == name {
			return &ps[i]
		}
	}
	return nil
}

// args checks arguments against parameters: named ones must exist,
// positional ones fill parameters in order, a variadic parameter takes the
// rest, and each value fits its parameter's type.
func (c *Checker) args(args ast.Args, params []Param, what string, sc *resolve.Scope, file *resolve.File, required bool) {
	given := map[string]bool{}
	pos := 0
	for _, a := range args.Args() {
		switch a := a.(type) {
		case ast.PositionalArg:
			if pos >= len(params) {
				c.errorf(file, a.N, "too-many-arguments", "%s takes %d positional argument%s", what, len(params), plural(len(params)))
				c.expr(a.Expr(), sc, file, nil)
				continue
			}
			p := params[pos]
			given[p.Name] = true
			if p.Variadic {
				c.variadic(a.Expr(), p, sc, file)
			} else {
				pos++
				c.value(a.Expr(), p.Type, sc, file, &p)
			}
		case ast.NamedArg:
			id := a.Ident()
			if id == nil {
				continue
			}
			p := findParam(params, id.Text)
			if p == nil {
				c.errorf(file, a.N, "unknown-argument", "%s takes no argument %s%s", what, id.Text, closestParam(params, id.Text))
				c.expr(a.Expr(), sc, file, nil)
				continue
			}
			given[p.Name] = true
			if p.Variadic {
				c.variadic(a.Expr(), *p, sc, file)
			} else {
				c.value(a.Expr(), p.Type, sc, file, p)
			}
		}
	}
	if !required {
		return
	}
	for _, p := range params {
		if !p.Optional && !p.Variadic && !given[p.Name] {
			at := args.N
			c.errorf(file, at, "missing-argument", "%s needs the argument %s", what, p.Name)
		}
	}
}

// pick selects the signature of a declaration with <overload>s: the first,
// in declaration order, whose parameters accept the arguments. When none
// does, the first is returned and its errors are what the call reports.
func (c *Checker) pick(d *resolve.Node, args ast.Args, instance *resolve.Node, sc *resolve.Scope, file *resolve.File) *resolve.Node {
	if d == nil || len(d.Overloads) == 0 {
		return d
	}
	for _, k := range append([]*resolve.Node{d}, d.Overloads...) {
		if c.fits(k, args, instance, sc, file) {
			return k
		}
	}
	return d
}

// fits reports whether arguments check against a signature without errors.
func (c *Checker) fits(k *resolve.Node, args ast.Args, instance *resolve.Node, sc *resolve.Scope, file *resolve.File) bool {
	saved, savedStrict := c.diags, c.strict
	uses := make(map[*resolve.Local]int, len(c.LocalUses))
	for l, n := range c.LocalUses {
		uses[l] = n
	}
	c.diags, c.strict = nil, true
	params := c.KindParams(k, instance)
	if instance == nil {
		params = c.Params(k)
	}
	c.args(args, params, k.Name, sc, file, c.IsKind(k))
	ok := !c.diags.HasErrors()
	c.diags, c.strict, c.LocalUses = saved, savedStrict, uses
	return ok
}

// variadic checks the values of a <variadic> parameter: any number of
// positional arguments, or one list.
func (c *Checker) variadic(e ast.Expr, p Param, sc *resolve.Scope, file *resolve.File) {
	if arr, ok := e.(ast.ArrayExpr); ok {
		for _, x := range arr.Exprs() {
			c.value(x, p.Type, sc, file, &p)
		}
		return
	}
	c.value(e, p.Type, sc, file, &p)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// --- anonymous nodes and calls ---

func (c *Checker) anon(n *resolve.Node) {
	p := calleePath(n)
	if p == nil {
		return
	}
	segs := resolve.PathSegs(p)
	sc, file := n.Decl.Scope, n.Decl.File
	var args ast.Args
	var flags ast.Flags
	if n.Decl.N.Kind == syntax.Invoke {
		inv := ast.Invoke{N: n.Decl.N}
		args, flags = inv.Args(), inv.Flags()
	}
	b := c.lookupBinding(sc, file, off(p), segs)
	if b.Local != nil {
		c.LocalUses[b.Local]++
		c.args(args, nil, segs[0], sc, file, false)
		return
	}
	if b.Node == nil {
		if c.methodCall(n, segs, p, args, sc, file) {
			return
		}
		c.errorf(file, p, "unresolved", "unknown kind or call %s%s", strings.Join(segs, "."), c.closest(sc, segs[0]))
		c.argExprs(args, sc, file)
		return
	}
	d := b.Node
	if m := c.metaOf(n).decl; m != nil {
		d = m
	}
	c.Refs[p] = d
	switch {
	case c.IsKind(d):
		c.kindFlags(flags, d, sc, file)
		c.args(args, c.KindParams(d, n), "kind "+d.Name, sc, file, true)
	case c.IsType(d):
		c.construct(d, args, sc, file)
	default:
		c.call(d, args, sc, file)
	}
	if n.Value != nil {
		c.expr(n.Value, n.Body, file, nil)
	}
}

// listMethods change a list or field in place, as in requires.add(x).
var listMethods = map[string]bool{"add": true, "remove": true, "insert": true, "append": true, "prepend": true}

// methodCall accepts `field.method(…)` on a field the node's kind or type
// declares, such as `requires.add(checks.e2e)` or `ci.timeout.remove()`.
func (c *Checker) methodCall(n *resolve.Node, segs []string, p *syntax.Node, args ast.Args, sc *resolve.Scope, file *resolve.File) bool {
	if len(segs) < 2 || !listMethods[segs[len(segs)-1]] {
		return false
	}
	host := n.Parent
	for i, s := range segs[:len(segs)-1] {
		if ch := host.Child(s); ch != nil {
			host = ch
			continue
		}
		if i == 0 {
			if b := c.lookupBinding(sc, file, off(p), segs[:1]); b.Node != nil {
				host = b.Node
				continue
			}
		}
		if _, ok := c.Field(host, s); !ok {
			return false
		}
		host = nil
		break
	}
	c.argExprs(args, sc, file)
	return true
}

func (c *Checker) argExprs(args ast.Args, sc *resolve.Scope, file *resolve.File) {
	for _, a := range args.Args() {
		switch a := a.(type) {
		case ast.PositionalArg:
			c.expr(a.Expr(), sc, file, nil)
		case ast.NamedArg:
			c.expr(a.Expr(), sc, file, nil)
		}
	}
}

// construct checks a constructor call: named arguments are the type's
// members.
func (c *Checker) construct(t *resolve.Node, args ast.Args, sc *resolve.Scope, file *resolve.File) {
	if a := c.aliasOf(t); a != nil {
		t = a
	}
	for _, a := range args.Args() {
		switch a := a.(type) {
		case ast.PositionalArg:
			c.expr(a.Expr(), sc, file, nil)
		case ast.NamedArg:
			id := a.Ident()
			if id == nil {
				continue
			}
			if resolve.IsBuiltin(t) || c.typeOpen(t) {
				c.expr(a.Expr(), sc, file, nil)
				continue
			}
			f, ok := c.typeMember(t, id.Text, map[*resolve.Node]bool{})
			if !ok {
				if p := findParam(c.Params(t), id.Text); p != nil {
					c.value(a.Expr(), p.Type, sc, file, p)
					continue
				}
				c.errorf(file, a.N, "unknown-field", "%s has no member %s%s", t.Name, id.Text, c.closestMember(t, id.Text))
				c.expr(a.Expr(), sc, file, nil)
				continue
			}
			c.value(a.Expr(), f.Type, sc, file, nil)
		}
	}
}

// call checks a call of a func, task, op or macro: named arguments are its
// parameters.
func (c *Checker) call(d *resolve.Node, args ast.Args, sc *resolve.Scope, file *resolve.File) {
	d = c.pick(d, args, nil, sc, file)
	params := c.Params(d)
	if len(params) == 0 {
		c.argExprs(args, sc, file)
		return
	}
	c.args(args, params, d.Name, sc, file, false)
}

// --- sets, enters and values ---

func (c *Checker) setValue(n *resolve.Node) {
	if n.Value == nil || n.Parent == nil {
		return
	}
	site := n.ValueSite
	if site.N != nil && site.N.Kind == syntax.MapEntry && c.Open(n.Parent) {
		c.expr(n.Value, site.Scope, site.File, nil)
		return
	}
	f, ok := c.Field(n.Parent, n.Name)
	if !ok {
		c.errorf(site.File, pathNode(site.N), "unknown-field", "%s has no field %s%s", describe(c, n.Parent), n.Name, c.closestField(n.Parent, n.Name))
		c.expr(n.Value, site.Scope, site.File, nil)
		return
	}
	if f.Flag {
		c.value(n.Value, ty{decl: builtin("Bool")}, site.Scope, site.File, nil)
		return
	}
	if _, isMap := n.Value.(ast.MapExpr); isMap && f.Type.decl != nil && !c.typeOpen(f.Type.decl) && !resolve.IsBuiltin(f.Type.decl) {
		// The keys were bound as members and are checked one by one.
		return
	}
	c.value(n.Value, f.Type, site.Scope, site.File, nil)
}

func describe(c *Checker, n *resolve.Node) string {
	what := qualified(n)
	if what == "" {
		what = "the root"
	}
	if k := c.KindOf(n); k != nil {
		return fmt.Sprintf("%s (a %s)", what, k.Name)
	}
	if t := c.TypeOf(n); t != nil {
		return fmt.Sprintf("%s (a %s)", what, t.Name)
	}
	return what
}

func builtin(name string) *resolve.Node {
	return resolve.Builtins().Node.Child(name)
}

// --- annotations ---

func (c *Checker) annotation(s resolve.Site) {
	a := ast.Annotation{N: s.N}
	segs := resolve.PathSegs(a.Path().N)
	if len(segs) == 0 {
		return
	}
	d := c.lookupPath(s.Scope, s.File, off(s.N), segs, c.IsAnnotation)
	if d == nil {
		c.errorf(s.File, a.Path().N, "unresolved", "unknown annotation @%s%s", strings.Join(segs, "."), c.closestWhere(s.Scope, segs[0], c.IsAnnotation))
		c.argExprs(a.Args(), s.Scope, s.File)
		return
	}
	if !c.IsAnnotation(d) {
		c.errorf(s.File, a.Path().N, "not-an-annotation", "%s is not an annotation", strings.Join(segs, "."))
		return
	}
	if len(segs) == 1 {
		if forms := c.ambiguousAnnotation(s.Scope, segs[0]); len(forms) > 1 {
			c.errorf(s.File, a.Path().N, "ambiguous", "@%s is declared by more than one imported package: write %s", segs[0], strings.Join(forms, " or "))
		}
	}
	c.Refs[a.Path().N] = d
	for _, f := range a.Flags().Idents() {
		c.flag(f, s.Scope, s.File)
	}
	c.args(a.Args(), c.Params(d), "@"+d.Name, s.Scope, s.File, len(a.Flags().Idents()) == 0)
}

// ambiguousAnnotation returns each package-qualified form of an
// unqualified annotation name, as @proto.code and @jsonrpc.code, when the
// innermost scope that binds it as an annotation does so through more than
// one imported package. A name the tree declares itself shadows imports.
func (c *Checker) ambiguousAnnotation(sc *resolve.Scope, name string) []string {
	seen := map[*resolve.Scope]bool{}
	var forms []string
	var walk func(sc *resolve.Scope) bool
	walk = func(sc *resolve.Scope) bool {
		for ; sc != nil; sc = sc.Parent {
			if seen[sc] {
				return false
			}
			seen[sc] = true
			if sc.Node != nil {
				if n := sc.Node.Child(name); n != nil && c.IsAnnotation(n) {
					return true
				}
			}
			pkgs := map[*resolve.Node]bool{}
			for _, p := range sc.Imports {
				n := p.Lookup(name)
				if n == nil || !c.IsAnnotation(n) || n.Parent == nil || pkgs[n.Parent] {
					continue
				}
				pkgs[n.Parent] = true
				forms = append(forms, "@"+n.Parent.Name+"."+name)
			}
			if len(forms) > 0 {
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
	sort.Strings(forms)
	return forms
}

// --- suggestions ---

func (c *Checker) closest(sc *resolve.Scope, name string) string {
	return c.closestWhere(sc, name, nil)
}

func (c *Checker) closestWhere(sc *resolve.Scope, name string, pred func(*resolve.Node) bool) string {
	if sc == nil {
		return ""
	}
	var cands []string
	sc.Names(func(n string, b resolve.Binding) {
		if pred == nil || (b.Node != nil && pred(b.Node)) {
			cands = append(cands, n)
		}
	})
	return suggest(name, cands)
}

func (c *Checker) closestField(n *resolve.Node, name string) string {
	var cands []string
	if k := c.KindOf(n); k != nil {
		for _, p := range c.KindParams(k, n) {
			cands = append(cands, p.Name)
		}
		for _, ch := range k.Children {
			if ch.Name != "" {
				cands = append(cands, ch.Name)
			}
		}
	}
	if t := c.TypeOf(n); t != nil {
		for _, ch := range t.Children {
			cands = append(cands, ch.Name)
		}
	}
	for _, ch := range n.Children {
		cands = append(cands, ch.Name)
	}
	return suggest(name, cands)
}

func (c *Checker) closestMember(t *resolve.Node, name string) string {
	var cands []string
	for _, ch := range t.Children {
		cands = append(cands, ch.Name)
	}
	return suggest(name, cands)
}

func closestParam(ps []Param, name string) string {
	var cands []string
	for _, p := range ps {
		cands = append(cands, p.Name)
	}
	return suggest(name, cands)
}

// suggest names the closest candidate within a small edit distance.
func suggest(name string, cands []string) string {
	best, bestD := "", 3
	sort.Strings(cands)
	for _, cand := range cands {
		if cand == "" || cand == name {
			continue
		}
		if d := distance(strings.ToLower(name), strings.ToLower(cand)); d < bestD {
			best, bestD = cand, d
		}
	}
	if best == "" {
		return ""
	}
	return "; did you mean " + best + "?"
}

func distance(a, b string) int {
	if len(a) > 40 || len(b) > 40 {
		return 99
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
