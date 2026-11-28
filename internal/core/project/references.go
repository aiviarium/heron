package project

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
)

// References are a node's constructor references: the arguments its kind's
// variadic reference parameter takes, when the kind declares the kinds they
// may name with `references`. A rule's, a scenario's or a goal's
// references are what it states a fact about, its subject and the goals it
// serves among them; each one's meaning comes from the kind it names.
func References(c *types.Checker, n *resolve.Node) []AnnArg {
	if n == nil {
		return nil
	}
	k := c.KindOf(n)
	if k == nil {
		return nil
	}
	if _, ok := c.ReferenceKinds(k); !ok {
		return nil
	}
	params := c.KindParams(k, n)
	args := KindArgs(n)
	if args.N == nil {
		return nil
	}
	slots := c.ArgSlots(args, params, n.Decl.Scope, n.Decl.File)
	var out []AnnArg
	for i, a := range args.Args() {
		if slots[i] < 0 || !params[slots[i]].Variadic {
			continue
		}
		var e ast.Expr
		switch a := a.(type) {
		case ast.PositionalArg:
			e = a.Expr()
		case ast.NamedArg:
			e = a.Expr()
		}
		if e == nil {
			continue
		}
		exprs := []ast.Expr{e}
		if arr, ok := e.(ast.ArrayExpr); ok {
			exprs = arr.Exprs()
		}
		for _, x := range exprs {
			out = append(out, AnnArg{Expr: x, Site: siteOf(n, x)})
		}
	}
	return out
}

// Touches are the hard references a node writes as `@touch`, beside or
// instead of its constructor's.
func Touches(n *resolve.Node) []AnnArg { return AnnotationArgs(n, "touch") }

// HardReferences are a node's hard references: its constructor's, then
// its `@touch`'s. Coverage and the gap checks count them.
func HardReferences(c *types.Checker, n *resolve.Node) []AnnArg {
	return append(References(c, n), Touches(n)...)
}

// HardTargets are the nodes a node's hard references name.
func HardTargets(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, a := range HardReferences(c, n) {
		if t := RefTarget(c, a.Expr.Syntax(), a.Site); t != nil {
			out = append(out, t)
		}
	}
	return out
}

// SeeAlso are the nodes a node's `@see` names: its soft references, which
// coverage doesn't count.
func SeeAlso(c *types.Checker, n *resolve.Node) []*resolve.Node {
	return AnnotationRefs(c, n, "see")
}

// ReferenceTargets are the nodes a node's hard references name, of one of the
// standard kinds given, or of a kind that extends one, or every one for
// none.
func ReferenceTargets(c *types.Checker, n *resolve.Node, kinds ...string) []*resolve.Node {
	var out []*resolve.Node
	for _, a := range HardReferences(c, n) {
		t := RefTarget(c, a.Expr.Syntax(), a.Site)
		if t == nil {
			continue
		}
		if len(kinds) == 0 || isKind(c, t, kinds...) {
			out = append(out, t)
		}
	}
	return out
}

// Subject is the actor, persona or part a rule's references name, whose
// behaviour it states, or nil for a rule that binds the system.
func Subject(c *types.Checker, n *resolve.Node) *resolve.Node {
	if s := ReferenceTargets(c, n, "spec.actor", "spec.persona", "spec.part"); len(s) > 0 {
		return s[0]
	}
	return nil
}

// AskedBy are the actors and personas a goal's references
// name: who asked for it.
func AskedBy(c *types.Checker, n *resolve.Node) []*resolve.Node {
	return ReferenceTargets(c, n, "spec.actor", "spec.persona")
}

// Served are the goals and qualities a node's references name.
func Served(c *types.Checker, n *resolve.Node) []*resolve.Node {
	return ReferenceTargets(c, n, "spec.goal", "spec.quality")
}

// ReliedOn are the assumptions a node's references name.
func ReliedOn(c *types.Checker, n *resolve.Node) []*resolve.Node {
	return ReferenceTargets(c, n, "spec.assumption")
}

// Described is what a scenario's or an example's references name that it is
// a case of: the rule, use case or step an outcome scenario describes, or
// the scenario an example pins down. Nil for none.
func Described(c *types.Checker, n *resolve.Node) *resolve.Node {
	for _, t := range ReferenceTargets(c, n) {
		if t.Shape == resolve.ShapeStep || isKind(c, t, "spec.rule", "spec.usecase", "spec.scenario") {
			return t
		}
	}
	return nil
}

// Rationale is a rule's rationale node, or nil.
func Rationale(c *types.Checker, n *resolve.Node) *resolve.Node {
	for _, ch := range n.Children {
		if KindPath(c, ch) == "spec.rationale" {
			return ch
		}
	}
	return nil
}

func isKind(c *types.Checker, n *resolve.Node, kinds ...string) bool {
	p := KindPath(c, n)
	for _, k := range kinds {
		if p == k {
			return true
		}
	}
	return false
}

// IsContract reports whether a node's kind, or a kind it extends, is in
// the CONTRACT layer: a surface entry, whose hard references are what it
// carries out.
func IsContract(c *types.Checker, n *resolve.Node) bool {
	for k, i := c.KindOf(n), 0; k != nil && i < 8; k, i = c.Extends(k), i+1 {
		if e := types.DeclNamedArg(k, "layer"); e != nil {
			return strings.HasSuffix(strings.TrimSpace(e.Syntax().Text()), "CONTRACT")
		}
	}
	return false
}

// Carried are the nodes a surface entry's hard references name, what it
// carries out, or nil for a node outside the CONTRACT layer.
func Carried(c *types.Checker, n *resolve.Node) []*resolve.Node {
	if !IsContract(c, n) {
		return nil
	}
	return HardTargets(c, n)
}

// KindArg is the expression a node's kind argument named name is given,
// by name or by position as the checker binds it, or nil. A list a
// variadic parameter takes is its first value.
func KindArg(c *types.Checker, n *resolve.Node, name string) ast.Expr {
	k := c.KindOf(n)
	if k == nil {
		return nil
	}
	params := c.KindParams(k, n)
	args := KindArgs(n)
	if args.N == nil {
		return nil
	}
	slots := c.ArgSlots(args, params, n.Decl.Scope, n.Decl.File)
	for i, a := range args.Args() {
		if slots[i] < 0 || params[slots[i]].Name != name {
			continue
		}
		switch a := a.(type) {
		case ast.PositionalArg:
			return a.Expr()
		case ast.NamedArg:
			return a.Expr()
		}
	}
	return nil
}
