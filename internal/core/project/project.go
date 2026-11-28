// Package project reads the implementation side of a detached spec: a tree
// whose root `implements` a spec package (std/project), the proofs and
// progress it gives the spec's requirements, its releases and its version.
// It checks project mode, where the spec kinds belong to the spec alone,
// and the implementation version scheme, and computes the conformance
// report.
package project

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// The std/project kinds, by declaration path.
const (
	KindImplements = "project.implements"
	KindProves     = "project.proves"
	KindProgress   = "project.progress"
	KindRelease    = "project.release"
	KindVerify     = "project.verify"
)

// SpecKinds are the kinds a project leaves to its spec: what the system
// must do, for whom, in which words, and its design. Each names what it is
// in a project-mode error.
var SpecKinds = map[string]string{
	"spec.goal":          "goal",
	"spec.actor":         "actor",
	"spec.persona":       "persona",
	"spec.nongoal":       "nongoal",
	"spec.assumption":    "assumption",
	"spec.quality":       "quality",
	"spec.rule":          "rule",
	"spec.constraint":    "constraint",
	"spec.part":          "part",
	"spec.feature":       "feature",
	"spec.scenario":      "scenario",
	"spec.usecase":       "use case",
	"spec.states":        "lifecycle",
	"spec.transition":    "transition",
	"types.error":        "error",
	"spec.command":       "command",
	"spec.screen":        "screen",
	"spec.region":        "screen region",
	"grammar.grammar":    "grammar",
	"grammar.production": "production",
	"grammar.token":      "token",
	"spec.term":          "term",
	"types.api":          "api",
}

// RequirementKinds are the spec kinds a proof or a progress may name: the
// requirements of the specification and the contract layers. Intent is
// validated, never proven.
var RequirementKinds = map[string]bool{
	"spec.rule":       true,
	"spec.constraint": true,
	"spec.scenario":   true,
	"spec.usecase":    true,
	"spec.transition": true,
	"types.error":     true,
}

// Project is a tree's `implements` node and what hangs off it.
type Project struct {
	Tree    *resolve.Tree
	Checker *types.Checker
	// Node is the implements node at the root.
	Node *resolve.Node
	// Spec is the node `implements` names, normally a spec package's
	// package node, and Package the package it belongs to, if any.
	Spec    *resolve.Node
	Package *resolve.Package
	// At is the spec version implemented, as written.
	At string
	// Version is the project's own version, as written, or "".
	Version     string
	VersionSite resolve.Site
	// Scheme is the version scheme its versioning config chooses.
	Scheme Scheme
	// Since is the path of an earlier spec version, as written, and Docs
	// the base URL of the spec's rendered site.
	Since, Docs string
	// The tree's own proofs, progresses and releases, in tree order.
	Proofs   []*resolve.Node
	Progress []*resolve.Node
	Releases []*resolve.Node
}

// Find returns the project a tree's root declares with `implements`, or
// nil when the tree implements no spec.
func Find(t *resolve.Tree, c *types.Checker) *Project {
	var p *Project
	for _, n := range t.Root.Children {
		if KindPath(c, n) == KindImplements {
			p = &Project{Tree: t, Checker: c, Node: n}
			break
		}
	}
	if p == nil {
		return nil
	}
	n := p.Node
	args := KindArgs(n)
	if es := ArgExprs(args); len(es) > 0 {
		p.Spec = RefTarget(c, es[0].Syntax(), n.Decl)
	} else if e := NamedArg(args, "spec"); e != nil {
		p.Spec = RefTarget(c, e.Syntax(), n.Decl)
	}
	if p.Spec != nil {
		p.Package = PackageOf(p.Spec)
	}
	p.At, _ = ArgString(args, 1, "at")
	if v := n.Child("version"); v != nil && v.Value != nil {
		p.Version, _ = resolve.StringValue(v.Value.Syntax())
		p.VersionSite = v.ValueSite
	}
	if v := n.Child("since"); v != nil && v.Value != nil {
		p.Since, _ = resolve.StringValue(v.Value.Syntax())
	}
	if v := n.Child("docs"); v != nil && v.Value != nil {
		p.Docs, _ = resolve.StringValue(v.Value.Syntax())
	}
	p.Scheme = schemeOf(n)
	Own(t, func(x *resolve.Node) bool {
		switch KindPath(c, x) {
		case KindProves:
			p.Proofs = append(p.Proofs, x)
		case KindProgress:
			p.Progress = append(p.Progress, x)
		case KindRelease:
			p.Releases = append(p.Releases, x)
		}
		return true
	})
	return p
}

// SpecVersion is the version the spec package declares, or "".
func (p *Project) SpecVersion() string {
	if p.Package != nil {
		return p.Package.Version
	}
	return ""
}

// SpecName is how messages and reports name the spec: its package node's
// name, or the path implements names.
func (p *Project) SpecName() string {
	if p.Package != nil && p.Package.Node != nil {
		return p.Package.Node.Name
	}
	if p.Spec != nil {
		return p.Spec.Path()
	}
	return "the spec"
}

// Check reports what project mode and the version scheme rule out in a
// tree: a spec kind declared in a project, an `implements` not at the
// root or not naming a spec, a proof or progress naming no spec requirement,
// and versions that don't follow the scheme or the implemented spec.
func Check(t *resolve.Tree, c *types.Checker) diag.List {
	var ds diag.List
	// An implements anywhere but the root is an error, project or not.
	roots := 0
	Own(t, func(n *resolve.Node) bool {
		if KindPath(c, n) != KindImplements {
			return true
		}
		if n.Parent != t.Root {
			errAt(&ds, n.Decl, "project", "implements stands at the root of a project's tree, not inside %s", n.Parent.Path())
		} else if roots++; roots > 1 {
			errAt(&ds, n.Decl, "project", "a tree implements one spec; this is a second implements")
		}
		return true
	})
	p := Find(t, c)
	if p == nil {
		return ds
	}
	switch {
	case p.Spec == nil:
		errAt(&ds, p.Node.Decl, "project", "implements names no spec package it can find; import the spec and name its package node")
	case p.Package == nil:
		errAt(&ds, p.Node.Decl, "project", "implements names %s, which this tree declares; it names a spec package the tree imports", p.Spec.Path())
	}
	// Project mode: the spec's kinds stay in the spec.
	Own(t, func(n *resolve.Node) bool {
		k := KindPath(c, n)
		what, ok := SpecKinds[k]
		if !ok {
			return true
		}
		name := n.Path()
		if n.Name == "" {
			name = "this " + what
		}
		errAt(&ds, n.Decl, "project-mode", "%s is a %s, which is spec content: this tree implements %s, so the spec's kinds belong in the spec package, and the project only refers to them", name, what, p.SpecName())
		return false
	})
	for _, n := range append(append([]*resolve.Node(nil), p.Proofs...), p.Progress...) {
		for _, e := range ArgExprs(KindArgs(n)) {
			target := RefTarget(c, e.Syntax(), n.Decl)
			if target == nil {
				// The checker reports an unresolved first name; past a
				// name that resolves, as `shop.gone`, the path is the
				// spec's, and naming nothing there is this check's.
				if txt := strings.TrimSpace(e.Syntax().Text()); strings.Contains(txt, ".") {
					errAt(&ds, siteOf(n, e), "unresolved", "unknown name %s: a proof or progress names a requirement of the spec", txt)
				}
				continue
			}
			if PackageOf(target) == nil {
				errAt(&ds, siteOf(n, e), "project", "%s is the project's own; a proof or progress names a requirement of the spec", target.Path())
				continue
			}
			if !RequirementKinds[KindPath(c, target)] {
				errAt(&ds, siteOf(n, e), "project", "%s is no spec requirement: a proof or progress names a rule, a constraint, a scenario, a use case or its step, a transition or an error", target.Path())
			}
		}
	}
	for _, n := range p.Proofs {
		if len(childrenOfKind(c, n, KindVerify)) == 0 {
			errAt(&ds, n.Decl, "project", "a proof holds the verify(path, test) that proves its requirements")
		}
	}
	ds = append(ds, p.checkVersions()...)
	return ds
}

// Requirement is one spec requirement and where the project has it.
type Requirement struct {
	Node *resolve.Node
	// Outdated are the verify nodes of proofs at an earlier revision.
	Outdated []*resolve.Node
	// Path is its path inside the spec package, as "checkout.pay_once".
	Path string
	Kind string
	// Proofs are the verify nodes of the proofs that name it.
	Proofs []*resolve.Node
	// Status is "proven", "planned", "untested" or "missing", or
	// "practice" for a requirement on a person or an agent that no proof
	// or progress names: a practice, not an obligation of the code.
	Status string
}

// Requirements lists the spec's requirements in tree order with their
// standing in the project: proven by a test, planned, untested, or missing
// when no proof names it and no progress covers it.
func (p *Project) Requirements() []*Requirement {
	if p.Package == nil || p.Package.Tree == nil {
		return nil
	}
	c := p.Checker
	byNode := map[*resolve.Node]*Requirement{}
	var out []*Requirement
	for _, n := range SpecRequirements(p.Package, c) {
		r := &Requirement{Node: n, Path: RelPath(p.Package, n), Kind: KindPath(c, n), Status: "missing"}
		byNode[n] = r
		out = append(out, r)
	}
	for _, s := range p.Progress {
		st := ""
		for _, f := range KindFlags(s) {
			if f == "planned" || f == "untested" {
				st = f
			}
		}
		for _, e := range ArgExprs(KindArgs(s)) {
			if r := byNode[RefTarget(c, e.Syntax(), s.Decl)]; r != nil && st != "" {
				r.Status = st
			}
		}
	}
	for _, pr := range p.Proofs {
		vs := childrenOfKind(c, pr, KindVerify)
		at := Revision(pr)
		for _, e := range ArgExprs(KindArgs(pr)) {
			if r := byNode[RefTarget(c, e.Syntax(), pr.Decl)]; r != nil {
				// A proof of an earlier revision proved an earlier meaning.
				if at < Revision(r.Node) {
					r.Outdated = append(r.Outdated, vs...)
					if r.Status != "proven" && len(vs) > 0 {
						r.Status = "outdated"
					}
					continue
				}
				r.Proofs = append(r.Proofs, vs...)
				if len(r.Proofs) > 0 {
					r.Status = "proven"
				}
			}
		}
	}
	for _, r := range out {
		if r.Status == "missing" && PracticeSubject(c, r.Node) {
			r.Status = "practice"
		}
	}
	return out
}

// PracticeSubject reports whether a requirement's subject is a person or
// an agent: an std/spec actor of the role person, the default, or agent,
// or a persona. What it asks of them is a practice of the people and agents
// who author and build, which no test of the code proves, unless a proof
// links one, as a test of contrast or of visible text can.
func PracticeSubject(c *types.Checker, n *resolve.Node) bool {
	by := Subject(c, n)
	if by == nil {
		return false
	}
	switch KindPath(c, by) {
	case "spec.persona":
		return true
	case "spec.actor":
		role := c.GroupFlag(by, "role")
		return role == "" || role == "person" || role == "agent"
	}
	return false
}

// SpecRequirements are a spec package's requirement nodes, in tree order.
func SpecRequirements(pkg *resolve.Package, c *types.Checker) []*resolve.Node {
	var out []*resolve.Node
	if pkg == nil || pkg.Tree == nil {
		return nil
	}
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range n.Children {
			if ch.Shape == resolve.ShapeMounted {
				continue
			}
			if RequirementKinds[KindPath(c, ch)] {
				out = append(out, ch)
			}
			walk(ch)
		}
	}
	walk(pkg.Tree.Root)
	return out
}

// RelPath is a node's path inside its package, without the package node's
// name, as "checkout.pay_once" for shop.checkout.pay_once.
func RelPath(pkg *resolve.Package, n *resolve.Node) string {
	path := n.Path()
	if pkg != nil && pkg.Node != nil {
		path = strings.TrimPrefix(path, pkg.Node.Name+".")
	}
	return path
}

// --- tree helpers ---

// KindPath is the declaration path of a node's standard kind, such as
// "spec.rule", or "" for a node of no standard kind.
func KindPath(c *types.Checker, n *resolve.Node) string {
	k := c.KindOf(n)
	// A kind of the tree's own that extends a standard kind counts as it.
	for i := 0; k != nil && (k.Decl.File == nil || !k.Decl.File.ID.IsStd()) && i < 8; i++ {
		k = c.Extends(k)
	}
	if k == nil || k.Decl.File == nil || !k.Decl.File.ID.IsStd() {
		return ""
	}
	return k.Path()
}

// PackageOf returns the imported package a node belongs to, through
// mounted nodes' origins, or nil for a node of the tree itself.
func PackageOf(n *resolve.Node) *resolve.Package {
	for n.Origin != nil {
		n = n.Origin
	}
	r := n
	for r.Parent != nil {
		r = r.Parent
	}
	return r.Package
}

// Own calls f for each node of the tree's own, in tree order, skipping
// mounted package nodes; f returns false to skip a node's children.
func Own(t *resolve.Tree, f func(*resolve.Node) bool) {
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range n.Children {
			if ch.Shape == resolve.ShapeMounted || ch.Decl.File != nil && ch.Decl.File.ID.IsStd() {
				continue
			}
			if f(ch) {
				walk(ch)
			}
		}
	}
	walk(t.Root)
}

func childrenOfKind(c *types.Checker, n *resolve.Node, kind string) []*resolve.Node {
	var out []*resolve.Node
	for _, ch := range n.Children {
		if KindPath(c, ch) == kind {
			out = append(out, ch)
		}
	}
	return out
}

// KindArgs returns the arguments a node's kind slot is written with.
func KindArgs(n *resolve.Node) ast.Args {
	if n.Decl.N == nil {
		return ast.Args{}
	}
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if t, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType); ok {
			return t.Args()
		}
	case syntax.Value:
		if t, ok := (ast.Value{N: n.Decl.N}).Type().(ast.NamedType); ok {
			return t.Args()
		}
	case syntax.Invoke:
		return ast.Invoke{N: n.Decl.N}.Args()
	case syntax.Tag:
		// A kind element's attributes, as its kind's arguments.
		for n.ElementArgs.N == nil && n.Origin != nil {
			n = n.Origin // a mounted node's were read in its package
		}
		return n.ElementArgs
	}
	return ast.Args{}
}

// KindFlags returns the flags on a node's kind slot, as <planned>.
func KindFlags(n *resolve.Node) []string {
	if n.Decl.N == nil {
		return nil
	}
	var fl ast.Flags
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if t, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType); ok {
			fl = t.Flags()
		}
	case syntax.Invoke:
		fl = ast.Invoke{N: n.Decl.N}.Flags()
	}
	if fl.N == nil {
		return nil
	}
	var out []string
	for _, t := range fl.Idents() {
		out = append(out, t.Text)
	}
	return out
}

// ArgString returns a positional (index ≥ 0) or named string argument.
func ArgString(args ast.Args, index int, name string) (string, bool) {
	if args.N == nil {
		return "", false
	}
	pos := 0
	for _, a := range args.Args() {
		switch a := a.(type) {
		case ast.PositionalArg:
			if pos == index {
				return resolve.StringValue(a.Expr().Syntax())
			}
			pos++
		case ast.NamedArg:
			if name != "" && a.Ident() != nil && a.Ident().Text == name {
				return resolve.StringValue(a.Expr().Syntax())
			}
		}
	}
	return "", false
}

// ArgExprs returns the positional argument expressions.
func ArgExprs(args ast.Args) []ast.Expr {
	if args.N == nil {
		return nil
	}
	var out []ast.Expr
	for _, a := range args.Args() {
		if pa, ok := a.(ast.PositionalArg); ok {
			out = append(out, pa.Expr())
		}
	}
	return out
}

// NamedArg returns a named argument's expression.
func NamedArg(args ast.Args, name string) ast.Expr {
	if args.N == nil {
		return nil
	}
	for _, a := range args.Args() {
		if na, ok := a.(ast.NamedArg); ok && na.Ident() != nil && na.Ident().Text == name {
			return na.Expr()
		}
	}
	return nil
}

// RefTarget resolves a path expression to a node, through the checker's
// references and then the scope it was written in.
func RefTarget(c *types.Checker, p *syntax.Node, site resolve.Site) *resolve.Node {
	if p == nil {
		return nil
	}
	if n := c.Refs[p]; n != nil {
		return n
	}
	var path *syntax.Node
	if p.Kind == syntax.Path {
		path = p
	} else {
		p.Walk(func(x *syntax.Node) bool {
			if path == nil && x.Kind == syntax.Path {
				path = x
			}
			return path == nil
		})
	}
	if path == nil {
		return nil
	}
	if n := c.Refs[path]; n != nil {
		return n
	}
	segs := resolve.PathSegs(path)
	if site.Scope == nil || len(segs) == 0 {
		return nil
	}
	var out *resolve.Node
	site.Scope.Each(segs[0], site.File, -1, func(b resolve.Binding) bool {
		n := b.Node
		for _, seg := range segs[1:] {
			if n == nil {
				break
			}
			n = n.Child(seg)
		}
		if n != nil {
			out = n
			return false
		}
		return true
	})
	return out
}

// RefsOf returns the nodes a node's positional kind arguments name.
func RefsOf(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, e := range ArgExprs(KindArgs(n)) {
		if t := RefTarget(c, e.Syntax(), n.Decl); t != nil {
			out = append(out, t)
		}
	}
	return out
}

func siteOf(n *resolve.Node, e ast.Expr) resolve.Site {
	return resolve.Site{File: n.Decl.File, N: e.Syntax(), Scope: n.Decl.Scope}
}

func errAt(ds *diag.List, s resolve.Site, code, format string, args ...any) {
	start, end := s.Span()
	file := ""
	if s.File != nil {
		file = string(s.File.ID)
	}
	ds.Errorf(file, start, end, code, format, args...)
}

// Revision is a requirement's or a proof's `@rev`, 1 when it's left out.
func Revision(n *resolve.Node) int {
	t := AnnotationText(n, "rev")
	if t == "" {
		return 1
	}
	v := 0
	for _, ch := range t {
		if ch < '0' || ch > '9' {
			return 1
		}
		v = v*10 + int(ch-'0')
	}
	if v < 1 {
		return 1
	}
	return v
}

// AnnArg is one positional argument of an annotation, with the site the
// annotation is written at, which its references resolve from.
type AnnArg struct {
	Expr ast.Expr
	Site resolve.Site
}

// Annotations are a node's annotations named name, by the last segment of
// their path, its own and those of the group it stands in, in reading
// order.
func Annotations(n *resolve.Node, name string) []resolve.Site {
	if n == nil {
		return nil
	}
	var out []resolve.Site
	for _, a := range append(append([]resolve.Site(nil), n.Annotations...), n.GroupAnnotations...) {
		if a.N == nil || a.N.Kind != syntax.Annotation {
			continue
		}
		an := ast.Annotation{N: a.N}
		if an.Path().N == nil {
			continue
		}
		if AnnotationNamed(resolve.PathSegs(an.Path().N), name) {
			out = append(out, a)
		}
	}
	return out
}

// AnnotationNamed reports whether an annotation's path names name: as
// written unqualified, or qualified by a core package, as `@spec.status`.
// A path qualified by any other package, as `@proto.status`, names that
// package's own annotation.
func AnnotationNamed(segs []string, name string) bool {
	if len(segs) == 0 || segs[len(segs)-1] != name {
		return false
	}
	return len(segs) == 1 || len(segs) == 2 && (segs[0] == "spec" || segs[0] == "core")
}

// HasAnnotation reports whether a node carries an annotation named name.
func HasAnnotation(n *resolve.Node, name string) bool { return len(Annotations(n, name)) > 0 }

// AnnotationArgs are the positional arguments of a node's annotations
// named name, as `@touch(a, b)` gives a and b.
func AnnotationArgs(n *resolve.Node, name string) []AnnArg {
	var out []AnnArg
	for _, a := range Annotations(n, name) {
		for _, e := range ArgExprs(ast.Annotation{N: a.N}.Args()) {
			out = append(out, AnnArg{Expr: e, Site: resolve.Site{File: a.File, N: e.Syntax(), Scope: a.Scope}})
		}
	}
	return out
}

// AnnotationArg is the first argument of a node's annotation named name,
// or nil.
func AnnotationArg(n *resolve.Node, name string) *AnnArg {
	if as := AnnotationArgs(n, name); len(as) > 0 {
		return &as[0]
	}
	return nil
}

// AnnotationString is the string an annotation such as `@display("…")`
// holds.
func AnnotationString(n *resolve.Node, name string) (string, bool) {
	a := AnnotationArg(n, name)
	if a == nil {
		return "", false
	}
	return resolve.StringValue(a.Expr.Syntax())
}

// AnnotationText is the first argument of an annotation as written, such
// as `404` for `@status(404)`, or "".
func AnnotationText(n *resolve.Node, name string) string {
	a := AnnotationArg(n, name)
	if a == nil {
		return ""
	}
	return strings.TrimSpace(a.Expr.Syntax().Text())
}

// AnnotationRefs are the nodes an annotation's arguments name, as
// `@touch(desk)` names desk.
func AnnotationRefs(c *types.Checker, n *resolve.Node, name string) []*resolve.Node {
	var out []*resolve.Node
	for _, a := range AnnotationArgs(n, name) {
		if t := RefTarget(c, a.Expr.Syntax(), a.Site); t != nil {
			out = append(out, t)
		}
	}
	return out
}

// AnnotationRef is the node an annotation's first argument names, or nil.
func AnnotationRef(c *types.Checker, n *resolve.Node, name string) *resolve.Node {
	a := AnnotationArg(n, name)
	if a == nil {
		return nil
	}
	return RefTarget(c, a.Expr.Syntax(), a.Site)
}

// ProseLinks are the nodes a node's own prose links to, in the order
// written.
func ProseLinks(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, ps := range n.Prose {
		if ps.N == nil {
			continue
		}
		ps.N.Walk(func(y *syntax.Node) bool {
			if y.Kind == syntax.Path {
				if t := RefTarget(c, y, ps); t != nil {
					out = append(out, t)
				}
				return false
			}
			return true
		})
	}
	return out
}

// ErrorRules are the requirements an error's prose links to: the rules,
// constraints, use cases and use case steps that say when it's reported.
// An error names no raiser and realizes nothing; this is how it points up.
func ErrorRules(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	seen := map[*resolve.Node]bool{}
	for _, t := range ProseLinks(c, n) {
		if seen[t] {
			continue
		}
		switch KindPath(c, t) {
		case "spec.rule", "spec.constraint", "spec.usecase":
		default:
			if t.Shape != resolve.ShapeStep {
				continue
			}
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// ErrorRaiser is who reports an error, derived from the other direction:
// the command that holds it, or else the subject of the first rule its prose
// links to. An op's `@throws` is the third way, which readers that index
// ops add. Nil when neither says.
func ErrorRaiser(c *types.Checker, n *resolve.Node) *resolve.Node {
	for a := n.Parent; a != nil; a = a.Parent {
		if KindPath(c, a) == "spec.command" {
			return a
		}
	}
	for _, r := range ErrorRules(c, n) {
		if by := Subject(c, r); by != nil {
			return by
		}
	}
	return nil
}

// ProseText is a node's own prose as written, its lines joined.
func ProseText(n *resolve.Node) string {
	var b strings.Builder
	for _, ps := range n.Prose {
		if ps.N != nil {
			b.WriteString(ps.N.Text())
		}
	}
	return b.String()
}

// ErrorMembers are the members of an error, the values it carries as data
// and in its message: its own in the order written, then those it
// inherits and doesn't declare again, nearest ancestor first.
func ErrorMembers(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	seen := map[string]bool{}
	for _, x := range append([]*resolve.Node{n}, c.ErrorAncestors(n)...) {
		for _, ch := range x.Children {
			if ch.Name == "" || seen[ch.Name] || ch.Decl.N == nil || ch.Decl.N.Kind != syntax.Declare && ch.Decl.N.Kind != syntax.Value {
				continue
			}
			if k := KindPath(c, ch); k == "" || k == "types.member" {
				seen[ch.Name] = true
				out = append(out, ch)
			}
		}
	}
	return out
}

// FeatureOptional reports whether a feature is optional, one a product may
// leave out.
func FeatureOptional(n *resolve.Node) bool {
	if e := NamedArg(KindArgs(n), "optional"); e != nil {
		return strings.TrimSpace(e.Syntax().Text()) == "true"
	}
	return false
}

// Feature is the feature a node stands in, the nearest one above it, or
// nil.
func Feature(c *types.Checker, n *resolve.Node) *resolve.Node {
	for a := n.Parent; a != nil; a = a.Parent {
		if KindPath(c, a) == "spec.feature" {
			return a
		}
	}
	return nil
}

// Features are the features a node belongs to: the one it stands in and
// those its constructor or @touch references, in that order.
func Features(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	seen := map[*resolve.Node]bool{}
	if f := Feature(c, n); f != nil {
		out = append(out, f)
		seen[f] = true
	}
	for _, a := range HardReferences(c, n) {
		if t := RefTarget(c, a.Expr.Syntax(), a.Site); t != nil && !seen[t] && KindPath(c, t) == "spec.feature" {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}
