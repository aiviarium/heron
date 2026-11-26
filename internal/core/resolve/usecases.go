package resolve

import (
	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/usecase"
)

// useCaseSteps gives each use case a child per step and extension of its
// body, named by its number, so `request_refund.3` and `request_refund.2a`
// resolve like any path.
func useCaseSteps(t *Tree) {
	t.Walk(func(n *Node) {
		if KindName(n) != "usecase" {
			return
		}
		for _, p := range n.Prose {
			if p.N == nil || p.File == nil {
				continue
			}
			s, e := p.N.Span()
			uc := usecase.Parse(p.File.Src[s:e], s)
			add := func(name string, start, end int) {
				if n.byName != nil && n.byName[name] != nil {
					return
				}
				n.add(&Node{Name: name, Shape: ShapeStep, Decl: Site{File: p.File, N: p.N, Scope: p.Scope}})
			}
			for _, st := range uc.Steps {
				add(st.Num, st.Start, st.End)
			}
			for _, x := range uc.Exts {
				add(x.Num, x.Start, x.End)
			}
		}
	})
}

// KindName is the last segment of the kind a node's declaration names, as
// written, or "".
func KindName(n *Node) string {
	if n.Decl.N == nil {
		return ""
	}
	var p *syntax.Node
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if nt, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType); ok {
			p = nt.Path().N
		}
	case syntax.Value:
		if nt, ok := (ast.Value{N: n.Decl.N}).Type().(ast.NamedType); ok {
			p = nt.Path().N
		}
	case syntax.Invoke:
		p = ast.Invoke{N: n.Decl.N}.Path().N
	case syntax.Tag:
		p = ast.Tag{N: n.Decl.N}.Path().N
	}
	if p == nil {
		return ""
	}
	segs := PathSegs(p)
	if len(segs) == 0 {
		return ""
	}
	return segs[len(segs)-1]
}
