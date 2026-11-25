package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/helightdev/specs-v0/internal/core/ast"
	"github.com/helightdev/specs-v0/internal/core/check"
	"github.com/helightdev/specs-v0/internal/core/diag"
	"github.com/helightdev/specs-v0/internal/core/lint"
	"github.com/helightdev/specs-v0/internal/core/registry"
	"github.com/helightdev/specs-v0/internal/core/resolve"
)

func checkCommand() registry.Command {
	return registry.Command{
		Name:    "check",
		Summary: "Parses, resolves and type-checks the tree from the entry point",
		Usage:   "[--json] [--watch] [entry points…]",
		Run:     func(env *registry.Env, args []string) int { return runCheck(env, args, nil) },
	}
}

func lintCommand(r *registry.Registry) registry.Command {
	return registry.Command{
		Name:    "lint",
		Summary: "Runs the core lints and those packages add",
		Usage:   "[--json] [--fix] [entry points…]",
		Run:     func(env *registry.Env, args []string) int { return runLint(env, args, r) },
	}
}

// entries turns arguments into entry points: a file, or a directory's
// index.heron, defaulting to index.heron in the working directory.
func entries(env *registry.Env, args []string) ([]resolve.FileID, error) {
	if len(args) == 0 {
		args = []string{"."}
	}
	var out []resolve.FileID
	for _, a := range args {
		if !filepath.IsAbs(a) {
			a = filepath.Join(env.Dir, a)
		}
		if st, err := os.Stat(a); err == nil && st.IsDir() {
			a = filepath.Join(a, "index.heron")
		}
		if _, err := os.Stat(a); err != nil {
			return nil, fmt.Errorf("no entry point %s", a)
		}
		id, err := resolve.Canonical(a)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// printDiags writes diagnostics with paths relative to the working
// directory and returns how many are errors.
func printDiags(w io.Writer, env *registry.Env, s *check.Session, ds diag.List) int {
	errs := 0
	for _, d := range ds {
		if d.Severity == diag.Error {
			errs++
		}
		fmt.Fprintln(w, relPaths(env, diag.Format(d, s.Pos)))
	}
	return errs
}

func relPaths(env *registry.Env, line string) string {
	dir := env.Dir
	if id, err := resolve.Canonical(dir); err == nil {
		dir = string(id)
	}
	return strings.ReplaceAll(line, dir+string(filepath.Separator), "")
}

func runCheck(env *registry.Env, args []string, stop <-chan struct{}) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	asJSON := fs.Bool("json", false, "print the diagnostics and the resolved tree as JSON")
	watch := fs.Bool("watch", false, "check again whenever a file of the tree changes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ids, err := entries(env, fs.Args())
	if err != nil {
		fmt.Fprintln(env.Stderr, "heron check:", err)
		return 2
	}
	s := check.NewSession()
	run := func() (int, []*check.Result) {
		errs := 0
		var results []*check.Result
		for _, id := range ids {
			res := s.Check(id)
			results = append(results, res)
			if *asJSON {
				continue
			}
			errs += printDiags(env.Stdout, env, s, res.Diags)
		}
		if *asJSON {
			var docs []jsonResult
			for _, r := range results {
				docs = append(docs, encodeResult(env, s, r))
				if r.Diags.HasErrors() {
					errs++
				}
			}
			enc := json.NewEncoder(env.Stdout)
			enc.SetIndent("", "  ")
			enc.Encode(docs)
		}
		return errs, results
	}
	errs, results := run()
	if !*watch {
		if errs > 0 {
			return 1
		}
		return 0
	}
	stamp := stamps(results)
	fmt.Fprintf(env.Stderr, "heron check: watching %d files\n", len(stamp))
	for {
		select {
		case <-stop:
			return 0
		case <-time.After(200 * time.Millisecond):
		}
		if next := stamps(results); !sameStamps(next, stamp) {
			before := s.Recomputed
			errs, results = run()
			stamp = stamps(results)
			fmt.Fprintf(env.Stderr, "heron check: %d errors, %d nodes rechecked\n", errs, s.Recomputed-before)
		}
	}
}

// stamps records the size and modification time of every file on disk the
// results read.
func stamps(results []*check.Result) map[string]string {
	out := map[string]string{}
	add := func(f *resolve.File) {
		if f.ID.IsStd() {
			return
		}
		if st, err := os.Stat(string(f.ID)); err == nil {
			out[string(f.ID)] = fmt.Sprint(st.Size(), st.ModTime().UnixNano())
		}
	}
	for _, r := range results {
		for _, f := range r.Tree.Files {
			add(f)
		}
		for _, p := range r.Tree.Imports {
			if p.Tree != nil {
				for _, f := range p.Tree.Files {
					add(f)
				}
			}
		}
	}
	return out
}

func sameStamps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func runLint(env *registry.Env, args []string, r *registry.Registry) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	asJSON := fs.Bool("json", false, "print the findings as JSON")
	fix := fs.Bool("fix", false, "apply each finding's first fix that isn't a suggestion and print the rest")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ids, err := entries(env, fs.Args())
	if err != nil {
		fmt.Fprintln(env.Stderr, "heron lint:", err)
		return 2
	}
	s := check.NewSession()
	var all diag.List
	defer r.Close()
	for _, id := range ids {
		res := s.Check(id)
		all = append(all, res.Diags...)
		// Extensions register the lints the tree declares.
		if err := r.LoadTree(context.Background(), res); err != nil {
			fmt.Fprintln(env.Stderr, "heron lint:", err)
		}
		all = append(all, lint.Run(r, res)...)
	}
	all = all.Sort()
	if *fix {
		rest, n, err := applyFixes(all)
		if err != nil {
			fmt.Fprintln(env.Stderr, "heron lint:", err)
			return 1
		}
		fmt.Fprintf(env.Stderr, "heron lint: applied %d fixes\n", n)
		all = rest
	}
	if *asJSON {
		out := []jsonDiag{}
		for _, d := range all {
			out = append(out, encodeDiag(env, s, d))
		}
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
	} else {
		printDiags(env.Stdout, env, s, all)
	}
	if len(all) > 0 {
		return 1
	}
	return 0
}

// --- JSON ---

type jsonDiag struct {
	diag.Diagnostic
	Line   int `json:"line"`
	Column int `json:"column"`
}

type jsonNode struct {
	Name     string      `json:"name,omitempty"`
	Path     string      `json:"path"`
	Kind     string      `json:"kind,omitempty"`
	Type     string      `json:"type,omitempty"`
	Value    any         `json:"value,omitempty"`
	File     string      `json:"file,omitempty"`
	Line     int         `json:"line,omitempty"`
	Children []*jsonNode `json:"children,omitempty"`
}

type jsonResult struct {
	Entry       string     `json:"entry"`
	Diagnostics []jsonDiag `json:"diagnostics"`
	Tree        *jsonNode  `json:"tree"`
}

func encodeDiag(env *registry.Env, s *check.Session, d diag.Diagnostic) jsonDiag {
	l, c := s.Pos(d.File, d.Start)
	d.File = relPaths(env, d.File)
	return jsonDiag{Diagnostic: d, Line: l, Column: c}
}

func encodeResult(env *registry.Env, s *check.Session, r *check.Result) jsonResult {
	out := jsonResult{Entry: relPaths(env, string(r.Tree.Entry.ID)), Diagnostics: []jsonDiag{}}
	for _, d := range r.Diags {
		out.Diagnostics = append(out.Diagnostics, encodeDiag(env, s, d))
	}
	var enc func(n *resolve.Node) *jsonNode
	enc = func(n *resolve.Node) *jsonNode {
		j := &jsonNode{Name: n.Name, Path: n.Path()}
		if k := r.Checker.KindOf(n); k != nil {
			j.Kind = k.Path()
		} else if t := r.Checker.TypeOf(n); t != nil {
			j.Type = t.Path()
		}
		if n.Decl.File != nil && n.Decl.N != nil {
			j.File = relPaths(env, string(n.Decl.File.ID))
			start, _ := n.Decl.Span()
			j.Line, _ = s.Pos(string(n.Decl.File.ID), start)
		}
		if n.Value != nil && len(n.Children) == 0 {
			j.Value = literal(n.Value)
		}
		for _, c := range n.Children {
			j.Children = append(j.Children, enc(c))
		}
		return j
	}
	out.Tree = enc(r.Tree.Root)
	return out
}

// literal returns a value as JSON where it's a literal, and its source text
// otherwise; evaluation comes with the interpreter.
func literal(e ast.Expr) any {
	switch e := e.(type) {
	case ast.Literal:
		switch {
		case e.KwTrue() != nil:
			return true
		case e.KwFalse() != nil:
			return false
		case e.Raw() != nil:
			return resolve.RawValue(e.Raw().Text)
		case e.Number() != nil:
			return json.Number(e.Number().Text)
		}
	case ast.StringLit:
		if s, ok := resolve.StringValue(e.N); ok {
			return s
		}
	case ast.ArrayExpr:
		out := []any{}
		for _, x := range e.Exprs() {
			out = append(out, literal(x))
		}
		return out
	default:
	}
	return map[string]string{"source": strings.TrimSpace(e.Syntax().Text())}
}
