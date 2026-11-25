package extension

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aiviarium/heron/internal/core/lint"
	"github.com/aiviarium/heron/internal/core/registry"
)

// A host that allows the namespaces but refuses the sandbox's mounts, as
// hardened CI runners do, runs the extension without the sandbox and warns,
// instead of failing its handshake.
func TestSandboxFallsBackWhenMountsAreDenied(t *testing.T) {
	probeOnce = sync.Once{}
	probeOnce.Do(func() { probeOK = false })
	t.Cleanup(func() { probeOnce = sync.Once{}; probeOK = false })

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
`, self)})
	os.MkdirAll(filepath.Join(dir, "out"), 0o755)
	r := registry.New()
	lint.Register(r)
	m := Register(r)
	var warnings []string
	m.Warn = func(s string) { warnings = append(warnings, s) }
	t.Cleanup(r.Close)
	if err := r.LoadTree(context.Background(), checkDir(t, dir)); err != nil {
		t.Fatal(err)
	}
	out, err := r.Func("write").Call(context.Background(), &registry.FuncCall{Func: "write", Args: map[string]any{"path": filepath.Join(dir, "out", "ok.txt")}})
	if err != nil || out != "ok" {
		t.Fatalf("the extension should run without the sandbox: %v %v", out, err)
	}
	if len(warnings) == 0 {
		t.Fatal("running without the sandbox should warn")
	}
}
