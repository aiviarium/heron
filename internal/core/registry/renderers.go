package registry

import (
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
}

type renderers struct {
	mu  sync.Mutex
	set map[string]*Renderer
}

var renderersOf sync.Map // *Registry → *renderers

func (r *Registry) renderers() *renderers {
	v, _ := renderersOf.LoadOrStore(r, &renderers{set: map[string]*Renderer{}})
	return v.(*renderers)
}

// SetRenderer registers the renderer of a kind, replacing an earlier one.
func (r *Registry) SetRenderer(x Renderer) error {
	if x.Kind == "" || x.Render == nil {
		return fmt.Errorf("registry: a renderer needs a kind and a Render func")
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
