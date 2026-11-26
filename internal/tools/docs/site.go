// Package docs renders a checked Heron tree as documentation: the static
// site `heron generate docs` writes, and the pages, nodes and fragments the
// spec browser and the editor's live preview show. Every kind renders
// through the renderer registered for it (registry.SetRenderer); a kind
// without one falls back to its fields and children.
package docs

import (
	"context"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/specmodel"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// Options configure a site.
type Options struct {
	// Title is the site's name in the top bar; it defaults to the root
	// project's display name, or the entry file's directory.
	Title string
	// Version is the version this build documents ("" for the working
	// tree); Versions lists the released builds the switch offers.
	Version  string
	Versions []string
	// Registry supplies renderers registered by packages and later units;
	// nil uses only the built-in ones.
	Registry *registry.Registry
	// Href rewrites a page's file name into a link, for a server that
	// serves pages under another scheme; nil links to the file names.
	Href func(file string) string
	// Assets is the prefix of the asset URLs, "_assets/" by default.
	Assets string
	// Data is the path of the data script, Assets + "data.js" unless set.
	Data string
	// Lens is the lens the site renders; nil renders everything, as the
	// explore lens does.
	Lens *lens.Lens
	// ServedOps are the ops of the API the serving server publishes, as
	// spec_server.preview, when `heron serve` serves the site; their pages
	// offer Try it against the page's own origin.
	ServedOps map[string]bool
}

// ServerAPI is the name of the API `heron serve` publishes.
const ServerAPI = "spec_server"

// Page is one page of the site.
type Page struct {
	Node  *resolve.Node // nil for the glossary and the site's own pages
	File  string        // file name, such as "orders.ordering.html"
	Title string
	// Layout is "home", "section", "api", "op", "type", "glossary",
	// "diagnostics", "conformance", "releases" or "spec"; a docs block's
	// layout(…) sets "api", "changelog" or
	// "landing" on sections.
	Layout string
	Area   *Area
	Parent *Page
	// Children are the pages listed below this one in the navigation.
	Children []*Page
	// Method is an op's HTTP method, shown in the navigation.
	Method string
	prev   *Page
	next   *Page
}

// Area is a top-level part of the site, a tab in the top bar with its own
// navigation tree.
type Area struct {
	Key    string // "docs", "reference", "glossary"
	Title  string
	Pages  []*Page // top-level navigation entries
	Groups []NavGroup
}

// NavGroup is a labelled group of navigation entries.
type NavGroup struct {
	Title string
	// Head is the page the group's label links to, if any: a TOPIC
	// group's section.
	Head  *Page
	Pages []*Page
}

// Site is a tree prepared for rendering.
type Site struct {
	Tree    *resolve.Tree
	Checker *types.Checker
	Opts    Options
	// Config is what the tree's `docs { … }` block sets.
	Config Config
	// Theme is the theme package the docs block names, if it loaded;
	// ThemeErr says why it didn't.
	Theme    *Theme
	ThemeErr error
	// RenderersErr says why a renderer module didn't load.
	RenderersErr error

	Pages []*Page
	Areas []*Area
	Home  *Page

	pageOf    map[*resolve.Node]*Page
	anchorOf  map[*resolve.Node]*Page // the page a node is shown on
	back      map[*resolve.Node]map[string][]*resolve.Node
	backlinks map[*resolve.Node][]*resolve.Node
	ownerOf   map[*syntax.Node]*resolve.Node
	cards     map[string]card
	terms     []*resolve.Node
	// diagnostics are the tree's diagnostic nodes, for their index.
	diagnostics []*resolve.Node
	types       []*resolve.Node
	errors      []*resolve.Node
	// grammarSources lists the grammars by their source file, once read.
	grammarSources map[string][]*resolve.Node
	apis           []*resolve.Node
	lifecycles     []*resolve.Node
	implementors   map[*resolve.Node][]*resolve.Node
	// project is the tree's implements, when it implements a spec.
	project *project.Project
	// specSite is the spec's own site, which links to spec nodes go to
	// when the project names it with `docs`.
	specSite *Site
	// specLenses are the spec's own lenses, when it builds more than one.
	specLenses *Lenses

	// modes memoizes how the lens renders each node; lenses is the set
	// of lens sites this one belongs to, if any.
	modes  map[*resolve.Node]lens.Mode
	places map[*resolve.Node]bool
	lenses *Lenses
	// fenceTitle titles the next code block prose renders, once.
	fenceTitle string
}

// New prepares a site for a checked tree.
func New(t *resolve.Tree, c *types.Checker, opts Options) *Site {
	if opts.Assets == "" {
		opts.Assets = "_assets/"
	}
	if opts.Data == "" {
		opts.Data = opts.Assets + "data.js"
	}
	s := &Site{
		Tree: t, Checker: c, Opts: opts,
		pageOf:       map[*resolve.Node]*Page{},
		anchorOf:     map[*resolve.Node]*Page{},
		backlinks:    map[*resolve.Node][]*resolve.Node{},
		ownerOf:      map[*syntax.Node]*resolve.Node{},
		cards:        map[string]card{},
		implementors: map[*resolve.Node][]*resolve.Node{},
		modes:        map[*resolve.Node]lens.Mode{},
		places:       map[*resolve.Node]bool{},
	}
	if opts.Registry != nil {
		// Renderer modules, such as a docs block's TS renderers, register
		// their kinds once their process runs.
		s.RenderersErr = opts.Registry.LoadRenderers(context.Background())
	}
	s.readConfig()
	if s.Config.Theme != "" {
		s.Theme, s.ThemeErr = LoadTheme(s.Config.Theme, s.Config.ThemeDir)
	}
	s.project = project.Find(t, c)
	s.classify()
	s.buildPages()
	s.buildBacklinks()
	return s
}

// kindPath is the declaration path of a node's kind, such as "spec.rule".
func (s *Site) kindPath(n *resolve.Node) string {
	if k := s.Checker.KindOf(n); k != nil {
		return k.Path()
	}
	return ""
}

// own reports whether a node belongs to the documented tree rather than a
// package mounted into it.
func own(n *resolve.Node) bool {
	for x := n; x != nil; x = x.Parent {
		if x.Shape == resolve.ShapeMounted {
			return false
		}
	}
	return n.Decl.File == nil || !n.Decl.File.ID.IsStd()
}

func (s *Site) classify() {
	s.Tree.Walk(func(n *resolve.Node) {
		if !own(n) || n.Name == "" && n.Shape != resolve.ShapeAnon || s.hidden(n) || !s.kept(n) {
			return
		}
		switch k := s.kindPath(n); {
		case k == "doc.term":
			s.terms = append(s.terms, n)
		case k == "spec.error":
			s.diagnostics = append(s.diagnostics, n)
		case k == "types.api":
			s.apis = append(s.apis, n)
		case k == "spec.states":
			s.lifecycles = append(s.lifecycles, n)
		case s.isTypeDecl(n):
			if s.isError(n) {
				s.errors = append(s.errors, n)
			} else {
				s.types = append(s.types, n)
			}
			for _, i := range s.Checker.Implements(n) {
				s.implementors[i] = append(s.implementors[i], n)
			}
		}
	})
}

// isTypeDecl reports whether a node declares a type, enum, union or
// interface of the documented tree.
func (s *Site) isTypeDecl(n *resolve.Node) bool {
	if n.Name == "" || n.Shape != resolve.ShapeDeclared {
		return false
	}
	switch s.kindPath(n) {
	case "types.type", "types.enum", "types.union", "types.interface":
		return true
	}
	return false
}

// isError reports whether a type is an error: it implements Error, directly
// or through an interface, or carries @status.
func (s *Site) isError(n *resolve.Node) bool {
	if annotation(s, n, "status") != nil {
		return true
	}
	seen := map[*resolve.Node]bool{}
	var walk func(t *resolve.Node) bool
	walk = func(t *resolve.Node) bool {
		if t == nil || seen[t] {
			return false
		}
		seen[t] = true
		for _, i := range s.Checker.Implements(t) {
			if i.Name == "Error" || walk(i) {
				return true
			}
		}
		return false
	}
	return walk(n)
}

// pageKinds are the kinds whose nodes get pages of their own at any depth:
// a component, a command, an epic, a use case and a screen are each read on
// their own.
var pageKinds = map[string]bool{
	"project.component": true,
	"spec.command":      true,
	"spec.usecase":      true,
	"spec.screen":       true,
}

// isPageNode reports whether a node gets a page of its own.
func (s *Site) isPageNode(n *resolve.Node, sectionDepth int) bool {
	if !own(n) || n.Name == "" || s.hidden(n) {
		return false
	}
	if s.gatheredDiagnostic(n) {
		return false
	}
	switch k := s.kindPath(n); {
	case s.authored(n):
		return true
	case k == "doc.section":
		return sectionDepth < s.depth() && s.hasContent(n)
	case k == "types.api", k == "types.op", pageKinds[k]:
		return true
	case k == "" && n.Shape == resolve.ShapeScoped:
		return s.hasDocs(n) && sectionDepth < s.depth() && s.hasContent(n)
	}
	return s.isTypeDecl(n)
}

// hasDocs reports whether a scope holds anything the docs show.
func (s *Site) hasDocs(n *resolve.Node) bool {
	if len(n.Prose) > 0 && n.Shape == resolve.ShapeScoped {
		// A folder whose index file opens with prose, as a section does.
		return true
	}
	for _, c := range n.Children {
		switch k := s.kindPath(c); {
		case strings.HasPrefix(k, "spec.") && k != "spec.project", strings.HasPrefix(k, "doc."), strings.HasPrefix(k, "grammar."):
			return true
		}
	}
	return false
}

// depth is how many levels of sections get pages: the lens's depth when
// it sets one, else the docs block's.
func (s *Site) depth() int {
	if l := s.Opts.Lens; l != nil && l.Depth > 0 {
		return l.Depth
	}
	return s.Config.Depth
}

func (s *Site) fileFor(n *resolve.Node) string {
	return n.Path() + ".html"
}

func (s *Site) buildPages() {
	docs := &Area{Key: "docs", Title: label("docs.area.docs")}
	ref := &Area{Key: "reference", Title: label("docs.area.reference")}
	s.Home = &Page{Node: s.Tree.Root, File: "index.html", Title: s.title(), Layout: "home", Area: docs}
	s.add(s.Home)

	// Sections and documented scopes, in tree order, two levels deep.
	var refSections []*Page
	var walk func(n *resolve.Node, parent *Page, depth int)
	walk = func(n *resolve.Node, parent *Page, depth int) {
		for _, c := range n.Children {
			k := s.kindPath(c)
			isSection := k == "doc.section" || pageKinds[k] || s.authored(c) || k == "" && c.Shape == resolve.ShapeScoped && s.hasDocs(c)
			if !isSection || !s.isPageNode(c, depth) {
				if c.Shape == resolve.ShapeScoped && k == "" && depth == 0 || depth == 0 && resolve.IsPackageNode(c) {
					// A folder scope or a package node, such as a spec
					// package's, holds sections of its own.
					walk(c, parent, depth)
				}
				continue
			}
			layout := s.layoutOf(c, "section")
			area := docs
			if parent != nil {
				area = parent.Area
			}
			p := &Page{Node: c, File: s.fileFor(c), Title: displayName(s, c), Layout: layout, Area: area, Parent: parent}
			s.add(p)
			switch {
			case layout == "api" && parent == nil:
				// A section in the API reference's layout is a group of
				// the reference, as an api is.
				p.Area = ref
				refSections = append(refSections, p)
			case parent == nil:
				docs.Pages = append(docs.Pages, p)
			default:
				parent.Children = append(parent.Children, p)
			}
			if layout == "changelog" {
				// A changelog's entries render on its own page.
				continue
			}
			walk(c, p, depth+1)
		}
	}
	walk(s.Tree.Root, nil, 0)

	// The API reference: the sections in its layout, each api with its
	// ops, then types, lifecycles and errors.
	for _, p := range refSections {
		ref.Groups = append(ref.Groups, NavGroup{Title: p.Title, Pages: append([]*Page{p}, p.Children...)})
	}
	for _, a := range s.apis {
		p := &Page{Node: a, File: s.fileFor(a), Title: displayName(s, a), Layout: "api", Area: ref}
		s.add(p)
		g := NavGroup{Title: p.Title, Pages: []*Page{p}}
		for _, op := range a.Children {
			if s.kindPath(op) != "types.op" {
				continue
			}
			m, _ := httpRoute(s, op)
			if m == "" && wsRoute(op) != "" {
				m = "WS"
			}
			q := &Page{Node: op, File: s.fileFor(op), Title: displayName(s, op), Layout: "op", Area: ref, Parent: p, Method: m}
			s.add(q)
			p.Children = append(p.Children, q)
			g.Pages = append(g.Pages, q)
		}
		g.Pages = g.Pages[1:]
		g.Title = p.Title
		ref.Groups = append(ref.Groups, NavGroup{Title: g.Title, Pages: append([]*Page{p}, g.Pages...)})
	}
	typeGroup := func(title string, nodes []*resolve.Node) {
		if len(nodes) == 0 {
			return
		}
		g := NavGroup{Title: title}
		sorted := append([]*resolve.Node(nil), nodes...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
		for _, t := range sorted {
			p := &Page{Node: t, File: s.fileFor(t), Title: t.Name, Layout: "type", Area: ref}
			s.add(p)
			g.Pages = append(g.Pages, p)
		}
		ref.Groups = append(ref.Groups, g)
	}
	// Types from more than one scope, such as an API's and a protocol's,
	// group by the scope that declares them.
	var scopes []*resolve.Node
	byScope := map[*resolve.Node][]*resolve.Node{}
	for _, t := range s.types {
		if _, ok := byScope[t.Parent]; !ok {
			scopes = append(scopes, t.Parent)
		}
		byScope[t.Parent] = append(byScope[t.Parent], t)
	}
	if len(scopes) > 1 {
		for _, sc := range scopes {
			title := label("docs.nav.types")
			if sc != nil && sc != s.Tree.Root && sc.Name != "" {
				title = label("docs.nav.types_in", "scope", displayName(s, sc))
			}
			typeGroup(title, byScope[sc])
		}
	} else {
		typeGroup(label("docs.nav.types"), s.types)
	}
	typeGroup(label("docs.nav.errors"), s.errors)

	s.Areas = append(s.Areas, docs)
	if len(ref.Groups) > 0 {
		s.Areas = append(s.Areas, ref)
	}
	if len(s.terms) > 0 {
		gl := &Area{Key: "glossary", Title: label("docs.area.glossary")}
		p := &Page{File: "_glossary.html", Title: label("docs.page.glossary"), Layout: "glossary", Area: gl}
		s.add(p)
		gl.Pages = []*Page{p}
		s.Areas = append(s.Areas, gl)
		for _, t := range s.terms {
			if s.anchorOf[t] == nil {
				s.anchorOf[t] = p
			}
		}
	}
	if len(s.diagnostics) > 0 {
		p := &Page{File: "_diagnostics.html", Title: label("docs.page.errors"), Layout: "diagnostics", Area: docs}
		s.Pages = append(s.Pages, p)
		docs.Pages = append(docs.Pages, p)
		if s.errorsGathered() {
			// Each diagnostic's anchor is its entry on this page.
			for _, d := range s.diagnostics {
				s.anchorOf[d] = p
			}
		}
	}

	if s.project != nil {
		// A project's own pages: how it conforms to the spec it implements.
		p := &Page{File: "_conformance.html", Title: label("docs.page.conformance"), Layout: "conformance", Area: docs}
		s.Pages = append(s.Pages, p)
		docs.Pages = append(docs.Pages, p)
		s.specPages(docs)
	}

	// Every other node is shown on the page of its nearest ancestor that
	// has one.
	s.Tree.Walk(func(n *resolve.Node) {
		if s.anchorOf[n] != nil || !own(n) || s.hidden(n) {
			return
		}
		for a := n.Parent; a != nil; a = a.Parent {
			if p := s.pageOf[a]; p != nil {
				s.anchorOf[n] = p
				return
			}
		}
		s.anchorOf[n] = s.Home
	})
	// Terms inside sections stay on their section's page; the glossary
	// repeats them with links back.
	for _, t := range s.terms {
		for a := t.Parent; a != nil; a = a.Parent {
			if p := s.pageOf[a]; p != nil && p != s.Home {
				s.anchorOf[t] = p
				break
			}
		}
	}

	s.applyNav(docs)

	// Previous and next follow the navigation order of each area.
	for _, a := range s.Areas {
		var order []*Page
		seen := map[*Page]bool{}
		var flat func(ps []*Page)
		flat = func(ps []*Page) {
			for _, p := range ps {
				if seen[p] {
					// A group can list a page and its children both, as
					// an api's group lists its ops.
					continue
				}
				seen[p] = true
				order = append(order, p)
				flat(p.Children)
			}
		}
		flat(a.Pages)
		for _, g := range a.Groups {
			if g.Head != nil {
				flat([]*Page{g.Head})
			}
			flat(g.Pages)
		}
		for i, p := range order {
			if i > 0 {
				p.prev = order[i-1]
			}
			if i+1 < len(order) {
				p.next = order[i+1]
			}
		}
	}
}

func (s *Site) add(p *Page) {
	s.Pages = append(s.Pages, p)
	if p.Node != nil {
		s.pageOf[p.Node] = p
		s.anchorOf[p.Node] = p
	}
}

// title is the site's name.
func (s *Site) title() string {
	if s.Opts.Title != "" {
		return s.Opts.Title
	}
	for _, c := range s.Tree.Root.Children {
		if own(c) && s.kindPath(c) == "spec.project" {
			return displayName(s, c)
		}
	}
	if s.project != nil {
		return displayName(s, s.project.Node)
	}
	if pk := s.homeNode(); pk != s.Tree.Root {
		return displayName(s, pk)
	}
	for _, c := range s.Tree.Root.Children {
		if own(c) && s.kindPath(c) == "doc.section" {
			return displayName(s, c)
		}
	}
	if s.Tree.Entry != nil {
		id := string(s.Tree.Entry.ID)
		parts := strings.Split(strings.TrimSuffix(id, "/index.heron"), "/")
		return sentenceCase(parts[len(parts)-1])
	}
	return "Heron"
}

// PageOf returns the page a node is shown on.
func (s *Site) PageOf(n *resolve.Node) *Page { return s.anchorOf[n] }

// OwnPage returns the page of a node's own, or nil.
func (s *Site) OwnPage(n *resolve.Node) *Page { return s.pageOf[n] }

// PageByFile returns the page with a file name, or nil.
func (s *Site) PageByFile(file string) *Page {
	for _, p := range s.Pages {
		if p.File == file {
			return p
		}
	}
	return nil
}

// href is the link to a page file.
func (s *Site) href(file string) string {
	if s.Opts.Href != nil {
		return s.Opts.Href(file)
	}
	return file
}

// Anchor is the id of a node on its page.
func Anchor(n *resolve.Node) string { return n.Path() }

// URL returns the link to a node: its page, and its anchor on that page.
func (s *Site) URL(n *resolve.Node) string {
	if u := s.specURL(n); u != "" {
		return u
	}
	if !s.kept(n) || s.anchorOf[n] == nil {
		if u := s.siblingURL(n); u != "" {
			return u
		}
	}
	return s.localURL(n)
}

// localURL is the link to a node within the site's own lens, or "".
func (s *Site) localURL(n *resolve.Node) string {
	if p := s.pageOf[n]; p != nil {
		return s.href(p.File)
	}
	p := s.anchorOf[n]
	if p == nil {
		return ""
	}
	return s.href(p.File) + "#" + strings.ReplaceAll(Anchor(n), "#", "%23")
}

// buildBacklinks records, for every node a path refers to, the nodes the
// references are written in.
func (s *Site) buildBacklinks() {
	s.Tree.Walk(func(n *resolve.Node) {
		if !own(n) {
			return
		}
		for _, site := range append([]resolve.Site{n.Decl}, n.Sites...) {
			if site.N != nil && s.ownerOf[site.N] == nil {
				s.ownerOf[site.N] = n
			}
		}
	})
	seen := map[[2]*resolve.Node]bool{}
	for p, target := range s.Checker.Refs {
		owner := s.owner(p)
		// A value set inside a node, as an example's request, counts as a
		// use by the node that has an anchor.
		for owner != nil && owner.Parent != nil && (owner.Shape == resolve.ShapeSet || owner.Shape == resolve.ShapeValue && !s.isTypeDecl(owner.Parent)) {
			owner = owner.Parent
		}
		if owner == nil || target == nil || owner == target || !own(target) {
			continue
		}
		// A reference inside the target's own declaration isn't a use.
		inside := false
		for a := owner; a != nil; a = a.Parent {
			if a == target {
				inside = true
			}
		}
		if inside {
			continue
		}
		k := [2]*resolve.Node{target, owner}
		if seen[k] {
			continue
		}
		seen[k] = true
		s.backlinks[target] = append(s.backlinks[target], owner)
	}
	for _, l := range s.backlinks {
		sort.Slice(l, func(i, j int) bool { return l[i].Path() < l[j].Path() })
	}
}

// owner is the innermost node whose declaration holds a syntax node.
func (s *Site) owner(p *syntax.Node) *resolve.Node {
	for x := p; x != nil; x = x.Parent {
		if n := s.ownerOf[x]; n != nil {
			return n
		}
	}
	return nil
}

// Backlinks returns the nodes that refer to n.
func (s *Site) Backlinks(n *resolve.Node) []*resolve.Node { return s.backlinks[n] }

// homeNode is the node the home page shows: the root, or the package
// node a spec package's tree is written in.
func (s *Site) homeNode() *resolve.Node {
	r := s.Tree.Root
	if len(r.Prose) > 0 {
		return r
	}
	var pkg *resolve.Node
	for _, c := range r.Children {
		if !own(c) || c.Shape == resolve.ShapeSet || c.Shape == resolve.ShapeValue || c == s.Config.Node {
			continue
		}
		if !resolve.IsPackageNode(c) || pkg != nil {
			return r
		}
		pkg = c
	}
	if pkg == nil {
		return r
	}
	return pkg
}

// isIntent reports whether a node is in the intent layer, as a goal is.
func (s *Site) isIntent(n *resolve.Node) bool {
	return n != nil && specmodel.LayerOf(s.Checker, n) == specmodel.Intent
}
