package registry

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Transports and generation adapters are registered by the path of the
// declaration they implement, such as "docs.site"; a later registration
// under the same path replaces the earlier one.

// Transport implements std/apis' Transport interface for an op's requests.
type Transport struct {
	Path string
	Send func(ctx context.Context, op string, request any) (any, error)
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
	Path   string // such as "gen.ts"
	Target string
	// Generate receives the gen package's *gen.Input.
	Generate func(ctx context.Context, in any) ([]GeneratedFile, error)
	// OnRequest marks a target that runs only when named, never as one of
	// every target, such as an extra for outside tooling.
	OnRequest bool
}

type parts struct {
	mu         sync.Mutex
	transports map[string]*Transport
	generators map[string]*Generator
}

func (r *Registry) parts() *parts {
	r.partsOnce.Do(func() {
		r.more = &parts{
			transports: map[string]*Transport{}, generators: map[string]*Generator{},
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

func transportsOf(p *parts) map[string]*Transport { return p.transports }
func generatorsOf(p *parts) map[string]*Generator { return p.generators }

// SetTransport registers a transport implementation.
func (r *Registry) SetTransport(t Transport) error {
	return setPart(r, transportsOf, t.Path, &t, t.Send != nil)
}

// Transport returns the transport registered for a declaration, or nil.
func (r *Registry) Transport(path string) *Transport { return getPart(r, transportsOf, path) }

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
