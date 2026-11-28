// Package specmodel checks what std/spec's layers ask of a tree: the layer
// of each node, references that never point down, the links' targets, and
// the shapes of the kinds that need more than a schema says: rules, constraints, scenarios, personas, use cases
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
		if ch.Name == "" && (ch.Shape == resolve.ShapeAnon || ch.Shape == resolve.ShapeElement) && LayerOf(c, ch) == None {
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
	graph := map[*resolve.Node][]Ref{}
	var order []*resolve.Node
	project.Own(t, func(n *resolve.Node) bool {
		if l := LayerOf(c, n); l != None {
			order = append(order, n)
			for _, r := range OwnRefs(c, t, n) {
				if r.Target != n && LayerOf(c, r.Target) != None && !Is(c, r.Target, "spec.term", "spec.feature") {
					graph[n] = append(graph[n], r)
				}
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
	cycles(graph, order, &ds)
	return ds
}

// cycles reports each cycle of the reference graph: the references in the
// signatures and the prose of the layered nodes, leaving out the terms
// they mention, which are vocabulary. Every reference points to the same
// layer or up, so the graph is acyclic once no cycle stays in a layer.
func cycles(graph map[*resolve.Node][]Ref, order []*resolve.Node, ds *diag.List) {
	const (
		white = iota
		grey
		black
	)
	color := map[*resolve.Node]int{}
	var stack []*resolve.Node
	var visit func(n *resolve.Node)
	visit = func(n *resolve.Node) {
		color[n] = grey
		stack = append(stack, n)
		for _, r := range graph[n] {
			switch color[r.Target] {
			case white:
				visit(r.Target)
			case grey:
				var names []string
				for i := len(stack) - 1; i >= 0; i-- {
					names = append([]string{stack[i].Name}, names...)
					if stack[i] == r.Target {
						break
					}
				}
				errAt(ds, r.Site, "cycle", "%s references itself through %s", r.Target.Name, strings.Join(append(names, r.Target.Name), " → "))
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
	}
	for _, n := range order {
		if color[n] == white {
			visit(n)
		}
	}
}

func displayPath(n *resolve.Node) string {
	p := n.Path()
	if i := strings.IndexByte(p, '.'); i >= 0 && n.Decl.File != nil && !n.Decl.File.ID.IsStd() {
		// Relative to the tree's root package, as a link writes it.
		return p
	}
	return p
}

func article(w string) string {
	if w != "" && strings.ContainsRune("aeiou", rune(w[0])) {
		return "an " + w
	}
	return "a " + w
}
