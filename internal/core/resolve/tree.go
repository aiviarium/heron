// Package resolve follows an entry point into one node tree: `import` mounts
// packages, `include` places a tree's own files, bindings amend by path, and
// names resolve through scopes. Its output is the tree every later stage
// reads.
package resolve

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// Shape is how a node came to exist.
type Shape int

const (
	ShapeRoot     Shape = iota // the root of a tree
	ShapeDeclared              // name: kind …
	ShapeValue                 // name: Type = value
	ShapeAnon                  // kind(args) { … }, kind {{ … }}, or a call in a body
	ShapeScoped                // created by name.{ … } or a folder include
	ShapeSet                   // created by setting it: name = value on an open node
	ShapeImplicit              // reached by a deeper path, entered or set; types checks it
	ShapeMounted               // a package's <export> node, mounted at the root
	ShapeHeading               // a heading in a prose file
	ShapeElement               // a kind element in prose: named by its id, or a member of a named one
)

func (s Shape) String() string {
	return [...]string{"root", "declared", "value", "anonymous", "scope", "set", "implicit", "mounted", "heading", "element", "builtin", "step"}[s]
}

// Site is a member in a file, with the scope it was read in.
type Site struct {
	File  *File
	N     *syntax.Node
	Scope *Scope
	// Start and End, when End is set, are the part of N the site is, as a
	// use case step is a range of its body.
	Start, End int
}

// Span returns the site's byte range.
func (s Site) Span() (int, int) {
	if s.N == nil {
		return 0, 0
	}
	if s.End > s.Start {
		return s.Start, s.End
	}
	return s.N.Span()
}

// Node is a node of the resolved tree.
type Node struct {
	Name     string // "" for an anonymous node
	Parent   *Node
	Shape    Shape
	Children []*Node
	byName   map[string]*Node

	// Decl is the member that declared or created the node: a Declare,
	// Value, Invoke, TextNode, Set or Heading. Mounted nodes keep their
	// package's declaration.
	Decl Site
	// Sites lists every member that entered or amended the node, in reading
	// order; Decl is not repeated.
	Sites []Site
	// Body is the scope the node's members were read in, or nil.
	Body *Scope

	Value     ast.Expr // the value set or declared, or nil
	ValueSite Site
	// Prose and Code hold the node's own text and `do` blocks.
	Prose []Site
	Code  []Site
	// Annotations and Docs from every site, in reading order.
	Annotations []Site
	Docs        []Site

	// Origin is the package node a mounted node stands for.
	Origin *Node
	// Package is set on the root node of a package tree.
	Package *Package
	// Entered records that an enter or deeper path reached a node nothing
	// declared; types decides whether a type's field allows it.
	Entered bool
	// Group annotations from `@a .{ … }` that cover this node.
	GroupAnnotations []Site
	// Overloads are the further signatures of a node declared <overload>,
	// in declaration order; lookups find the first.
	Overloads []*Node
	// ElementArgs are a kind element's attributes as the arguments of its
	// kind, which the checker writes once it knows the kind's parameters.
	ElementArgs ast.Args
}

// Child returns the named child, looking through a mounted node to its
// package node.
func (n *Node) Child(name string) *Node {
	if c := n.byName[name]; c != nil {
		return c
	}
	if n.Origin != nil {
		return n.Origin.Child(name)
	}
	return nil
}

// AllChildren returns the node's children, followed by those of its origin
// that it doesn't shadow.
func (n *Node) AllChildren() []*Node {
	if n.Origin == nil {
		return n.Children
	}
	out := append([]*Node(nil), n.Children...)
	for _, c := range n.Origin.AllChildren() {
		if c.Name == "" || n.byName[c.Name] == nil {
			out = append(out, c)
		}
	}
	return out
}

func (n *Node) add(c *Node) {
	c.Parent = n
	n.Children = append(n.Children, c)
	if c.Name != "" {
		if n.byName == nil {
			n.byName = map[string]*Node{}
		}
		n.byName[c.Name] = c
	}
}

func (n *Node) remove(c *Node) {
	for i, x := range n.Children {
		if x == c {
			n.Children = append(n.Children[:i:i], n.Children[i+1:]...)
			break
		}
	}
	if c.Name != "" && n.byName[c.Name] == c {
		delete(n.byName, c.Name)
	}
}

// Path returns the node's path from the root, with anonymous nodes as
// their position, such as "refunds.late_rejected" or "payments.#2".
func (n *Node) Path() string {
	var segs []string
	for x := n; x != nil && x.Parent != nil; x = x.Parent {
		if x.Name != "" {
			segs = append(segs, x.Name)
		} else {
			for i, c := range x.Parent.Children {
				if c == x {
					segs = append(segs, "#"+itoa(i))
				}
			}
		}
	}
	for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
		segs[i], segs[j] = segs[j], segs[i]
	}
	return strings.Join(segs, ".")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// Local is a `let` binding, a parameter, or a code-local name.
type Local struct {
	Name  string
	Site  Site
	Type  ast.Type // nil when untyped
	Value ast.Expr // nil for parameters
	Param bool
	// Offset is where the local becomes visible in its file.
	Offset int
}

// Scope is where names are looked up: a node's children, the locals of its
// body, the packages imported there, and the enclosing scope.
type Scope struct {
	Parent  *Scope
	Node    *Node // whose children are visible; nil for a code scope
	File    *File
	Locals  map[string]*Local
	Imports []*Package
	// Also holds the scopes of later includes of the same file, which the
	// include guard turns into extra visibility instead of a second copy.
	Also []*Scope
}

func (s *Scope) addLocal(l *Local) {
	if s.Locals == nil {
		s.Locals = map[string]*Local{}
	}
	s.Locals[l.Name] = l
}

// Binding is what a name resolves to: a node, a local, or nothing.
type Binding struct {
	Node  *Node
	Local *Local
}

// Found reports whether the name resolved.
func (b Binding) Found() bool { return b.Node != nil || b.Local != nil }

// Lookup resolves a name at an offset in a file, through locals visible
// there, the scope's node, the imports in scope, and outward. The offset
// keeps a local invisible above the line that declares it; pass -1 to skip
// that check.
func (s *Scope) Lookup(name string, file *File, off int) Binding {
	var out Binding
	s.Each(name, file, off, func(b Binding) bool { out = b; return false })
	return out
}

// Each calls f for every binding of a name visible from the scope, innermost
// first, until f returns false. Lookups that depend on what a name is, such
// as a flag or an annotation, take the first binding that fits.
func (s *Scope) Each(name string, file *File, off int, f func(Binding) bool) {
	s.each(name, file, off, map[*Scope]bool{}, f)
}

func (s *Scope) each(name string, file *File, off int, seen map[*Scope]bool, f func(Binding) bool) bool {
	for sc := s; sc != nil; sc = sc.Parent {
		if seen[sc] {
			return true
		}
		seen[sc] = true
		if l := sc.Locals[name]; l != nil {
			if off < 0 || file == nil || l.Site.File != file || l.Offset <= off {
				if !f(Binding{Local: l}) {
					return false
				}
			}
		}
		if sc.Node != nil {
			if c := sc.Node.Child(name); c != nil {
				if !f(Binding{Node: c}) {
					return false
				}
			}
		}
		for _, p := range sc.Imports {
			if !p.each(name, func(n *Node) bool { return f(Binding{Node: n}) }) {
				return false
			}
		}
		for _, a := range sc.Also {
			if !a.each(name, file, off, seen, f) {
				return false
			}
		}
	}
	return true
}

// Names calls f for every name visible from the scope, innermost first, for
// completion and "did you mean" suggestions.
func (s *Scope) Names(f func(name string, b Binding)) {
	seen := map[*Scope]bool{}
	var walk func(*Scope)
	walk = func(s *Scope) {
		for sc := s; sc != nil && !seen[sc]; sc = sc.Parent {
			seen[sc] = true
			for n, l := range sc.Locals {
				f(n, Binding{Local: l})
			}
			if sc.Node != nil {
				for _, c := range sc.Node.AllChildren() {
					if c.Name != "" {
						f(c.Name, Binding{Node: c})
					}
				}
			}
			for _, p := range sc.Imports {
				p.names(func(n string, x *Node) { f(n, Binding{Node: x}) })
			}
			for _, a := range sc.Also {
				walk(a)
			}
		}
	}
	walk(s)
}

// Package is an imported package or local file of helpers, resolved as a
// tree of its own.
type Package struct {
	Name    string // "std/doc", or the import string of a local import
	Major   int    // the major version asked for; 0 for a local import, or a git import at v0
	Version string // the version the package declares, if any
	Tree    *Tree
	// Exports are the nodes flagged <export>, mounted at an importing root.
	Exports []*Node
	// Alias is the folder name a folder import is also reachable by, as
	// `refunds.refund_window` after importing "../refunds/".
	Alias string
	// Sealed is set when the package node says `sealed = true`: nothing
	// outside the package may amend, scope or set into its nodes.
	Sealed bool
	// Node is the package node, as `shop: package(…)`, or nil.
	Node *Node
	// Git is the import of a package installed from git, or nil.
	Git *GitImport
}

// Display names a package in a message: its package node's name and
// version, as "shop 2.1.3", or its import string.
func (p *Package) Display() string {
	name := p.Name
	if p.Node != nil {
		name = p.Node.Name
	}
	if p.Version != "" {
		return name + " " + p.Version
	}
	return name
}

// Lookup returns a name the package makes visible: its root's named nodes,
// and the members of its `package` nodes.
func (p *Package) Lookup(name string) *Node {
	if p.Tree == nil {
		return nil
	}
	root := p.Tree.Root
	if c := root.Child(name); c != nil {
		return c
	}
	for _, c := range root.Children {
		if isPackageNode(c) {
			if m := c.Child(name); m != nil {
				return m
			}
		}
	}
	return nil
}

// each calls f for every node the package makes visible under a name: a
// root node, then members of its package nodes, as `http` is both std/http's
// package node and its annotation.
func (p *Package) each(name string, f func(*Node) bool) bool {
	if p.Tree == nil {
		return true
	}
	root := p.Tree.Root
	if p.Alias != "" && name == p.Alias && !f(root) {
		return false
	}
	if c := root.Child(name); c != nil && !f(c) {
		return false
	}
	for _, c := range root.Children {
		if isPackageNode(c) {
			if m := c.Child(name); m != nil && !f(m) {
				return false
			}
		}
	}
	return true
}

func (p *Package) names(f func(string, *Node)) {
	if p.Tree == nil {
		return
	}
	for _, c := range p.Tree.Root.Children {
		if c.Name != "" {
			f(c.Name, c)
		}
		if isPackageNode(c) {
			for _, m := range c.Children {
				if m.Name != "" {
					f(m.Name, m)
				}
			}
		}
	}
}

// isPackageNode reports whether a root node declares a package, as in
// `doc: package(version = "1.0") { … }`.
// IsPackageNode reports whether a node is declared as `name: package(…)`.
func IsPackageNode(n *Node) bool { return isPackageNode(n) }

func isPackageNode(n *Node) bool {
	if n.Shape != ShapeDeclared {
		return false
	}
	d := ast.Declare{N: n.Decl.N}
	if n.Decl.N == nil || n.Decl.N.Kind != syntax.Declare {
		return false
	}
	t, ok := d.Type().(ast.NamedType)
	return ok && PathText(t.Path().N) == "package"
}

// Tree is the resolved tree of one entry point.
type Tree struct {
	Entry *File
	Root  *Node
	// Files lists the tree's own files in the order they joined it.
	Files []*File
	// Imports lists every package imported anywhere in the tree.
	Imports []*Package
	// GitImports lists the git imports the tree's files write, installed
	// or not, for `heron run setup` to pin and install.
	GitImports []GitImport
	Diags      diag.List
	// includes maps each file to the files it includes directly.
	includes map[FileID][]FileID
	scopes   map[FileID]*Scope
	// Context is the outermost scope, if the tree was resolved against one.
	Context *Scope
	// Targets maps each string of an import or include directive to the
	// file it names (a package's entry file, a folder's index.heron), for
	// go-to-definition from the string.
	Targets map[*syntax.Node]FileID
	// Elements maps each kind element that declares a node to the node.
	Elements map[*syntax.Node]*Node
}

// ElementNode returns the node a kind element declares, or nil when the
// element is content of its text.
func (t *Tree) ElementNode(tag *syntax.Node) *Node {
	if t == nil || t.Elements == nil {
		return nil
	}
	return t.Elements[tag]
}

// FileScope returns the scope a file of the tree was read in.
func (t *Tree) FileScope(id FileID) *Scope { return t.scopes[id] }

// Includes reports whether file a includes file b, directly or not.
func (t *Tree) Includes(a, b FileID) bool {
	seen := map[FileID]bool{}
	var walk func(FileID) bool
	walk = func(x FileID) bool {
		if seen[x] {
			return false
		}
		seen[x] = true
		for _, y := range t.includes[x] {
			if y == b || walk(y) {
				return true
			}
		}
		return false
	}
	return walk(a)
}

// Walk calls f for every node of the tree in document order.
func (t *Tree) Walk(f func(*Node)) {
	var walk func(*Node)
	walk = func(n *Node) {
		f(n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(t.Root)
}

// Find returns the node at a dotted path from the root, or nil.
func (t *Tree) Find(path string) *Node {
	n := t.Root
	for _, seg := range strings.Split(path, ".") {
		if n = n.Child(seg); n == nil {
			return nil
		}
	}
	return n
}

// PackageDiags returns the resolution problems of every package the tree
// imports, directly or not, so a broken package shows where it's used.
func (t *Tree) PackageDiags() diag.List {
	var out diag.List
	seen := map[*Package]bool{}
	var visit func(*Tree)
	visit = func(t *Tree) {
		for _, p := range t.Imports {
			if seen[p] || p.Tree == nil {
				continue
			}
			seen[p] = true
			out = append(out, p.Tree.Diags...)
			visit(p.Tree)
		}
	}
	visit(t)
	return out
}
