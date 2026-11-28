package rpc

// The Sparrow binding of std/sparrow: an api with @sparrow is a
// service of a facet, its ops are the facet's methods, and the types they
// reach are the facet's type definitions, numbered so a payload is read by
// numbers alone. The facets are built with the reference library's schema
// package, so their canonical bytes and hashes are Sparrow's own, and each
// is validated by it.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aiviarium/sparrow/go/cbor"
	"github.com/aiviarium/sparrow/go/names"
	"github.com/aiviarium/sparrow/go/schema"
	"github.com/aiviarium/sparrow/go/status"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// DefaultSparrowVersion is a facet's version when no api of it sets one.
const DefaultSparrowVersion = "1.0.0"

// SparrowFacet is one facet: the services of the apis that name it, and
// the types and sigils their methods reach.
type SparrowFacet struct {
	ID       uint64
	Name     string
	Version  string
	Services []*SparrowService
	// Types are the facet's type definitions in the order the methods
	// reach them; Value and VariantValue stand for Any.
	Types   []*SparrowType
	Sigils  []*SparrowSigil
	Retired []schema.Retired
	// Def is the facet as Sparrow's schema package holds it, and Hash its
	// facet hash; nil when the facet isn't valid.
	Def  *schema.Facet
	Hash []byte

	api        *resolve.Node
	versionAt  *resolve.Node // the api that set the version
	explicitID bool
	idSite     resolve.Site
	byKey      map[string]*SparrowType
	sigilOf    map[*resolve.Node]*SparrowSigil
	both       map[*resolve.Node]bool // errors reported for a code and a sigil
	value      *SparrowType
	variant    *SparrowType
	names      map[string]bool
	retired    map[schema.Retired]bool
}

// HashHex is the facet hash in lower-case hex.
func (f *SparrowFacet) HashHex() string { return hex.EncodeToString(f.Hash) }

// SparrowService is an api as a service of its facet.
type SparrowService struct {
	API      *resolve.Node
	Facet    *SparrowFacet
	ID       uint64
	Name     string
	Methods  []*SparrowMethod
	Doc      string
	explicit bool
	idSite   resolve.Site
}

// SparrowMethod is an op as a method of its facet.
type SparrowMethod struct {
	Op      *resolve.Node
	Service *SparrowService
	ID      uint64
	Name    string
	Kind    Kind
	Input   SparrowRef
	Output  SparrowRef
	// InputType is the struct made from the op's parameters, nil when it
	// takes none or streams them.
	InputType *SparrowType
	// Errors are the errors the op throws, each with its core code or its
	// own sigil.
	Errors       []Error
	Notification bool
	Idempotent   bool
	Shared       bool
	explicit     bool
	idSite       resolve.Site
}

// Flags are the method's flags: 0x1 idempotent, 0x2 notification, 0x4
// shared, 0x8 and 0x10 for a streamed input and output.
func (m *SparrowMethod) Flags() uint64 {
	var fl uint64
	if m.Idempotent {
		fl |= schema.MethodIdempotent
	}
	if m.Notification {
		fl |= schema.MethodNotification
	}
	if m.Shared {
		fl |= schema.MethodShared
	}
	if m.Input.Kind == schema.RefStream {
		fl |= schema.MethodInputEnd
	}
	if m.Output.Kind == schema.RefStream {
		fl |= schema.MethodOutputEnd
	}
	return fl
}

// FlagNames are the method's flags by name.
func (m *SparrowMethod) FlagNames() []string { return flagNames(m.Flags()) }

func flagNames(fl uint64) []string {
	out := []string{}
	for _, x := range []struct {
		bit  uint64
		name string
	}{{schema.MethodIdempotent, "idempotent"}, {schema.MethodNotification, "notification"}, {schema.MethodShared, "shared"}, {schema.MethodInputEnd, "input_end"}, {schema.MethodOutputEnd, "output_end"}} {
		if fl&x.bit != 0 {
			out = append(out, x.name)
		}
	}
	return out
}

// Shape is the call shape: unary, a stream on either side, or a
// notification.
func (m *SparrowMethod) Shape() string {
	if m.Notification {
		return "notification"
	}
	return m.Kind.Key()
}

// ErrorSigils are the sigils the method returns beyond the core codes.
func (m *SparrowMethod) ErrorSigils() []uint64 {
	var out []uint64
	seen := map[uint64]bool{}
	for _, e := range m.Errors {
		if e.SparrowSigil != nil && !seen[e.SparrowSigil.ID] {
			seen[e.SparrowSigil.ID] = true
			out = append(out, e.SparrowSigil.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SparrowType is a type definition of a facet.
type SparrowType struct {
	Node *resolve.Node // the Heron type; nil for a method's input and for Value
	Name string
	Kind schema.TypeKind
	ID   uint64
	// Fields are a struct's fields or a union's variants, by tag.
	Fields []*SparrowField
	// Values are an enum's values.
	Values []*SparrowSigil
	Sparse bool
	Error  bool
	Doc    string
	// Method is the method whose input the struct is.
	Method *SparrowMethod
	// Builtin marks Value and VariantValue, which Any stands for.
	Builtin  bool
	explicit bool
	idSite   resolve.Site
}

// SparrowField is a struct field or a union variant.
type SparrowField struct {
	Tag      uint64
	Name     string
	Type     SparrowRef
	Optional bool
	Node     *resolve.Node
	Doc      string
}

// SparrowRef is a type reference: a built-in TypeId, a type of the facet,
// or a list, map, optional or stream of them.
type SparrowRef struct {
	Kind    schema.RefKind
	Builtin uint64       // a built-in TypeId, when Kind is RefNamed and Type nil
	Type    *SparrowType // a type of the facet
	Elem    *SparrowRef
	Key     *SparrowRef
}

func builtinRef(id uint64) SparrowRef { return SparrowRef{Kind: schema.RefNamed, Builtin: id} }

func wrapRef(k schema.RefKind, e SparrowRef) SparrowRef { return SparrowRef{Kind: k, Elem: &e} }

// Ref is the reference as Sparrow's schema package writes it; the facet's
// ids must be assigned first.
func (r SparrowRef) Ref() schema.TypeRef {
	switch r.Kind {
	case schema.RefList:
		return schema.List(r.Elem.Ref())
	case schema.RefMap:
		return schema.MapOf(r.Key.Ref(), r.Elem.Ref())
	case schema.RefOptional:
		return schema.Optional(r.Elem.Ref())
	case schema.RefStream:
		return schema.Stream(r.Elem.Ref())
	case schema.RefByteStream:
		return schema.ByteStream()
	}
	if r.Type != nil {
		return schema.Named(r.Type.ID)
	}
	return schema.Named(r.Builtin)
}

var builtinNames = [...]string{"unit", "bool", "int", "uint", "float32", "float64", "string", "bytes", "timestamp", "uuid", "dynamic", "symbol"}

// String writes the reference as the docs show it: string, Parcel,
// list<Scan>, map<string, string>, optional<T>, stream<T>.
func (r SparrowRef) String() string {
	switch r.Kind {
	case schema.RefList:
		return "list<" + r.Elem.String() + ">"
	case schema.RefMap:
		return "map<" + r.Key.String() + ", " + r.Elem.String() + ">"
	case schema.RefOptional:
		return "optional<" + r.Elem.String() + ">"
	case schema.RefStream:
		return "stream<" + r.Elem.String() + ">"
	case schema.RefByteStream:
		return "bytestream"
	}
	if r.Type != nil {
		return r.Type.Name
	}
	if r.Builtin < uint64(len(builtinNames)) {
		return builtinNames[r.Builtin]
	}
	return strconv.FormatUint(r.Builtin, 10)
}

// Named is the facet's type the reference names, through a list, map,
// optional or stream, or nil.
func (r SparrowRef) Named() *SparrowType {
	switch {
	case r.Type != nil:
		return r.Type
	case r.Kind == schema.RefMap && r.Elem != nil:
		return r.Elem.Named()
	case r.Elem != nil:
		return r.Elem.Named()
	}
	return nil
}

// SparrowSigil is an enum value or an error code of a facet.
type SparrowSigil struct {
	ID       uint64
	Name     string
	Kind     uint64 // schema.SigilEnumValue or schema.SigilError
	Node     *resolve.Node
	explicit bool
	idSite   resolve.Site
}

// SparrowValueTypes are the definitions Heron's Any is in a facet: the
// recursive union Value at firstID and its helper struct VariantValue at
// firstID+1. Every facet that reaches Any defines them alike, so a value
// reads the same in each.
//
//	Value: union, variants by tag
//	  0 null: unit       1 bool: bool        2 int: int
//	  3 uint: uint       (only above the int64 range)
//	  4 bigint: string   (decimal digits, an optional '-')
//	  5 float: float64   6 string: string    7 bytes: bytes
//	  8 decimal: string  (as "40.00", the exponent kept)
//	  9 time: string     (RFC 3339 with nanoseconds, UTC)
//	  10 duration: int   (milliseconds)
//	  11 list: [0, Value]  12 set: [0, Value]  13 map: [1, string, Value]
//	  14 variant: VariantValue  15 ref: string (a node path)
//	  16 prose: [1, string, Value] (an mdast root node)
//	VariantValue: struct { 0 name: string, 1 value: Value }
func SparrowValueTypes(firstID uint64) []schema.TypeDef {
	value := schema.Named(firstID)
	v := func(tag uint64, name string, t schema.TypeRef) schema.Field {
		return schema.Field{Tag: tag, Name: name, Type: t}
	}
	n := schema.Named
	return []schema.TypeDef{
		{Kind: schema.KindUnion, ID: firstID, Name: "Value", Fields: []schema.Field{
			v(0, "null", n(schema.TypeUnit)),
			v(1, "bool", n(schema.TypeBool)),
			v(2, "int", n(schema.TypeInt)),
			v(3, "uint", n(schema.TypeUint)),
			v(4, "bigint", n(schema.TypeString)),
			v(5, "float", n(schema.TypeFloat64)),
			v(6, "string", n(schema.TypeString)),
			v(7, "bytes", n(schema.TypeBytes)),
			v(8, "decimal", n(schema.TypeString)),
			v(9, "time", n(schema.TypeString)),
			v(10, "duration", n(schema.TypeInt)),
			v(11, "list", schema.List(value)),
			v(12, "set", schema.List(value)),
			v(13, "map", schema.MapOf(n(schema.TypeString), value)),
			v(14, "variant", n(firstID+1)),
			v(15, "ref", n(schema.TypeString)),
			v(16, "prose", schema.MapOf(n(schema.TypeString), value)),
		}},
		{Kind: schema.KindStruct, ID: firstID + 1, Name: "VariantValue", Fields: []schema.Field{
			v(0, "name", n(schema.TypeString)),
			v(1, "value", value),
		}},
	}
}

// SparrowFacetByName returns a facet, or nil.
func (m *Model) SparrowFacetByName(name string) *SparrowFacet {
	for _, f := range m.Facets {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// SparrowServiceOf returns the Sparrow service of an api, or nil.
func (m *Model) SparrowServiceOf(api *resolve.Node) *SparrowService {
	for _, f := range m.Facets {
		for _, s := range f.Services {
			if s.API == api {
				return s
			}
		}
	}
	return nil
}

// SparrowMethod returns an op's Sparrow method, or nil.
func (m *Model) SparrowMethod(op *resolve.Node) *SparrowMethod { return m.sparrowOps[op] }

// SparrowTypeOf returns the type definition a Heron type is in a facet,
// or nil.
func (m *Model) SparrowTypeOf(n *resolve.Node) *SparrowType {
	for _, f := range m.Facets {
		for _, t := range f.Types {
			if t.Node == n {
				return t
			}
		}
	}
	return nil
}

var sparrowVersion = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*([-+][0-9A-Za-z.+-]*)?$`)

// idTooLarge reports an id, tag, TypeId or sigil above 2^53 - 1, the
// largest integer a Sparrow message defines itself (core 3.1): a facet's
// numbers all travel in its schema.
func (m *Model) idTooLarge(n int64, s resolve.Site, what, name string) bool {
	if n <= cbor.MaxSafeInt {
		return false
	}
	m.errorf(s, "sparrow-id", "%d can't be the %s of %s: a Sparrow message holds no integer above 2^53 - 1", n, what, name)
	return true
}

// intArg reads an integer argument of an annotation.
func intArg(a ast.Annotation, index int, name string) (int64, bool) {
	e := arg(a, index, name)
	if e == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(e.Syntax().Text()), 10, 64)
	return n, err == nil
}

// intList reads a list of integers, or one integer.
func intList(e ast.Expr) []int64 {
	if e == nil {
		return nil
	}
	es := []ast.Expr{e}
	if a, ok := e.(ast.ArrayExpr); ok {
		es = a.Exprs()
	}
	var out []int64
	for _, x := range es {
		if n, err := strconv.ParseInt(strings.TrimSpace(x.Syntax().Text()), 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func isSparrow(a ann, name string) bool {
	return a.name == name && (a.pkg == "sparrow" || a.pkg == "")
}

// buildSparrow reads an api's Sparrow service into its facet.
func (m *Model) buildSparrow(api *resolve.Node) {
	r := m.r
	as := r.annsOf(api)
	sa := find(as, "sparrow", "sparrow")
	ops := m.opsOf(api)
	if sa == nil {
		for _, op := range ops {
			if a := find(r.annsOf(op), "sparrow", "sparrow"); a != nil {
				m.errorf(a.site, "sparrow", "%s carries @sparrow, but its api isn't bound to a Sparrow facet", op.Name)
			}
		}
		return
	}
	for _, name := range []string{"method", "idempotent", "shared"} {
		if e := arg(sa.a, -1, name); e != nil {
			m.errorf(siteOf(sa.site, e.Syntax()), "sparrow", "@sparrow on api doesn't take %s", name)
		}
	}
	name := exprString(arg(sa.a, 0, "facet"))
	at := sa.site
	if e := arg(sa.a, 0, "facet"); e != nil {
		at = siteOf(sa.site, e.Syntax())
	}
	if name == "" {
		name = strings.ToLower(strings.ReplaceAll(api.Name, "_", "-"))
	}
	if !names.IsFacetName(name) {
		m.errorf(at, "sparrow", "%s isn't a valid facet name: write lower-case words, digits and hyphens joined by dots, as acme.parcels", name)
		return
	}
	f := m.sparrowFacets[name]
	if f == nil {
		f = &SparrowFacet{Name: name, Version: DefaultSparrowVersion, api: api, byKey: map[string]*SparrowType{}, sigilOf: map[*resolve.Node]*SparrowSigil{}, both: map[*resolve.Node]bool{}, names: map[string]bool{}, retired: map[schema.Retired]bool{}}
		m.sparrowFacets[name] = f
		m.Facets = append(m.Facets, f)
	}
	if e := arg(sa.a, -1, "version"); e != nil {
		v := exprString(e)
		switch {
		case !sparrowVersion.MatchString(v):
			m.errorf(siteOf(sa.site, e.Syntax()), "sparrow", "%s isn't a version: write numbers joined by dots, as 1.2.0", v)
		case f.versionAt != nil && f.Version != v:
			m.errorf(siteOf(sa.site, e.Syntax()), "sparrow", "%s puts facet %s at version %s, which %s puts at %s", api.Name, name, v, f.versionAt.Name, f.Version)
		default:
			f.Version, f.versionAt = v, api
		}
	}
	if n, ok := intArg(sa.a, -1, "facet_id"); ok {
		e := arg(sa.a, -1, "facet_id")
		s := siteOf(sa.site, e.Syntax())
		switch {
		case n < int64(schema.FirstFreeFacetID):
			m.errorf(s, "sparrow-id", "%d can't be the id of facet %s: 0 to 15 are Sparrow's own", n, name)
		case m.idTooLarge(n, s, "id", "facet "+name):
		case f.explicitID && f.ID != uint64(n):
			m.errorf(s, "sparrow-id", "%s gives facet %s the id %d, which it already has as %d", api.Name, name, n, f.ID)
		default:
			f.ID, f.explicitID, f.idSite = uint64(n), true, s
		}
	}
	if a := find(as, "sparrow", "retired"); a != nil {
		for i, space := range []string{"types", "services", "methods", "sigils"} {
			for _, n := range intList(arg(a.a, -1, space)) {
				rt := schema.Retired{Space: schema.Space(i), ID: uint64(n)}
				if m.idTooLarge(n, a.site, "retired id", "facet "+name) {
					continue
				}
				if n >= 0 && !f.retired[rt] {
					f.retired[rt] = true
					f.Retired = append(f.Retired, rt)
				}
			}
		}
	}
	svc := &SparrowService{API: api, Facet: f, Name: exprString(arg(sa.a, -1, "service")), Doc: DocOf(api)}
	if svc.Name == "" {
		svc.Name = Pascal(api.Name)
	}
	if n, ok := intArg(sa.a, -1, "id"); ok {
		s := siteOf(sa.site, arg(sa.a, -1, "id").Syntax())
		if n < 0 {
			m.errorf(s, "sparrow-id", "%d can't be the id of service %s: ids can't be negative", n, svc.Name)
		} else if !m.idTooLarge(n, s, "id", "service "+svc.Name) {
			svc.ID, svc.explicit, svc.idSite = uint64(n), true, s
		}
	}
	f.Services = append(f.Services, svc)

	type pending struct {
		meth   *SparrowMethod
		params []param
		result Type
	}
	var todo []pending
	for _, op := range ops {
		meth := &SparrowMethod{Op: op, Service: svc, Name: lowerFirst(Pascal(op.Name)), Kind: StreamKind(r.c, op)}
		if a := find(r.annsOf(op), "sparrow", "sparrow"); a != nil {
			for _, name := range []string{"facet", "version", "facet_id", "service"} {
				idx := -1
				if name == "facet" {
					idx = 0
				}
				if e := arg(a.a, idx, name); e != nil {
					m.errorf(siteOf(a.site, e.Syntax()), "sparrow", "@sparrow on op doesn't take %s", name)
				}
			}
			if v := exprString(arg(a.a, -1, "method")); v != "" {
				meth.Name = v
			}
			meth.Idempotent = exprString(arg(a.a, -1, "idempotent")) == "true"
			meth.Shared = exprString(arg(a.a, -1, "shared")) == "true"
			if n, ok := intArg(a.a, -1, "id"); ok {
				s := siteOf(a.site, arg(a.a, -1, "id").Syntax())
				if n < 0 {
					m.errorf(s, "sparrow-id", "%d can't be the id of method %s: ids can't be negative", n, meth.Name)
				} else if !m.idTooLarge(n, s, "id", "method "+meth.Name) {
					meth.ID, meth.explicit, meth.idSite = uint64(n), true, s
				}
			}
		}
		params, result := r.opSignature(op)
		var fields []param
		var stream *param
		for i, p := range params {
			if p.typ.IsStream() && stream == nil {
				stream = &params[i]
			}
		}
		for _, p := range params {
			if stream != nil && p.name != stream.name {
				m.errorf(p.site, "sparrow", "%s streams from the client, so %s has no place in its call", op.Name, p.name)
				continue
			}
			fields = append(fields, p)
		}
		hasResult := result.Name != "" || len(result.Union) > 0
		meth.Notification = !hasResult && stream == nil
		meth.Errors = m.errorsOf(op, nil)
		if meth.Notification {
			for _, e := range meth.Errors {
				m.errorf(e.Throws, "sparrow", "%s is a notification, which is never answered, so it can't throw %s", op.Name, e.Node.Name)
				break
			}
		}
		m.sparrowOps[op] = meth
		svc.Methods = append(svc.Methods, meth)
		todo = append(todo, pending{meth, fields, result})
	}
	// The types the methods reach first, so an input struct that would
	// take a type's name takes the service's too.
	for _, p := range todo {
		meth := p.meth
		if len(p.params) == 1 && p.params[0].typ.IsStream() {
			meth.Input = wrapRef(schema.RefStream, m.sparrowRef(f, p.params[0].typ.Elem()))
		}
		switch {
		case p.result.IsStream():
			meth.Output = wrapRef(schema.RefStream, m.sparrowRef(f, p.result.Elem()))
		case p.result.Name == "" && len(p.result.Union) == 0, p.result.Name == "Null" && len(p.result.Union) == 0:
			meth.Output = builtinRef(schema.TypeUnit)
		default:
			meth.Output = m.sparrowRef(f, p.result)
		}
		for i := range meth.Errors {
			e := &meth.Errors[i]
			switch {
			case e.SparrowSigilAt.N != nil && e.SparrowExplicit:
				if !f.both[e.Node] {
					f.both[e.Node] = true
					m.errorf(e.SparrowSigilAt, "sparrow", "%s has a core code, @sparrow.status(%s), and a sigil of its own: give it one or the other", e.Node.Name, e.Sparrow)
				}
			case e.SparrowSigilAt.N != nil || e.Sparrow == "":
				// A sigil of its own: its @sigil, or a type with no code.
				e.Sparrow = ""
				e.SparrowSigil = f.errorSigil(m, e)
			}
			m.sparrowRef(f, Type{Decl: e.Node, Name: e.Node.Name, Text: e.Node.Name})
		}
	}
	for _, p := range todo {
		meth := p.meth
		if meth.Input.Kind == schema.RefStream {
			continue
		}
		if len(p.params) == 0 {
			meth.Input = builtinRef(schema.TypeUnit)
			continue
		}
		meth.InputType = m.sparrowInput(f, svc, meth, p.params)
		meth.Input = SparrowRef{Kind: schema.RefNamed, Type: meth.InputType}
	}
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// sparrowInput makes a method's input struct from its parameters.
func (m *Model) sparrowInput(f *SparrowFacet, svc *SparrowService, meth *SparrowMethod, params []param) *SparrowType {
	name := Pascal(meth.Op.Name) + "Input"
	if f.names[name] {
		name = svc.Name + name
	}
	t := &SparrowType{Name: name, Kind: schema.KindStruct, Method: meth}
	f.names[name] = true
	f.Types = append(f.Types, t)
	var fs []tagField
	for _, p := range params {
		x := tagField{name: p.name, site: p.site}
		for _, a := range p.anns {
			an := m.r.annAt(a, p.site)
			if isSparrow(an, "tag") {
				x.has, x.at = true, an.site
				x.n, _ = intArg(a, 0, "n")
			}
		}
		fs = append(fs, x)
	}
	m.sparrowTags(name, fs, nil)
	for i, p := range params {
		ft, opt := p.typ, p.optional
		if ft.Optional {
			ft.Optional, opt = false, true
		}
		t.Fields = append(t.Fields, &SparrowField{Tag: uint64(fs[i].n), Name: p.name, Type: m.sparrowRef(f, ft), Optional: opt, Doc: p.doc})
	}
	return t
}

type tagField struct {
	name string
	has  bool
	n    int64
	at   resolve.Site
	site resolve.Site
}

// sparrowTags tags a struct's fields or a union's variants in place: by
// @tag, or else from 0 in the order written skipping the reserved tags,
// and reports what's wrong.
func (m *Model) sparrowTags(owner string, fs []tagField, reserved map[int64]bool) {
	some, all := false, true
	for _, f := range fs {
		if f.has {
			some = true
		} else {
			all = false
		}
	}
	if some && !all {
		for _, f := range fs {
			if !f.has {
				m.errorf(f.site, "sparrow-id", "%s tags some fields with @tag and not %s: tag all of them or none", owner, f.name)
				break
			}
		}
	}
	if !some {
		n := int64(0)
		for i := range fs {
			for reserved[n] {
				n++
			}
			fs[i].n = n
			n++
		}
		return
	}
	seen := map[int64]string{}
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
		case f.n < 0:
			m.errorf(at, "sparrow-id", "%d can't tag %s: tags start at 0", f.n, f.name)
			fs[i].n = 0
		case m.idTooLarge(f.n, at, "tag", f.name):
			fs[i].n = 0
		case reserved[f.n]:
			m.errorf(at, "sparrow-id", "%s has tag %d, which %s reserves", f.name, f.n, owner)
		case seen[f.n] != "":
			m.errorf(at, "sparrow-id", "%s has tag %d, which %s already has in %s", f.name, f.n, seen[f.n], owner)
		default:
			seen[f.n] = f.name
		}
	}
}

// sparrowRef maps a Heron type to a reference, making the definitions it
// reaches.
func (m *Model) sparrowRef(f *SparrowFacet, t Type) SparrowRef {
	fail := func(reason string) SparrowRef {
		if at := t.Site; at.N != nil && Own(&resolve.Node{Decl: at}) {
			m.errorf(at, "sparrow", "%s has no Sparrow form: %s", t.Text, reason)
		}
		return builtinRef(schema.TypeString)
	}
	if t.Optional {
		x := t
		x.Optional = false
		return wrapRef(schema.RefOptional, m.sparrowRef(f, x))
	}
	if len(t.Union) > 0 {
		var other []Type
		for _, u := range t.Union {
			if u.Name != "Null" {
				other = append(other, u)
			}
		}
		switch len(other) {
		case 0:
			return builtinRef(schema.TypeUnit)
		case 1:
			return wrapRef(schema.RefOptional, m.sparrowRef(f, other[0]))
		}
		return fail("a union expression other than T | Null")
	}
	switch t.Name {
	case "List", "Set":
		if len(t.Args) != 1 {
			return fail("a list needs its element type")
		}
		return wrapRef(schema.RefList, m.sparrowRef(f, t.Args[0]))
	case "Stream":
		return fail("a stream can only be an op's whole parameter or result")
	case "Map":
		if len(t.Args) != 2 {
			return fail("a map needs its key and value types")
		}
		k, v := m.sparrowRef(f, t.Args[0]), m.sparrowRef(f, t.Args[1])
		return SparrowRef{Kind: schema.RefMap, Key: &k, Elem: &v}
	}
	if isWellKnown(t) {
		switch t.Name {
		case "Null":
			return builtinRef(schema.TypeUnit)
		case "Timestamp":
			return builtinRef(schema.TypeTimestamp)
		case "Duration":
			// Milliseconds, as the Value union's duration holds them.
			return builtinRef(schema.TypeInt)
		case "Any":
			return SparrowRef{Kind: schema.RefNamed, Type: f.valueType()}
		case "Object":
			k, v := builtinRef(schema.TypeString), SparrowRef{Kind: schema.RefNamed, Type: f.valueType()}
			return SparrowRef{Kind: schema.RefMap, Key: &k, Elem: &v}
		}
	}
	if isScalar(t) {
		switch t.Name {
		case "String", "Prose", "Decimal":
			// A decimal is exact only as its text, as "40.00".
			return builtinRef(schema.TypeString)
		case "Bool":
			return builtinRef(schema.TypeBool)
		case "Bytes":
			return builtinRef(schema.TypeBytes)
		case "Int":
			if t.Unsigned {
				return builtinRef(schema.TypeUint)
			}
			return builtinRef(schema.TypeInt)
		case "Float":
			if t.Bits == 32 {
				return builtinRef(schema.TypeFloat32)
			}
			return builtinRef(schema.TypeFloat64)
		}
	}
	if t.Decl != nil && m.r.c.IsEnum(t.Decl) {
		return SparrowRef{Kind: schema.RefNamed, Type: m.sparrowEnum(f, t.Decl)}
	}
	if st := m.sparrowStruct(f, t); st != nil {
		return SparrowRef{Kind: schema.RefNamed, Type: st}
	}
	if t.Decl != nil && project.KindPath(m.r.c, t.Decl) == "types.interface" {
		return fail("an interface has no fields of its own to send")
	}
	return fail("it isn't a record, an enum, a union or a primitive type")
}

// valueType is the facet's Value union, which Any stands for, made with
// VariantValue on first use.
func (f *SparrowFacet) valueType() *SparrowType {
	if f.value == nil {
		f.value = &SparrowType{Name: "Value", Kind: schema.KindUnion, Builtin: true, Doc: "Any Heron value."}
		f.variant = &SparrowType{Name: "VariantValue", Kind: schema.KindStruct, Builtin: true, Doc: "An enum value or a union variant, by name, with its value."}
		f.Types = append(f.Types, f.value, f.variant)
		f.names["Value"], f.names["VariantValue"] = true, true
	}
	return f.value
}

// typeID reads a type's @sparrow.id.
func (m *Model) typeID(t *SparrowType, d *resolve.Node, generic bool) {
	if a := find(m.r.annsOf(d), "sparrow", "id"); a != nil {
		if generic {
			m.errorf(a.site, "sparrow-id", "%s is generic, and each of its instances is a type of its own: @sparrow.id can't number them", d.Name)
			return
		}
		if n, ok := intArg(a.a, 0, "n"); ok {
			t.explicit, t.idSite = true, a.site
			if n >= 0 {
				t.ID = uint64(n)
			}
			if n < int64(schema.FirstFreeTypeID) {
				m.errorf(a.site, "sparrow-id", "%d can't be the TypeId of %s: a facet's own types start at 64", n, d.Name)
				t.explicit = false
			} else if m.idTooLarge(n, a.site, "TypeId", d.Name) {
				t.ID, t.explicit = 0, false
			}
		}
	}
}

// sparrowStruct is the struct or union a record or union is, made on
// first use.
func (m *Model) sparrowStruct(f *SparrowFacet, t Type) *SparrowType {
	d := t.Decl
	if d == nil || isScalar(t) || isWellKnown(t) {
		return nil
	}
	c := m.r.c
	kind := project.KindPath(c, d)
	if kind != "types.type" && kind != "types.union" && kind != "types.error" {
		return nil
	}
	name := m.messageName(t)
	if st := f.byKey[name]; st != nil {
		return st
	}
	st := &SparrowType{Name: name, Node: d, Kind: schema.KindStruct, Doc: DocOf(d)}
	if len(t.Args) > 0 {
		st.Node = nil
	}
	f.byKey[name] = st
	f.names[name] = true
	f.Types = append(f.Types, st)
	if Own(d) {
		m.typeID(st, d, len(t.Args) > 0)
	}
	st.Error = c.IsError(d)
	as := m.r.annsOf(d)
	reserved := map[int64]bool{}
	if a := find(as, "sparrow", "reserved"); a != nil {
		for _, e := range argAll(a.a) {
			if n, err := strconv.ParseInt(strings.TrimSpace(e.Syntax().Text()), 10, 64); err == nil {
				reserved[n] = true
			}
		}
	}
	type part struct {
		name     string
		typ      Type
		optional bool
		doc      string
		node     *resolve.Node
	}
	var parts []part
	if kind == "types.union" {
		st.Kind = schema.KindUnion
		for _, uv := range c.UnionVariants(d) {
			parts = append(parts, part{name: uv.Node.Name, typ: m.r.variantType(d, uv), doc: DocOf(uv.Node), node: uv.Node})
		}
	} else {
		if a := find(as, "sparrow", "sparse"); a != nil {
			st.Sparse = true
		}
		env := m.r.envFor(t)
		patch := d.Name == "Patch" && isStd(d) && len(t.Args) == 1
		src := d
		if patch && t.Args[0].Decl != nil {
			src = t.Args[0].Decl
			env = m.r.envFor(t.Args[0])
		}
		for _, mem := range m.r.members(src, env) {
			parts = append(parts, part{name: mem.name, typ: mem.typ, optional: mem.optional || patch, doc: mem.doc, node: mem.node})
		}
	}
	var fs []tagField
	for _, p := range parts {
		x := tagField{name: p.name, site: p.node.Decl}
		for _, a := range m.r.annsOf(p.node) {
			if isSparrow(a, "tag") {
				x.has, x.at = true, a.site
				x.n, _ = intArg(a.a, 0, "n")
			}
		}
		fs = append(fs, x)
	}
	if Own(d) {
		m.sparrowTags(name, fs, reserved)
	} else {
		for i := range fs {
			fs[i].n = int64(i)
		}
	}
	for i, p := range parts {
		ft, opt := p.typ, p.optional
		if ft.Optional && st.Kind == schema.KindStruct {
			ft.Optional, opt = false, true
		}
		if st.Kind == schema.KindUnion {
			opt = false
		}
		st.Fields = append(st.Fields, &SparrowField{Tag: uint64(fs[i].n), Name: p.name, Type: m.sparrowRef(f, ft), Optional: opt, Node: p.node, Doc: p.doc})
	}
	return st
}

// sparrowEnum is an enum's definition, its values kind-0 sigils, made on
// first use.
func (m *Model) sparrowEnum(f *SparrowFacet, d *resolve.Node) *SparrowType {
	key := "enum:" + d.Name
	if st := f.byKey[key]; st != nil {
		return st
	}
	st := &SparrowType{Name: d.Name, Node: d, Kind: schema.KindEnum, Doc: DocOf(d)}
	f.byKey[key] = st
	f.names[d.Name] = true
	f.Types = append(f.Types, st)
	own := Own(d)
	if own {
		m.typeID(st, d, false)
	}
	var vs []*resolve.Node
	some, all := false, true
	for _, v := range d.Children {
		if v.Name == "" {
			continue
		}
		vs = append(vs, v)
		if find(m.r.annsOf(v), "sparrow", "sigil") != nil {
			some = true
		} else {
			all = false
		}
	}
	for _, v := range vs {
		if own && some && !all && find(m.r.annsOf(v), "sparrow", "sigil") == nil {
			m.errorf(v.Decl, "sparrow-id", "%s gives some values a @sigil and not %s: give all of them one or none", d.Name, v.Name)
			some = false
		}
		s := &SparrowSigil{Name: d.Name + "." + v.Name, Kind: schema.SigilEnumValue, Node: v}
		if own {
			m.sigilID(s, v)
		}
		f.sigilOf[v] = s
		f.Sigils = append(f.Sigils, s)
		st.Values = append(st.Values, s)
	}
	return st
}

// sigilID reads a node's @sparrow.sigil.
func (m *Model) sigilID(s *SparrowSigil, n *resolve.Node) {
	a := find(m.r.annsOf(n), "sparrow", "sigil")
	if a == nil {
		return
	}
	v, ok := intArg(a.a, 0, "n")
	if !ok {
		return
	}
	if v < int64(schema.FirstFreeSigil) {
		m.errorf(a.site, "sparrow-id", "%d can't be the sigil of %s: a facet's own sigils start at 64", v, n.Name)
		return
	}
	if m.idTooLarge(v, a.site, "sigil", n.Name) {
		return
	}
	s.ID, s.explicit, s.idSite = uint64(v), true, a.site
}

// errorSigil is the kind-1 sigil of an error without a core code.
func (f *SparrowFacet) errorSigil(m *Model, e *Error) *SparrowSigil {
	if s := f.sigilOf[e.Node]; s != nil {
		return s
	}
	s := &SparrowSigil{Name: e.Node.Name, Kind: schema.SigilError, Node: e.Node}
	if Own(e.Node) {
		m.sigilID(s, e.Node)
	}
	f.sigilOf[e.Node] = s
	f.Sigils = append(f.Sigils, s)
	return s
}

// finishSparrow numbers the facets and what's in them, explicit ids first
// and the rest in the order reached, then builds and validates each facet.
func (m *Model) finishSparrow() {
	usedFacet := map[uint64]*SparrowFacet{}
	for _, f := range m.Facets {
		if !f.explicitID {
			continue
		}
		if prev := usedFacet[f.ID]; prev != nil {
			m.errorf(f.idSite, "sparrow-id", "facet %s has id %d, which facet %s already has", f.Name, f.ID, prev.Name)
			continue
		}
		usedFacet[f.ID] = f
	}
	next := schema.FirstFreeFacetID
	for _, f := range m.Facets {
		if f.explicitID {
			continue
		}
		for usedFacet[next] != nil {
			next++
		}
		f.ID = next
		usedFacet[next] = f
	}
	for _, f := range m.Facets {
		m.numberFacet(f)
	}
	sort.SliceStable(m.Facets, func(i, j int) bool { return m.Facets[i].ID < m.Facets[j].ID })
	for _, f := range m.Facets {
		def := f.build()
		if err := def.Validate(); err != nil {
			m.errorf(f.api.Decl, "sparrow", "facet %s isn't a valid Sparrow facet: %v", f.Name, err)
			continue
		}
		h, err := def.Hash()
		if err != nil {
			m.errorf(f.api.Decl, "sparrow", "facet %s can't be hashed: %v", f.Name, err)
			continue
		}
		f.Def, f.Hash = def, h
	}
}

// idSpace assigns ids in one space: the explicit ones are checked, the
// others take the next free id from first, skipping retired ones.
type idSpace struct {
	m     *Model
	f     *SparrowFacet
	space schema.Space
	what  string
	first uint64
	used  map[uint64]string
}

func (sp *idSpace) claim(id uint64, name string, at resolve.Site) {
	if sp.f.retired[schema.Retired{Space: sp.space, ID: id}] {
		sp.m.errorf(at, "sparrow-id", "%s has %s %d, which facet %s retires", name, sp.what, id, sp.f.Name)
		return
	}
	if prev, ok := sp.used[id]; ok {
		sp.m.errorf(at, "sparrow-id", "%s has %s %d, which %s already has in facet %s", name, sp.what, id, prev, sp.f.Name)
		return
	}
	sp.used[id] = name
}

func (sp *idSpace) free(n int) uint64 {
	id := sp.first
	for {
		ok := true
		for k := 0; k < n; k++ {
			if _, taken := sp.used[id+uint64(k)]; taken || sp.f.retired[schema.Retired{Space: sp.space, ID: id + uint64(k)}] {
				ok = false
				break
			}
		}
		if ok {
			return id
		}
		id++
	}
}

func (sp *idSpace) assign(name string) uint64 {
	id := sp.free(1)
	sp.used[id] = name
	return id
}

func (m *Model) numberFacet(f *SparrowFacet) {
	types := &idSpace{m: m, f: f, space: schema.SpaceType, what: "TypeId", first: schema.FirstFreeTypeID, used: map[uint64]string{}}
	sigils := &idSpace{m: m, f: f, space: schema.SpaceSigil, what: "sigil", first: schema.FirstFreeSigil, used: map[uint64]string{}}
	svcs := &idSpace{m: m, f: f, space: schema.SpaceService, what: "service id", used: map[uint64]string{}}
	meths := &idSpace{m: m, f: f, space: schema.SpaceMethod, what: "method id", used: map[uint64]string{}}
	someTypes, someMethods := false, false
	for _, t := range f.Types {
		if t.explicit {
			someTypes = true
			types.claim(t.ID, t.Name, t.idSite)
		}
	}
	for _, s := range f.Sigils {
		if s.explicit {
			sigils.claim(s.ID, s.Name, s.idSite)
		}
	}
	for _, s := range f.Services {
		if s.explicit {
			svcs.claim(s.ID, s.Name, s.idSite)
		}
		for _, me := range s.Methods {
			if me.explicit {
				someMethods = true
				meths.claim(me.ID, me.Name, me.idSite)
			}
		}
	}
	for _, t := range f.Types {
		switch {
		case t.explicit:
		case t == f.value:
			// Value and VariantValue take two ids in a row.
			id := types.free(2)
			types.used[id], types.used[id+1] = "Value", "VariantValue"
			f.value.ID, f.variant.ID = id, id+1
		case t == f.variant:
		default:
			t.ID = types.assign(t.Name)
			if someTypes && t.Node != nil && Own(t.Node) {
				m.sparrowLint(t.Node, t.Node.Decl, "%s has no @sparrow.id while other types of facet %s do: its TypeId, %d, follows the order the facet's methods reach it", t.Name, f.Name, t.ID)
			}
		}
	}
	for _, s := range f.Sigils {
		if !s.explicit {
			s.ID = sigils.assign(s.Name)
		}
	}
	for _, s := range f.Services {
		if !s.explicit {
			s.ID = svcs.assign(s.Name)
		}
		for _, me := range s.Methods {
			if !me.explicit {
				me.ID = meths.assign(me.Name)
				if someMethods {
					m.sparrowLint(me.Op, me.Op.Decl, "%s has no id in @sparrow while other methods of facet %s do: its method id, %d, follows the order the ops are written", me.Op.Name, f.Name, me.ID)
				}
			}
		}
	}
}

func (m *Model) sparrowLint(n *resolve.Node, s resolve.Site, format string, args ...any) {
	m.Lints = append(m.Lints, Finding{Node: n, Site: s, Message: fmt.Sprintf(format, args...), Lint: "sparrow-id"})
}

// build writes the facet as Sparrow's schema package holds it.
func (f *SparrowFacet) build() *schema.Facet {
	def := &schema.Facet{ID: f.ID, Name: f.Name, Version: f.Version, Retired: append([]schema.Retired(nil), f.Retired...)}
	for _, t := range f.Types {
		if t.Builtin {
			if t == f.value {
				def.Types = append(def.Types, SparrowValueTypes(t.ID)...)
			}
			continue
		}
		td := schema.TypeDef{Kind: t.Kind, ID: t.ID, Name: t.Name}
		switch t.Kind {
		case schema.KindStruct, schema.KindUnion:
			for _, x := range t.Fields {
				var fl uint64
				if x.Optional {
					fl |= schema.FieldOptional
				}
				if x.Node != nil && isDeprecated(x.Node) {
					fl |= schema.FieldDeprecated
				}
				td.Fields = append(td.Fields, schema.Field{Tag: x.Tag, Name: x.Name, Type: x.Type.Ref(), Flags: fl})
			}
			if t.Sparse {
				td.Flags = schema.StructSparse
			}
		case schema.KindEnum:
			for _, v := range t.Values {
				td.Values = append(td.Values, v.ID)
			}
		}
		def.Types = append(def.Types, td)
	}
	for _, s := range f.Sigils {
		def.Sigils = append(def.Sigils, schema.Sigil{ID: s.ID, Name: s.Name, Kind: s.Kind})
	}
	for _, s := range f.Services {
		sd := schema.Service{ID: s.ID, Name: s.Name}
		for _, me := range s.Methods {
			sd.Methods = append(sd.Methods, schema.Method{ID: me.ID, Name: me.Name, Input: me.Input.Ref(), Output: me.Output.Ref(), Errors: me.ErrorSigils(), Flags: me.Flags()})
		}
		def.Services = append(def.Services, sd)
	}
	return def.Canonical()
}

// isDeprecated reports a member carrying std/doc's @deprecated.
func isDeprecated(n *resolve.Node) bool {
	for _, s := range n.Annotations {
		if s.N == nil || s.N.Kind != syntax.Annotation {
			continue
		}
		segs := resolve.PathSegs(ast.Annotation{N: s.N}.Path().N)
		if last(segs) == "deprecated" {
			return true
		}
	}
	return false
}

// SparrowSchema is the tree's facets as one Sparrow schema, named after
// its first facet; nil when the tree binds none or one isn't valid.
func (m *Model) SparrowSchema() *schema.Schema {
	if len(m.Facets) == 0 {
		return nil
	}
	s := &schema.Schema{Format: schema.Format, Name: m.Facets[0].Name, Version: m.Facets[0].Version}
	for _, f := range m.Facets {
		if f.Def == nil {
			return nil
		}
		s.Facets = append(s.Facets, *f.Def)
	}
	return s.Canonical()
}

// SparrowCode is a core status code's number by its name, as not_found.
func SparrowCode(name string) (uint64, bool) {
	c, ok := status.ByName(name)
	return uint64(c), ok
}

// SparrowFiles writes each facet as its canonical CBOR, <facet>.sparrow,
// beside a JSON rendering with its hash, and the schema of every facet as
// schema.sparrow and schema.json.
func (m *Model) SparrowFiles() ([]File, error) {
	s := m.SparrowSchema()
	if s == nil {
		return nil, nil
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("the Sparrow schema isn't valid: %w", err)
	}
	var out []File
	for _, f := range m.Facets {
		b, err := f.Def.CanonicalBytes()
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: f.Name + ".sparrow", Content: b})
		j, err := jsonIndent(f.jsonForm())
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: f.Name + ".json", Content: j})
	}
	b, err := s.CanonicalBytes()
	if err != nil {
		return nil, err
	}
	h, err := s.Hash()
	if err != nil {
		return nil, err
	}
	out = append(out, File{Path: "schema.sparrow", Content: b})
	type facetEntry struct {
		ID   uint64 `json:"id"`
		Name string `json:"name"`
		Hash string `json:"hash"`
		File string `json:"file"`
	}
	doc := struct {
		Format  uint64       `json:"format"`
		Name    string       `json:"name"`
		Version string       `json:"version"`
		Hash    string       `json:"hash"`
		Facets  []facetEntry `json:"facets"`
	}{s.Format, s.Name, s.Version, hex.EncodeToString(h), nil}
	for _, f := range m.Facets {
		doc.Facets = append(doc.Facets, facetEntry{f.ID, f.Name, f.HashHex(), f.Name + ".sparrow"})
	}
	j, err := jsonIndent(doc)
	if err != nil {
		return nil, err
	}
	out = append(out, File{Path: "schema.json", Content: j})
	return out, nil
}

// jsonIndent writes a value as indented JSON, list<T> left as written.
func jsonIndent(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	return b.Bytes(), err
}

// The JSON rendering of a facet, for people and tools: every definition
// by id, references written as the docs write them.
type jsonFacet struct {
	ID       uint64        `json:"id"`
	Name     string        `json:"name"`
	Version  string        `json:"version"`
	Hash     string        `json:"hash"`
	Services []jsonService `json:"services"`
	Types    []jsonType    `json:"types"`
	Sigils   []jsonSigil   `json:"sigils"`
	Retired  []jsonRetired `json:"retired,omitempty"`
}

type jsonService struct {
	ID      uint64       `json:"id"`
	Name    string       `json:"name"`
	Methods []jsonMethod `json:"methods"`
}

type jsonMethod struct {
	ID     uint64   `json:"id"`
	Name   string   `json:"name"`
	Shape  string   `json:"shape"`
	Input  string   `json:"input"`
	Output string   `json:"output"`
	Errors []uint64 `json:"errors"`
	Flags  []string `json:"flags"`
}

type jsonType struct {
	ID     uint64      `json:"id"`
	Kind   string      `json:"kind"`
	Name   string      `json:"name"`
	Sparse bool        `json:"sparse,omitempty"`
	Fields []jsonField `json:"fields,omitempty"`
	Values []uint64    `json:"values,omitempty"`
}

type jsonField struct {
	Tag      uint64 `json:"tag"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Optional bool   `json:"optional,omitempty"`
}

type jsonSigil struct {
	ID   uint64 `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type jsonRetired struct {
	Space string `json:"space"`
	ID    uint64 `json:"id"`
}

// refString writes a reference of a built definition, by the facet's
// type names.
func refString(r schema.TypeRef, types map[uint64]string) string {
	switch r.Kind {
	case schema.RefList:
		return "list<" + refString(*r.Elem, types) + ">"
	case schema.RefMap:
		return "map<" + refString(*r.Key, types) + ", " + refString(*r.Elem, types) + ">"
	case schema.RefOptional:
		return "optional<" + refString(*r.Elem, types) + ">"
	case schema.RefStream:
		return "stream<" + refString(*r.Elem, types) + ">"
	case schema.RefByteStream:
		return "bytestream"
	}
	if n, ok := types[r.ID]; ok {
		return n
	}
	if r.ID < uint64(len(builtinNames)) {
		return builtinNames[r.ID]
	}
	return strconv.FormatUint(r.ID, 10)
}

func (f *SparrowFacet) jsonForm() jsonFacet {
	d := f.Def
	out := jsonFacet{ID: d.ID, Name: d.Name, Version: d.Version, Hash: f.HashHex(), Services: []jsonService{}, Types: []jsonType{}, Sigils: []jsonSigil{}}
	names := map[uint64]string{}
	for _, t := range d.Types {
		names[t.ID] = t.Name
	}
	shape := map[uint64]string{}
	for _, s := range f.Services {
		for _, me := range s.Methods {
			shape[me.ID] = me.Shape()
		}
	}
	for _, s := range d.Services {
		js := jsonService{ID: s.ID, Name: s.Name, Methods: []jsonMethod{}}
		for _, me := range s.Methods {
			jm := jsonMethod{ID: me.ID, Name: me.Name, Shape: shape[me.ID], Input: refString(me.Input, names), Output: refString(me.Output, names), Errors: me.Errors}
			if jm.Errors == nil {
				jm.Errors = []uint64{}
			}
			jm.Flags = flagNames(me.Flags)
			js.Methods = append(js.Methods, jm)
		}
		out.Services = append(out.Services, js)
	}
	for _, t := range d.Types {
		jt := jsonType{ID: t.ID, Kind: t.Kind.String(), Name: t.Name, Sparse: t.Sparse(), Values: t.Values}
		if t.Kind == schema.KindAlias {
			jt.Fields = []jsonField{{Name: "target", Type: refString(t.Target, names)}}
		}
		for _, x := range t.Fields {
			jt.Fields = append(jt.Fields, jsonField{Tag: x.Tag, Name: x.Name, Type: refString(x.Type, names), Optional: x.Optional()})
		}
		out.Types = append(out.Types, jt)
	}
	for _, s := range d.Sigils {
		k := "enum value"
		switch s.Kind {
		case schema.SigilError:
			k = "error"
		case schema.SigilSymbol:
			k = "symbol"
		}
		out.Sigils = append(out.Sigils, jsonSigil{s.ID, s.Name, k})
	}
	for _, r := range d.Retired {
		out.Retired = append(out.Retired, jsonRetired{r.Space.String(), r.ID})
	}
	return out
}
