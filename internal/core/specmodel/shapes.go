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
	references(c, n, ds)
	switch {
	case Is(c, n, "spec.rule"):
		subjects(c, n, ds)
		ruleLinks(c, t, n, ds)
		triggers(c, n, ds)
		revision(n, ds)
	case Is(c, n, "spec.constraint"):
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
		for _, r := range nodeRefs(c, n) {
			if r.Target != nil && Is(c, r.Target, "spec.scenario") && !isOutcome(c, r.Target) {
				errAt(ds, r.Site, "reference", "%s is a quality scenario; an example pins down an outcome scenario", r.Target.Name)
			}
		}
	}
	seeTargets(c, n, ds)
}

// nodeRefs resolves a node's hard references: its constructor's, then its
// @touch's.
func nodeRefs(c *types.Checker, n *resolve.Node) []Ref {
	var out []Ref
	for _, a := range project.HardReferences(c, n) {
		out = append(out, Ref{Site: a.Site, Target: project.RefTarget(c, a.Expr.Syntax(), a.Site)})
	}
	return out
}

// realizedNames is how a message names the kinds in types.realized.
const realizedNames = "a rule, a constraint, a use case or its step, a part, a lifecycle or a transition"

// references checks a node's hard references, in its constructor and its
// @touch: each names a kind its kind's `references` lists, or a kind that
// extends one, and on a kind with no list, as a type, a kind in
// types.realized. A term is vocabulary, which prose mentions and no
// reference names, and no reference is listed twice.
func references(c *types.Checker, n *resolve.Node, ds *diag.List) {
	k := c.KindOf(n)
	allowed, ok := c.ReferenceKinds(k)
	var refs []Ref
	if ok {
		refs = nodeRefs(c, n)
	} else {
		for _, a := range project.Touches(n) {
			refs = append(refs, Ref{Site: a.Site, Target: project.RefTarget(c, a.Expr.Syntax(), a.Site)})
		}
	}
	what := "node"
	if k != nil {
		what = k.Name
	}
	ctor := len(project.References(c, n))
	seen := map[*resolve.Node]bool{}
	for i, r := range refs {
		if r.Target == nil {
			txt := strings.TrimSpace(r.Site.N.Text())
			if i := strings.LastIndexByte(txt, '.'); i > 0 && i+1 < len(txt) && txt[i+1] >= '0' && txt[i+1] <= '9' {
				errAt(ds, r.Site, "usecase", "use case %s has no step %s", txt[:i], txt[i+1:])
			}
			continue
		}
		if seen[r.Target] && ok && i >= ctor {
			errAt(ds, r.Site, "reference", "%s is listed twice, in the constructor and in @touch; list it once", r.Target.Name)
			continue
		}
		seen[r.Target] = true
		if Is(c, r.Target, "doc.term") {
			errAt(ds, r.Site, "reference", "%s is a term, which is vocabulary: a %s doesn't reference it; mention it in the prose", r.Target.Name, what)
			continue
		}
		if ok && allows(c, allowed, r.Target) || !ok && realized(c, r.Target) {
			continue
		}
		if !ok {
			errAt(ds, r.Site, "reference", "%s is a %s, and @touch on a %s takes only %s", r.Target.Name, KindName(c, r.Target), what, realizedNames)
			continue
		}
		var names []string
		for _, a := range allowed {
			if c.IsCategory(a) && types.IsStd(a, "types", "types.realized") {
				names = append(names, realizedNames)
				continue
			}
			names = append(names, a.Name)
		}
		errAt(ds, r.Site, "reference", "%s is a %s, and a %s references only %s", r.Target.Name, KindName(c, r.Target), what, orList(names))
	}
}

// allows reports whether a reference to n fits a `references` list: n's
// kind, or a kind it extends, is on the list or in a category on it, or n
// is a step of a use case where use cases fit.
func allows(c *types.Checker, allowed []*resolve.Node, n *resolve.Node) bool {
	tk := c.KindOf(n)
	if StepTarget(n) {
		tk = c.KindOf(n.Parent)
		if tk == nil || !Is(c, n.Parent, "spec.usecase") {
			return false
		}
	}
	for k, i := tk, 0; k != nil && i < 8; k, i = c.Extends(k), i+1 {
		if c.Fits(k, allowed) {
			return true
		}
	}
	return false
}

// realized reports whether a node's kind, or a kind it extends, is in
// types.realized, or the node is a step of a use case.
func realized(c *types.Checker, n *resolve.Node) bool {
	if StepTarget(n) {
		return n.Parent != nil && Is(c, n.Parent, "spec.usecase")
	}
	for k, i := c.KindOf(n), 0; k != nil && i < 8; k, i = c.Extends(k), i+1 {
		for _, cat := range c.Categories(k) {
			if types.IsStd(cat, "types", "types.realized") {
				return true
			}
		}
	}
	return false
}

func orList(names []string) string {
	switch len(names) {
	case 0:
		return "nothing"
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// subjects checks that a rule references at most one actor, persona or
// part: its subject. A rule with none binds the system.
func subjects(c *types.Checker, n *resolve.Node, ds *diag.List) {
	var subs []Ref
	for _, r := range nodeRefs(c, n) {
		if r.Target != nil && Is(c, r.Target, "spec.actor", "spec.persona", "spec.part") {
			subs = append(subs, r)
		}
	}
	if len(subs) > 1 {
		errAt(ds, subs[1].Site, "subject", "rule %s binds %s and %s; a rule references at most one actor or part, its subject", n.Name, subs[0].Target.Name, subs[1].Target.Name)
	}
}

// ruleLinks checks that a rule's prose links no other rule: a rule is a
// leaf, which states its own fact.
func ruleLinks(c *types.Checker, t *resolve.Tree, n *resolve.Node, ds *diag.List) {
	// The signature's references are checked by references.
	sig := map[[2]int]bool{}
	for _, r := range nodeRefs(c, n) {
		s, e := r.Site.Span()
		sig[[2]int{s, e}] = true
	}
	inSig := func(r Ref) bool {
		s, _ := r.Site.Span()
		for k := range sig {
			if s >= k[0] && s < k[1] {
				return true
			}
		}
		return false
	}
	for _, r := range OwnRefs(c, t, n) {
		if !inSig(r) && r.Target != n && Is(c, r.Target, "spec.rule") {
			errAt(ds, r.Site, "rule-link", "rule %s links the rule %s; a rule references no other rule, so link the term, part or section they share, or state the fact in the rule itself", n.Name, r.Target.Name)
		}
	}
}

// seeTargets checks @see, the soft references: a term is vocabulary its
// prose mentions, which @see doesn't name.
func seeTargets(c *types.Checker, n *resolve.Node, ds *diag.List) {
	for _, a := range project.AnnotationArgs(n, "see") {
		if t := project.RefTarget(c, a.Expr.Syntax(), a.Site); t != nil && Is(c, t, "doc.term") {
			errAt(ds, a.Site, "reference", "%s is a term, which is vocabulary: @see doesn't reference it; mention it in the prose", t.Name)
		}
	}
}

func revision(n *resolve.Node, ds *diag.List) {
	a := project.AnnotationArg(n, "rev")
	if a == nil {
		return
	}
	if v := strings.TrimSpace(a.Expr.Syntax().Text()); v == "0" || strings.HasPrefix(v, "-") {
		errAt(ds, a.Site, "type", "a revision is a whole number from 1, not %s", v)
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
		var ill []Ref
		for _, r := range nodeRefs(c, n) {
			if r.Target == nil || !Is(c, r.Target, "spec.quality") {
				ill = append(ill, r)
			}
		}
		switch {
		case len(ill) == 0:
			problem = "references no rule or path of a use case"
		case len(ill) > 1:
			problem = "references more than one rule or path"
		}
	case quality:
		if m := n.Child("measure"); m == nil || m.Value == nil {
			problem = "has no measure"
		} else if len(project.ReferenceTargets(c, n, "spec.quality")) == 0 {
			problem = "references no quality it measures"
		} else if len(project.ReferenceTargets(c, n, "spec.quality")) < len(nodeRefs(c, n)) {
			problem = "references something other than the quality it measures"
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
