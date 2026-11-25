// Package setup backs std/repo: the toolchain check, the packages and
// sources pinned in heron.lock, and the generated files `heron run setup`
// writes. Its funcs run inside eval and reach the tree, the project's root
// and processes through the machine that calls them, so a fake process
// capability tests them and every process they start is in the run record.
package setup

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/eval"
	"github.com/aiviarium/heron/internal/core/gen"
	"github.com/aiviarium/heron/internal/core/registry"
)

// IgnoreWriter writes setup's entries of the managed ignore block. The
// generation framework owns the block; setup writes its own section of it
// through gen.IgnoreSection.
type IgnoreWriter interface {
	// WriteIgnore makes the managed block of the ignore file under root
	// hold exactly entries, keeping everything outside the block, and
	// reports whether the file changed.
	WriteIgnore(root string, entries []string) (changed bool, err error)
}

// Installer returns the command that installs a tool at a version, or
// false when heron doesn't know how.
type Installer func(version string) (eval.Command, bool)

// Options configures the setup funcs.
type Options struct {
	Ignore     IgnoreWriter         // nil uses gen's section for setup
	Installers map[string]Installer // nil uses DefaultInstallers
}

// DefaultInstallers install package managers through npm; node itself is
// left to the machine's version manager.
func DefaultInstallers() map[string]Installer {
	npm := func(tool string) Installer {
		return func(v string) (eval.Command, bool) {
			return eval.Command{Path: "npm", Args: []string{"install", "--global", tool + "@" + v}}, true
		}
	}
	return map[string]Installer{"pnpm": npm("pnpm"), "yarn": npm("yarn")}
}

// Register installs the Go side of std/repo's funcs and of std/spec's
// git(…).
func Register(r *registry.Registry, o Options) {
	if o.Ignore == nil {
		o.Ignore = gen.IgnoreSection{Owner: gen.OwnerSetup}
	}
	if o.Installers == nil {
		o.Installers = DefaultInstallers()
	}
	r.MustAddFunc(registry.Func{Name: "spec.git", Summary: "a git source at a ref", Call: gitSource})
	r.MustAddFunc(registry.Func{Name: "repo.ensure_toolchain", Summary: "checks or installs the toolchain",
		Call: func(ctx context.Context, c *registry.FuncCall) (any, error) { return ensureToolchain(ctx, c, o) }})
	r.MustAddFunc(registry.Func{Name: "repo.install_packages", Summary: "pins and installs packages and sources",
		Call: func(ctx context.Context, c *registry.FuncCall) (any, error) { return installPackages(ctx, c, r) }})
	r.MustAddFunc(registry.Func{Name: "repo.generate", Summary: "writes the generated files",
		Call: func(ctx context.Context, c *registry.FuncCall) (any, error) { return generate(ctx, r, o) }})
}

// fail is a SetupError, which `attempt(…)` can catch.
func fail(format string, a ...any) error {
	return &registry.Thrown{Type: "SetupError", Fields: map[string]any{"message": fmt.Sprintf(format, a...)}}
}

func machine(ctx context.Context) (*eval.Machine, error) {
	m := eval.MachineOf(ctx)
	if m == nil {
		return nil, fmt.Errorf("setup: called outside a run")
	}
	return m, nil
}

// run starts a process through the machine's capability and records it.
func run(ctx context.Context, m *eval.Machine, dir string, path string, args ...string) (eval.Exit, error) {
	if m.Caps.Processes == nil {
		return eval.Exit{}, fmt.Errorf("setup: this run can't start processes")
	}
	c := eval.Command{Path: path, Args: args, Dir: dir}
	if m.Caps.Env != nil {
		c.Env = m.Caps.Env.Environ()
	}
	exit, err := m.Caps.Processes.Run(ctx, c)
	code := int64(exit.Code)
	if err != nil {
		code = -1
	}
	vargs := make([]eval.Value, len(args))
	for i, a := range args {
		vargs[i] = a
	}
	m.Report(eval.Event{Kind: eval.EventProcess, Name: path, Args: map[string]eval.Value{"args": vargs},
		Value: &eval.Record{Type: "Process", Fields: map[string]eval.Value{"exit": code}}, Err: err})
	return exit, err
}

func gitSource(_ context.Context, c *registry.FuncCall) (any, error) {
	src := map[string]any{"url": c.Args["url"]}
	if ref, ok := c.Args["ref"].(string); ok {
		src["ref"] = ref
	}
	return src, nil
}

var versionRE = regexp.MustCompile(`\d+(\.\d+)*`)

// matches reports whether a found version has the wanted leading numbers.
func matches(found, wants string) bool {
	f, w := strings.Split(found, "."), strings.Split(strings.TrimPrefix(wants, "v"), ".")
	if len(w) > len(f) {
		return false
	}
	for i := range w {
		if w[i] != f[i] {
			return false
		}
	}
	return true
}

func toolVersion(ctx context.Context, m *eval.Machine, tool string) string {
	exit, err := run(ctx, m, m.Dir, tool, "--version")
	if err != nil || exit.Code != 0 {
		return ""
	}
	return versionRE.FindString(string(exit.Stdout))
}

func ensureToolchain(ctx context.Context, c *registry.FuncCall, o Options) (any, error) {
	m, err := machine(ctx)
	if err != nil {
		return nil, err
	}
	tools, _ := c.Args["tools"].(map[string]any)
	install, _ := c.Args["install"].(bool)
	names := make([]string, 0, len(tools))
	for n := range tools {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []any
	for _, name := range names {
		wants := fmt.Sprint(tools[name])
		found := toolVersion(ctx, m, name)
		installed := false
		if !matches(found, wants) {
			have := "it isn't on PATH"
			if found != "" {
				have = "found " + found
			}
			if !install {
				return nil, fail("the toolchain wants %s %s, but %s; install it, or run setup with --install_toolchain", name, wants, have)
			}
			inst, ok := o.Installers[name]
			var cmd eval.Command
			if ok {
				cmd, ok = inst(wants)
			}
			if !ok {
				return nil, fail("the toolchain wants %s %s, but %s, and heron can't install %s; install it with your version manager", name, wants, have, name)
			}
			exit, err := run(ctx, m, m.Dir, cmd.Path, cmd.Args...)
			if err != nil || exit.Code != 0 {
				return nil, fail("installing %s %s failed: %s", name, wants, failure(exit, err))
			}
			if found = toolVersion(ctx, m, name); !matches(found, wants) {
				return nil, fail("installed %s %s, but PATH still has %q", name, wants, found)
			}
			installed = true
		}
		t := map[string]any{"name": name, "wants": wants, "found": found, "installed": installed}
		out = append(out, t)
	}
	if out == nil {
		out = []any{}
	}
	return out, nil
}

func failure(exit eval.Exit, err error) string {
	if err != nil {
		return err.Error()
	}
	s := strings.TrimSpace(string(exit.Stderr))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return fmt.Sprintf("exit status %d %s", exit.Code, s)
}
