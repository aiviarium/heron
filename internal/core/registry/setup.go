package registry

import (
	"context"
	"fmt"
	"sort"
)

// ToolPin is a tool pinned in heron.lock's `tools` section: its version,
// and the checksum of the binary setup found.
type ToolPin struct {
	Version  string
	Checksum string
}

// SetupStep is a step `heron run setup` runs after pinning packages and
// sources, for a tool a unit brings, such as Vale for prose linting. It
// receives the repository's root and the pin heron.lock holds for it (nil
// when there's none) and returns the pin to write, or nil when the
// repository doesn't use the tool, and what it did, for the setup's
// result. A pin that moved fails unless update is set.
type SetupStep struct {
	Name    string // the key in heron.lock's tools section
	Summary string
	Run     func(ctx context.Context, root string, old *ToolPin, update bool) (pin *ToolPin, result any, err error)
}

// AddSetupStep registers a setup step by name.
func (r *Registry) AddSetupStep(s SetupStep) error {
	if s.Name == "" || s.Run == nil {
		return fmt.Errorf("registry: a setup step needs a name and a Run function")
	}
	if r.setup == nil {
		r.setup = map[string]*SetupStep{}
	}
	if _, dup := r.setup[s.Name]; dup {
		return fmt.Errorf("registry: setup step %q is registered twice", s.Name)
	}
	r.setup[s.Name] = &s
	return nil
}

// MustAddSetupStep is AddSetupStep for registrations at start-up.
func (r *Registry) MustAddSetupStep(s SetupStep) {
	if err := r.AddSetupStep(s); err != nil {
		panic(err)
	}
}

// SetupSteps returns the registered setup steps, sorted by name.
func (r *Registry) SetupSteps() []*SetupStep {
	out := make([]*SetupStep, 0, len(r.setup))
	for _, s := range r.setup {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
