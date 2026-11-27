package resolve

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/syntax"
)

// Kind elements in prose: an element with an `id` attribute declares a
// node named by it, of the element's kind, a child of the node whose text
// or body holds it. Inside a named element's body, each element on lines
// of its own is a member, named by its id or anonymous. Any other element
// is content of its text, which renders in place and declares no node.

// prose adds a text to a node and declares the nodes its elements name.
func (b *builder) prose(n *Node, site Site) {
	if site.N == nil {
		n.Prose = append(n.Prose, site)
		return
	}
	// A prose file's item is an inline form itself, an element included.
	b.elements(site.File, site.Scope, &syntax.Node{Kind: syntax.ProseLine, Children: []syntax.Elem{site.N}}, n, false)
	if b.t.Elements[site.N] != nil {
		// The item is a node of its own, not text of n.
		return
	}
	n.Prose = append(n.Prose, site)
}

// elements walks a text's inline forms for elements that declare nodes.
// members is set inside a named element's body, where an element on
// lines of its own is a member even without an id.
func (b *builder) elements(f *File, sc *Scope, container *syntax.Node, target *Node, members bool) {
	if container == nil {
		return
	}
	for _, ch := range container.Children {
		x, ok := ch.(*syntax.Node)
		if !ok {
			continue
		}
		switch x.Kind {
		case syntax.Tag:
			id := TagAttrString(x, "id")
			if id == "" && !(members && ownLines(f, x)) {
				// Content of the text; a named element inside it is still
				// the text's node's.
				b.elements(f, sc, x, target, false)
				continue
			}
			site := Site{File: f, N: x, Scope: sc}
			var n *Node
			if id != "" {
				n = b.declare(site, []string{id}, target, ShapeElement)
			} else {
				n = &Node{Shape: ShapeElement, Decl: site}
				target.add(n)
			}
			if n == nil {
				continue
			}
			b.t.Elements[x] = n
			body := &Scope{Parent: sc, Node: n, File: f}
			n.Body = body
			n.Annotations = append(n.Annotations, ElementAnnotations(f, x, sc, nil)...)
			n.Prose = append(n.Prose, Site{File: f, N: x, Scope: body})
			b.elements(f, body, x, n, true)
		case syntax.ProseLine, syntax.ProseLines, syntax.ProseBlock, syntax.DocLine:
			b.elements(f, sc, x, target, members)
		}
	}
}

// TagAttrString is the value of an element's quoted attribute, or "".
func TagAttrString(tag *syntax.Node, name string) string {
	for _, ch := range tag.Children {
		a, ok := ch.(*syntax.Node)
		if !ok || a.Kind != syntax.TagAttr {
			continue
		}
		at, key := false, ""
		var val *syntax.Node
		for _, c := range a.Children {
			switch c := c.(type) {
			case *syntax.Token:
				switch {
				case c.Kind == syntax.At:
					at = true
				case c.Kind == syntax.Ident && key == "":
					key = c.Text
				}
			case *syntax.Node:
				if c.Kind == syntax.StringLit {
					val = c
				}
			}
		}
		if !at && key == name && val != nil {
			s, _ := StringValue(val)
			return s
		}
	}
	return ""
}

// ownLines reports whether an element stands on lines of its own: only
// indentation and a line prefix before it, and nothing after it.
func ownLines(f *File, tag *syntax.Node) bool {
	if f == nil {
		return false
	}
	s, e := tag.Span()
	src := f.Src
	ls := strings.LastIndexByte(src[:s], '\n') + 1
	before := strings.TrimSpace(src[ls:s])
	before = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(before, "///"), "|"))
	le := strings.IndexByte(src[e:], '\n')
	if le < 0 {
		le = len(src) - e
	}
	after := strings.TrimSpace(src[e : e+le])
	return before == "" && (after == "" || after == "}}")
}

// ElementAnnotations reads an element's `@name="args"` and `@name={args}`
// attributes as the annotations `@name(args)`, parsed apart and placed at
// the attributes' text. A quoted value is one string argument, unless raw
// says the annotation's arguments are read as written, as `@lens="user"`.
func ElementAnnotations(f *File, tag *syntax.Node, sc *Scope, raw func(name string) bool) []Site {
	return ElementAnnotationsWith(f, tag, sc, raw, nil)
}

// ElementAnnotationsWith is ElementAnnotations where rewrite may restate
// the arguments read as written, as a reference that a link would reach
// spelled as a path in scope.
func ElementAnnotationsWith(f *File, tag *syntax.Node, sc *Scope, raw func(name string) bool, rewrite func(name, args string) string) []Site {
	var out []Site
	for _, ch := range tag.Children {
		a, ok := ch.(*syntax.Node)
		if !ok || a.Kind != syntax.TagAttr {
			continue
		}
		at, name, args, argStart, quoted := false, "", "", -1, false
		for _, c := range a.Children {
			switch c := c.(type) {
			case *syntax.Token:
				switch {
				case c.Kind == syntax.At:
					at = true
				case c.Kind == syntax.Ident && name == "":
					name = c.Text
				}
			case *syntax.Node:
				if c.Kind == syntax.StringLit {
					st, _ := c.Span()
					args, argStart, quoted = strings.Trim(c.Text(), `"`), st+1, true
				} else if argStart < 0 {
					st, _ := c.Span()
					args, argStart = c.Text(), st
				}
			}
		}
		if !at || name == "" {
			continue
		}
		as, _ := a.Span()
		written := len(args)
		if quoted && (raw == nil || !raw(name)) {
			// One string argument, its quotes kept.
			args, argStart = `"`+args+`"`, argStart-1
			written = len(args)
		} else if rewrite != nil && argStart >= 0 {
			args = rewrite(name, args)
		}
		src := "@" + name
		if argStart >= 0 {
			src += "(" + args + ")"
		}
		head := len(src) - len(args) - 1
		if argStart < 0 {
			head = len(src)
		}
		t := syntax.Parse("element.heron", src+"\nx: y\n")
		var ann *syntax.Node
		t.Root.Walk(func(n *syntax.Node) bool {
			if ann == nil && n.Kind == syntax.Annotation {
				ann = n
			}
			return ann == nil
		})
		if ann == nil {
			continue
		}
		syntax.Shift(ann, func(o int) int {
			if argStart >= 0 && o >= head && o < head+len(args) {
				return argStart + min(o-head, max(written-1, 0))
			}
			return as
		})
		out = append(out, Site{File: f, N: ann, Scope: sc})
	}
	return out
}
