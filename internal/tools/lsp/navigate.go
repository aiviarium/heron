package lsp

import (
	"strings"

	"github.com/helightdev/specs-v0/internal/core/resolve"
	"github.com/helightdev/specs-v0/internal/core/syntax"
)

// tokenAt returns the token at an offset and its ancestors, innermost
// first. At the end of a word (the cursor just past it) the word counts.
func tokenAt(root *syntax.Node, off int) (*syntax.Token, []*syntax.Node) {
	tok, anc := tokenContaining(root, off)
	if (tok == nil || !wordToken(tok)) && off > 0 {
		if t2, a2 := tokenContaining(root, off-1); t2 != nil && wordToken(t2) {
			return t2, a2
		}
	}
	return tok, anc
}

func wordToken(t *syntax.Token) bool {
	switch t.Kind {
	case syntax.Ident, syntax.StringText, syntax.Text, syntax.HeadingId, syntax.Url:
		return true
	}
	return false
}

func tokenContaining(n *syntax.Node, off int) (*syntax.Token, []*syntax.Node) {
	var anc []*syntax.Node
	for {
		anc = append(anc, n)
		var next *syntax.Node
		for _, c := range n.Children {
			s, e := c.Span()
			if off < s || off >= e {
				continue
			}
			switch c := c.(type) {
			case *syntax.Token:
				reverse(anc)
				return c, anc
			case *syntax.Node:
				next = c
			}
			break
		}
		if next == nil {
			reverse(anc)
			return nil, anc
		}
		n = next
	}
}

func reverse(l []*syntax.Node) {
	for i, j := 0, len(l)-1; i < j; i, j = i+1, j-1 {
		l[i], l[j] = l[j], l[i]
	}
}

// pathIdents returns the identifier tokens of a path, one per segment.
func pathIdents(p *syntax.Node) []*syntax.Token {
	var out []*syntax.Token
	for _, c := range p.Children {
		if t, ok := c.(*syntax.Token); ok && t.Kind == syntax.Ident {
			out = append(out, t)
		}
	}
	return out
}

func childOf(n *syntax.Node, k syntax.Kind) *syntax.Node {
	for _, c := range n.Children {
		if c, ok := c.(*syntax.Node); ok && c.Kind == k {
			return c
		}
	}
	return nil
}

// ascend walks up from the node a whole path names to the node segment i
// of n names.
func ascend(n *resolve.Node, i, n0 int) *resolve.Node {
	for k := n0 - 1; k > i && n != nil; k-- {
		n = n.Parent
	}
	return n
}

func same(a, b *resolve.Node) bool {
	if a == nil || b == nil {
		return false
	}
	return a == b || a.Origin == b || b.Origin == a || (a.Origin != nil && a.Origin == b.Origin)
}

// isMember reports whether a syntax node is a member that declares,
// enters or amends a node by its path.
func isMember(k syntax.Kind) bool {
	switch k {
	case syntax.Declare, syntax.Value, syntax.Enter, syntax.TextNode, syntax.Scope, syntax.Set:
		return true
	}
	return false
}

// sites maps the members of a result's trees to the nodes they declare or
// amend.
func (r *result) sites() map[*syntax.Node]*resolve.Node {
	if r.siteMap != nil {
		return r.siteMap
	}
	m := map[*syntax.Node]*resolve.Node{}
	for _, t := range r.trees() {
		t.Walk(func(n *resolve.Node) {
			if n.Decl.N != nil && n.Shape != resolve.ShapeMounted {
				m[n.Decl.N] = n
			}
			for _, s := range n.Sites {
				if s.N != nil {
					m[s.N] = n
				}
			}
		})
	}
	r.siteMap = m
	return m
}

// trees returns the result's tree and the trees of every package it
// imports, directly or not.
func (r *result) trees() []*resolve.Tree {
	out := []*resolve.Tree{r.res.Tree}
	seen := map[*resolve.Tree]bool{r.res.Tree: true}
	for i := 0; i < len(out); i++ {
		for _, p := range out[i].Imports {
			if p.Tree != nil && !seen[p.Tree] {
				seen[p.Tree] = true
				out = append(out, p.Tree)
			}
		}
	}
	return out
}

// target is what a position names.
type target struct {
	node  *resolve.Node
	file  resolve.FileID // an import or include string names a file
	tok   *syntax.Token  // the word at the position
	local *resolve.Local
}

// targetAt finds what the word at an offset of a file names: a node
// through a path, a link, an insert or a declaration, or a file through
// an import or include string.
func (s *Session) targetAt(id resolve.FileID, off int) (target, *result) {
	r := s.resultFor(id)
	f := s.file(id)
	if f == nil {
		return target{}, r
	}
	tok, anc := tokenAt(f.Tree.Root, off)
	if tok == nil {
		return target{}, r
	}
	res := r.completed()
	for _, a := range anc {
		if a.Kind == syntax.StringLit {
			for _, t := range r.trees() {
				if fid, ok := t.Targets[a]; ok {
					return target{file: fid, tok: tok}, r
				}
			}
		}
	}
	if tok.Kind != syntax.Ident {
		return target{tok: tok}, r
	}
	for i, a := range anc {
		if a.Kind != syntax.Path {
			continue
		}
		ids := pathIdents(a)
		idx := -1
		for k, t := range ids {
			if t == tok {
				idx = k
			}
		}
		if idx < 0 {
			break
		}
		if n := res.Checker.Refs[a]; n != nil {
			return target{node: ascend(n, idx, len(ids)), tok: tok}, r
		}
		if i+1 < len(anc) && isMember(anc[i+1].Kind) {
			if n := r.sites()[anc[i+1]]; n != nil {
				return target{node: ascend(n, idx, len(ids)), tok: tok}, r
			}
		}
		if len(ids) == 1 {
			if l := s.localAt(r, f, anc, tok); l != nil {
				return target{local: l, tok: tok}, r
			}
		}
		break
	}
	// A `let` or parameter name where it's declared.
	if len(anc) > 0 && (anc[0].Kind == syntax.Let || anc[0].Kind == syntax.Param || anc[0].Kind == syntax.LetStmt) {
		if l := s.localAt(r, f, anc, tok); l != nil {
			return target{local: l, tok: tok}, r
		}
	}
	return target{tok: tok}, r
}

// localAt finds the local a name refers to from the innermost body scope
// around it.
func (s *Session) localAt(r *result, f *resolve.File, anc []*syntax.Node, tok *syntax.Token) *resolve.Local {
	sc := s.scopeAt(r, f, anc)
	if sc == nil {
		return nil
	}
	b := sc.Lookup(tok.Text, f, -1)
	return b.Local
}

// scopeAt returns the scope of the innermost member around a position
// whose node has a body, or the file's scope.
func (s *Session) scopeAt(r *result, f *resolve.File, anc []*syntax.Node) *resolve.Scope {
	sites := r.sites()
	for _, a := range anc {
		if n := sites[a]; n != nil && n.Body != nil && n.Body.File == f {
			return n.Body
		}
	}
	for _, t := range r.trees() {
		if sc := t.FileScope(f.ID); sc != nil {
			return sc
		}
	}
	return nil
}

// declSpan is where a node is declared: its declaring member's path, or
// the member itself.
func declSpan(n *resolve.Node) (resolve.Site, int, int) {
	site := n.Decl
	if site.N == nil && len(n.Sites) > 0 {
		site = n.Sites[0]
	}
	if site.N == nil || site.File == nil {
		return resolve.Site{}, 0, 0
	}
	if p := childOf(site.N, syntax.Path); p != nil {
		if ids := pathIdents(p); len(ids) > 0 {
			last := ids[len(ids)-1]
			s, e := last.Span()
			return site, s, e
		}
	}
	if id := childOf(site.N, syntax.HeadingId); id != nil {
		s, e := id.Span()
		return site, s, e
	}
	s, e := site.Span()
	return site, s, e
}

// signature renders a node's declaration without its docs and body.
func signature(n *resolve.Node) string {
	site := n.Decl
	if site.N == nil {
		return n.Name
	}
	var b strings.Builder
	for _, c := range site.N.Children {
		switch c := c.(type) {
		case *syntax.Node:
			switch c.Kind {
			case syntax.Doc:
				continue
			case syntax.StructBody, syntax.ProseBlock, syntax.DoBlock:
				goto done
			}
			b.WriteString(c.Text())
		case *syntax.Token:
			b.WriteString(c.Text)
		}
	}
done:
	out := strings.TrimSpace(b.String())
	lines := strings.Split(out, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	out = strings.Join(lines, "\n")
	if len(out) > 400 {
		out = out[:400] + " …"
	}
	return out
}

// docText returns the `///` docs of a node from every site, as Markdown.
func docText(n *resolve.Node) string {
	var paras []string
	for _, d := range n.Docs {
		if d.N == nil {
			continue
		}
		var ls []string
		for _, line := range strings.Split(d.N.Text(), "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "///")
			line = strings.TrimPrefix(line, " ")
			ls = append(ls, line)
		}
		if t := strings.TrimSpace(strings.Join(ls, "\n")); t != "" {
			paras = append(paras, t)
		}
	}
	if len(paras) == 0 && n.Origin != nil {
		return docText(n.Origin)
	}
	return strings.Join(paras, "\n\n")
}
