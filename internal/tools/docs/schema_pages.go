package docs

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// The pages of the schema reference: the landing page, a kind's page and
// an annotation's page.

// treeAnchor is the anchor of the landing page's containment tree.
const treeAnchor = "schema-tree"

// declFile is the file a declaration is written in, as its readers know
// it: `std/spec/package.heron` for a standard package's.
func (s *Site) declFile(n *resolve.Node) string {
	if n.Decl.File != nil && n.Decl.File.ID.IsStd() {
		return "std/" + strings.TrimPrefix(string(n.Decl.File.ID), "std:")
	}
	return s.relFile(n.Decl.File)
}

// --- the landing page ---

func (s *Site) schemaLanding(c *Ctx) error {
	sr := s.schema
	if sr == nil {
		return nil
	}
	c.Heading(2, treeAnchor, label("docs.schema.tree"), "")
	c.containmentTree()
	for _, p := range sr.pkgs {
		c.Heading(2, p.id, p.name, "")
		if p.node != nil {
			if h := s.schemaSentence(p.node); h != "" {
				c.W(`<p class="lead-line">`, h, `</p>`)
			}
		}
		c.schemaKindsList(p)
		c.schemaAnnotationsList(p)
		c.schemaFlagsList(p)
		c.schemaCategoriesList(p)
	}
	return nil
}

// schemaTable writes rows as a table where there are three or more, and
// as one line each otherwise; each row is its cells, the first its name,
// and ids the anchor of each row, if any.
func (c *Ctx) schemaTable(cols []string, rows [][]string, ids []string) {
	if len(rows) == 0 {
		return
	}
	if len(rows) < 3 {
		c.W(`<ul class="lines">`)
		for i, r := range rows {
			id := ""
			if ids != nil && ids[i] != "" {
				id = ` id="` + attr(ids[i]) + `"`
			}
			c.W(`<li`, id, `><p class="line-head">`, r[0])
			for j, cell := range r[1:] {
				if cell == "" || j+1 == len(r)-1 {
					continue
				}
				c.W(`<span class="sep"> · </span>`, cell)
			}
			c.W(`</p>`)
			if last := r[len(r)-1]; len(r) > 1 && last != "" {
				c.W(`<div class="line-doc">`, last, `</div>`)
			}
			c.W(`</li>`)
		}
		c.W(`</ul>`)
		return
	}
	c.W(`<div class="table-wrap"><table class="fields schema-table"><thead><tr>`)
	for _, h := range cols {
		c.W(`<th>`, esc(h), `</th>`)
	}
	c.W(`</tr></thead><tbody>`)
	for i, r := range rows {
		id := ""
		if ids != nil && ids[i] != "" {
			id = ` id="` + attr(ids[i]) + `"`
		}
		c.W(`<tr`, id, `>`)
		for _, cell := range r {
			c.W(`<td>`, cell, `</td>`)
		}
		c.W(`</tr>`)
	}
	c.W(`</tbody></table></div>`)
}

// links joins declarations as their links.
func (s *Site) schemaLinks2(ns []*resolve.Node) string {
	var out []string
	for _, n := range ns {
		out = append(out, s.schemaLink(n))
	}
	return strings.Join(out, ", ")
}

func (c *Ctx) schemaKindsList(p *schemaPkg) {
	s := c.Site
	if len(p.kinds) == 0 {
		return
	}
	c.Heading(3, p.id+"--kinds", label("docs.schema.kinds"), "")
	var rows [][]string
	for _, k := range p.kinds {
		name := `<a class="ref" href="` + attr(s.schemaURL(k)) + `">` + s.kindIcon(k, "icon") + `<code>` + esc(k.Name) + `</code></a>`
		rows = append(rows, []string{name, s.schemaLinks2(s.Checker.Categories(k)), s.schemaSentence(k)})
	}
	c.schemaTable([]string{label("docs.schema.col_kind"), label("docs.schema.belongs_to"), label("docs.col.description")}, rows, nil)
}

// annotationSignature is an annotation as it's written: its name after
// `@` and its parameters, as `@exit(code: Int)`.
func (s *Site) annotationSignature(a *resolve.Node) string {
	return s.writtenName(a) + strings.TrimPrefix(signatureOf(a), a.Name)
}

// attachesTo is what an annotation or a flag attaches to: the kinds and
// categories its `on` names, linked, or any node.
func (s *Site) attachesTo(n *resolve.Node) string {
	on := s.Checker.On(n)
	if len(on) == 0 {
		return esc(label("docs.schema.any_node"))
	}
	return s.schemaLinks2(on)
}

func (c *Ctx) schemaAnnotationsList(p *schemaPkg) {
	s := c.Site
	if len(p.annotations) == 0 {
		return
	}
	c.Heading(3, p.id+"--annotations", label("docs.schema.annotations"), "")
	var rows [][]string
	for _, a := range p.annotations {
		name := `<a class="ref" href="` + attr(s.schemaURL(a)) + `"><code>` + esc(s.annotationSignature(a)) + `</code></a>`
		rows = append(rows, []string{name, s.attachesTo(a), s.schemaSentence(a)})
	}
	c.schemaTable([]string{label("docs.schema.col_annotation"), label("docs.schema.attaches_to"), label("docs.col.description")}, rows, nil)
}

// flagsByGroup orders flags by their group, in the order each group is
// first declared, the flags of no group last.
func flagsByGroup(fs []*resolve.Node) []*resolve.Node {
	var order []string
	by := map[string][]*resolve.Node{}
	for _, f := range fs {
		g := flagGroup(f)
		if _, ok := by[g]; !ok && g != "" {
			order = append(order, g)
		}
		by[g] = append(by[g], f)
	}
	var out []*resolve.Node
	for _, g := range order {
		out = append(out, by[g]...)
	}
	return append(out, by[""]...)
}

// flagName is a flag as it's written, linked to its anchor, with its
// group's default marked.
func (s *Site) flagName(f *resolve.Node) string {
	h := s.schemaLink(f)
	if flagDefault(f) {
		h += ` <span class="badge">` + esc(label("docs.schema.default")) + `</span>`
	}
	return h
}

func (c *Ctx) schemaFlagsList(p *schemaPkg) {
	s := c.Site
	if len(p.flags) == 0 {
		return
	}
	c.Heading(3, p.id+"--flags", label("docs.schema.flags"), "")
	var rows [][]string
	var ids []string
	for _, f := range flagsByGroup(p.flags) {
		g := flagGroup(f)
		if g != "" {
			g = `<code>` + esc(strconv.Quote(g)) + `</code>`
		}
		rows = append(rows, []string{s.flagName(f), g, s.attachesTo(f), s.schemaSentence(f)})
		ids = append(ids, Anchor(f))
	}
	c.schemaTable([]string{label("docs.schema.col_flag"), label("docs.schema.col_group"), label("docs.schema.col_on"), label("docs.col.description")}, rows, ids)
}

func (c *Ctx) schemaCategoriesList(p *schemaPkg) {
	s := c.Site
	if len(p.categories) == 0 {
		return
	}
	c.Heading(3, p.id+"--categories", label("docs.schema.categories"), "")
	var rows [][]string
	var ids []string
	for _, cat := range p.categories {
		rows = append(rows, []string{`<code>` + esc(cat.Name) + `</code>`, s.schemaLinks2(s.schema.members[cat]), s.schemaSentence(cat)})
		ids = append(ids, Anchor(cat))
	}
	c.schemaTable([]string{label("docs.schema.col_category"), label("docs.schema.col_members"), label("docs.col.description")}, rows, ids)
}

// --- the containment tree ---

// accepted are the kinds and categories a kind accepts, in the order its
// body writes them.
func (s *Site) accepted(k *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, a := range s.Checker.AcceptEntries(k) {
		switch {
		case a.Category != nil:
			out = appendOnce(out, a.Category)
		case a.Kind != nil:
			out = appendOnce(out, a.Kind)
		}
	}
	return out
}

// reach is every kind a kind holds, at any depth.
func (s *Site) reach(k *resolve.Node) map[*resolve.Node]bool {
	seen := map[*resolve.Node]bool{}
	queue := []*resolve.Node{k}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, a := range s.accepted(x) {
			kinds := []*resolve.Node{a}
			if s.Checker.IsCategory(a) {
				kinds = s.schema.members[a]
			}
			for _, m := range kinds {
				if !seen[m] {
					seen[m] = true
					queue = append(queue, m)
				}
			}
		}
	}
	return seen
}

// treeTop is what stands at a file's top: the categories the nodes at the
// top of the tree's files join that some kind accepts, or, in a tree with
// no such node, the categories some kind of their own accepts; then the
// kinds no kind holds.
func (s *Site) treeTop() (cats, kinds []*resolve.Node) {
	sr := s.schema
	seen := map[*resolve.Node]bool{}
	addCat := func(c *resolve.Node) {
		if !seen[c] && len(sr.holders[c]) > 0 {
			seen[c] = true
			cats = append(cats, c)
		}
	}
	var tops []*resolve.Node
	for _, ch := range s.Tree.Root.Children {
		if !own(ch) {
			continue
		}
		if ch.Shape == resolve.ShapeScoped || resolve.IsPackageNode(ch) {
			tops = append(tops, ch.Children...)
			continue
		}
		tops = append(tops, ch)
	}
	for _, n := range tops {
		if k := s.Checker.KindOf(n); k != nil && own(n) {
			for _, c := range s.Checker.Categories(k) {
				addCat(c)
			}
		}
	}
	if len(cats) == 0 {
		for _, p := range sr.pkgs {
			for _, c := range p.categories {
				for _, m := range sr.members[c] {
					for _, h := range sr.holders[c] {
						if h == m {
							addCat(c)
						}
					}
				}
			}
		}
	}
	for _, p := range sr.pkgs {
		for _, k := range p.kinds {
			if hs, cs := s.holdersOf(k); len(hs)+len(cs) == 0 {
				kinds = append(kinds, k)
			}
		}
	}
	return cats, kinds
}

func (c *Ctx) containmentTree() {
	s := c.Site
	c.W(`<div class="schema-tree"><p class="tree-file">`, esc(label("docs.schema.file")), `</p><ul>`)
	cats, kinds := s.treeTop()
	path := map[*resolve.Node]bool{}
	for _, cat := range cats {
		c.treeCategory(treeItem{cat: cat, kinds: s.schema.members[cat]}, path, 0)
	}
	for _, k := range kinds {
		c.treeKind(k, path, 0)
	}
	c.W(`</ul></div>`)
}

// treeItem is one entry under a kind of the containment tree: a kind it
// holds by its name alone, or a category with the kinds it holds of it.
type treeItem struct {
	cat   *resolve.Node
	kinds []*resolve.Node
}

// treeItems are what a kind holds, in the order its body writes them: each
// category it accepts with that category's kinds, and the kinds it
// accepts by name grouped under the category they join, where it accepts
// that category too or they're more than half of its kinds, two or more,
// so a category's kinds stand together under its name.
func (s *Site) treeItems(k *resolve.Node) []treeItem {
	accepted := s.accepted(k)
	byCat := map[*resolve.Node]bool{}
	count := map[*resolve.Node]int{}
	for _, x := range accepted {
		if s.Checker.IsCategory(x) {
			byCat[x] = true
			continue
		}
		for _, cat := range s.Checker.Categories(x) {
			count[cat]++
		}
	}
	groupOf := func(x *resolve.Node) *resolve.Node {
		cs := s.Checker.Categories(x)
		for _, cat := range cs {
			if byCat[cat] {
				return cat
			}
		}
		for _, cat := range cs {
			if count[cat] > 1 && 2*count[cat] > len(s.schema.members[cat]) {
				return cat
			}
		}
		return nil
	}
	var out []treeItem
	at := map[*resolve.Node]int{}
	add := func(cat *resolve.Node, ks ...*resolve.Node) {
		i, ok := at[cat]
		if !ok {
			i = len(out)
			at[cat] = i
			out = append(out, treeItem{cat: cat})
		}
		for _, m := range ks {
			out[i].kinds = appendOnce(out[i].kinds, m)
		}
	}
	for _, x := range accepted {
		switch {
		case s.Checker.IsCategory(x):
			add(x, s.schema.members[x]...)
		case groupOf(x) != nil:
			add(groupOf(x), x)
		default:
			out = append(out, treeItem{kinds: []*resolve.Node{x}})
		}
	}
	return out
}

func (c *Ctx) treeKind(k *resolve.Node, path map[*resolve.Node]bool, depth int) {
	s := c.Site
	entry := `<span class="tree-kind">` + s.kindIcon(k, "icon") + `<a class="ref" href="` + attr(s.schemaURL(k)) + `"><code>` + esc(k.Name) + `</code></a></span>`
	items := s.treeItems(k)
	if path[k] || len(items) == 0 || depth > 8 {
		c.W(`<li>`, entry, `</li>`)
		return
	}
	// A kind opens where the tree first meets it, and is its name alone
	// wherever it's met again.
	path[k] = true
	open := ""
	if depth < 2 {
		open = " open"
	}
	c.W(`<li><details`, open, `><summary>`, entry, `</summary><ul>`)
	for _, it := range items {
		if it.cat != nil {
			c.treeCategory(it, path, depth+1)
		} else {
			c.treeKind(it.kinds[0], path, depth+1)
		}
	}
	c.W(`</ul></details></li>`)
}

// treeCategoryID is the anchor of the place the containment tree first
// lists a category's kinds.
func treeCategoryID(cat *resolve.Node) string { return "tree-" + Anchor(cat) }

// treeCategory writes a category and its kinds where the tree first meets
// it, and its name alone, linked to that first place, wherever it's met
// again.
func (c *Ctx) treeCategory(it treeItem, path map[*resolve.Node]bool, depth int) {
	s := c.Site
	cat := it.cat
	if path[cat] {
		c.W(`<li><span class="tree-cat tree-again"><a class="ref" href="#`, attr(treeCategoryID(cat)), `">`, esc(cat.Name), `</a></span></li>`)
		return
	}
	entry := `<span class="tree-cat"><a class="ref" href="` + attr(s.schemaURL(cat)) + `">` + esc(cat.Name) + `</a></span>`
	if len(it.kinds) == 0 {
		c.W(`<li>`, entry, `</li>`)
		return
	}
	path[cat] = true
	open := ""
	if depth < 1 {
		open = " open"
	}
	c.W(`<li id="`, attr(treeCategoryID(cat)), `"><details`, open, `><summary>`, entry, `</summary><ul>`)
	for _, m := range it.kinds {
		c.treeKind(m, path, depth+1)
	}
	c.W(`</ul></details></li>`)
}

// --- a kind's page ---

// paramsOf are a declaration's parameters, as an op's are read.
func (s *Site) paramsOf(n *resolve.Node) []opParam {
	if n.Decl.N == nil || n.Decl.N.Kind != syntax.Declare {
		return nil
	}
	d := ast.Declare{N: n.Decl.N}
	site := resolve.Site{File: n.Decl.File, N: n.Decl.N, Scope: n.Decl.Scope}
	var out []opParam
	for _, p := range d.Params().Params() {
		if p.Ident() == nil {
			continue
		}
		pp := opParam{name: p.Ident().Text, syn: p, site: site, optional: p.Question() != nil || p.Default() != nil, def: p.Default()}
		pp.typ = s.readType(p.Type(), site, nil)
		if doc := p.Doc(); doc.N != nil {
			pp.doc = doc.N
		}
		for _, f := range p.Flags().Idents() {
			if f.Text == "variadic" {
				pp.name += "…"
			}
		}
		out = append(out, pp)
	}
	return out
}

// kindForms are how a node of a kind is written in a Heron file and as an
// element of a prose file; the element form is "" for a kind that stands
// in structure alone.
func (s *Site) kindForms(k *resolve.Node) (heron, md string) {
	var args, attrs []string
	for _, p := range s.Checker.KindParams(k, k) {
		switch {
		case p.Variadic:
			args = append(args, p.Name+"…")
		case p.Optional:
			args = append(args, p.Name+" = …")
		default:
			args = append(args, p.Name)
		}
		attrs = append(attrs, p.Name+`="…"`)
	}
	holds, prose := false, false
	for _, a := range s.Checker.AcceptEntries(k) {
		switch {
		case a.Kind != nil || a.Category != nil:
			holds = true
		case a.Mode == "prose" || a.Mode == "code" || a.Text != "":
			prose = true
		}
	}
	heron = "name: " + k.Name
	if len(args) > 0 {
		heron += "(" + strings.Join(args, ", ") + ")"
	}
	switch {
	case holds:
		heron += " { … }"
	case prose:
		heron += " {{ … }}"
	}
	if s.Checker.StructureOnly(k) {
		return heron, ""
	}
	md = "<" + k.Name + ` id="name"`
	if len(attrs) > 0 {
		md += " " + strings.Join(attrs, " ")
	}
	if holds || prose {
		md += ">…</" + k.Name + ">"
	} else {
		md += "/>"
	}
	return heron, md
}

// leadAndRest splits a declaration's docs into its first sentence and the
// rest of its first paragraph.
func (s *Site) leadAndRest(n *resolve.Node) (lead, rest string) {
	h := s.schemaDoc(n)
	if h == "" {
		return "", ""
	}
	if !strings.Contains(h, "<p>") {
		h = "<p>" + h + "</p>"
	}
	i := strings.Index(h, "<p>")
	j := strings.Index(h[i:], "</p>")
	para := h[i+3 : i+j]
	lead = firstSentence(para)
	rest = strings.TrimSpace(para[len(lead):])
	return lead, rest
}

// phrase joins items as one phrase: "a, b and c".
func phrase(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " " + esc(label("docs.schema.and")) + " " + items[len(items)-1]
}

func (s *Site) kindPage(c *Ctx) error {
	k := s.schema.pageOf[c.Page]
	if k == nil {
		return nil
	}
	sigs := append([]*resolve.Node{k}, k.Overloads...)
	c.W(`<section class="node kind-page" id="`, attr(Anchor(k)), `">`)
	lead, rest := s.leadAndRest(k)
	if lead != "" {
		c.W(`<p class="lead">`, lead, `</p>`)
	}
	// How a node of it is written, in each of its signatures.
	var herons, mds []string
	for _, sig := range sigs {
		heron, md := s.kindForms(sig)
		herons = append(herons, `<code>`+s.highlightSource(heron)+`</code>`)
		if md != "" {
			mds = append(mds, `<code>`+esc(md)+`</code>`)
		}
	}
	c.W(`<dl class="facts kind-forms"><div class="fact"><dt>`, esc(label("docs.schema.tab_heron")), `</dt><dd>`, strings.Join(herons, `<br>`), `</dd></div>`)
	if len(mds) > 0 {
		c.W(`<div class="fact"><dt>`, esc(label("docs.schema.tab_md")), `</dt><dd>`, strings.Join(mds, `<br>`), `</dd></div>`)
	}
	c.W(`</dl>`)

	// The summary.
	var rows [][2]string
	if l := s.layerOf(k); l != "" {
		rows = append(rows, [2]string{label("docs.schema.layer"), esc(sentenceCase(strings.ToLower(l)))})
	}
	if cats := s.Checker.Categories(k); len(cats) > 0 {
		rows = append(rows, [2]string{label("docs.schema.belongs_to"), s.schemaLinks2(cats)})
	}
	if kinds, cats := s.holdersOf(k); len(kinds)+len(cats) > 0 {
		var items []string
		for _, h := range kinds {
			items = append(items, s.schemaLink(h))
		}
		for _, cat := range cats {
			items = append(items, strings.Replace(esc(label("docs.schema.any_accepting")), "{category}", s.schemaLink(cat), 1))
		}
		rows = append(rows, [2]string{label("docs.schema.goes_inside"), phrase(items)})
	}
	if groups, modes := s.holdsOf(k); len(groups)+len(modes) > 0 {
		var parts []string
		for _, g := range groups {
			name := esc(label("docs.schema.other"))
			if g.cat != nil {
				name = s.schemaLink(g.cat)
				if g.catMax > 0 {
					name += ` <span class="mark">` + esc(label("docs.schema.at_most", "n", strconv.Itoa(g.catMax))) + `</span>`
				}
			}
			var ks []string
			for _, m := range g.kinds {
				l := s.schemaLink(m)
				if n := g.max[m]; n > 0 {
					l += ` <span class="mark">` + esc(label("docs.schema.at_most", "n", strconv.Itoa(n))) + `</span>`
				}
				ks = append(ks, l)
			}
			parts = append(parts, `<span class="hold-group">`+name+`: `+strings.Join(ks, ", ")+`</span>`)
		}
		for _, m := range modes {
			parts = append(parts, `<span class="hold-group">`+esc(m)+`</span>`)
		}
		rows = append(rows, [2]string{label("docs.schema.holds"), strings.Join(parts, "")})
	}
	if refs, ok := s.Checker.ReferenceKinds(k); ok && len(refs) > 0 {
		rows = append(rows, [2]string{label("docs.schema.references"), s.schemaLinks2(refs)})
	}
	if named, anywhere := s.annotationsOn(k); len(named)+len(anywhere) > 0 {
		h := s.schemaLinks2(named)
		if len(anywhere) > 0 {
			any := `<span class="any-node">` + esc(label("docs.schema.any_node")) + `: ` + s.schemaLinks2(anywhere) + `</span>`
			if h != "" {
				h += `<br>`
			}
			h += any
		}
		rows = append(rows, [2]string{label("docs.schema.annotations"), h})
	}
	if fs := s.flagsOn(k); len(fs) > 0 {
		rows = append(rows, [2]string{label("docs.schema.flags"), s.flagsPhrase(fs)})
	}
	if len(rows) > 0 {
		c.W(`<dl class="facts kind-summary">`)
		for _, r := range rows {
			c.W(`<div class="fact"><dt>`, esc(r[0]), `</dt><dd>`, r[1], `</dd></div>`)
		}
		c.W(`</dl>`)
	}
	if rest != "" {
		c.W(`<div class="prose"><p>`, rest, `</p></div>`)
	}

	if len(sigs) == 1 {
		if ps := s.paramsOf(k); len(ps) > 0 {
			c.Heading(2, Anchor(k)+".params", label("docs.schema.parameters"), "")
			c.paramsTable(ps)
		}
		if ms := s.members(k, nil); len(ms) > 0 {
			c.Heading(2, Anchor(k)+".fields", label("docs.schema.fields"), "")
			c.fieldsTable(k, ms)
		}
	} else {
		// An overloaded kind: each signature with its docs, its
		// parameters and its fields.
		c.Heading(2, Anchor(k)+".signatures", label("docs.schema.signatures"), "")
		for i, sig := range sigs {
			id := Anchor(k) + ".signature-" + strconv.Itoa(i+1)
			c.Heading(3, id, signatureOf(sig), "")
			if i > 0 {
				if h := s.schemaDoc(sig); h != "" {
					c.W(`<div class="prose">`, h, `</div>`)
				}
			}
			if ps := s.paramsOf(sig); len(ps) > 0 {
				c.W(`<p class="label">`, esc(label("docs.schema.parameters")), `</p>`)
				c.paramsTable(ps)
			}
			if ms := s.members(sig, nil); len(ms) > 0 {
				c.W(`<p class="label">`, esc(label("docs.schema.fields")), `</p>`)
				start := c.out.Len()
				c.fieldsTable(sig, ms)
				// The signatures share their path: each row's anchor
				// names its signature.
				all := c.out.String()
				c.out.Reset()
				c.out.WriteString(all[:start])
				c.out.WriteString(strings.ReplaceAll(all[start:], ` id="`+Anchor(k)+`.`, ` id="`+id+`.`))
			}
		}
	}
	c.kindExample(k)
	c.checkedBySection(k)
	c.usedIn(k)
	c.W(`</section>`)
	return nil
}

// flagsPhrase writes flags by their group: a group's flags joined by "or",
// in the group's quotes, and the flags of no group after.
func (s *Site) flagsPhrase(fs []*resolve.Node) string {
	var out []string
	var cur string
	var group []string
	flush := func() {
		if len(group) > 0 {
			out = append(out, `<span class="hold-group"><code>`+esc(strconv.Quote(cur))+`</code>: `+strings.Join(group, ", ")+`</span>`)
			group = nil
		}
	}
	for _, f := range flagsByGroup(fs) {
		g := flagGroup(f)
		if g == "" {
			flush()
			out = append(out, `<span class="hold-group">`+s.flagName(f)+`</span>`)
			continue
		}
		if g != cur {
			flush()
			cur = g
		}
		group = append(group, s.flagName(f))
	}
	flush()
	return strings.Join(out, "")
}

// checkedBySection lists the rules that link a declaration, with the ids
// of the errors that link each.
func (c *Ctx) checkedBySection(n *resolve.Node) {
	s := c.Site
	rules, codes := s.checkedBy(n)
	if len(rules) == 0 {
		return
	}
	c.Heading(2, Anchor(n)+".checked_by", label("docs.schema.checked_by"), "")
	for _, r := range rules {
		d := c.at(r, 3)
		d.anchorSuffix = "--" + Anchor(n)
		d.renderRef()
		if cs := codes[r]; len(cs) > 0 {
			c.W(`<p class="rule-codes">`)
			for _, code := range cs {
				c.W(`<code class="code-id">`, esc(code), `</code> `)
			}
			c.W(`</p>`)
		}
	}
}

// usedIn writes how many nodes of the tree are of a kind, and links the
// nodes whose text links it.
func (c *Ctx) usedIn(k *resolve.Node) {
	s := c.Site
	count := s.instances(k)
	var linked []*resolve.Node
	rules, _ := s.checkedBy(k)
	isRule := map[*resolve.Node]bool{}
	for _, r := range rules {
		isRule[r] = true
	}
	for _, o := range s.schemaLinks(k) {
		if !isRule[o] {
			linked = append(linked, o)
		}
	}
	if count == 0 && len(linked) == 0 {
		return
	}
	c.Heading(2, Anchor(k)+".used_in", label("docs.schema.used_in"), "")
	switch count {
	case 0:
		c.W(`<p class="used-count">`, esc(label("docs.schema.nodes_none")), `</p>`)
	case 1:
		c.W(`<p class="used-count">`, esc(label("docs.schema.nodes_one")), `</p>`)
	default:
		c.W(`<p class="used-count">`, esc(label("docs.schema.nodes_count", "count", strconv.Itoa(count))), `</p>`)
	}
	if len(linked) > 0 {
		c.W(`<p class="label">`, esc(label("docs.schema.linked_from")), `</p><ul class="refs">`)
		for _, r := range linked {
			pre, post := s.kindMark(r)
			c.W(`<li>`, pre, s.linkHTML(r, esc(displayName(s, r)), ""), post, `</li>`)
		}
		c.W(`</ul>`)
	}
}

// --- an annotation's page ---

func (s *Site) annotationPage(c *Ctx) error {
	a := s.schema.pageOf[c.Page]
	if a == nil {
		return nil
	}
	c.W(`<section class="node annotation-page" id="`, attr(Anchor(a)), `">`)
	c.W(s.codeFrame("heron", s.declFile(a), s.highlightDecl(a.Decl, true), dedent(strings.TrimSpace(declText(a)), declIndent(a.Decl))))
	if h := s.schemaDoc(a); h != "" {
		c.W(`<div class="prose">`, h, `</div>`)
	}
	rep := label("docs.schema.no")
	if types.HasKindFlag(a, "repeatable") {
		rep = label("docs.schema.yes")
	}
	c.W(`<dl class="facts"><div class="fact"><dt>`, esc(label("docs.schema.attaches_to")), `</dt><dd>`, s.attachesTo(a), `</dd></div>`)
	c.W(`<div class="fact"><dt>`, esc(label("docs.schema.repeatable")), `</dt><dd>`, esc(rep), `</dd></div></dl>`)
	if ps := s.paramsOf(a); len(ps) > 0 {
		c.Heading(2, Anchor(a)+".params", label("docs.schema.parameters"), "")
		c.paramsTable(ps)
	}
	c.checkedBySection(a)
	c.W(`</section>`)
	return nil
}

// declText is a declaration's source without its doc lines.
func declText(n *resolve.Node) string {
	if n.Decl.N == nil {
		return ""
	}
	var b strings.Builder
	writeSkipping(&b, n.Decl.N, func(x *syntax.Node) bool { return x.Kind == syntax.Doc })
	return b.String()
}
