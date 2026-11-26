package registry

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Macros, transports, resource drivers and generation adapters are
// registered by the path of the declaration they implement, such as
// "inventory.slugify". A built-in part and one an extension implements
// register alike, so a later registration under the same path, such as a
// newer external version of a built-in, replaces the earlier one.

// Macro implements a macro: it returns Heron source.
type Macro struct {
	Path   string
	Expand func(ctx context.Context, args map[string]any) (string, error)
}

// Transport implements std/apis' Transport interface for an op's requests.
type Transport struct {
	Path string
	Send func(ctx context.Context, op string, request any) (any, error)
}

// ResourceDriver provides instances of a resource. Setup returns an
// instance handle; Snapshot returns a snapshot handle. A driver that can't
// reset, snapshot, restore or fork leaves those nil.
type ResourceDriver struct {
	Path     string
	Setup    func(ctx context.Context) (any, error)
	Reset    func(ctx context.Context, handle any) error
	Snapshot func(ctx context.Context, handle any) (any, error)
	Restore  func(ctx context.Context, handle, snapshot any) error
	Fork     func(ctx context.Context, handle any) (any, error)
	Teardown func(ctx context.Context, handle any) error
}

// GeneratedFile is one file a generation adapter writes, relative to the
// project directory.
type GeneratedFile struct {
	Path    string
	Content []byte
	// Ignored marks a build output, which the managed ignore block lists.
	Ignored bool
}

// Generator is a generation adapter for a target of `heron generate`.
type Generator struct {
	Path   string // the declaration, such as "js.ts"
	Target string
	// Generate receives the gen package's *gen.Input.
	Generate func(ctx context.Context, in any) ([]GeneratedFile, error)
	// OnRequest marks a target that runs only when named, never as one of
	// every target, such as an extra for outside tooling.
	OnRequest bool
}

// Loader registers the parts a checked tree declares, such as what its
// extensions implement, before a command uses them. It receives the check
// package's *check.Result.
type Loader struct {
	Name string
	Load func(ctx context.Context, res any) error
}

type parts struct {
	mu         sync.Mutex
	macros     map[string]*Macro
	transports map[string]*Transport
	resources  map[string]*ResourceDriver
	generators map[string]*Generator
	loaders    []*Loader
	runs       map[string]*Run
	closers    []func()
}

func (r *Registry) parts() *parts {
	r.partsOnce.Do(func() {
		r.more = &parts{
			macros: map[string]*Macro{}, transports: map[string]*Transport{},
			resources: map[string]*ResourceDriver{}, generators: map[string]*Generator{},
		}
	})
	return r.more
}

func setPart[T any](r *Registry, m func(*parts) map[string]*T, path string, v *T, ok bool) error {
	if path == "" || !ok {
		return fmt.Errorf("registry: a part needs a path and an implementation")
	}
	p := r.parts()
	p.mu.Lock()
	defer p.mu.Unlock()
	m(p)[path] = v
	return nil
}

func getPart[T any](r *Registry, m func(*parts) map[string]*T, path string) *T {
	p := r.parts()
	p.mu.Lock()
	defer p.mu.Unlock()
	return m(p)[path]
}

func listParts[T any](r *Registry, m func(*parts) map[string]*T) []*T {
	p := r.parts()
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := make([]string, 0, len(m(p)))
	for k := range m(p) {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*T, 0, len(keys))
	for _, k := range keys {
		out = append(out, m(p)[k])
	}
	return out
}

func macrosOf(p *parts) map[string]*Macro             { return p.macros }
func transportsOf(p *parts) map[string]*Transport     { return p.transports }
func resourcesOf(p *parts) map[string]*ResourceDriver { return p.resources }
func generatorsOf(p *parts) map[string]*Generator     { return p.generators }

// SetMacro registers a macro implementation.
func (r *Registry) SetMacro(m Macro) error { return setPart(r, macrosOf, m.Path, &m, m.Expand != nil) }

// Macro returns the macro registered for a declaration, or nil.
func (r *Registry) Macro(path string) *Macro { return getPart(r, macrosOf, path) }

// SetTransport registers a transport implementation.
func (r *Registry) SetTransport(t Transport) error {
	return setPart(r, transportsOf, t.Path, &t, t.Send != nil)
}

// Transport returns the transport registered for a declaration, or nil.
func (r *Registry) Transport(path string) *Transport { return getPart(r, transportsOf, path) }

// Transports returns every registered transport, sorted by path.
func (r *Registry) Transports() []*Transport { return listParts(r, transportsOf) }

// SetResourceDriver registers the driver of a resource.
func (r *Registry) SetResourceDriver(d ResourceDriver) error {
	return setPart(r, resourcesOf, d.Path, &d, d.Setup != nil)
}

// ResourceDriver returns the driver registered for a resource, or nil.
func (r *Registry) ResourceDriver(path string) *ResourceDriver { return getPart(r, resourcesOf, path) }

// ResourceDrivers returns every registered driver, sorted by path.
func (r *Registry) ResourceDrivers() []*ResourceDriver { return listParts(r, resourcesOf) }

// SetGenerator registers a generation adapter.
func (r *Registry) SetGenerator(g Generator) error {
	if g.Target == "" {
		return fmt.Errorf("registry: generator %q needs a target", g.Path)
	}
	return setPart(r, generatorsOf, g.Path, &g, g.Generate != nil)
}

// Generators returns the adapters registered for a target, or every
// adapter for "", sorted by path.
func (r *Registry) Generators(target string) []*Generator {
	var out []*Generator
	for _, g := range listParts(r, generatorsOf) {
		if target == "" || g.Target == target {
			out = append(out, g)
		}
	}
	return out
}

// Targets returns every generation target that has an adapter, sorted,
// leaving out those that run only when named.
func (r *Registry) Targets() []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range r.Generators("") {
		if !seen[g.Target] && !g.OnRequest {
			seen[g.Target] = true
			out = append(out, g.Target)
		}
	}
	sort.Strings(out)
	return out
}

// AddLoader registers a loader, which LoadTree runs.
func (r *Registry) AddLoader(l Loader) {
	p := r.parts()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loaders = append(p.loaders, &l)
}

// LoadTree runs every loader over a checked tree, in registration order,
// and returns the first error.
func (r *Registry) LoadTree(ctx context.Context, res any) error {
	p := r.parts()
	p.mu.Lock()
	loaders := append([]*Loader(nil), p.loaders...)
	p.mu.Unlock()
	var first error
	for _, l := range loaders {
		if err := l.Load(ctx, res); err != nil && first == nil {
			first = fmt.Errorf("%s: %w", l.Name, err)
		}
	}
	return first
}

// OnClose registers a function Close calls, such as one stopping extension
// processes.
func (r *Registry) OnClose(f func()) {
	p := r.parts()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closers = append(p.closers, f)
}

// Close runs the functions OnClose registered, last first, once each.
func (r *Registry) Close() {
	p := r.parts()
	p.mu.Lock()
	cs := p.closers
	p.closers = nil
	p.mu.Unlock()
	for i := len(cs) - 1; i >= 0; i-- {
		cs[i]()
	}
}

// SetLint registers a lint, replacing one of the same name, as an
// extension does when it loads again for a changed tree.
func (r *Registry) SetLint(l Lint) error {
	if l.Name == "" || l.Run == nil {
		return fmt.Errorf("registry: a lint needs a name and a Run function")
	}
	p := r.parts()
	p.mu.Lock()
	defer p.mu.Unlock()
	r.lints[l.Name] = &l
	return nil
}
