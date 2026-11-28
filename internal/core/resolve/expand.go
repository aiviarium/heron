package resolve

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// Expansion is what a file of produced source stands for: the invocation
// in structure that a macro expanded, as `resource_ops(name = "category")`
// in an api body.
type Expansion struct {
	// Site is the invocation: errors in the produced source are reported
	// there.
	Site Site
	// Node is the invocation's node, which leaves the tree for what it
	// produced; Macro the macro it calls.
	Node, Macro *Node
}

// Expanded is one invocation's expansion: the source the macro produced,
// or why it produced none.
type Expanded struct {
	Inv, Macro *Node
	Src        string
	Err        error
}

// maxExpansionDepth bounds macros that produce invocations of macros.
const maxExpansionDepth = 32

// expand adds what each macro invocation in the tree's bodies produces to
// the body that invokes it, parsed as if it had been written there, at the
// invocation's place, before anything checks the body. Produced source may
// invoke macros in turn; those expand next.
func (b *builder) expand() {
	if b.r.Expand == nil {
		return
	}
	tried := map[*Node]bool{}
	for depth := 0; ; depth++ {
		var invs []*Node
		b.t.Walk(func(n *Node) {
			if n.Shape == ShapeAnon && n.Decl.N != nil && n.Decl.N.Kind == syntax.Invoke && !tried[n] {
				tried[n] = true
				invs = append(invs, n)
			}
		})
		if len(invs) == 0 {
			return
		}
		out := b.r.Expand(b.t, invs)
		if len(out) == 0 {
			return
		}
		if depth == maxExpansionDepth {
			e := out[0]
			b.errAt(e.Inv.Decl, "macro", "macro %s produces invocations of macros more than %d deep", e.Macro.Name, maxExpansionDepth)
			return
		}
		for _, e := range out {
			b.expandOne(e)
		}
	}
}

func (b *builder) expandOne(e Expanded) {
	inv := e.Inv
	if e.Err != nil {
		b.errAt(inv.Decl, "macro", "macro %s: %v", e.Macro.Name, e.Err)
		return
	}
	start, _ := inv.Decl.Span()
	id := FileID(fmt.Sprintf("%s@%d:%s", inv.Decl.File.ID, start, e.Macro.Name))
	f := &File{ID: id, Src: e.Src, Hash: sha256.Sum256([]byte(e.Src)), Tree: syntax.Parse(string(id), e.Src)}
	f.Expansion = &Expansion{Site: inv.Decl, Node: inv, Macro: e.Macro}
	if b.t.expansions == nil {
		b.t.expansions = map[FileID]*File{}
	}
	b.t.expansions[id] = f
	b.t.Expansions = append(b.t.Expansions, f)
	for _, d := range f.Tree.Diags {
		b.t.Diags.Errorf(string(id), d.Start, d.End, "syntax", "%s", d.Message)
	}
	target := inv.Parent
	sc := &Scope{Parent: inv.Decl.Scope, Node: target, File: f}
	b.t.scopes[id] = sc
	before, prose := len(target.Children), len(target.Prose)
	b.members(f, ast.File{N: f.Tree.Root}.Members(), target, sc)
	// What it produced takes the invocation's place: the nodes, and the
	// text among the node's own.
	added := append([]*Node(nil), target.Children[before:]...)
	kept := target.Children[:0:0]
	for _, c := range target.Children[:before] {
		if c == inv {
			kept = append(kept, added...)
			continue
		}
		kept = append(kept, c)
	}
	target.Children = kept
	b.t.Invocations = append(b.t.Invocations, inv)
	if text := target.Prose[prose:]; len(text) > 0 {
		text = append([]Site(nil), text...)
		at := prose
		for i, p := range target.Prose[:prose] {
			if p.File == inv.Decl.File && p.N != nil {
				if s, _ := p.Span(); s > start {
					at = i
					break
				}
			}
		}
		rest := append([]Site(nil), target.Prose[at:prose]...)
		target.Prose = append(append(target.Prose[:at:at], text...), rest...)
	}
}

// Holder returns the site in a file that stands for a site: for one in
// source a macro produced, the invocation that produced it, through every
// level of expansion; for any other, the site itself.
func (s Site) Holder() Site {
	for s.File != nil && s.File.Expansion != nil {
		s = s.File.Expansion.Site
	}
	return s
}

// Expansion returns the file of produced source an identity names, in the
// tree or a package it imports, or nil when the identity names a file of
// its own.
func (t *Tree) Expansion(id FileID) *File {
	if !strings.Contains(string(id), "@") {
		return nil
	}
	seen := map[*Tree]bool{}
	var find func(*Tree) *File
	find = func(t *Tree) *File {
		if t == nil || seen[t] {
			return nil
		}
		seen[t] = true
		if f := t.expansions[id]; f != nil {
			return f
		}
		for _, p := range t.Imports {
			if f := find(p.Tree); f != nil {
				return f
			}
		}
		return nil
	}
	return find(t)
}

// Invocation maps a place in produced source to the invocation that
// produced it, in a file of its own: the place itself when it is in one
// already, and the produced source's lines on the way, innermost first.
func (t *Tree) Invocation(file string, start, end int) (string, int, int, []int) {
	var lines []int
	for {
		f := t.Expansion(FileID(file))
		if f == nil {
			return file, start, end, lines
		}
		line, _ := syntax.LineCol(f.Src, start)
		lines = append(lines, line)
		s := f.Expansion.Site
		file = string(s.File.ID)
		start, end = s.Span()
		if p := calleeSpan(s); p != nil {
			start, end = p[0], p[1]
		}
	}
}

func calleeSpan(s Site) []int {
	if s.N == nil {
		return nil
	}
	for _, ch := range s.N.Children {
		if ch, ok := ch.(*syntax.Node); ok && ch.Kind == syntax.Path {
			a, z := ch.Span()
			return []int{a, z}
		}
	}
	return nil
}

// MapDiags reports what lands in produced source at the invocation that
// produced it, with the line of the produced source it came from, as the
// checker's expansion_errors rule says.
func (t *Tree) MapDiags(ds diag.List) diag.List {
	for i, d := range ds {
		f := t.Expansion(FileID(d.File))
		if f == nil {
			continue
		}
		name := f.Expansion.Macro.Name
		file, start, end, lines := t.Invocation(d.File, d.Start, d.End)
		d.File, d.Start, d.End = file, start, end
		d.Message = fmt.Sprintf("%s (line %d of what %s produced)", d.Message, lines[0], name)
		for j, r := range d.Related {
			r.File, r.Start, r.End, _ = t.Invocation(r.File, r.Start, r.End)
			d.Related[j] = r
		}
		var fixes []diag.Fix
		for _, fx := range d.Fixes {
			ok := true
			for _, e := range fx.Edits {
				if t.Expansion(FileID(e.File)) != nil {
					ok = false
				}
			}
			if ok {
				fixes = append(fixes, fx)
			}
		}
		d.Fixes = fixes
		ds[i] = d
	}
	return ds
}
