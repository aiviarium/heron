package docs

import (
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/icon"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// The schema reference: the kinds, annotations, flags and categories of
// every package a tree sees that declares them, written from their
// declarations. Its landing page is _schema.html, each kind and annotation
// has a page of its own, _schema.<path>.html, and flags and categories
// have their anchors on the landing page.

// schemaPkg is one package of the schema reference.
type schemaPkg struct {
	name string        // the import name, as "std/spec", or the tree's own package's
	node *resolve.Node // its package node, or nil for the tree's root
	id   string        // its anchor on the landing page

	kinds, annotations, flags, categories []*resolve.Node
}

// schemaRef is the schema reference of a site.
type schemaRef struct {
	pkgs    []*schemaPkg
	pkgOf   map[*resolve.Node]*schemaPkg
	pages   map[*resolve.Node]*Page
	landing *Page
	// direct maps a kind to the kinds that accept it by its name; holders
	// maps a category to the kinds that accept it; members a category to
	// the kinds that join it.
	direct  map[*resolve.Node][]*resolve.Node
	holders map[*resolve.Node][]*resolve.Node
	members map[*resolve.Node][]*resolve.Node
	// links maps a declaration to the named nodes of the tree whose text
	// links it, once worked out.
	links map[*resolve.Node][]*resolve.Node
}

// schemaPagePrefix starts the file of every page of the schema reference.
const schemaPagePrefix = "_schema"

// importsSchema reports whether a file of the tree imports std/schema, the
// package kinds are declared with.
func (s *Site) importsSchema() bool {
	for _, f := range s.Tree.Files {
		for sc := s.Tree.FileScope(f.ID); sc != nil; sc = sc.Parent {
			for _, p := range sc.Imports {
				if p.Name == "std/schema" {
					return true
				}
			}
		}
	}
	return false
}

// schemaPackages are the packages the tree sees that declare a kind, an
// annotation, a flag or a category: its own first, then the core, then its
// imports, each once.
func (s *Site) schemaPackages() []*schemaPkg {
	c := s.Checker
	var out []*schemaPkg
	seen := map[*resolve.Node]bool{}
	var collect func(p *schemaPkg, n *resolve.Node, depth int)
	collect = func(p *schemaPkg, n *resolve.Node, depth int) {
		for _, ch := range n.Children {
			if ch.Name == "" || seen[ch] || depth > 6 {
				continue
			}
			switch {
			case c.IsKind(ch):
				seen[ch] = true
				p.kinds = append(p.kinds, ch)
			case c.IsAnnotation(ch):
				seen[ch] = true
				p.annotations = append(p.annotations, ch)
			case c.IsFlag(ch):
				seen[ch] = true
				p.flags = append(p.flags, ch)
			case c.IsCategory(ch):
				seen[ch] = true
				p.categories = append(p.categories, ch)
			case ch.Shape == resolve.ShapeMounted, s.isTypeDecl(ch) || c.IsType(ch):
			default:
				if p.node != nil || own(ch) {
					collect(p, ch, depth+1)
				}
			}
		}
	}
	add := func(p *schemaPkg) {
		if len(p.kinds)+len(p.annotations)+len(p.flags)+len(p.categories) > 0 {
			out = append(out, p)
		}
	}
	// The tree's own: each package node it declares, then its root.
	var ownPkgs []*resolve.Node
	s.Tree.Walk(func(n *resolve.Node) {
		if own(n) && n.Name != "" && c.IsPackage(n) {
			ownPkgs = append(ownPkgs, n)
		}
	})
	for _, n := range ownPkgs {
		p := &schemaPkg{name: n.Name, node: n, id: Anchor(n)}
		collect(p, n, 0)
		add(p)
	}
	root := &schemaPkg{name: s.title(), id: "_tree"}
	collect(root, s.Tree.Root, 0)
	add(root)
	// The core, then every import, in the order they joined the tree.
	var pkgs []*resolve.Package
	if s.Tree.Entry != nil {
		for sc := s.Tree.FileScope(s.Tree.Entry.ID); sc != nil; sc = sc.Parent {
			pkgs = append(pkgs, sc.Imports...)
		}
	}
	// The core first: std/core, then std/spec.
	sort.SliceStable(pkgs, func(i, j int) bool { return coreRank(pkgs[i].Name) < coreRank(pkgs[j].Name) })
	pkgs = append(pkgs, s.Tree.Imports...)
	named := map[string]bool{}
	for _, pk := range pkgs {
		if pk == nil || pk.Node == nil || named[pk.Name] || seen[pk.Node] {
			continue
		}
		named[pk.Name] = true
		p := &schemaPkg{name: pk.Name, node: pk.Node, id: Anchor(pk.Node)}
		collect(p, pk.Node, 0)
		add(p)
	}
	return out
}

func coreRank(name string) int {
	switch name {
	case resolve.PreludeName:
		return 0
	case resolve.SpecName:
		return 1
	}
	return 2
}

// buildSchema adds the schema reference's pages to the Reference area,
// when the tree imports std/schema.
func (s *Site) buildSchema(ref *Area) []NavGroup {
	if !s.importsSchema() {
		return nil
	}
	sr := &schemaRef{pkgOf: map[*resolve.Node]*schemaPkg{}, pages: map[*resolve.Node]*Page{},
		direct: map[*resolve.Node][]*resolve.Node{}, holders: map[*resolve.Node][]*resolve.Node{}, members: map[*resolve.Node][]*resolve.Node{}}
	sr.pkgs = s.schemaPackages()
	if len(sr.pkgs) == 0 {
		return nil
	}
	s.schema = sr
	sr.landing = &Page{File: schemaPagePrefix + ".html", Title: label("docs.page.schema"), Layout: "schema", Area: ref}
	s.Pages = append(s.Pages, sr.landing)
	groups := []NavGroup{{Part: label("docs.nav.part_schema"), Pages: []*Page{sr.landing}}}
	for _, p := range sr.pkgs {
		g := NavGroup{Part: label("docs.nav.part_schema"), Title: p.name, Fold: true}
		for _, k := range p.kinds {
			sr.pkgOf[k] = p
			pg := &Page{File: schemaFile(k), Title: k.Name, Layout: "kind", Area: ref, Parent: sr.landing, icon: s.kindIcon(k, "icon")}
			sr.pages[k] = pg
			s.Pages = append(s.Pages, pg)
			g.Pages = append(g.Pages, pg)
		}
		for _, a := range p.annotations {
			sr.pkgOf[a] = p
			pg := &Page{File: schemaFile(a), Title: "@" + a.Name, Layout: "annotation", Area: ref, Parent: sr.landing}
			sr.pages[a] = pg
			s.Pages = append(s.Pages, pg)
			g.Pages = append(g.Pages, pg)
		}
		for _, x := range append(append([]*resolve.Node(nil), p.flags...), p.categories...) {
			sr.pkgOf[x] = p
		}
		if p.node != nil {
			sr.pkgOf[p.node] = p
		}
		if len(g.Pages) > 0 {
			groups = append(groups, g)
		}
	}
	// What accepts what.
	for _, p := range sr.pkgs {
		for _, k := range p.kinds {
			for _, cat := range s.Checker.Categories(k) {
				sr.members[cat] = appendOnce(sr.members[cat], k)
			}
		}
	}
	for _, p := range sr.pkgs {
		for _, k := range p.kinds {
			for _, a := range s.Checker.AcceptEntries(k) {
				switch {
				case a.Kind != nil:
					sr.direct[a.Kind] = appendOnce(sr.direct[a.Kind], k)
				case a.Category != nil:
					sr.holders[a.Category] = appendOnce(sr.holders[a.Category], k)
				}
			}
		}
	}
	return groups
}

func appendOnce(l []*resolve.Node, n *resolve.Node) []*resolve.Node {
	for _, x := range l {
		if x == n {
			return l
		}
	}
	return append(l, n)
}

// schemaFile is the page file of a kind or an annotation.
func schemaFile(n *resolve.Node) string { return schemaPagePrefix + "." + n.Path() + ".html" }

// schemaURL is the link to a declaration of the schema reference: its page,
// or its anchor on the landing page, or "".
func (s *Site) schemaURL(n *resolve.Node) string {
	sr := s.schema
	if sr == nil || n == nil {
		return ""
	}
	if p := sr.pages[n]; p != nil {
		return s.href(p.File)
	}
	if _, ok := sr.pkgOf[n]; ok {
		return s.href(sr.landing.File) + "#" + Anchor(n)
	}
	return ""
}

// kindIcon is a kind's own icon, or the icon of the kind it extends.
func (s *Site) kindIcon(k *resolve.Node, class string) string {
	for x, i := k, 0; x != nil && i < 8; x, i = s.Checker.Extends(x), i+1 {
		if name, _, ok := icon.Mark(s.Checker, x); ok {
			return s.namedIcon(name, class)
		}
	}
	return ""
}

// schemaDoc is a declaration's docs, rendered.
func (s *Site) schemaDoc(n *resolve.Node) string {
	var out []string
	for _, d := range n.Docs {
		if h := s.renderText([]resolve.Site{d}); h != "" {
			out = append(out, h)
		}
	}
	for _, p := range n.Prose {
		if h := s.renderText([]resolve.Site{p}); h != "" {
			out = append(out, h)
		}
	}
	return strings.Join(out, "\n")
}

// schemaSentence is the first sentence of a declaration's docs, as inline
// HTML.
func (s *Site) schemaSentence(n *resolve.Node) string {
	h := s.schemaDoc(n)
	if !strings.Contains(h, "<p>") {
		h = "<p>" + h + "</p>"
	}
	return firstSentence(inlineParagraph(h))
}

// schemaLink links a declaration of the schema reference by its name, in
// code, as `@touch` or `<external>` for an annotation or a flag.
func (s *Site) schemaLink(n *resolve.Node) string {
	name := n.Name
	switch {
	case s.Checker.IsAnnotation(n):
		name = "@" + name
	case s.Checker.IsFlag(n):
		name = "<" + name + ">"
	}
	inner := `<code>` + esc(name) + `</code>`
	if u := s.schemaURL(n); u != "" {
		return `<a class="ref" href="` + attr(u) + `">` + inner + `</a>`
	}
	return inner
}

// layerOf is a kind's layer, its own or the one of the kind it extends,
// as written after the enum's name, or "".
func (s *Site) layerOf(k *resolve.Node) string {
	for x, i := k, 0; x != nil && i < 8; x, i = s.Checker.Extends(x), i+1 {
		if e := types.DeclNamedArg(x, "layer"); e != nil {
			t := strings.TrimSpace(e.Syntax().Text())
			if j := strings.LastIndexByte(t, '.'); j >= 0 {
				t = t[j+1:]
			}
			return t
		}
	}
	return ""
}

// flagGroup is the group a flag's declaration names, or "".
func flagGroup(f *resolve.Node) string {
	if e := types.DeclNamedArg(f, "group"); e != nil {
		if v, ok := resolve.StringValue(e.Syntax()); ok {
			return v
		}
	}
	return ""
}

// flagDefault reports whether a flag's declaration names it its group's
// default, as `default = true`.
func flagDefault(f *resolve.Node) bool {
	if e := types.DeclNamedArg(f, "default"); e != nil {
		if l, ok := e.(ast.Literal); ok && l.KwTrue() != nil {
			return true
		}
	}
	return false
}

// holdersOf are the kinds that accept a kind by its name, and the
// categories it joins that some kind accepts.
func (s *Site) holdersOf(k *resolve.Node) (kinds, cats []*resolve.Node) {
	sr := s.schema
	kinds = sr.direct[k]
	for _, c := range s.Checker.Categories(k) {
		if len(sr.holders[c]) > 0 {
			cats = append(cats, c)
		}
	}
	return
}

// holdGroup is what a kind holds through one category, or through no
// category ("other").
type holdGroup struct {
	cat    *resolve.Node
	catMax int // the bound the category's own accept sets
	kinds  []*resolve.Node
	max    map[*resolve.Node]int
}

// holdsOf groups what a kind accepts: each category it accepts with its
// kinds, then the kinds it accepts by name outside them; and the modes of
// text it reads.
func (s *Site) holdsOf(k *resolve.Node) (groups []holdGroup, modes []string) {
	sr := s.schema
	entries := s.Checker.AcceptEntries(k)
	inGroup := map[*resolve.Node]int{}
	for _, a := range entries {
		if a.Category != nil {
			g := holdGroup{cat: a.Category, catMax: a.Max, max: map[*resolve.Node]int{}}
			for _, m := range sr.members[a.Category] {
				g.kinds = append(g.kinds, m)
				if _, ok := inGroup[m]; !ok {
					inGroup[m] = len(groups)
				}
			}
			groups = append(groups, g)
		}
	}
	other := holdGroup{max: map[*resolve.Node]int{}}
	for _, a := range entries {
		switch {
		case a.Kind != nil:
			if i, ok := inGroup[a.Kind]; ok {
				if a.Max > 0 {
					groups[i].max[a.Kind] = a.Max
				}
				continue
			}
			other.kinds = appendOnce(other.kinds, a.Kind)
			if a.Max > 0 {
				other.max[a.Kind] = a.Max
			}
		case a.Text == "Verbatim":
			modes = append(modes, label("docs.schema.verbatim"))
		case a.Mode == "prose":
			modes = append(modes, label("docs.schema.prose"))
		case a.Mode == "code":
			modes = append(modes, label("docs.schema.code"))
		}
	}
	if len(other.kinds) > 0 {
		groups = append(groups, other)
	}
	return
}

// annotationsOn are the annotations that attach to a kind: those whose
// `on` names it or a category it joins, then those that attach anywhere.
func (s *Site) annotationsOn(k *resolve.Node) (named, anywhere []*resolve.Node) {
	for _, p := range s.schema.pkgs {
		for _, a := range p.annotations {
			on := s.Checker.On(a)
			switch {
			case len(on) == 0:
				anywhere = append(anywhere, a)
			case s.Checker.Fits(k, on):
				named = append(named, a)
			}
		}
	}
	return
}

// flagsOn are the flags whose `on` names a kind or a category it joins.
func (s *Site) flagsOn(k *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, p := range s.schema.pkgs {
		for _, f := range p.flags {
			if on := s.Checker.On(f); len(on) > 0 && s.Checker.Fits(k, on) {
				out = append(out, f)
			}
		}
	}
	return out
}

// schemaLinks are the named nodes of the tree whose text, or whose
// references, name a declaration of the schema reference.
func (s *Site) schemaLinks(n *resolve.Node) []*resolve.Node {
	sr := s.schema
	if sr.links == nil {
		sr.links = map[*resolve.Node][]*resolve.Node{}
		for p, target := range s.Checker.Refs {
			if _, ok := sr.pkgOf[target]; !ok || !inLink(p) {
				continue
			}
			owner := s.owner(p)
			for owner != nil && owner.Name == "" && owner.Parent != nil {
				owner = owner.Parent
			}
			if owner == nil || !own(owner) || owner == target {
				continue
			}
			sr.links[target] = appendOnce(sr.links[target], owner)
		}
		for _, l := range sr.links {
			sort.Slice(l, func(i, j int) bool { return l[i].Path() < l[j].Path() })
		}
	}
	return sr.links[n]
}

// checkedBy are the rules and constraints of the tree that link a
// declaration in their text, each with the ids of the errors whose prose
// links it.
func (s *Site) checkedBy(n *resolve.Node) (rules []*resolve.Node, codes map[*resolve.Node][]string) {
	codes = map[*resolve.Node][]string{}
	for _, o := range s.schemaLinks(n) {
		switch s.kindPath(o) {
		case "spec.rule", "spec.constraint":
		default:
			continue
		}
		rules = append(rules, o)
		for _, b := range s.backlinks[o] {
			for b != nil && b.Name == "" && b.Parent != nil {
				b = b.Parent
			}
			if b != nil && s.kindPath(b) == "spec.error" {
				if id, ok := argString(kindArgs(b), 0, "id"); ok && !contains(codes[o], id) {
					codes[o] = append(codes[o], id)
				}
			}
		}
	}
	return
}

// instances counts the nodes of the tree that are of a kind.
func (s *Site) instances(k *resolve.Node) int {
	n := 0
	s.Tree.Walk(func(x *resolve.Node) {
		if own(x) && s.Checker.KindOf(x) == k {
			n++
		}
	})
	return n
}

// schemaSearch are the search entries of the schema reference: every
// kind, annotation, flag and category of a package the tree doesn't
// declare itself, whose own nodes the index has already.
func (s *Site) schemaSearch() []entry {
	sr := s.schema
	if sr == nil {
		return nil
	}
	var out []entry
	add := func(n *resolve.Node, title, kind string) {
		if own(n) {
			return
		}
		text := strings.Join(strings.Fields(stripTags(s.schemaSentence(n))), " ")
		if len(text) > 240 {
			text = text[:240]
		}
		e := entry{Title: title, Kind: kind, Path: n.Path(), URL: s.schemaURL(n), Text: htmlUnescape(text)}
		if p := sr.pkgOf[n]; p != nil {
			e.Page = p.name
		}
		if name, _, ok := icon.Mark(s.Checker, n); ok && s.iconSet().Get(name) != nil {
			e.Icon = name
		}
		out = append(out, e)
	}
	for _, p := range sr.pkgs {
		for _, k := range p.kinds {
			add(k, k.Name, "kind")
		}
		for _, a := range p.annotations {
			add(a, "@"+a.Name, "annotation")
		}
		for _, f := range p.flags {
			add(f, "<"+f.Name+">", "flag")
		}
		for _, c := range p.categories {
			add(c, c.Name, "category")
		}
	}
	return out
}

// inLink reports whether a path is written in a link of prose, as
// `[rule](#spec.rule)`, `[spec.rule]` or `<ref to="spec.rule"/>`, rather
// than as a node's kind or reference.
func inLink(p *syntax.Node) bool {
	for x := p; x != nil; x = x.Parent {
		switch x.Kind {
		case syntax.Link, syntax.ShortLink, syntax.TagAttr:
			return true
		case syntax.Declare, syntax.Value, syntax.Invoke, syntax.Enter:
			return false
		}
	}
	return false
}
