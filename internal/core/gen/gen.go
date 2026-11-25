// Package gen is the generation framework: adapters read the resolved tree
// and return files, none with a config format of its own, and `heron
// generate <target>` runs the adapters registered for a target. Built-in
// adapters and those extensions implement register alike, through
// registry.Generator. The framework also writes the managed ignore block.
package gen

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/check"
	"github.com/helightdev/specs-v0/internal/core/diag"
	"github.com/helightdev/specs-v0/internal/core/registry"
	"github.com/helightdev/specs-v0/internal/core/resolve"
	"github.com/helightdev/specs-v0/internal/core/types"
)

// Input is what an adapter reads: the checked tree and the project
// directory files are written under.
type Input struct {
	Tree    *resolve.Tree
	Checker *types.Checker
	Dir     string
}

// ResolvedTree gives an adapter in another package the tree without
// importing gen, as the extension host's adapters do.
func (in *Input) ResolvedTree() (*resolve.Tree, *types.Checker) { return in.Tree, in.Checker }

// Result is what a run wrote.
type Result struct {
	Written   []string // paths changed, relative to the directory
	Unchanged []string
}

// Run runs the adapters of the targets, every target for none, writes their
// files under in.Dir, and updates the managed ignore block.
func Run(ctx context.Context, r *registry.Registry, in *Input, targets []string) (*Result, error) {
	all := len(targets) == 0
	if all {
		targets = r.Targets()
	}
	res := &Result{}
	ignored := []string{"/.heron/"}
	for _, t := range targets {
		gens := r.Generators(t)
		if len(gens) == 0 {
			return res, fmt.Errorf("no adapter is registered for target %q; targets: %s", t, strings.Join(r.Targets(), ", "))
		}
		for _, g := range gens {
			files, err := g.Generate(ctx, in)
			if err != nil {
				return res, fmt.Errorf("%s (%s): %w", g.Path, t, err)
			}
			for _, f := range files {
				rel, err := inside(f.Path)
				if err != nil {
					return res, fmt.Errorf("%s: %w", g.Path, err)
				}
				changed, err := writeIfChanged(filepath.Join(in.Dir, rel), f.Content)
				if err != nil {
					return res, err
				}
				if changed {
					res.Written = append(res.Written, rel)
				} else {
					res.Unchanged = append(res.Unchanged, rel)
				}
				if f.Ignored && !strings.HasPrefix(rel, ".heron/") {
					ignored = append(ignored, "/"+rel)
				}
			}
		}
	}
	if all {
		changed, err := IgnoreSection{Owner: OwnerGenerate}.WriteIgnore(in.Dir, ignored)
		if err != nil {
			return res, err
		}
		if changed {
			res.Written = append(res.Written, ".gitignore")
		}
	}
	return res, nil
}

// inside checks that a generated path stays in the project directory.
func inside(p string) (string, error) {
	c := filepath.ToSlash(filepath.Clean(p))
	if filepath.IsAbs(p) || c == ".." || strings.HasPrefix(c, "../") || c == "." {
		return "", fmt.Errorf("generated path %q is outside the project directory", p)
	}
	return c, nil
}

func writeIfChanged(path string, content []byte) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, content, 0o644)
}

// Register installs `heron generate` and the built-in adapters.
func Register(r *registry.Registry) {
	r.MustAddCommand(registry.Command{
		Name:    "generate",
		Summary: "Runs the generation targets, plus setup files and the managed ignore block",
		Usage:   "[--entry file] [targets…]",
		Run:     func(env *registry.Env, args []string) int { return command(env, args, r) },
	})
	if err := r.SetGenerator(registry.Generator{Path: "js.ts", Target: "ts", Generate: func(ctx context.Context, in any) ([]registry.GeneratedFile, error) {
		return TS(in.(*Input))
	}}); err != nil {
		panic(err)
	}
}

func command(env *registry.Env, args []string, r *registry.Registry) int {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	entry := fs.String("entry", ".", "the entry point: a file, or a directory's index.heron")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	e := *entry
	if !filepath.IsAbs(e) {
		e = filepath.Join(env.Dir, e)
	}
	if st, err := os.Stat(e); err == nil && st.IsDir() {
		e = filepath.Join(e, "index.heron")
	}
	id, err := resolve.Canonical(e)
	if err != nil {
		fmt.Fprintln(env.Stderr, "heron generate:", err)
		return 2
	}
	s := check.NewSession()
	res := s.Check(id)
	if res.Diags.HasErrors() {
		for _, d := range res.Diags {
			if d.Severity == diag.Error {
				fmt.Fprintln(env.Stderr, diag.Format(d, s.Pos))
			}
		}
		fmt.Fprintln(env.Stderr, "heron generate: the tree doesn't check")
		return 1
	}
	defer r.Close()
	ctx := context.Background()
	if err := r.LoadTree(ctx, res); err != nil {
		fmt.Fprintln(env.Stderr, "heron generate:", err)
		return 1
	}
	out, err := Run(ctx, r, &Input{Tree: res.Tree, Checker: res.Checker, Dir: env.Dir}, fs.Args())
	if out != nil {
		for _, w := range out.Written {
			fmt.Fprintln(env.Stdout, "wrote", w)
		}
	}
	if err != nil {
		fmt.Fprintln(env.Stderr, "heron generate:", err)
		return 1
	}
	return 0
}
