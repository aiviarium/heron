package docs

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/tools/prose"
)

// The gaps page and contract coverage: what's about building the product,
// in the lenses that show the system's parts. The gaps page gathers the
// intent gaps `heron lint` reports, grouped by the innermost feature each
// stands in; contract coverage is a figure on the feature pages and the
// Features page. Neither fails a build.

// gapGroup is the gaps in one feature, or in one area of the tree for the
// gaps in no feature.
type gapGroup struct {
	// Feature is the feature the gaps stand in, or nil; Area is the first
	// two names of their path then.
	Feature *resolve.Node
	Area    string
	Items   []prose.Gap
}

// buildsProduct reports whether the lens shows what's about building the
// product: whether its lists keep the system's parts, the kind `part` of
// std/spec, as explore and contributor do and user doesn't.
func (s *Site) buildsProduct() bool {
	if s.explore() {
		return true
	}
	if s.partKind == nil {
		s.partKind = []*resolve.Node{stdKind(s.Tree, "spec", "part")}
	}
	k := s.partKind[0]
	return k == nil || s.Opts.Lens.KindMode(s.Checker, k) != lens.Hide
}

// stdKind finds a kind a standard package declares, by the package's
// node name and the kind's, among the tree's imports and theirs.
func stdKind(t *resolve.Tree, pkg, name string) *resolve.Node {
	seen := map[*resolve.Tree]bool{}
	var find func(ps []*resolve.Package) *resolve.Node
	find = func(ps []*resolve.Package) *resolve.Node {
		for _, p := range ps {
			if p.Tree == nil || seen[p.Tree] {
				continue
			}
			seen[p.Tree] = true
			for _, n := range p.Tree.Root.Children {
				if n.Name == pkg && n.Decl.File != nil && n.Decl.File.ID.IsStd() {
					if k := n.Child(name); k != nil {
						return k
					}
				}
			}
			if k := find(p.Tree.Imports); k != nil {
				return k
			}
		}
		return nil
	}
	return find(t.Imports)
}

// gapGroups groups the tree's intent gaps by feature, in the order the
// groups first appear; none in a lens that doesn't show what's about
// building the product, or when the gaps can't be read.
func (s *Site) gapGroups() (out []gapGroup) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	if !s.buildsProduct() {
		return nil
	}
	at := map[string]int{}
	for _, g := range prose.IntentGaps(s.Tree, s.Checker) {
		var feature *resolve.Node
		for x := g.Node.Parent; x != nil && feature == nil; x = x.Parent {
			if s.kindPath(x) == "spec.feature" {
				feature = x
			}
		}
		key := ""
		grp := gapGroup{Feature: feature}
		if feature != nil {
			key = feature.Path()
		} else {
			segs := strings.SplitN(g.Node.Path(), ".", 3)
			grp.Area = segs[0]
			if len(segs) == 3 {
				grp.Area = segs[0] + "." + segs[1]
			}
			key = "area:" + grp.Area
		}
		i, ok := at[key]
		if !ok {
			i = len(out)
			at[key] = i
			out = append(out, grp)
		}
		out[i].Items = append(out[i].Items, g)
	}
	return out
}

// gapsPage writes the intent gaps: a group per feature, folded, with its
// count, and each gap as its node, linked, and the message `heron lint`
// reports for it.
func (s *Site) gapsPage(c *Ctx) error {
	if len(s.gaps) == 0 {
		return nil
	}
	c.W(`<p>`, esc(label("docs.gaps.note")), `</p>`)
	for _, g := range s.gaps {
		prefix := g.Area
		head := `<code>` + esc(g.Area) + `</code>`
		if g.Feature != nil {
			prefix = g.Feature.Path()
			head = esc(displayName(s, g.Feature))
		}
		c.W(`<details class="gap-group"><summary>`, head, ` <span class="gap-count">`, strconv.Itoa(len(g.Items)), `</span></summary><ul class="gap-list">`)
		for _, it := range g.Items {
			inner := `<code>` + esc(strings.TrimPrefix(it.Node.Path(), prefix+".")) + `</code>`
			c.W(`<li>`, s.linkHTML(it.Node, inner, "spec-ref"), ` <span class="gap-message">`, esc(it.Message), `</span></li>`)
		}
		c.W(`</ul></details>`)
	}
	return nil
}

// contractCoverage is the figure of a feature's contract coverage: how
// many of its rules about a part or the system a contract entry carries
// out, of all of them; all is 0 in a lens that doesn't show what's about
// building the product.
func (s *Site) contractCoverage(f *resolve.Node) (done, all int) {
	if !s.buildsProduct() {
		return 0, 0
	}
	if s.carried == nil {
		s.carried = project.CarriedOut(s.Tree, s.Checker)
	}
	return project.ContractCoverage(s.Checker, s.featureRequirements(f), s.carried)
}

// coverageFigure is a contract coverage figure, "N of M".
func coverageFigure(done, all int) string {
	return label("docs.coverage.figure", "done", strconv.Itoa(done), "all", strconv.Itoa(all))
}
