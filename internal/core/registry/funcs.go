package registry

import (
	"context"
	"fmt"
	"sort"
)

// FuncImpl implements a Heron func outside Heron. Values are plain Go
// data, as eval.Plain gives them: nil, bool, int64, float64, string,
// []any and map[string]any (records are maps of their members). A
// returned *Thrown is a Heron error that `attempt(…)` can catch; any other
// error fails the run.
type FuncImpl func(ctx context.Context, call *FuncCall) (any, error)

// FuncCall is one call of a FuncImpl.
type FuncCall struct {
	// Func is the qualified path of the Heron declaration being called,
	// such as "orders.export_orders".
	Func string
	// Args holds the arguments by parameter name, after positional ones
	// were matched to the declaration's parameters and defaults applied.
	Args map[string]any
	// Dir is the directory of the file that declares the func.
	Dir string
	// Step runs the trailing block of the call, as in check("x") { … },
	// as a named step; it is nil when the call has none.
	Step func(ctx context.Context) error
}

// Thrown is an error a func throws as a Heron value: the error type's name,
// such as "ProcessError", and its members.
type Thrown struct {
	Type   string
	Fields map[string]any
}

func (t *Thrown) Error() string {
	if m, ok := t.Fields["message"].(string); ok {
		return t.Type + ": " + m
	}
	return t.Type
}

// Func is a func implemented in Go, registered by the
// qualified path of its Heron declaration, which has no body of its own.
// Eval calls it where the declaration is called.
type Func struct {
	Name    string // such as "seed.seed_pets"
	Summary string
	Call    FuncImpl
}

// AddFunc registers a func by its declaration's path; a path registered
// twice is an error.
func (r *Registry) AddFunc(f Func) error {
	if f.Name == "" || f.Call == nil {
		return fmt.Errorf("registry: a func needs a name and a Call function")
	}
	if r.funcs == nil {
		r.funcs = map[string]*Func{}
	}
	if _, dup := r.funcs[f.Name]; dup {
		return fmt.Errorf("registry: func %q is registered twice", f.Name)
	}
	r.funcs[f.Name] = &f
	return nil
}

// MustAddFunc is AddFunc for registrations at start-up.
func (r *Registry) MustAddFunc(f Func) {
	if err := r.AddFunc(f); err != nil {
		panic(err)
	}
}

// Func returns the func registered for a declaration's path, or nil.
func (r *Registry) Func(name string) *Func { return r.funcs[name] }

// Funcs returns every registered func, sorted by name.
func (r *Registry) Funcs() []*Func {
	out := make([]*Func, 0, len(r.funcs))
	for _, f := range r.funcs {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
