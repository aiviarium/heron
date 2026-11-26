// Package rpc projects a tree's apis onto their RPC bindings: gRPC
// services, served over gRPC, gRPC-Web and Connect, with their methods,
// messages and enums, by std/proto; and JSON-RPC methods by std/jsonrpc.
// The docs, the proto and openrpc generators and the checker all read the
// one projection built here, so every name and number agrees.
package rpc

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// Kind is an op's streaming kind.
type Kind int

const (
	Unary Kind = iota
	ServerStreaming
	ClientStreaming
	BidiStreaming
)

func (k Kind) String() string {
	return [...]string{"unary", "server streaming", "client streaming", "bidirectional streaming"}[k]
}

// Key is the kind as a word, for classes and data.
func (k Kind) Key() string {
	return [...]string{"unary", "server", "client", "bidi"}[k]
}

// Model is the RPC projection of a tree.
type Model struct {
	Services []*Service
	// Packages holds each protobuf package's messages and enums, in the
	// order its services reach them.
	Packages map[string]*Package
	// JSONAPIs are the apis with JSON-RPC methods, in tree order.
	JSONAPIs []*JSONAPI
	methods  map[*resolve.Node]*Method
	jsonOps  map[*resolve.Node]*JSONMethod
	// typeIn maps a Heron type to the package and message or enum it is.
	typeIn map[*resolve.Node]*Message
	enumIn map[*resolve.Node]*Enum

	Diags diag.List
	// Lints are the findings `heron lint` reports: errors without a code
	// on a binding and JSON-RPC codes in the reserved range.
	Lints []Finding

	r           *reader
	implementor map[*resolve.Node][]*resolve.Node
}

// Package is one protobuf package: its services, and the messages and
// enums they reach.
type Package struct {
	Name     string
	Services []*Service
	Messages []*Message
	Enums    []*Enum
	names    map[string]bool
	byKey    map[string]*Message
	enums    map[*resolve.Node]*Enum
	// imports are the well-known types' files the package uses.
	imports map[string]bool
}

// Service is a gRPC service, an api with @proto(package).
type Service struct {
	API       *resolve.Node
	Package   string
	Name      string
	Protocols []string // grpc, grpc_web, connect
	Methods   []*Method
	Servers   []string
	Doc       string
}

// FullName is the service's fully qualified name.
func (s *Service) FullName() string { return s.Package + "." + s.Name }

// Has reports whether the service is served over a protocol.
func (s *Service) Has(p string) bool {
	for _, x := range s.Protocols {
		if x == p {
			return true
		}
	}
	return false
}

// Method is one op as a gRPC method.
type Method struct {
	Op          *resolve.Node
	Service     *Service
	Name        string
	Kind        Kind
	Request     *Message
	Response    *Message
	Metadata    []Metadata
	Idempotency string // "", NO_SIDE_EFFECTS or IDEMPOTENT
	Timeout     string
	Errors      []Error
}

// Path is the method's path, /<package>.<Service>/<Method>.
func (m *Method) Path() string { return "/" + m.Service.FullName() + "/" + m.Name }

// Metadata is a parameter that travels as gRPC metadata.
type Metadata struct {
	Param  string
	Name   string
	Binary bool
	Doc    string
	Type   string
}

// Message is a protobuf message: a Heron type's, or a method's request or
// response.
type Message struct {
	Name     string
	Package  string
	Node     *resolve.Node // the Heron type; nil for a request or response
	Method   *Method       // the method whose request or response it is
	Doc      string
	Fields   []*Field
	Oneof    string // the oneof all fields stand in, for a union
	Reserved []int
	Error    bool
}

// FullName is the message's fully qualified name.
func (m *Message) FullName() string { return m.Package + "." + m.Name }

// Field is one field of a message.
type Field struct {
	Name     string
	JSON     string
	Number   int
	Label    string // "", "optional" or "repeated"
	Type     string // as the .proto writes it: string, Parcel, map<string, int64>
	Message  *Message
	Enum     *Enum
	Node     *resolve.Node // the member, parameter or variant
	Doc      string
	Optional bool
	// Scalar is the protobuf scalar type, or the well-known type's name.
	Scalar string
	// Map's value field, for a map<K, V>.
	MapValue *Field
	// Stream marks the field a streamed parameter's or result's element is.
	Stream bool
}

// Enum is a protobuf enum.
type Enum struct {
	Name    string
	Package string
	Node    *resolve.Node
	Doc     string
	Values  []EnumValue
}

// EnumValue is one value of an enum.
type EnumValue struct {
	Name    string // PARCEL_STATUS_IN_TRANSIT
	Variant string // IN_TRANSIT, the Heron variant's name
	Number  int
	Doc     string
}

// Error is an error an op throws, with its projections.
type Error struct {
	Node      *resolve.Node
	IsNode    bool   // an error node of std/spec rather than a type
	ID        string // an error node's id
	Status    string // HTTP
	GRPC      string // the gRPC code's name, as NOT_FOUND; "" without @code
	JSONRPC   string // the JSON-RPC code, as -32004; "" without @code
	Message   string // the error's message, or an error node's title
	Fields    []ErrorField
	Detail    *Message // the error detail message of a type, in the service's package
	Throws    resolve.Site
	FromAPI   bool
	JSONCodes resolve.Site
}

// ErrorField is a field an error carries.
type ErrorField struct {
	Name string
	Type string
}

// JSONAPI is an api with JSON-RPC methods.
type JSONAPI struct {
	API      *resolve.Node
	Framing  string // http, content_length, length_prefixed or websocket
	Encoding string // json or cbor
	Batch    bool
	Servers  []string
	Methods  []*JSONMethod
	// Bound reports whether the api's own @jsonrpc binds every op.
	Bound bool
}

// JSONMethod is one op as a JSON-RPC method.
type JSONMethod struct {
	Op           *resolve.Node
	API          *JSONAPI
	Name         string
	Notification bool
	NullResult   bool
	ByPosition   bool
	Params       []JSONParam
	// Whole is the @params parameter, whose value is the whole of params.
	Whole  *JSONParam
	Result Type
	Errors []Error
}

// JSONParam is a parameter of a JSON-RPC method.
type JSONParam struct {
	Name     string
	Type     Type
	Optional bool
	Doc      string
}

// Method returns an op's gRPC method, or nil.
func (m *Model) Method(op *resolve.Node) *Method { return m.methods[op] }

// JSONMethod returns an op's JSON-RPC method, or nil.
func (m *Model) JSONMethod(op *resolve.Node) *JSONMethod { return m.jsonOps[op] }

// MessageOf returns the message a Heron type is, or nil.
func (m *Model) MessageOf(t *resolve.Node) *Message { return m.typeIn[t] }

// EnumOf returns the enum a Heron enum is, or nil.
func (m *Model) EnumOf(t *resolve.Node) *Enum { return m.enumIn[t] }

// ServiceOf returns the gRPC service of an api, or nil.
func (m *Model) ServiceOf(api *resolve.Node) *Service {
	for _, s := range m.Services {
		if s.API == api {
			return s
		}
	}
	return nil
}

// JSONAPIOf returns the JSON-RPC side of an api, or nil.
func (m *Model) JSONAPIOf(api *resolve.Node) *JSONAPI {
	for _, a := range m.JSONAPIs {
		if a.API == api {
			return a
		}
	}
	return nil
}

// StreamKind is an op's streaming kind, whatever binds it.
func StreamKind(c *types.Checker, op *resolve.Node) Kind {
	r := &reader{c: c}
	in, out := false, false
	params, result := r.opSignature(op)
	for _, p := range params {
		if p.typ.IsStream() {
			in = true
		}
	}
	out = result.IsStream()
	switch {
	case in && out:
		return BidiStreaming
	case in:
		return ClientStreaming
	case out:
		return ServerStreaming
	}
	return Unary
}

type param struct {
	name     string
	typ      Type
	optional bool
	doc      string
	syn      ast.Param
	anns     []ast.Annotation
	site     resolve.Site
}

// opSignature reads an op's parameters and result type.
func (r *reader) opSignature(op *resolve.Node) ([]param, Type) {
	var ps []param
	var result Type
	if op.Decl.N == nil || op.Decl.N.Kind != syntax.Declare {
		return nil, result
	}
	d := ast.Declare{N: op.Decl.N}
	site := resolve.Site{File: op.Decl.File, N: op.Decl.N, Scope: op.Decl.Scope}
	for _, p := range d.Params().Params() {
		if p.Ident() == nil {
			continue
		}
		ps = append(ps, param{
			name: p.Ident().Text, typ: r.readType(p.Type(), site, nil),
			optional: p.Question() != nil || p.Default() != nil,
			doc:      docText(p.Doc()), syn: p, anns: p.Annotations(),
			site: resolve.Site{File: op.Decl.File, N: p.N, Scope: op.Decl.Scope},
		})
	}
	if t, ok := d.Type().(ast.NamedType); ok {
		args := t.Args()
		if es := project.ArgExprs(args); len(es) > 0 {
			result = r.readTypeExpr(es[0], site, nil)
		} else if e := project.NamedArg(args, "returns"); e != nil {
			result = r.readTypeExpr(e, site, nil)
		}
	}
	return ps, result
}

// ann is an annotation with the package that declares it.
type ann struct {
	a    ast.Annotation
	pkg  string // proto, jsonrpc, http, ws, apis, types…
	name string
	site resolve.Site
}

// annsOf reads a node's annotations, each with its declaring package.
func (r *reader) annsOf(n *resolve.Node) []ann {
	var out []ann
	for _, s := range append(append([]resolve.Site(nil), n.Annotations...), n.GroupAnnotations...) {
		if s.N == nil || s.N.Kind != syntax.Annotation {
			continue
		}
		out = append(out, r.annAt(ast.Annotation{N: s.N}, s))
	}
	return out
}

func (r *reader) annAt(a ast.Annotation, site resolve.Site) ann {
	segs := resolve.PathSegs(a.Path().N)
	x := ann{a: a, name: last(segs), site: resolve.Site{File: site.File, N: a.N, Scope: site.Scope}}
	d := r.c.Refs[a.Path().N]
	if d == nil && site.Scope != nil && len(segs) > 0 {
		site.Scope.Each(segs[0], site.File, -1, func(b resolve.Binding) bool {
			n := b.Node
			for _, s := range segs[1:] {
				if n == nil {
					break
				}
				n = n.Child(s)
			}
			if n != nil && r.c.IsAnnotation(n) {
				d = n
				return false
			}
			return true
		})
	}
	if d != nil && d.Parent != nil {
		x.pkg = d.Parent.Name
	} else if len(segs) > 1 {
		x.pkg = segs[0]
	}
	return x
}

func find(as []ann, pkg, name string) *ann {
	for i := range as {
		if as[i].name == name && (as[i].pkg == pkg || as[i].pkg == "") {
			return &as[i]
		}
	}
	return nil
}

// arg returns a named argument, or a positional one at index (-1 for
// none), with the site of its expression.
func arg(a ast.Annotation, index int, name string) ast.Expr {
	args := a.Args()
	if args.N == nil {
		return nil
	}
	pos := 0
	for _, x := range args.Args() {
		switch x := x.(type) {
		case ast.PositionalArg:
			if pos == index {
				return x.Expr()
			}
			pos++
		case ast.NamedArg:
			if x.Ident() != nil && x.Ident().Text == name {
				return x.Expr()
			}
		}
	}
	return nil
}

func exprString(e ast.Expr) string {
	if e == nil {
		return ""
	}
	if v, ok := resolve.StringValue(e.Syntax()); ok {
		return v
	}
	return strings.TrimSpace(e.Syntax().Text())
}

// variantName reads an enum value written bare or qualified, as
// no_side_effects or Idempotency.no_side_effects.
func variantName(e ast.Expr) string {
	if e == nil {
		return ""
	}
	t := strings.TrimSpace(e.Syntax().Text())
	if i := strings.LastIndex(t, "."); i >= 0 {
		t = t[i+1:]
	}
	return t
}

func variantList(e ast.Expr) []string {
	if e == nil {
		return nil
	}
	if a, ok := e.(ast.ArrayExpr); ok {
		var out []string
		for _, x := range a.Exprs() {
			out = append(out, variantName(x))
		}
		return out
	}
	return []string{variantName(e)}
}

func siteOf(base resolve.Site, n *syntax.Node) resolve.Site {
	return resolve.Site{File: base.File, N: n, Scope: base.Scope}
}

func (m *Model) errorf(s resolve.Site, code, format string, args ...any) {
	start, end := s.Span()
	file := ""
	if s.File != nil {
		file = string(s.File.ID)
	}
	m.Diags.Errorf(file, start, end, code, format, args...)
}

// Finding is a lint finding about a node.
type Finding struct {
	Node    *resolve.Node
	Site    resolve.Site
	Message string
}

func (m *Model) warnf(n *resolve.Node, s resolve.Site, format string, args ...any) {
	m.Lints = append(m.Lints, Finding{Node: n, Site: s, Message: fmt.Sprintf(format, args...)})
}

var packageName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// Build projects the tree's own apis onto their RPC bindings.
func Build(t *resolve.Tree, c *types.Checker) *Model {
	m := &Model{
		Packages: map[string]*Package{},
		methods:  map[*resolve.Node]*Method{},
		jsonOps:  map[*resolve.Node]*JSONMethod{},
		typeIn:   map[*resolve.Node]*Message{},
		enumIn:   map[*resolve.Node]*Enum{},
		r:        &reader{c: c},
	}
	var apis []*resolve.Node
	m.implementor = map[*resolve.Node][]*resolve.Node{}
	t.Walk(func(n *resolve.Node) {
		if !Own(n) || n.Name == "" {
			return
		}
		switch project.KindPath(c, n) {
		case "types.api":
			apis = append(apis, n)
		case "types.op":
			if n.Parent == nil || project.KindPath(c, n.Parent) != "types.api" {
				apis = append(apis, nil) // placeholder: a lone op has no service
			}
		}
		if c.IsType(n) {
			for _, i := range c.Implements(n) {
				m.implementor[i] = append(m.implementor[i], n)
			}
		}
	})
	// Types the tree is checked against, such as a fixture's context, can
	// implement the interfaces its ops throw.
	for sc := t.Context; sc != nil; sc = sc.Parent {
		if sc.Node == nil {
			continue
		}
		var walk func(n *resolve.Node)
		walk = func(n *resolve.Node) {
			for _, ch := range n.Children {
				if c.IsType(ch) {
					for _, i := range c.Implements(ch) {
						m.implementor[i] = append(m.implementor[i], ch)
					}
				}
				walk(ch)
			}
		}
		walk(sc.Node)
	}
	for _, a := range apis {
		if a != nil {
			m.buildService(a)
			m.buildJSON(a)
		}
	}
	m.reservedCodes(t)
	m.Diags = m.Diags.Sort()
	return m
}

// servers reads an api's @server URLs.
func (m *Model) servers(as []ann) []string {
	var out []string
	for _, a := range as {
		if a.name == "server" && (a.pkg == "apis" || a.pkg == "") {
			if v := exprString(arg(a.a, 0, "url")); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// HTTPBound reports whether an op has an HTTP route or a socket.
func HTTPBound(c *types.Checker, op *resolve.Node) bool {
	r := &reader{c: c}
	for _, a := range r.annsOf(op) {
		if a.name == "http" && (a.pkg == "http" || a.pkg == "") {
			if e := arg(a.a, 0, "path"); e != nil {
				return true
			}
		}
		if a.name == "ws" && (a.pkg == "ws" || a.pkg == "") {
			return true
		}
	}
	if op.Parent != nil {
		if find(r.annsOf(op.Parent), "ws", "ws") != nil {
			return true
		}
	}
	return false
}

func (m *Model) buildService(api *resolve.Node) {
	r := m.r
	as := r.annsOf(api)
	pa := find(as, "proto", "proto")
	ops := m.opsOf(api)
	if pa == nil {
		for _, op := range ops {
			if a := find(r.annsOf(op), "proto", "proto"); a != nil {
				m.errorf(a.site, "proto", "%s carries @proto, but its api names no protobuf package", op.Name)
			}
		}
		return
	}
	for _, name := range []string{"method", "idempotency", "timeout"} {
		if e := arg(pa.a, -1, name); e != nil {
			m.errorf(siteOf(pa.site, e.Syntax()), "proto", "@proto on api doesn't take %s", name)
		}
	}
	pkgExpr := arg(pa.a, 0, "package")
	if pkgExpr == nil {
		for _, op := range ops {
			if a := find(r.annsOf(op), "proto", "proto"); a != nil {
				m.errorf(a.site, "proto", "%s carries @proto, but its api names no protobuf package", op.Name)
			}
		}
		return
	}
	pkgName := exprString(pkgExpr)
	if !packageName.MatchString(pkgName) {
		m.errorf(siteOf(pa.site, pkgExpr.Syntax()), "proto", "%s isn't a protobuf package: write lower-case words joined by dots, as acme.parcels.v1", pkgName)
		return
	}
	s := &Service{API: api, Package: pkgName, Doc: DocOf(api), Servers: m.servers(as)}
	s.Name = exprString(arg(pa.a, -1, "service"))
	if s.Name == "" {
		s.Name = Pascal(api.Name)
		if !strings.HasSuffix(s.Name, "Service") {
			s.Name += "Service"
		}
	}
	s.Protocols = variantList(arg(pa.a, -1, "protocols"))
	if len(s.Protocols) == 0 {
		s.Protocols = []string{"grpc"}
	}
	pkg := m.Packages[pkgName]
	if pkg == nil {
		pkg = &Package{Name: pkgName, names: map[string]bool{}, byKey: map[string]*Message{}, enums: map[*resolve.Node]*Enum{}, imports: map[string]bool{}}
		m.Packages[pkgName] = pkg
	}
	pkg.Services = append(pkg.Services, s)
	m.Services = append(m.Services, s)

	type pending struct {
		meth   *Method
		params []param
		result Type
	}
	var todo []pending
	for _, op := range ops {
		oas := r.annsOf(op)
		meth := &Method{Op: op, Service: s, Name: Pascal(op.Name), Kind: StreamKind(r.c, op)}
		if a := find(oas, "proto", "proto"); a != nil {
			for _, name := range []string{"package", "service", "protocols"} {
				idx := -1
				if name == "package" {
					idx = 0
				}
				if e := arg(a.a, idx, name); e != nil {
					m.errorf(siteOf(a.site, e.Syntax()), "proto", "@proto on op doesn't take %s", name)
				}
			}
			if v := exprString(arg(a.a, -1, "method")); v != "" {
				meth.Name = v
			}
			switch variantName(arg(a.a, -1, "idempotency")) {
			case "no_side_effects":
				meth.Idempotency = "NO_SIDE_EFFECTS"
			case "idempotent":
				meth.Idempotency = "IDEMPOTENT"
			}
			meth.Timeout = exprString(arg(a.a, -1, "timeout"))
		}
		params, result := r.opSignature(op)
		// Metadata: @metadata, and @header for an op bound to HTTP too.
		var fields []param
		streamed := false
		for _, p := range params {
			if p.typ.IsStream() {
				streamed = true
			}
		}
		for _, p := range params {
			var md *Metadata
			for _, a := range p.anns {
				x := r.annAt(a, p.site)
				switch {
				case x.name == "metadata" && (x.pkg == "proto" || x.pkg == ""):
					md = &Metadata{Param: p.name, Name: exprString(arg(a, 0, "name"))}
				case x.name == "header" && (x.pkg == "http" || x.pkg == ""):
					md = &Metadata{Param: p.name, Name: strings.ToLower(exprString(arg(a, 0, "name")))}
				}
			}
			if md != nil {
				if md.Name == "" {
					md.Name = p.name
				}
				if p.typ.Name == "Bytes" {
					md.Binary = true
					if !strings.HasSuffix(md.Name, "-bin") {
						md.Name += "-bin"
					}
				}
				md.Doc, md.Type = p.doc, p.typ.Text
				meth.Metadata = append(meth.Metadata, *md)
				continue
			}
			if streamed && !p.typ.IsStream() {
				m.errorf(p.site, "proto", "%s streams from the client, so %s can only travel as @metadata", op.Name, p.name)
				continue
			}
			fields = append(fields, p)
		}
		m.methods[op] = meth
		s.Methods = append(s.Methods, meth)
		todo = append(todo, pending{meth, fields, result})
	}
	// The types the methods reach become messages first, so a request or
	// response that would take a type's name takes the service's too.
	for _, p := range todo {
		for _, f := range p.params {
			m.reach(pkg, f.typ.Elem())
		}
		res := p.result.Elem()
		if res.Name != "" && res.Name != "Null" {
			m.reach(pkg, res)
		}
	}
	for _, p := range todo {
		meth := p.meth
		meth.Request = m.request(pkg, s, meth, p.params)
		meth.Response = m.response(pkg, s, meth, p.result)
		meth.Errors = m.errorsOf(meth.Op, pkg)
		for i := range meth.Errors {
			e := &meth.Errors[i]
			if e.GRPC == "" && Own(e.Node) {
				m.warnf(e.Node, errSite(e), "%s is thrown by %s, a gRPC method, and has no @code: it's answered as UNKNOWN", e.Node.Name, meth.Op.Name)
			}
		}
	}
}

func errSite(e *Error) resolve.Site {
	if e.Throws.N != nil {
		return e.Throws
	}
	return e.Node.Decl
}

// opsOf lists an api's ops in the order written.
func (m *Model) opsOf(api *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, ch := range api.Children {
		if project.KindPath(m.r.c, ch) == "types.op" {
			out = append(out, ch)
		}
	}
	return out
}

// request makes a method's request message from its parameters.
func (m *Model) request(pkg *Package, s *Service, meth *Method, params []param) *Message {
	msg := &Message{Name: m.methodMessageName(pkg, s, meth, "Request"), Package: pkg.Name, Method: meth}
	type numbered struct {
		p   param
		n   int
		at  resolve.Site
		has bool
	}
	var ns []numbered
	for _, p := range params {
		x := numbered{p: p}
		for _, a := range p.anns {
			an := m.r.annAt(a, p.site)
			if an.name == "field" && (an.pkg == "proto" || an.pkg == "") {
				x.has = true
				x.at = an.site
				x.n, _ = strconv.Atoi(strings.TrimSpace(exprString(arg(a, 0, "number"))))
			}
		}
		ns = append(ns, x)
	}
	var fs []numField
	for _, x := range ns {
		fs = append(fs, numField{name: x.p.name, has: x.has, n: x.n, at: x.at, site: x.p.site})
	}
	m.number(msg.Name, fs, nil)
	for i, x := range ns {
		f := m.field(pkg, x.p.name, x.p.typ.Elem(), x.p.optional)
		f.Number = fs[i].n
		f.Doc = x.p.doc
		f.Stream = x.p.typ.IsStream()
		msg.Fields = append(msg.Fields, f)
	}
	pkg.add(msg)
	return msg
}

// response makes a method's response message from its result type.
func (m *Model) response(pkg *Package, s *Service, meth *Method, result Type) *Message {
	msg := &Message{Name: m.methodMessageName(pkg, s, meth, "Response"), Package: pkg.Name, Method: meth}
	res := result.Elem()
	if res.Name != "" && res.Name != "Null" {
		name := "value"
		switch {
		case res.Name == "List" || res.Name == "Set":
			name = "items"
		case res.Name == "Map":
			name = "entries"
		default:
			if mm := m.typeMessage(pkg, res); mm != nil {
				name = Snake(mm.Name)
			} else if res.Decl != nil && m.r.c.IsEnum(res.Decl) {
				name = Snake(res.Decl.Name)
			}
		}
		f := m.field(pkg, name, res, false)
		f.Number = 1
		f.Stream = result.IsStream()
		msg.Fields = append(msg.Fields, f)
	}
	pkg.add(msg)
	return msg
}

func (m *Model) methodMessageName(pkg *Package, s *Service, meth *Method, suffix string) string {
	name := meth.Name + suffix
	if pkg.names[name] {
		name = s.Name + meth.Name + suffix
	}
	return name
}

func (p *Package) add(msg *Message) {
	p.names[msg.Name] = true
	p.Messages = append(p.Messages, msg)
}

type numField struct {
	name string
	has  bool
	n    int
	at   resolve.Site // the @field annotation
	site resolve.Site // the field
}

// number numbers a message's fields in place: by @field, or else in the
// order written skipping the reserved numbers, and reports what's wrong.
func (m *Model) number(message string, fs []numField, reserved []int) {
	some, all := false, true
	for _, f := range fs {
		if f.has {
			some = true
		} else {
			all = false
		}
	}
	res := map[int]bool{}
	for _, n := range reserved {
		res[n] = true
	}
	if some && !all {
		for _, f := range fs {
			if !f.has {
				m.errorf(f.site, "field-number", "%s numbers some fields with @field and not %s: number all of them or none", message, f.name)
				break
			}
		}
	}
	if !some {
		n := 0
		for i := range fs {
			n++
			for res[n] || n >= 19000 && n <= 19999 {
				n++
			}
			fs[i].n = n
		}
		return
	}
	seen := map[int]string{}
	for i := range fs {
		f := fs[i]
		if !f.has {
			continue
		}
		at := f.at
		if at.N == nil {
			at = f.site
		}
		switch {
		case f.n < 1 || f.n > 536870911 || f.n >= 19000 && f.n <= 19999:
			m.errorf(at, "field-number", "%d can't number %s: use 1 to 536870911, outside 19000 to 19999", f.n, f.name)
		case res[f.n]:
			m.errorf(at, "field-number", "%s has number %d, which %s reserves", f.name, f.n, message)
		case seen[f.n] != "":
			m.errorf(at, "field-number", "%s has number %d, which %s already has in %s", f.name, f.n, seen[f.n], message)
		default:
			seen[f.n] = f.name
		}
	}
}

// reach makes the message or enum of a type a method reaches, and of
// every type it reaches in turn.
func (m *Model) reach(pkg *Package, t Type) {
	m.field(pkg, "x", t, false)
}

// typeMessage is the message a type maps to, made on first use.
func (m *Model) typeMessage(pkg *Package, t Type) *Message {
	d := t.Decl
	if d == nil || isScalar(t) || isWellKnown(t) {
		return nil
	}
	c := m.r.c
	kind := project.KindPath(c, d)
	if kind != "types.type" && kind != "types.union" {
		return nil
	}
	name := m.messageName(t)
	key := name
	if msg := pkg.byKey[key]; msg != nil {
		return msg
	}
	msg := &Message{Name: name, Package: pkg.Name, Node: d, Doc: DocOf(d)}
	if len(t.Args) > 0 {
		msg.Node = nil // an instance of a generic type has no page of its own
	}
	pkg.byKey[key] = msg
	if len(t.Args) == 0 {
		m.typeIn[d] = msg
	}
	for _, i := range c.Implements(d) {
		if i.Name == "Error" && isStd(i) {
			msg.Error = true
		}
	}
	as := m.r.annsOf(d)
	if a := find(as, "proto", "reserved"); a != nil {
		for _, e := range argAll(a.a) {
			if n, err := strconv.Atoi(strings.TrimSpace(e.Syntax().Text())); err == nil {
				msg.Reserved = append(msg.Reserved, n)
			}
		}
	}
	var fs []numField
	type pf struct {
		name     string
		typ      Type
		optional bool
		doc      string
		node     *resolve.Node
	}
	var parts []pf
	if kind == "types.union" {
		msg.Oneof = Snake(d.Name)
		for _, v := range d.Children {
			if v.Decl.N == nil || v.Decl.N.Kind != syntax.Declare {
				continue
			}
			site := v.Decl
			if d.Body != nil && site.Scope == nil {
				site.Scope = d.Body
			}
			vt := m.r.readType(ast.Declare{N: v.Decl.N}.Type(), site, nil)
			if vt.Name == "List" || vt.Name == "Set" || vt.Name == "Map" {
				m.errorf(v.Decl, "proto", "%s has no protobuf form: a list or map can't be a variant of a union", d.Name)
			}
			parts = append(parts, pf{name: v.Name, typ: vt, doc: DocOf(v), node: v})
		}
	} else {
		env := m.r.envFor(t)
		patch := d.Name == "Patch" && isStd(d) && len(t.Args) == 1
		src := d
		if patch && t.Args[0].Decl != nil {
			src = t.Args[0].Decl
			env = m.r.envFor(t.Args[0])
		}
		for _, mem := range m.r.members(src, env) {
			opt := mem.optional || patch
			if msg.Error && mem.name == "message" {
				continue
			}
			parts = append(parts, pf{name: mem.name, typ: mem.typ, optional: opt, doc: mem.doc, node: mem.node})
		}
	}
	for _, p := range parts {
		x := numField{name: p.name, site: p.node.Decl}
		for _, a := range m.r.annsOf(p.node) {
			if a.name == "field" && (a.pkg == "proto" || a.pkg == "") {
				x.has, x.at = true, a.site
				x.n, _ = strconv.Atoi(strings.TrimSpace(exprString(arg(a.a, 0, "number"))))
			}
		}
		if !Own(d) {
			x.site = resolve.Site{}
		}
		fs = append(fs, x)
	}
	if Own(d) {
		m.number(name, fs, msg.Reserved)
	} else {
		n := 0
		for i := range fs {
			n++
			fs[i].n = n
		}
	}
	// Add the message before its fields' types, so the file lists types
	// in the order they're first reached, a type before those it holds.
	pkg.add(msg)
	for i, p := range parts {
		f := m.field(pkg, p.name, p.typ, p.optional)
		f.Number = fs[i].n
		f.Doc = p.doc
		f.Node = p.node
		for _, a := range m.r.annsOf(p.node) {
			if a.name == "field" && (a.pkg == "proto" || a.pkg == "") {
				m.encoding(f, a.a)
			}
		}
		if msg.Oneof != "" {
			f.Label = ""
		}
		msg.Fields = append(msg.Fields, f)
	}
	return msg
}

func argAll(a ast.Annotation) []ast.Expr {
	var out []ast.Expr
	for _, e := range project.ArgExprs(a.Args()) {
		if arr, ok := e.(ast.ArrayExpr); ok {
			out = append(out, arr.Exprs()...)
			continue
		}
		out = append(out, e)
	}
	return out
}

// encoding applies @field<sint> or @field<fixed> to an integer field.
func (m *Model) encoding(f *Field, a ast.Annotation) {
	for _, fl := range a.Flags().Idents() {
		bits := "64"
		if strings.Contains(f.Scalar, "32") {
			bits = "32"
		}
		unsigned := strings.HasPrefix(f.Scalar, "uint")
		if !strings.Contains(f.Scalar, "int") {
			return
		}
		switch fl.Text {
		case "sint":
			if !unsigned {
				f.Scalar = "sint" + bits
			}
		case "fixed":
			if unsigned {
				f.Scalar = "fixed" + bits
			} else {
				f.Scalar = "sfixed" + bits
			}
		}
		if f.MapValue == nil {
			f.Type = f.Scalar
		}
	}
}

// messageName names a type's message: the type's name, then its
// arguments' for an instance of a generic type.
func (m *Model) messageName(t Type) string {
	name := t.Decl.Name
	for _, a := range t.Args {
		switch {
		case a.Decl != nil && !isScalar(a):
			name += m.messageName(a)
		default:
			name += Pascal(a.Name)
		}
	}
	return name
}

func isScalar(t Type) bool {
	switch t.Name {
	case "String", "Bool", "Bytes", "Int", "Float", "Decimal", "Prose":
		return t.Decl == nil || resolveBuiltin(t.Decl)
	}
	return false
}

func resolveBuiltin(d *resolve.Node) bool {
	return d.Shape == resolve.ShapeBuiltin || d.Decl.File == nil
}

func isWellKnown(t Type) bool {
	switch t.Name {
	case "Timestamp", "Duration", "Any", "Object", "Null":
		return t.Decl == nil || isStd(t.Decl) || resolveBuiltin(t.Decl)
	}
	return false
}

// scalarOf maps a primitive type to its protobuf scalar.
func scalarOf(t Type) string {
	switch t.Name {
	case "String", "Decimal", "Prose":
		return "string"
	case "Bool":
		return "bool"
	case "Bytes":
		return "bytes"
	case "Int":
		switch {
		case t.Bits > 0 && t.Bits <= 32 && t.Unsigned:
			return "uint32"
		case t.Bits > 0 && t.Bits <= 32:
			return "int32"
		case t.Unsigned:
			return "uint64"
		}
		return "int64"
	case "Float":
		if t.Bits == 32 {
			return "float"
		}
		return "double"
	}
	return ""
}

var wrappers = map[string]string{
	"string": "google.protobuf.StringValue", "bool": "google.protobuf.BoolValue", "bytes": "google.protobuf.BytesValue",
	"int64": "google.protobuf.Int64Value", "int32": "google.protobuf.Int32Value", "uint64": "google.protobuf.UInt64Value",
	"uint32": "google.protobuf.UInt32Value", "double": "google.protobuf.DoubleValue", "float": "google.protobuf.FloatValue",
}

// field maps a field's type, making the messages and enums it reaches.
func (m *Model) field(pkg *Package, name string, t Type, optional bool) *Field {
	f := &Field{Name: name, JSON: Camel(name), Optional: optional || t.Optional}
	if f.Optional {
		f.Label = "optional"
	}
	fail := func(reason string) *Field {
		at := t.Site
		if at.N != nil && Own(&resolve.Node{Decl: at}) {
			m.errorf(at, "proto", "%s has no protobuf form: %s", t.Text, reason)
		}
		f.Type, f.Scalar = "string", "string"
		return f
	}
	if len(t.Union) > 0 {
		var other []Type
		null := false
		for _, u := range t.Union {
			if u.Name == "Null" {
				null = true
			} else {
				other = append(other, u)
			}
		}
		if null && len(other) == 1 && isScalar(other[0]) {
			f.Scalar = wrappers[scalarOf(other[0])]
			f.Type = f.Scalar
			f.Label = ""
			pkg.imports["google/protobuf/wrappers.proto"] = true
			return f
		}
		return fail("a union expression other than T | Null of a primitive type")
	}
	switch t.Name {
	case "List", "Set", "Stream":
		if len(t.Args) != 1 {
			return fail("a list needs its element type")
		}
		e := t.Args[0]
		if e.Name == "List" || e.Name == "Set" || e.Name == "Map" {
			return fail("a list of lists or maps")
		}
		inner := m.field(pkg, name, e, false)
		inner.Label = "repeated"
		inner.Optional = false
		return inner
	case "Map":
		if len(t.Args) != 2 {
			return fail("a map needs its key and value types")
		}
		k, v := t.Args[0], t.Args[1]
		if k.Name == "String" && v.Name == "Any" {
			f.Type, f.Scalar = "google.protobuf.Struct", "google.protobuf.Struct"
			f.Label = ""
			pkg.imports["google/protobuf/struct.proto"] = true
			return f
		}
		ks := scalarOf(k)
		if ks == "" || ks == "double" || ks == "float" || ks == "bytes" || k.Name == "Decimal" {
			return fail("a map keyed by anything but a string, an integer or a boolean")
		}
		if v.Name == "List" || v.Name == "Set" || v.Name == "Map" {
			return fail("a map of lists or maps")
		}
		vf := m.field(pkg, "value", v, false)
		vf.Label = ""
		f.MapValue = vf
		f.Type = "map<" + ks + ", " + vf.Type + ">"
		f.Scalar = ks
		f.Label = ""
		return f
	case "Timestamp", "Duration":
		if isWellKnown(t) {
			f.Scalar = "google.protobuf." + t.Name
			f.Type = f.Scalar
			pkg.imports["google/protobuf/"+strings.ToLower(t.Name)+".proto"] = true
			return f
		}
	case "Any":
		if isWellKnown(t) {
			f.Scalar, f.Type = "google.protobuf.Value", "google.protobuf.Value"
			f.Label = ""
			pkg.imports["google/protobuf/struct.proto"] = true
			return f
		}
	case "Object":
		if isWellKnown(t) {
			f.Scalar, f.Type = "google.protobuf.Struct", "google.protobuf.Struct"
			f.Label = ""
			pkg.imports["google/protobuf/struct.proto"] = true
			return f
		}
	}
	if s := scalarOf(t); s != "" && isScalar(t) {
		f.Scalar, f.Type = s, s
		return f
	}
	if t.Decl != nil && m.r.c.IsEnum(t.Decl) {
		e := m.enumOf(pkg, t.Decl)
		f.Enum, f.Type = e, e.Name
		return f
	}
	if msg := m.typeMessage(pkg, t); msg != nil {
		f.Message, f.Type = msg, msg.Name
		return f
	}
	if t.Decl != nil && project.KindPath(m.r.c, t.Decl) == "types.interface" {
		return fail("an interface has no fields of its own to send")
	}
	return fail("it isn't a record, an enum, a union or a primitive type")
}

// enumOf makes an enum's protobuf enum.
func (m *Model) enumOf(pkg *Package, d *resolve.Node) *Enum {
	if e := pkg.enums[d]; e != nil {
		return e
	}
	e := &Enum{Name: d.Name, Package: pkg.Name, Node: d, Doc: DocOf(d)}
	pkg.enums[d] = e
	m.enumIn[d] = e
	prefix := UpperSnake(d.Name) + "_"
	e.Values = append(e.Values, EnumValue{Name: prefix + "UNSPECIFIED", Number: 0})
	var fs []numField
	var vs []*resolve.Node
	for _, v := range d.Children {
		if v.Name == "" {
			continue
		}
		vs = append(vs, v)
		x := numField{name: v.Name, site: v.Decl}
		for _, a := range m.r.annsOf(v) {
			if a.name == "field" && (a.pkg == "proto" || a.pkg == "") {
				x.has, x.at = true, a.site
				x.n, _ = strconv.Atoi(strings.TrimSpace(exprString(arg(a.a, 0, "number"))))
			}
		}
		fs = append(fs, x)
	}
	if Own(d) {
		m.number(d.Name, fs, nil)
	} else {
		for i := range fs {
			fs[i].n = i + 1
		}
	}
	for i, v := range vs {
		e.Values = append(e.Values, EnumValue{Name: prefix + UpperSnake(v.Name), Variant: v.Name, Number: fs[i].n, Doc: DocOf(v)})
	}
	pkg.Enums = append(pkg.Enums, e)
	return e
}

// errorsOf reads the errors an op throws, its own first, an interface as
// each type that implements it, with their projections.
func (m *Model) errorsOf(op *resolve.Node, pkg *Package) []Error {
	c := m.r.c
	var out []Error
	seen := map[*resolve.Node]bool{}
	var add func(t *resolve.Node, at resolve.Site, fromAPI bool)
	add = func(t *resolve.Node, at resolve.Site, fromAPI bool) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		if project.KindPath(c, t) == "types.interface" && len(m.implementor[t]) > 0 {
			for _, i := range m.implementor[t] {
				add(i, at, fromAPI)
			}
			return
		}
		e := Error{Node: t, Throws: at, FromAPI: fromAPI}
		as := m.r.annsOf(t)
		if project.KindPath(c, t) == "spec.error" {
			e.IsNode = true
			e.ID, _ = project.ArgString(project.KindArgs(t), 0, "id")
			if st := project.NamedArg(project.KindArgs(t), "status"); st != nil {
				e.Status = strings.TrimSpace(st.Syntax().Text())
			}
			for _, ch := range t.Children {
				switch {
				case ch.Name == "title" && ch.Value != nil:
					e.Message, _ = resolve.StringValue(ch.Value.Syntax())
				case project.KindPath(c, ch) == "spec.field":
					ty := ""
					if es := project.ArgExprs(project.KindArgs(ch)); len(es) > 0 {
						ty = strings.TrimSpace(es[0].Syntax().Text())
					}
					e.Fields = append(e.Fields, ErrorField{Name: ch.Name, Type: ty})
				}
			}
		} else {
			if a := find(as, "http", "status"); a != nil {
				e.Status = strings.TrimSpace(exprString(arg(a.a, 0, "code")))
			}
			for _, mem := range m.r.members(t, nil) {
				if mem.name == "message" {
					if mem.node.Decl.N != nil && mem.node.Decl.N.Kind == syntax.Value {
						e.Message, _ = resolve.StringValue(ast.Value{N: mem.node.Decl.N}.Expr().Syntax())
					}
					continue
				}
				e.Fields = append(e.Fields, ErrorField{Name: mem.name, Type: mem.typ.Text})
			}
			if pkg != nil {
				e.Detail = m.typeMessage(pkg, Type{Decl: t, Name: t.Name, Text: t.Name})
			}
		}
		for _, a := range as {
			if a.name != "code" {
				continue
			}
			switch a.pkg {
			case "proto":
				e.GRPC = variantName(arg(a.a, 0, "code"))
			case "jsonrpc":
				e.JSONRPC = strings.TrimSpace(exprString(arg(a.a, 0, "code")))
				e.JSONCodes = a.site
			}
		}
		out = append(out, e)
	}
	for i, holder := range []*resolve.Node{op, op.Parent} {
		if holder == nil || i == 1 && project.KindPath(c, holder) != "types.api" {
			continue
		}
		for _, a := range m.r.annsOf(holder) {
			if a.name != "throws" {
				continue
			}
			for _, e := range project.ArgExprs(a.a.Args()) {
				add(project.RefTarget(c, e.Syntax(), holder.Decl), a.site, i == 1)
			}
		}
	}
	return out
}

// buildJSON reads an api's JSON-RPC methods.
func (m *Model) buildJSON(api *resolve.Node) {
	r := m.r
	as := r.annsOf(api)
	ja := find(as, "jsonrpc", "jsonrpc")
	ops := m.opsOf(api)
	j := &JSONAPI{API: api, Framing: "http", Encoding: "json", Servers: m.servers(as), Bound: ja != nil}
	apiPositional := false
	if ja != nil {
		if e := arg(ja.a, 0, "method"); e != nil {
			m.errorf(siteOf(ja.site, e.Syntax()), "jsonrpc", "@jsonrpc on api doesn't take method")
		}
		if v := variantName(arg(ja.a, -1, "framing")); v != "" {
			j.Framing = v
		}
		if v := variantName(arg(ja.a, -1, "encoding")); v != "" {
			j.Encoding = v
		}
		j.Batch = exprString(arg(ja.a, -1, "batch")) == "true"
		apiPositional = variantName(arg(ja.a, -1, "params")) == "by_position"
	}
	names := map[string]*resolve.Node{}
	for _, op := range ops {
		oas := r.annsOf(op)
		oa := find(oas, "jsonrpc", "jsonrpc")
		if ja == nil && oa == nil {
			continue
		}
		jm := &JSONMethod{Op: op, API: j, Name: op.Name, ByPosition: apiPositional}
		if oa != nil {
			for _, name := range []string{"batch", "framing", "encoding"} {
				if e := arg(oa.a, -1, name); e != nil {
					m.errorf(siteOf(oa.site, e.Syntax()), "jsonrpc", "@jsonrpc on op doesn't take %s", name)
				}
			}
			if v := exprString(arg(oa.a, 0, "method")); v != "" {
				jm.Name = v
			}
			if v := variantName(arg(oa.a, -1, "params")); v != "" {
				jm.ByPosition = v == "by_position"
			}
		}
		if prev := names[jm.Name]; prev != nil {
			at := op.Decl
			if oa != nil {
				at = oa.site
			}
			m.errorf(at, "jsonrpc", "%s is the JSON-RPC method %s, which %s already is", op.Name, jm.Name, prev.Name)
		} else {
			names[jm.Name] = op
		}
		params, result := r.opSignature(op)
		jm.Result = result
		hasResult := result.Name != "" || len(result.Union) > 0
		jm.Notification = !hasResult
		jm.NullResult = result.Name == "Null"
		streams := result.IsStream()
		for _, p := range params {
			if p.typ.IsStream() {
				streams = true
			}
		}
		if streams {
			m.errorf(op.Decl, "jsonrpc", "%s streams, and a JSON-RPC method answers once", op.Name)
		}
		for _, p := range params {
			jp := JSONParam{Name: p.name, Type: p.typ, Optional: p.optional, Doc: p.doc}
			whole := false
			for _, a := range p.anns {
				x := r.annAt(a, p.site)
				if x.name == "params" && (x.pkg == "jsonrpc" || x.pkg == "") {
					whole = true
				}
			}
			if whole {
				isRecord := p.typ.Decl != nil && project.KindPath(r.c, p.typ.Decl) == "types.type" && !isScalar(p.typ) && !isWellKnown(p.typ)
				if len(params) != 1 || !isRecord {
					m.errorf(p.site, "jsonrpc", "@params %s must be the only parameter of %s, of a record type", p.name, op.Name)
				}
				jp := jp
				jm.Whole = &jp
			}
			jm.Params = append(jm.Params, jp)
		}
		jm.Errors = m.errorsOf(op, nil)
		if jm.Notification {
			for _, e := range jm.Errors {
				m.errorf(e.Throws, "jsonrpc", "%s is a notification, which is never answered, so it can't throw %s", op.Name, e.Node.Name)
				break
			}
		}
		for i := range jm.Errors {
			e := &jm.Errors[i]
			if e.JSONRPC == "" && Own(e.Node) && !jm.Notification {
				m.warnf(e.Node, errSite(e), "%s is thrown by %s, a JSON-RPC method, and has no @code", e.Node.Name, op.Name)
			}
		}
		m.jsonOps[op] = jm
		j.Methods = append(j.Methods, jm)
	}
	if len(j.Methods) > 0 {
		m.JSONAPIs = append(m.JSONAPIs, j)
	}
}

// reservedCodes reports JSON-RPC codes in the reserved range.
func (m *Model) reservedCodes(t *resolve.Tree) {
	t.Walk(func(n *resolve.Node) {
		if !Own(n) {
			return
		}
		for _, a := range m.r.annsOf(n) {
			if a.name != "code" || a.pkg != "jsonrpc" {
				continue
			}
			v, err := strconv.Atoi(strings.TrimSpace(exprString(arg(a.a, 0, "code"))))
			if err != nil {
				continue
			}
			predefined := v == -32700 || v >= -32603 && v <= -32600
			server := v >= -32099 && v <= -32000
			if v >= -32768 && v <= -32000 && !predefined && !server {
				m.warnf(n, a.site, "%d is in JSON-RPC's reserved range: use a code outside -32768 to -32000, or a server code from -32099 to -32000", v)
			}
		}
	})
}

// Sorted returns the packages by name.
func (m *Model) Sorted() []*Package {
	var out []*Package
	for _, p := range m.Packages {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Imports lists the well-known types' files a package uses, sorted.
func (p *Package) Imports() []string {
	var out []string
	for k := range p.imports {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ConnectCode is a gRPC code's name as Connect writes it.
func ConnectCode(code string) string {
	if code == "CANCELLED" {
		return "canceled"
	}
	return strings.ToLower(code)
}

// ConnectStatus is the HTTP status Connect answers a code with.
func ConnectStatus(code string) int {
	switch code {
	case "CANCELLED":
		return 499
	case "INVALID_ARGUMENT", "FAILED_PRECONDITION", "OUT_OF_RANGE":
		return 400
	case "UNAUTHENTICATED":
		return 401
	case "PERMISSION_DENIED":
		return 403
	case "NOT_FOUND":
		return 404
	case "ALREADY_EXISTS", "ABORTED":
		return 409
	case "RESOURCE_EXHAUSTED":
		return 429
	case "UNIMPLEMENTED":
		return 501
	case "UNAVAILABLE":
		return 503
	case "DEADLINE_EXCEEDED":
		return 504
	}
	return 500
}
