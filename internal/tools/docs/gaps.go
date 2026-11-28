package docs

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// The gap report as a page of the site: the behaviour no contract entry
// carries out, grouped by the innermost feature each entry stands in, in
// the lenses that show it. It's for reading and never fails a build.

// gapGroup is the entries of the gap report in one feature, or in one
// area of the tree for the entries in no feature.
type gapGroup struct {
	// Feature is the feature the entries stand in, or nil; Area is the
	// first two names of their path then.
	Feature *resolve.Node
	Area    string
	Items   []gapItem
}

// gapItem is one entry: its path, and the node the lens shows it at.
type gapItem struct {
	Path string
	Node *resolve.Node
}

// gapGroups builds the gap report's groups of the entries the lens keeps,
// in the order the groups first appear; none when the report can't be
// read.
func (s *Site) gapGroups() (out []gapGroup) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	paths := project.Unrealized(s.Tree, s.Checker)
	if len(paths) == 0 {
		return nil
	}
	byPath := map[string]*resolve.Node{}
	s.Tree.Walk(func(n *resolve.Node) {
		if own(n) && n.Name != "" {
			byPath[n.Path()] = n
		}
	})
	at := map[string]int{}
	for _, path := range paths {
		n := byPath[path]
		if n == nil {
			// A use case's step: shown at its use case.
			if i := strings.LastIndexByte(path, '.'); i > 0 {
				n = byPath[path[:i]]
			}
		}
		if n == nil || !s.kept(n) || s.hidden(n) {
			continue
		}
		var feature *resolve.Node
		for x := n.Parent; x != nil && feature == nil; x = x.Parent {
			if s.kindPath(x) == "spec.feature" {
				feature = x
			}
		}
		key := ""
		g := gapGroup{Feature: feature}
		if feature != nil {
			key = feature.Path()
		} else {
			segs := strings.SplitN(path, ".", 3)
			g.Area = segs[0]
			if len(segs) == 3 {
				g.Area = segs[0] + "." + segs[1]
			}
			key = "area:" + g.Area
		}
		i, ok := at[key]
		if !ok {
			i = len(out)
			at[key] = i
			out = append(out, g)
		}
		out[i].Items = append(out[i].Items, gapItem{Path: path, Node: n})
	}
	return out
}

// gapsPage writes the gap report: one list per gap, its groups folded,
// each with its count, and each entry linked.
func (s *Site) gapsPage(c *Ctx) error {
	if len(s.gaps) == 0 {
		return nil
	}
	c.Heading(2, "gap-unrealized", label("docs.gaps.unrealized"), "")
	c.W(`<p>`, esc(label("docs.gaps.unrealized_note")), `</p>`)
	for _, g := range s.gaps {
		prefix := g.Area
		head := `<code>` + esc(g.Area) + `</code>`
		if g.Feature != nil {
			prefix = g.Feature.Path()
			head = esc(displayName(s, g.Feature))
		}
		c.W(`<details class="gap-group"><summary>`, head, ` <span class="gap-count">`, strconv.Itoa(len(g.Items)), `</span></summary><ul class="gap-list">`)
		for _, it := range g.Items {
			inner := `<code>` + esc(strings.TrimPrefix(it.Path, prefix+".")) + `</code>`
			c.W(`<li>`, s.linkHTML(it.Node, inner, "spec-ref"), `</li>`)
		}
		c.W(`</ul></details>`)
	}
	return nil
}
