// Package ast gives typed views over the syntax tree, one per node kind,
// generated from grammar/heron.ungram so the kinds and the parser can't drift
// apart. Views are values wrapping a *syntax.Node; accessors return the zero
// view (with a nil N) or nil when a child is absent.
package ast

import "github.com/helightdev/specs-v0/internal/core/syntax"

// Node is any typed view.
type Node interface {
	Syntax() *syntax.Node
}

func kindIs(k syntax.Kind) func(syntax.Kind) bool {
	return func(x syntax.Kind) bool { return x == k }
}

// child returns the idx-th child node whose kind matches.
func child(n *syntax.Node, match func(syntax.Kind) bool, idx int) *syntax.Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c, ok := c.(*syntax.Node); ok && match(c.Kind) {
			if idx == 0 {
				return c
			}
			idx--
		}
	}
	return nil
}

func children(n *syntax.Node, match func(syntax.Kind) bool) []*syntax.Node {
	if n == nil {
		return nil
	}
	var out []*syntax.Node
	for _, c := range n.Children {
		if c, ok := c.(*syntax.Node); ok && match(c.Kind) {
			out = append(out, c)
		}
	}
	return out
}

func token(n *syntax.Node, k syntax.Kind, idx int) *syntax.Token {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if t, ok := c.(*syntax.Token); ok && t.Kind == k {
			if idx == 0 {
				return t
			}
			idx--
		}
	}
	return nil
}

func tokens(n *syntax.Node, k syntax.Kind) []*syntax.Token {
	if n == nil {
		return nil
	}
	var out []*syntax.Token
	for _, c := range n.Children {
		if t, ok := c.(*syntax.Token); ok && t.Kind == k {
			out = append(out, t)
		}
	}
	return out
}
