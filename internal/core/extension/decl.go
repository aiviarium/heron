// Package extension runs what packages declare and code implements: it
// starts extension processes and the JS host, speaks the extension
// protocol, a Sparrow session, to them, runs the initialize handshake,
// applies the limits each declares, and registers what each implements in
// the registry the built-in parts use, as the Schemas and Interop tabs
// describe.
package extension

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/extension/protocol"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// Decl is a declaration a process implements, with where it was declared.
type Decl struct {
	protocol.Declaration
	Node *resolve.Node
}

// Spec is one process to start: the JS host, or an `extension(…)` node.
type Spec struct {
	// Name is the extension node's path, or JSHost.
	Name    string
	Command []string
	Dir     string
	// Files, Network and Commands are what it declared it needs.
	Files    []string
	Network  []string
	Commands []string
	Version  string
	Checksum string
	Decls    []Decl
	Node     *resolve.Node
}

// JSHost names the JS host's spec.
const JSHost = "js"

// Discovery is what a checked tree declares for extensions to implement.
type Discovery struct {
	Specs []*Spec
	Diags diag.List
}

// Discover finds every declaration bound to a module through js(…) and
// every `@extern(…)` declaration, in the tree and the packages it imports,
// and groups them by the process that serves them.
func Discover(t *resolve.Tree, c *types.Checker) *Discovery {
	d := &Discovery{}
	js := &Spec{Name: JSHost}
	byExt := map[*resolve.Node]*Spec{}
	seen := map[*resolve.Node]bool{}
	visit := func(n *resolve.Node) {
		if n.Name == "renderers" && n.Parent != nil && types.IsStd(c.KindOf(n.Parent), "docs", "docs.site") && !seen[n] {
			seen[n] = true
			js.Decls = append(js.Decls, rendererDecls(c, n)...)
			return
		}
		if n.Shape == resolve.ShapeMounted && n.Origin != nil {
			n = n.Origin
		}
		if seen[n] || n.Decl.N == nil {
			return
		}
		seen[n] = true
		point, ok := pointOf(c, n)
		if !ok {
			return
		}
		if ext := externOf(c, n); ext != nil {
			s := byExt[ext]
			if s == nil {
				s = extSpec(ext)
				byExt[ext] = s
				d.Specs = append(d.Specs, s)
			}
			s.Decls = append(s.Decls, declOf(c, n, point, "", n.Name))
			return
		}
		module, export, ok := moduleOf(c, n)
		if !ok {
			return
		}
		js.Decls = append(js.Decls, declOf(c, n, point, module, export))
	}
	walk := func(t *resolve.Tree) {
		t.Walk(visit)
	}
	walk(t)
	done := map[*resolve.Package]bool{}
	var pkgs func(*resolve.Tree)
	pkgs = func(t *resolve.Tree) {
		for _, p := range t.Imports {
			if done[p] || p.Tree == nil {
				continue
			}
			done[p] = true
			walk(p.Tree)
			pkgs(p.Tree)
		}
	}
	pkgs(t)
	if len(js.Decls) > 0 {
		d.Specs = append([]*Spec{js}, d.Specs...)
	}
	for _, s := range d.Specs {
		sort.SliceStable(s.Decls, func(i, j int) bool { return s.Decls[i].Path < s.Decls[j].Path })
	}
	return d
}

// rendererDecls reads a docs block's `renderers = [js("…"), …]`: each
// module is a renderer declaration, named by the module, whose kinds the
// JS host reports in the handshake.
func rendererDecls(c *types.Checker, n *resolve.Node) []Decl {
	arr, ok := n.Value.(ast.ArrayExpr)
	if !ok {
		return nil
	}
	var out []Decl
	for _, e := range arr.Exprs() {
		var module string
		switch e := e.(type) {
		case ast.CallExpr:
			spec, ok := jsCall(c, e, n.ValueSite.Scope, n.Decl.File)
			if !ok {
				continue
			}
			module = absModule(spec, n.Decl.File)
		case ast.PathExpr:
			m := lookupWhere(n.ValueSite.Scope, n.Decl.File, e.N, resolve.PathSegs(e.Path().N), func(m *resolve.Node) bool { return types.IsStd(c.KindOf(m), "js", "js.module") })
			if m == nil {
				continue
			}
			call, isCall := m.Value.(ast.CallExpr)
			if !isCall {
				continue
			}
			spec, ok := jsCall(c, call, m.Decl.Scope, m.Decl.File)
			if !ok {
				continue
			}
			module = absModule(spec, m.Decl.File)
		default:
			continue
		}
		out = append(out, Decl{Node: n, Declaration: protocol.Declaration{Path: RendererPath(module), Point: protocol.PointRenderer, Module: module, Export: "*"}})
	}
	return out
}

// RendererPath is the declaration path of a renderer module.
func RendererPath(module string) string { return "renderers:" + module }

// pointOf returns the extension point a node's kind is.
func pointOf(c *types.Checker, n *resolve.Node) (protocol.Point, bool) {
	k := c.KindOf(n)
	switch {
	case k == nil:
		return "", false
	case types.IsStd(k, "core", "core.func"):
		return protocol.PointFunc, true
	case types.IsStd(k, "core", "core.macro"):
		return protocol.PointMacro, true
	case types.IsStd(k, "apis", "apis.transport"):
		return protocol.PointTransport, true
	case types.IsStd(k, "schema", "schema.lint"):
		return protocol.PointLint, true
	case types.IsStd(k, "schema", "schema.resource"):
		return protocol.PointResource, true
	case types.IsStd(k, "schema", "schema.generator"):
		return protocol.PointGenerator, true
	}
	return "", false
}

// externOf returns the extension an `@extern(…)` annotation names.
func externOf(c *types.Checker, n *resolve.Node) *resolve.Node {
	for _, s := range n.Annotations {
		a := ast.Annotation{N: s.N}
		if s.N == nil || s.N.Kind != syntax.Annotation {
			continue
		}
		target := c.Refs[a.Path().N]
		if target == nil {
			target = lookup(s.Scope, s.File, s.N, resolve.PathSegs(a.Path().N))
		}
		if target == nil || !types.IsStd(target, "schema", "schema.extern") {
			target = lookupWhere(s.Scope, s.File, s.N, resolve.PathSegs(a.Path().N), func(n *resolve.Node) bool { return types.IsStd(n, "schema", "schema.extern") })
		}
		if target == nil {
			continue
		}
		for _, arg := range a.Args().Args() {
			var e ast.Expr
			switch arg := arg.(type) {
			case ast.PositionalArg:
				e = arg.Expr()
			case ast.NamedArg:
				e = arg.Expr()
			}
			if p, ok := e.(ast.PathExpr); ok {
				if ext := lookup(s.Scope, s.File, p.N, resolve.PathSegs(p.Path().N)); ext != nil {
					return ext
				}
			}
		}
	}
	return nil
}

func lookup(sc *resolve.Scope, f *resolve.File, at *syntax.Node, segs []string) *resolve.Node {
	return lookupWhere(sc, f, at, segs, nil)
}

// LookupWhere resolves a dotted path from a scope at a syntax node, taking
// the first binding that pred (when given) accepts.
func LookupWhere(sc *resolve.Scope, f *resolve.File, at *syntax.Node, segs []string, pred func(*resolve.Node) bool) *resolve.Node {
	return lookupWhere(sc, f, at, segs, pred)
}

// lookupWhere resolves a path, taking the first binding that pred accepts,
// as `js` names both std/js's package node and its func.
func lookupWhere(sc *resolve.Scope, f *resolve.File, at *syntax.Node, segs []string, pred func(*resolve.Node) bool) *resolve.Node {
	if sc == nil || len(segs) == 0 {
		return nil
	}
	start, _ := at.Span()
	var out *resolve.Node
	sc.Each(segs[0], f, start, func(b resolve.Binding) bool {
		n := b.Node
		for _, s := range segs[1:] {
			if n == nil {
				break
			}
			n = n.Child(s)
		}
		if n != nil && (pred == nil || pred(n)) {
			out = n
			return false
		}
		return true
	})
	return out
}

// moduleOf reads `= impl.name`, where impl is a module, or `= js("…")`.
func moduleOf(c *types.Checker, n *resolve.Node) (module, export string, ok bool) {
	switch e := n.Value.(type) {
	case ast.CallExpr:
		if spec, ok := jsCall(c, e, n.Decl.Scope, n.Decl.File); ok {
			return absModule(spec, n.Decl.File), "default", true
		}
	case ast.PathExpr:
		segs := resolve.PathSegs(e.Path().N)
		if len(segs) < 2 {
			return "", "", false
		}
		m := lookupWhere(n.Decl.Scope, n.Decl.File, e.N, segs[:1], func(m *resolve.Node) bool { return types.IsStd(c.KindOf(m), "js", "js.module") })
		if m == nil {
			return "", "", false
		}
		call, isCall := m.Value.(ast.CallExpr)
		if !isCall {
			return "", "", false
		}
		if spec, ok := jsCall(c, call, m.Decl.Scope, m.Decl.File); ok {
			return absModule(spec, m.Decl.File), strings.Join(segs[1:], "."), true
		}
	default:
	}
	return "", "", false
}

func jsCall(c *types.Checker, e ast.CallExpr, sc *resolve.Scope, f *resolve.File) (string, bool) {
	p, ok := e.Callee().(ast.PathExpr)
	if !ok {
		return "", false
	}
	fn := lookupWhere(sc, f, p.N, resolve.PathSegs(p.Path().N), func(n *resolve.Node) bool { return types.IsStd(n, "js", "js.js") })
	if fn == nil {
		return "", false
	}
	for _, a := range e.Args().Args() {
		if pa, ok := a.(ast.PositionalArg); ok {
			return resolve.StringValue(pa.Expr().Syntax())
		}
		if na, ok := a.(ast.NamedArg); ok && na.Ident() != nil && na.Ident().Text == "path" {
			return resolve.StringValue(na.Expr().Syntax())
		}
	}
	return "", false
}

// absModule makes a file module absolute, relative to the file naming it;
// a package name stays as written.
func absModule(spec string, f *resolve.File) string {
	if f == nil || f.ID.IsStd() || !(strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../")) {
		return spec
	}
	return filepath.Join(f.ID.Dir(), spec)
}

func declOf(c *types.Checker, n *resolve.Node, point protocol.Point, module, export string) Decl {
	d := Decl{Node: n, Declaration: protocol.Declaration{Path: n.Path(), Point: point, Module: module, Export: export}}
	var ps ast.Params
	var slot ast.Type
	switch n.Decl.N.Kind {
	case syntax.Declare:
		ps, slot = ast.Declare{N: n.Decl.N}.Params(), ast.Declare{N: n.Decl.N}.Type()
	case syntax.Value:
		ps, slot = ast.Value{N: n.Decl.N}.Params(), ast.Value{N: n.Decl.N}.Type()
	}
	for _, p := range ps.Params() {
		if p.Ident() == nil {
			continue
		}
		sig := protocol.ParamSig{Name: p.Ident().Text, Type: TypeRefOf(p.Type(), false)}
		if p.Question() != nil || p.Default() != nil {
			t := true
			sig.Optional = &t
		}
		for _, f := range p.Flags().Idents() {
			if f.Text == "variadic" {
				t := true
				sig.Variadic = &t
			}
		}
		d.Params = append(d.Params, sig)
	}
	if nt, ok := slot.(ast.NamedType); ok {
		for _, f := range nt.Flags().Idents() {
			if f.Text == "pure" {
				t := true
				d.Pure = &t
			}
		}
		for _, a := range nt.Args().Args() {
			switch a := a.(type) {
			case ast.PositionalArg:
				if point == protocol.PointFunc || point == protocol.PointResource {
					r := typeRefOfExpr(a.Expr())
					d.Returns = &r
				}
			case ast.NamedArg:
				if a.Ident() != nil && a.Ident().Text == "target" {
					d.Target, _ = resolve.StringValue(a.Expr().Syntax())
				}
				if a.Ident() != nil && (a.Ident().Text == "returns" || a.Ident().Text == "type") {
					r := typeRefOfExpr(a.Expr())
					d.Returns = &r
				}
			}
		}
	}
	for _, s := range n.Annotations {
		a := ast.Annotation{N: s.N}
		if s.N == nil || s.N.Kind != syntax.Annotation || resolve.PathText(a.Path().N) != "throws" {
			continue
		}
		for _, arg := range a.Args().Args() {
			if pa, ok := arg.(ast.PositionalArg); ok {
				d.Throws = append(d.Throws, strings.TrimSpace(pa.Expr().Syntax().Text()))
			}
		}
	}
	return d
}

// TypeRefOf describes a type as written.
func TypeRefOf(t ast.Type, optional bool) protocol.TypeRef {
	switch t := t.(type) {
	case ast.NamedType:
		r := protocol.TypeRef{Name: resolve.PathText(t.Path().N)}
		if optional {
			o := true
			r.Optional = &o
		}
		for _, a := range t.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				r.Args = append(r.Args, typeRefOfExpr(pa.Expr()))
			}
		}
		return r
	case ast.OptionalType:
		return TypeRefOf(t.Type(), true)
	default:
	}
	r := protocol.TypeRef{Name: "Any"}
	if optional {
		o := true
		r.Optional = &o
	}
	return r
}

func typeRefOfExpr(e ast.Expr) protocol.TypeRef {
	switch e := e.(type) {
	case ast.PathExpr:
		return protocol.TypeRef{Name: resolve.PathText(e.Path().N)}
	case ast.CallExpr:
		r := typeRefOfExpr(e.Callee())
		for _, a := range e.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				r.Args = append(r.Args, typeRefOfExpr(pa.Expr()))
			}
		}
		return r
	default:
	}
	return protocol.TypeRef{Name: "Any"}
}

// extSpec reads an `extension(command = […], …)` node.
func extSpec(n *resolve.Node) *Spec {
	s := &Spec{Name: n.Path(), Node: n}
	if n.Decl.File != nil && !n.Decl.File.ID.IsStd() {
		s.Dir = n.Decl.File.ID.Dir()
	}
	var args []ast.Arg
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if nt, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType); ok {
			args = nt.Args().Args()
		}
	case syntax.Invoke:
		args = ast.Invoke{N: n.Decl.N}.Args().Args()
	}
	for _, a := range args {
		na, ok := a.(ast.NamedArg)
		if !ok || na.Ident() == nil {
			continue
		}
		switch na.Ident().Text {
		case "command":
			s.Command = strings_(na.Expr())
		case "files":
			s.Files = strings_(na.Expr())
		case "network":
			s.Network = strings_(na.Expr())
		case "commands":
			s.Commands = strings_(na.Expr())
		case "version":
			s.Version, _ = resolve.StringValue(na.Expr().Syntax())
		case "checksum":
			s.Checksum, _ = resolve.StringValue(na.Expr().Syntax())
		}
	}
	return s
}

func strings_(e ast.Expr) []string {
	arr, ok := e.(ast.ArrayExpr)
	if !ok {
		if s, ok := resolve.StringValue(e.Syntax()); ok {
			return []string{s}
		}
		return nil
	}
	var out []string
	for _, x := range arr.Exprs() {
		if s, ok := resolve.StringValue(x.Syntax()); ok {
			out = append(out, s)
		}
	}
	return out
}

// mismatch is the check error for a declaration an extension doesn't
// implement, reported at the declaration.
func mismatch(d Decl, spec, why string) diag.Diagnostic {
	out := diag.Diagnostic{Severity: diag.Error, Code: "extension", Message: fmt.Sprintf("%s: %s %s", d.Path, spec, why)}
	if d.Node != nil && d.Node.Decl.File != nil {
		out.File = string(d.Node.Decl.File.ID)
		out.Start, out.End = d.Node.Decl.Span()
	}
	return out
}
