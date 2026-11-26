package docs

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
)

// conformancePage is a project's conformance report: each requirement of
// the spec it implements with the tests that prove it, or planned,
// untested or missing, and what changed since the earlier spec version.
func (s *Site) conformancePage(c *Ctx) error {
	rep := s.project.Conformance()
	c.W(`<div class="conformance">`)
	c.W(`<p class="conformance-summary">`, esc(label("docs.conformance.summary",
		"project", displayName(s, s.project.Node), "spec", rep.Spec, "at", rep.At,
		"proven", strconv.Itoa(rep.Counts["proven"]), "planned", strconv.Itoa(rep.Counts["planned"]),
		"untested", strconv.Itoa(rep.Counts["untested"]), "missing", strconv.Itoa(rep.Counts["missing"]),
		"total", strconv.Itoa(rep.Obligations()))))
	if n := rep.Counts["practice"]; n > 0 {
		c.W(` `, esc(label("docs.conformance.practices_count", "practices", strconv.Itoa(n))))
	}
	c.W(`</p>`)
	if rep.Version != "" {
		c.W(`<p class="conformance-version">`, esc(label("docs.conformance.version", "version", rep.Version)), `</p>`)
	}
	s.conformanceTable(c, rep, func(row project.Row) bool { return row.Status != "practice" })
	if rep.Counts["practice"] > 0 {
		c.Heading(2, "practices", label("docs.conformance.practices"), "")
		c.W(`<p>`, esc(label("docs.conformance.practices_note")), `</p>`)
		s.conformanceTable(c, rep, func(row project.Row) bool { return row.Status == "practice" })
	}
	if n := rep.Counts["outdated"]; n > 0 {
		c.W(`<p class="conformance-outdated">`, esc(label("docs.conformance.outdated_count", "outdated", strconv.Itoa(n))), `</p>`)
	}
	// The gap report: one list per gap, each entry linked.
	for _, g := range []struct {
		id, key string
		items   []string
	}{{"gap-unrealized", "unrealized", rep.Gaps.Unrealized}, {"gap-uncited", "uncited", rep.Gaps.UncitedTests}} {
		if len(g.items) == 0 {
			continue
		}
		c.Heading(2, g.id, label("docs.conformance.gap_"+g.key), "")
		c.W(`<p>`, esc(label("docs.conformance.gap_"+g.key+"_note")), `</p><ul class="gap-list">`)
		for _, it := range g.items {
			inner := `<code>` + esc(it) + `</code>`
			if n := s.specNode(it); n != nil {
				c.W(`<li>`, s.linkHTML(n, inner, "spec-ref"), `</li>`)
			} else {
				c.W(`<li>`, inner, `</li>`)
			}
		}
		c.W(`</ul>`)
	}
	if ch := rep.Changes; ch != nil {
		title := label("docs.conformance.changes", "spec", rep.Spec, "from", ch.From, "to", ch.To)
		c.Heading(2, "changes", title, "")
		if ch.Err != "" {
			c.W(`<p>`, esc(label("docs.conformance.unreadable", "error", ch.Err)), `</p>`)
		} else {
			c.W(`<dl class="spec-changes">`)
			for _, g := range []struct {
				key   string
				paths []string
			}{{"added", ch.Added}, {"removed", ch.Removed}, {"changed", ch.Changed}} {
				c.W(`<dt>`, esc(label("docs.conformance."+g.key)), `</dt><dd>`)
				if len(g.paths) == 0 {
					c.W(esc(label("docs.conformance.none")))
				}
				for i, p := range g.paths {
					if i > 0 {
						c.W(`, `)
					}
					inner := `<code>` + esc(p) + `</code>`
					if n := s.specNode(p); n != nil {
						c.W(s.linkHTML(n, inner, "spec-ref"))
					} else {
						c.W(inner)
					}
				}
				c.W(`</dd>`)
			}
			c.W(`</dl>`)
		}
	}
	c.W(`</div>`)
	return nil
}

// specNode finds a node of the implemented spec by its path inside the
// package.
func (s *Site) specNode(path string) *resolve.Node {
	if s.project == nil || s.project.Package == nil || s.project.Package.Node == nil {
		return nil
	}
	n := s.project.Package.Node
	for _, seg := range strings.Split(path, ".") {
		if n = n.Child(seg); n == nil {
			return nil
		}
	}
	return n
}

func addProjectRenderers() {
	for k, f := range map[string]RenderFunc{
		"project.implements": renderImplements,
		"project.proves":     renderProves,
		"project.status":     renderImplStatus,
		"project.release":    renderRelease,
	} {
		builtins[k] = f
	}
}

// renderImplements is a project's implements: the spec and its version,
// linked to the spec, the project's version and its prose.
func renderImplements(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<section class="node implements" id="`, attr(Anchor(n)), `">`)
	if c.Page != s.Home {
		// On the home page the page's title is the project's name.
		c.title("")
	}
	if p := s.project; p != nil && p.Node == n {
		var facts [][2]string
		if p.Spec != nil {
			facts = append(facts, [2]string{label("docs.page.spec"), s.linkHTML(p.Spec, `<code>`+esc(p.SpecName())+`</code>`, "spec-ref") + ` <code>` + esc(p.At) + `</code>`})
		}
		if p.Version != "" {
			facts = append(facts, [2]string{label("docs.col.version"), `<code>` + esc(p.Version) + `</code>`})
		}
		c.factList(facts)
	}
	c.Prose(n)
	c.W(`</section>`)
	return nil
}

// renderProves is a proof: the spec requirements it names, linked to the
// spec, and its tests.
func renderProves(c *Ctx) error {
	n := c.Node
	c.W(`<section class="node proves" id="`, attr(Anchor(n)), `">`)
	if n.Name != "" {
		c.title("")
	}
	c.W(`<div class="targets">`, c.specLinks(c.Site.targets(n)), `</div>`)
	c.Prose(n)
	c.W(c.verifyList(n))
	c.W(`</section>`)
	return nil
}

// renderImplStatus is a status: the requirements it covers and whether
// they are planned or untested.
func renderImplStatus(c *Ctx) error {
	n := c.Node
	st := ""
	for _, f := range kindFlags(n) {
		if f == "planned" || f == "untested" {
			st = f
		}
	}
	extra := ""
	if st != "" {
		extra = ` <span class="badge status-` + attr(st) + `">` + esc(label("docs.conformance."+st)) + `</span>`
	}
	c.W(`<section class="node impl-status" id="`, attr(Anchor(n)), `">`)
	if n.Name != "" {
		c.title(extra)
	} else if extra != "" {
		c.W(`<div>`, extra, `</div>`)
	}
	c.W(`<div class="targets">`, c.specLinks(c.Site.targets(n)), `</div>`)
	c.Prose(n)
	c.W(`</section>`)
	return nil
}

// renderRelease is one release: its version, git tag and date.
func renderRelease(c *Ctx) error {
	n := c.Node
	v, _ := argString(kindArgs(n), 0, "version")
	date, _ := argString(kindArgs(n), 1, "date")
	c.W(`<section class="node release" id="`, attr(Anchor(n)), `">`)
	c.title(` <code>` + esc(v) + `</code>`)
	c.factList([][2]string{
		{label("docs.col.tag"), `<code>v` + esc(v) + `</code>`},
		{label("docs.col.date"), esc(date)},
	})
	c.Prose(n)
	c.W(`</section>`)
	return nil
}

// releasesPage lists a project's releases, newest first, by version, tag
// and date.
func (s *Site) releasesPage(c *Ctx) error {
	c.W(`<div class="table-wrap"><table class="releases"><thead><tr><th>`, esc(label("docs.col.version")), `</th><th>`,
		esc(label("docs.col.tag")), `</th><th>`, esc(label("docs.col.date")), `</th><th>`, esc(label("docs.col.description")), `</th></tr></thead><tbody>`)
	for _, r := range s.project.ReleaseList() {
		v, _ := argString(kindArgs(r.Node), 0, "version")
		date, _ := argString(kindArgs(r.Node), 1, "date")
		c.W(`<tr><td>`, s.linkHTML(r.Node, `<code>`+esc(v)+`</code>`, ""), `</td><td><code>v`, esc(v),
			`</code></td><td>`, esc(date), `</td><td>`, s.inlineHTML(r.Node), `</td></tr>`)
	}
	c.W(`</tbody></table></div>`)
	return nil
}

// specPage renders the spec a project implements, read-only and marked as
// the spec, when the project names no rendered spec site to link to.
func (s *Site) specPage(c *Ctx) error {
	p := s.project
	pkg := p.Package.Node
	c.W(`<div class="spec-readonly" id="`, attr(Anchor(pkg)), `" data-spec="`, attr(p.SpecName()), `">`)
	c.W(`<p class="spec-note">`, esc(label("docs.project.spec_note", "spec", p.SpecName(), "version", p.SpecVersion())), `</p>`)
	c.Prose(pkg)
	for _, ch := range pkg.Children {
		if ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue {
			continue
		}
		if err := c.at(ch, 2).render(); err != nil {
			return err
		}
	}
	c.W(`</div>`)
	return nil
}

// realizes returns the spec subjects a component's @realizes names.
func (s *Site) realizes(n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, a := range annotationsNamed(n, "realizes") {
		for _, e := range argExprs(a.Args()) {
			if t := s.refTarget(e.Syntax(), n.Decl); t != nil {
				out = append(out, t)
			}
		}
	}
	return out
}

// hasOwnPage reports whether a node is shown on a page of the tree's other
// than the home page.
func (s *Site) hasOwnPage(n *resolve.Node) bool {
	for a := n.Parent; a != nil; a = a.Parent {
		if p := s.pageOf[a]; p != nil && p != s.Home {
			return true
		}
	}
	return false
}

// specPages adds a project's pages beyond its tree's: the releases and,
// when no spec site is named, the spec itself, read-only, on which every
// node of the spec is then shown.
func (s *Site) specPages(docs *Area) {
	p := s.project
	if len(p.Releases) > 0 {
		pg := &Page{File: "_releases.html", Title: label("docs.page.releases"), Layout: "releases", Area: docs}
		s.Pages = append(s.Pages, pg)
		if !s.hasOwnPage(p.Releases[0]) {
			// A tree whose releases sit on a page of its own, such as a
			// releases section, lists that page once, not this one too.
			docs.Pages = append(docs.Pages, pg)
		}
	}
	if p.Package == nil || p.Package.Tree == nil || p.Package.Node == nil {
		return
	}
	if p.Docs != "" {
		// Links go to the spec's own rendered site, at the page that site
		// shows each node on.
		c := types.New(p.Package.Tree)
		s.specLenses = NewLenses(p.Package.Tree, c, Options{})
		s.specSite = s.specLenses.Default()
		return
	}
	pg := &Page{File: "_spec.html", Title: label("docs.page.spec") + " " + p.SpecName() + " " + p.SpecVersion(), Layout: "spec", Area: docs}
	s.Pages = append(s.Pages, pg)
	docs.Pages = append(docs.Pages, pg)
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range n.Children {
			if s.anchorOf[ch] == nil {
				s.anchorOf[ch] = pg
			}
			walk(ch)
		}
	}
	s.anchorOf[p.Package.Node] = pg
	walk(p.Package.Node)
}

// specURL is the address of a spec node on the spec's own site, when the
// project names one with `docs`.
func (s *Site) specURL(n *resolve.Node) string {
	if s.specSite == nil || project.PackageOf(n) != s.project.Package {
		return ""
	}
	base := s.project.Docs
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	// The first of the spec's lenses that renders the node, in the order
	// its docs block lists them.
	sites := []*Site{s.specSite}
	if s.specLenses != nil {
		sites = s.specLenses.ordered
	}
	for _, o := range sites {
		if o.anchorOf[n] == nil || !o.kept(n) {
			continue
		}
		return base + lensRel(nil, o.Opts.Lens) + o.localURL(n)
	}
	return ""
}

// specLinks links spec nodes by their path inside the spec, as
// `parts.cart`.
func (c *Ctx) specLinks(ns []*resolve.Node) string {
	var parts []string
	for _, n := range ns {
		text := n.Path()
		if pkg := project.PackageOf(n); pkg != nil {
			text = project.RelPath(pkg, n)
		}
		parts = append(parts, c.Site.linkHTML(n, `<code>`+esc(text)+`</code>`, "spec-ref"))
	}
	return strings.Join(parts, `<span class="sep">, </span>`)
}

// conformanceTable writes the report's rows that keep holds as a table.
func (s *Site) conformanceTable(c *Ctx, rep *project.Report, keep func(project.Row) bool) {
	c.W(`<div class="table-wrap"><table class="conformance-table"><thead><tr><th>`, esc(label("docs.col.requirement")),
		`</th><th>`, esc(label("docs.col.kind")), `</th><th>`, esc(label("docs.col.status")),
		`</th><th>`, esc(label("docs.col.proven_by")), `</th></tr></thead><tbody>`)
	for _, row := range rep.Rows {
		if !keep(row) {
			continue
		}
		// A long path breaks at its dots, not inside a name.
		c.W(`<tr><td>`, s.linkHTML(row.Node, `<code>`+strings.ReplaceAll(esc(row.Path), ".", ".<wbr>")+`</code>`, "spec-ref"),
			`</td><td>`, esc(row.Kind), `</td><td><span class="badge status-`, attr(row.Status), `">`,
			esc(label("docs.conformance."+row.Status)), `</span></td><td>`)
		for i, p := range row.Proofs {
			if i > 0 {
				c.W(`<br>`)
			}
			c.W(`<code class="path">`, strings.ReplaceAll(esc(p.Path), "/", "/<wbr>"), `</code>`)
			if p.Test != "" {
				c.W(` <code class="test">`, esc(p.Test), `</code>`)
			}
		}
		c.W(`</td></tr>`)
	}
	c.W(`</tbody></table></div>`)
}
