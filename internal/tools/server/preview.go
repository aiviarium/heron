package server

import (
	"fmt"
	"html"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/check"
	"github.com/helightdev/specs-v0/internal/core/resolve"
	"github.com/helightdev/specs-v0/internal/tools/server/api"
)

func resolveID(p string) resolve.FileID {
	id, err := resolve.Canonical(p)
	if err != nil {
		return resolve.FileID(p)
	}
	return id
}

// declaredIn reports whether a node is declared in a file, at what span.
// A node only entered or amended in the file counts at its first site there.
func declaredIn(n *resolve.Node, file resolve.FileID) (int, int, bool) {
	for _, site := range append([]resolve.Site{n.Decl}, n.Sites...) {
		if site.File != nil && site.File.ID == file && site.N != nil {
			s, e := site.Span()
			return s, e, true
		}
	}
	return 0, 0, false
}

// kindName names a node's kind for the preview and the outline.
func kindName(res *check.Result, n *resolve.Node) string {
	c := res.Checker
	if k := c.KindOf(n); k != nil {
		return k.Name
	}
	switch {
	case c.IsEnum(n):
		return "enum"
	case c.IsType(n):
		return "type"
	}
	return ""
}

// outline lists the nodes a file declares, in reading order.
func (a *Analysis) outline(file resolve.FileID) []api.OutlineNode {
	out := []api.OutlineNode{}
	var walk func(n *resolve.Node, depth int)
	walk = func(n *resolve.Node, depth int) {
		for _, c := range n.Children {
			if c.Name == "" {
				continue
			}
			s, e, ok := declaredIn(c, file)
			if ok {
				l, _ := a.Pos(string(file), s)
				el, _ := a.Pos(string(file), e)
				k := kindName(a.Res, c)
				out = append(out, api.OutlineNode{Path: c.Path(), Name: c.Name, Kind: k, Depth: int64(depth),
					Line: int64(l), EndLine: int64(el), Runnable: k == "task"})
			}
			// An example's fields are its values, not nodes to navigate.
			if depth < 6 && kindName(a.Res, c) != "example" {
				walk(c, depth+1)
			}
		}
	}
	walk(a.Res.Tree.Root, 0)
	// Declarations come in tree order; the outline reads in file order.
	sortByLine(out)
	return out
}

func sortByLine(ns []api.OutlineNode) {
	for i := 1; i < len(ns); i++ {
		for j := i; j > 0 && ns[j].Line < ns[j-1].Line; j-- {
			ns[j], ns[j-1] = ns[j-1], ns[j]
		}
	}
}

// nodeAt returns the innermost node with a kind declared at an offset.
func (a *Analysis) nodeAt(file resolve.FileID, off int) *resolve.Node {
	var best *resolve.Node
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, c := range n.Children {
			s, e, ok := declaredIn(c, file)
			if ok && s <= off && off <= e && kindName(a.Res, c) != "" {
				best = c
			}
			walk(c)
		}
	}
	walk(a.Res.Tree.Root)
	return best
}

// preview renders the node under the cursor, or the file's nodes when the
// cursor is on none. A failed render shows its error in place.
func (a *Analysis) preview(r Renderer, file resolve.FileID, off int) (out *api.Preview) {
	n := a.nodeAt(file, off)
	out = &api.Preview{Results: api.NodeResults{Results: []api.NodeResult{}}}
	defer func() {
		if p := recover(); p != nil {
			msg := fmt.Sprint(p)
			out.Error = &msg
		}
	}()
	if n == nil {
		if r != nil {
			h, err := r.RenderFile(a.Res, file)
			if err != nil {
				msg := err.Error()
				out.Error = &msg
			}
			out.HTML = h
			return out
		}
		var b strings.Builder
		for _, c := range a.Res.Tree.Root.Children {
			if _, _, ok := declaredIn(c, file); ok && c.Name != "" {
				b.WriteString(a.generic(c, 2))
			}
		}
		out.HTML = b.String()
		return out
	}
	out.Path, out.Kind = n.Path(), kindName(a.Res, n)
	out.Results.Path = out.Path
	out.HTML = a.render(r, n)
	return out
}

func (a *Analysis) render(r Renderer, n *resolve.Node) string {
	if r != nil {
		h, err := r.RenderNode(a.Res, n)
		if err != nil {
			panic(err.Error())
		}
		return h
	}
	return a.generic(n, 2)
}

// generic is the plain node view for a kind with no renderer: its name,
// kind, docs, prose and children, and nothing that isn't in the tree.
func (a *Analysis) generic(n *resolve.Node, level int) string {
	var b strings.Builder
	k := kindName(a.Res, n)
	fmt.Fprintf(&b, `<section class="h-node" data-node="%s" data-kind="%s">`, html.EscapeString(n.Path()), html.EscapeString(k))
	if level > 6 {
		level = 6
	}
	fmt.Fprintf(&b, `<h%d><code>%s</code>`, level, html.EscapeString(n.Name))
	if k != "" {
		fmt.Fprintf(&b, ` <span class="h-kind">%s</span>`, html.EscapeString(k))
	}
	fmt.Fprintf(&b, "</h%d>", level)
	for _, d := range n.Docs {
		for _, l := range strings.Split(d.N.Text(), "\n") {
			l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "///"))
			if l != "" {
				fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(l))
			}
		}
	}
	for _, p := range n.Prose {
		if t := proseText(p.N.Text()); t != "" {
			fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(t))
		}
	}
	if n.Value != nil {
		fmt.Fprintf(&b, `<pre class="h-value"><code>%s</code></pre>`, html.EscapeString(strings.TrimSpace(n.Value.Syntax().Text())))
	}
	for _, c := range n.Children {
		if c.Name == "" || c.Origin != nil {
			continue
		}
		b.WriteString(a.generic(c, level+1))
	}
	b.WriteString("</section>")
	return b.String()
}

// proseText strips the `|` and `{{ }}` markers of a prose site.
func proseText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "{{"), "}}")
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		l = strings.TrimSpace(strings.TrimPrefix(l, "|"))
		if l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, " ")
}
