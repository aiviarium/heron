package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/helightdev/specs-v0/internal/core/ast"
	"github.com/helightdev/specs-v0/internal/core/registry"
	"github.com/helightdev/specs-v0/internal/core/resolve"
	"github.com/helightdev/specs-v0/internal/core/syntax"
	"github.com/helightdev/specs-v0/internal/core/types"
)

// What crosses into foreign code besides bound funcs: op calls through the
// transport their api is bound to, calls back from foreign code
// (ctx.call), and the cache of pure funcs.

// PureCache holds results of pure foreign funcs by a hash of their inputs.
type PureCache interface {
	Get(key string) (Value, bool)
	Put(key string, v Value)
}

// MemoryCache is a PureCache that lasts as long as the machine.
type MemoryCache map[string]Value

// Get returns a cached result.
func (c MemoryCache) Get(key string) (Value, bool) { v, ok := c[key]; return v, ok }

// Put caches a result.
func (c MemoryCache) Put(key string, v Value) { c[key] = v }

// pureKey hashes a pure func's path and its arguments as plain data;
// encoding/json writes map keys sorted, so equal inputs hash alike.
func pureKey(fn string, vars map[string]Value) string {
	b, _ := json.Marshal(map[string]any{"func": fn, "args": Plain(vars)})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (m *Machine) now() time.Time {
	if m.Caps.Clock == nil {
		return time.Time{}
	}
	return m.Caps.Clock.Now()
}

func (m *Machine) isOp(n *resolve.Node) bool {
	if m.Types == nil {
		return false
	}
	return types.IsStd(m.Types.KindOf(n), "types", "types.op")
}

// callOp calls an op through the transport its bindings name. The APIs
// tab has transport packages bind ops with annotations, and the Interop
// tab has a package bind a transport (`grpc: transport = js(…)`): an op is
// carried by the transport declared in the package of an annotation on it
// or on its api. None, or more than one, is an error, never a guess.
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
	start := m.now()
	var out any
	m.foreignCall(func() { out, err = impl.Send(context.WithValue(ctx, machineKey{}, m), qualified(n), Plain(vars)) })
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
	m.event(Event{Kind: EventForeign, Name: qualified(tr) + ".send", Args: map[string]Value{"op": qualified(n), "request": vars}, Value: v, Err: err, Duration: m.now().Sub(start)})
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

// CallPath calls a func or op by its qualified path with arguments as
// plain data, and returns the result as plain data: an extension's
// ctx.call. A Heron error comes back as a *registry.Thrown. Calls from
// foreign code run one at a time, while the machine waits for that code.
func (m *Machine) CallPath(ctx context.Context, target string, args map[string]any) (any, error) {
	n := m.findPath(target)
	if n == nil {
		return nil, fmt.Errorf("no func or op %s", target)
	}
	if k := m.kindName(n); k != "func" && !m.isOp(n) && m.Registry.Func(qualified(n)) == nil {
		return nil, fmt.Errorf("%s is no func or op, so it can't be called from foreign code", target)
	}
	named := map[string]Value{}
	for k, v := range args {
		named[k] = FromPlain(v)
	}
	var v Value
	var err error
	call := func() { v, err = m.callNode(ctx, n, callArgs{named: named}, nil, nil) }
	if m.waiting == 0 {
		call() // no foreign call is waiting: the caller owns the machine
	} else {
		done := make(chan struct{})
		select {
		case m.inbox <- func() { call(); close(done) }:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		<-done
	}
	var th *Throw
	if errors.As(err, &th) {
		fields := map[string]any{}
		for k, x := range th.Value.Fields {
			fields[k] = Plain(x)
		}
		return nil, &registry.Thrown{Type: th.Value.Type, Fields: fields}
	}
	if err != nil {
		return nil, err
	}
	return Plain(v), nil
}

// foreignCall runs f, a call into foreign code, and meanwhile serves the
// calls that code makes back into the machine on this goroutine, so they
// run one at a time and nest.
func (m *Machine) foreignCall(f func()) {
	done := make(chan struct{})
	m.waiting++
	go func() { f(); close(done) }()
	for {
		select {
		case <-done:
			m.waiting--
			return
		case cb := <-m.inbox:
			cb()
		}
	}
}

// findPath finds a node by its path in the tree or one of its packages.
func (m *Machine) findPath(p string) *resolve.Node {
	if n := m.Tree.Find(p); n != nil {
		return n
	}
	for _, pk := range m.Tree.Imports {
		if pk.Tree != nil {
			if n := pk.Tree.Find(p); n != nil && n != pk.Tree.Root {
				return n
			}
		}
	}
	return nil
}
