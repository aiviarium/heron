package extension

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/lint"
	"github.com/aiviarium/heron/internal/core/registry"
)

const sampleDir = "../../../web/samples/inventory"

// needJSHost skips unless Node and the built runtime are there; ci.sh
// builds web/runtime before the Go tests that need it.
func needJSHost(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node isn't installed")
	}
	if _, err := os.Stat(filepath.Join(sampleDir, "node_modules/@heron/runtime/dist/host-main.js")); err != nil {
		t.Skip("web/runtime isn't built; run pnpm install and pnpm -r build in web/")
	}
	if _, err := os.Stat(filepath.Join(sampleDir, ".heron/bindings.ts")); err != nil {
		t.Skip("the sample's bindings aren't generated; run heron generate ts in web/samples/inventory")
	}
}

func sample(t *testing.T) (*registry.Registry, *Manager, *check.Result, *bytes.Buffer) {
	needJSHost(t)
	r := registry.New()
	lint.Register(r)
	m := Register(r)
	logs := &bytes.Buffer{}
	m.Log = logs
	m.Warn = func(s string) { logs.WriteString("warning: " + s + "\n") }
	t.Cleanup(r.Close)
	res := checkDir(t, filepath.Join(sampleDir, "example"))
	if err := r.LoadTree(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	return r, m, res, logs
}

func TestSampleFuncThroughTheBinder(t *testing.T) {
	r, _, _, logs := sample(t)
	impl, err := r.Foreign("js").Bind(registry.Module{Kind: "js", Spec: "./src/index.ts", Dir: mustAbs(t, sampleDir)}, "slugify")
	if err != nil {
		t.Fatal(err)
	}
	out, err := impl(context.Background(), &registry.FuncCall{Func: "inventory.slugify", Args: map[string]any{"text": "Hello, World!"}})
	if err != nil || out != "hello-world" {
		t.Fatalf("%v %v\n%s", out, err, logs)
	}
}

// A run scopes resource instances and serves ctx.call: instances are set
// up on first use within the run, apart from other runs, and torn down when
// it ends; an extension's ctx.call reaches the run's Call.
func TestRunScopesResourcesAndCalls(t *testing.T) {
	r, m, _, logs := sample(t)
	f := r.Foreign("js")
	dir := mustAbs(t, sampleDir)
	count, _ := f.Bind(registry.Module{Kind: "js", Spec: "./src/index.ts", Dir: dir}, "count")
	describe, _ := f.Bind(registry.Module{Kind: "js", Spec: "./src/index.ts", Dir: dir}, "describe")

	var events []string
	run := r.StartRun("run-a")
	run.OnResource = func(e registry.ResourceEvent) { events = append(events, e.Phase+" "+e.Resource) }
	var called []string
	run.Call = func(_ context.Context, target string, args map[string]any) (any, error) {
		called = append(called, target)
		if target == "inventory.shelf.find" {
			return map[string]any{"op": target, "found": true, "item": map[string]any{"sku": args["sku"], "name": "Kibble", "count": int64(12)}}, nil
		}
		return args["name"].(string) + "!", nil
	}
	ctx := registry.WithRun(context.Background(), run)
	h1, err := m.Instance(ctx, "inventory.store")
	if err != nil {
		t.Fatalf("%v\n%s", err, logs)
	}
	if n, err := count(ctx, &registry.FuncCall{Func: "inventory.count", Args: map[string]any{"sku": "kib-1"}}); err != nil || n != int64(12) {
		t.Fatalf("count in the run: %v %v", n, err)
	}
	if got := run.Instances(); len(got) != 1 || got[0] != "inventory.store" {
		t.Fatalf("instances: %v (ctx.resource from the host should reuse the run's)", got)
	}
	other := r.StartRun("run-b")
	h2, err := m.Instance(registry.WithRun(context.Background(), other), "inventory.store")
	if err != nil || fmt.Sprint(h2) == fmt.Sprint(h1) {
		t.Fatalf("another run shares the instance: %v %v", h2, err)
	}
	_ = other.End(context.Background())

	out, err := describe(ctx, &registry.FuncCall{Func: "inventory.describe", Args: map[string]any{"sku": "kib-1"}})
	if err != nil || out != "Kibble!" || strings.Join(called, ",") != "inventory.shelf.find,inventory.label" {
		t.Fatalf("describe through ctx.call: %v %v %v\n%s", out, err, called, logs)
	}

	if err := run.End(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "setup inventory.store,teardown inventory.store" {
		t.Errorf("lifecycle: %v", events)
	}
	if _, err := run.Instance(ctx, "inventory.store"); err == nil {
		t.Error("an ended run set an instance up")
	}
	if r.RunByID("run-a") != nil {
		t.Error("an ended run is still open")
	}
}

func mustAbs(t *testing.T, p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSampleTransportAndResource(t *testing.T) {
	r, m, _, logs := sample(t)
	ctx := context.Background()
	tr := r.Transport("inventory.memory")
	if tr == nil {
		t.Fatal("the transport isn't registered")
	}
	out, err := tr.Send(ctx, "inventory.count", map[string]any{"sku": "toy-7"})
	if err != nil {
		t.Fatalf("%v\n%s", err, logs)
	}
	got := out.(map[string]any)
	if got["found"] != true || got["op"] != "inventory.count" {
		t.Fatalf("send: %#v", got)
	}
	d := r.ResourceDriver("inventory.store")
	h, err := m.Instance(ctx, "inventory.store")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := d.Snapshot(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Reset(ctx, h); err != nil {
		t.Fatal(err)
	}
	f := r.Foreign("js")
	count, _ := f.Bind(registry.Module{Kind: "js", Spec: "./src/index.ts", Dir: mustAbs(t, sampleDir)}, "count")
	n, err := count(ctx, &registry.FuncCall{Func: "inventory.count", Args: map[string]any{"sku": "kib-1"}})
	if err != nil || n != int64(0) {
		t.Fatalf("after reset: %v %v", n, err)
	}
	if err := d.Restore(ctx, h, snap); err != nil {
		t.Fatal(err)
	}
	n, err = count(ctx, &registry.FuncCall{Func: "inventory.count", Args: map[string]any{"sku": "kib-1"}})
	if err != nil || n != int64(12) {
		t.Fatalf("after restore: %v %v", n, err)
	}
	fork, err := d.Fork(ctx, h)
	if err != nil || fork == nil {
		t.Fatalf("fork: %v %v", fork, err)
	}
	if err := d.Teardown(ctx, fork); err != nil {
		t.Fatal(err)
	}
}

func TestSampleLintThroughHeronLint(t *testing.T) {
	r, _, res, logs := sample(t)
	ds := lint.Run(r, res)
	var found []string
	for _, d := range ds {
		found = append(found, d.Code+": "+d.Message)
	}
	if strings.Join(found, "\n") != "inventory.no_todo: Bin still says TODO" {
		t.Fatalf("findings:\n%s\n%s", strings.Join(found, "\n"), logs)
	}
}

func TestCrashFailsCallsAndRestarts(t *testing.T) {
	r, m, _, _ := sample(t)
	ctx := context.Background()
	slug, _ := r.Foreign("js").Bind(registry.Module{Kind: "js", Spec: "./src/index.ts", Dir: mustAbs(t, sampleDir)}, "slugify")
	if _, err := slug(ctx, &registry.FuncCall{Func: "inventory.slugify", Args: map[string]any{"text": "a"}}); err != nil {
		t.Fatal(err)
	}
	p, _ := m.process(JSHost)
	p.Kill()
	out, err := slug(ctx, &registry.FuncCall{Func: "inventory.slugify", Args: map[string]any{"text": "B c"}})
	if err != nil || out != "b-c" {
		t.Fatalf("after a crash: %v %v", out, err)
	}
	// A call in flight when the host dies fails with ForeignError: describe
	// calls back into Heron, which holds it until the host is killed, so
	// the kill can't come before the call or after its answer.
	describe, _ := r.Foreign("js").Bind(registry.Module{Kind: "js", Spec: "./src/index.ts", Dir: mustAbs(t, sampleDir)}, "describe")
	run := r.StartRun("crash")
	inFlight, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	run.Call = func(context.Context, string, map[string]any) (any, error) {
		once.Do(func() { close(inFlight) })
		<-release
		return nil, errors.New("the host is gone")
	}
	errc := make(chan error, 1)
	go func() {
		_, err := describe(registry.WithRun(ctx, run), &registry.FuncCall{Func: "inventory.describe", Args: map[string]any{"sku": "kib-1"}})
		errc <- err
	}()
	<-inFlight
	p.Kill()
	err = <-errc
	close(release)
	var th *registry.Thrown
	if !errors.As(err, &th) || th.Type != "ForeignError" {
		t.Fatalf("the call in flight: %v", err)
	}
	// And the next call starts the host again.
	if out, err := slug(ctx, &registry.FuncCall{Func: "inventory.slugify", Args: map[string]any{"text": "D e"}}); err != nil || out != "d-e" {
		t.Fatalf("after the second crash: %v %v", out, err)
	}
}

func TestHandshakeMismatchIsReported(t *testing.T) {
	needJSHost(t)
	dir := writeTree(t, map[string]string{
		"index.heron": `import = ["std/schema@1", "std/types@1", "std/js@1"]

impl: module = js("./impl.mjs")

/// Exported.
there(x: Int): func(Int) = impl.there

/// Not exported.
missing(x: Int): func(Int) = impl.missing
`,
		"impl.mjs": "export const there = ({ x }) => x + 1;\n",
	})
	host, _ := filepath.Abs(filepath.Join(sampleDir, "node_modules/@heron/runtime/dist/host-main.js"))
	t.Setenv("HERON_JS_HOST", host)
	r := registry.New()
	lint.Register(r)
	Register(r)
	t.Cleanup(r.Close)
	res := checkDir(t, dir)
	if err := r.LoadTree(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, d := range lint.Run(r, res) {
		if d.Code == "extension" && d.Severity == diag.Error {
			msgs = append(msgs, d.Message)
		}
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0], "missing: js doesn't implement it") {
		t.Fatalf("got %q", msgs)
	}
	impl, _ := r.Foreign("js").Bind(registry.Module{Kind: "js", Spec: "./impl.mjs", Dir: dir}, "there")
	out, err := impl(context.Background(), &registry.FuncCall{Func: "there", Args: map[string]any{"x": int64(41)}})
	if err != nil || out != int64(42) {
		t.Fatalf("%v %v", out, err)
	}
	missing, _ := r.Foreign("js").Bind(registry.Module{Kind: "js", Spec: "./impl.mjs", Dir: dir}, "missing")
	_, err = missing(context.Background(), &registry.FuncCall{Func: "missing", Args: map[string]any{"x": int64(1)}})
	var th *registry.Thrown
	if !errors.As(err, &th) || th.Type != "ForeignError" {
		t.Fatalf("calling what isn't implemented: %v", err)
	}
}
