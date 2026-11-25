// Package release is `heron release`: the next version of a project that
// implements a spec, and the check of its versions and release tags.
package release

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/check"
	"github.com/helightdev/specs-v0/internal/core/diag"
	"github.com/helightdev/specs-v0/internal/core/gen"
	"github.com/helightdev/specs-v0/internal/core/project"
	"github.com/helightdev/specs-v0/internal/core/registry"
	"github.com/helightdev/specs-v0/internal/core/resolve"
)

// Register installs `heron release`.
func Register(r *registry.Registry) {
	r.MustAddCommand(registry.Command{
		Name:    "release",
		Summary: "Computes and checks the versions of a project that implements a spec",
		Usage:   "next [--entry file] [--tag label] | check [--entry file] [--git]",
		Run:     func(env *registry.Env, args []string) int { return command(env, args) },
	})
	// `heron generate conformance`: the report of a project that
	// implements a spec, as Markdown and JSON; nothing for other trees.
	if err := r.SetGenerator(registry.Generator{Path: "project.conformance", Target: "conformance", Generate: func(ctx context.Context, in any) ([]registry.GeneratedFile, error) {
		gi := in.(*gen.Input)
		p := project.Find(gi.Tree, gi.Checker)
		if p == nil {
			return nil, nil
		}
		rep := p.Conformance()
		return []registry.GeneratedFile{
			{Path: ConformanceDir + "/conformance.md", Content: rep.Markdown(), Ignored: true},
			{Path: ConformanceDir + "/conformance.json", Content: rep.JSON(), Ignored: true},
		}, nil
	}}); err != nil {
		panic(err)
	}
}

// ConformanceDir is where `heron generate conformance` writes its report.
const ConformanceDir = ".heron/conformance"

func command(env *registry.Env, args []string) int {
	if len(args) == 0 || (args[0] != "next" && args[0] != "check") {
		fmt.Fprintln(env.Stderr, "usage: heron release next [--entry file] [--tag label]")
		fmt.Fprintln(env.Stderr, "       heron release check [--entry file] [--git]")
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("release "+sub, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	entry := fs.String("entry", ".", "the entry point: a file, or a directory's index.heron")
	tag := fs.String("tag", "", "next: the metadata label, as build, rc or beta")
	git := fs.Bool("git", false, "check: also require a git tag v<version> for every release")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	p, s, res, code := load(env, *entry)
	if p == nil {
		return code
	}
	switch sub {
	case "next":
		if res.Diags.HasErrors() {
			printErrors(env.Stderr, s, res.Diags)
			fmt.Fprintln(env.Stderr, "heron release: the tree doesn't check")
			return 1
		}
		v, err := p.NextVersion(*tag)
		if err != nil {
			fmt.Fprintln(env.Stderr, "heron release:", err)
			return 1
		}
		fmt.Fprintln(env.Stdout, v.String())
		return 0
	default:
		var bad diag.List
		for _, d := range res.Diags {
			if d.Severity == diag.Error && (d.Code == "version" || d.Code == "project") {
				bad = append(bad, d)
			}
		}
		if *git {
			tags, err := gitTags(filepath.Dir(string(res.Tree.Entry.ID)))
			if err != nil {
				fmt.Fprintln(env.Stderr, "heron release:", err)
				return 1
			}
			for _, r := range p.ReleaseList() {
				if r.Err == nil && !tags[r.Version.GitTag()] {
					start, end := r.Node.Decl.Span()
					bad.Errorf(string(r.Node.Decl.File.ID), start, end, "version", "release %s has no git tag %s", r.Version, r.Version.GitTag())
				}
			}
		}
		if len(bad) > 0 {
			printErrors(env.Stderr, s, bad)
			return 1
		}
		fmt.Fprintf(env.Stdout, "%s implements %s at %s, version %s (%s)\n", projectName(p), p.SpecName(), p.At, orNone(p.Version), p.Scheme)
		return 0
	}
}

func load(env *registry.Env, entry string) (*project.Project, *check.Session, *check.Result, int) {
	e := entry
	if !filepath.IsAbs(e) {
		e = filepath.Join(env.Dir, e)
	}
	if st, err := os.Stat(e); err == nil && st.IsDir() {
		e = filepath.Join(e, "index.heron")
	}
	id, err := resolve.Canonical(e)
	if err != nil {
		fmt.Fprintln(env.Stderr, "heron release:", err)
		return nil, nil, nil, 2
	}
	s := check.NewSession()
	res := s.Check(id)
	p := project.Find(res.Tree, res.Checker)
	if p == nil {
		fmt.Fprintln(env.Stderr, "heron release: the tree implements no spec; its root has no implements(spec, at)")
		return nil, nil, nil, 1
	}
	return p, s, res, 0
}

func printErrors(w io.Writer, s *check.Session, ds diag.List) {
	for _, d := range ds.Sort() {
		if d.Severity == diag.Error {
			fmt.Fprintln(w, diag.Format(d, s.Pos))
		}
	}
}

func gitTags(dir string) (map[string]bool, error) {
	cmd := exec.Command("git", "tag", "--list")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git tag --list in %s: %v", dir, err)
	}
	tags := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			tags[l] = true
		}
	}
	return tags, nil
}

func projectName(p *project.Project) string {
	if p.Node.Name != "" {
		return p.Node.Name
	}
	return "The project"
}

func orNone(s string) string {
	if s == "" {
		return "unset"
	}
	return s
}
