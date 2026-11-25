package extension

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/helightdev/specs-v0/internal/core/extension/protocol"
	"github.com/helightdev/specs-v0/internal/core/registry"
)

// A process that speaks another major version of the protocol is stopped
// after the handshake, and one that keeps exiting is started at most
// MaxRestarts times within RestartWindow.
func TestHandshakeVersionAndRestartLimit(t *testing.T) {
	ctx := context.Background()
	p := &Process{Spec: &Spec{Name: "probe"}, Argv: []string{os.Args[0]}, Env: []string{"HERON_TEST_EXTENSION=1", "HERON_TEST_PROTOCOL=2.0"}}
	if _, err := p.Start(ctx); err == nil || !strings.Contains(err.Error(), "speaks protocol 2.0, and heron speaks 1.0") {
		t.Errorf("version: %v", err)
	}
	p.Shutdown()
	same := &Process{Spec: &Spec{Name: "probe"}, Argv: []string{os.Args[0]}, Env: []string{"HERON_TEST_EXTENSION=1", "HERON_TEST_PROTOCOL=1.4"}}
	if _, err := same.Start(ctx); err != nil {
		t.Errorf("a minor version apart: %v", err)
	}
	same.Shutdown()
	dead := &Process{Spec: &Spec{Name: "dead"}, Argv: []string{"true"}}
	for i := 0; i < MaxRestarts; i++ {
		if _, err := dead.Start(ctx); err == nil || !strings.Contains(err.Error(), "the handshake failed") {
			t.Fatalf("start %d: %v", i+1, err)
		}
	}
	if _, err := dead.Start(ctx); err == nil || !strings.Contains(err.Error(), "not starting it again") {
		t.Errorf("after %d starts: %v", MaxRestarts, err)
	}
}

// What the host serves foreign code without a run: secrets from the
// environment, run-scoped paths kept inside their folder, and ctx.call
// refused outside a run.
func TestHostServicesOutsideARun(t *testing.T) {
	m := NewManager(registry.New())
	m.TempRoot = t.TempDir()
	h := hostServices{m}
	ctx := context.Background()
	t.Setenv("HERON_SECRET_STRIPE_API_KEY", "sk")
	if v, err := h.Secret(ctx, &protocol.NameParams{Name: "stripe.api-key"}); err != nil || v != "sk" {
		t.Errorf("secret: %q %v", v, err)
	}
	if _, err := h.Secret(ctx, &protocol.NameParams{Name: "missing"}); err == nil || !strings.Contains(err.Error(), "no secret missing: set HERON_SECRET_MISSING") {
		t.Errorf("missing secret: %v", err)
	}
	p, err := h.Temp(ctx, &protocol.NameParams{Name: "../x"})
	if err != nil || p != filepath.Join(m.TempRoot, "local", "x") {
		t.Errorf("temp: %q %v", p, err)
	}
	if _, err := h.Call(ctx, &protocol.CallParams{Target: "store.list"}); err == nil || !strings.Contains(err.Error(), "only during a heron run") {
		t.Errorf("call outside a run: %v", err)
	}
	t.Setenv("HERON_NODE", "no-such-node-binary")
	if _, err := m.jsHostCommand(&Spec{Name: JSHost}); err == nil || !strings.Contains(err.Error(), "the JS host needs Node") {
		t.Errorf("no node: %v", err)
	}
	t.Setenv("HERON_NODE", "true")
	t.Setenv("HERON_JS_HOST", "")
	m.root = t.TempDir()
	if _, err := m.jsHostCommand(&Spec{Name: JSHost}); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Errorf("no host script: %v", err)
	}
}

// A process's standard error reaches heron's after its name, line by line.
func TestStderrIsPrefixed(t *testing.T) {
	var b strings.Builder
	w := prefixWriter{w: &b, prefix: "probe: "}
	w.Write([]byte("one\ntwo\n"))
	if b.String() != "probe: one\nprobe: two\n" {
		t.Errorf("got %q", b.String())
	}
}

// ctx.node reads the checked tree, and an imported package, by path, and
// answers null where nothing is; shutdown ends a process that answers it.
func TestHostNodeAndShutdown(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.heron": "import = [\"std/types@1\"]\n\nPet: type {\n  name: String\n}\n"})
	m := NewManager(registry.New())
	if err := m.Load(context.Background(), checkDir(t, dir)); err != nil {
		t.Fatal(err)
	}
	h := hostServices{m}
	n, err := h.Node(context.Background(), &protocol.NodeParams{Path: "Pet"})
	if err != nil || n == nil || n.Path != "Pet" || len(n.Children) != 1 {
		t.Errorf("Pet: %+v %v", n, err)
	}
	if n, _ := h.Node(context.Background(), &protocol.NodeParams{Path: "types.Error"}); n == nil {
		t.Error("a node of an imported package isn't found")
	}
	if n, err := h.Node(context.Background(), &protocol.NodeParams{Path: "Nope"}); n != nil || err != nil {
		t.Errorf("nothing there: %+v %v", n, err)
	}
	p := &Process{Spec: &Spec{Name: "probe"}, Argv: []string{os.Args[0]}, Env: []string{"HERON_TEST_EXTENSION=1"}}
	if _, err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	exited := p.exited
	p.Shutdown()
	select {
	case <-exited:
	default:
		t.Error("the process is still running after shutdown")
	}
}
