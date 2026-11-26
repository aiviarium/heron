package docs

import (
	"strings"
	"unicode"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	uistrings "github.com/aiviarium/heron/web/strings"
)

// label returns a catalogue string.
func label(key string, args ...string) string {
	if len(args) > 0 {
		return uistrings.Format(key, args...)
	}
	return uistrings.Get(key)
}

// sentenceCase turns a name into display text: words split at underscores
// and case changes, the first capitalised, as the Markup tab names
// sections. Names written in upper case or as types keep their case.
func sentenceCase(name string) string {
	words := splitName(name)
	for i, w := range words {
		if i == 0 {
			r := []rune(w)
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
		} else if !isAcronym(w) {
			words[i] = strings.ToLower(w)
		}
	}
	return strings.Join(words, " ")
}

func isAcronym(w string) bool {
	if len(w) < 2 {
		return false
	}
	for _, r := range w {
		if unicode.IsLower(r) {
			return false
		}
	}
	return true
}

// splitName splits snake_case and camelCase into words.
func splitName(name string) []string {
	var words []string
	for _, part := range strings.Split(name, "_") {
		if part == "" {
			continue
		}
		start := 0
		rs := []rune(part)
		for i := 1; i < len(rs); i++ {
			if unicode.IsUpper(rs[i]) && unicode.IsLower(rs[i-1]) {
				words = append(words, string(rs[start:i]))
				start = i
			}
		}
		words = append(words, string(rs[start:]))
	}
	if len(words) == 0 {
		return []string{name}
	}
	return words
}

// kindWords is a kind's name as words, such as "must not" for mustNot.
func kindWords(name string) string {
	return strings.ToLower(strings.Join(splitName(name), " "))
}

// displayName is a node's title: its @display name, or its name in
// sentence case. Types keep their names.
func displayName(s *Site, n *resolve.Node) string {
	if a := annotation(s, n, "display"); a != nil {
		if args := a.Args().Args(); len(args) > 0 {
			if pa, ok := args[0].(ast.PositionalArg); ok {
				if v, ok := resolve.StringValue(pa.Expr().Syntax()); ok {
					return v
				}
			}
		}
	}
	if n.Name == "" {
		if k := s.Checker.KindOf(n); k != nil {
			return sentenceCase(k.Name)
		}
		return ""
	}
	if s.kindPath(n) == "tech.command" {
		// A command reads as it's typed, as `heron check`.
		if v, ok := argString(kindArgs(n), 0, "name"); ok {
			return v
		}
	}
	if s.isTypeDecl(n) || n.Parent != nil && s.isTypeDecl(n.Parent) {
		return n.Name
	}
	return sentenceCase(n.Name)
}

// annotation returns the first annotation on a node (its own, then its
// group's) whose name's last segment is name.
func annotation(s *Site, n *resolve.Node, name string) *ast.Annotation {
	for _, a := range annotations(n) {
		if annName(a) == name {
			a := a
			return &a
		}
	}
	return nil
}

func annotations(n *resolve.Node) []ast.Annotation {
	var out []ast.Annotation
	for _, site := range append(append([]resolve.Site(nil), n.Annotations...), n.GroupAnnotations...) {
		if site.N != nil && site.N.Kind == syntax.Annotation {
			out = append(out, ast.Annotation{N: site.N})
		}
	}
	return out
}

func annotationsNamed(n *resolve.Node, name string) []ast.Annotation {
	var out []ast.Annotation
	for _, a := range annotations(n) {
		if annName(a) == name {
			out = append(out, a)
		}
	}
	return out
}

func annName(a ast.Annotation) string {
	segs := resolve.PathSegs(a.Path().N)
	if len(segs) == 0 {
		return ""
	}
	return segs[len(segs)-1]
}

func flagsOf(f ast.Flags) []string {
	var out []string
	if f.N == nil {
		return nil
	}
	for _, t := range f.Idents() {
		out = append(out, t.Text)
	}
	return out
}

// argString returns a positional (index ≥ 0) or named string argument.
func argString(args ast.Args, index int, name string) (string, bool) {
	if args.N == nil {
		return "", false
	}
	pos := 0
	for _, a := range args.Args() {
		switch a := a.(type) {
		case ast.PositionalArg:
			if pos == index {
				return resolve.StringValue(a.Expr().Syntax())
			}
			pos++
		case ast.NamedArg:
			if name != "" && a.Ident() != nil && a.Ident().Text == name {
				return resolve.StringValue(a.Expr().Syntax())
			}
		}
	}
	return "", false
}

// argExprs returns the positional argument expressions.
func argExprs(args ast.Args) []ast.Expr {
	if args.N == nil {
		return nil
	}
	var out []ast.Expr
	for _, a := range args.Args() {
		if pa, ok := a.(ast.PositionalArg); ok {
			out = append(out, pa.Expr())
		}
	}
	return out
}

// namedArg returns a named argument's expression.
func namedArg(args ast.Args, name string) ast.Expr {
	if args.N == nil {
		return nil
	}
	for _, a := range args.Args() {
		if na, ok := a.(ast.NamedArg); ok && na.Ident() != nil && na.Ident().Text == name {
			return na.Expr()
		}
	}
	return nil
}

// kindArgs returns the arguments a node's kind slot is written with, as
// the targets of `rule(store.place_order)`.
func kindArgs(n *resolve.Node) ast.Args {
	if n.Decl.N == nil {
		return ast.Args{}
	}
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if t, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType); ok {
			return t.Args()
		}
	case syntax.Value:
		if t, ok := (ast.Value{N: n.Decl.N}).Type().(ast.NamedType); ok {
			return t.Args()
		}
	case syntax.Invoke:
		return ast.Invoke{N: n.Decl.N}.Args()
	case syntax.Tag:
		// A kind element: its attributes, as the checker read them.
		return n.ElementArgs
	}
	return ast.Args{}
}

// kindFlags returns the flags on a node's kind slot, as <post> or <accepted>.
func kindFlags(n *resolve.Node) []string {
	if n.Decl.N == nil {
		return nil
	}
	switch n.Decl.N.Kind {
	case syntax.Declare:
		if t, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType); ok {
			return flagsOf(t.Flags())
		}
	case syntax.Invoke:
		return flagsOf(ast.Invoke{N: n.Decl.N}.Flags())
	}
	return nil
}

// refTarget resolves a path expression or path syntax node to a node,
// through the checker's references and then the scope it was written in.
func (s *Site) refTarget(p *syntax.Node, site resolve.Site) *resolve.Node {
	if p == nil {
		return nil
	}
	if n := s.Checker.Refs[p]; n != nil {
		return n
	}
	var path *syntax.Node
	if p.Kind == syntax.Path {
		path = p
	} else {
		p.Walk(func(x *syntax.Node) bool {
			if path == nil && x.Kind == syntax.Path {
				path = x
			}
			return path == nil
		})
	}
	if path == nil {
		return nil
	}
	if n := s.Checker.Refs[path]; n != nil {
		return n
	}
	return s.lookup(site, resolve.PathSegs(path))
}

// lookup resolves a dotted path from a site's scope.
func (s *Site) lookup(site resolve.Site, segs []string) *resolve.Node {
	sc := site.Scope
	if sc == nil || len(segs) == 0 {
		return nil
	}
	var out *resolve.Node
	sc.Each(segs[0], site.File, -1, func(b resolve.Binding) bool {
		n := b.Node
		for _, seg := range segs[1:] {
			if n == nil {
				break
			}
			n = n.Child(seg)
		}
		if n != nil {
			out = n
			return false
		}
		return true
	})
	return out
}

// targets returns the nodes a requirement's kind arguments name.
func (s *Site) targets(n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, e := range argExprs(kindArgs(n)) {
		if t := s.refTarget(e.Syntax(), n.Decl); t != nil {
			out = append(out, t)
		}
	}
	return out
}

// httpRoute returns an op's HTTP method and path, from @http<method>(path).
func httpRoute(s *Site, op *resolve.Node) (method, path string) {
	for _, a := range annotationsNamed(op, "http") {
		for _, f := range flagsOf(a.Flags()) {
			method = strings.ToUpper(f)
		}
		if p, ok := argString(a.Args(), 0, "path"); ok {
			path = p
		}
	}
	return method, path
}

// basePath returns an api's base path, from @http(base_path = …).
func basePath(s *Site, api *resolve.Node) string {
	for _, a := range annotationsNamed(api, "http") {
		if p, ok := argString(a.Args(), -1, "base_path"); ok {
			return p
		}
	}
	return ""
}

// wsRoute returns an op's or api's WebSocket path, from @ws(path).
func wsRoute(n *resolve.Node) string {
	for _, a := range annotationsNamed(n, "ws") {
		if p, ok := argString(a.Args(), 0, "path"); ok {
			return p
		}
	}
	return ""
}
