// Command protogen reads the protocol description, the spec package's
// language/interop/protocol_messages.heron, through the same check every
// Heron tree goes through, and writes the Go side and the TS side of the
// extension protocol from it. Without -in it reads the spec package the
// repository at -root imports: $HERON_SPECS_DIR, or the one installed
// under .heron/packages/.
//
//	go run ./internal/core/extension/protocol/internal/protogen \
//	    -go internal/core/extension/protocol/protocol_gen.go \
//	    -ts web/runtime/src/protocol.gen.ts
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/fixtures"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// typeRef is a type as written: List(Node), String?.
type typeRef struct {
	name     string
	args     []typeRef
	optional bool
}

type field struct {
	name string
	doc  string
	ty   typeRef
}

type record struct {
	name   string
	doc    string
	fields []field
}

type enum struct {
	name     string
	doc      string
	variants []string
}

type constant struct {
	name  string
	doc   string
	ty    string
	value string
}

type method struct {
	iface        string // Extension, Host or Notifications
	name         string // the func's name
	wire         string
	doc          string
	params       typeRef
	result       *typeRef
	notification bool
}

type proto struct {
	consts  []constant
	records []record
	enums   []enum
	methods []method
}

func main() {
	in := flag.String("in", "", "the protocol description (default: the spec package's language/interop/protocol_messages.heron)")
	root := flag.String("root", ".", "the repository root, whose spec package -in defaults to")
	entry := flag.String("entry", "", "the tree -in is checked in (default: the spec package's index.heron when -in is defaulted, else -in itself)")
	goOut := flag.String("go", "", "the Go file to write")
	tsOut := flag.String("ts", "", "the TS file to write")
	flag.Parse()
	if *in == "" {
		dir, err := fixtures.SpecDir(*root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "protogen:", err)
			os.Exit(1)
		}
		*in = filepath.Join(dir, "language", "interop", "protocol_messages.heron")
		if *entry == "" {
			*entry = filepath.Join(dir, "index.heron")
		}
	}
	if *entry == "" {
		*entry = *in
	}
	p, err := load(*entry, *in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
	src, err := goFile(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*goOut, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*tsOut, tsFile(p), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
}

// load checks the tree of entry and reads the protocol from the nodes the
// file at path declares, wherever the tree includes it.
func load(entry, path string) (*proto, error) {
	canon := func(p string) (resolve.FileID, error) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		return resolve.Canonical(abs)
	}
	id, err := canon(path)
	if err != nil {
		return nil, err
	}
	eid, err := canon(entry)
	if err != nil {
		return nil, err
	}
	s := check.NewSession()
	res := s.Check(eid)
	if res.Diags.HasErrors() {
		var b strings.Builder
		for _, d := range res.Diags {
			b.WriteString(diag.Format(d, s.Pos) + "\n")
		}
		return nil, fmt.Errorf("%s doesn't check:\n%s", path, b.String())
	}
	c := res.Checker
	p := &proto{}
	in := func(n *resolve.Node) bool { return n.Decl.File != nil && n.Decl.File.ID == id }
	// The file's top-level nodes: those it declares whose parent it doesn't.
	var top []*resolve.Node
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range n.Children {
			if ch.Shape == resolve.ShapeMounted {
				continue
			}
			if in(ch) && !in(n) {
				top = append(top, ch)
				continue
			}
			walk(ch)
		}
	}
	walk(res.Tree.Root)
	if len(top) == 0 {
		return nil, fmt.Errorf("the tree of %s doesn't include %s", entry, path)
	}
	for _, n := range top {
		switch {
		case n.Decl.N.Kind == syntax.Value:
			v := ast.Value{N: n.Decl.N}
			nt, _ := v.Type().(ast.NamedType)
			p.consts = append(p.consts, constant{name: n.Name, doc: docOf(n), ty: resolve.PathText(nt.Path().N), value: strings.TrimSpace(v.Expr().Syntax().Text())})
		case c.IsEnum(n):
			e := enum{name: n.Name, doc: docOf(n)}
			for _, v := range n.Children {
				e.variants = append(e.variants, v.Name)
			}
			p.enums = append(p.enums, e)
		case isAPI(c, n):
			// Each side's methods: an api bound by std/jsonrpc, its ops
			// named on the wire by @jsonrpc, one without a result a
			// notification.
			for _, f := range n.Children {
				if f.Name != "" && f.Decl.N != nil && f.Decl.N.Kind == syntax.Declare {
					p.methods = append(p.methods, methodOf(goName(n.Name), f))
				}
			}
		case c.IsType(n):
			r := record{name: n.Name, doc: docOf(n)}
			for _, m := range n.Children {
				d := ast.Declare{N: m.Decl.N}
				r.fields = append(r.fields, field{name: m.Name, doc: docOf(m), ty: refOf(d.Type(), d.Question() != nil)})
			}
			p.records = append(p.records, r)
		}
	}
	return p, nil
}

func isAPI(c *types.Checker, n *resolve.Node) bool {
	k := c.KindOf(n)
	return k != nil && k.Path() == "types.api"
}

func methodOf(iface string, n *resolve.Node) method {
	d := ast.Declare{N: n.Decl.N}
	m := method{iface: iface, name: n.Name, wire: n.Name, doc: docOf(n)}
	for _, a := range d.Annotations() {
		if resolve.PathText(a.Path().N) != "jsonrpc" {
			continue
		}
		for _, arg := range a.Args().Args() {
			switch arg := arg.(type) {
			case ast.PositionalArg:
				m.wire, _ = resolve.StringValue(arg.Expr().Syntax())
			case ast.NamedArg:
				if arg.Ident() != nil && arg.Ident().Text == "method" {
					m.wire, _ = resolve.StringValue(arg.Expr().Syntax())
				}
			}
		}
	}
	if ps := d.Params().Params(); len(ps) == 1 {
		m.params = refOf(ps[0].Type(), false)
	}
	m.notification = true
	if nt, ok := d.Type().(ast.NamedType); ok {
		for _, a := range nt.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				r := exprRef(pa.Expr())
				m.result = &r
				m.notification = false
			}
		}
	}
	return m
}

func refOf(t ast.Type, optional bool) typeRef {
	switch t := t.(type) {
	case ast.NamedType:
		r := typeRef{name: resolve.PathText(t.Path().N), optional: optional}
		for _, a := range t.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				r.args = append(r.args, exprRef(pa.Expr()))
			}
		}
		return r
	case ast.OptionalType:
		return refOf(t.Type(), true)
	default:
	}
	return typeRef{name: "Any", optional: optional}
}

func exprRef(e ast.Expr) typeRef {
	switch e := e.(type) {
	case ast.PathExpr:
		return typeRef{name: resolve.PathText(e.Path().N)}
	case ast.CallExpr:
		r := exprRef(e.Callee())
		for _, a := range e.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				r.args = append(r.args, exprRef(pa.Expr()))
			}
		}
		return r
	default:
	}
	return typeRef{name: "Any"}
}

// docOf joins a node's /// lines.
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

var _ = types.IsStd

// --- Go ---

func goName(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		switch {
		case r == '_' || r == '/' || r == '$':
			up = true
		case up:
			b.WriteString(strings.ToUpper(string(r)))
			up = false
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "Id" {
		return "ID"
	}
	return out
}

func (p *proto) isRecord(name string) bool {
	for _, r := range p.records {
		if r.name == name {
			return true
		}
	}
	return false
}

func (p *proto) goType(t typeRef, inMessage bool) string {
	opt := t.optional
	var base string
	ptr := false
	switch t.name {
	case "String":
		base = "string"
		opt = false
	case "Int":
		base, ptr = "int64", true
	case "Bool":
		base, ptr = "bool", true
	case "Float":
		base, ptr = "float64", true
	case "Bytes":
		return "[]byte"
	case "Any":
		if inMessage {
			return "Raw"
		}
		return "Value"
	case "List":
		return "[]" + p.goType(t.args[0], false)
	case "Map":
		return "map[string]" + p.goType(t.args[1], false)
	default:
		base = goName(t.name)
		ptr = p.isRecord(t.name)
		if !ptr {
			opt = false
		}
	}
	if opt && ptr {
		return "*" + base
	}
	return base
}

func comment(b *bytes.Buffer, indent, doc string) {
	if doc == "" {
		return
	}
	fmt.Fprintf(b, "%s// %s\n", indent, doc)
}

func goFile(p *proto) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by protogen from the spec package's language/interop/protocol_messages.heron. DO NOT EDIT.\n\n")
	b.WriteString("package protocol\n\nimport \"context\"\n\n")
	for _, c := range p.consts {
		comment(&b, "", c.doc)
		fmt.Fprintf(&b, "const %s = %s\n\n", goName(c.name), c.value)
	}
	for _, e := range p.enums {
		comment(&b, "", e.doc)
		fmt.Fprintf(&b, "type %s string\n\nconst (\n", e.name)
		for _, v := range e.variants {
			fmt.Fprintf(&b, "\t%s%s %s = %q\n", e.name, goName(v), e.name, v)
		}
		b.WriteString(")\n\n")
	}
	for _, r := range p.records {
		comment(&b, "", r.doc)
		fmt.Fprintf(&b, "type %s struct {\n", r.name)
		for _, f := range r.fields {
			comment(&b, "\t", f.doc)
			tag := f.name
			if f.ty.optional {
				tag += ",omitempty"
			}
			fmt.Fprintf(&b, "\t%s %s `cbor:%q json:%q`\n", goName(f.name), p.goType(f.ty, r.name == "Message"), tag, tag)
		}
		b.WriteString("}\n\n")
	}
	// Method names, once per wire name.
	seen := map[string]bool{}
	b.WriteString("// The methods, by wire name.\nconst (\n")
	for _, m := range p.methods {
		if seen[m.wire] {
			continue
		}
		seen[m.wire] = true
		fmt.Fprintf(&b, "\tMethod%s = %q\n", goName(m.name), m.wire)
	}
	b.WriteString(")\n\n")
	b.WriteString("// Caller sends a request and decodes its result.\ntype Caller interface {\n\tCall(ctx context.Context, method string, params, result any) error\n}\n\n")
	b.WriteString("// Notifier sends a notification.\ntype Notifier interface {\n\tNotify(method string, params any) error\n}\n\n")
	b.WriteString("// Handler serves one method: it decodes its params and returns its result.\ntype Handler func(ctx context.Context, decode func(any) error) (any, error)\n\n")
	for _, iface := range []string{"Extension", "Host"} {
		fmt.Fprintf(&b, "// %s is what a peer serves.\ntype %s interface {\n", iface, iface)
		for _, m := range p.methods {
			if m.iface != iface {
				continue
			}
			comment(&b, "\t", m.doc)
			fmt.Fprintf(&b, "\t%s(ctx context.Context, p *%s) (%s, error)\n", goName(m.name), goName(m.params.name), p.resultGo(m))
		}
		b.WriteString("}\n\n")
		fmt.Fprintf(&b, "// %sClient calls a peer that serves %s.\ntype %sClient struct{ C Caller }\n\n", iface, iface, iface)
		for _, m := range p.methods {
			if m.iface != iface {
				continue
			}
			res := p.resultGo(m)
			fmt.Fprintf(&b, "func (c %sClient) %s(ctx context.Context, p *%s) (%s, error) {\n", iface, goName(m.name), goName(m.params.name), res)
			if strings.HasPrefix(res, "*") {
				fmt.Fprintf(&b, "\tvar out %s\n\terr := c.C.Call(ctx, %q, p, &out)\n\treturn &out, err\n}\n\n", res[1:], m.wire)
			} else {
				fmt.Fprintf(&b, "\tvar out %s\n\terr := c.C.Call(ctx, %q, p, &out)\n\treturn out, err\n}\n\n", res, m.wire)
			}
		}
		fmt.Fprintf(&b, "// %sHandlers serves an implementation of %s by wire name.\nfunc %sHandlers(impl %s) map[string]Handler {\n\treturn map[string]Handler{\n", iface, iface, iface, iface)
		for _, m := range p.methods {
			if m.iface != iface {
				continue
			}
			fmt.Fprintf(&b, "\t\t%q: func(ctx context.Context, decode func(any) error) (any, error) {\n\t\t\tvar p %s\n\t\t\tif err := decode(&p); err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n\t\t\treturn impl.%s(ctx, &p)\n\t\t},\n", m.wire, goName(m.params.name), goName(m.name))
		}
		b.WriteString("\t}\n}\n\n")
	}
	b.WriteString("// Notifications either side may send.\n")
	for _, m := range p.methods {
		if m.iface != "Notifications" {
			continue
		}
		comment(&b, "", m.doc)
		fmt.Fprintf(&b, "func Notify%s(n Notifier, p *%s) error { return n.Notify(%q, p) }\n\n", goName(m.name), goName(m.params.name), m.wire)
	}
	return format.Source(b.Bytes())
}

func (p *proto) resultGo(m method) string {
	if m.result == nil {
		return "Value"
	}
	r := *m.result
	if p.isRecord(r.name) {
		return "*" + goName(r.name)
	}
	t := p.goType(r, false)
	return strings.TrimPrefix(t, "*")
}

// --- TS ---

func tsName(s string) string {
	g := goName(s)
	if g == "ID" {
		return "id"
	}
	return strings.ToLower(g[:1]) + g[1:]
}

func (p *proto) tsType(t typeRef) string {
	switch t.name {
	case "String":
		return "string"
	case "Int":
		return "number"
	case "Bool":
		return "boolean"
	case "Float":
		return "number"
	case "Bytes":
		return "Uint8Array"
	case "Any":
		return "unknown"
	case "List":
		return p.tsType(t.args[0]) + "[]"
	case "Map":
		return "Record<string, " + p.tsType(t.args[1]) + ">"
	}
	return t.name
}

func tsComment(b *bytes.Buffer, indent, doc string) {
	if doc != "" {
		fmt.Fprintf(b, "%s/** %s */\n", indent, doc)
	}
}

func tsFile(p *proto) []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated by protogen from the spec package's language/interop/protocol_messages.heron. DO NOT EDIT.\n\n")
	for _, c := range p.consts {
		tsComment(&b, "", c.doc)
		fmt.Fprintf(&b, "export const %s = %s;\n\n", tsName(c.name), c.value)
	}
	for _, e := range p.enums {
		tsComment(&b, "", e.doc)
		var vs []string
		for _, v := range e.variants {
			vs = append(vs, fmt.Sprintf("%q", v))
		}
		fmt.Fprintf(&b, "export type %s = %s;\n\n", e.name, strings.Join(vs, " | "))
	}
	for _, r := range p.records {
		tsComment(&b, "", r.doc)
		if len(r.fields) == 0 {
			fmt.Fprintf(&b, "export type %s = Record<string, never>;\n\n", r.name)
			continue
		}
		fmt.Fprintf(&b, "export interface %s {\n", r.name)
		for _, f := range r.fields {
			tsComment(&b, "  ", f.doc)
			q := ""
			if f.ty.optional {
				q = "?"
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", f.name, q, p.tsType(f.ty))
		}
		b.WriteString("}\n\n")
	}
	seen := map[string]bool{}
	b.WriteString("/** The methods, by wire name. */\nexport const methods = {\n")
	for _, m := range p.methods {
		if seen[m.wire] {
			continue
		}
		seen[m.wire] = true
		fmt.Fprintf(&b, "  %s: %q,\n", tsName(m.name), m.wire)
	}
	b.WriteString("} as const;\n\n")
	for _, iface := range []string{"Extension", "Host", "Notifications"} {
		fmt.Fprintf(&b, "/** The params and result of each method of %s, by wire name. */\nexport interface %sMethods {\n", iface, iface)
		var names []string
		lines := map[string]string{}
		for _, m := range p.methods {
			if m.iface != iface {
				continue
			}
			res := "void"
			if m.result != nil {
				res = p.tsType(*m.result)
				if m.wire == "node" {
					res += " | null"
				}
			}
			names = append(names, m.wire)
			lines[m.wire] = fmt.Sprintf("  %q: { params: %s; result: %s };\n", m.wire, p.tsType(m.params), res)
		}
		sort.Strings(names)
		for _, n := range names {
			b.WriteString(lines[n])
		}
		b.WriteString("}\n\n")
	}
	return b.Bytes()
}
