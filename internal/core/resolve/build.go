package resolve

import (
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/helightdev/specs-v0/internal/core/ast"
	"github.com/helightdev/specs-v0/internal/core/diag"
	"github.com/helightdev/specs-v0/internal/core/syntax"
)

// Resolver resolves entry points into trees. It memoizes packages, so a
// watch loop resolves the standard packages once.
type Resolver struct {
	Loader *Loader
	// PackageDirs are searched for packages that aren't standard ones:
	// "acme/widgets@1" is <dir>/acme/widgets/package.heron. This stands in
	// for the registry until heron.lock and setup exist.
	PackageDirs []string
	// Context, when set, is the outermost scope of the trees Resolve reads
	// (not of the packages they import), for excerpts that refer to names
	// declared elsewhere.
	Context *Scope

	packages map[string]*pkgEntry
	inflight map[string]*Package
	stack    []string
}

type pkgEntry struct {
	pkg    *Package
	hashes map[FileID][32]byte
}

// New returns a resolver reading through the loader.
func New(l *Loader) *Resolver {
	return &Resolver{Loader: l, packages: map[string]*pkgEntry{}, inflight: map[string]*Package{}}
}

// PreludeName is the package every tree sees without importing it: the
// core types, flags and annotations the Core format tab says need no import.
const PreludeName = "std/core"

// Resolve reads the tree of an entry point.
func (r *Resolver) Resolve(entry FileID) *Tree {
	return r.resolveTree(entry, true, nil, r.Context)
}

func (r *Resolver) resolve(entry FileID, prelude bool, self *Package) *Tree {
	return r.resolveTree(entry, prelude, self, nil)
}

// resolve reads a tree. A package's tree sees its own names, including the
// members of its package node, throughout its files, as its importers do.
func (r *Resolver) resolveTree(entry FileID, prelude bool, self *Package, ctx *Scope) *Tree {
	t := &Tree{includes: map[FileID][]FileID{}, scopes: map[FileID]*Scope{}, Targets: map[*syntax.Node]FileID{}}
	t.Root = &Node{Shape: ShapeRoot, Package: self}
	base := ctx
	if base == nil {
		base = Builtins()
	}
	outer := &Scope{Parent: base}
	t.Context = ctx
	b := &builder{r: r, t: t, included: map[FileID]*Scope{}}
	if prelude {
		if p := b.pkg(PreludeName+"@1", nil, Site{}); p != nil {
			outer.Imports = append(outer.Imports, p)
		}
	}
	if self != nil {
		outer.Imports = append(outer.Imports, self)
	}
	f, err := r.Loader.Load(entry)
	if err != nil {
		t.Diags.Errorf(string(entry), 0, 0, "read", "cannot read the entry point: %v", err)
		return t
	}
	t.Entry = f
	t.Root.Decl = Site{File: f}
	b.file(f, t.Root, outer)
	return t
}

type builder struct {
	r        *Resolver
	t        *Tree
	included map[FileID]*Scope
}

func (b *builder) errAt(s Site, code, format string, args ...any) {
	start, end := s.Span()
	file := ""
	if s.File != nil {
		file = string(s.File.ID)
	}
	b.t.Diags.Errorf(file, start, end, code, format, args...)
}

func related(s Site, msg string) diag.Related {
	start, end := s.Span()
	file := ""
	if s.File != nil {
		file = string(s.File.ID)
	}
	return diag.Related{File: file, Start: start, End: end, Message: msg}
}

// file reads a file's members into target, in a scope of its own.
func (b *builder) file(f *File, target *Node, parent *Scope) {
	sc := &Scope{Parent: parent, Node: target, File: f}
	b.included[f.ID] = sc
	b.t.scopes[f.ID] = sc
	b.t.Files = append(b.t.Files, f)
	for _, d := range f.Tree.Diags {
		b.t.Diags.Errorf(string(f.ID), d.Start, d.End, "syntax", "%s", d.Message)
	}
	switch f.Tree.Root.Kind {
	case syntax.File:
		b.members(f, ast.File{N: f.Tree.Root}.Members(), target, sc)
	case syntax.ProseFile:
		b.proseFile(f, target, sc)
	}
}

func (b *builder) members(f *File, ms []ast.Member, target *Node, sc *Scope) {
	for _, m := range ms {
		b.member(f, m, target, sc)
	}
}

func (b *builder) member(f *File, m ast.Member, target *Node, sc *Scope) {
	site := Site{File: f, N: m.Syntax(), Scope: sc}
	switch m := m.(type) {
	case ast.Declare:
		if b.sealedPath(site, PathSegs(m.Path().N), target, sc, false) {
			return
		}
		n := b.declare(site, PathSegs(m.Path().N), target, ShapeDeclared)
		if n != nil {
			body := b.bodyScope(f, n, sc, m.Params())
			b.body(f, m.Body(), n, body)
		}
	case ast.Value:
		if b.sealedPath(site, PathSegs(m.Path().N), target, sc, false) {
			return
		}
		n := b.declare(site, PathSegs(m.Path().N), target, ShapeValue)
		if n != nil {
			b.bodyScope(f, n, sc, m.Params())
			n.Value, n.ValueSite = m.Expr(), site
		}
	case ast.Enter:
		if b.sealedPath(site, PathSegs(m.Path().N), target, sc, true) {
			return
		}
		n := b.walk(target, PathSegs(m.Path().N), site, ShapeImplicit)
		n.Sites = append(n.Sites, site)
		b.attach(n, site)
		body := &Scope{Parent: sc, Node: n, File: f}
		n.Body = body
		b.body(f, m.Body(), n, body)
	case ast.TextNode:
		n := b.anon(target, site)
		n.Prose = append(n.Prose, Site{File: f, N: m.ProseBlock().N, Scope: sc})
	case ast.Invoke:
		n := b.anon(target, site)
		body := &Scope{Parent: sc, Node: n, File: f}
		n.Body = body
		b.body(f, m.Body(), n, body)
		if e := m.Expr(); e != nil {
			n.Value, n.ValueSite = e, site
		}
	case ast.Scope:
		segs := PathSegs(m.Path().N)
		if len(segs) == 0 {
			before := len(target.Children)
			b.members(f, m.StructBody().Members(), target, sc)
			for _, c := range target.Children[before:] {
				c.GroupAnnotations = append(c.GroupAnnotations, annotationSites(site)...)
			}
			return
		}
		if b.sealedPath(site, segs, target, sc, true) {
			return
		}
		n := b.walk(target, segs, site, ShapeScoped)
		n.Sites = append(n.Sites, site)
		b.attach(n, site)
		body := &Scope{Parent: sc, Node: n, File: f}
		n.Body = body
		b.members(f, m.StructBody().Members(), n, body)
	case ast.Set:
		b.set(site, m, target, sc)
	case ast.Let:
		if id := m.Ident(); id != nil {
			sc.addLocal(&Local{Name: id.Text, Site: site, Type: m.Type(), Value: m.Expr(), Offset: id.Start})
		}
	case ast.Action:
		target.Code = append(target.Code, site)
	case ast.ProseLines, ast.ProseBlock:
		target.Prose = append(target.Prose, site)
	case ast.CommentLine, ast.ErrorNode, ast.Doc:
	}
}

// bodyScope gives a declared node the scope its body, parameters and value
// are read in.
func (b *builder) bodyScope(f *File, n *Node, sc *Scope, params ast.Params) *Scope {
	body := &Scope{Parent: sc, Node: n, File: f}
	for _, p := range params.Params() {
		if id := p.Ident(); id != nil {
			body.addLocal(&Local{Name: id.Text, Site: Site{File: f, N: p.N, Scope: sc}, Type: p.Type(), Value: p.Default(), Param: true, Offset: -1})
		}
	}
	n.Body = body
	return body
}

func (b *builder) body(f *File, body ast.Body, n *Node, sc *Scope) {
	switch body := body.(type) {
	case ast.StructBody:
		b.members(f, body.Members(), n, sc)
	case ast.ProseBlock:
		n.Prose = append(n.Prose, Site{File: f, N: body.N, Scope: sc})
	case ast.DoBlock:
		n.Code = append(n.Code, Site{File: f, N: body.N, Scope: sc})
	}
}

// attach records a site's annotations and docs on a node.
func (b *builder) attach(n *Node, s Site) {
	n.Annotations = append(n.Annotations, annotationSites(s)...)
	if d := childNode(s.N, syntax.Doc); d != nil {
		n.Docs = append(n.Docs, Site{File: s.File, N: d, Scope: s.Scope})
	}
}

func annotationSites(s Site) []Site {
	var out []Site
	if s.N == nil {
		return nil
	}
	for _, c := range s.N.Children {
		if c, ok := c.(*syntax.Node); ok && c.Kind == syntax.Annotation {
			out = append(out, Site{File: s.File, N: c, Scope: s.Scope})
		}
	}
	return out
}

// walk follows a path from a node, creating what's missing with the given
// shape. Nodes created on the way are implicit.
func (b *builder) walk(from *Node, segs []string, site Site, last Shape) *Node {
	n := from
	for i, seg := range segs {
		c := n.Child(seg)
		if c != nil && c.Parent != n {
			// A child of a mounted node's origin: amend a local copy.
			cp := &Node{Name: seg, Shape: ShapeMounted, Origin: c, Decl: c.Decl}
			n.add(cp)
			c = cp
		}
		if c == nil {
			shape := ShapeImplicit
			if i == len(segs)-1 {
				shape = last
			}
			c = &Node{Name: seg, Shape: shape, Decl: site, Entered: shape == ShapeImplicit}
			n.add(c)
		}
		n = c
	}
	return n
}

func (b *builder) anon(target *Node, site Site) *Node {
	n := &Node{Shape: ShapeAnon, Decl: site}
	target.add(n)
	b.attach(n, site)
	return n
}

// declare makes a node once; a second declaration of the name conflicts.
func (b *builder) declare(site Site, segs []string, target *Node, shape Shape) *Node {
	if len(segs) == 0 {
		return nil
	}
	parent := b.walk(target, segs[:len(segs)-1], site, ShapeImplicit)
	name := segs[len(segs)-1]
	if old := parent.byName[name]; old != nil {
		switch old.Shape {
		case ShapeImplicit, ShapeScoped:
			// Entered or scoped before the declaration was read: the
			// declaration completes it.
			old.Sites = append(old.Sites, old.Decl)
			old.Shape, old.Decl, old.Entered = shape, site, false
			b.attach(old, site)
			return old
		}
		if hasFlag(old, "overload") && hasFlagAt(site, "overload") {
			// Another signature of the same kind, tried in declaration order.
			n := &Node{Name: name, Shape: shape, Decl: site, Parent: parent}
			parent.Children = append(parent.Children, n)
			old.Overloads = append(old.Overloads, n)
			b.attach(n, site)
			return n
		}
		d := diag.Diagnostic{Severity: diag.Error, Code: "conflict",
			Message: fmt.Sprintf("%s is declared twice", qualified(parent, name))}
		d.File = string(site.File.ID)
		d.Start, d.End = site.Span()
		d.Related = []diag.Related{related(old.Decl, "first declared here")}
		b.t.Diags.Add(d)
		return nil
	}
	n := &Node{Name: name, Shape: shape, Decl: site}
	parent.add(n)
	b.attach(n, site)
	return n
}

func qualified(parent *Node, name string) string {
	if p := parent.Path(); p != "" {
		return p + "." + name
	}
	return name
}

// set binds a value by path. Maps merge by key and everything else
// replaces; setting one path twice in a body, or from two files neither of
// which includes the other, is an error.
func (b *builder) set(site Site, m ast.Set, target *Node, sc *Scope) {
	segs := PathSegs(m.Path().N)
	if len(segs) == 1 {
		switch segs[0] {
		case "import":
			b.imports(site, m.Expr(), sc)
			return
		case "include":
			b.includes(site, m.Expr(), target, sc)
			return
		}
	}
	if len(segs) == 0 || b.sealedPath(site, segs, target, sc, false) {
		return
	}
	b.attachSet(site, segs, m.Expr(), target, sc)
}

// sealedPath reports, with an error at the site, a path that would amend,
// scope or set into a node of a sealed package from outside it. Every
// segment but the last is entered, and the last too when whole is set, as
// for `a.b.{ … }`; a last segment that only declares or sets a name of the
// tree's own is a new node, not an amendment. The first segment may name
// the package's nodes through an import, as `shop.checkout` does once
// "../shop/" is imported.
func (b *builder) sealedPath(site Site, segs []string, target *Node, sc *Scope, whole bool) bool {
	n := target
	for i, seg := range segs {
		if i == len(segs)-1 && !whole {
			// The name itself: setting or declaring it amends a sealed
			// node only when the node it lands on is one.
			c := n.Child(seg)
			if c == nil {
				return false
			}
			n = c
		} else {
			c := n.Child(seg)
			if c == nil && i == 0 && sc != nil {
				c = sc.Lookup(seg, nil, -1).Node
			}
			if c == nil {
				return false
			}
			n = c
		}
		if p := b.sealedOf(n); p != nil {
			b.errAt(site, "sealed", "%s is in the sealed package %s: nothing outside it may amend, scope or set into its nodes; import it and refer to them instead", strings.Join(segs, "."), p.Display())
			return true
		}
	}
	return false
}

// sealedOf returns the sealed package a node belongs to, through the
// origins of mounted nodes, when that package isn't the tree being built.
func (b *builder) sealedOf(n *Node) *Package {
	for o := n; o != nil; o = o.Origin {
		r := o
		for r.Parent != nil {
			r = r.Parent
		}
		if r != b.t.Root && r.Package != nil && r.Package.Sealed {
			return r.Package
		}
		if o.Origin == nil {
			break
		}
	}
	return nil
}

func (b *builder) attachSet(site Site, segs []string, e ast.Expr, target *Node, sc *Scope) {
	parent := b.walk(target, segs[:len(segs)-1], site, ShapeImplicit)
	name := segs[len(segs)-1]
	leaf := parent.Child(name)
	if leaf != nil && leaf.Parent != parent {
		leaf = b.walk(parent, []string{name}, site, ShapeSet)
	}
	if leaf == nil {
		leaf = &Node{Name: name, Shape: ShapeSet, Decl: site}
		parent.add(leaf)
	} else if old := leaf.ValueSite; old.N != nil && old.N != site.N {
		switch {
		case old.Scope == sc && old.File == site.File:
			b.conflict(site, old, fmt.Sprintf("%s is set twice in one body", qualified(parent, name)), "set here first")
			return
		case old.File != site.File && !b.t.Includes(site.File.ID, old.File.ID) && !b.t.Includes(old.File.ID, site.File.ID):
			b.conflict(site, old, fmt.Sprintf("%s is set in two files, and neither includes the other", qualified(parent, name)), "also set here")
			return
		}
	}
	leaf.Sites = append(leaf.Sites, site)
	b.attach(leaf, site)
	leaf.Value, leaf.ValueSite = e, site
	// A map value replaces the node's set members and binds each key, so
	// later paths amend it by key.
	if mp, ok := e.(ast.MapExpr); ok {
		for _, c := range append([]*Node(nil), leaf.Children...) {
			if c.Shape == ShapeSet {
				leaf.remove(c)
			}
		}
		for _, ent := range mp.MapEntries() {
			ks := PathSegs(ent.Path().N)
			if len(ks) > 0 {
				b.attachSet(Site{File: site.File, N: ent.N, Scope: sc}, ks, ent.Expr(), leaf, sc)
			}
		}
	}
}

func (b *builder) conflict(site, old Site, msg, rel string) {
	d := diag.Diagnostic{Severity: diag.Error, Code: "conflict", Message: msg, File: string(site.File.ID)}
	d.Start, d.End = site.Span()
	d.Related = []diag.Related{related(old, rel)}
	b.t.Diags.Add(d)
}

// stringList reads a directive's value: a list of plain strings.
func (b *builder) stringList(site Site, e ast.Expr, what string) []stringAt {
	arr, ok := e.(ast.ArrayExpr)
	if !ok {
		b.errAt(site, "directive", "%s takes a list of strings", what)
		return nil
	}
	var out []stringAt
	for _, x := range arr.Exprs() {
		s, ok := StringValue(x.Syntax())
		if !ok {
			b.errAt(Site{File: site.File, N: x.Syntax()}, "directive", "%s entries are plain strings", what)
			continue
		}
		out = append(out, stringAt{s, Site{File: site.File, N: x.Syntax(), Scope: site.Scope}})
	}
	return out
}

type stringAt struct {
	s    string
	site Site
}

// --- include ---

func (b *builder) includes(site Site, e ast.Expr, target *Node, sc *Scope) {
	entries := b.stringList(site, e, "include")
	dir := site.File.ID.Dir()
	type inc struct {
		id    FileID
		mount string
		site  Site
	}
	var list []inc
	excluded := map[FileID]bool{}
	for _, ent := range entries {
		s := ent.s
		if strings.HasPrefix(s, "!") {
			if id, err := join(dir, s[1:]); err == nil {
				excluded[id] = true
			}
			continue
		}
		if strings.ContainsAny(s, "*?[") {
			ids, err := b.r.Loader.glob(dir, s)
			if err != nil {
				b.errAt(ent.site, "include", "%v", err)
			}
			for _, id := range ids {
				if strings.HasSuffix(string(id), ".heron") || strings.HasSuffix(string(id), ".heron.md") {
					list = append(list, inc{id: id, site: ent.site})
				}
			}
			continue
		}
		id, err := join(dir, s)
		if err != nil {
			b.errAt(ent.site, "include", "%v", err)
			continue
		}
		if strings.HasSuffix(s, "/") || b.r.Loader.isDir(id) {
			name := folderName(id)
			idx, _ := join(string(id), "index.heron")
			list = append(list, inc{id: idx, mount: name, site: ent.site})
			b.t.Targets[ent.site.N] = idx
			continue
		}
		list = append(list, inc{id: id, site: ent.site})
		b.t.Targets[ent.site.N] = id
	}
	seen := map[FileID]bool{}
	for _, in := range list {
		if excluded[in.id] || seen[in.id] {
			continue
		}
		seen[in.id] = true
		b.t.includes[site.File.ID] = append(b.t.includes[site.File.ID], in.id)
		if fsc := b.included[in.id]; fsc != nil {
			// The include guard: the file is in the tree already, so this
			// include only lets it see this scope too.
			fsc.Also = append(fsc.Also, sc)
			continue
		}
		f, err := b.r.Loader.Load(in.id)
		if err != nil {
			b.errAt(in.site, "include", "cannot include %s: no such file", displayPath(in.id))
			continue
		}
		t := target
		if in.mount != "" {
			t = b.walk(target, []string{in.mount}, in.site, ShapeScoped)
		}
		b.file(f, t, sc)
	}
}

func folderName(id FileID) string {
	s := strings.TrimPrefix(string(id), stdPrefix)
	s = strings.TrimRight(filepath.ToSlash(s), "/")
	return path.Base(s)
}

func displayPath(id FileID) string { return string(id) }

// --- import ---

func (b *builder) imports(site Site, e ast.Expr, sc *Scope) {
	for _, ent := range b.stringList(site, e, "import") {
		p := b.pkg(ent.s, site.File, ent.site)
		if p == nil {
			continue
		}
		if p.Tree != nil && p.Tree.Entry != nil {
			b.t.Targets[ent.site.N] = p.Tree.Entry.ID
		}
		dup := false
		for _, q := range sc.Imports {
			if q == p {
				dup = true
			}
		}
		if !dup {
			sc.Imports = append(sc.Imports, p)
			b.t.Imports = append(b.t.Imports, p)
		}
		b.mount(p, ent.site)
	}
}

// mount puts a package's <export> nodes at the tree's root, once however
// often it's imported.
func (b *builder) mount(p *Package, site Site) {
	root := b.t.Root
	for _, ex := range p.Exports {
		old := root.byName[ex.Name]
		switch {
		case old == nil:
			root.add(&Node{Name: ex.Name, Shape: ShapeMounted, Origin: ex, Decl: ex.Decl})
		case old.Origin == ex:
		case old.Shape == ShapeImplicit:
			old.Shape, old.Origin, old.Entered = ShapeMounted, ex, false
			old.Sites = append(old.Sites, old.Decl)
			old.Decl = ex.Decl
		case old.Shape == ShapeMounted:
			d := diag.Diagnostic{Severity: diag.Error, Code: "conflict", File: string(site.File.ID),
				Message: fmt.Sprintf("two packages export %s", ex.Name)}
			d.Start, d.End = site.Span()
			d.Related = []diag.Related{related(old.Decl, "exported here"), related(ex.Decl, "and here")}
			b.t.Diags.Add(d)
		default:
			b.conflict(site, old.Decl, fmt.Sprintf("%s is exported by %s and also declared in this tree", ex.Name, p.Name), "declared here")
		}
	}
}

// pkg resolves an import string to a package: "std/doc@1", a name found in
// a package directory, or a local file or folder such as "../spec/".
func (b *builder) pkg(spec string, from *File, site Site) *Package {
	var key string
	var entry FileID
	major := 0
	name := spec
	alias := ""
	var git *GitImport
	switch {
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/"):
		if from == nil {
			return nil
		}
		id, err := join(from.ID.Dir(), spec)
		if err != nil {
			b.errAt(site, "import", "%v", err)
			return nil
		}
		if strings.HasSuffix(spec, "/") || b.r.Loader.isDir(id) {
			alias = folderName(id)
			id, _ = join(string(id), "index.heron")
		}
		if !b.r.Loader.exists(id) {
			b.errAt(site, "import", "cannot import %s: no such file", spec)
			return nil
		}
		key, entry = string(id), id
	case strings.HasPrefix(spec, "npm:"):
		b.errAt(site, "import", "%s: packages from npm and git are installed by `heron run setup`, which isn't available yet", spec)
		return nil
	case strings.HasPrefix(spec, "git+"):
		g, err := ParseGitImport(spec)
		if err != nil {
			b.errAt(site, "import", "%v", err)
			return nil
		}
		b.t.GitImports = append(b.t.GitImports, g)
		entry = b.r.findGit(g, from)
		if entry == "" {
			b.errAt(site, "import", "%s: packages from git are installed by `heron run setup`, and this one isn't installed yet", spec)
			return nil
		}
		name, major, git = g.Name, g.Major, &g
		key = name + "@" + strconv.Itoa(major)
	default:
		at := strings.LastIndexByte(spec, '@')
		if at <= 0 {
			b.errAt(site, "import", "%s: a package import names a major version, as in %s@1", spec, spec)
			return nil
		}
		name = spec[:at]
		v, err := strconv.Atoi(strings.TrimPrefix(spec[at+1:], "v"))
		if err != nil {
			b.errAt(site, "import", "%s: the version after @ is a major version number", spec)
			return nil
		}
		major = v
		key = name + "@" + strconv.Itoa(major)
		if strings.HasPrefix(name, "std/") {
			entry = FileID(stdPrefix + strings.TrimPrefix(name, "std/") + "/package.heron")
		} else {
			for _, d := range b.r.PackageDirs {
				id, err := join(d, name+"/package.heron")
				if err == nil && b.r.Loader.exists(id) {
					entry = id
					break
				}
			}
		}
		if entry == "" || !b.r.Loader.exists(entry) {
			b.errAt(site, "import", "unknown package %s", name)
			return nil
		}
	}
	for i, k := range b.r.stack {
		if k == key {
			// The standard packages that define the language refer to each
			// other: std/schema types its kinds with std/types, which is
			// declared with std/schema. Names resolve lazily, so this one
			// kind of cycle is finite; any other import cycle is an error.
			if p := b.r.inflight[key]; p != nil && entry.IsStd() {
				return p
			}
			cycle := append(append([]string(nil), b.r.stack[i:]...), key)
			b.errAt(site, "import-cycle", "import cycle: %s", strings.Join(cycle, " → "))
			return nil
		}
	}
	if e := b.r.packages[key]; e != nil && b.r.fresh(e) {
		return e.pkg
	}
	p := &Package{Name: name, Major: major, Alias: alias, Git: git}
	b.r.inflight[key] = p
	b.r.stack = append(b.r.stack, key)
	t := b.r.resolve(entry, name != PreludeName, p)
	b.r.stack = b.r.stack[:len(b.r.stack)-1]
	delete(b.r.inflight, key)
	p.Tree = t
	p.Exports, p.Version = exportsOf(t)
	for _, c := range t.Root.Children {
		if isPackageNode(c) {
			p.Node = c
			p.Sealed = KindArg(c, "sealed") == "true"
			break
		}
	}
	if major > 0 && p.Version != "" {
		if mv, _ := strconv.Atoi(strings.SplitN(p.Version, ".", 2)[0]); mv != major {
			b.errAt(site, "import", "%s@%d: the package is version %s", name, major, p.Version)
		}
	}
	hashes := map[FileID][32]byte{}
	for _, f := range t.Files {
		hashes[f.ID] = f.Hash
	}
	for _, q := range t.Imports {
		if q.Tree == nil {
			continue
		}
		for _, f := range q.Tree.Files {
			hashes[f.ID] = f.Hash
		}
	}
	b.r.packages[key] = &pkgEntry{pkg: p, hashes: hashes}
	return p
}

// fresh reports whether every file of a memoized package is unchanged.
func (r *Resolver) fresh(e *pkgEntry) bool {
	for id, h := range e.hashes {
		f, err := r.Loader.Load(id)
		if err != nil || f.Hash != h {
			return false
		}
	}
	return true
}

// exportsOf finds the <export> nodes of a package tree and the version its
// package node declares.
func exportsOf(t *Tree) (exports []*Node, version string) {
	var visit func(n *Node, depth int)
	visit = func(n *Node, depth int) {
		for _, c := range n.Children {
			if c.Shape == ShapeDeclared || c.Shape == ShapeValue {
				for _, fl := range NameFlags(c) {
					if fl == "export" {
						exports = append(exports, c)
					}
				}
			}
			if depth == 0 && isPackageNode(c) {
				if v := KindArg(c, "version"); v != "" && version == "" {
					version = v
				}
				visit(c, 1)
			}
		}
	}
	visit(t.Root, 0)
	return exports, version
}

// NameFlags returns the flags written on a node's name, as in
// `development<export>`.
func NameFlags(n *Node) []string {
	if n.Decl.N == nil {
		return nil
	}
	fl := childNode(n.Decl.N, syntax.Flags)
	if fl == nil {
		return nil
	}
	var out []string
	for _, t := range (ast.Flags{N: fl}).Idents() {
		out = append(out, t.Text)
	}
	return out
}

// KindArg returns a named string argument of a node's kind, such as the
// version in `package(version = "1.0")`; any other literal, such as the
// `true` of `sealed = true`, comes back as written.
func KindArg(n *Node, name string) string {
	if n.Decl.N == nil || n.Decl.N.Kind != syntax.Declare {
		return ""
	}
	t, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType)
	if !ok {
		return ""
	}
	for _, a := range t.Args().Args() {
		if na, ok := a.(ast.NamedArg); ok && na.Ident() != nil && na.Ident().Text == name {
			if s, ok := StringValue(na.Expr().Syntax()); ok {
				return s
			}
			return strings.TrimSpace(na.Expr().Syntax().Text())
		}
	}
	return ""
}

// --- prose files ---

func (b *builder) proseFile(f *File, target *Node, sc *Scope) {
	type open struct {
		level int
		node  *Node
		scope *Scope
	}
	var stack []open
	cur := func() (*Node, *Scope) {
		if len(stack) == 0 {
			return target, sc
		}
		top := stack[len(stack)-1]
		return top.node, top.scope
	}
	for _, it := range (ast.ProseFile{N: f.Tree.Root}).ProseItems() {
		site := Site{File: f, N: it.Syntax(), Scope: sc}
		switch it := it.(type) {
		case ast.Heading:
			level := strings.Count(strings.TrimSpace(it.HeadingMark().Text), "#")
			for len(stack) > 0 && stack[len(stack)-1].level >= level {
				stack = stack[:len(stack)-1]
			}
			parent, psc := cur()
			name := headingName(it)
			if name == "" {
				continue
			}
			site.Scope = psc
			n := b.declare(site, []string{name}, parent, ShapeHeading)
			if n == nil {
				continue
			}
			body := &Scope{Parent: psc, Node: n, File: f}
			n.Body = body
			stack = append(stack, open{level, n, body})
		case ast.HeronFence:
			n, s := cur()
			b.members(f, it.Members(), n, s)
		default:
			n, s := cur()
			site.Scope = s
			n.Prose = append(n.Prose, site)
		}
	}
}

// headingName is the section name a heading declares: its {#id}, or its
// text in snake_case.
func headingName(h ast.Heading) string {
	if id := h.HeadingId(); id != nil {
		s := strings.TrimSpace(id.Text)
		s = strings.TrimPrefix(strings.TrimSuffix(s, "}"), "{#")
		return s
	}
	var text strings.Builder
	for _, c := range h.N.Children {
		switch c := c.(type) {
		case *syntax.Token:
			if c.Kind == syntax.Text || c.Kind == syntax.CodeSpan {
				text.WriteString(c.Text)
			}
		case *syntax.Node:
			text.WriteString(c.Text())
		}
	}
	return Slug(text.String())
}

// Slug turns display text into a name: lower case, with runs of anything
// that isn't a letter or digit as one underscore.
func Slug(s string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if under && b.Len() > 0 {
				b.WriteByte('_')
			}
			under = false
			b.WriteRune(r)
		} else {
			under = true
		}
	}
	return b.String()
}

// Package resolves a package by its import string, such as "std/doc@1", as
// an import would, so a package can be checked on its own.
func (r *Resolver) Package(spec string) (*Package, diag.List) {
	t := &Tree{includes: map[FileID][]FileID{}, scopes: map[FileID]*Scope{}, Root: &Node{Shape: ShapeRoot}}
	b := &builder{r: r, t: t, included: map[FileID]*Scope{}}
	p := b.pkg(spec, nil, Site{})
	return p, t.Diags
}

func hasFlag(n *Node, flag string) bool {
	for _, f := range NameFlags(n) {
		if f == flag {
			return true
		}
	}
	return false
}

func hasFlagAt(s Site, flag string) bool { return hasFlag(&Node{Decl: s}, flag) }
