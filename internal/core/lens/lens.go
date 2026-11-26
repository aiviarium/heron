// Package lens is std/docs's Go side: the lenses a docs site builds, what
// each one shows, sums up or leaves out, the `@lens(…)` restrictions on
// nodes and prose blocks, and the checks on them. The checker and the docs
// generator read the same model, so what a lens shows can't drift between
// them.
package lens

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// The std/docs and std/doc declarations the model reads.
const (
	KindLens   = "docs.lens"
	KindSite   = "docs.site"
	Annotation = "doc.lens"
	// Authored is the category of the authored kinds.
	Authored = "docs.authored"
	// ExploreName is the lens that shows everything, whatever `@lens(…)`
	// says.
	ExploreName = "explore"
)

// Nav is how a lens orders its navigation: a LensNav value.
type Nav string

const (
	NavTask  Nav = "TASK"
	NavTopic Nav = "TOPIC"
	NavTree  Nav = "TREE"
)

// Mode is how a lens renders a node.
type Mode int

const (
	Show Mode = iota
	Summarize
	Hide
)

// Lens is one `lens` node, read.
type Lens struct {
	Node    *resolve.Node
	Name    string
	Title   string
	Default bool
	Nav     Nav
	// Prefix is the URL path segment the lens is served under, "" at the
	// site's root.
	Prefix string
	// Show, Summarize and Hide hold the kinds and categories each list
	// names.
	Show, Summarize, Hide []*resolve.Node
	// Entry lists the authored pages shown first.
	Entry []*resolve.Node
	// Depth is how many levels of sections get pages in the lens, in
	// place of the docs block's; 0 when the lens doesn't set it.
	Depth int
}

// Explore reports whether the lens shows everything.
func (l *Lens) Explore() bool { return l == nil || l.Name == ExploreName }

// Read reads a lens node.
func Read(c *types.Checker, n *resolve.Node) *Lens {
	l := &Lens{Node: n, Name: n.Name, Title: n.Name, Nav: NavTree}
	for _, ch := range n.Children {
		if ch.Value == nil {
			continue
		}
		site := ch.ValueSite
		switch ch.Name {
		case "title":
			l.Title, _ = resolve.StringValue(ch.Value.Syntax())
		case "prefix":
			l.Prefix, _ = resolve.StringValue(ch.Value.Syntax())
		case "default":
			l.Default = strings.TrimSpace(ch.Value.Syntax().Text()) == "true"
		case "nav":
			t := strings.TrimSpace(ch.Value.Syntax().Text())
			if i := strings.LastIndex(t, "."); i >= 0 {
				t = t[i+1:]
			}
			l.Nav = Nav(t)
		case "show":
			l.Show = refList(c, ch.Value, site, nil)
		case "summarize":
			l.Summarize = refList(c, ch.Value, site, nil)
		case "hide":
			l.Hide = refList(c, ch.Value, site, nil)
		case "entry":
			l.Entry = refList(c, ch.Value, site, nil)
		case "depth":
			l.Depth, _ = strconv.Atoi(strings.TrimSpace(ch.Value.Syntax().Text()))
		}
	}
	return l
}

// refList resolves the paths of a list value, each to the first binding
// pred accepts, or the first binding when pred is nil.
func refList(c *types.Checker, v ast.Expr, site resolve.Site, pred func(*resolve.Node) bool) []*resolve.Node {
	arr, ok := v.(ast.ArrayExpr)
	if !ok {
		return nil
	}
	var out []*resolve.Node
	for _, e := range arr.Exprs() {
		if t := resolveRef(c, e.Syntax(), site, pred); t != nil {
			out = append(out, t)
		}
	}
	return out
}

// resolveRef resolves a path, preferring a binding pred accepts, so a lens
// named `user` is found past a tree's own `user` node.
func resolveRef(c *types.Checker, p *syntax.Node, site resolve.Site, pred func(*resolve.Node) bool) *resolve.Node {
	path := p
	if path != nil && path.Kind != syntax.Path {
		path = nil
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
	if n := c.Refs[path]; n != nil && (pred == nil || pred(n)) {
		return n
	}
	segs := resolve.PathSegs(path)
	if site.Scope == nil || len(segs) == 0 {
		return nil
	}
	var first, match *resolve.Node
	site.Scope.Each(segs[0], site.File, -1, func(b resolve.Binding) bool {
		n := b.Node
		for _, seg := range segs[1:] {
			if n == nil {
				break
			}
			n = n.Child(seg)
		}
		if n == nil {
			return true
		}
		if first == nil {
			first = n
		}
		if pred == nil || pred(n) {
			match = n
			return false
		}
		return true
	})
	if match != nil {
		return match
	}
	if pred != nil {
		return nil
	}
	return first
}

// IsLens reports whether n is a lens node.
func IsLens(c *types.Checker, n *resolve.Node) bool {
	return n != nil && project.KindPath(c, n) == KindLens
}

// Site is what a tree's docs block chooses: the lenses it builds.
type Site struct {
	// Docs is the tree's docs node, or nil.
	Docs *resolve.Node
	// Lenses are the chosen lenses in the order written; Default is the
	// one served at the root.
	Lenses  []*Lens
	Default *Lens
	// Chosen reports whether the docs block sets `lenses`; when it
	// doesn't, the site builds explore alone, at its root.
	Chosen bool
}

// DocsNode returns the tree's docs block, or nil.
func DocsNode(t *resolve.Tree, c *types.Checker) *resolve.Node {
	d := t.Root.Child("docs")
	if d == nil || project.KindPath(c, d) != KindSite {
		return nil
	}
	return d
}

// SiteOf reads the lenses a tree's docs block chooses. Without a choice,
// the site is std/docs's explore lens at the root.
func SiteOf(t *resolve.Tree, c *types.Checker) *Site {
	s := &Site{Docs: DocsNode(t, c)}
	if s.Docs != nil {
		if ch := s.Docs.Child("lenses"); ch != nil && ch.Value != nil {
			s.Chosen = true
			for _, n := range refList(c, ch.Value, ch.ValueSite, func(n *resolve.Node) bool { return IsLens(c, n) }) {
				if IsLens(c, n) {
					s.Lenses = append(s.Lenses, Read(c, n))
				}
			}
		}
	}
	if len(s.Lenses) == 0 {
		l := &Lens{Name: ExploreName, Title: "Explore", Nav: NavTree, Default: true}
		if n := stdLens(t, ExploreName); n != nil {
			l = Read(c, n)
			l.Default, l.Prefix = true, ""
		}
		s.Lenses = []*Lens{l}
	}
	for _, l := range s.Lenses {
		if l.Default && s.Default == nil {
			s.Default = l
		}
	}
	if s.Default == nil && len(s.Lenses) > 0 {
		s.Default = s.Lenses[0]
	}
	if s.Default != nil {
		s.Default.Prefix = ""
	}
	return s
}

// stdLens finds a lens std/docs ships, by name, among the tree's imports.
func stdLens(t *resolve.Tree, name string) *resolve.Node {
	for _, p := range t.Imports {
		if p.Tree == nil {
			continue
		}
		for _, n := range p.Tree.Root.Children {
			if n.Name == "docs" && n.Decl.File != nil && n.Decl.File.ID.IsStd() {
				if l := n.Child(name); l != nil {
					return l
				}
			}
		}
	}
	return nil
}

// Restriction returns the lenses `@lens(…)` keeps a node to, or nil when
// it carries none.
func Restriction(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	found := false
	for _, a := range n.Annotations {
		if ls, ok := annotationLenses(c, a); ok {
			found = true
			out = append(out, ls...)
		}
	}
	if !found {
		return nil
	}
	if out == nil {
		out = []*resolve.Node{}
	}
	return out
}

// ProseRestriction returns the lenses `@lens(…)` keeps a prose block to,
// or nil when it carries none. A prose file's item that is an element,
// as `<p @lens="user">…</p>`, is a block marked by its `@lens` attribute.
func ProseRestriction(c *types.Checker, site resolve.Site) []*resolve.Node {
	if site.N == nil {
		return nil
	}
	if site.N.Kind == syntax.Tag {
		return ElementRestriction(c, site)
	}
	var out []*resolve.Node
	found := false
	for _, ch := range site.N.Children {
		x, ok := ch.(*syntax.Node)
		if !ok || x.Kind != syntax.Annotation {
			continue
		}
		if ls, ok := annotationLenses(c, resolve.Site{File: site.File, N: x, Scope: site.Scope}); ok {
			found = true
			out = append(out, ls...)
		}
	}
	if !found {
		return nil
	}
	if out == nil {
		out = []*resolve.Node{}
	}
	return out
}

// ElementMarks are the `@lens` attributes of an element, read as the
// annotations `@lens(names…)`.
func ElementMarks(site resolve.Site) []resolve.Site {
	var out []resolve.Site
	for _, a := range resolve.ElementAnnotations(site.File, site.N, site.Scope, func(name string) bool { return name == "lens" }) {
		if p := (ast.Annotation{N: a.N}).Path().N; p != nil {
			if segs := resolve.PathSegs(p); len(segs) == 1 && segs[0] == "lens" {
				out = append(out, a)
			}
		}
	}
	return out
}

// ElementRestriction returns the lenses an element's `@lens` attribute
// keeps its content to, or nil when it has none: on an element that
// declares no node it marks the content as a mark marks a prose run.
func ElementRestriction(c *types.Checker, site resolve.Site) []*resolve.Node {
	marks := ElementMarks(site)
	if len(marks) == 0 {
		return nil
	}
	out := []*resolve.Node{}
	for _, a := range marks {
		ls, _ := annotationLenses(c, a)
		out = append(out, ls...)
	}
	return out
}

// annotationLenses reads one annotation: whether it's `@lens`, and the
// lens nodes it names.
func annotationLenses(c *types.Checker, a resolve.Site) ([]*resolve.Node, bool) {
	an := ast.Annotation{N: a.N}
	if an.N == nil || an.Path().N == nil {
		return nil, false
	}
	d := c.Refs[an.Path().N]
	if d == nil {
		segs := resolve.PathSegs(an.Path().N)
		if len(segs) != 1 || segs[0] != "lens" {
			return nil, false
		}
	} else if d.Path() != Annotation || d.Decl.File == nil || !d.Decl.File.ID.IsStd() {
		return nil, false
	}
	var out []*resolve.Node
	for _, e := range project.ArgExprs(an.Args()) {
		if t := resolveRef(c, e.Syntax(), a, func(n *resolve.Node) bool { return IsLens(c, n) }); t != nil {
			out = append(out, t)
		}
	}
	return out, true
}

// Allows reports whether a restriction lets a lens show what it's on.
func (l *Lens) Allows(restriction []*resolve.Node) bool {
	if restriction == nil || l.Explore() {
		return true
	}
	for _, r := range restriction {
		if r == l.Node || r.Name == l.Name {
			return true
		}
	}
	return false
}

// Marks returns the lenses the lens marks on a node and above it leave it
// to: those of the nearest mark, narrowed by every mark above it, or nil
// when no mark stands on it or above it.
func Marks(c *types.Checker, n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	marked := false
	for x := n; x != nil; x = x.Parent {
		r := Restriction(c, x)
		if r == nil {
			continue
		}
		if !marked {
			out, marked = r, true
			continue
		}
		var keep []*resolve.Node
		for _, a := range out {
			for _, b := range r {
				if a == b || a.Name == b.Name {
					keep = append(keep, a)
					break
				}
			}
		}
		out = keep
	}
	if marked && out == nil {
		out = []*resolve.Node{}
	}
	return out
}

// Marked reports whether the marks on and above a node leave it out of
// the lens: the node and its whole subtree, whatever their kinds.
func (l *Lens) Marked(c *types.Checker, n *resolve.Node) bool {
	return !l.Allows(Marks(c, n))
}

// Mode returns how the lens renders a node. A lens mark decides first: a
// node whose marks name the lens shows in full, and one whose marks don't
// is left out. Then the node's kind: a list naming the kind itself wins
// over one naming a category of it, and between equal lists hide wins
// over summarize and summarize over show. A node of a kind no list names
// is treated as its parent is, and a child of the tree's root shows when
// the lens has no show list and is left out when it has one. Explore
// shows everything.
func (l *Lens) Mode(c *types.Checker, n *resolve.Node) Mode {
	if l.Explore() {
		return Show
	}
	if m := Marks(c, n); m != nil {
		if l.Allows(m) {
			return Show
		}
		return Hide
	}
	return l.kindMode(c, n)
}

func (l *Lens) kindMode(c *types.Checker, n *resolve.Node) Mode {
	if k := c.KindOf(n); k != nil {
		has := func(list []*resolve.Node, x *resolve.Node) bool {
			for _, y := range list {
				if y == x {
					return true
				}
			}
			return false
		}
		cands := append([]*resolve.Node{k}, c.Categories(k)...)
		for _, x := range cands {
			switch {
			case has(l.Hide, x):
				return Hide
			case has(l.Summarize, x):
				return Summarize
			case has(l.Show, x):
				return Show
			}
		}
	}
	if n.Parent == nil || n.Parent.Parent == nil {
		if len(l.Show) > 0 {
			return Hide
		}
		return Show
	}
	return l.kindMode(c, n.Parent)
}

// IsAuthored reports whether a node is of an authored kind: a guide, a
// how-to, a concept or a tutorial.
func IsAuthored(c *types.Checker, n *resolve.Node) bool {
	k := c.KindOf(n)
	if k == nil {
		return false
	}
	for _, cat := range c.Categories(k) {
		if cat.Path() == Authored && cat.Decl.File != nil && cat.Decl.File.ID.IsStd() {
			return true
		}
	}
	return false
}

// ExampleChecker checks the source of an example as a tree of its own,
// returning the first error's message and line, or ok.
type ExampleChecker func(src string) (msg string, line int, ok bool)

// Check reports what's wrong with a tree's lenses and authored pages: a
// lens mark or `lenses` entry naming nothing or something that isn't a
// lens, a lens list naming what isn't a kind or a category, an entry that
// isn't an authored page, a malformed or missing prefix, a site with no
// default lens or more than one, and an authored page's example that
// doesn't check. It also returns the positions where it reported a name,
// whose generic "unknown name" errors it replaces.
func Check(t *resolve.Tree, c *types.Checker, examples ExampleChecker) (diag.List, map[string]bool) {
	var ds diag.List
	replaced := map[string]bool{}
	at := func(s resolve.Site) string {
		start, _ := s.Span()
		f := ""
		if s.File != nil {
			f = string(s.File.ID)
		}
		return fmt.Sprintf("%s:%d", f, start)
	}
	isLens := func(n *resolve.Node) bool { return IsLens(c, n) }
	inScope := func(site resolve.Site) string {
		seen := map[string]bool{}
		var names []string
		for sc := site.Scope; sc != nil; sc = sc.Parent {
			sc.Names(func(name string, b resolve.Binding) {
				if b.Node != nil && IsLens(c, b.Node) && !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			})
		}
		sort.Strings(names)
		return strings.Join(names, ", ")
	}
	// lensName checks one name where a lens is wanted.
	lensName := func(e ast.Expr, site resolve.Site) {
		es := resolve.Site{File: site.File, N: e.Syntax(), Scope: site.Scope}
		if resolveRef(c, e.Syntax(), site, isLens) != nil {
			return
		}
		name := strings.TrimSpace(e.Syntax().Text())
		replaced[at(es)] = true
		if t := resolveRef(c, e.Syntax(), site, nil); t != nil {
			errAt(&ds, es, "lens", "%s is a %s, not a lens", name, kindName(c, t))
			return
		}
		errAt(&ds, es, "unresolved", "unknown lens %s; the lenses in scope are %s", name, inScope(site))
	}
	mark := func(site resolve.Site) {
		an := ast.Annotation{N: site.N}
		if an.N == nil || an.Path().N == nil {
			return
		}
		if d := c.Refs[an.Path().N]; d == nil || d.Path() != Annotation || d.Decl.File == nil || !d.Decl.File.ID.IsStd() {
			return
		}
		for _, e := range project.ArgExprs(an.Args()) {
			lensName(e, site)
		}
	}
	var lensNodes []*resolve.Node
	project.Own(t, func(n *resolve.Node) bool {
		for _, a := range n.Annotations {
			mark(a)
		}
		for _, p := range n.Prose {
			if p.N == nil {
				continue
			}
			for _, ch := range p.N.Children {
				if x, ok := ch.(*syntax.Node); ok && x.Kind == syntax.Annotation {
					mark(resolve.Site{File: p.File, N: x, Scope: p.Scope})
				}
			}
			// `@lens` on an element that declares no node marks its
			// content, and is checked as a mark on a prose run.
			p.N.Walk(func(x *syntax.Node) bool {
				if x.Kind == syntax.Tag && t.ElementNode(x) != nil && x != p.N {
					// A node of its own, whose text is checked with it.
					return false
				}
				if x.Kind == syntax.Tag && t.ElementNode(x) == nil {
					for _, a := range ElementMarks(resolve.Site{File: p.File, N: x, Scope: p.Scope}) {
						for _, e := range project.ArgExprs(ast.Annotation{N: a.N}.Args()) {
							lensName(e, a)
						}
					}
				}
				return true
			})
		}
		if IsAuthored(c, n) && examples != nil {
			checkExamples(c, n, examples, &ds)
		}
		if IsLens(c, n) {
			lensNodes = append(lensNodes, n)
		}
		return true
	})
	if d := DocsNode(t, c); d != nil {
		// Lenses declared in the docs block, which amends std/docs's.
		for _, n := range d.Children {
			if IsLens(c, n) && n.Decl.File != nil && !n.Decl.File.ID.IsStd() {
				lensNodes = append(lensNodes, n)
			}
		}
	}
	for _, n := range lensNodes {
		for _, ch := range n.Children {
			if ch.Value == nil {
				continue
			}
			switch ch.Name {
			case "show", "summarize", "hide", "entry":
				arr, ok := ch.Value.(ast.ArrayExpr)
				if !ok {
					continue
				}
				for _, e := range arr.Exprs() {
					x := resolveRef(c, e.Syntax(), ch.ValueSite, nil)
					if x == nil {
						continue
					}
					es := resolve.Site{File: ch.ValueSite.File, N: e.Syntax()}
					name := strings.TrimSpace(e.Syntax().Text())
					if ch.Name == "entry" {
						if !IsAuthored(c, x) {
							errAt(&ds, es, "lens", "%s is a %s; a lens's entry lists guides, how-tos, concepts and tutorials", name, kindName(c, x))
						}
					} else if !c.IsKind(x) && !c.IsCategory(x) {
						errAt(&ds, es, "lens", "%s is a %s; a lens's %s lists kinds and categories", name, kindName(c, x), ch.Name)
					}
				}
			case "prefix":
				p, _ := resolve.StringValue(ch.Value.Syntax())
				if reason := prefixProblem(p); reason != "" {
					errAt(&ds, resolve.Site{File: ch.ValueSite.File, N: ch.Value.Syntax()}, "lens", "%s: %s", n.Name, reason)
				}
			}
		}
	}

	d := DocsNode(t, c)
	if d == nil {
		return ds, replaced
	}
	lc := d.Child("lenses")
	if lc == nil || lc.Value == nil {
		return ds, replaced
	}
	site := lc.ValueSite
	list := resolve.Site{File: site.File, N: lc.Value.Syntax()}
	arr, ok := lc.Value.(ast.ArrayExpr)
	if !ok {
		return ds, replaced
	}
	for _, e := range arr.Exprs() {
		lensName(e, site)
	}
	s := SiteOf(t, c)
	if len(arr.Exprs()) == 0 {
		errAt(&ds, list, "lens", "the site builds no lens: name at least one in lenses")
		return ds, replaced
	}
	var defaults, names []string
	prefixes := map[string]string{}
	for _, l := range s.Lenses {
		names = append(names, l.Name)
		if l.Default {
			defaults = append(defaults, l.Name)
			continue
		}
		if l.Prefix == "" {
			errAt(&ds, list, "lens", "%s: only the default lens is served at the site's root; give it a prefix", l.Name)
			continue
		}
		if o, ok := prefixes[l.Prefix]; ok {
			errAt(&ds, list, "lens", "%s and %s are both served under %s/", o, l.Name, l.Prefix)
		}
		prefixes[l.Prefix] = l.Name
	}
	if len(s.Lenses) > 0 && len(defaults) != 1 {
		errAt(&ds, list, "lens", "a site has exactly one default lens; %d of %s set default = true", len(defaults), strings.Join(names, ", "))
	}
	return ds, replaced
}

// prefixProblem says what's wrong with a lens's prefix, or "".
func prefixProblem(p string) string {
	if p == "" {
		return ""
	}
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return fmt.Sprintf("the prefix %q isn't one path segment of lower-case letters, digits and -", p)
		}
	}
	return ""
}

// kindName is the name of a node's kind, or what it declares.
func kindName(c *types.Checker, n *resolve.Node) string {
	if k := c.KindOf(n); k != nil {
		return k.Name
	}
	switch {
	case c.IsKind(n):
		return "kind"
	case c.IsCategory(n):
		return "category"
	case c.IsType(n):
		return "type"
	}
	return "node"
}

// checkExamples checks the Heron code blocks of an authored page's
// examples, at any depth below it, each as a tree of its own.
func checkExamples(c *types.Checker, page *resolve.Node, check ExampleChecker, ds *diag.List) {
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range n.Children {
			if project.KindPath(c, ch) == "spec.example" {
				for _, p := range ch.Prose {
					for _, f := range Fences(p.N) {
						if f.Lang != "heron" {
							continue
						}
						if msg, line, ok := check(f.Code); !ok {
							errAt(ds, resolve.Site{File: p.File, N: f.Node}, "example", "the example %s doesn't check: %s (line %d)", relPath(ch), msg, line)
						}
					}
				}
			}
			if !IsAuthored(c, ch) {
				walk(ch)
			}
		}
	}
	walk(page)
}

// relPath is a node's path inside its package, as `learn.getting_started`.
func relPath(n *resolve.Node) string {
	if pkg := project.PackageOf(n); pkg != nil {
		return project.RelPath(pkg, n)
	}
	var parts []string
	for x := n; x != nil && x.Parent != nil; x = x.Parent {
		if resolve.IsPackageNode(x) && x.Parent.Parent == nil {
			break
		}
		parts = append([]string{x.Name}, parts...)
	}
	return strings.Join(parts, ".")
}

// Fence is a fenced code block of a prose block.
type Fence struct {
	Node *syntax.Node
	Lang string
	Code string
}

// Fences returns the fenced code blocks of a prose block, their common
// indentation removed.
func Fences(n *syntax.Node) []Fence {
	var out []Fence
	if n == nil {
		return nil
	}
	n.Walk(func(x *syntax.Node) bool {
		if x.Kind != syntax.Fence {
			return true
		}
		lines := syntax.FenceLines(x)
		if len(lines) == 0 {
			return false
		}
		indent := -1
		for _, l := range lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			k := len(l) - len(strings.TrimLeft(l, " \t"))
			if indent < 0 || k < indent {
				indent = k
			}
		}
		for i, l := range lines {
			if indent > 0 && len(l) >= indent {
				lines[i] = l[indent:]
			}
		}
		lang := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(lines[0]), "`~"))
		body := lines[1:]
		if len(body) > 0 && strings.HasPrefix(strings.TrimSpace(body[len(body)-1]), "```") {
			body = body[:len(body)-1]
		}
		out = append(out, Fence{Node: x, Lang: lang, Code: strings.Join(body, "\n") + "\n"})
		return false
	})
	return out
}

func errAt(ds *diag.List, s resolve.Site, code, format string, args ...any) {
	start, end := s.Span()
	file := ""
	if s.File != nil {
		file = string(s.File.ID)
	}
	ds.Errorf(file, start, end, code, "%s", fmt.Sprintf(format, args...))
}
