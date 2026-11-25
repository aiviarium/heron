package extension

import (
	"strconv"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/ast"
	"github.com/helightdev/specs-v0/internal/core/extension/protocol"
	"github.com/helightdev/specs-v0/internal/core/resolve"
	"github.com/helightdev/specs-v0/internal/core/types"
)

// EncodeNode turns a node of the resolved tree into its protocol value,
// the shape `heron check --json` prints, to a depth (-1 for all of it).
func EncodeNode(c *types.Checker, n *resolve.Node, depth int) *protocol.Node {
	out := &protocol.Node{Path: n.Path(), Name: n.Name}
	if k := c.KindOf(n); k != nil {
		out.Kind = k.Path()
	} else if t := c.TypeOf(n); t != nil {
		out.Type = t.Path()
	}
	if n.Decl.File != nil && n.Decl.N != nil {
		out.File = string(n.Decl.File.ID)
		s, e := n.Decl.Span()
		out.Start, out.End = ptr(int64(s)), ptr(int64(e))
		line, _ := lineOf(n.Decl.File.Src, s)
		out.Line = ptr(int64(line))
	}
	out.Flags = resolve.NameFlags(n)
	for _, l := range [][]resolve.Site{n.Docs, n.Prose} {
		for _, s := range l {
			if s.N != nil {
				out.Prose = append(out.Prose, proseText(s.N.Text()))
			}
		}
	}
	if n.Value != nil && len(n.Children) == 0 {
		out.Value = Literal(n.Value)
	}
	if depth != 0 {
		for _, ch := range n.Children {
			out.Children = append(out.Children, *EncodeNode(c, ch, depth-1))
		}
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func lineOf(src string, off int) (int, int) {
	if off > len(src) {
		off = len(src)
	}
	line := 1 + strings.Count(src[:off], "\n")
	col := off - strings.LastIndexByte(src[:off], '\n')
	return line, col
}

// proseText strips the markers of doc lines and prose lines, keeping the
// words a lint reads.
func proseText(s string) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimSpace(l)
		for _, p := range []string{"///", "|", "{{", "}}"} {
			t = strings.TrimSpace(strings.TrimPrefix(t, p))
			t = strings.TrimSpace(strings.TrimSuffix(t, "}}"))
		}
		if t != "" {
			lines = append(lines, t)
		}
	}
	return strings.Join(lines, "\n")
}

// Literal returns a value where it's a literal, and its source text
// otherwise; evaluating it is the interpreter's work.
func Literal(e ast.Expr) protocol.Value {
	switch e := e.(type) {
	case ast.Literal:
		switch {
		case e.KwTrue() != nil:
			return true
		case e.KwFalse() != nil:
			return false
		case e.Raw() != nil:
			return resolve.RawValue(e.Raw().Text)
		case e.Number() != nil:
			if i, err := strconv.ParseInt(e.Number().Text, 10, 64); err == nil {
				return i
			}
			if f, err := strconv.ParseFloat(e.Number().Text, 64); err == nil {
				return f
			}
		}
	case ast.StringLit:
		if s, ok := resolve.StringValue(e.N); ok {
			return s
		}
	case ast.ArrayExpr:
		out := []any{}
		for _, x := range e.Exprs() {
			out = append(out, Literal(x))
		}
		return out
	default:
	}
	return map[string]any{"source": strings.TrimSpace(e.Syntax().Text())}
}

// ownNodes lists the nodes of a tree's own files, without the packages it
// mounts, each without its children, and indexes them by path.
func ownNodes(t *resolve.Tree, c *types.Checker) ([]protocol.Node, map[string]*resolve.Node) {
	var out []protocol.Node
	by := map[string]*resolve.Node{}
	t.Walk(func(n *resolve.Node) {
		if n.Parent == nil {
			return
		}
		if n.Shape == resolve.ShapeMounted && n.Origin != nil && n.Decl.N == n.Origin.Decl.N {
			return
		}
		e := EncodeNode(c, n, 0)
		out = append(out, *e)
		by[e.Path] = n
	})
	return out, by
}
