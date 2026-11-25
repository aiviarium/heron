package registry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Run is the scope of one `heron run`: the resource instances its code and
// its extensions use, and the way extensions call back into the Heron code
// the run executes. Calls the run makes carry its id across the protocol,
// so an extension's ctx.resource and ctx.call land in the same run.
type Run struct {
	ID string
	// Call serves an extension's ctx.call: a Heron func or op by its
	// qualified path, with arguments as plain data. The interpreter sets it.
	Call func(ctx context.Context, target string, args map[string]any) (any, error)
	// OnResource, when set, hears each instance set up or torn down.
	OnResource func(ResourceEvent)

	r     *Registry
	mu    sync.Mutex
	inst  map[string]*instance
	order []string
	ended bool
}

// ResourceEvent is one step of a resource instance's lifecycle in a run.
type ResourceEvent struct {
	Resource string
	Phase    string // "setup" or "teardown"
	Duration time.Duration
	Err      error
}

type instance struct {
	ready  chan struct{}
	handle any
	err    error
}

type runKey struct{}

// StartRun opens a run scope under an id; EndRun closes it.
func (r *Registry) StartRun(id string) *Run {
	run := &Run{ID: id, r: r, inst: map[string]*instance{}}
	p := r.parts()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runs == nil {
		p.runs = map[string]*Run{}
	}
	p.runs[id] = run
	return run
}

// RunByID returns the open run with an id, or nil.
func (r *Registry) RunByID(id string) *Run {
	if id == "" {
		return nil
	}
	p := r.parts()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runs[id]
}

// WithRun returns a context that belongs to a run.
func WithRun(ctx context.Context, run *Run) context.Context {
	return context.WithValue(ctx, runKey{}, run)
}

// RunOf returns the run a context belongs to, or nil.
func RunOf(ctx context.Context) *Run {
	run, _ := ctx.Value(runKey{}).(*Run)
	return run
}

// Instance returns the run's instance of a resource, setting it up through
// its driver on first use; concurrent first uses share one setup.
func (run *Run) Instance(ctx context.Context, path string) (any, error) {
	run.mu.Lock()
	if run.ended {
		run.mu.Unlock()
		return nil, fmt.Errorf("run %s has ended; %s can't be set up", run.ID, path)
	}
	if in := run.inst[path]; in != nil {
		run.mu.Unlock()
		<-in.ready
		return in.handle, in.err
	}
	in := &instance{ready: make(chan struct{})}
	run.inst[path] = in
	run.mu.Unlock()

	d := run.r.ResourceDriver(path)
	start := time.Now()
	if d == nil {
		in.err = fmt.Errorf("no driver for resource %s", path)
	} else {
		in.handle, in.err = d.Setup(ctx)
	}
	run.report(ResourceEvent{Resource: path, Phase: "setup", Duration: time.Since(start), Err: in.err})
	run.mu.Lock()
	if in.err == nil {
		run.order = append(run.order, path)
	} else {
		delete(run.inst, path) // a later use tries again
	}
	run.mu.Unlock()
	close(in.ready)
	return in.handle, in.err
}

// Instances returns the resources set up so far, in setup order.
func (run *Run) Instances() []string {
	run.mu.Lock()
	defer run.mu.Unlock()
	return append([]string(nil), run.order...)
}

func (run *Run) report(e ResourceEvent) {
	if run.OnResource != nil {
		run.OnResource(e)
	}
}

// End tears down every instance the run set up, newest first, and closes
// the run; later uses fail. It returns the teardown errors joined.
func (run *Run) End(ctx context.Context) error {
	run.mu.Lock()
	if run.ended {
		run.mu.Unlock()
		return nil
	}
	run.ended = true
	order := run.order
	inst := run.inst
	run.order, run.inst = nil, map[string]*instance{}
	run.mu.Unlock()
	var errs []error
	for i := len(order) - 1; i >= 0; i-- {
		path := order[i]
		d := run.r.ResourceDriver(path)
		if d == nil || d.Teardown == nil {
			continue
		}
		start := time.Now()
		err := d.Teardown(ctx, inst[path].handle)
		run.report(ResourceEvent{Resource: path, Phase: "teardown", Duration: time.Since(start), Err: err})
		if err != nil {
			errs = append(errs, fmt.Errorf("tearing down %s: %w", path, err))
		}
	}
	p := run.r.parts()
	p.mu.Lock()
	if p.runs[run.ID] == run {
		delete(p.runs, run.ID)
	}
	p.mu.Unlock()
	return errors.Join(errs...)
}
