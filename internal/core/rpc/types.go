package rpc

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// Type is a type as written, resolved: its declaration, its arguments,
// and a union expression's alternatives.
type Type struct {
	Decl     *resolve.Node
	Name     string // the path's last segment, as String or Page
	Args     []Type
	Union    []Type
	Optional bool
	Bits     int  // Int(bits = 32), u32, f32: 0 when unsized
	Unsigned bool // Int(signed = false), u32
	Text     string
	Site     resolve.Site
}

// IsStream reports whether the type is Stream(T).
func (t Type) IsStream() bool { return t.Name == "Stream" && len(t.Args) == 1 }

// Elem is a stream's element type, or the type itself.
func (t Type) Elem() Type {
	if t.IsStream() {
		return t.Args[0]
	}
	return t
}

type reader struct {
	c *types.Checker
}

// resolvePath resolves a path syntax node written at a site.
func (r *reader) resolvePath(p *syntax.Node, site resolve.Site) *resolve.Node {
	return project.RefTarget(r.c, p, site)
}

func (r *reader) readType(t ast.Type, site resolve.Site, env map[string]Type) Type {
	switch t := t.(type) {
	case ast.NamedType:
		segs := resolve.PathSegs(t.Path().N)
		if len(segs) == 1 && env != nil {
			if b, ok := env[segs[0]]; ok {
				return b
			}
		}
		x := Type{Name: last(segs), Text: strings.TrimSpace(t.N.Text()), Site: resolve.Site{File: site.File, N: t.N, Scope: site.Scope}}
		x.Decl = r.resolvePath(t.Path().N, site)
		for _, a := range t.Args().Args() {
			switch a := a.(type) {
			case ast.PositionalArg:
				x.Args = append(x.Args, r.readTypeExpr(a.Expr(), site, env))
			case ast.NamedArg:
				r.sizeArg(&x, a)
			}
		}
		r.alias(&x)
		return x
	case ast.OptionalType:
		x := r.readType(t.Type(), site, env)
		x.Optional = true
		x.Text += "?"
		return x
	case ast.UnionType:
		x := Type{Text: strings.TrimSpace(t.N.Text()), Site: resolve.Site{File: site.File, N: t.N, Scope: site.Scope}}
		for _, v := range []Type{r.readType(t.Lhs(), site, env), r.readType(t.Rhs(), site, env)} {
			if len(v.Union) > 0 {
				x.Union = append(x.Union, v.Union...)
			} else {
				x.Union = append(x.Union, v)
			}
		}
		return x
	case nil:
		return Type{}
	default:
		return Type{Text: strings.TrimSpace(t.Syntax().Text()), Site: resolve.Site{File: site.File, N: t.Syntax(), Scope: site.Scope}}
	}
}

func (r *reader) readTypeExpr(e ast.Expr, site resolve.Site, env map[string]Type) Type {
	switch e := e.(type) {
	case ast.PathExpr:
		segs := resolve.PathSegs(e.Path().N)
		if len(segs) == 1 && env != nil {
			if b, ok := env[segs[0]]; ok {
				return b
			}
		}
		x := Type{Name: last(segs), Decl: r.resolvePath(e.Path().N, site), Text: strings.TrimSpace(e.N.Text()), Site: resolve.Site{File: site.File, N: e.N, Scope: site.Scope}}
		r.alias(&x)
		return x
	case ast.CallExpr:
		x := r.readTypeExpr(e.Callee(), site, env)
		x.Text = strings.TrimSpace(e.N.Text())
		for _, a := range e.Args().Args() {
			switch a := a.(type) {
			case ast.PositionalArg:
				x.Args = append(x.Args, r.readTypeExpr(a.Expr(), site, env))
			case ast.NamedArg:
				r.sizeArg(&x, a)
			}
		}
		return x
	case nil:
		return Type{}
	default:
		return Type{Text: strings.TrimSpace(e.Syntax().Text()), Site: resolve.Site{File: site.File, N: e.Syntax(), Scope: site.Scope}}
	}
}

// sizeArg reads Int(bits = 32, signed = false).
func (r *reader) sizeArg(x *Type, a ast.NamedArg) {
	if a.Ident() == nil {
		return
	}
	v := strings.TrimSpace(a.Expr().Syntax().Text())
	switch a.Ident().Text {
	case "bits":
		n := 0
		for _, ch := range v {
			if ch < '0' || ch > '9' {
				return
			}
			n = n*10 + int(ch-'0')
		}
		x.Bits = n
	case "signed":
		x.Unsigned = v == "false"
	}
}

// alias reads a sized alias of std/types, such as u32 = Int(bits = 32,
// signed = false), as the type it names.
func (r *reader) alias(x *Type) {
	d := x.Decl
	if d == nil || d.Value == nil || !isStd(d) {
		return
	}
	call, ok := d.Value.(ast.CallExpr)
	if !ok {
		return
	}
	segs := resolve.PathSegs(syntaxPath(call.Callee().Syntax()))
	if len(segs) == 0 {
		return
	}
	x.Name = last(segs)
	x.Decl = nil
	for _, a := range call.Args().Args() {
		if na, ok := a.(ast.NamedArg); ok {
			r.sizeArg(x, na)
		}
	}
}

func syntaxPath(n *syntax.Node) *syntax.Node {
	if n == nil {
		return nil
	}
	if n.Kind == syntax.Path {
		return n
	}
	var out *syntax.Node
	n.Walk(func(x *syntax.Node) bool {
		if out == nil && x.Kind == syntax.Path {
			out = x
		}
		return out == nil
	})
	return out
}

// member is a field of a record, read from its declaration.
type member struct {
	node     *resolve.Node
	name     string
	typ      Type
	optional bool
	doc      string
}

// members returns a type's fields, its own first, then those of the
// interfaces it implements that it doesn't redeclare.
func (r *reader) members(t *resolve.Node, env map[string]Type) []member {
	var out []member
	seen := map[string]bool{}
	var collect func(t *resolve.Node, depth int)
	collect = func(t *resolve.Node, depth int) {
		if depth > 8 {
			return
		}
		for _, ch := range t.Children {
			if ch.Name == "" || seen[ch.Name] || ch.Decl.N == nil {
				continue
			}
			if k := project.KindPath(r.c, ch); k != "" && k != "types.member" {
				continue
			}
			site := ch.Decl
			if t.Body != nil && site.Scope == nil {
				site.Scope = t.Body
			}
			m := member{node: ch, name: ch.Name, doc: DocOf(ch)}
			switch ch.Decl.N.Kind {
			case syntax.Declare:
				d := ast.Declare{N: ch.Decl.N}
				m.typ = r.readType(d.Type(), site, env)
				m.optional = d.Question() != nil
			case syntax.Value:
				v := ast.Value{N: ch.Decl.N}
				m.typ = r.readType(v.Type(), site, env)
				m.optional = v.Question() != nil
			default:
				continue
			}
			seen[ch.Name] = true
			out = append(out, m)
		}
		for _, i := range r.c.Implements(t) {
			collect(i, depth+1)
		}
	}
	collect(t, 0)
	return out
}

// variantType reads the type of one of a union's variants.
func (r *reader) variantType(u *resolve.Node, v types.UnionVariant) Type {
	site := v.Node.Decl
	if u.Body != nil && site.Scope == nil {
		site.Scope = u.Body
	}
	if v.Expr != nil {
		return r.readTypeExpr(v.Expr, u.Decl, nil)
	}
	return r.readType(ast.Declare{N: v.Node.Decl.N}.Type(), site, nil)
}

// envFor binds a generic type's parameters to the arguments it's used with.
func (r *reader) envFor(x Type) map[string]Type {
	if x.Decl == nil || len(x.Args) == 0 || x.Decl.Decl.N == nil || x.Decl.Decl.N.Kind != syntax.Declare {
		return nil
	}
	env := map[string]Type{}
	for i, p := range (ast.Declare{N: x.Decl.Decl.N}).Params().Params() {
		if i < len(x.Args) && p.Ident() != nil {
			env[p.Ident().Text] = x.Args[i]
		}
	}
	return env
}

// DocOf joins a node's /// lines.
func DocOf(n *resolve.Node) string {
	var lines []string
	for _, s := range n.Docs {
		if s.N == nil {
			continue
		}
		for _, l := range strings.Split(s.N.Text(), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "///") {
				lines = append(lines, strings.TrimSpace(strings.TrimPrefix(l, "///")))
			}
		}
	}
	return strings.Join(lines, " ")
}

// ProseText is a node's docs, or else its prose lines, as plain text.
func ProseText(n *resolve.Node) string {
	if d := DocOf(n); d != "" {
		return d
	}
	var lines []string
	for _, s := range n.Prose {
		if s.N == nil {
			continue
		}
		for _, l := range strings.Split(s.N.Text(), "\n") {
			l = strings.TrimSpace(l)
			l = strings.TrimSpace(strings.TrimPrefix(l, "|"))
			if l != "" {
				lines = append(lines, l)
			}
		}
	}
	return strings.Join(lines, " ")
}

func docText(d ast.Doc) string {
	if d.N == nil {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(d.N.Text(), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "///") {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(l, "///")))
		}
	}
	return strings.Join(lines, " ")
}

func last(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	return segs[len(segs)-1]
}

// isStd reports whether a node is declared in a standard package.
func isStd(n *resolve.Node) bool {
	return n.Decl.File != nil && n.Decl.File.ID.IsStd()
}

// Own reports whether a node belongs to the tree rather than a package
// mounted into it.
func Own(n *resolve.Node) bool {
	for x := n; x != nil; x = x.Parent {
		if x.Shape == resolve.ShapeMounted {
			return false
		}
	}
	return n.Decl.File == nil || !n.Decl.File.ID.IsStd()
}

// Pascal writes a snake_case name in upper camel case: get_parcel is
// GetParcel.
func Pascal(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		switch {
		case r == '_' || r == '-' || r == '.' || r == '/' || r == '$':
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

// Camel writes a snake_case name in lower camel case, as protobuf's JSON
// names fields: weight_grams is weightGrams.
func Camel(s string) string {
	var b strings.Builder
	up := false
	for _, r := range s {
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

// Snake writes a name in lower snake case: ParcelEvent is parcel_event.
func Snake(s string) string { return types.Snake(s) }

// UpperSnake writes a name in upper snake case: ParcelStatus is
// PARCEL_STATUS, inTransit is IN_TRANSIT.
func UpperSnake(s string) string { return strings.ToUpper(Snake(s)) }
