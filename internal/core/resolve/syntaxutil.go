package resolve

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/syntax"
)

// PathSegs returns the segments of a Path node: names, and the values of
// quoted segments such as "std/ci".
func PathSegs(p *syntax.Node) []string {
	if p == nil {
		return nil
	}
	var out []string
	for _, c := range p.Children {
		switch c := c.(type) {
		case *syntax.Token:
			if c.Kind == syntax.Ident {
				out = append(out, c.Text)
			}
		case *syntax.Node:
			if c.Kind == syntax.StringLit {
				s, _ := StringValue(c)
				out = append(out, s)
			}
		}
	}
	return out
}

// PathText returns a path as written with dots, such as "types.op".
func PathText(p *syntax.Node) string { return strings.Join(PathSegs(p), ".") }

// StringValue returns the text of a string literal or raw string. ok is
// false when it interpolates, since its value is only known when evaluated.
func StringValue(n *syntax.Node) (s string, ok bool) {
	if n == nil {
		return "", false
	}
	if n.Kind == syntax.Literal {
		for _, c := range n.Children {
			if t, isTok := c.(*syntax.Token); isTok && t.Kind == syntax.Raw {
				return RawValue(t.Text), true
			}
		}
		return "", false
	}
	if n.Kind != syntax.StringLit {
		return "", false
	}
	var b strings.Builder
	ok = true
	for _, c := range n.Children {
		switch c := c.(type) {
		case *syntax.Token:
			switch c.Kind {
			case syntax.StringText:
				b.WriteString(c.Text)
			case syntax.Escape:
				b.WriteString(unescape(c.Text))
			}
		case *syntax.Node:
			ok = false
		}
	}
	return b.String(), ok
}

// RawValue strips the backtick run of a raw string.
func RawValue(text string) string {
	k := 0
	for k < len(text) && text[k] == '`' {
		k++
	}
	if len(text) < 2*k {
		return ""
	}
	return text[k : len(text)-k]
}

func unescape(e string) string {
	if len(e) < 2 {
		return e
	}
	switch e[1] {
	case 'n':
		return "\n"
	case 't':
		return "\t"
	case 'r':
		return "\r"
	case '0':
		return "\x00"
	}
	return e[1:]
}

// firstToken returns the first non-trivia token under n.
func firstToken(n *syntax.Node) *syntax.Token {
	var out *syntax.Token
	n.Tokens(func(t *syntax.Token) {
		if out == nil && !t.Kind.IsTrivia() {
			out = t
		}
	})
	return out
}

// childNode returns the first child of n with kind k.
func childNode(n *syntax.Node, k syntax.Kind) *syntax.Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c, ok := c.(*syntax.Node); ok && c.Kind == k {
			return c
		}
	}
	return nil
}
