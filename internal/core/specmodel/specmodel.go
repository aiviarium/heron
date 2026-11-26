// Package specmodel checks what std/spec's layers ask of a tree: the layer
// of each node, references that never point down, the links' targets, the
// retired kinds and packages, and the shapes of the kinds that need more
// than a schema says: rules, constraints, scenarios, personas, use cases
// and errors.
package specmodel

import (
	"fmt"
	"os"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// Layers, from the top. None is outside the layer checks.
const (
	None = iota
	Intent
	Specification
	Contract
)

// LayerNames are the layers as std/schema's Layer names them.
var LayerNames = []string{"", "INTENT", "SPECIFICATION", "CONTRACT"}

// KindLayer is the layer a kind declares with `layer`, or else the layer of
// the kind it extends.
func KindLayer(c *types.Checker, k *resolve.Node) int {
	for i := 0; k != nil && i < 8; i++ {
		if e := types.DeclNamedArg(k, "layer"); e != nil {
			txt := strings.TrimSpace(e.Syntax().Text())
			for l := Intent; l <= Contract; l++ {
				if strings.HasSuffix(txt, LayerNames[l]) {
					return l
				}
			}
			return None
		}
		k = c.Extends(k)
	}
	return None
}

// LayerOf is the layer of a node's kind.
func LayerOf(c *types.Checker, n *resolve.Node) int {
	if n == nil {
		return None
	}
	return KindLayer(c, c.KindOf(n))
}

// Ref is one layer reference a node makes in its own text or arguments.
type Ref struct {
	Site   resolve.Site
	Target *resolve.Node
}

// OwnRefs are the references a node makes itself: in its kind arguments
// and annotations, its prose and the values of the fields it sets, leaving
// out what its children declare. Inserts aren't references here.
func OwnRefs(c *types.Checker, t *resolve.Tree, n *resolve.Node) []Ref {
	var out []Ref
	seen := map[*syntax.Node]bool{}
	add := func(f *resolve.File, sc *resolve.Scope, root *syntax.Node, skipRoot bool) {
		if root == nil {
			return
		}
		root.Walk(func(x *syntax.Node) bool {
			if x != root || skipRoot {
				if x.Kind == syntax.Tag && t.ElementNode(x) != nil && x != root {
					return false // a node of its own
				}
				if x.Kind == syntax.Insert || x.Kind == syntax.Declare || x.Kind == syntax.Value || x.Kind == syntax.Invoke {
					if x != root {
						return false
					}
				}
			}
			if x.Kind == syntax.Path || x.Kind == syntax.StringLit {
				if target := c.Refs[x]; target != nil && !seen[x] {
					seen[x] = true
					out = append(out, Ref{Site: resolve.Site{File: f, N: x, Scope: sc}, Target: target})
				}
			}
			return true
		})
	}
	// The kind slot's arguments.
	if n.Decl.N != nil {
		switch n.Decl.N.Kind {
		case syntax.Tag:
			for _, a := range (ast.Tag{N: n.Decl.N}).TagAttrs() {
				add(n.Decl.File, n.Decl.Scope, a.N, false)
			}
			for _, x := range n.ElementArgs.Args() {
				if na, ok := x.(ast.NamedArg); ok && na.Expr() != nil {
					add(n.Decl.File, n.Decl.Scope, na.Expr().Syntax(), false)
				} else if pa, ok := x.(ast.PositionalArg); ok && pa.Expr() != nil {
					add(n.Decl.File, n.Decl.Scope, pa.Expr().Syntax(), false)
				}
			}
		default:
			for _, a := range types.DeclArgs(n) {
				add(n.Decl.File, n.Decl.Scope, a.Syntax(), false)
			}
		}
	}
	for _, a := range n.Annotations {
		if an := (ast.Annotation{N: a.N}); an.N != nil {
			if p := an.Path().N; p != nil {
				if segs := resolve.PathSegs(p); len(segs) == 1 && segs[0] == "realizes" {
					continue // a link up, checked by its own rule
				}
			}
		}
		add(a.File, a.Scope, a.N, false)
	}
	for _, p := range n.Prose {
		add(p.File, p.Scope, p.N, true)
	}
	for _, d := range n.Docs {
		add(d.File, d.Scope, d.N, true)
	}
	for _, ch := range n.Children {
		if (ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue) && ch.Value != nil && c.KindOf(ch) == nil {
			add(ch.ValueSite.File, ch.ValueSite.Scope, ch.Value.Syntax(), false)
		}
		// A rule's strength and conditions are the rule's own text.
		if ch.Name == "" && ch.Shape == resolve.ShapeAnon && LayerOf(c, ch) == None {
			for _, r := range OwnRefs(c, t, ch) {
				if !seen[r.Site.N] {
					seen[r.Site.N] = true
					out = append(out, r)
				}
			}
		}
	}
	return out
}

func errAt(ds *diag.List, s resolve.Site, code, format string, args ...any) {
	start, end := s.Span()
	file := ""
	if s.File != nil {
		file = string(s.File.ID)
	}
	ds.Errorf(file, start, end, code, format, args...)
}

func warnAt(ds *diag.List, s resolve.Site, code, format string, args ...any) {
	start, end := s.Span()
	file := ""
	if s.File != nil {
		file = string(s.File.ID)
	}
	*ds = append(*ds, diag.Diagnostic{File: file, Start: start, End: end, Severity: diag.Warning, Code: code, Message: fmt.Sprintf(format, args...)})
}

// KindName is how a message names a node's kind.
func KindName(c *types.Checker, n *resolve.Node) string {
	if k := c.KindOf(n); k != nil {
		return k.Name
	}
	if c.TypeOf(n) != nil || c.IsType(n) {
		return "type"
	}
	return "node"
}

// Check reports what the layers and the spec kinds ask of a tree's own
// nodes.
func Check(t *resolve.Tree, c *types.Checker) diag.List {
	var ds diag.List
	project.Own(t, func(n *resolve.Node) bool {
		retired(c, n, &ds)
		if l := LayerOf(c, n); l != None {
			for _, r := range OwnRefs(c, t, n) {
				tl := LayerOf(c, r.Target)
				if tl > l {
					if os.Getenv("HERON_LAYER_DEBUG") != "" {
						ts, _ := r.Target.Decl.Span()
						fmt.Fprintf(os.Stderr, "LAYER\t%s\t%d\t%s\t%s\t%d\t%s\t%s\t%s\n", r.Site.File.ID, func() int { a, _ := r.Site.Span(); return a }(), n.Path(), r.Target.Decl.File.ID, ts, r.Target.Path(), project.KindPath(c, n), project.KindPath(c, r.Target))
					}
					errAt(&ds, r.Site, "layer", "%s is a %s in the %s layer, and %s node can't reference a lower layer; link it from the lower node instead",
						displayPath(r.Target), KindName(c, r.Target), strings.ToLower(LayerNames[tl]), article(strings.ToLower(LayerNames[l])))
				}
			}
		}
		shapes(t, c, n, &ds)
		return true
	})
	retiredImports(t, c, &ds)
	return ds
}

func displayPath(n *resolve.Node) string {
	p := n.Path()
	if i := strings.IndexByte(p, '.'); i >= 0 && n.Decl.File != nil && !n.Decl.File.ID.IsStd() {
		// Relative to the tree's root package, as a link writes it.
		return p
	}
	return p
}

// retiredUse is the advice of a retired kind's @retired, or "".
func RetiredUse(c *types.Checker, k *resolve.Node) string {
	if k == nil {
		return ""
	}
	for _, a := range k.Annotations {
		an := ast.Annotation{N: a.N}
		if an.N == nil || an.Path().N == nil {
			continue
		}
		if segs := resolve.PathSegs(an.Path().N); len(segs) == 0 || segs[len(segs)-1] != "retired" {
			continue
		}
		for _, x := range an.Args().Args() {
			var e ast.Expr
			switch x := x.(type) {
			case ast.PositionalArg:
				e = x.Expr()
			case ast.NamedArg:
				e = x.Expr()
			}
			if e != nil {
				if s, ok := resolve.StringValue(e.Syntax()); ok {
					return s
				}
			}
		}
		return "it has no replacement"
	}
	return ""
}

func retired(c *types.Checker, n *resolve.Node, ds *diag.List) {
	k := c.KindOf(n)
	use := RetiredUse(c, k)
	if use == "" {
		return
	}
	at := n.Decl
	p := kindPathNode(n)
	if p != nil {
		at = resolve.Site{File: n.Decl.File, N: p, Scope: n.Decl.Scope}
	}
	errAt(ds, at, "retired", "%s is retired; %s", k.Name, use)
	// The mechanical migrations rewrite the kind's name.
	to := map[string]string{"invariant": "rule", "obligation": "rule", "component": "part", "criterion": "scenario", "outOfScope": "nongoal"}[k.Name]
	if to == "" || p == nil || at.File == nil || (k.Name == "obligation" || k.Name == "component") && len(project.KindArgs(n).Args()) > 0 {
		return
	}
	s, e := p.Span()
	d := &(*ds)[len(*ds)-1]
	d.Fixes = []diag.Fix{{Title: "Write " + to + " instead of " + k.Name, Edits: []diag.Edit{{File: string(at.File.ID), Start: s, End: e, New: to}}}}
}

// kindPathNode is the syntax of a node's kind name, where it has one.
func kindPathNode(n *resolve.Node) *syntax.Node {
	if n.Decl.N == nil {
		return nil
	}
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if nt, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType); ok {
			return nt.Path().N
		}
	case syntax.Value:
		if nt, ok := (ast.Value{N: n.Decl.N}).Type().(ast.NamedType); ok {
			return nt.Path().N
		}
	case syntax.Invoke:
		return ast.Invoke{N: n.Decl.N}.Path().N
	case syntax.Tag:
		return ast.Tag{N: n.Decl.N}.Path().N
	}
	return nil
}

func retiredImports(t *resolve.Tree, c *types.Checker, ds *diag.List) {
	for _, p := range t.Imports {
		site, ok := t.ImportSites[p]
		if p.Node == nil || !ok || site.N == nil {
			continue
		}
		if use := RetiredUse(c, p.Node); use != "" {
			errAt(ds, site, "retired", "%s is merged into std/spec; %s", p.Name, use)
			s, e := site.Span()
			d := &(*ds)[len(*ds)-1]
			d.Fixes = []diag.Fix{{Title: "Import std/spec@1", Edits: []diag.Edit{{File: string(site.File.ID), Start: s, End: e, New: `"std/spec@1"`}}}}
		}
	}
}

func article(w string) string {
	if w != "" && strings.ContainsRune("aeiou", rune(w[0])) {
		return "an " + w
	}
	return "a " + w
}
