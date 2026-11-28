package docs

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// The view of std/spec's feature kind: a page of its own with its goals,
// its requirements and their coverage, and one generated Features page
// that lists every feature of the tree.

// featureKinds are the kinds a feature counts among its requirements,
// in the order its page groups them.
var featureKinds = []string{"spec.rule", "spec.usecase", "spec.scenario", "spec.states"}

// featureRequirements are a feature's requirements: those it holds at any
// depth outside a nested feature, then the rules anywhere that reference
// it.
func (s *Site) featureRequirements(f *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	seen := map[*resolve.Node]bool{}
	isReq := func(n *resolve.Node) bool {
		k := s.kindPath(n)
		for _, x := range featureKinds {
			if k == x {
				return true
			}
		}
		return false
	}
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range n.Children {
			if s.kindPath(ch) == "spec.feature" {
				continue
			}
			if isReq(ch) && !seen[ch] {
				seen[ch] = true
				out = append(out, ch)
			}
			walk(ch)
		}
	}
	walk(f)
	s.Tree.Walk(func(n *resolve.Node) {
		if seen[n] || !own(n) {
			return
		}
		if s.kindPath(n) != "spec.rule" {
			return
		}
		for _, t := range project.HardTargets(s.Checker, n) {
			if t == f {
				seen[n] = true
				out = append(out, n)
				return
			}
		}
	})
	return out
}

// covered reports whether a requirement is covered: an outcome scenario or
// an example names it, or it holds an example.
func (s *Site) covered(r *resolve.Node) bool {
	if len(s.childrenOfKind(r, "spec.example")) > 0 {
		return true
	}
	for _, x := range s.specLinks()[r]["hard"] {
		switch s.kindPath(x) {
		case "spec.scenario", "spec.example":
			return true
		}
	}
	return false
}

// featureFacts are a feature's goals, qualities, actors and assumptions as
// links and its flags.
func (s *Site) featureFacts(f *resolve.Node) []string {
	var out []string
	var goals, actors, assumptions []string
	for _, t := range project.HardTargets(s.Checker, f) {
		link := s.linkHTML(t, esc(displayName(s, t)), "")
		switch s.kindPath(t) {
		case "spec.goal", "spec.quality":
			goals = append(goals, link)
		case "spec.actor":
			actors = append(actors, link)
		case "spec.assumption":
			assumptions = append(assumptions, link)
		}
	}
	if len(goals) > 0 {
		out = append(out, esc(label("docs.label.goal"))+` `+strings.Join(goals, ", "))
	}
	if len(actors) > 0 {
		out = append(out, esc(label("docs.label.for_actors"))+` `+strings.Join(actors, ", "))
	}
	if len(assumptions) > 0 {
		out = append(out, esc(label("docs.label.relies"))+` `+strings.Join(assumptions, ", "))
	}
	for _, fl := range project.KindFlags(f) {
		out = append(out, `<span class="mark">`+esc(fl)+`</span>`)
	}
	return out
}

// renderFeature shows a feature as a page: its title, its facts, its
// text, its requirements by kind with their coverage, and its children.
func renderFeature(c *Ctx) error {
	n := c.Node
	s := c.Site
	c.W(`<section class="section feature" id="`, attr(Anchor(n)), `">`)
	c.title("")
	if fs := s.featureFacts(n); len(fs) > 0 {
		c.W(`<p class="rule-meta">`, strings.Join(fs, `<span class="dot"> · </span>`), `</p>`)
	}
	c.Prose(n)
	reqs := s.featureRequirements(n)
	var shown []*resolve.Node
	for _, r := range reqs {
		if s.kept(r) || s.anchorOf[r] != nil {
			shown = append(shown, r)
		}
	}
	if len(reqs) > 0 {
		// The counts are of every requirement, as the Features page's
		// are; the lists link those the lens shows.
		c.Heading(c.Level+1, Anchor(n)+".requirements", label("docs.label.requirements"), "")
		covered := 0
		var open []*resolve.Node
		for _, r := range reqs {
			if s.covered(r) {
				covered++
			} else if s.kept(r) || s.anchorOf[r] != nil {
				open = append(open, r)
			}
		}
		c.W(`<p class="feature-coverage">`, esc(label("docs.label.covered")), ` `, strconv.Itoa(covered), ` / `, strconv.Itoa(len(reqs)), `</p>`)
		if done, all := s.contractCoverage(n); all > 0 {
			c.W(`<p class="feature-contract"><span class="feature-contract-label">`, esc(label("docs.label.contract_coverage")), `</span> <span class="feature-figure">`, esc(coverageFigure(done, all)), `</span> <span class="feature-contract-note">`, esc(label("docs.coverage.note")), `</span></p>`)
		}
		c.W(`<ul class="lines">`)
		for _, k := range featureKinds {
			var links []string
			for _, r := range shown {
				if s.kindPath(r) == k {
					links = append(links, s.linkHTML(r, esc(displayName(s, r)), ""))
				}
			}
			if len(links) > 0 {
				c.W(`<li><p class="line-head">`, esc(sentenceCase(kindWords(k[strings.LastIndexByte(k, '.')+1:]))), ` (`, strconv.Itoa(len(links)), `)</p><p>`, strings.Join(links, ", "), `</p></li>`)
			}
		}
		if len(open) > 0 {
			var links []string
			for _, r := range open {
				links = append(links, s.linkHTML(r, esc(displayName(s, r)), ""))
			}
			c.W(`<li><p class="line-head">`, esc(label("docs.label.not_covered")), `</p><p>`, strings.Join(links, ", "), `</p></li>`)
		}
		c.W(`</ul>`)
	}
	if err := c.childrenOrLinks(); err != nil {
		return err
	}
	c.W(`</section>`)
	return nil
}

// featuresPage lists every feature of the tree, nested as they nest, with
// its goals, its marks and the number of its requirements, and, in the
// lenses that show it, its contract coverage as a column.
func (s *Site) featuresPage(c *Ctx) error {
	var list func(fs []*resolve.Node)
	children := func(f *resolve.Node) []*resolve.Node {
		var out []*resolve.Node
		var walk func(n *resolve.Node)
		walk = func(n *resolve.Node) {
			for _, ch := range n.Children {
				if s.kindPath(ch) == "spec.feature" {
					if s.kept(ch) {
						out = append(out, ch)
					}
					continue
				}
				walk(ch)
			}
		}
		walk(f)
		return out
	}
	// The contract coverage column, in the lenses that show it, when a
	// feature has a rule it counts.
	figures := false
	if s.buildsProduct() {
		s.Tree.Walk(func(n *resolve.Node) {
			if !figures && own(n) && s.kindPath(n) == "spec.feature" && s.kept(n) {
				_, all := s.contractCoverage(n)
				figures = all > 0
			}
		})
	}
	list = func(fs []*resolve.Node) {
		c.W(`<ul class="features">`)
		for _, f := range fs {
			c.W(`<li id="`, attr("features."+Anchor(f)), `"><p class="line-head"><span class="feature-line">`, s.linkHTML(f, esc(displayName(s, f)), ""))
			facts := s.featureFacts(f)
			facts = append(facts, esc(label("docs.label.requirements"))+` `+strconv.Itoa(len(s.featureRequirements(f))))
			c.W(` <span class="rule-meta">`, strings.Join(facts, `<span class="dot"> · </span>`), `</span></span>`)
			if figures {
				fig := ""
				if done, all := s.contractCoverage(f); all > 0 {
					fig = esc(coverageFigure(done, all))
				}
				c.W(`<span class="feature-figure">`, fig, `</span>`)
			}
			c.W(`</p>`)
			if sub := children(f); len(sub) > 0 {
				list(sub)
			}
			c.W(`</li>`)
		}
		c.W(`</ul>`)
	}
	if figures {
		c.W(`<p class="feature-contract"><span class="feature-contract-label">`, esc(label("docs.label.contract_coverage")), `</span> <span class="feature-contract-note">`, esc(label("docs.coverage.note")), `</span></p>`)
		c.W(`<p class="features-head"><span>`, esc(label("docs.label.feature")), `</span><span>`, esc(label("docs.label.contract_coverage")), `</span></p>`)
	}
	list(children(s.Tree.Root))
	return nil
}

// hasFeatures reports whether the lens shows a feature of the tree.
func (s *Site) hasFeatures() bool {
	found := false
	s.Tree.Walk(func(n *resolve.Node) {
		if !found && own(n) && s.kindPath(n) == "spec.feature" && s.kept(n) {
			found = true
		}
	})
	return found
}
