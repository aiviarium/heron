// Package jsonnames reads std/json's @key and @keys annotations: the name a member
// or a parameter has in JSON, and the case a type or an api writes its
// fields' names in. The interpreter's json.encode and json.decode, the
// JSON-RPC projection and the docs read names through it.
package jsonnames

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// Camel is the case that writes line_number as lineNumber.
const Camel = "camel"

// is reports whether an annotation is std/json's @key or @keys, written
// bare or qualified, as @json.key.
func is(a ast.Annotation, name string) bool {
	segs := resolve.PathSegs(a.Path().N)
	switch len(segs) {
	case 1:
		return segs[0] == name
	case 2:
		return segs[0] == "json" && segs[1] == name
	}
	return false
}

func annotations(n *resolve.Node) []ast.Annotation {
	var out []ast.Annotation
	for _, s := range append(append([]resolve.Site(nil), n.Annotations...), n.GroupAnnotations...) {
		if s.N != nil && s.N.Kind == syntax.Annotation {
			out = append(out, ast.Annotation{N: s.N})
		}
	}
	return out
}

// arg reads @key's name or @keys's case, positional or named.
func arg(as []ast.Annotation, name string) string {
	ann := map[string]string{"name": "key", "case": "keys"}[name]
	for _, a := range as {
		if !is(a, ann) || a.Args().N == nil {
			continue
		}
		for _, x := range a.Args().Args() {
			switch x := x.(type) {
			case ast.PositionalArg:
				if v, ok := resolve.StringValue(x.Expr().Syntax()); ok {
					return v
				}
				t := strings.TrimSpace(x.Expr().Syntax().Text())
				if i := strings.LastIndex(t, "."); i >= 0 {
					t = t[i+1:]
				}
				return t
			case ast.NamedArg:
				if x.Ident() != nil && x.Ident().Text == name {
					if v, ok := resolve.StringValue(x.Expr().Syntax()); ok {
						return v
					}
					t := strings.TrimSpace(x.Expr().Syntax().Text())
					if i := strings.LastIndex(t, "."); i >= 0 {
						t = t[i+1:]
					}
					return t
				}
			}
		}
	}
	return ""
}

// Case is the case a type's or an api's @keys sets, "camel" or "".
func Case(n *resolve.Node) string {
	if n == nil {
		return ""
	}
	if c := arg(annotations(n), "case"); c == Camel {
		return c
	}
	return ""
}

// Member is a member's name in JSON: its @key, or its name in the
// case its type sets, or else the fallback case, as an api's.
func Member(member, owner *resolve.Node, fallback string) string {
	if v := arg(annotations(member), "name"); v != "" {
		return v
	}
	c := Case(owner)
	if c == "" {
		c = fallback
	}
	return Apply(member.Name, c)
}

// Param is a parameter's name in JSON: its @key, or its name in
// the case given, as its api's.
func Param(p ast.Param, c string) string {
	name := ""
	if p.Ident() != nil {
		name = p.Ident().Text
	}
	if v := arg(p.Annotations(), "name"); v != "" {
		return v
	}
	return Apply(name, c)
}

// Apply writes a name in a case.
func Apply(name, c string) string {
	if c != Camel {
		return name
	}
	var b strings.Builder
	up := false
	for _, r := range name {
		switch {
		case r == '_':
			up = true
		case up:
			b.WriteString(strings.ToUpper(string(r)))
			up = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
