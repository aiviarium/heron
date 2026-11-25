package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/extension/protocol"
	"github.com/aiviarium/heron/internal/core/lint"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
)

// Manager supervises the processes a tree's declarations need and
// registers what they implement in the registry.
type Manager struct {
	Registry *registry.Registry
	// Log receives extensions' stderr and logs; Warn receives warnings.
	Log  io.Writer
	Warn func(string)
	Wire protocol.Wire
	// Approve is asked before an extension process first runs on this
	// machine; trusted extensions are remembered under .heron/.
	Approve func(s *Spec) (bool, error)
	// NoSandbox runs extensions without namespaces.
	NoSandbox bool
	// TempRoot is where run-scoped files go.
	TempRoot string
	// Call serves ctx.call outside a run (see registry.Run) for targets
	// the registry doesn't implement.
	Call func(ctx context.Context, target string, args map[string]any) (any, error)

	mu      sync.Mutex
	root    string
	tree    *resolve.Tree
	checker *types.Checker
	procs   map[string]*Process
	specs   map[string]*Spec
	session *registry.Run // resource instances used outside a heron run
	diags   diag.List
	lints   map[string]bool
}

// NewManager returns a manager for a registry.
func NewManager(r *registry.Registry) *Manager {
	return &Manager{
		Registry: r, Log: os.Stderr, Warn: func(s string) { fmt.Fprintln(os.Stderr, "heron: warning:", s) },
		procs: map[string]*Process{}, specs: map[string]*Spec{}, lints: map[string]bool{},
	}
}

// Register installs the extension host in a registry: the loader that
// registers what a checked tree's extensions implement, the binder of js(…)
// modules, the lint that reports handshake mismatches, and shutdown.
func Register(r *registry.Registry) *Manager {
	m := NewManager(r)
	m.Approve = askOnTerminal
	r.AddLoader(registry.Loader{Name: "extensions", Load: func(ctx context.Context, res any) error {
		cr, ok := res.(*check.Result)
		if !ok {
			return fmt.Errorf("extension: LoadTree wants a *check.Result")
		}
		return m.Load(ctx, cr)
	}})
	r.MustAddForeign(registry.Foreign{Kind: "js", Bind: m.bindJS})
	r.MustAddLint(registry.Lint{Name: "extension", Summary: "a declaration its extension doesn't implement", Run: func(in any) {
		m.reportMismatches(in.(*lint.Input))
	}})
	r.OnClose(m.Close)
	return m
}

// Load discovers what a checked tree declares and registers each part.
// Processes start on first use.
func (m *Manager) Load(ctx context.Context, res *check.Result) error {
	d := Discover(res.Tree, res.Checker)
	m.mu.Lock()
	m.tree, m.checker = res.Tree, res.Checker
	if res.Tree.Entry != nil {
		m.root = res.Tree.Entry.ID.Dir()
	}
	if m.TempRoot == "" {
		m.TempRoot = filepath.Join(os.TempDir(), "heron-runs")
	}
	m.diags = append(m.diags[:0], d.Diags...)
	for _, s := range d.Specs {
		if old := m.specs[s.Name]; old != nil && sameDecls(old, s) {
			continue
		}
		if p := m.procs[s.Name]; p != nil {
			go p.Shutdown()
			delete(m.procs, s.Name)
		}
		m.specs[s.Name] = s
	}
	specs := append([]*Spec(nil), d.Specs...)
	m.mu.Unlock()
	for _, s := range specs {
		for _, dc := range s.Decls {
			if err := m.register(s, dc); err != nil {
				return err
			}
		}
	}
	return nil
}

func sameDecls(a, b *Spec) bool {
	if len(a.Decls) != len(b.Decls) || strings.Join(a.Command, "\x00") != strings.Join(b.Command, "\x00") {
		return false
	}
	for i := range a.Decls {
		if a.Decls[i].Path != b.Decls[i].Path || a.Decls[i].Module != b.Decls[i].Module || a.Decls[i].Export != b.Decls[i].Export {
			return false
		}
	}
	return true
}

func (m *Manager) register(s *Spec, d Decl) error {
	name, path := s.Name, d.Path
	call := func(ctx context.Context, method string, params, result any) error {
		p, err := m.process(name)
		if err != nil {
			return err
		}
		return p.Call(withRunID(ctx), method, params, result)
	}
	switch d.Point {
	case protocol.PointFunc:
		// js(…) bindings reach the interpreter through the "js" binder;
		// @extern funcs have no body, so the interpreter looks them up here.
		if d.Module == "" && m.Registry.Func(path) == nil {
			return m.Registry.AddFunc(registry.Func{Name: path, Summary: "implemented by " + name, Foreign: name, Call: func(ctx context.Context, c *registry.FuncCall) (any, error) {
				return m.callFunc(ctx, name, path, c.Args)
			}})
		}
	case protocol.PointMacro:
		return m.Registry.SetMacro(registry.Macro{Path: path, Expand: func(ctx context.Context, args map[string]any) (string, error) {
			var out string
			err := call(ctx, protocol.MethodCall, &protocol.CallParams{Target: path, Args: args}, &out)
			return out, err
		}})
	case protocol.PointTransport:
		return m.Registry.SetTransport(registry.Transport{Path: path, Send: func(ctx context.Context, op string, request any) (any, error) {
			var out any
			err := call(ctx, protocol.MethodCall, &protocol.CallParams{Target: path, Member: "send", Args: map[string]any{"op": protocol.Ref(op), "request": request}}, &out)
			return plain(out), thrown(err)
		}})
	case protocol.PointLint:
		m.mu.Lock()
		m.lints[path] = true
		m.mu.Unlock()
		return m.Registry.SetLint(registry.Lint{Name: path, Summary: "implemented by " + name, Run: func(in any) {
			m.runLint(in.(*lint.Input), name, d)
		}})
	case protocol.PointResource:
		life := func(method string) func(ctx context.Context, handle any) (any, error) {
			return func(ctx context.Context, handle any) (any, error) {
				var out protocol.ResourceResult
				err := call(ctx, method, &protocol.ResourceParams{Resource: path, Handle: handle}, &out)
				if out.Snapshot != nil {
					return out.Snapshot, err
				}
				return out.Handle, err
			}
		}
		drop := func(method string) func(ctx context.Context, handle any) error {
			f := life(method)
			return func(ctx context.Context, h any) error { _, err := f(ctx, h); return err }
		}
		return m.Registry.SetResourceDriver(registry.ResourceDriver{
			Path:     path,
			Setup:    func(ctx context.Context) (any, error) { return life(protocol.MethodResourceSetup)(ctx, nil) },
			Reset:    drop(protocol.MethodResourceReset),
			Snapshot: life(protocol.MethodResourceSnapshot),
			Restore: func(ctx context.Context, h, snap any) error {
				return call(ctx, protocol.MethodResourceRestore, &protocol.ResourceParams{Resource: path, Handle: h, Snapshot: snap}, nil)
			},
			Fork:     life(protocol.MethodResourceFork),
			Teardown: drop(protocol.MethodResourceTeardown),
		})
	case protocol.PointRenderer:
		module := d.Module
		m.Registry.SetRendererSource(registry.RendererSource{Name: path, Load: func(ctx context.Context) ([]registry.Renderer, error) {
			p, err := m.process(name)
			if err != nil {
				return nil, err
			}
			if _, err := p.Start(ctx); err != nil {
				return nil, err
			}
			var out []registry.Renderer
			for _, im := range p.Implements() {
				if im.Path != path {
					continue
				}
				for _, kind := range im.Kinds {
					kind := kind
					out = append(out, registry.Renderer{Kind: kind, Plain: func(ctx context.Context, in *registry.RenderInput) (*registry.RenderOutput, error) {
						return m.render(ctx, name, path, kind, in)
					}})
				}
			}
			if len(out) == 0 {
				m.warn(fmt.Sprintf("%s renders no kind: export renderers made with defineRenderer", module))
			}
			return out, nil
		}})
	case protocol.PointGenerator:
		target := d.Target
		return m.Registry.SetGenerator(registry.Generator{Path: path, Target: target, Generate: func(ctx context.Context, in any) ([]registry.GeneratedFile, error) {
			src, ok := in.(interface {
				ResolvedTree() (*resolve.Tree, *types.Checker)
			})
			if !ok {
				return nil, fmt.Errorf("%s: the generator input has no tree", path)
			}
			t, c := src.ResolvedTree()
			var out protocol.GenerateResult
			if err := call(ctx, protocol.MethodGenerate, &protocol.GenerateParams{Generator: path, Target: target, Tree: *EncodeNode(c, t.Root, -1)}, &out); err != nil {
				return nil, err
			}
			var files []registry.GeneratedFile
			for _, f := range out.Files {
				files = append(files, registry.GeneratedFile{Path: f.Path, Content: []byte(f.Content), Ignored: f.Ignored != nil && *f.Ignored})
			}
			return files, nil
		}})
	}
	return nil
}

// render calls a renderer module's renderer for a kind.
func (m *Manager) render(ctx context.Context, spec, path, kind string, in *registry.RenderInput) (*registry.RenderOutput, error) {
	p, err := m.process(spec)
	if err != nil {
		return nil, err
	}
	params := &protocol.RenderParams{
		Renderer: path, Kind: kind, Path: in.Path, Name: in.Name, Title: in.Title, Anchor: in.Anchor,
		Prose: in.Prose, Children: in.Children, Body: in.Body, Slots: in.Slots,
	}
	if len(in.Attrs) > 0 {
		params.Attrs = map[string]protocol.Value{}
		for k, v := range in.Attrs {
			params.Attrs[k] = v
		}
	}
	if in.Level > 0 {
		l := int64(in.Level)
		params.Level = &l
	}
	var out protocol.RenderResult
	if err := p.Call(withRunID(ctx), protocol.MethodRender, params, &out); err != nil {
		return nil, err
	}
	return &registry.RenderOutput{HTML: out.Html, Block: out.Block != nil && *out.Block, Islands: out.Islands}, nil
}

// thrown turns a declared error from the foreign side into a Heron throw.
func thrown(err error) error {
	var e *protocol.Error
	if errors.As(err, &e) {
		if e.Code != protocol.CodeThrown {
			e = &protocol.Error{Code: e.Code, Message: e.Message, Type: "ForeignError", Data: map[string]any{"message": e.Message, "code": e.Code}}
		}
		fields, _ := e.Data.(map[string]any)
		if fields == nil {
			fields = map[string]any{}
		}
		if _, ok := fields["message"]; !ok {
			fields["message"] = e.Message
		}
		t := e.Type
		if t == "" {
			t = "ForeignError"
		}
		return &registry.Thrown{Type: t, Fields: fields}
	}
	return err
}

func (m *Manager) callFunc(ctx context.Context, spec, path string, args map[string]any) (any, error) {
	p, err := m.process(spec)
	if err != nil {
		return nil, thrown(err)
	}
	var out any
	err = p.Call(withRunID(ctx), protocol.MethodCall, &protocol.CallParams{Target: path, Args: args}, &out)
	return plain(out), thrown(err)
}

// withRunID makes the calls a run sends carry its id, so what the
// extension asks back (ctx.call, ctx.resource) lands in the same run.
func withRunID(ctx context.Context) context.Context {
	if run := registry.RunOf(ctx); run != nil && protocol.RunOf(ctx) == "" {
		return protocol.WithRun(ctx, run.ID)
	}
	return ctx
}

// runOf is the run a call from an extension belongs to, or nil.
func (m *Manager) runOf(ctx context.Context) *registry.Run {
	if run := registry.RunOf(ctx); run != nil {
		return run
	}
	return m.Registry.RunByID(protocol.RunOf(ctx))
}

// plain turns protocol values into the plain data the interpreter takes.
func plain(v any) any {
	switch v := v.(type) {
	case uint64:
		return int64(v)
	case []any:
		for i := range v {
			v[i] = plain(v[i])
		}
		return v
	case map[string]any:
		for k := range v {
			v[k] = plain(v[k])
		}
		return v
	case protocol.Set:
		return plain([]any(v))
	case protocol.Ref:
		return string(v)
	case protocol.Duration:
		return int64(v)
	case protocol.Decimal:
		return v.String()
	case protocol.Variant:
		return map[string]any{"type": v.Name, "value": plain(v.Value)}
	case protocol.Prose:
		return map[string]any(v)
	}
	return v
}

// bindJS binds a func declared `= module.export` for the interpreter. The
// declaration normally came from Load; a tree run without it is bound here.
func (m *Manager) bindJS(mod registry.Module, export string) (registry.FuncImpl, error) {
	return func(ctx context.Context, c *registry.FuncCall) (any, error) {
		m.ensureJSDecl(c.Func, mod, export)
		return m.callFunc(ctx, JSHost, c.Func, c.Args)
	}, nil
}

func (m *Manager) ensureJSDecl(path string, mod registry.Module, export string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.specs[JSHost]
	if s == nil {
		s = &Spec{Name: JSHost}
		m.specs[JSHost] = s
	}
	for _, d := range s.Decls {
		if d.Path == path {
			return
		}
	}
	spec := mod.Spec
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		spec = filepath.Join(mod.Dir, spec)
	}
	ns := *s
	ns.Decls = append(append([]Decl(nil), s.Decls...), Decl{Declaration: protocol.Declaration{Path: path, Point: protocol.PointFunc, Module: spec, Export: export}})
	m.specs[JSHost] = &ns
	if m.root == "" {
		m.root = mod.Dir
	}
	if p := m.procs[JSHost]; p != nil {
		go p.Shutdown()
		delete(m.procs, JSHost)
	}
}

// process returns the running process of a spec, making it on first use.
func (m *Manager) process(name string) (*Process, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.procs[name]; p != nil {
		return p, nil
	}
	s := m.specs[name]
	if s == nil {
		return nil, ForeignError("%s: no such extension", name)
	}
	p := &Process{
		Spec: s, Wire: m.Wire, Log: m.Log, Warn: m.Warn, Host: hostServices{m},
		Limits: protocol.DefaultLimits(),
		Init: protocol.InitializeParams{
			Protocol: protocol.Version, Host: protocol.PeerInfo{Name: "heron", Version: "0.1.0"},
			Root: m.root, Temp: m.TempRoot,
			Capabilities: protocol.Capabilities{Methods: []string{protocol.MethodNode, protocol.MethodCall, protocol.MethodResource, protocol.MethodSecret, protocol.MethodTemp}, Streaming: ptr(true), Cancellation: ptr(true)},
		},
	}
	for _, d := range s.Decls {
		p.Init.Declarations = append(p.Init.Declarations, d.Declaration)
	}
	if name == JSHost {
		argv, err := m.jsHostCommand(s)
		if err != nil {
			return nil, err
		}
		p.Argv = argv
		if s.Dir == "" {
			s.Dir = m.root
		}
	} else {
		if err := m.approve(s); err != nil {
			return nil, err
		}
		argv, err := resolveCommand(s)
		if err != nil {
			return nil, err
		}
		p.Argv = argv
		p.Sandbox = !m.NoSandbox
		p.Limits.Files, p.Limits.Network, p.Limits.Commands = s.Files, s.Network, s.Commands
		if len(s.Network) > 0 && p.Sandbox {
			m.warn(fmt.Sprintf("%s: it may reach any host: per-host network limits (%s) aren't enforced yet", s.Name, strings.Join(s.Network, ", ")))
		}
		if p.Sandbox {
			bin, err := commandDir(s)
			if err != nil {
				return nil, err
			}
			p.Env = append(p.Env, "PATH="+bin)
		}
	}
	m.procs[name] = p
	return p, nil
}

func (m *Manager) warn(s string) {
	if m.Warn != nil {
		m.Warn(s)
	}
}

func resolveCommand(s *Spec) ([]string, error) {
	if len(s.Command) == 0 {
		return nil, ForeignError("%s: the extension has no command", s.Name)
	}
	argv := append([]string(nil), s.Command...)
	switch {
	case filepath.IsAbs(argv[0]):
	case strings.ContainsRune(argv[0], '/'):
		argv[0] = filepath.Join(s.Dir, argv[0])
	default:
		p, err := exec.LookPath(argv[0])
		if err != nil {
			return nil, ForeignError("%s: %v", s.Name, err)
		}
		argv[0] = p
	}
	return argv, nil
}

// commandDir makes a directory holding only the commands an extension
// declared, as its PATH.
func commandDir(s *Spec) (string, error) {
	dir, err := os.MkdirTemp("", "heron-ext-bin-")
	if err != nil {
		return "", err
	}
	for _, c := range s.Commands {
		p, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		_ = os.Symlink(p, filepath.Join(dir, filepath.Base(c)))
	}
	return dir, nil
}

// jsHostCommand finds @heron/runtime's host: HERON_JS_HOST, or
// node_modules/@heron/runtime/dist/host-main.js above the project or a
// module it loads.
func (m *Manager) jsHostCommand(s *Spec) ([]string, error) {
	node := os.Getenv("HERON_NODE")
	if node == "" {
		node = "node"
	}
	if p, err := exec.LookPath(node); err == nil {
		node = p
	} else {
		return nil, ForeignError("the JS host needs Node: %v", err)
	}
	argv := []string{node}
	if os.Getenv("HERON_JS_INSPECT") != "" {
		argv = append(argv, "--inspect")
	}
	if h := os.Getenv("HERON_JS_HOST"); h != "" {
		return append(argv, h), nil
	}
	dirs := []string{m.root}
	for _, d := range s.Decls {
		if filepath.IsAbs(d.Module) {
			dirs = append(dirs, filepath.Dir(d.Module))
		}
	}
	for _, d := range dirs {
		for dir := d; dir != "" && dir != "/" && dir != "."; dir = filepath.Dir(dir) {
			h := filepath.Join(dir, "node_modules", "@heron", "runtime", "dist", "host-main.js")
			if _, err := os.Stat(h); err == nil {
				return append(argv, h), nil
			}
		}
	}
	return nil, ForeignError("the JS host isn't installed: add @heron/runtime to the project's dev dependencies, or set HERON_JS_HOST")
}

// approve asks before an extension's first run, and remembers the answer
// by a hash of its command and limits.
func (m *Manager) approve(s *Spec) error {
	h := sha256.Sum256([]byte(strings.Join([]string{s.Name, strings.Join(s.Command, "\x00"), strings.Join(s.Files, "\x00"),
		strings.Join(s.Network, "\x00"), strings.Join(s.Commands, "\x00"), s.Version, s.Checksum}, "\x01")))
	key := s.Name + " " + hex.EncodeToString(h[:])
	file := filepath.Join(m.root, ".heron", "trusted-extensions")
	if data, err := os.ReadFile(file); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if l == key {
				return nil
			}
		}
	}
	if os.Getenv("HERON_TRUST_EXTENSIONS") == "1" {
		return nil
	}
	if m.Approve == nil {
		return ForeignError("%s hasn't run on this machine before: run it once from a terminal to approve it, or set HERON_TRUST_EXTENSIONS=1", s.Name)
	}
	ok, err := m.Approve(s)
	if err != nil {
		return err
	}
	if !ok {
		return ForeignError("%s wasn't approved", s.Name)
	}
	_ = os.MkdirAll(filepath.Dir(file), 0o755)
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		fmt.Fprintln(f, key)
		f.Close()
	}
	return nil
}

// askOnTerminal asks on the terminal whether an extension may run, showing
// what it declared it needs; without a terminal it refuses.
func askOnTerminal(s *Spec) (bool, error) {
	st, err := os.Stdin.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false, ForeignError("%s hasn't run on this machine before; run it once from a terminal to approve it, or set HERON_TRUST_EXTENSIONS=1", s.Name)
	}
	none := func(l []string) string {
		if len(l) == 0 {
			return "none"
		}
		return strings.Join(l, ", ")
	}
	fmt.Fprintf(os.Stderr, "heron: the extension %s wants to run %q\n  writes: %s\n  network: %s\n  commands: %s\nAllow it? [y/N] ",
		s.Name, strings.Join(s.Command, " "), none(s.Files), none(s.Network), none(s.Commands))
	var answer string
	fmt.Fscanln(os.Stdin, &answer)
	a := strings.ToLower(strings.TrimSpace(answer))
	return a == "y" || a == "yes", nil
}

// Verify starts every process and reports what the schema declares but an
// extension doesn't implement, as check errors at the declaration.
func (m *Manager) Verify(ctx context.Context) diag.List {
	m.mu.Lock()
	var specs []*Spec
	for _, s := range m.specs {
		specs = append(specs, s)
	}
	m.mu.Unlock()
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	var out diag.List
	for _, s := range specs {
		p, err := m.process(s.Name)
		if err == nil {
			_, err = p.Start(ctx)
		}
		if err != nil {
			for _, d := range s.Decls {
				out.Add(mismatch(d, s.Name, "can't start: "+err.Error()))
				break
			}
			continue
		}
		got := map[string]protocol.Point{}
		for _, i := range p.Implements() {
			got[i.Path] = i.Point
		}
		for _, d := range s.Decls {
			if _, ok := got[d.Path]; !ok {
				out.Add(mismatch(d, s.Name, "doesn't implement it"))
			}
		}
	}
	return out
}

func (m *Manager) reportMismatches(in *lint.Input) {
	m.mu.Lock()
	n := len(m.specs)
	m.mu.Unlock()
	if n == 0 {
		return
	}
	for _, d := range m.Verify(context.Background()) {
		node := in.Tree.Root
		var f *resolve.File
		for _, s := range m.specs {
			for _, dc := range s.Decls {
				if dc.Node != nil && dc.Node.Decl.File != nil && string(dc.Node.Decl.File.ID) == d.File {
					node, f = dc.Node, dc.Node.Decl.File
				}
			}
		}
		in.ReportAt(node, f, d.Start, d.End, d.Severity, d.Message)
	}
}

func (m *Manager) runLint(in *lint.Input, spec string, d Decl) {
	nodes, by := ownNodes(in.Tree, in.Checker)
	p, err := m.process(spec)
	var res protocol.LintResult
	if err == nil {
		err = p.Call(context.Background(), protocol.MethodLint, &protocol.LintParams{Lint: d.Path, Nodes: nodes}, &res)
	}
	if err != nil {
		if d.Node != nil && d.Node.Decl.File != nil {
			s, e := d.Node.Decl.Span()
			in.ReportAt(d.Node, d.Node.Decl.File, s, e, diag.Error, fmt.Sprintf("lint %s failed: %v", d.Path, err))
		}
		return
	}
	files := map[string]*resolve.File{}
	for _, f := range in.Tree.Files {
		files[string(f.ID)] = f
	}
	for _, f := range res.Findings {
		n := by[f.Node]
		if n == nil {
			continue
		}
		sev := diag.Warning
		switch derefSev(f.Severity) {
		case protocol.SeverityError:
			sev = diag.Error
		case protocol.SeverityInfo:
			sev = diag.Hint
		}
		file, start, end := n.Decl.File, 0, 0
		start, end = n.Decl.Span()
		if f.File != "" && files[f.File] != nil {
			file = files[f.File]
		}
		if f.Start != nil {
			start = int(*f.Start)
			end = start
			if f.End != nil {
				end = int(*f.End)
			}
		}
		var fixes []diag.Fix
		for _, pf := range f.Fixes {
			fx := diag.Fix{Title: pf.Title}
			for _, e := range pf.Edits {
				ef := e.File
				if ef == "" {
					ef = string(file.ID)
				}
				fx.Edits = append(fx.Edits, diag.Edit{File: ef, Start: int(e.Start), End: int(e.End), New: e.New})
			}
			fixes = append(fixes, fx)
		}
		in.ReportFix(n, file, start, end, sev, f.Message, fixes)
	}
}

func derefSev(s protocol.Severity) protocol.Severity { return s }

// Instance returns a resource's instance in the run the call belongs to,
// set up on first use and torn down when the run ends. Outside a run, as in
// a test or `heron lint`, instances live until Close.
func (m *Manager) Instance(ctx context.Context, path string) (any, error) {
	run := m.runOf(ctx)
	if run == nil {
		m.mu.Lock()
		if m.session == nil {
			m.session = m.Registry.StartRun(fmt.Sprintf("session-%p", m))
		}
		run = m.session
		m.mu.Unlock()
	}
	return run.Instance(ctx, path)
}

// Close tears down instances used outside a run and shuts every process
// down.
func (m *Manager) Close() {
	m.mu.Lock()
	session := m.session
	m.session = nil
	procs := m.procs
	m.procs = map[string]*Process{}
	m.mu.Unlock()
	if session != nil {
		_ = session.End(context.Background())
	}
	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Add(1)
		go func(p *Process) { defer wg.Done(); p.Shutdown() }(p)
	}
	wg.Wait()
}

// hostServices is what extensions call back into: the way back into Heron.
type hostServices struct{ m *Manager }

func (h hostServices) Node(ctx context.Context, p *protocol.NodeParams) (*protocol.Node, error) {
	h.m.mu.Lock()
	t, c := h.m.tree, h.m.checker
	h.m.mu.Unlock()
	if t == nil {
		return nil, nil
	}
	depth := -1
	if p.Depth != nil {
		depth = int(*p.Depth)
	}
	find := func(t *resolve.Tree) *resolve.Node {
		if p.Path == "" {
			return t.Root
		}
		return t.Find(p.Path)
	}
	if n := find(t); n != nil {
		return EncodeNode(c, n, depth), nil
	}
	for _, pk := range t.Imports {
		if pk.Tree != nil {
			if n := find(pk.Tree); n != nil && n != pk.Tree.Root {
				return EncodeNode(c, n, depth), nil
			}
		}
	}
	return nil, nil
}

// Call serves ctx.call: in a run, through the interpreter, so a Heron func
// runs its code and an op goes through its transport, both recorded.
func (h hostServices) Call(ctx context.Context, p *protocol.CallParams) (protocol.Value, error) {
	args, _ := plain(map[string]any(p.Args)).(map[string]any)
	var out any
	var err error
	switch run := h.m.runOf(ctx); {
	case run != nil && run.Call != nil:
		out, err = run.Call(ctx, p.Target, args)
	case h.m.Registry.Func(p.Target) != nil:
		out, err = h.m.Registry.Func(p.Target).Call(ctx, &registry.FuncCall{Func: p.Target, Args: args})
	case h.m.Call != nil:
		out, err = h.m.Call(ctx, p.Target, args)
	default:
		return nil, protocol.Errorf(protocol.CodeInvalidParams, "%s can be called from an extension only during a heron run", p.Target)
	}
	var th *registry.Thrown
	if errors.As(err, &th) {
		return nil, &protocol.Error{Code: protocol.CodeThrown, Message: th.Error(), Type: th.Type, Data: th.Fields}
	}
	if err != nil {
		return nil, protocol.Errorf(protocol.CodeInvalidParams, "%s: %v", p.Target, err)
	}
	return out, nil
}

func (h hostServices) Resource(ctx context.Context, p *protocol.NameParams) (protocol.Value, error) {
	return h.m.Instance(ctx, p.Name)
}

func (h hostServices) Secret(ctx context.Context, p *protocol.NameParams) (string, error) {
	key := "HERON_SECRET_" + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(p.Name))
	if v, ok := os.LookupEnv(key); ok {
		return v, nil
	}
	return "", protocol.Errorf(protocol.CodeInvalidParams, "no secret %s: set %s", p.Name, key)
}

func (h hostServices) Temp(ctx context.Context, p *protocol.NameParams) (string, error) {
	dir := filepath.Join(h.m.TempRoot, orDefault(protocol.RunOf(ctx), "local"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.Clean("/"+p.Name)), nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
