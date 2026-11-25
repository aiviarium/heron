// Package registry holds the extension points every unit registers through:
// commands, generation targets, lints, prose lint providers, record kinds,
// drivers and renderers. Built-in parts and extensions register the same
// way, so an earlier unit never knows which later units exist.
package registry

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// Env is what a command runs with.
type Env struct {
	Dir    string // the working directory
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Command is a subcommand of the heron binary.
type Command struct {
	Name    string
	Summary string // one line, shown by `heron help`
	Usage   string // the argument synopsis, such as "[paths…]"
	Run     func(env *Env, args []string) int
}

// Registry is the set of registered extension points.
type Registry struct {
	commands map[string]*Command
	lints    map[string]*Lint
	funcs    map[string]*Func
	foreign  map[string]*Foreign
	prose    map[string]*ProseProvider
	records  map[string]*RecordKind
	setup    map[string]*SetupStep

	// The parts packages declare and code implements, in parts.go.
	partsOnce sync.Once
	more      *parts
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{commands: map[string]*Command{}, lints: map[string]*Lint{}}
}

// Lint is a check over the resolved tree that `heron lint` runs. Core lints,
// lints from packages and lints from extensions register alike.
type Lint struct {
	Name    string // stable, as written in @lint(allow = […])
	Summary string
	// Run receives the lint package's *lint.Input. The registry stays free
	// of the stages, so every unit can import it.
	Run func(in any)
}

// AddLint registers a lint by name; a name registered twice is an error.
func (r *Registry) AddLint(l Lint) error {
	if l.Name == "" || l.Run == nil {
		return fmt.Errorf("registry: a lint needs a name and a Run function")
	}
	if _, dup := r.lints[l.Name]; dup {
		return fmt.Errorf("registry: lint %q is registered twice", l.Name)
	}
	r.lints[l.Name] = &l
	return nil
}

// MustAddLint is AddLint for registrations at start-up.
func (r *Registry) MustAddLint(l Lint) {
	if err := r.AddLint(l); err != nil {
		panic(err)
	}
}

// Lints returns every registered lint, sorted by name.
func (r *Registry) Lints() []*Lint {
	out := make([]*Lint, 0, len(r.lints))
	for _, l := range r.lints {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AddCommand registers a command by name. Registering a name twice is an
// error, so two units can't silently claim one command.
func (r *Registry) AddCommand(c Command) error {
	if c.Name == "" || c.Run == nil {
		return fmt.Errorf("registry: a command needs a name and a Run function")
	}
	if _, dup := r.commands[c.Name]; dup {
		return fmt.Errorf("registry: command %q is registered twice", c.Name)
	}
	r.commands[c.Name] = &c
	return nil
}

// MustAddCommand is AddCommand for registrations at start-up.
func (r *Registry) MustAddCommand(c Command) {
	if err := r.AddCommand(c); err != nil {
		panic(err)
	}
}

// Command returns the command registered under name, or nil.
func (r *Registry) Command(name string) *Command { return r.commands[name] }

// Commands returns every registered command, sorted by name.
func (r *Registry) Commands() []*Command {
	out := make([]*Command, 0, len(r.commands))
	for _, c := range r.commands {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
