package eval

import (
	"context"

	"github.com/helightdev/specs-v0/internal/core/ast"
	"github.com/helightdev/specs-v0/internal/core/resolve"
	"github.com/helightdev/specs-v0/internal/core/syntax"
)

// What Go implementations of funcs reach the running machine through, such
// as the setup funcs of std/repo, which read the tree they run in.

type machineKey struct{}

// MachineOf returns the machine running the func a registry.FuncImpl was
// called for, or nil when ctx didn't come from a machine.
func MachineOf(ctx context.Context) *Machine {
	m, _ := ctx.Value(machineKey{}).(*Machine)
	return m
}

// Value returns a node's value: a field's or a binding's value evaluated
// once, an enum variant's name, or a Ref for any other node.
func (m *Machine) Value(ctx context.Context, n *resolve.Node) (Value, error) {
	return m.nodeValue(ctx, n)
}

// KindArg evaluates a named argument of a node's kind, as `path` in
// `mailer: project(path = "vendor/mailer")`; ok is false when the node
// doesn't set it.
func (m *Machine) KindArg(ctx context.Context, n *resolve.Node, name string) (v Value, ok bool, err error) {
	if n == nil || n.Decl.N == nil {
		return nil, false, nil
	}
	var args []ast.Arg
	var tt ast.Type
	switch n.Decl.N.Kind {
	case syntax.Declare:
		tt = ast.Declare{N: n.Decl.N}.Type()
	case syntax.Value:
		tt = ast.Value{N: n.Decl.N}.Type()
	case syntax.Invoke:
		args = ast.Invoke{N: n.Decl.N}.Args().Args()
	}
	if nt, isNamed := tt.(ast.NamedType); isNamed {
		args = nt.Args().Args()
	}
	for _, a := range args {
		na, isNamed := a.(ast.NamedArg)
		if !isNamed || na.Ident() == nil || na.Ident().Text != name {
			continue
		}
		f := &frame{node: n, scope: n.Decl.Scope, file: n.Decl.File, vars: map[string]Value{}}
		v, err := m.expr(ctx, f, na.Expr())
		return v, true, err
	}
	return nil, false, nil
}

// Report adds an event to the run record, for what a Go implementation did,
// such as a process it started.
func (m *Machine) Report(e Event) { m.event(e) }
