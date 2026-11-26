package types

import (
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// A link's path, in `[name]`, ``[`name`]`` or `[text](#path)`, resolves
// as a path in code does, from the scope it's written in outward; where
// that names nothing, by suffix: to the nodes whose path ends with the
// link's names, name by name. The tree's own nodes come before those of
// the packages it imports, and two matches at one precedence are an
// ambiguity the author settles.

// linkIndex holds every named node a link can reach by suffix, by its
// last name, the tree's own apart from the imported packages'.
type linkIndex struct {
	own, imported map[string][]*resolve.Node
}

func (c *Checker) links() *linkIndex {
	if c.linkIdx != nil {
		return c.linkIdx
	}
	ix := &linkIndex{own: map[string][]*resolve.Node{}, imported: map[string][]*resolve.Node{}}
	var walk func(n *resolve.Node, into map[string][]*resolve.Node, own bool)
	walk = func(n *resolve.Node, into map[string][]*resolve.Node, own bool) {
		for _, ch := range n.Children {
			if ch.Shape == resolve.ShapeMounted || ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue {
				// A mounted node is its package's, indexed there; a field
				// is a value, not a node a link names.
				continue
			}
			if ch.Decl.File != nil && ch.Decl.File.ID.IsStd() && own {
				continue
			}
			if ch.Name != "" {
				into[ch.Name] = append(into[ch.Name], ch)
			}
			walk(ch, into, own)
		}
	}
	walk(c.Tree.Root, ix.own, true)
	seen := map[*resolve.Tree]bool{c.Tree: true}
	for _, p := range c.Tree.Imports {
		if p.Tree == nil || seen[p.Tree] {
			continue
		}
		seen[p.Tree] = true
		walk(p.Tree.Root, ix.imported, false)
	}
	c.linkIdx = ix
	return ix
}

// segsOf is a node's path as names.
func segsOf(n *resolve.Node) []string { return strings.Split(n.Path(), ".") }

// endsWith reports whether a node's path ends with the given names.
func endsWith(n *resolve.Node, segs []string) bool {
	p := segsOf(n)
	if len(p) < len(segs) {
		return false
	}
	for i := range segs {
		if p[len(p)-len(segs)+i] != segs[i] {
			return false
		}
	}
	return true
}

// suffixMatches are the nodes whose path ends with the names, the tree's
// own if it has any, else the imported packages'.
func (c *Checker) suffixMatches(segs []string) []*resolve.Node {
	ix := c.links()
	last := segs[len(segs)-1]
	for _, m := range []map[string][]*resolve.Node{ix.own, ix.imported} {
		var out []*resolve.Node
		for _, n := range m[last] {
			if endsWith(n, segs) {
				out = append(out, n)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// shortestEnd is the shortest end of a node's path that matches it alone
// among the nodes of its last name.
func (c *Checker) shortestEnd(n *resolve.Node) string {
	ix := c.links()
	p := segsOf(n)
	pool := append(append([]*resolve.Node(nil), ix.own[n.Name]...), ix.imported[n.Name]...)
	for k := 1; k <= len(p); k++ {
		end := p[len(p)-k:]
		count := 0
		for _, o := range pool {
			if endsWith(o, end) {
				count++
			}
		}
		if count <= 1 {
			return strings.Join(end, ".")
		}
	}
	return strings.Join(p, ".")
}

// linkTarget resolves a link's path: lexically, then by suffix. It
// returns the node, or the candidates when two or more match.
func (c *Checker) linkTarget(sc *resolve.Scope, file *resolve.File, segs []string) (*resolve.Node, []*resolve.Node) {
	if len(segs) == 0 {
		return nil, nil
	}
	if t := c.lookupPath(sc, file, -1, segs, nil); t != nil {
		return t, nil
	}
	ms := c.suffixMatches(segs)
	if len(ms) == 1 {
		return ms[0], nil
	}
	return nil, ms
}

// checkLinkPath resolves the path of a link and records it, or reports
// the ambiguity or, through unresolved, that it names nothing.
func (c *Checker) checkLinkPath(path *syntax.Node, sc *resolve.Scope, file *resolve.File, unresolved func(segs []string, hint string)) {
	segs := resolve.PathSegs(path)
	if len(segs) == 0 {
		return
	}
	t, cands := c.linkTarget(sc, file, segs)
	switch {
	case t != nil:
		c.Refs[path] = t
	case len(cands) > 1:
		c.ambiguousLink(path, file, segs, cands)
	default:
		// The closest name a link could reach, the tree's own first, then
		// the names in scope.
		var names []string
		for k := range c.links().own {
			names = append(names, k)
		}
		hint := suggest(segs[len(segs)-1], names)
		if hint == "" {
			hint = c.closest(sc, segs[0])
		}
		unresolved(segs, hint)
	}
}

// ambiguousLink reports a link whose path matches two or more nodes,
// with one suggested fix per candidate writing the end that picks it.
func (c *Checker) ambiguousLink(path *syntax.Node, file *resolve.File, segs []string, cands []*resolve.Node) {
	if file == nil {
		return
	}
	ends := make([]string, len(cands))
	for i, n := range cands {
		ends[i] = c.shortestEnd(n)
	}
	sort.Strings(ends)
	s, e := spanOf(path)
	d := diag.Diagnostic{File: string(file.ID), Start: s, End: e, Severity: diag.Error, Code: "ambiguous",
		Message: strings.Join(segs, ".") + " names " + itoa(len(cands)) + " nodes: " + strings.Join(ends, ", ") + "; write a longer path"}
	for _, end := range ends {
		d.Fixes = append(d.Fixes, diag.Fix{Title: "Link to " + end, Edits: []diag.Edit{{File: string(file.ID), Start: s, End: e, New: end}}, Suggestion: true})
	}
	c.diags.Add(d)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// shortLink checks `[name]` or “[`name`]“: its path must name one node.
func (c *Checker) shortLink(l ast.ShortLink, sc *resolve.Scope, file *resolve.File) {
	p := l.Path().N
	if p == nil {
		return
	}
	c.checkLinkPath(p, sc, file, func(segs []string, hint string) {
		c.errorf(file, p, "unresolved", "[%s] names no node%s", strings.Join(segs, "."), hint)
	})
}
