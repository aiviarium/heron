package specmodel

import (
	"regexp"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
)

// argSite is the syntax of a named argument of a node's kind slot.
func argSite(n *resolve.Node, name string) (ast.Expr, resolve.Site) {
	e := project.NamedArg(project.KindArgs(n), name)
	if e == nil {
		return nil, n.Decl
	}
	return e, resolve.Site{File: n.Decl.File, N: e.Syntax(), Scope: n.Decl.Scope}
}

// refsOf resolves an argument's references, one or a list.
func refsOf(c *types.Checker, n *resolve.Node, name string) []Ref {
	e, _ := argSite(n, name)
	if e == nil {
		return nil
	}
	var exprs []ast.Expr
	if arr, ok := e.(ast.ArrayExpr); ok {
		exprs = arr.Exprs()
	} else {
		exprs = []ast.Expr{e}
	}
	var out []Ref
	for _, x := range exprs {
		site := resolve.Site{File: n.Decl.File, N: x.Syntax(), Scope: n.Decl.Scope}
		out = append(out, Ref{Site: site, Target: project.RefTarget(c, x.Syntax(), n.Decl)})
	}
	return out
}

// Is reports whether a node is of one of the standard kinds, or of a kind
// that extends one.
func Is(c *types.Checker, n *resolve.Node, kinds ...string) bool {
	if n == nil {
		return false
	}
	p := project.KindPath(c, n)
	for _, k := range kinds {
		if p == k {
			return true
		}
	}
	return false
}

// StepTarget reports whether a node is a use case step or extension a
// reference names, as `request_refund.2a`.
func StepTarget(n *resolve.Node) bool { return n != nil && n.Shape == resolve.ShapeStep }

var placeholderRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func shapes(t *resolve.Tree, c *types.Checker, n *resolve.Node, ds *diag.List) {
	link := func(name string, ok func(*resolve.Node) bool, kinds string) {
		for _, r := range refsOf(c, n, name) {
			if r.Target == nil || ok(r.Target) {
				continue
			}
			errAt(ds, r.Site, "link", "%s takes %s, and %s is a %s", name, kinds, r.Target.Name, KindName(c, r.Target))
		}
	}
	goalOrQuality := func(x *resolve.Node) bool { return Is(c, x, "spec.goal", "spec.quality") }
	switch {
	case Is(c, n, "spec.rule"):
		link("serves", goalOrQuality, "a goal or a quality")
		link("derives", func(x *resolve.Node) bool { return Is(c, x, "spec.rule") }, "a rule")
		link("relies", func(x *resolve.Node) bool { return Is(c, x, "spec.assumption") }, "an assumption")
		for _, r := range refsOf(c, n, "by") {
			if r.Target != nil && !Is(c, r.Target, "spec.actor", "spec.part", "spec.persona") {
				errAt(ds, r.Site, "type", "%s is a %s; a rule's by names an actor or a part", r.Target.Name, KindName(c, r.Target))
			}
		}
		triggers(c, n, ds)
		derivesCycle(c, n, ds)
		revision(n, ds)
	case Is(c, n, "spec.constraint"):
		link("serves", goalOrQuality, "a goal or a quality")
		if e, _ := argSite(n, "reason"); e == nil || strings.TrimSpace(strings.Trim(e.Syntax().Text(), `"`)) == "" {
			errAt(ds, n.Decl, "missing-argument", "constraint %s needs a reason from outside the solution", n.Name)
		}
		revision(n, ds)
	case Is(c, n, "spec.persona"):
		for _, r := range refsOf(c, n, "plays") {
			if r.Target != nil && !Is(c, r.Target, "spec.actor") {
				errAt(ds, r.Site, "type", "%s is a %s, not an actor", r.Target.Name, KindName(c, r.Target))
			}
		}
	case Is(c, n, "spec.scenario"):
		scenario(c, n, ds)
		revision(n, ds)
	case Is(c, n, "spec.usecase"):
		link("serves", goalOrQuality, "a goal or a quality")
		link("relies", func(x *resolve.Node) bool { return Is(c, x, "spec.assumption") }, "an assumption")
		for _, r := range refsOf(c, n, "actor") {
			if r.Target != nil && !Is(c, r.Target, "spec.actor") {
				errAt(ds, r.Site, "type", "%s is a %s, not an actor", r.Target.Name, KindName(c, r.Target))
			}
		}
		UseCaseChecks(t, c, n, ds)
		revision(n, ds)
	case Is(c, n, "spec.error"):
		placeholders(c, n, ds)
		revision(n, ds)
	case Is(c, n, "project.story"):
		for _, ch := range n.Children {
			if Is(c, ch, "spec.given", "spec.when", "spec.then", "spec.must", "spec.should", "spec.may", "spec.mustNot", "spec.shouldNot") {
				errAt(ds, ch.Decl, "story", "story %s holds a %s; a story lists the spec items it adds or changes, which state the behaviour", n.Name, KindName(c, ch))
			}
		}
	case Is(c, n, "spec.example"):
		link("refines", func(x *resolve.Node) bool { return Is(c, x, "spec.scenario") && isOutcome(c, x) }, "an outcome scenario")
	}
	realizesTargets(c, n, ds)
}

func revision(n *resolve.Node, ds *diag.List) {
	e, site := argSite(n, "rev")
	if e == nil {
		return
	}
	if v := strings.TrimSpace(e.Syntax().Text()); v == "0" || strings.HasPrefix(v, "-") {
		errAt(ds, site, "type", "a revision is a whole number from 1, not %s", v)
	}
}

// children of a node by their standard kind.
func childrenOf(c *types.Checker, n *resolve.Node, kinds ...string) []*resolve.Node {
	var out []*resolve.Node
	for _, ch := range n.Children {
		if Is(c, ch, kinds...) {
			out = append(out, ch)
		}
	}
	return out
}

func triggers(c *types.Checker, n *resolve.Node, ds *diag.List) {
	tr := childrenOf(c, n, "spec.when", "spec.if")
	if len(tr) > 1 {
		errAt(ds, tr[1].Decl, "accept", "rule accepts at most 1 trigger, when or if")
	}
}

func derivesCycle(c *types.Checker, n *resolve.Node, ds *diag.List) {
	seen := map[*resolve.Node]bool{}
	var walk func(x *resolve.Node, path []string) bool
	walk = func(x *resolve.Node, path []string) bool {
		for _, r := range refsOf(c, x, "derives") {
			if r.Target == nil {
				continue
			}
			if r.Target == n {
				rs := refsOf(c, n, "derives")
				errAt(ds, rs[0].Site, "cycle", "%s derives from itself through %s", n.Name, strings.Join(append(path, n.Name), " → "))
				return true
			}
			if !seen[r.Target] {
				seen[r.Target] = true
				if walk(r.Target, append(path, r.Target.Name)) {
					return true
				}
			}
		}
		return false
	}
	walk(n, []string{n.Name})
}

func isOutcome(c *types.Checker, n *resolve.Node) bool {
	return len(childrenOf(c, n, "spec.given", "spec.when", "spec.then")) > 0
}

func scenario(c *types.Checker, n *resolve.Node, ds *diag.List) {
	outcome := isOutcome(c, n)
	quality := false
	for _, f := range []string{"stimulus", "environment", "response", "measure"} {
		if ch := n.Child(f); ch != nil && ch.Value != nil {
			quality = true
		}
	}
	problem := ""
	switch {
	case outcome && quality:
		problem = "mixes outcome clauses and quality fields"
	case !outcome && !quality:
		problem = "has neither outcome clauses nor quality fields"
	case outcome:
		ill := refsOf(c, n, "illustrates")
		switch {
		case len(ill) == 0:
			problem = "illustrates nothing"
		case len(ill) == 1 && ill[0].Target == nil:
			txt := strings.TrimSpace(ill[0].Site.N.Text())
			if i := strings.LastIndexByte(txt, '.'); i > 0 && txt[i+1] >= '0' && txt[i+1] <= '9' {
				errAt(ds, ill[0].Site, "usecase", "use case %s has no step %s", txt[:i], txt[i+1:])
			} else {
				errAt(ds, ill[0].Site, "unresolved", "%s names no node", txt)
			}
		case len(ill) > 1:
			problem = "illustrates more than one rule or path"
		case ill[0].Target != nil && !Is(c, ill[0].Target, "spec.rule", "spec.usecase") && !StepTarget(ill[0].Target):
			problem = "illustrates " + ill[0].Target.Name + ", which is no rule or path of a use case"
		}
	case quality:
		if m := n.Child("measure"); m == nil || m.Value == nil {
			problem = "has no measure"
		} else if len(refsOf(c, n, "serves")) == 0 {
			problem = "serves no quality"
		} else {
			for _, r := range refsOf(c, n, "serves") {
				if r.Target != nil && !Is(c, r.Target, "spec.quality") {
					problem = "serves " + r.Target.Name + ", which is no quality"
				}
			}
		}
	}
	if problem != "" {
		errAt(ds, n.Decl, "scenario", "scenario %s %s", n.Name, problem)
	}
}

func placeholders(c *types.Checker, n *resolve.Node, ds *diag.List) {
	fields := map[string]*resolve.Node{}
	for _, ch := range childrenOf(c, n, "spec.field") {
		fields[ch.Name] = ch
	}
	used := map[string]bool{}
	for _, name := range []string{"title", "related"} {
		ch := n.Child(name)
		if ch == nil || ch.Value == nil {
			continue
		}
		text := ch.Value.Syntax().Text()
		for _, m := range placeholderRe.FindAllStringSubmatchIndex(text, -1) {
			p := text[m[2]:m[3]]
			used[p] = true
			if fields[p] == nil {
				start, _ := ch.Value.Syntax().Span()
				ds.Errorf(string(ch.ValueSite.File.ID), start+m[0], start+m[1], "placeholder",
					"{%s} in the title of %s is no field of it; declare %s: field(…)", p, n.Name, p)
			}
		}
	}
	for name, f := range fields {
		if !used[name] {
			warnAt(ds, f.Decl, "placeholder", "field %s of %s appears in neither its title nor its related message", name, n.Name)
		}
	}
}

// realizesTargets checks @realizes: on a contract node or a component, it
// names rules, use cases, steps, extensions or parts.
func realizesTargets(c *types.Checker, n *resolve.Node, ds *diag.List) {
	if Is(c, n, "project.component") {
		return // a component names the spec subjects it builds
	}
	for _, a := range n.Annotations {
		an := ast.Annotation{N: a.N}
		if an.N == nil || an.Path().N == nil {
			continue
		}
		if segs := resolve.PathSegs(an.Path().N); len(segs) == 0 || segs[len(segs)-1] != "realizes" {
			continue
		}
		for _, e := range project.ArgExprs(an.Args()) {
			t := project.RefTarget(c, e.Syntax(), a)
			if t == nil {
				continue
			}
			if !Is(c, t, "spec.rule", "spec.usecase", "spec.constraint", "spec.part", "spec.transition", "spec.states") && !StepTarget(t) {
				errAt(ds, resolve.Site{File: a.File, N: e.Syntax(), Scope: a.Scope}, "link",
					"@realizes takes rules, use cases, steps, extensions or parts, and %s is a %s", t.Name, KindName(c, t))
			}
		}
	}
}
