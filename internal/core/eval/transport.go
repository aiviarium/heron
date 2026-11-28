package eval

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

func (m *Machine) isOp(n *resolve.Node) bool {
	if m.Types == nil {
		return false
	}
	return types.IsStd(m.Types.KindOf(n), "types", "types.op")
}

// callOp calls an op through the transport its bindings name: transport
// packages bind ops with annotations, and an op is carried by the
// transport declared in the package of an annotation on it or on its api.
// None, or more than one, is an error, never a guess.
func (m *Machine) callOp(ctx context.Context, n *resolve.Node, args callArgs, at *frame) (Value, error) {
	vars, err := m.bind(ctx, n, args, at)
	if err != nil {
		return nil, err
	}
	if m.evaluating > 0 {
		return nil, m.errorf(n.Decl, nil, "op %s can't be called while the tree is evaluated", n.Path())
	}
	tr, err := m.transportOf(n)
	if err != nil {
		return nil, err
	}
	impl := m.Registry.Transport(qualified(tr))
	if impl == nil {
		return nil, m.errorf(n.Decl, nil, "op %s is carried by transport %s, but nothing implements it", n.Path(), qualified(tr))
	}
	m.depth++
	out, err := impl.Send(context.WithValue(ctx, machineKey{}, m), qualified(n), Plain(vars))
	var v Value
	var th *registry.Thrown
	switch {
	case errors.As(err, &th):
		fields := map[string]Value{}
		for k, x := range th.Fields {
			fields[k] = FromPlain(x)
		}
		err = Thrown(th.Type, fields)
	case err == nil:
		v = FromPlain(out)
	}
	m.depth--
	m.event(Event{Kind: EventCall, Name: n.Path(), Args: vars, Value: v, Err: err})
	return v, err
}

// transportOf finds the transport that carries an op's calls.
func (m *Machine) transportOf(op *resolve.Node) (*resolve.Node, error) {
	for op.Origin != nil {
		op = op.Origin
	}
	sites := append(append([]resolve.Site(nil), op.Annotations...), op.GroupAnnotations...)
	if api := op.Parent; api != nil {
		sites = append(append(sites, api.Annotations...), api.GroupAnnotations...)
	}
	var found []*resolve.Node
	var bindings []string
	seen := map[*resolve.Node]bool{}
	for _, s := range sites {
		if s.N == nil || s.N.Kind != syntax.Annotation {
			continue
		}
		a := m.annotationDecl(s)
		if a == nil {
			continue
		}
		pkg := a.Parent
		for pkg != nil && !m.Types.IsPackage(pkg) {
			pkg = pkg.Parent
		}
		if pkg == nil {
			continue
		}
		bindings = append(bindings, "@"+resolve.PathText(ast.Annotation{N: s.N}.Path().N))
		for _, c := range pkg.AllChildren() {
			if !seen[c] && types.IsStd(m.Types.KindOf(c), "apis", "apis.transport") {
				seen[c] = true
				found = append(found, c)
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		if len(bindings) == 0 {
			return nil, m.errorf(op.Decl, nil, "op %s has no binding: annotate it or its api with a transport package's annotation", op.Path())
		}
		return nil, m.errorf(op.Decl, nil, "op %s is bound by %s, but no transport of those packages is declared", op.Path(), strings.Join(bindings, ", "))
	}
	names := make([]string, len(found))
	for i, t := range found {
		names[i] = qualified(t)
	}
	sort.Strings(names)
	return nil, m.errorf(op.Decl, nil, "op %s is bound to several transports (%s); a run can't pick one", op.Path(), strings.Join(names, ", "))
}

// annotationDecl is the annotation declaration an annotation site names.
func (m *Machine) annotationDecl(s resolve.Site) *resolve.Node {
	path := ast.Annotation{N: s.N}.Path().N
	if d := m.Types.Refs[path]; d != nil {
		return d
	}
	segs := resolve.PathSegs(path)
	if s.Scope == nil || len(segs) == 0 {
		return nil
	}
	start, _ := s.N.Span()
	var out *resolve.Node
	s.Scope.Each(segs[0], s.File, start, func(b resolve.Binding) bool {
		n := b.Node
		for _, seg := range segs[1:] {
			if n == nil {
				break
			}
			n = n.Child(seg)
		}
		if n != nil && m.Types.IsAnnotation(n) {
			out = n
			return false
		}
		return true
	})
	return out
}
