package lint

import (
	"fmt"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/rpc"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// Register installs the core lints. Unknown fields are reported by
// `heron check` itself, since setting one is an error the Core format tab
// states; the lints here are what checking leaves to `heron lint`.
func Register(r *registry.Registry) {
	for _, l := range []registry.Lint{
		{Name: "unused-let", Summary: "a let that nothing below it uses", Run: wrap(unusedLet)},
		{Name: "flag-placement", Summary: "a flag on a kind it doesn't attach to", Run: wrap(flagPlacement)},
		{Name: "annotation-placement", Summary: "an annotation where it doesn't attach, or repeated without <repeatable>", Run: wrap(annotationPlacement)},
		{Name: "tag-kind", Summary: "a tag whose name isn't a kind that may stand in prose", Run: wrap(tagKind)},
		{Name: "example-params", Summary: "an op example that leaves out a required parameter", Run: wrap(exampleParams)},
		{Name: "repeated-import", Summary: "an import that an enclosing scope already makes", Run: wrap(repeatedImport)},
		{Name: "rpc-code", Summary: "an error an RPC method throws without a code for its binding, or a JSON-RPC code in the reserved range", Run: wrap(rpcCodes)},
	} {
		r.MustAddLint(l)
	}
}

func wrap(f func(*Input)) func(any) { return func(in any) { f(in.(*Input)) } }

// rpcCodes reports the errors gRPC and JSON-RPC methods throw without a
// code of their binding, and JSON-RPC codes in the reserved range.
func rpcCodes(in *Input) {
	for _, f := range rpc.Build(in.Tree, in.Checker).Lints {
		in.Report(f.Node, f.Site, diag.Warning, f.Message)
	}
}

// eachNode calls f for every node of the tree's own files.
func eachNode(in *Input, f func(*resolve.Node)) {
	in.Tree.Walk(func(n *resolve.Node) {
		if n.Shape == resolve.ShapeMounted && n.Origin != nil && n.Decl.N == n.Origin.Decl.N {
			return
		}
		f(n)
	})
}

func unusedLet(in *Input) {
	seen := map[*resolve.Scope]bool{}
	visit := func(sc *resolve.Scope, n *resolve.Node) {
		if sc == nil || seen[sc] {
			return
		}
		seen[sc] = true
		for _, l := range sc.Locals {
			if !l.Param && in.Checker.LocalUses[l] == 0 {
				in.Report(n, l.Site, diag.Warning, fmt.Sprintf("let %s is never used", l.Name))
			}
		}
	}
	for _, f := range in.Tree.Files {
		visit(in.Tree.FileScope(f.ID), in.Tree.Root)
	}
	eachNode(in, func(n *resolve.Node) { visit(n.Body, n) })
}

// kindSlot returns a node's kind slot flags and the scope they're read in.
func kindSlotFlags(n *resolve.Node) []*syntax.Token {
	if n.Decl.N == nil {
		return nil
	}
	var fl ast.Flags
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if t, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType); ok {
			fl = t.Flags()
		}
	case syntax.Value:
		if t, ok := (ast.Value{N: n.Decl.N}).Type().(ast.NamedType); ok {
			fl = t.Flags()
		}
	case syntax.Invoke:
		fl = (ast.Invoke{N: n.Decl.N}).Flags()
	}
	return fl.Idents()
}

func names(ns []*resolve.Node) string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return strings.Join(out, ", ")
}

func flagPlacement(in *Input) {
	c := in.Checker
	eachNode(in, func(n *resolve.Node) {
		k := c.KindOf(n)
		if k == nil {
			return
		}
		for _, t := range kindSlotFlags(n) {
			fd := c.FlagFor(t.Text, k, n.Decl.Scope, n.Decl.File, t.Start)
			if fd == nil {
				continue
			}
			if on := c.On(fd); len(on) > 0 && !c.Fits(k, on) {
				in.ReportAt(n, n.Decl.File, t.Start, t.End(), diag.Warning,
					fmt.Sprintf("<%s> doesn't attach to %s; it attaches to %s", t.Text, k.Name, names(on)))
			}
		}
		for _, a := range n.Annotations {
			ann := ast.Annotation{N: a.N}
			d := c.Refs[ann.Path().N]
			if d == nil {
				continue
			}
			for _, t := range ann.Flags().Idents() {
				fd := c.FlagFor(t.Text, d, a.Scope, a.File, t.Start)
				if fd == nil {
					continue
				}
				if on := c.On(fd); len(on) > 0 && !c.Fits(d, on) {
					in.ReportAt(n, a.File, t.Start, t.End(), diag.Warning,
						fmt.Sprintf("<%s> doesn't attach to @%s; it attaches to %s", t.Text, d.Name, names(on)))
				}
			}
		}
	})
}

func annotationPlacement(in *Input) {
	c := in.Checker
	eachNode(in, func(n *resolve.Node) {
		k := c.EffectiveKind(n)
		count := map[*resolve.Node]int{}
		for _, a := range n.Annotations {
			ann := ast.Annotation{N: a.N}
			d := c.Refs[ann.Path().N]
			if d == nil {
				continue
			}
			count[d]++
			if count[d] == 2 && !types.HasKindFlag(d, "repeatable") {
				in.Report(n, a, diag.Warning, fmt.Sprintf("@%s stands twice on one node, and it isn't <repeatable>", d.Name))
			}
			on := c.On(d)
			if len(on) == 0 {
				continue
			}
			if k == nil || !c.Fits(k, on) {
				what := "this node"
				if k != nil {
					what = k.Name
				}
				in.Report(n, a, diag.Warning, fmt.Sprintf("@%s doesn't attach to %s; it attaches to %s", d.Name, what, names(on)))
			}
		}
		// Annotations on parameters attach to types.param.
		if n.Decl.N == nil {
			return
		}
		ps := childOf(n.Decl.N, syntax.Params)
		if ps == nil {
			return
		}
		for _, p := range (ast.Params{N: ps}).Params() {
			for _, an := range childrenOf(p.N, syntax.Annotation) {
				ann := ast.Annotation{N: an}
				d := c.Refs[ann.Path().N]
				if d == nil {
					continue
				}
				on := c.On(d)
				ok := len(on) == 0
				for _, x := range on {
					if types.IsStd(x, "types", "types.param") {
						ok = true
					}
				}
				if !ok {
					in.Report(n, resolve.Site{File: n.Decl.File, N: an}, diag.Warning,
						fmt.Sprintf("@%s doesn't attach to a parameter; it attaches to %s", d.Name, names(on)))
				}
			}
		}
	})
}

func childOf(n *syntax.Node, k syntax.Kind) *syntax.Node {
	for _, c := range n.Children {
		if c, ok := c.(*syntax.Node); ok && c.Kind == k {
			return c
		}
	}
	return nil
}

func childrenOf(n *syntax.Node, k syntax.Kind) []*syntax.Node {
	var out []*syntax.Node
	for _, c := range n.Children {
		if c, ok := c.(*syntax.Node); ok && c.Kind == k {
			out = append(out, c)
		}
	}
	return out
}

func tagKind(in *Input) {
	c := in.Checker
	eachNode(in, func(n *resolve.Node) {
		for _, p := range append(append([]resolve.Site(nil), n.Prose...), n.Docs...) {
			p.N.Walk(func(m *syntax.Node) bool {
				if m.Kind != syntax.Tag {
					return true
				}
				path := childOf(m, syntax.Path)
				d := c.Refs[path]
				switch {
				case d == nil:
				case !c.IsKind(d):
					in.Report(n, resolve.Site{File: p.File, N: path}, diag.Warning,
						fmt.Sprintf("<%s> names %s, which isn't a kind; a tag must name a kind in scope", resolve.PathText(path), d.Name))
				case c.StructureOnly(d):
					in.Report(n, resolve.Site{File: p.File, N: path}, diag.Warning,
						fmt.Sprintf("<%s> is a kind that stands only in structure, not as a tag", d.Name))
				}
				return true
			})
		}
	})
}

func exampleParams(in *Input) {
	c := in.Checker
	eachNode(in, func(n *resolve.Node) {
		k := c.KindOf(n)
		if k == nil || !types.IsStd(k, "types", "types.example") {
			return
		}
		if len(n.Children) == 0 {
			return // an example that is only text
		}
		var op *resolve.Node
		for a := n.Parent; a != nil; a = a.Parent {
			if ak := c.KindOf(a); ak != nil && types.IsStd(ak, "types", "types.op") {
				op = a
				break
			}
		}
		if op == nil {
			return
		}
		for _, p := range c.Params(op) {
			if !p.Optional && n.Child(p.Name) == nil {
				in.Report(n, n.Decl, diag.Warning, fmt.Sprintf("the example leaves out %s, which %s requires", p.Name, op.Name))
			}
		}
	})
}

func repeatedImport(in *Input) {
	for _, f := range in.Tree.Files {
		sc := in.Tree.FileScope(f.ID)
		if sc == nil {
			continue
		}
		outer := map[*resolve.Package]bool{}
		// A tree's context, such as a fixture's tab, stands outside it and
		// includes nothing, so its imports don't count.
		for p := sc.Parent; p != nil && p != in.Tree.Context; p = p.Parent {
			for _, q := range p.Imports {
				outer[q] = true
			}
		}
		for _, q := range sc.Imports {
			if outer[q] {
				in.Report(in.Tree.Root, importSite(f), diag.Warning,
					fmt.Sprintf("%s is already imported where this file is included, so it needs no import here", q.Name))
			}
		}
	}
}

// importSite finds a file's `import = […]` member.
func importSite(f *resolve.File) resolve.Site {
	if f.Tree.Root.Kind != syntax.File {
		return resolve.Site{File: f}
	}
	for _, m := range (ast.File{N: f.Tree.Root}).Members() {
		if s, ok := m.(ast.Set); ok && resolve.PathText(s.Path().N) == "import" {
			return resolve.Site{File: f, N: s.N}
		}
	}
	return resolve.Site{File: f}
}
