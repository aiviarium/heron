// Package spec reads `spec_server` from a checked Heron tree and writes the
// Go handlers and the TS client of its ops.
package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// Load checks the base entry with the extension files included and returns
// the ops and types to write: those of the extension files when there are
// any, otherwise all of the tree's own.
func Load(base string, exts []string) (*API, error) {
	baseID, err := resolve.Canonical(base)
	if err != nil {
		return nil, err
	}
	s := check.NewSession()
	entry := baseID
	emit := map[resolve.FileID]bool{}
	if len(exts) > 0 {
		inc := []string{strconv.Quote(string(baseID))}
		for _, e := range exts {
			id, err := resolve.Canonical(e)
			if err != nil {
				return nil, err
			}
			if _, err := os.Stat(string(id)); err != nil {
				return nil, err
			}
			emit[id] = true
			inc = append(inc, strconv.Quote(string(id)))
		}
		entry = resolve.FileID(filepath.Join(filepath.Dir(string(baseID)), ".apigen-entry.heron"))
		s.Loader.Overlay[entry] = "include = [" + strings.Join(inc, ", ") + "]\n"
	}
	res := s.Check(entry)
	if res.Diags.HasErrors() {
		var b strings.Builder
		for _, d := range res.Diags {
			b.WriteString(diag.Format(d, s.Pos) + "\n")
		}
		return nil, fmt.Errorf("the API doesn't check:\n%s", b.String())
	}
	emits := func(n *resolve.Node) bool {
		if n.Decl.File == nil || n.Decl.File.ID.IsStd() || (len(emit) > 0 && n.Decl.File.ID == entry) {
			return false
		}
		return len(emit) == 0 || emit[n.Decl.File.ID]
	}
	c := res.Checker
	a := &API{}
	var server *resolve.Node
	for _, n := range res.Tree.Root.Children {
		switch {
		case n.Name == "spec_server":
			server = n
		case c.IsType(n) && emits(n) && !c.IsEnum(n):
			r := &Record{Name: n.Name, Doc: docOf(n)}
			for _, m := range n.Children {
				d := ast.Declare{N: m.Decl.N}
				r.Fields = append(r.Fields, Field{Name: m.Name, Doc: docOf(m), Type: refOf(d.Type(), d.Question() != nil)})
			}
			a.Records = append(a.Records, r)
		}
	}
	if server == nil {
		return nil, fmt.Errorf("no spec_server api in the tree")
	}
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
	first := 0 // the base file's ops come first, in their order
	for _, n := range server.Children {
		if !emits(n) {
			continue
		}
		op, err := opOf(n, basePath)
		if err != nil {
			return nil, err
		}
		if n.Decl.File.ID == resolve.FileID(baseID) {
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
		for _, a := range nt.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				r := exprRef(pa.Expr())
				op.Returns = &r
			}
		}
	}
	return op, nil
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

func docOf(n *resolve.Node) string {
	var lines []string
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
