package syntax

import (
	"fmt"
	"strings"
)

// Dump returns an indented outline of the tree, for tests and debugging.
func Dump(n *Node) string {
	var b strings.Builder
	dump(&b, n, 0)
	return b.String()
}

func dump(b *strings.Builder, n *Node, depth int) {
	fmt.Fprintf(b, "%s%s\n", strings.Repeat("  ", depth), n.Kind)
	for _, c := range n.Children {
		switch c := c.(type) {
		case *Node:
			dump(b, c, depth+1)
		case *Token:
			if c.Kind == Whitespace || c.Kind == Newline {
				continue
			}
			fmt.Fprintf(b, "%s%s %q\n", strings.Repeat("  ", depth+1), c.Kind, c.Text)
		}
	}
}
