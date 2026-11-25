package records

import (
	"fmt"
	"time"

	"github.com/aiviarium/heron/internal/core/eval"
	"github.com/aiviarium/heron/internal/core/registry"
)

// RunKind is the kind of the record every `heron run` leaves.
const RunKind = "run"

// Register installs the record kinds of the language and runtime unit.
func Register(r *registry.Registry) {
	r.MustAddRecordKind(registry.RecordKind{
		Name:    RunKind,
		Summary: "A task run: its arguments, tasks, steps, processes, result and timing",
		Version: 1,
		Validate: func(d map[string]any) error {
			if _, ok := d["task"].(string); !ok {
				return fmt.Errorf("it names no task")
			}
			switch d["status"] {
			case "ok", "failed":
			default:
				return fmt.Errorf("status is %v, not ok or failed", d["status"])
			}
			return nil
		},
	})
}

// RunRecorder collects what one run did from the machine's events.
type RunRecorder struct {
	now     func() time.Time
	start   time.Time
	task    string
	entry   string
	args    map[string]any
	tasks   []any
	steps   []any
	procs   []any
	logs    []any
	calls   []any
	foreign []any
	res     []any
	id      string
	current map[string]any
}

// NewRunRecorder starts the record of a run of a task with its arguments.
func NewRunRecorder(task, entry string, args map[string]eval.Value, now func() time.Time) *RunRecorder {
	if now == nil {
		now = time.Now
	}
	plain := map[string]any{}
	for k, v := range args {
		plain[k] = eval.Plain(v)
	}
	start := now()
	return &RunRecorder{now: now, start: start, task: task, entry: entry, args: plain, id: newID(start.UTC())}
}

// ID is the run's id, which its calls into extensions carry.
func (r *RunRecorder) ID() string { return r.id }

// OnResource records a resource instance set up or torn down in the run.
func (r *RunRecorder) OnResource(e registry.ResourceEvent) {
	ev := map[string]any{"at_ms": r.ms(r.now()), "resource": e.Resource, "phase": e.Phase, "duration_ms": e.Duration.Milliseconds()}
	if e.Err != nil {
		ev["error"] = e.Err.Error()
	}
	r.res = append(r.res, ev)
}

func (r *RunRecorder) ms(t time.Time) int64 { return t.Sub(r.start).Milliseconds() }

// StartTask notes that one task of the run, the asked one or one it needs
// or an undo, starts; FinishTask closes it.
func (r *RunRecorder) StartTask(path, role string) {
	r.current = map[string]any{"task": path, "role": role, "start_ms": r.ms(r.now())}
}

// FinishTask closes the task StartTask opened.
func (r *RunRecorder) FinishTask(result eval.Value, err error) {
	if r.current == nil {
		return
	}
	t := r.current
	r.current = nil
	t["end_ms"] = r.ms(r.now())
	if err != nil {
		t["status"] = "failed"
		t["error"] = err.Error()
	} else {
		t["status"] = "ok"
		t["result"] = eval.Plain(result)
	}
	r.tasks = append(r.tasks, t)
}

// OnEvent is the machine's event hook.
func (r *RunRecorder) OnEvent(e eval.Event) {
	ev := map[string]any{"at_ms": r.ms(r.now()), "depth": int64(e.Depth)}
	if e.Name != "" {
		ev["name"] = e.Name
	}
	if r.current != nil {
		ev["task"] = r.current["task"]
	}
	if e.Err != nil {
		ev["error"] = e.Err.Error()
	}
	switch e.Kind {
	case eval.EventLog:
		ev["line"] = eval.Str(e.Value)
		r.logs = append(r.logs, ev)
	case eval.EventStep:
		r.steps = append(r.steps, ev)
	case eval.EventProcess:
		ev["args"] = eval.Plain(e.Args["args"])
		if p, ok := e.Value.(*eval.Record); ok {
			ev["exit"] = p.Fields["exit"]
		}
		r.procs = append(r.procs, ev)
	case eval.EventCall:
		ev["args"] = plainMap(e.Args)
		if e.Err == nil {
			ev["result"] = eval.Plain(e.Value)
		}
		r.calls = append(r.calls, ev)
	case eval.EventForeign:
		ev["func"] = e.Name
		delete(ev, "name")
		ev["args"] = plainMap(e.Args)
		ev["duration_ms"] = e.Duration.Milliseconds()
		if e.Err == nil {
			ev["result"] = eval.Plain(e.Value)
		}
		r.foreign = append(r.foreign, ev)
	case eval.EventSet:
		ev["value"] = eval.Plain(e.Value)
		r.calls = append(r.calls, ev)
	}
}

// Data is the record of the finished run; err is the failure, if any, and
// exit the command's exit status.
func (r *RunRecorder) Data(result eval.Value, err error, exit int) map[string]any {
	end := r.now()
	d := map[string]any{
		"task":        r.task,
		"entry":       r.entry,
		"args":        r.args,
		"started":     r.start.UTC().Format(time.RFC3339Nano),
		"finished":    end.UTC().Format(time.RFC3339Nano),
		"duration_ms": end.Sub(r.start).Milliseconds(),
		"exit":        int64(exit),
		"tasks":       list(r.tasks),
		"steps":       list(r.steps),
		"processes":   list(r.procs),
		"logs":        list(r.logs),
		"calls":       list(r.calls),
		"foreign":     list(r.foreign),
		"resources":   list(r.res),
		"run_id":      r.id,
	}
	if err != nil {
		d["status"] = "failed"
		d["error"] = err.Error()
	} else {
		d["status"] = "ok"
		d["result"] = eval.Plain(result)
	}
	return d
}

func list(xs []any) []any {
	if xs == nil {
		return []any{}
	}
	return xs
}

func plainMap(m map[string]eval.Value) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = eval.Plain(v)
	}
	return out
}
