package docs

import (
	"github.com/helightdev/specs-v0/internal/core/resolve"
)

// taskNeeds returns the tasks among a node's children and the tasks each
// one's @needs names.
func (s *Site) taskNeeds(n *resolve.Node) ([]*resolve.Node, map[*resolve.Node][]*resolve.Node) {
	var tasks []*resolve.Node
	needs := map[*resolve.Node][]*resolve.Node{}
	for _, ch := range n.Children {
		if s.kindPath(ch) != "core.task" {
			continue
		}
		tasks = append(tasks, ch)
		for _, a := range annotationsNamed(ch, "needs") {
			for _, e := range argExprs(a.Args()) {
				if t := s.refTarget(e.Syntax(), ch.Decl); t != nil {
					needs[ch] = append(needs[ch], t)
				}
			}
		}
	}
	return tasks, needs
}

// dependencyGraph writes the tasks of a node and their @needs as a graph,
// with its table, when any task needs another.
func (c *Ctx) dependencyGraph(n *resolve.Node) {
	s := c.Site
	tasks, needs := s.taskNeeds(n)
	if len(needs) == 0 {
		return
	}
	g := &graph{}
	byNode := map[*resolve.Node]*gnode{}
	node := func(t *resolve.Node) *gnode {
		if x := byNode[t]; x != nil {
			return x
		}
		x := &gnode{id: t.Path(), label: t.Name, href: s.URL(t)}
		byNode[t] = x
		g.nodes = append(g.nodes, x)
		return x
	}
	for _, t := range tasks {
		node(t)
	}
	hasNeeds := map[*resolve.Node]bool{}
	for t, ns := range needs {
		for _, d := range ns {
			// An edge runs from what's needed to what needs it, in the
			// order they run.
			g.edges = append(g.edges, &gedge{from: node(d), to: node(t)})
			hasNeeds[t] = true
		}
	}
	var roots []*gnode
	for _, t := range tasks {
		if !hasNeeds[t] {
			roots = append(roots, byNode[t])
		}
	}
	w, h := g.layout(roots)
	id := Anchor(n) + ".needs"
	c.Island("diagram")
	c.W(`<div class="diagram-view" data-island="diagram-view" id="`, attr(id), `">`)
	c.W(`<div class="seg" role="tablist" aria-label="`, attr(label("docs.label.view")), `">`)
	c.W(`<button type="button" role="tab" aria-selected="true" data-view="diagram">`, esc(label("docs.action.diagram")), `</button>`)
	c.W(`<button type="button" role="tab" aria-selected="false" data-view="table">`, esc(label("docs.action.table")), `</button></div>`)
	c.W(`<div class="diagram" data-pane="diagram" data-island="diagram">`, g.svg(id, displayName(s, n), w, h), `</div>`)
	c.W(`<div class="table-wrap" data-pane="table" hidden><table><thead><tr><th>`, esc(label("docs.col.task")), `</th><th>`, esc(label("docs.col.needs")), `</th></tr></thead><tbody>`)
	for _, t := range tasks {
		c.W(`<tr><td>`, s.linkHTML(t, `<code>`+esc(t.Name)+`</code>`, ""), `</td><td>`)
		for i, d := range needs[t] {
			if i > 0 {
				c.W(", ")
			}
			c.W(s.linkHTML(d, `<code>`+esc(d.Name)+`</code>`, ""))
		}
		c.W(`</td></tr>`)
	}
	c.W(`</tbody></table></div></div>`)
}
