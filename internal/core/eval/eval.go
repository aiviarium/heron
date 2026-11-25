package eval

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/helightdev/specs-v0/internal/core/ast"
	"github.com/helightdev/specs-v0/internal/core/registry"
	"github.com/helightdev/specs-v0/internal/core/resolve"
	"github.com/helightdev/specs-v0/internal/core/syntax"
	"github.com/helightdev/specs-v0/internal/core/types"
)

// Machine runs code of one checked tree.
type Machine struct {
	Tree     *resolve.Tree
	Types    *types.Checker
	Registry *registry.Registry
	Caps     Caps
	// Dir is where relative paths in run, read and write start: the
	// project's root.
	Dir string
	// OnEvent, when set, receives what the run did, in order: the run
	// record is written from these.
	OnEvent func(Event)
	// Pure caches the results of pure foreign funcs by a hash of their
	// inputs; New gives an in-memory cache, and a caller may back it with
	// files.
	Pure PureCache

	// State holds the fields code set on nodes, as in `db.ready = true`.
	State map[*resolve.Node]map[string]Value

	active  map[*resolve.Node]bool
	lets    map[*resolve.Local]Value
	values  map[*resolve.Node]Value
	foreign map[*resolve.Node]registry.FuncImpl
	emits   []*[]string
	depth   int
	tempDir string
	// evaluating counts the tree evaluations under way, such as a computed
	// value or a macro's expansion, during which only pure foreign funcs run.
	evaluating int
	// waiting counts the foreign calls the machine waits for; inbox carries
	// the calls their code makes back (ctx.call), served while it waits.
	waiting int
	inbox   chan func()
}

// New returns a machine for a checked tree.
func New(t *resolve.Tree, c *types.Checker, r *registry.Registry, caps Caps) *Machine {
	if r == nil {
		r = registry.New()
	}
	return &Machine{
		Tree: t, Types: c, Registry: r, Caps: caps,
		State:   map[*resolve.Node]map[string]Value{},
		active:  map[*resolve.Node]bool{},
		lets:    map[*resolve.Local]Value{},
		values:  map[*resolve.Node]Value{},
		foreign: map[*resolve.Node]registry.FuncImpl{},
		Pure:    MemoryCache{},
		inbox:   make(chan func()),
	}
}

// Close removes what the run made for itself, such as temp(…) files.
func (m *Machine) Close() error {
	if m.tempDir == "" || m.Caps.Files == nil {
		return nil
	}
	err := m.Caps.Files.RemoveAll(m.tempDir)
	m.tempDir = ""
	return err
}

// Event is one thing a run did.
type Event struct {
	Kind  EventKind
	Name  string // the func, task, step or field
	Args  map[string]Value
	Value Value // the result, the value set or the line logged
	Err   error
	Depth int // how many calls deep it happened
	// Duration is how long a foreign call took, by the run's clock.
	Duration time.Duration
}

// EventKind says what an event records.
type EventKind string

// The event kinds.
const (
	EventCall    EventKind = "call"    // a func, task or foreign call, after it returned
	EventStep    EventKind = "step"    // a trailing block, as in check("x") { … }, after it ran
	EventLog     EventKind = "log"     // a line of log(…)
	EventSet     EventKind = "set"     // node.field = value
	EventProcess EventKind = "process" // a process run(…) started, after it exited
	EventForeign EventKind = "foreign" // a call into foreign code, a func or a transport, after it returned
)

func (m *Machine) event(e Event) {
	e.Depth = m.depth
	if m.OnEvent != nil {
		m.OnEvent(e)
	}
}

// Args are the arguments of a call from Go: positional ones fill the
// parameters in order, named ones by name.
type Args struct {
	Positional []Value
	Named      map[string]Value
}

// Error is a fault in code, such as an unknown name or a wrong operand. It
// fails the run; unlike a Throw, attempt(…) doesn't catch it.
type Error struct {
	File string
	Line int
	Col  int
	Msg  string
}

func (e *Error) Error() string {
	if e.File == "" {
		return e.Msg
	}
	return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Col, e.Msg)
}

// Throw is an error a func or op threw, as a Heron value such as a
// ProcessError record. attempt(…) turns it into a Result.
type Throw struct {
	Value *Record
}

func (t *Throw) Error() string {
	if msg, ok := t.Value.Fields["message"].(string); ok {
		return t.Value.Type + ": " + msg
	}
	return t.Value.Type
}

// Thrown makes a throw of an error type with members.
func Thrown(typ string, fields map[string]Value) *Throw {
	return &Throw{Value: &Record{Type: typ, Fields: fields}}
}

// --- entry points ---

// Call calls a func or task with arguments and returns its result.
func (m *Machine) Call(ctx context.Context, fn *resolve.Node, args Args) (Value, error) {
	return m.callNode(ctx, fn, callArgs{pos: args.Positional, named: args.Named}, nil, nil)
}

// RunCode runs the `do` code a node holds, such as a hook's `setup do { … }`
// or the `do { … }` among a node's members, with vars visible as locals.
func (m *Machine) RunCode(ctx context.Context, n *resolve.Node, vars map[string]Value) (Value, error) {
	return m.runCode(ctx, n, vars)
}

// Expand runs a macro and returns the source it produces: its template, or
// what its `do` body emits, joined by newlines.
func (m *Machine) Expand(ctx context.Context, macro *resolve.Node, args Args) (string, error) {
	vars, err := m.bind(ctx, macro, callArgs{pos: args.Positional, named: args.Named}, nil)
	if err != nil {
		return "", err
	}
	m.evaluating++
	defer func() { m.evaluating-- }()
	if len(macro.Code) > 0 {
		var out []string
		m.emits = append(m.emits, &out)
		defer func() { m.emits = m.emits[:len(m.emits)-1] }()
		if _, err := m.runCode(ctx, macro, vars); err != nil {
			return "", err
		}
		return strings.Join(out, "\n"), nil
	}
	if macro.Value == nil {
		return "", m.errorf(macro.ValueSite, nil, "macro %s has no template", macro.Name)
	}
	v, err := m.expr(ctx, m.frameOf(macro, vars), macro.Value)
	if err != nil {
		return "", err
	}
	return Str(v), nil
}

// --- frames and locals ---

// frame is where code runs: the node it belongs to, the scope names
// resolve in, and the locals bound so far.
type frame struct {
	node  *resolve.Node
	scope *resolve.Scope
	file  *resolve.File
	vars  map[string]Value
	up    *frame // the enclosing block of the same call
}

func (f *frame) get(name string) (Value, bool) {
	for x := f; x != nil; x = x.up {
		if v, ok := x.vars[name]; ok {
			return v, true
		}
	}
	return nil, false
}

func (f *frame) child() *frame {
	return &frame{node: f.node, scope: f.scope, file: f.file, vars: map[string]Value{}, up: f}
}

func (m *Machine) frameOf(n *resolve.Node, vars map[string]Value) *frame {
	sc := n.Body
	file := n.Decl.File
	if sc == nil {
		sc = n.Decl.Scope
	}
	if vars == nil {
		vars = map[string]Value{}
	}
	return &frame{node: n, scope: sc, file: file, vars: vars}
}

func (m *Machine) errorf(s resolve.Site, at *syntax.Node, format string, args ...any) error {
	e := &Error{Msg: fmt.Sprintf(format, args...)}
	f := s.File
	if at == nil {
		at = s.N
	}
	if f != nil {
		e.File = string(f.ID)
		if at != nil {
			start, _ := at.Span()
			e.Line, e.Col = syntax.LineCol(f.Src, start)
		}
	}
	return e
}

func (m *Machine) errAt(f *frame, at *syntax.Node, format string, args ...any) error {
	return m.errorf(resolve.Site{File: f.file, N: at}, at, format, args...)
}

// --- calls ---

// callArgs are evaluated arguments.
type callArgs struct {
	pos   []Value
	named map[string]Value
}

// step is a trailing block of a call.
type step struct {
	f    *frame
	code ast.Code
}

func (m *Machine) callNode(ctx context.Context, n *resolve.Node, args callArgs, st *step, at *frame) (Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.isTypeLike(n) {
		return m.construct(n, args)
	}
	if m.isOp(n) {
		return m.callOp(ctx, n, args, at)
	}
	kind := m.kindName(n)
	key := qualified(n)
	native := natives[key]
	if native == nil && kind != "func" && kind != "task" && kind != "macro" && m.Registry.Func(key) == nil {
		where := resolve.Site{}
		if at != nil {
			where.File = at.file
		}
		return nil, m.errorf(where, nil, "%s can't be called: it is no func, task or type", n.Path())
	}
	if kind == "macro" {
		return nil, m.errorf(n.Decl, nil, "macro %s is invoked in structure, not called from code", n.Name)
	}
	if m.active[n] {
		return nil, m.errorf(n.Decl, nil, "%s calls itself; code has no recursion", n.Name)
	}
	vars, err := m.bind(ctx, n, args, at)
	if err != nil {
		return nil, err
	}
	m.active[n] = true
	m.depth++
	defer func() { delete(m.active, n); m.depth-- }()
	var v Value
	switch {
	case native != nil:
		v, err = native(ctx, m, vars, n)
	case len(n.Code) > 0:
		v, err = m.runCode(ctx, n, vars)
	case n.Value != nil:
		if impl, ok, ferr := m.foreignImpl(n); ferr != nil {
			err = ferr
		} else if ok {
			v, err = m.callImpl(ctx, n, impl, vars, st, "js")
		} else {
			v, err = m.expr(ctx, m.frameOf(n, vars), n.Value)
		}
	case m.Registry.Func(key) != nil:
		f := m.Registry.Func(key)
		v, err = m.callImpl(ctx, n, f.Call, vars, st, f.Foreign)
	default:
		err = m.errorf(n.Decl, nil, "%s has no body and no implementation is registered for %s", n.Name, key)
	}
	m.depth--
	m.event(Event{Kind: EventCall, Name: n.Path(), Args: vars, Value: v, Err: err})
	m.depth++
	return v, err
}

// callImpl calls a func implemented outside Heron. foreign names the
// process that serves it when that is foreign code, such as "js"; such a
// call is recorded with its duration, and while the tree is evaluated only
// a pure one may run, cached by a hash of its inputs.
func (m *Machine) callImpl(ctx context.Context, n *resolve.Node, impl registry.FuncImpl, vars map[string]Value, st *step, foreign string) (Value, error) {
	if foreign == "" {
		return m.callImplNow(ctx, n, impl, vars, st)
	}
	pure := types.HasKindFlag(n, "pure")
	if m.evaluating > 0 && !pure {
		return nil, m.errorf(n.Decl, nil, "%s is a foreign func that isn't <pure>, so it can't run while the tree is evaluated", n.Path())
	}
	var key string
	if pure && m.Pure != nil && st == nil {
		key = pureKey(qualified(n), vars)
		if v, ok := m.Pure.Get(key); ok {
			return v, nil
		}
	}
	start := m.now()
	var v Value
	var err error
	m.foreignCall(func() { v, err = m.callImplNow(ctx, n, impl, vars, st) })
	m.event(Event{Kind: EventForeign, Name: qualified(n), Args: vars, Value: v, Err: err, Duration: m.now().Sub(start)})
	if err == nil && key != "" {
		m.Pure.Put(key, v)
	}
	return v, err
}

func (m *Machine) callImplNow(ctx context.Context, n *resolve.Node, impl registry.FuncImpl, vars map[string]Value, st *step) (Value, error) {
	call := &registry.FuncCall{Func: qualified(n), Args: map[string]any{}, Dir: m.dirOf(n.Decl.File)}
	for k, v := range vars {
		call.Args[k] = Plain(v)
	}
	if st != nil {
		call.Step = func(ctx context.Context) error {
			_, _, err := m.block(ctx, st.f.child(), st.code)
			return err
		}
	}
	out, err := impl(context.WithValue(ctx, machineKey{}, m), call)
	var th *registry.Thrown
	if errors.As(err, &th) {
		fields := map[string]Value{}
		for k, v := range th.Fields {
			fields[k] = FromPlain(v)
		}
		return nil, Thrown(th.Type, fields)
	}
	if err != nil {
		return nil, err
	}
	return FromPlain(out), nil
}

// foreignImpl finds the implementation of a func bound with `=` to an
// export of a module node, as in `= tools.exportOrders`.
func (m *Machine) foreignImpl(n *resolve.Node) (registry.FuncImpl, bool, error) {
	if impl, ok := m.foreign[n]; ok {
		return impl, true, nil
	}
	pe, ok := n.Value.(ast.PathExpr)
	if !ok {
		return nil, false, nil
	}
	segs := resolve.PathSegs(pe.Path().N)
	if len(segs) != 2 {
		return nil, false, nil
	}
	sc := n.ValueSite.Scope
	if sc == nil {
		sc = n.Decl.Scope
	}
	b := sc.Lookup(segs[0], n.ValueSite.File, -1)
	mod := b.Node
	if mod == nil || mod.Child(segs[1]) != nil {
		return nil, false, nil
	}
	k := m.Types.KindOf(mod)
	if k == nil {
		k = m.Types.Meta(mod)
	}
	if k == nil || k.Name != "module" {
		return nil, false, nil
	}
	info := registry.Module{Path: mod.Path(), Dir: m.dirOf(mod.Decl.File)}
	if ce, ok := mod.Value.(ast.CallExpr); ok {
		if p, ok := ce.Callee().(ast.PathExpr); ok {
			info.Kind = resolve.PathText(p.Path().N)
		}
		for _, a := range ce.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				info.Spec, _ = resolve.StringValue(pa.Expr().Syntax())
				break
			}
		}
	}
	fb := m.Registry.Foreign(info.Kind)
	if fb == nil {
		return nil, false, m.errorf(n.ValueSite, nil, "%s is bound to %s.%s, but no host for %s modules is registered", n.Name, segs[0], segs[1], info.Kind)
	}
	impl, err := fb.Bind(info, segs[1])
	if err != nil {
		return nil, false, m.errorf(n.ValueSite, nil, "binding %s to %s.%s: %v", n.Name, segs[0], segs[1], err)
	}
	m.foreign[n] = impl
	return impl, true, nil
}

func (m *Machine) dirOf(f *resolve.File) string {
	if f == nil || f.ID.IsStd() {
		return m.Dir
	}
	return path.Dir(string(f.ID))
}

// bind matches arguments to a declaration's parameters: positional ones in
// order, a <variadic> one taking the rest, named ones by name, and
// defaults for the ones left out.
func (m *Machine) bind(ctx context.Context, n *resolve.Node, args callArgs, at *frame) (map[string]Value, error) {
	params := m.Types.Params(n)
	vars := map[string]Value{}
	where := n.Decl
	if at != nil {
		where = resolve.Site{File: at.file}
	}
	i := 0
	for _, v := range args.pos {
		if i >= len(params) {
			return nil, m.errorf(where, nil, "%s takes %d positional argument%s", n.Name, len(params), plural(len(params)))
		}
		p := params[i]
		if p.Variadic {
			if l, ok := v.([]Value); ok && len(args.pos) == 1 {
				vars[p.Name] = l
			} else {
				l, _ := vars[p.Name].([]Value)
				vars[p.Name] = append(l, v)
			}
			continue
		}
		vars[p.Name] = v
		i++
	}
	for k, v := range args.named {
		found := false
		for _, p := range params {
			if p.Name == k {
				found = true
			}
		}
		if !found {
			return nil, m.errorf(where, nil, "%s takes no argument %s", n.Name, k)
		}
		vars[k] = v
	}
	for _, p := range params {
		if _, ok := vars[p.Name]; ok {
			continue
		}
		if p.Variadic {
			vars[p.Name] = []Value{}
			continue
		}
		if p.N != nil {
			if d := (ast.Param{N: p.N}).Default(); d != nil {
				v, err := m.expr(ctx, m.frameOf(n, vars), d)
				if err != nil {
					return nil, err
				}
				vars[p.Name] = v
				continue
			}
		}
		if p.Optional {
			vars[p.Name] = nil
			continue
		}
		return nil, m.errorf(where, nil, "%s needs the argument %s", n.Name, p.Name)
	}
	return vars, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// qualified is the path a declaration is registered by: its path in the
// package that declares it, such as "process.run".
func qualified(n *resolve.Node) string {
	for n.Origin != nil {
		n = n.Origin
	}
	return n.Path()
}

// kindName is the name of a node's kind when the kind is one of std/core's
// code kinds: func, task or macro.
func (m *Machine) kindName(n *resolve.Node) string {
	if m.Types == nil {
		return ""
	}
	k := m.Types.KindOf(n)
	if k == nil {
		return ""
	}
	for _, name := range []string{"func", "task", "macro"} {
		if types.IsStd(k, "core", "core."+name) {
			return name
		}
	}
	return ""
}

func (m *Machine) isTypeLike(n *resolve.Node) bool {
	return resolve.IsBuiltin(n) || m.Types.IsType(n)
}

// construct makes a value of a type: a record from named arguments, or a
// type when every argument is one, as in List(Pet).
func (m *Machine) construct(n *resolve.Node, args callArgs) (Value, error) {
	allTypes := len(args.pos)+len(args.named) > 0 && len(args.named) == 0
	for _, a := range args.pos {
		if _, ok := a.(TypeValue); !ok {
			allTypes = false
		}
	}
	if allTypes {
		tv := TypeValue{Node: n}
		for _, a := range args.pos {
			tv.Args = append(tv.Args, a.(TypeValue))
		}
		return tv, nil
	}
	if len(args.pos) == 1 && len(args.named) == 0 {
		// A conversion, such as String(42) or Int("3").
		return convert(n.Name, args.pos[0])
	}
	if len(args.pos) > 0 {
		return nil, &Error{Msg: fmt.Sprintf("%s takes its members by name", n.Name)}
	}
	fields := map[string]Value{}
	for k, v := range args.named {
		fields[k] = v
	}
	return &Record{Type: n.Name, Fields: fields}, nil
}

// --- code ---

// runCode runs every `do` block a node holds, in reading order, and returns
// the value of a `return`.
func (m *Machine) runCode(ctx context.Context, n *resolve.Node, vars map[string]Value) (Value, error) {
	for _, site := range n.Code {
		var code ast.Code
		switch site.N.Kind {
		case syntax.DoBlock:
			code = ast.DoBlock{N: site.N}.Code()
		case syntax.Action:
			code = ast.Action{N: site.N}.DoBlock().Code()
		default:
			continue
		}
		f := &frame{node: n, scope: site.Scope, file: site.File, vars: map[string]Value{}}
		for k, v := range vars {
			f.vars[k] = v
		}
		v, returned, err := m.block(ctx, f, code)
		if err != nil {
			return nil, err
		}
		if returned {
			return v, nil
		}
	}
	return nil, nil
}

// block runs statements top to bottom. It returns the value of the last
// expression statement, which is what an `if` or a `for` used as an
// expression yields, and whether a `return` ended it.
func (m *Machine) block(ctx context.Context, f *frame, code ast.Code) (Value, bool, error) {
	if code.N == nil {
		return nil, false, nil
	}
	var last Value
	for _, s := range code.Stmts() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		last = nil
		switch s := s.(type) {
		case ast.LetStmt:
			id := s.Ident()
			if id == nil {
				continue
			}
			if _, dup := f.vars[id.Text]; dup {
				return nil, false, m.errAt(f, s.N, "%s is already bound in this block; locals never change", id.Text)
			}
			v, err := m.flow(ctx, f, s.Expr())
			if err != nil {
				return nil, false, err
			}
			if r, ok := v.(returned); ok {
				return r.v, true, nil
			}
			f.vars[id.Text] = v
		case ast.AssignStmt:
			if err := m.assign(ctx, f, s); err != nil {
				return nil, false, err
			}
		case ast.ReturnStmt:
			var v Value
			if e := s.Expr(); e != nil {
				var err error
				if v, err = m.flow(ctx, f, e); err != nil {
					return nil, false, err
				}
				if r, ok := v.(returned); ok {
					return r.v, true, nil
				}
			}
			return v, true, nil
		case ast.ExprStmt:
			var v Value
			var err error
			if s.Code().N != nil {
				v, err = m.stepCall(ctx, f, s.Expr(), s.Code())
			} else {
				v, err = m.flow(ctx, f, s.Expr())
			}
			if err != nil {
				return nil, false, err
			}
			if r, ok := v.(returned); ok {
				return r.v, true, nil
			}
			last = v
		case ast.CommentLine:
		case ast.ErrorNode:
			return nil, false, m.errAt(f, s.N, "this statement doesn't parse")
		default:
		}
	}
	return last, false, nil
}

// returned carries a `return` out of an `if` or `for` body.
type returned struct{ v Value }

// flow evaluates an expression that may contain blocks with `return`:
// an `if` or a `for`. A return inside comes back as a returned value.
func (m *Machine) flow(ctx context.Context, f *frame, e ast.Expr) (Value, error) {
	switch e := e.(type) {
	case ast.IfExpr:
		return m.ifExpr(ctx, f, e)
	case ast.ForExpr:
		return m.forExpr(ctx, f, e)
	default:
		return m.expr(ctx, f, e)
	}
}

func (m *Machine) ifExpr(ctx context.Context, f *frame, e ast.IfExpr) (Value, error) {
	c, err := m.expr(ctx, f, e.Cond())
	if err != nil {
		return nil, err
	}
	b, ok := c.(bool)
	if !ok {
		return nil, m.errAt(f, e.Cond().Syntax(), "if needs a Bool, got %s", TypeName(c))
	}
	var code ast.Code
	switch {
	case b:
		code = e.Then()
	case e.ElseIf().N != nil:
		return m.ifExpr(ctx, f, e.ElseIf())
	case e.Else().N != nil:
		code = e.Else()
	default:
		return nil, nil
	}
	v, ret, err := m.block(ctx, f.child(), code)
	if err != nil {
		return nil, err
	}
	if ret {
		return returned{v}, nil
	}
	return v, nil
}

func (m *Machine) forExpr(ctx context.Context, f *frame, e ast.ForExpr) (Value, error) {
	coll, err := m.expr(ctx, f, e.Expr())
	if err != nil {
		return nil, err
	}
	var items []Value
	switch c := coll.(type) {
	case []Value:
		items = c
	case map[string]Value:
		for _, k := range sortedKeys(c) {
			items = append(items, k)
		}
	case nil:
	default:
		return nil, m.errAt(f, e.Expr().Syntax(), "for iterates a list, a set or a map, not %s", TypeName(coll))
	}
	id := e.Ident()
	out := []Value{}
	for _, it := range items {
		inner := f.child()
		if id != nil {
			inner.vars[id.Text] = it
		}
		v, ret, err := m.block(ctx, inner, e.Code())
		if err != nil {
			return nil, err
		}
		if ret {
			return returned{v}, nil
		}
		out = append(out, v)
	}
	return out, nil
}

// assign runs `node.field = value`, which sets a field of a node that
// exists, such as a resource's state; it never sets a local.
func (m *Machine) assign(ctx context.Context, f *frame, s ast.AssignStmt) error {
	segs := resolve.PathSegs(s.Path().N)
	if len(segs) == 0 {
		return nil
	}
	if _, ok := f.get(segs[0]); ok {
		return m.errAt(f, s.N, "%s is a local, and locals never change; only a node's field can be set", segs[0])
	}
	if len(segs) < 2 {
		return m.errAt(f, s.N, "only a node's field can be set, as in node.%s = …", segs[0])
	}
	b := f.scope.Lookup(segs[0], f.file, off(s.N))
	n := b.Node
	for _, seg := range segs[1 : len(segs)-1] {
		if n == nil {
			break
		}
		n = n.Child(seg)
	}
	if n == nil {
		return m.errAt(f, s.N, "%s is no node", strings.Join(segs[:len(segs)-1], "."))
	}
	v, err := m.expr(ctx, f, s.Expr())
	if err != nil {
		return err
	}
	field := segs[len(segs)-1]
	if m.State[n] == nil {
		m.State[n] = map[string]Value{}
	}
	m.State[n][field] = v
	m.event(Event{Kind: EventSet, Name: n.Path() + "." + field, Value: v})
	return nil
}

func off(n *syntax.Node) int {
	s, _ := n.Span()
	return s
}

// stepCall runs a call with a trailing block, as in check("rejected") { … }:
// the block runs as a named step. A registered func receives the block as
// its Step; any other call names the step with its first argument.
func (m *Machine) stepCall(ctx context.Context, f *frame, e ast.Expr, code ast.Code) (Value, error) {
	st := &step{f: f, code: code}
	ce, isCall := e.(ast.CallExpr)
	name := ""
	if isCall {
		if p, ok := ce.Callee().(ast.PathExpr); ok {
			segs := resolve.PathSegs(p.Path().N)
			if _, local := f.get(segs[0]); !local {
				if n := m.lookupNode(f, segs, p.N); n != nil && m.Registry.Func(qualified(n)) != nil {
					args, err := m.args(ctx, f, ce.Args())
					if err != nil {
						return nil, err
					}
					return m.callNode(ctx, n, args, st, f)
				}
			}
			name = strings.Join(segs, ".")
		}
		args, err := m.args(ctx, f, ce.Args())
		if err != nil {
			return nil, err
		}
		if len(args.pos) > 0 {
			name = Str(args.pos[0])
		}
	}
	m.depth++
	v, _, err := m.block(ctx, f.child(), code)
	m.depth--
	m.event(Event{Kind: EventStep, Name: name, Value: v, Err: err})
	return v, err
}

// lookupNode resolves a path to a node in the tree, or nil.
func (m *Machine) lookupNode(f *frame, segs []string, at *syntax.Node) *resolve.Node {
	b := f.scope.Lookup(segs[0], f.file, off(at))
	n := b.Node
	for _, s := range segs[1:] {
		if n == nil {
			return nil
		}
		n = n.Child(s)
	}
	return n
}
