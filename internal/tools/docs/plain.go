package docs

import (
	"context"
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// plainRenderer adapts a renderer that takes plain data, such as a TS
// module's through the JS host: the node's prose and children are rendered
// here first, and the HTML it returns is written as is.
func (s *Site) plainRenderer(x *registry.Renderer) RenderFunc {
	return func(c *Ctx) error {
		in, err := c.plainInput()
		if err != nil {
			return err
		}
		out, err := x.Plain(context.Background(), in)
		if err != nil {
			return err
		}
		c.W(out.HTML)
		for _, i := range out.Islands {
			c.Island(i)
		}
		return nil
	}
}

// plainInput is the node a context is on, as plain data.
func (c *Ctx) plainInput() (*registry.RenderInput, error) {
	n := c.Node
	s := c.Site
	kind := s.kindPath(n)
	in := &registry.RenderInput{
		Kind: kind, Path: n.Path(), Name: n.Name, Anchor: Anchor(n), Level: c.Level,
		Prose: s.proseHTML(n), Attrs: s.kindAttrs(n),
	}
	if n.Name != "" {
		in.Title = displayName(s, n)
	}
	d := c.at(n, c.Level)
	d.out = &strings.Builder{}
	if err := d.childrenOrLinks(); err != nil {
		return nil, err
	}
	in.Children = d.out.String()
	for _, sl := range s.Config.Slots {
		if sl.Of == kind && sl.Name != "before" && sl.Name != "after" {
			if in.Slots == nil {
				in.Slots = map[string]string{}
			}
			if _, done := in.Slots[sl.Name]; !done {
				in.Slots[sl.Name] = s.slotHTML(c, sl.Name, kind)
			}
		}
	}
	return in, nil
}

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

// paramNames lists a kind's parameters in order; paramTypes gives the
// type each is written with, such as "Int".
func (s *Site) paramNames(k *resolve.Node) []string {
	var out []string
	for _, p := range s.kindParams(k) {
		if p.Ident() != nil {
			out = append(out, p.Ident().Text)
		}
	}
	return out
}

func (s *Site) paramTypes(k *resolve.Node) map[string]string {
	out := map[string]string{}
	for _, p := range s.kindParams(k) {
		if p.Ident() == nil {
			continue
		}
		t := p.Type()
		if o, ok := t.(ast.OptionalType); ok {
			t = o.Type()
		}
		if nt, ok := t.(ast.NamedType); ok {
			out[p.Ident().Text] = resolve.PathText(nt.Path().N)
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

// typedAttrs types a markup tag's attributes by its kind's parameters:
// an Int, a Number or a Bool attribute becomes that value.
func (s *Site) typedAttrs(kind string, attrs map[string]string) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	var types map[string]string
	if k := s.Tree.Find(kind); k != nil {
		types = s.paramTypes(k)
	} else {
		types = s.paramTypes(s.findKind(kind))
	}
	out := map[string]any{}
	for k, v := range attrs {
		var val any = v
		switch types[k] {
		case "Int":
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				val = i
			}
		case "Number", "Float", "Decimal":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				val = f
			}
		case "Bool":
			if b, err := strconv.ParseBool(v); err == nil {
				val = b
			}
		}
		out[k] = val
	}
	return out
}

// findKind finds a kind declaration by path among the packages a tree
// imports.
func (s *Site) findKind(path string) *resolve.Node {
	var out *resolve.Node
	seen := map[*resolve.Package]bool{}
	var visit func(t *resolve.Tree)
	visit = func(t *resolve.Tree) {
		for _, p := range t.Imports {
			if out != nil || seen[p] || p.Tree == nil {
				continue
			}
			seen[p] = true
			if n := p.Tree.Find(path); n != nil {
				out = n
				return
			}
			visit(p.Tree)
		}
	}
	visit(s.Tree)
	return out
}
