package records_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiviarium/heron/internal/core/eval"
	"github.com/aiviarium/heron/internal/core/records"
	"github.com/aiviarium/heron/internal/core/registry"
)

// A sample record kind, as a later unit would register one: test runs.
func sampleKind() registry.RecordKind {
	return registry.RecordKind{
		Name:    "sample_test_run",
		Summary: "A test run, from a unit that isn't built",
		Version: 2,
		Validate: func(d map[string]any) error {
			if _, ok := d["passed"].(float64); !ok {
				if _, ok := d["passed"].(int); !ok {
					return errors.New("passed is not a number")
				}
			}
			return nil
		},
	}
}

func TestSampleRecordKind(t *testing.T) {
	r := registry.New()
	records.Register(r)
	r.MustAddRecordKind(sampleKind())
	if err := r.AddRecordKind(sampleKind()); err == nil {
		t.Error("a kind registered twice is accepted")
	}
	root := t.TempDir()
	s := records.Open(root, records.Config{Paths: map[string]string{"sample_test_run": "out/tests"}}, r)
	clock := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { clock = clock.Add(time.Second); return clock }

	if _, err := s.Write("sample_test_run", map[string]any{"passed": "lots"}); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Errorf("invalid data is written: %v", err)
	}
	if _, err := s.Write("unknown", map[string]any{}); err == nil {
		t.Error("an unregistered kind is written")
	}
	a, err := s.Write("sample_test_run", map[string]any{"passed": 3})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Write("sample_test_run", map[string]any{"passed": 5, "suite": "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(a.Path) != filepath.Join(root, "out/tests") {
		t.Errorf("configured location not used: %s", a.Path)
	}
	got, err := s.List("sample_test_run")
	if err != nil || len(got) != 2 || got[0].ID != b.ID || got[1].ID != a.ID {
		t.Fatalf("list: %v %v", got, err)
	}
	if got[0].Version != 2 || got[0].Data["suite"] != "orders" || got[0].Data["passed"] != float64(5) {
		t.Errorf("read back: %+v", got[0])
	}
	one, err := s.Read("sample_test_run", a.ID)
	if err != nil || one.Data["passed"] != float64(3) {
		t.Errorf("read: %+v %v", one, err)
	}
	if rs, _ := s.List(records.RunKind); len(rs) != 0 {
		t.Errorf("runs listed before any: %v", rs)
	}

	s2 := records.Open(root, records.Config{Keep: 90 * time.Second, Paths: map[string]string{"sample_test_run": "out/tests"}}, r)
	s2.Now = func() time.Time { return clock.Add(89500 * time.Millisecond) }
	n, err := s2.Prune()
	if err != nil || n != 1 {
		t.Fatalf("prune removed %d: %v", n, err)
	}
	if _, err := os.Stat(a.Path); err == nil {
		t.Error("the old record is kept")
	}
}

func TestRunRecorder(t *testing.T) {
	clock := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { clock = clock.Add(10 * time.Millisecond); return clock }
	rec := records.NewRunRecorder("deploy", "/repo/index.heron", map[string]eval.Value{"count": int64(2)}, now)
	rec.StartTask("prepare", "needed")
	rec.OnEvent(eval.Event{Kind: eval.EventProcess, Name: "pnpm", Args: map[string]eval.Value{"args": []eval.Value{"install"}},
		Value: &eval.Record{Type: "Process", Fields: map[string]eval.Value{"exit": int64(0)}}})
	rec.FinishTask(nil, nil)
	rec.StartTask("deploy", "task")
	rec.OnEvent(eval.Event{Kind: eval.EventStep, Name: "rejected", Depth: 1})
	rec.OnEvent(eval.Event{Kind: eval.EventLog, Value: "hello"})
	rec.OnEvent(eval.Event{Kind: eval.EventCall, Name: "tools.export", Args: map[string]eval.Value{"id": int64(1)}, Value: "/tmp/x.csv"})
	rec.FinishTask(nil, errors.New("boom"))
	d := rec.Data(nil, errors.New("deploy failed: boom"), 1)
	if d["status"] != "failed" || d["error"] != "deploy failed: boom" || d["task"] != "deploy" {
		t.Errorf("summary: %v", d)
	}
	if a := d["args"].(map[string]any); a["count"] != int64(2) {
		t.Errorf("args: %v", a)
	}
	tasks := d["tasks"].([]any)
	if len(tasks) != 2 || tasks[1].(map[string]any)["status"] != "failed" {
		t.Errorf("tasks: %v", tasks)
	}
	procs := d["processes"].([]any)
	if len(procs) != 1 || procs[0].(map[string]any)["task"] != "prepare" {
		t.Errorf("processes: %v", procs)
	}
	if steps := d["steps"].([]any); len(steps) != 1 || steps[0].(map[string]any)["name"] != "rejected" {
		t.Errorf("steps: %v", steps)
	}
	calls := d["calls"].([]any)
	if len(calls) != 1 || calls[0].(map[string]any)["name"] != "tools.export" || calls[0].(map[string]any)["result"] != "/tmp/x.csv" {
		t.Errorf("calls: %v", calls)
	}
	if d["duration_ms"].(int64) <= 0 {
		t.Errorf("timing: %v", d["duration_ms"])
	}
}
