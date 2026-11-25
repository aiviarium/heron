package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/helightdev/specs-v0/internal/core/cli"
	"github.com/helightdev/specs-v0/internal/core/registry"
)

func env(dir string) (*registry.Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return &registry.Env{Dir: dir, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}, &out, &errb
}

// TestSampleCommand proves the command extension point: a command
// registered from outside the unit runs through the same dispatcher.
func TestSampleCommand(t *testing.T) {
	r := registry.New()
	cli.Register(r)
	r.MustAddCommand(registry.Command{Name: "sample", Summary: "A sample extension command", Run: func(e *registry.Env, args []string) int {
		e.Stdout.Write([]byte("sample " + strings.Join(args, ",")))
		return 3
	}})
	e, out, _ := env(t.TempDir())
	if code := cli.Main(r, []string{"sample", "a", "b"}, e); code != 3 || out.String() != "sample a,b" {
		t.Fatalf("code %d, out %q", code, out)
	}
	if err := r.AddCommand(registry.Command{Name: "sample", Run: func(*registry.Env, []string) int { return 0 }}); err == nil {
		t.Fatal("a second registration of a name must fail")
	}
	e, _, errb := env(t.TempDir())
	if code := cli.Main(r, []string{"nope"}, e); code != 2 || !strings.Contains(errb.String(), "sample") {
		t.Fatalf("unknown command: code %d, usage %q", code, errb)
	}
	if !strings.Contains(errb.String(), `heron: unknown command "nope"`) {
		t.Errorf("unknown command: %q", errb)
	}
	for args, want := range map[string]int{"": 2, "help": 0, "-h": 0, "--help": 0} {
		e, out, errb := env(t.TempDir())
		var a []string
		if args != "" {
			a = []string{args}
		}
		if code := cli.Main(r, a, e); code != want || out.Len() != 0 || !strings.Contains(errb.String(), "  sample     A sample extension command") {
			t.Errorf("heron %s: code %d, out %q, usage %q", args, code, out, errb)
		}
	}
	e, out, _ = env(t.TempDir())
	if code := cli.Main(r, []string{"help", "sample"}, e); code != 0 || !strings.HasPrefix(out.String(), "usage: heron sample") {
		t.Errorf("help sample: code %d, out %q", code, out)
	}
}

func TestFmt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.heron")
	os.WriteFile(p, []byte("a: b {\nc = 1  \n}"), 0o644)
	r := registry.New()
	cli.Register(r)
	e, out, _ := env(dir)
	if code := cli.Main(r, []string{"fmt", "--check"}, e); code != 1 || strings.TrimSpace(out.String()) != "a.heron" {
		t.Fatalf("check: code %d, out %q", code, out)
	}
	e, _, _ = env(dir)
	if code := cli.Main(r, []string{"fmt"}, e); code != 0 {
		t.Fatalf("fmt: code %d", code)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "a: b {\n  c = 1\n}\n" {
		t.Fatalf("formatted: %q", got)
	}
	e, _, _ = env(dir)
	if code := cli.Main(r, []string{"fmt", "--check"}, e); code != 0 {
		t.Fatalf("check after fmt: code %d", code)
	}
}
