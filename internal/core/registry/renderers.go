package registry

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Renderer renders the nodes of one kind for the docs and the live
// preview. Kind is the kind's declaration path, such as "spec.rule" or
// "types.op"; a package registering a kind of its own uses that kind's
// path, and registering a built-in kind again overrides it.
type Renderer struct {
	Kind string
	// Name identifies who registered it, such as "docs" or a package.
	Name string
	// Render receives the docs package's *docs.Ctx, positioned on the
	// node, and writes the node's HTML through it. The registry stays free
	// of the stages, so every unit can register one.
	Render func(ctx any) error
	// Plain renders from plain data instead, for a renderer outside Go
	// such as a TS module run by the JS host: the docs package fills the
	// input and writes the output. A renderer sets Render or Plain.
	Plain func(ctx context.Context, in *RenderInput) (*RenderOutput, error)
}

// RenderInput is a node or markup tag as plain data: the docs package has
// rendered its prose and children already.
type RenderInput struct {
	Kind   string
	Path   string // "" for a markup tag
	Name   string
	Title  string
	Anchor string
	Level  int
	Prose  string
	// Children is the node's children, each through its kind's renderer.
	Children string
	// Attrs are the kind's arguments, or a tag's attributes, typed by the
	// kind's parameters.
	Attrs map[string]any
	// Body is a markup tag's rendered content.
	Body string
	// Slots are the kind's slots other than before and after, rendered.
	Slots map[string]string
}

// RenderOutput is what a plain renderer returns.
type RenderOutput struct {
	HTML string
	// Block says a markup tag renders as a block.
	Block   bool
	Islands []string
}

// RendererSource registers renderers whose kinds are known only once
// something runs, such as a TS module's: LoadRenderers runs it.
type RendererSource struct {
	Name string
	Load func(ctx context.Context) ([]Renderer, error)
}

type renderers struct {
	mu      sync.Mutex
	set     map[string]*Renderer
	sources map[string]*rendererSource
}

type rendererSource struct {
	RendererSource
	loaded bool
	kinds  []string
}

var renderersOf sync.Map // *Registry → *renderers

func (r *Registry) renderers() *renderers {
	v, _ := renderersOf.LoadOrStore(r, &renderers{set: map[string]*Renderer{}, sources: map[string]*rendererSource{}})
	return v.(*renderers)
}

// SetRenderer registers the renderer of a kind, replacing an earlier one.
func (r *Registry) SetRenderer(x Renderer) error {
	if x.Kind == "" || x.Render == nil && x.Plain == nil {
		return fmt.Errorf("registry: a renderer needs a kind and a Render or Plain func")
	}
	rs := r.renderers()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.set[x.Kind] = &x
	return nil
}

// Renderer returns the renderer of a kind, or nil.
func (r *Registry) Renderer(kind string) *Renderer {
	rs := r.renderers()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.set[kind]
}

// Renderers returns every registered renderer, sorted by kind.
func (r *Registry) Renderers() []*Renderer {
	rs := r.renderers()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]*Renderer, 0, len(rs.set))
	for _, x := range rs.set {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// SetRendererSource registers a source of renderers, replacing one of the
// same name; LoadRenderers runs it.
func (r *Registry) SetRendererSource(s RendererSource) {
	rs := r.renderers()
	rs.mu.Lock()
	defer rs.mu.Unlock()
	old := rs.sources[s.Name]
	n := &rendererSource{RendererSource: s}
	if old != nil {
		n.kinds = old.kinds
	}
	rs.sources[s.Name] = n
}

// LoadRenderers runs every source that hasn't run since it was set and
// registers what it returns, replacing the renderers it registered
// before. It returns the first error; the other sources still load.
func (r *Registry) LoadRenderers(ctx context.Context) error {
	rs := r.renderers()
	rs.mu.Lock()
	var pending []*rendererSource
	for _, s := range rs.sources {
		if !s.loaded {
			pending = append(pending, s)
		}
	}
	rs.mu.Unlock()
	sort.Slice(pending, func(i, j int) bool { return pending[i].Name < pending[j].Name })
	var first error
	for _, s := range pending {
		xs, err := s.Load(ctx)
		rs.mu.Lock()
		s.loaded = true
		for _, k := range s.kinds {
			if x := rs.set[k]; x != nil && x.Name == s.Name {
				delete(rs.set, k)
			}
		}
		s.kinds = nil
		for _, x := range xs {
			if x.Kind == "" || x.Render == nil && x.Plain == nil {
				continue
			}
			x := x
			x.Name = s.Name
			rs.set[x.Kind] = &x
			s.kinds = append(s.kinds, x.Kind)
		}
		rs.mu.Unlock()
		if err != nil && first == nil {
			first = fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	return first
}
