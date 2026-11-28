// Package project reads a checked tree as the spec model the tools share:
// a node's standard kind, its arguments, annotations and references, the
// features it belongs to and the links of its prose, and the contract
// coverage those links give.
package project

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// RelPath is a node's path inside its package, without the package node's
// name, as "checkout.pay_once" for shop.checkout.pay_once.
func RelPath(pkg *resolve.Package, n *resolve.Node) string {
	path := n.Path()
	if pkg != nil && pkg.Node != nil {
		path = strings.TrimPrefix(path, pkg.Node.Name+".")
	}
	return path
}

// --- tree helpers ---

// KindPath is the declaration path of a node's standard kind, such as
// "spec.rule", or "" for a node of no standard kind.
func KindPath(c *types.Checker, n *resolve.Node) string {
	k := c.KindOf(n)
	// A kind of the tree's own that extends a standard kind counts as it.
	for i := 0; k != nil && (k.Decl.File == nil || !k.Decl.File.ID.IsStd()) && i < 8; i++ {
		k = c.Extends(k)
	}
	if k == nil || k.Decl.File == nil || !k.Decl.File.ID.IsStd() {
		return ""
	}
	return k.Path()
}

// PackageOf returns the imported package a node belongs to, through
// mounted nodes' origins, or nil for a node of the tree itself.
func PackageOf(n *resolve.Node) *resolve.Package {
	for n.Origin != nil {
		n = n.Origin
	}
	r := n
	for r.Parent != nil {
		r = r.Parent
	}
	return r.Package
}

// Own calls f for each node of the tree's own, in tree order, skipping
// mounted package nodes; f returns false to skip a node's children.
func Own(t *resolve.Tree, f func(*resolve.Node) bool) {
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range n.Children {
			if ch.Shape == resolve.ShapeMounted || ch.Decl.File != nil && ch.Decl.File.ID.IsStd() {
				continue
			}
			if f(ch) {
				walk(ch)
			}
		}
	}
	walk(t.Root)
}

// KindArgs returns the arguments a node's kind slot is written with.
func KindArgs(n *resolve.Node) ast.Args {
	if n.Decl.N == nil {
		return ast.Args{}
	}
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if t, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType); ok {
			return t.Args()
		}
	case syntax.Value:
		if t, ok := (ast.Value{N: n.Decl.N}).Type().(ast.NamedType); ok {
			return t.Args()
		}
	case syntax.Invoke:
		return ast.Invoke{N: n.Decl.N}.Args()
	case syntax.Tag:
		// A kind element's attributes, as its kind's arguments.
		for n.ElementArgs.N == nil && n.Origin != nil {
			n = n.Origin // a mounted node's were read in its package
		}
		return n.ElementArgs
	}
	return ast.Args{}
}

// KindFlags returns the flags on a node's kind slot, as <security>.
func KindFlags(n *resolve.Node) []string {
	if n.Decl.N == nil {
		return nil
	}
	var fl ast.Flags
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if t, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType); ok {
			fl = t.Flags()
		}
	case syntax.Invoke:
		fl = ast.Invoke{N: n.Decl.N}.Flags()
	}
	if fl.N == nil {
		return nil
	}
	var out []string
	for _, t := range fl.Idents() {
		out = append(out, t.Text)
	}
	return out
}

// ArgString returns a positional (index ≥ 0) or named string argument.
func ArgString(args ast.Args, index int, name string) (string, bool) {
	if args.N == nil {
		return "", false
	}
	pos := 0
	for _, a := range args.Args() {
		switch a := a.(type) {
		case ast.PositionalArg:
			if pos == index {
				return resolve.StringValue(a.Expr().Syntax())
			}
			pos++
		case ast.NamedArg:
			if name != "" && a.Ident() != nil && a.Ident().Text == name {
				return resolve.StringValue(a.Expr().Syntax())
			}
		}
	}
	return "", false
}

// ArgExprs returns the positional argument expressions.
func ArgExprs(args ast.Args) []ast.Expr {
	if args.N == nil {
		return nil
	}
	var out []ast.Expr
	for _, a := range args.Args() {
		if pa, ok := a.(ast.PositionalArg); ok {
			out = append(out, pa.Expr())
		}
	}
	return out
}

// NamedArg returns a named argument's expression.
func NamedArg(args ast.Args, name string) ast.Expr {
	if args.N == nil {
		return nil
	}
	for _, a := range args.Args() {
		if na, ok := a.(ast.NamedArg); ok && na.Ident() != nil && na.Ident().Text == name {
			return na.Expr()
		}
	}
	return nil
}

// RefTarget resolves a path expression to a node, through the checker's
// references and then the scope it was written in.
func RefTarget(c *types.Checker, p *syntax.Node, site resolve.Site) *resolve.Node {
	if p == nil {
		return nil
	}
	if n := c.Refs[p]; n != nil {
		return n
	}
	var path *syntax.Node
	if p.Kind == syntax.Path {
		path = p
	} else {
		p.Walk(func(x *syntax.Node) bool {
			if path == nil && x.Kind == syntax.Path {
				path = x
			}
			return path == nil
		})
	}
	if path == nil {
		return nil
	}
	if n := c.Refs[path]; n != nil {
		return n
	}
	segs := resolve.PathSegs(path)
	if site.Scope == nil || len(segs) == 0 {
		return nil
	}
	var out *resolve.Node
	site.Scope.Each(segs[0], site.File, -1, func(b resolve.Binding) bool {
		n := b.Node
		for _, seg := range segs[1:] {
			if n == nil {
				break
			}
			n = n.Child(seg)
		}
		if n != nil {
			out = n
			return false
		}
		return true
	})
	return out
}

// RefsOf returns the nodes a node's positional kind arguments name.
func RefsOf(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, e := range ArgExprs(KindArgs(n)) {
		if t := RefTarget(c, e.Syntax(), n.Decl); t != nil {
			out = append(out, t)
		}
	}
	return out
}

func siteOf(n *resolve.Node, e ast.Expr) resolve.Site {
	return resolve.Site{File: n.Decl.File, N: e.Syntax(), Scope: n.Decl.Scope}
}

// AnnArg is one positional argument of an annotation, with the site the
// annotation is written at, which its references resolve from.
type AnnArg struct {
	Expr ast.Expr
	Site resolve.Site
}

// Annotations are a node's annotations named name, by the last segment of
// their path, its own and those of the group it stands in, in reading
// order.
func Annotations(n *resolve.Node, name string) []resolve.Site {
	if n == nil {
		return nil
	}
	var out []resolve.Site
	for _, a := range append(append([]resolve.Site(nil), n.Annotations...), n.GroupAnnotations...) {
		if a.N == nil || a.N.Kind != syntax.Annotation {
			continue
		}
		an := ast.Annotation{N: a.N}
		if an.Path().N == nil {
			continue
		}
		if AnnotationNamed(resolve.PathSegs(an.Path().N), name) {
			out = append(out, a)
		}
	}
	return out
}

// AnnotationNamed reports whether an annotation's path names name: as
// written unqualified, or qualified by a core package, as `@spec.status`.
// A path qualified by any other package, as `@proto.status`, names that
// package's own annotation.
func AnnotationNamed(segs []string, name string) bool {
	if len(segs) == 0 || segs[len(segs)-1] != name {
		return false
	}
	return len(segs) == 1 || len(segs) == 2 && (segs[0] == "spec" || segs[0] == "core")
}

// HasAnnotation reports whether a node carries an annotation named name.
func HasAnnotation(n *resolve.Node, name string) bool { return len(Annotations(n, name)) > 0 }

// AnnotationArgs are the positional arguments of a node's annotations
// named name, as `@touch(a, b)` gives a and b.
func AnnotationArgs(n *resolve.Node, name string) []AnnArg {
	var out []AnnArg
	for _, a := range Annotations(n, name) {
		for _, e := range ArgExprs(ast.Annotation{N: a.N}.Args()) {
			out = append(out, AnnArg{Expr: e, Site: resolve.Site{File: a.File, N: e.Syntax(), Scope: a.Scope}})
		}
	}
	return out
}

// AnnotationArg is the first argument of a node's annotation named name,
// or nil.
func AnnotationArg(n *resolve.Node, name string) *AnnArg {
	if as := AnnotationArgs(n, name); len(as) > 0 {
		return &as[0]
	}
	return nil
}

// AnnotationString is the string an annotation such as `@display("…")`
// holds.
func AnnotationString(n *resolve.Node, name string) (string, bool) {
	a := AnnotationArg(n, name)
	if a == nil {
		return "", false
	}
	return resolve.StringValue(a.Expr.Syntax())
}

// AnnotationText is the first argument of an annotation as written, such
// as `404` for `@status(404)`, or "".
func AnnotationText(n *resolve.Node, name string) string {
	a := AnnotationArg(n, name)
	if a == nil {
		return ""
	}
	return strings.TrimSpace(a.Expr.Syntax().Text())
}

// AnnotationRefs are the nodes an annotation's arguments name, as
// `@touch(desk)` names desk.
func AnnotationRefs(c *types.Checker, n *resolve.Node, name string) []*resolve.Node {
	var out []*resolve.Node
	for _, a := range AnnotationArgs(n, name) {
		if t := RefTarget(c, a.Expr.Syntax(), a.Site); t != nil {
			out = append(out, t)
		}
	}
	return out
}

// AnnotationRef is the node an annotation's first argument names, or nil.
func AnnotationRef(c *types.Checker, n *resolve.Node, name string) *resolve.Node {
	a := AnnotationArg(n, name)
	if a == nil {
		return nil
	}
	return RefTarget(c, a.Expr.Syntax(), a.Site)
}

// ProseLinks are the nodes a node's own prose links to, in the order
// written.
func ProseLinks(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, ps := range n.Prose {
		if ps.N == nil {
			continue
		}
		ps.N.Walk(func(y *syntax.Node) bool {
			if y.Kind == syntax.Path {
				if t := RefTarget(c, y, ps); t != nil {
					out = append(out, t)
				}
				return false
			}
			return true
		})
	}
	return out
}

// ErrorRules are the requirements an error's prose links to: the rules,
// use cases and use case steps that say when it's reported.
// An error names no raiser and realizes nothing; this is how it points up.
func ErrorRules(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	seen := map[*resolve.Node]bool{}
	for _, t := range ProseLinks(c, n) {
		if seen[t] {
			continue
		}
		switch KindPath(c, t) {
		case "spec.rule", "spec.usecase":
		default:
			if t.Shape != resolve.ShapeStep {
				continue
			}
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ErrorRaiser is who reports an error, derived from the other direction:
// the command that holds it, or else the subject of the first rule its prose
// links to. An op's `@throws` is the third way, which readers that index
// ops add. Nil when neither says.
func ErrorRaiser(c *types.Checker, n *resolve.Node) *resolve.Node {
	for a := n.Parent; a != nil; a = a.Parent {
		if KindPath(c, a) == "spec.command" {
			return a
		}
	}
	for _, r := range ErrorRules(c, n) {
		if by := Subject(c, r); by != nil {
			return by
		}
	}
	return nil
}

// ProseText is a node's own prose as written, its lines joined.
func ProseText(n *resolve.Node) string {
	var b strings.Builder
	for _, ps := range n.Prose {
		if ps.N != nil {
			b.WriteString(ps.N.Text())
		}
	}
	return b.String()
}

// ErrorMembers are the members of an error, the values it carries as data
// and in its message: its own in the order written, then those it
// inherits and doesn't declare again, nearest ancestor first.
func ErrorMembers(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	seen := map[string]bool{}
	for _, x := range append([]*resolve.Node{n}, c.ErrorAncestors(n)...) {
		for _, ch := range x.Children {
			if ch.Name == "" || seen[ch.Name] || ch.Decl.N == nil || ch.Decl.N.Kind != syntax.Declare && ch.Decl.N.Kind != syntax.Value {
				continue
			}
			if k := KindPath(c, ch); k == "" || k == "types.member" {
				seen[ch.Name] = true
				out = append(out, ch)
			}
		}
	}
	return out
}

// Feature is the feature a node stands in, the nearest one above it, or
// nil.
func Feature(c *types.Checker, n *resolve.Node) *resolve.Node {
	for a := n.Parent; a != nil; a = a.Parent {
		if KindPath(c, a) == "spec.feature" {
			return a
		}
	}
	return nil
}

// Features are the features a node belongs to: the one it stands in and
// those its constructor or @touch references, in that order.
func Features(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	seen := map[*resolve.Node]bool{}
	if f := Feature(c, n); f != nil {
		out = append(out, f)
		seen[f] = true
	}
	for _, a := range HardReferences(c, n) {
		if t := RefTarget(c, a.Expr.Syntax(), a.Site); t != nil && !seen[t] && KindPath(c, t) == "spec.feature" {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}
