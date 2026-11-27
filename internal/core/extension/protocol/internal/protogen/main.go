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
	"github.com/aiviarium/sparrow-rpc/go/schema"
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
	iface        string // Extension or Host
	name         string // the func's name
	wire         string
	doc          string
	params       typeRef
	result       *typeRef
	notification bool
	// stream is a result of Stream(T): result is T.
	stream bool
}

type proto struct {
	consts  []constant
	records []record
	enums   []enum
	methods []method
	// order is the records' and enums' names in the order the file
	// declares them, which numbers their Sparrow types.
	order []string
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
	facets, err := p.facets()
	if err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
	src, err := goFile(p, facets)
	if err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*goOut, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*tsOut, tsFile(p, facets), 0o644); err != nil {
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
			p.order = append(p.order, n.Name)
		case isAPI(c, n):
			// Each side's methods: an api is a facet, its ops its
			// methods by name, one without a result a notification and
			// one whose result is a Stream a server stream.
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
			p.order = append(p.order, n.Name)
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
	if ps := d.Params().Params(); len(ps) == 1 {
		m.params = refOf(ps[0].Type(), false)
	}
	m.notification = true
	if nt, ok := d.Type().(ast.NamedType); ok {
		for _, a := range nt.Args().Args() {
			if pa, ok := a.(ast.PositionalArg); ok {
				r := exprRef(pa.Expr())
				if r.name == "Stream" && len(r.args) == 1 {
					r = r.args[0]
					m.stream = true
				}
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

// facetName names an api's facet: heron.<api>.
func facetName(api string) string { return "heron." + strings.ToLower(api) }

// facetDef is one api's facet, built from the messages, with its canonical
// CBOR.
type facetDef struct {
	iface string
	name  string
	def   *schema.Facet
	cbor  []byte
	hash  []byte
}

// The Sparrow types every facet that reaches Any defines first: the Value
// union and the struct VariantValue, as data_mapping's table gives them.
const (
	valueID   = schema.FirstFreeTypeID
	variantID = schema.FirstFreeTypeID + 1
)

func valueTypes() []schema.TypeDef {
	v := schema.Named(valueID)
	str := schema.Named(schema.TypeString)
	variants := []struct {
		name string
		t    schema.TypeRef
	}{
		{"null", schema.Named(schema.TypeUnit)},
		{"bool", schema.Named(schema.TypeBool)},
		{"int", schema.Named(schema.TypeInt)},
		{"uint", schema.Named(schema.TypeUint)},
		{"bigint", str},
		{"float", schema.Named(schema.TypeFloat64)},
		{"string", str},
		{"bytes", schema.Named(schema.TypeBytes)},
		{"decimal", str},
		{"time", str},
		{"duration", schema.Named(schema.TypeInt)},
		{"list", schema.List(v)},
		{"set", schema.List(v)},
		{"map", schema.MapOf(str, v)},
		{"variant", schema.Named(variantID)},
		{"ref", str},
		{"prose", schema.MapOf(str, v)},
	}
	u := schema.TypeDef{Kind: schema.KindUnion, ID: valueID, Name: "Value", Annotations: schema.Annotations{}}
	for i, x := range variants {
		u.Fields = append(u.Fields, schema.Field{Tag: uint64(i), Name: x.name, Type: x.t, Annotations: schema.Annotations{}})
	}
	vv := schema.TypeDef{Kind: schema.KindStruct, ID: variantID, Name: "VariantValue", Annotations: schema.Annotations{}, Fields: []schema.Field{
		{Tag: 0, Name: "name", Type: str, Annotations: schema.Annotations{}},
		{Tag: 1, Name: "value", Type: v, Annotations: schema.Annotations{}},
	}}
	return []schema.TypeDef{u, vv}
}

// facets builds the facets heron.extension and heron.host. Types are
// numbered from 66 in the order the file declares them, after Value and
// VariantValue, and enum values' sigils from 64 likewise, so a type has
// the same ID in both facets; each facet defines the types its methods
// reach, and ErrorDetails, which its errors carry.
func (p *proto) facets() ([]facetDef, error) {
	typeID := map[string]uint64{}
	sigils := map[string][]uint64{}
	nextSigil := schema.FirstFreeSigil
	for i, n := range p.order {
		typeID[n] = schema.FirstFreeTypeID + 2 + uint64(i)
		for _, e := range p.enums {
			if e.name == n {
				for range e.variants {
					sigils[n] = append(sigils[n], nextSigil)
					nextSigil++
				}
			}
		}
	}
	var ref func(t typeRef) (schema.TypeRef, error)
	ref = func(t typeRef) (schema.TypeRef, error) {
		var r schema.TypeRef
		switch t.name {
		case "String":
			r = schema.Named(schema.TypeString)
		case "Int":
			r = schema.Named(schema.TypeInt)
		case "Bool":
			r = schema.Named(schema.TypeBool)
		case "Float":
			r = schema.Named(schema.TypeFloat64)
		case "Bytes":
			r = schema.Named(schema.TypeBytes)
		case "Any":
			r = schema.Named(valueID)
		case "List":
			e, err := ref(t.args[0])
			if err != nil {
				return r, err
			}
			r = schema.List(e)
		case "Map":
			e, err := ref(t.args[1])
			if err != nil {
				return r, err
			}
			r = schema.MapOf(schema.Named(schema.TypeString), e)
		default:
			id, ok := typeID[t.name]
			if !ok {
				return r, fmt.Errorf("no type %s for the facets", t.name)
			}
			r = schema.Named(id)
		}
		return r, nil
	}
	defs := map[string]schema.TypeDef{}
	for _, rec := range p.records {
		td := schema.TypeDef{Kind: schema.KindStruct, ID: typeID[rec.name], Name: rec.name, Fields: []schema.Field{}, Annotations: schema.Annotations{}}
		for i, f := range rec.fields {
			t, err := ref(f.ty)
			if err != nil {
				return nil, err
			}
			fl := schema.Field{Tag: uint64(i), Name: f.name, Type: t, Annotations: schema.Annotations{}}
			if f.ty.optional {
				fl.Flags |= schema.FieldOptional
			}
			td.Fields = append(td.Fields, fl)
		}
		defs[rec.name] = td
	}
	sigilDefs := map[string][]schema.Sigil{}
	for _, e := range p.enums {
		td := schema.TypeDef{Kind: schema.KindEnum, ID: typeID[e.name], Name: e.name, Values: sigils[e.name], Annotations: schema.Annotations{}}
		defs[e.name] = td
		for i, v := range e.variants {
			sigilDefs[e.name] = append(sigilDefs[e.name], schema.Sigil{ID: sigils[e.name][i], Name: v, Kind: schema.SigilEnumValue, Annotations: schema.Annotations{}})
		}
	}
	byID := map[uint64]string{}
	for n, id := range typeID {
		byID[id] = n
	}
	var out []facetDef
	for _, iface := range []string{"Extension", "Host"} {
		f := &schema.Facet{ID: schema.FirstFreeFacetID, Name: facetName(iface), Version: majorOf(p.version()) + ".0.0",
			Types: []schema.TypeDef{}, Services: []schema.Service{}, Sigils: []schema.Sigil{}, Retired: []schema.Retired{}, Annotations: schema.Annotations{}}
		svc := schema.Service{ID: 0, Name: strings.ToLower(iface), Methods: []schema.Method{}, Annotations: schema.Annotations{}}
		reached := map[string]bool{}
		usesValue := false
		var reach func(t schema.TypeRef)
		reach = func(t schema.TypeRef) {
			switch t.Kind {
			case schema.RefList, schema.RefOptional, schema.RefStream:
				reach(*t.Elem)
				return
			case schema.RefMap:
				reach(*t.Key)
				reach(*t.Elem)
				return
			case schema.RefByteStream:
				return
			}
			if t.ID == valueID {
				usesValue = true
				return
			}
			n, ok := byID[t.ID]
			if !ok || reached[n] {
				return
			}
			reached[n] = true
			for _, fl := range defs[n].Fields {
				reach(fl.Type)
			}
		}
		reach(schema.Named(typeID["ErrorDetails"]))
		for _, m := range p.methods {
			if m.iface != iface {
				continue
			}
			in, err := ref(m.params)
			if err != nil {
				return nil, err
			}
			sm := schema.Method{ID: uint64(len(svc.Methods)), Name: m.wire, Input: in, Output: schema.Named(schema.TypeUnit), Errors: []uint64{}, Annotations: schema.Annotations{}}
			switch {
			case m.notification:
				sm.Flags |= schema.MethodNotification
			case m.stream:
				o, err := ref(*m.result)
				if err != nil {
					return nil, err
				}
				sm.Output = schema.Stream(o)
				sm.Flags |= schema.MethodOutputEnd
			default:
				o, err := ref(*m.result)
				if err != nil {
					return nil, err
				}
				// A record result may be null, as node's when nothing is
				// at the path.
				if p.isRecord(m.result.name) {
					o = schema.Optional(o)
				}
				sm.Output = o
			}
			reach(sm.Input)
			reach(sm.Output)
			svc.Methods = append(svc.Methods, sm)
		}
		if usesValue {
			f.Types = append(f.Types, valueTypes()...)
		}
		for _, n := range p.order {
			if reached[n] {
				f.Types = append(f.Types, defs[n])
				f.Sigils = append(f.Sigils, sigilDefs[n]...)
			}
		}
		f.Services = append(f.Services, svc)
		if err := f.Validate(); err != nil {
			return nil, fmt.Errorf("facet %s: %w", f.Name, err)
		}
		b, err := f.Canonical().Encode()
		if err != nil {
			return nil, err
		}
		h, err := f.Hash()
		if err != nil {
			return nil, err
		}
		out = append(out, facetDef{iface: iface, name: f.Name, def: f, cbor: b, hash: h})
	}
	return out, nil
}

func (p *proto) version() string {
	for _, c := range p.consts {
		if c.name == "version" {
			return strings.Trim(c.value, `"`)
		}
	}
	return "0"
}

func majorOf(v string) string { return strings.SplitN(v, ".", 2)[0] }

func goBytes(b []byte) string {
	var s strings.Builder
	for i, x := range b {
		if i%16 == 0 {
			s.WriteString("\n\t")
		} else {
			s.WriteString(" ")
		}
		fmt.Fprintf(&s, "0x%02x,", x)
	}
	return s.String() + "\n"
}

func goFile(p *proto, facets []facetDef) ([]byte, error) {
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
			fmt.Fprintf(&b, "\t%s %s `json:%q`\n", goName(f.name), p.goType(f.ty, false), tag)
		}
		b.WriteString("}\n\n")
	}
	b.WriteString("// The facets, by name.\nconst (\n")
	for _, f := range facets {
		fmt.Fprintf(&b, "\tFacet%s = %q\n", f.iface, f.name)
	}
	b.WriteString(")\n\n")
	for _, f := range facets {
		fmt.Fprintf(&b, "// facet%sCBOR is the facet %s in canonical CBOR; its facet hash is %x.\nvar facet%sCBOR = []byte{%s}\n\n", f.iface, f.name, f.hash, f.iface, goBytes(f.cbor))
	}
	// Method names, once per wire name.
	seen := map[string]bool{}
	b.WriteString("// The methods, by name in their facet.\nconst (\n")
	for _, m := range p.methods {
		if seen[m.wire] {
			continue
		}
		seen[m.wire] = true
		fmt.Fprintf(&b, "\tMethod%s = %q\n", goName(m.name), m.wire)
	}
	b.WriteString(")\n\n")
	b.WriteString("// Caller sends a request and decodes its result.\ntype Caller interface {\n\tCall(ctx context.Context, method string, params, result any) error\n}\n\n")
	b.WriteString("// StreamCaller sends a request whose result streams, and decodes each item.\ntype StreamCaller interface {\n\tCallStream(ctx context.Context, method string, params any, item func(decode func(any) error) error) error\n}\n\n")
	b.WriteString("// Notifier sends a notification.\ntype Notifier interface {\n\tNotify(method string, params any) error\n}\n\n")
	b.WriteString("// Handler serves one method: it decodes its params and returns its result. A\n// handler of a streamed method sends each item with StreamItem.\ntype Handler func(ctx context.Context, decode func(any) error) (any, error)\n\n")
	for _, iface := range []string{"Extension", "Host"} {
		fmt.Fprintf(&b, "// %s is what the facet %s serves.\ntype %s interface {\n", iface, facetName(iface), iface)
		for _, m := range p.methods {
			if m.iface != iface || m.notification {
				continue
			}
			comment(&b, "\t", m.doc)
			fmt.Fprintf(&b, "\t%s(ctx context.Context, p *%s) (%s, error)\n", goName(m.name), goName(m.params.name), p.resultGo(m))
		}
		b.WriteString("}\n\n")
		fmt.Fprintf(&b, "// %sClient calls a peer that serves %s.\ntype %sClient struct{ C Caller }\n\n", iface, iface, iface)
		for _, m := range p.methods {
			if m.iface != iface || m.notification {
				continue
			}
			res := p.resultGo(m)
			if m.stream {
				fmt.Fprintf(&b, "func (c %sClient) %s(ctx context.Context, p *%s, item func(%s) error) error {\n", iface, goName(m.name), goName(m.params.name), res)
				fmt.Fprintf(&b, "\treturn c.C.(StreamCaller).CallStream(ctx, %q, p, func(decode func(any) error) error {\n\t\tvar v %s\n\t\tif err := decode(&v); err != nil {\n\t\t\treturn err\n\t\t}\n\t\treturn item(v)\n\t})\n}\n\n", m.wire, strings.TrimPrefix(res, "*"))
				continue
			}
			fmt.Fprintf(&b, "func (c %sClient) %s(ctx context.Context, p *%s) (%s, error) {\n", iface, goName(m.name), goName(m.params.name), res)
			if strings.HasPrefix(res, "*") {
				fmt.Fprintf(&b, "\tvar out %s\n\terr := c.C.Call(ctx, %q, p, &out)\n\treturn &out, err\n}\n\n", res[1:], m.wire)
			} else {
				fmt.Fprintf(&b, "\tvar out %s\n\terr := c.C.Call(ctx, %q, p, &out)\n\treturn out, err\n}\n\n", res, m.wire)
			}
		}
		fmt.Fprintf(&b, "// %sHandlers serves an implementation of %s by method name.\nfunc %sHandlers(impl %s) map[string]Handler {\n\treturn map[string]Handler{\n", iface, iface, iface, iface)
		for _, m := range p.methods {
			if m.iface != iface || m.notification {
				continue
			}
			fmt.Fprintf(&b, "\t\t%q: func(ctx context.Context, decode func(any) error) (any, error) {\n\t\t\tvar p %s\n\t\t\tif err := decode(&p); err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n\t\t\treturn impl.%s(ctx, &p)\n\t\t},\n", m.wire, goName(m.params.name), goName(m.name))
		}
		b.WriteString("\t}\n}\n\n")
		for _, m := range p.methods {
			if m.iface != iface || !m.notification {
				continue
			}
			comment(&b, "", m.doc)
			fmt.Fprintf(&b, "func Notify%s(n Notifier, p *%s) error { return n.Notify(%q, p) }\n\n", goName(m.name), goName(m.params.name), m.wire)
		}
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

func tsFile(p *proto, facets []facetDef) []byte {
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
	b.WriteString("/** The facets, by name. */\nexport const facets = {\n")
	for _, f := range facets {
		fmt.Fprintf(&b, "  %s: %q,\n", tsName(f.iface), f.name)
	}
	b.WriteString("} as const;\n\n")
	for _, f := range facets {
		fmt.Fprintf(&b, "/** The facet %s in canonical CBOR, as hex; its facet hash is %x. */\nexport const facet%sHex =\n", f.name, f.hash, f.iface)
		h := fmt.Sprintf("%x", f.cbor)
		var parts []string
		for len(h) > 96 {
			parts = append(parts, fmt.Sprintf("  %q", h[:96]))
			h = h[96:]
		}
		parts = append(parts, fmt.Sprintf("  %q", h))
		b.WriteString(strings.Join(parts, " +\n") + ";\n\n")
	}
	seen := map[string]bool{}
	b.WriteString("/** The methods, by name in their facet. */\nexport const methods = {\n")
	for _, m := range p.methods {
		if seen[m.wire] {
			continue
		}
		seen[m.wire] = true
		fmt.Fprintf(&b, "  %s: %q,\n", tsName(m.name), m.wire)
	}
	b.WriteString("} as const;\n\n")
	for _, iface := range []string{"Extension", "Host"} {
		fmt.Fprintf(&b, "/** The params and result of each method of %s, by name; a stream's result is its items' type. */\nexport interface %sMethods {\n", facetName(iface), iface)
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
			kind := "unary"
			switch {
			case m.notification:
				kind = "notification"
			case m.stream:
				kind = "stream"
			}
			names = append(names, m.wire)
			lines[m.wire] = fmt.Sprintf("  %q: { params: %s; result: %s; kind: %q };\n", m.wire, p.tsType(m.params), res, kind)
		}
		sort.Strings(names)
		for _, n := range names {
			b.WriteString(lines[n])
		}
		b.WriteString("}\n\n")
	}
	return b.Bytes()
}
