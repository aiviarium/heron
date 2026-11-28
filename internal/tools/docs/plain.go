package docs

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// kindAttrs returns the arguments a node's kind is written with, by
// parameter name, as plain values.
func (s *Site) kindAttrs(n *resolve.Node) map[string]any {
	args := kindArgs(n)
	if args.N == nil {
		return nil
	}
	names := s.paramNames(s.Checker.KindOf(n))
	out := map[string]any{}
	pos := 0
	for _, a := range args.Args() {
		switch a := a.(type) {
		case ast.PositionalArg:
			if pos < len(names) {
				out[names[pos]] = s.plainValue(a.Expr(), n.Decl)
			}
			pos++
		case ast.NamedArg:
			if a.Ident() != nil {
				out[a.Ident().Text] = s.plainValue(a.Expr(), n.Decl)
			}
		}
	}
	return out
}

// paramNames lists a kind's parameters in order.
func (s *Site) paramNames(k *resolve.Node) []string {
	var out []string
	for _, p := range s.kindParams(k) {
		if p.Ident() != nil {
			out = append(out, p.Ident().Text)
		}
	}
	return out
}

func (s *Site) kindParams(k *resolve.Node) []ast.Param {
	if k == nil || k.Decl.N == nil || k.Decl.N.Kind != syntax.Declare {
		return nil
	}
	return ast.Declare{N: k.Decl.N}.Params().Params()
}

// plainValue is an argument as plain data: strings, numbers and booleans
// as themselves, a reference as the path it names, anything else as its
// source text.
func (s *Site) plainValue(e ast.Expr, site resolve.Site) any {
	if e == nil {
		return nil
	}
	n := e.Syntax()
	if v, ok := resolve.StringValue(n); ok {
		return v
	}
	text := strings.TrimSpace(n.Text())
	if v, err := strconv.ParseInt(text, 10, 64); err == nil {
		return v
	}
	if v, err := strconv.ParseFloat(text, 64); err == nil {
		return v
	}
	if text == "true" || text == "false" {
		return text == "true"
	}
	if t := s.refTarget(n, site); t != nil {
		return t.Path()
	}
	return text
}
