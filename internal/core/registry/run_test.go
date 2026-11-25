package registry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/helightdev/specs-v0/internal/core/registry"
)

func TestRunResourceLifecycle(t *testing.T) {
	r := registry.New()
	var log []string
	fails := 1
	driver := func(name string) registry.ResourceDriver {
		return registry.ResourceDriver{Path: name,
			Setup: func(context.Context) (any, error) {
				if name == "flaky" && fails > 0 {
					fails--
					return nil, errors.New("not yet")
				}
				log = append(log, "setup "+name)
				return name + "-handle", nil
			},
			Teardown: func(_ context.Context, h any) error { log = append(log, "teardown "+h.(string)); return nil },
		}
	}
	for _, n := range []string{"db", "cache", "flaky"} {
		if err := r.SetResourceDriver(driver(n)); err != nil {
			t.Fatal(err)
		}
	}
	run := r.StartRun("r1")
	if r.RunByID("r1") != run {
		t.Fatal("the run isn't open by its id")
	}
	ctx := registry.WithRun(context.Background(), run)
	if registry.RunOf(ctx) != run {
		t.Fatal("the context doesn't carry the run")
	}
	for _, n := range []string{"db", "cache", "db"} {
		if h, err := run.Instance(ctx, n); err != nil || h != n+"-handle" {
			t.Fatalf("%s: %v %v", n, h, err)
		}
	}
	if _, err := run.Instance(ctx, "flaky"); err == nil {
		t.Fatal("a failed setup succeeded")
	}
	if _, err := run.Instance(ctx, "flaky"); err != nil {
		t.Fatalf("a failed setup isn't retried: %v", err)
	}
	if _, err := run.Instance(ctx, "nothing"); err == nil {
		t.Fatal("a resource without a driver was set up")
	}
	if err := run.End(ctx); err != nil {
		t.Fatal(err)
	}
	want := "setup db,setup cache,setup flaky,teardown flaky-handle,teardown cache-handle,teardown db-handle"
	if got := strings.Join(log, ","); got != want {
		t.Errorf("lifecycle:\n got %s\nwant %s", got, want)
	}
	if _, err := run.Instance(ctx, "db"); err == nil {
		t.Error("an ended run set an instance up")
	}
	if r.RunByID("r1") != nil {
		t.Error("an ended run is still open")
	}
}
