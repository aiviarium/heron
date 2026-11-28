package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/eval"
	"github.com/aiviarium/heron/internal/core/eval/host"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/records"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/setup"
	"github.com/aiviarium/heron/internal/core/types"
)

func runCommand(r *registry.Registry) registry.Command {
	return registry.Command{
		Name:    "run",
		Summary: "Runs a task by its path, with its params as options",
		Usage:   "[--json] [--yes] [--undo-on-failure] [--entry file] <task> [--param value…]",
		Run:     func(env *registry.Env, args []string) int { return runTask(env, args, r) },
	}
}

// Exit statuses of heron run.
const (
	exitFailed = 1 // the task or a task it needs failed
	exitUsage  = 2 // bad arguments, a missing parameter, or no such task
)

type runOpts struct {
	json, yes, undo bool
	entry, task     string
	params          []string
}

func parseRunArgs(args []string) (runOpts, error) {
	var o runOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		if o.task != "" {
			o.params = append(o.params, args[i:]...)
			break
		}
		switch {
		case a == "--json":
			o.json = true
		case a == "--yes":
			o.yes = true
		case a == "--undo-on-failure":
			o.undo = true
		case a == "--entry" && i+1 < len(args):
			i++
			o.entry = args[i]
		case strings.HasPrefix(a, "--entry="):
			o.entry = strings.TrimPrefix(a, "--entry=")
		case strings.HasPrefix(a, "-"):
			return o, fmt.Errorf("unknown option %s", a)
		default:
			o.task = a
		}
	}
	if o.task == "" {
		return o, fmt.Errorf("name the task to run, as in heron run seed_local")
	}
	return o, nil
}

func runTask(env *registry.Env, args []string, r *registry.Registry) int {
	fail := func(code int, format string, a ...any) int {
		fmt.Fprintf(env.Stderr, "heron run: "+format+"\n", a...)
		return code
	}
	o, err := parseRunArgs(args)
	if err != nil {
		return fail(exitUsage, "%v", err)
	}
	var entryArgs []string
	if o.entry != "" {
		entryArgs = []string{o.entry}
	}
	ids, err := entries(env, entryArgs)
	if err != nil {
		return fail(exitUsage, "%v", err)
	}
	if o.task == "setup" {
		// The packages the tree imports from git are installed before it's
		// checked: a tree that imports one doesn't check without it.
		if err := installGitPackages(env, ids[0], o.params); err != nil {
			return fail(exitFailed, "%v", err)
		}
	}
	s := check.NewSession()
	res := s.Check(ids[0])
	if res.Diags.HasErrors() {
		var errs diag.List
		for _, d := range res.Diags {
			if d.Severity == diag.Error {
				errs = append(errs, d)
			}
		}
		printDiags(env.Stderr, env, s, errs)
		return fail(exitFailed, "the tree doesn't check")
	}
	task := findTask(res, o.task)
	if task == nil || !isTask(res.Checker, task) {
		return fail(exitUsage, "no task %s", o.task)
	}
	in := bufio.NewReader(env.Stdin)
	tty := isTTY(env.Stdin)
	vars, err := taskArgs(res.Checker, task, o.params, in, env.Stderr, tty)
	if err != nil {
		return fail(exitUsage, "%v", err)
	}
	order, err := needsOrder(res.Checker, task)
	if err != nil {
		return fail(exitUsage, "%v", err)
	}
	for _, t := range order {
		if !types.HasKindFlag(t, "confirm") || o.yes {
			continue
		}
		if !tty {
			return fail(exitUsage, "%s asks before it runs; pass --yes to run it without a terminal", t.Path())
		}
		fmt.Fprintf(env.Stderr, "Run %s? [y/N] ", t.Path())
		line, _ := in.ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return fail(exitFailed, "%s was not confirmed", t.Path())
		}
	}

	m := eval.New(res.Tree, res.Checker, r, host.Caps(func(l string) { fmt.Fprintln(env.Stderr, l) }))
	m.Dir = env.Dir
	defer m.Close()
	rec := records.NewRunRecorder(task.Path(), string(ids[0]), vars, nil)
	m.OnEvent = rec.OnEvent
	ctx := context.Background()
	code, result, runErr := runOrder(ctx, m, res.Checker, order, task, vars, o.undo, rec, env.Stderr)
	store := records.Open(filepath.Dir(string(ids[0])), records.Config{}, r)
	if _, err := store.Write(records.RunKind, rec.Data(result, runErr, code)); err != nil {
		fmt.Fprintf(env.Stderr, "heron run: the run wasn't recorded: %v\n", err)
	}
	if code != 0 || (result == nil && !o.json) {
		return code
	}
	writeResult(env.Stdout, result, o.json)
	return 0
}

// runOrder runs the tasks in order, the asked one last, and undoes the
// finished ones on a failure when asked to.
func runOrder(ctx context.Context, m *eval.Machine, c *types.Checker, order []*resolve.Node, task *resolve.Node,
	vars map[string]eval.Value, undo bool, rec *records.RunRecorder, w io.Writer) (int, eval.Value, error) {
	var done []*resolve.Node
	var result eval.Value
	for _, t := range order {
		args := eval.Args{}
		role := "needed"
		if t == task {
			args.Named = vars
			role = "task"
		}
		rec.StartTask(t.Path(), role)
		v, err := m.Call(ctx, t, args)
		rec.FinishTask(v, err)
		if err != nil {
			fmt.Fprintf(w, "heron run: %s failed: %v\n", t.Path(), err)
			if undo {
				undoAll(ctx, m, c, done, rec, w)
			}
			return exitFailed, nil, fmt.Errorf("%s failed: %w", t.Path(), err)
		}
		done = append(done, t)
		result = v
	}
	return 0, result, nil
}

// findTask finds a task by its path from the root, or else inside the
// tree's root project, std/impl's project at ".", so `heron run setup`
// runs that project's setup task.
func findTask(res *check.Result, p string) *resolve.Node {
	if n := res.Tree.Find(p); n != nil {
		return n
	}
	for _, r := range res.Tree.Root.Children {
		k := res.Checker.KindOf(r)
		if k == nil || !types.IsStd(k, "impl", "impl.project") {
			continue
		}
		e := project.KindArg(res.Checker, r, "path")
		if e == nil {
			continue
		}
		if path, ok := resolve.StringValue(e.Syntax()); !ok || path != "." {
			continue
		}
		n := r
		for _, seg := range strings.Split(p, ".") {
			if n = n.Child(seg); n == nil {
				break
			}
		}
		if n != nil {
			return n
		}
	}
	return nil
}

func isTask(c *types.Checker, n *resolve.Node) bool {
	k := c.KindOf(n)
	return k != nil && types.IsStd(k, "core", "core.task")
}

// writeResult prints a task's return value as data: a string as it is,
// anything else as JSON.
func writeResult(w io.Writer, v eval.Value, asJSON bool) {
	if s, ok := v.(string); ok && !asJSON {
		fmt.Fprintln(w, s)
		return
	}
	b, err := json.MarshalIndent(eval.Plain(v), "", "  ")
	if err != nil {
		fmt.Fprintln(w, eval.Str(v))
		return
	}
	fmt.Fprintln(w, string(b))
}

// undoAll runs the @undo task of every task that finished, newest first.
func undoAll(ctx context.Context, m *eval.Machine, c *types.Checker, done []*resolve.Node, rec *records.RunRecorder, w io.Writer) {
	for i := len(done) - 1; i >= 0; i-- {
		for _, u := range annotationRefs(done[i], "undo") {
			fmt.Fprintf(w, "heron run: undoing %s with %s\n", done[i].Path(), u.Path())
			rec.StartTask(u.Path(), "undo")
			v, err := m.Call(ctx, u, eval.Args{})
			rec.FinishTask(v, err)
			if err != nil {
				fmt.Fprintf(w, "heron run: %s failed: %v\n", u.Path(), err)
			}
		}
	}
}

// annotationRefs returns the nodes an annotation's arguments name, as in
// @needs(a, b) or @undo(rollback).
func annotationRefs(n *resolve.Node, name string) []*resolve.Node {
	var out []*resolve.Node
	for _, s := range n.Annotations {
		a := ast.Annotation{N: s.N}
		if resolve.PathText(a.Path().N) != name {
			continue
		}
		for _, arg := range a.Args().Args() {
			var e ast.Expr
			switch x := arg.(type) {
			case ast.PositionalArg:
				e = x.Expr()
			case ast.NamedArg:
				e = x.Expr()
			}
			var exprs []ast.Expr
			if arr, ok := e.(ast.ArrayExpr); ok {
				exprs = arr.Exprs()
			} else {
				exprs = []ast.Expr{e}
			}
			for _, x := range exprs {
				pe, ok := x.(ast.PathExpr)
				if !ok {
					continue
				}
				segs := resolve.PathSegs(pe.Path().N)
				t := s.Scope.Lookup(segs[0], s.File, -1).Node
				for _, seg := range segs[1:] {
					if t != nil {
						t = t.Child(seg)
					}
				}
				if t != nil {
					out = append(out, t)
				}
			}
		}
	}
	return out
}

// needsOrder lists the tasks to run: what @needs names, transitively and
// each once, before the tasks that need it, and the task itself last.
func needsOrder(c *types.Checker, task *resolve.Node) ([]*resolve.Node, error) {
	var out []*resolve.Node
	state := map[*resolve.Node]int{} // 1 visiting, 2 done
	var visit func(n *resolve.Node, chain []string) error
	visit = func(n *resolve.Node, chain []string) error {
		chain = append(chain, n.Path())
		switch state[n] {
		case 1:
			return fmt.Errorf("@needs forms a cycle: %s", strings.Join(chain, " → "))
		case 2:
			return nil
		}
		state[n] = 1
		for _, d := range annotationRefs(n, "needs") {
			if !isTask(c, d) {
				return fmt.Errorf("%s needs %s, which is no task", n.Path(), d.Path())
			}
			if err := visit(d, chain); err != nil {
				return err
			}
		}
		state[n] = 2
		out = append(out, n)
		return nil
	}
	return out, visit(task, nil)
}

// taskArgs reads a task's parameters from `--name value` options, parsed
// by their types; missing required ones are asked for on a terminal.
func taskArgs(c *types.Checker, task *resolve.Node, opts []string, in *bufio.Reader, prompt io.Writer, tty bool) (map[string]eval.Value, error) {
	params := c.Params(task)
	byName := map[string]types.Param{}
	for _, p := range params {
		byName[p.Name] = p
	}
	raw := map[string][]string{}
	for i := 0; i < len(opts); i++ {
		a := opts[i]
		if !strings.HasPrefix(a, "--") {
			return nil, fmt.Errorf("unexpected argument %s; parameters are given as --name value", a)
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		name = strings.ReplaceAll(name, "-", "_")
		p, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("%s takes no parameter --%s", task.Path(), name)
		}
		if !hasVal {
			if typeName(p) == "Bool" && (i+1 >= len(opts) || strings.HasPrefix(opts[i+1], "--")) {
				val = "true"
			} else if i+1 < len(opts) {
				i++
				val = opts[i]
			} else {
				return nil, fmt.Errorf("--%s needs a value", name)
			}
		}
		raw[name] = append(raw[name], val)
	}
	out := map[string]eval.Value{}
	for _, p := range params {
		vals, given := raw[p.Name]
		if !given {
			if p.Optional {
				continue
			}
			if !tty {
				return nil, fmt.Errorf("%s needs --%s (%s)", task.Path(), p.Name, typeName(p))
			}
			for {
				fmt.Fprintf(prompt, "%s (%s): ", p.Name, typeName(p))
				line, err := in.ReadString('\n')
				line = strings.TrimSpace(line)
				if line == "" && err != nil {
					return nil, fmt.Errorf("%s needs --%s (%s)", task.Path(), p.Name, typeName(p))
				}
				v, perr := parseParam(c, task, p, []string{line})
				if perr == nil {
					out[p.Name] = v
					break
				}
				fmt.Fprintln(prompt, perr)
			}
			continue
		}
		v, err := parseParam(c, task, p, vals)
		if err != nil {
			return nil, err
		}
		out[p.Name] = v
	}
	return out, nil
}

// paramType is a parameter's type as written: its name, its first
// argument's name, and whether it is optional.
func paramType(p types.Param) (name, arg string) {
	if p.N == nil {
		return "", ""
	}
	t := ast.Param{N: p.N}.Type()
	if o, ok := t.(ast.OptionalType); ok {
		t = o.Type()
	}
	nt, ok := t.(ast.NamedType)
	if !ok {
		return "", ""
	}
	name = resolve.PathText(nt.Path().N)
	for _, a := range nt.Args().Args() {
		if pa, ok := a.(ast.PositionalArg); ok {
			if pe, ok := pa.Expr().(ast.PathExpr); ok {
				arg = resolve.PathText(pe.Path().N)
			}
			break
		}
	}
	return name, arg
}

func typeName(p types.Param) string {
	n, a := paramType(p)
	if n == "" {
		return "String"
	}
	if a != "" {
		return n + "(" + a + ")"
	}
	return n
}

// parseParam parses a parameter's command-line values by its type; a list
// takes repeated options or comma-separated values.
func parseParam(c *types.Checker, task *resolve.Node, p types.Param, vals []string) (eval.Value, error) {
	name, arg := paramType(p)
	if name == "List" || name == "Set" {
		var out []eval.Value
		for _, v := range vals {
			for _, part := range strings.Split(v, ",") {
				x, err := parseScalar(c, task, p.Name, arg, strings.TrimSpace(part))
				if err != nil {
					return nil, err
				}
				out = append(out, x)
			}
		}
		return out, nil
	}
	if len(vals) > 1 {
		return nil, fmt.Errorf("--%s is given more than once", p.Name)
	}
	return parseScalar(c, task, p.Name, name, vals[0])
}

func parseScalar(c *types.Checker, task *resolve.Node, param, typ, s string) (eval.Value, error) {
	switch typ {
	case "", "String", "Any":
		return s, nil
	case "Int":
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("--%s takes an Int, not %q", param, s)
		}
		return i, nil
	case "Float", "Decimal":
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("--%s takes a number, not %q", param, s)
		}
		return f, nil
	case "Bool":
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("--%s takes true or false, not %q", param, s)
		}
		return b, nil
	}
	sc := task.Body
	if sc == nil {
		sc = task.Decl.Scope
	}
	segs := strings.Split(typ, ".")
	d := sc.Lookup(segs[0], task.Decl.File, -1).Node
	for _, seg := range segs[1:] {
		if d != nil {
			d = d.Child(seg)
		}
	}
	if d != nil && c.IsEnum(d) {
		if c.Variant(d, s) == nil {
			var names []string
			for _, ch := range d.Children {
				names = append(names, ch.Name)
			}
			return nil, fmt.Errorf("--%s takes one of %s, not %q", param, strings.Join(names, ", "), s)
		}
		return s, nil
	}
	// Records and other types are given as JSON.
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		var v any
		dec := json.NewDecoder(strings.NewReader(s))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("--%s: %v", param, err)
		}
		return eval.FromPlain(v), nil
	}
	return s, nil
}

// isTTY reports whether the reader is a terminal, so missing parameters
// can be asked for.
func isTTY(r io.Reader) bool {
	if t, ok := r.(interface{ IsTerminal() bool }); ok {
		return t.IsTerminal()
	}
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// installGitPackages is setup's step before the check: it installs the git
// packages heron.lock pins, from the lock alone, then pins and installs the
// git imports of the tree the lock doesn't pin yet (all of them again with
// --update), so the tree that imports them checks and its setup task runs.
func installGitPackages(env *registry.Env, entry resolve.FileID, params []string) error {
	ctx := context.Background()
	update := false
	for _, p := range params {
		if p == "--update" || p == "--update=true" {
			update = true
		}
	}
	installed, err := setup.InstallFromLock(ctx, env.Dir, setup.ExecGit)
	for _, name := range installed {
		fmt.Fprintf(env.Stderr, "heron run: installed %s from heron.lock\n", name)
	}
	if err != nil {
		return err
	}
	t := check.NewSession().Resolver.Resolve(entry)
	pinned, err := setup.PinGitImports(ctx, env.Dir, t.GitImports, update, setup.ExecGit)
	for _, name := range pinned {
		fmt.Fprintf(env.Stderr, "heron run: pinned and installed %s\n", name)
	}
	return err
}
