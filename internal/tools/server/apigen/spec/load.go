// Package spec reads `spec_server` from a checked Heron tree and writes the
// Go handlers and the TS client of its ops.
package spec

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// Type is a type as written: List(Node), String?.
type Type struct {
	Name     string
	Args     []Type
	Optional bool
}

// Field is a member of a record type.
type Field struct {
	Name string
	Doc  string
	Type Type
}

// Record is a declared type with fields.
type Record struct {
	Name   string
	Doc    string
	Fields []Field
}

// Param is an op's parameter and where it goes.
type Param struct {
	Name string
	In   string // body, query, path or header
	Type Type
}

// Op is one op of spec_server.
type Op struct {
	Name    string // the op's name, such as get_record
	Doc     string
	Method  string // GET, POST, …; empty for a WebSocket
	Path    string // the full path, base path included
	Socket  bool
	Params  []Param
	Returns *Type // the op's result; Stream(T) for streams
}

// Stream returns the element type when the op returns a stream.
func (o *Op) Stream() *Type {
	if o.Returns != nil && o.Returns.Name == "Stream" && len(o.Returns.Args) == 1 {
		return &o.Returns.Args[0]
	}
	return nil
}

// API is what is written: the ops and types of the emitted files.
type API struct {
	Ops     []*Op
	Records []*Record
}

// Load checks the tree of the spec package's entry and returns the ops and
// types to write: those of the folder that declares `spec_server`.
func Load(entryPath string) (*API, error) {
	entry, err := resolve.Canonical(entryPath)
	if err != nil {
		return nil, err
	}
	s := check.NewSession()
	res := s.Check(entry)
	if res.Diags.HasErrors() {
		var b strings.Builder
		for _, d := range res.Diags {
			if d.Severity == diag.Error {
				b.WriteString(diag.Format(d, s.Pos) + "\n")
			}
		}
		return nil, fmt.Errorf("the API doesn't check:\n%s", b.String())
	}
	c := res.Checker
	var server *resolve.Node
	res.Tree.Walk(func(n *resolve.Node) {
		if n.Name == "spec_server" && server == nil && c.KindOf(n) != nil && c.KindOf(n).Name == "api" {
			server = n
		}
	})
	if server == nil {
		return nil, fmt.Errorf("no spec_server api in the tree")
	}
	apiDir := filepath.Dir(string(server.Decl.File.ID))
	emits := func(n *resolve.Node) bool {
		if n.Decl.File == nil || n.Decl.File.ID.IsStd() {
			return false
		}
		return filepath.Dir(string(n.Decl.File.ID)) == apiDir
	}
	a := &API{}
	res.Tree.Walk(func(n *resolve.Node) {
		if n.Name == "" || !c.IsType(n) || c.IsEnum(n) || !emits(n) || n.Parent == server {
			return
		}
		if k := c.KindOf(n); k != nil && k.Name != "type" {
			return
		}
		r := &Record{Name: n.Name, Doc: docOf(n)}
		for _, m := range n.Children {
			d := ast.Declare{N: m.Decl.N}
			r.Fields = append(r.Fields, Field{Name: m.Name, Doc: docOf(m), Type: refOf(d.Type(), d.Question() != nil)})
		}
		a.Records = append(a.Records, r)
	})
	basePath := ""
	for _, an := range (ast.Declare{N: server.Decl.N}).Annotations() {
		if resolve.PathText(an.Path().N) != "http" {
			continue
		}
		for _, arg := range an.Args().Args() {
			if na, ok := arg.(ast.NamedArg); ok && na.Ident().Text == "base_path" {
				basePath, _ = resolve.StringValue(na.Expr().Syntax())
			}
		}
	}
	baseID := server.Decl.File.ID
	first := 0 // the base file's ops come first, in their order
	for _, n := range server.Children {
		if !emits(n) {
			continue
		}
		op, err := opOf(n, basePath)
		if err != nil {
			return nil, err
		}
		if n.Decl.File.ID == baseID {
			a.Ops = append(a.Ops[:first], append([]*Op{op}, a.Ops[first:]...)...)
			first++
		} else {
			a.Ops = append(a.Ops, op)
		}
	}
	return a, nil
}

func opOf(n *resolve.Node, basePath string) (*Op, error) {
	d := ast.Declare{N: n.Decl.N}
	op := &Op{Name: n.Name, Doc: docOf(n)}
	for _, an := range d.Annotations() {
		name := resolve.PathText(an.Path().N)
		if name != "http" && name != "ws" {
			continue
		}
		for _, arg := range an.Args().Args() {
			if pa, ok := arg.(ast.PositionalArg); ok {
				op.Path, _ = resolve.StringValue(pa.Expr().Syntax())
			}
		}
		if name == "ws" {
			op.Socket = true
			continue
		}
		for _, fl := range an.Flags().Idents() {
			op.Method = strings.ToUpper(fl.Text)
		}
	}
	if op.Path == "" || (!op.Socket && op.Method == "") {
		return nil, fmt.Errorf("op %s has no @http<method>(path) or @ws(path)", n.Name)
	}
	op.Path = basePath + op.Path
	for _, p := range d.Params().Params() {
		pr := Param{Name: p.Ident().Text, Type: refOf(p.Type(), p.Question() != nil)}
		for _, an := range p.Annotations() {
			switch w := resolve.PathText(an.Path().N); w {
			case "body", "query", "path", "header":
				pr.In = w
			}
		}
		if pr.In == "" && !op.Socket {
			return nil, fmt.Errorf("op %s: param %s says neither @body, @query, @path nor @header", n.Name, pr.Name)
		}
		op.Params = append(op.Params, pr)
	}
	if nt, ok := d.Type().(ast.NamedType); ok {
		// The result type is the first positional argument, unless it's a
		// reference: a lower-case path such as a rule's.
		for _, a := range nt.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				if !isReference(pa.Expr()) {
					r := exprRef(pa.Expr())
					op.Returns = &r
				}
				break
			}
			if na, ok := a.(ast.NamedArg); ok && na.Ident() != nil && na.Ident().Text == "returns" {
				r := exprRef(na.Expr())
				op.Returns = &r
				break
			}
		}
	}
	return op, nil
}

// isReference reports whether an op's positional argument is one of its
// references rather than its result type: a path whose last name starts
// with a lower-case letter or a digit, as a rule's or a step's.
func isReference(e ast.Expr) bool {
	pe, ok := e.(ast.PathExpr)
	if !ok || pe.Path().N == nil {
		return false
	}
	segs := resolve.PathSegs(pe.Path().N)
	if len(segs) == 0 || segs[len(segs)-1] == "" {
		return false
	}
	c := segs[len(segs)-1][0]
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}

func refOf(t ast.Type, optional bool) Type {
	switch t := t.(type) {
	case ast.NamedType:
		r := Type{Name: resolve.PathText(t.Path().N), Optional: optional}
		for _, a := range t.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				r.Args = append(r.Args, exprRef(pa.Expr()))
			}
		}
		return r
	case ast.OptionalType:
		return refOf(t.Type(), true)
	default:
	}
	return Type{Name: "Any", Optional: optional}
}

func exprRef(e ast.Expr) Type {
	switch e := e.(type) {
	case ast.PathExpr:
		return Type{Name: resolve.PathText(e.Path().N)}
	case ast.CallExpr:
		r := exprRef(e.Callee())
		for _, a := range e.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				r.Args = append(r.Args, exprRef(pa.Expr()))
			}
		}
		return r
	default:
	}
	return Type{Name: "Any"}
}

// docOf is a node's docs: its `///` lines, else its prose.
func docOf(n *resolve.Node) string {
	var lines []string
	if len(n.Docs) == 0 {
		for _, p := range n.Prose {
			if p.N == nil {
				continue
			}
			for _, l := range strings.Split(p.N.Text(), "\n") {
				l = strings.TrimSpace(l)
				l = strings.TrimSpace(strings.TrimPrefix(l, "|"))
				if l != "" {
					lines = append(lines, l)
				}
			}
		}
		return strings.Join(lines, " ")
	}
	for _, s := range n.Docs {
		for _, l := range strings.Split(s.N.Text(), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "///") {
				lines = append(lines, strings.TrimSpace(strings.TrimPrefix(l, "///")))
			}
		}
	}
	return strings.Join(lines, " ")
}
