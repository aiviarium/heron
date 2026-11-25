package extension

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiviarium/heron/internal/core/extension/protocol"
	"github.com/aiviarium/heron/internal/core/lint"
	"github.com/aiviarium/heron/internal/core/registry"
)

// The test binary doubles as an extension written in Go: with
// HERON_TEST_EXTENSION set it serves the protocol on stdio, implementing
// probe funcs that try to write files and reach the network.
func TestMain(m *testing.M) {
	if os.Getenv("HERON_TEST_EXTENSION") != "" {
		serveProbe()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type probe struct{ decls []protocol.Declaration }

func (p *probe) Initialize(ctx context.Context, in *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	version := protocol.Version
	if v := os.Getenv("HERON_TEST_PROTOCOL"); v != "" {
		version = v
	}
	out := &protocol.InitializeResult{Protocol: version, Extension: protocol.PeerInfo{Name: "probe", Version: "1.0.0"}, Capabilities: protocol.Capabilities{Methods: []string{"call"}}}
	for _, d := range in.Declarations {
		out.Implements = append(out.Implements, protocol.Implemented{Path: d.Path, Point: d.Point})
	}
	return out, nil
}

func (p *probe) Call(ctx context.Context, c *protocol.CallParams) (protocol.Value, error) {
	switch c.Target {
	case "write":
		if err := os.WriteFile(c.Args["path"].(string), []byte("x"), 0o644); err != nil {
			return "denied: " + err.Error(), nil
		}
		return "ok", nil
	case "dial":
		conn, err := net.DialTimeout("tcp", "1.1.1.1:53", 500*time.Millisecond)
		if err != nil {
			return "denied: " + err.Error(), nil
		}
		conn.Close()
		return "ok", nil
	case "throw":
		return nil, &protocol.Error{Code: protocol.CodeThrown, Type: "ProbeError", Message: "as declared", Data: map[string]any{"message": "as declared"}}
	}
	return nil, fmt.Errorf("no target %s", c.Target)
}

func (p *probe) Lint(ctx context.Context, l *protocol.LintParams) (*protocol.LintResult, error) {
	return &protocol.LintResult{}, nil
}
func (p *probe) Generate(ctx context.Context, g *protocol.GenerateParams) (*protocol.GenerateResult, error) {
	return &protocol.GenerateResult{}, nil
}
func (p *probe) Render(ctx context.Context, r *protocol.RenderParams) (*protocol.RenderResult, error) {
	return &protocol.RenderResult{}, nil
}
func (p *probe) ResourceSetup(context.Context, *protocol.ResourceParams) (*protocol.ResourceResult, error) {
	return &protocol.ResourceResult{}, nil
}
func (p *probe) ResourceReset(context.Context, *protocol.ResourceParams) (*protocol.ResourceResult, error) {
	return &protocol.ResourceResult{}, nil
}
func (p *probe) ResourceSnapshot(context.Context, *protocol.ResourceParams) (*protocol.ResourceResult, error) {
	return &protocol.ResourceResult{}, nil
}
func (p *probe) ResourceRestore(context.Context, *protocol.ResourceParams) (*protocol.ResourceResult, error) {
	return &protocol.ResourceResult{}, nil
}
func (p *probe) ResourceFork(context.Context, *protocol.ResourceParams) (*protocol.ResourceResult, error) {
	return &protocol.ResourceResult{}, nil
}
func (p *probe) ResourceTeardown(context.Context, *protocol.ResourceParams) (*protocol.ResourceResult, error) {
	return &protocol.ResourceResult{}, nil
}
func (p *probe) Shutdown(context.Context, *protocol.Empty) (*protocol.Empty, error) {
	go func() { time.Sleep(10 * time.Millisecond); os.Exit(0) }()
	return &protocol.Empty{}, nil
}

func serveProbe() {
	c := protocol.NewConn(os.Stdin, os.Stdout, protocol.Options{Handlers: protocol.ExtensionHandlers(&probe{})})
	c.Serve()
}

func TestExtensionRunsWithItsLimits(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERON_TEST_EXTENSION", "1")
	t.Setenv("HERON_TRUST_EXTENSIONS", "1")
	dir := writeTree(t, map[string]string{"index.heron": fmt.Sprintf(`import = ["std/schema@1", "std/types@1"]

probe: extension(command = [%q], files = ["./out"])

@extern(probe)
write(path: String): func(String)

@extern(probe)
dial: func(String)

@extern(probe)
@throws(Error)
throw: func(String)
`, self)})
	os.MkdirAll(filepath.Join(dir, "out"), 0o755)
	r := registry.New()
	lint.Register(r)
	m := Register(r)
	var warnings []string
	m.Warn = func(s string) { warnings = append(warnings, s) }
	m.Log = os.Stderr
	t.Cleanup(r.Close)
	res := checkDir(t, dir)
	if err := r.LoadTree(context.Background(), res); err != nil {
		t.Fatal(err)
	}
	call := func(name string, args map[string]any) (any, error) {
		f := r.Func(name)
		if f == nil {
			t.Fatalf("%s isn't registered", name)
		}
		return f.Call(context.Background(), &registry.FuncCall{Func: name, Args: args})
	}
	if out, err := call("write", map[string]any{"path": filepath.Join(dir, "out", "ok.txt")}); err != nil || out != "ok" {
		t.Fatalf("writing a declared file: %v %v (%v)", out, err, warnings)
	}
	_, err = call("throw", nil)
	var th *registry.Thrown
	if !errors.As(err, &th) || th.Type != "ProbeError" {
		t.Fatalf("a declared throw: %v", err)
	}
	if len(warnings) > 0 {
		t.Skipf("the sandbox isn't available here: %v", warnings)
	}
	out, err := call("write", map[string]any{"path": filepath.Join(dir, "elsewhere.txt")})
	if err != nil || !strings.HasPrefix(out.(string), "denied") {
		t.Fatalf("writing an undeclared file: %v %v", out, err)
	}
	out, err = call("dial", nil)
	if err != nil || !strings.HasPrefix(out.(string), "denied") {
		t.Fatalf("reaching the network without declaring it: %v %v", out, err)
	}
}
