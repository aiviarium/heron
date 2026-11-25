// Package syntax lexes and parses Heron into a lossless syntax tree.
//
// Every byte of the input belongs to exactly one token, and printing the
// tokens in order reproduces the input, even when the input has errors. The
// parser switches modes for `|` lines, `{{ }}` prose bodies, `do` code and
// strings, and recovers from errors so a half-written file still has a tree.
//
// The package does no I/O.
package syntax

import "strings"

//go:generate go run ../ast/internal/astgen -grammar ../../../grammar/heron.ungram -syntax kinds_gen.go -ast ../ast/nodes_gen.go

// Kind is the kind of a node or a token. Token kinds come first.
type Kind uint16

// String returns the kind's name from the grammar.
func (k Kind) String() string {
	if int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return "Kind(?)"
}

// IsToken reports whether k is a token kind.
func (k Kind) IsToken() bool { return k > KindNone && k < firstNodeKind }

// IsTrivia reports whether tokens of kind k are layout or comments.
func (k Kind) IsTrivia() bool { return k == Whitespace || k == Newline || k == Comment }

// Elem is a node or a token.
type Elem interface {
	ElemKind() Kind
	Span() (start, end int)
}

// Token is a leaf of the tree: its kind, its exact text and its offset.
type Token struct {
	Kind  Kind
	Text  string
	Start int
}

// ElemKind returns the token's kind.
func (t *Token) ElemKind() Kind { return t.Kind }

// Span returns the token's byte range.
func (t *Token) Span() (int, int) { return t.Start, t.Start + len(t.Text) }

// End returns the offset just past the token.
func (t *Token) End() int { return t.Start + len(t.Text) }

// Node is an interior node of the tree.
type Node struct {
	Kind     Kind
	Children []Elem
	Parent   *Node
	start    int
	end      int
}

// ElemKind returns the node's kind.
func (n *Node) ElemKind() Kind { return n.Kind }

// Span returns the node's byte range.
func (n *Node) Span() (int, int) { return n.start, n.end }

// Text returns the source text the node covers.
func (n *Node) Text() string {
	var b strings.Builder
	n.write(&b)
	return b.String()
}

func (n *Node) write(b *strings.Builder) {
	for _, c := range n.Children {
		switch c := c.(type) {
		case *Token:
			b.WriteString(c.Text)
		case *Node:
			c.write(b)
		}
	}
}

// Tokens calls f for every token under n, in order.
func (n *Node) Tokens(f func(*Token)) {
	for _, c := range n.Children {
		switch c := c.(type) {
		case *Token:
			f(c)
		case *Node:
			c.Tokens(f)
		}
	}
}

// Walk calls f for n and every node under it, in pre-order. If f returns
// false the node's children are skipped.
func (n *Node) Walk(f func(*Node) bool) {
	if !f(n) {
		return
	}
	for _, c := range n.Children {
		if c, ok := c.(*Node); ok {
			c.Walk(f)
		}
	}
}

// Diagnostic is a syntax error with the byte range it concerns.
type Diagnostic struct {
	Start, End int
	Message    string
}

// Tree is a parsed file.
type Tree struct {
	Name  string
	Src   string
	Root  *Node
	Diags []Diagnostic
}

// finish sets parents and spans after parsing.
func finish(n *Node, parent *Node, pos int) int {
	n.Parent = parent
	n.start = pos
	for _, c := range n.Children {
		switch c := c.(type) {
		case *Token:
			pos = c.End()
		case *Node:
			pos = finish(c, n, pos)
		}
	}
	n.end = pos
	return pos
}

// LineCol converts a byte offset to a 1-based line and column (in bytes).
func LineCol(src string, off int) (line, col int) {
	if off > len(src) {
		off = len(src)
	}
	line = 1 + strings.Count(src[:off], "\n")
	col = off - strings.LastIndexByte(src[:off], '\n')
	return line, col
}

// FenceLines returns the lines of a fenced code block, its opening line
// first, with an empty string for each `|` line that holds nothing.
func FenceLines(n *Node) []string {
	var lines []string
	open := false
	for _, ch := range n.Children {
		t, ok := ch.(*Token)
		if !ok {
			continue
		}
		switch t.Kind {
		case Pipe:
			lines = append(lines, "")
			open = true
		case FenceLine:
			if !open {
				lines = append(lines, "")
			}
			lines[len(lines)-1] = t.Text
			open = false
		}
	}
	return lines
}
