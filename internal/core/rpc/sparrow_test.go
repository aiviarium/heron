package rpc_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/aiviarium/sparrow/go/schema"

	"github.com/aiviarium/heron/internal/core/rpc"
)

func sparrowMethod(t *testing.T, m *rpc.Model, name string) *rpc.SparrowMethod {
	t.Helper()
	for _, f := range m.Facets {
		for _, s := range f.Services {
			for _, me := range s.Methods {
				if me.Name == name {
					return me
				}
			}
		}
	}
	t.Fatalf("no Sparrow method %s", name)
	return nil
}

func sparrowType(t *testing.T, f *rpc.SparrowFacet, name string) *rpc.SparrowType {
	t.Helper()
	for _, x := range f.Types {
		if x.Name == name {
			return x
		}
	}
	t.Fatalf("facet %s has no type %s", f.Name, name)
	return nil
}

func sparrowField(t *testing.T, x *rpc.SparrowType, name string) *rpc.SparrowField {
	t.Helper()
	for _, f := range x.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("%s has no field %s", x.Name, name)
	return nil
}

// The parcels facet: its id, version and service, the methods with their
// ids, shapes and flags, the inputs made from the parameters, explicit
// and declaration-order tags, enum values as sigils, and errors as core
// codes or sigils of their own; the facet validates and has its hash.
func TestSparrowFacet(t *testing.T) {
	m, _ := parcels(t)
	if len(m.Facets) != 1 {
		t.Fatalf("facets: %d", len(m.Facets))
	}
	f := m.Facets[0]
	if f.Name != "acme.parcels" || f.ID != 16 || f.Version != "1.0.0" || len(f.Services) != 1 || f.Services[0].Name != "Parcels" || f.Services[0].ID != 0 {
		t.Fatalf("facet: %s %d %s %+v", f.Name, f.ID, f.Version, f.Services)
	}
	if f.Def == nil || len(f.Hash) != schema.HashLen || f.Hash[0] != schema.HashAlgSHA256 {
		t.Fatalf("the facet isn't built: %v", m.Diags)
	}
	if err := f.Def.Validate(); err != nil {
		t.Error(err)
	}
	if h, _ := f.Def.Hash(); !bytes.Equal(h, f.Hash) || f.HashHex() != hex.EncodeToString(h) {
		t.Error("hash")
	}
	for name, want := range map[string]struct {
		id    uint64
		shape string
		flags string
		in    string
		out   string
	}{
		"getParcel":   {0, "unary", "idempotent", "GetParcelInput", "Parcel"},
		"createLabel": {1, "unary", "idempotent", "CreateLabelInput", "Parcel"},
		"watchParcel": {2, "server", "shared,output_end", "WatchParcelInput", "stream<ParcelEvent>"},
		"uploadScans": {3, "client", "input_end", "stream<Scan>", "ScanSummary"},
		"navigate":    {4, "bidi", "input_end,output_end", "stream<Position>", "stream<Instruction>"},
	} {
		me := sparrowMethod(t, m, name)
		if me.ID != want.id || me.Shape() != want.shape || strings.Join(me.FlagNames(), ",") != want.flags || me.Input.String() != want.in || me.Output.String() != want.out {
			t.Errorf("%s: %d %s %v %s %s", name, me.ID, me.Shape(), me.FlagNames(), me.Input, me.Output)
		}
	}
	if m.SparrowMethod(sparrowMethod(t, m, "getParcel").Op) == nil || m.SparrowServiceOf(f.Services[0].API) != f.Services[0] {
		t.Error("lookups")
	}
	get := sparrowMethod(t, m, "getParcel")
	if in := get.InputType; in == nil || len(in.Fields) != 2 || in.Fields[0].Tag != 0 || in.Fields[1].Name != "tenant" || !in.Fields[1].Optional {
		t.Errorf("input: %+v", get.InputType)
	}
	p := sparrowType(t, f, "Parcel")
	if m.SparrowTypeOf(p.Node) != p || p.Kind != schema.KindStruct || p.ID < 64 {
		t.Errorf("Parcel: %+v", p)
	}
	for name, tag := range map[string]uint64{"id": 0, "status": 1, "weight_grams": 2, "destination": 4, "labels": 5} {
		if x := sparrowField(t, p, name); x.Tag != tag {
			t.Errorf("%s: tag %d, want %d", name, x.Tag, tag)
		}
	}
	if x := sparrowField(t, p, "destination"); !x.Optional || x.Type.String() != "Destination" {
		t.Errorf("destination: %+v", x)
	}
	if x := sparrowField(t, p, "labels"); x.Type.String() != "map<string, string>" || x.Optional {
		t.Errorf("labels: %+v", x)
	}
	if x := sparrowField(t, p, "weight_grams"); x.Type.String() != "int" {
		t.Errorf("weight_grams: %s", x.Type)
	}
	if x := sparrowField(t, sparrowType(t, f, "ParcelEvent"), "at"); x.Type.String() != "timestamp" || !x.Optional {
		t.Errorf("at: %+v", x)
	}
	if x := sparrowField(t, sparrowType(t, f, "Position"), "lat"); x.Type.String() != "float64" {
		t.Errorf("lat: %s", x.Type)
	}
	d := sparrowType(t, f, "Destination")
	if d.Kind != schema.KindUnion || len(d.Fields) != 2 || d.Fields[1].Name != "locker" || d.Fields[1].Tag != 1 {
		t.Errorf("a union: %+v", d)
	}
	st := sparrowType(t, f, "ParcelStatus")
	if st.Kind != schema.KindEnum || len(st.Values) != 4 || st.Values[1].Name != "ParcelStatus.IN_TRANSIT" || st.Values[0].ID < 64 || st.Values[0].Kind != schema.SigilEnumValue {
		t.Errorf("an enum: %+v", st.Values)
	}
	if e := get.Errors[0]; e.Sparrow != "not_found" || e.SparrowSigil != nil {
		t.Errorf("a core code: %+v", e)
	}
	if code, ok := rpc.SparrowCode("not_found"); !ok || code != 5 {
		t.Error("not_found is 5")
	}
	label := sparrowMethod(t, m, "createLabel")
	if e := label.Errors[0]; e.Sparrow != "" || e.SparrowSigil == nil || e.SparrowSigil.ID != 80 || e.SparrowSigil.Kind != schema.SigilError {
		t.Errorf("a sigil of its own: %+v", e)
	}
	if got := label.ErrorSigils(); len(got) != 1 || got[0] != 80 {
		t.Errorf("errors: %v", got)
	}
	if nf := sparrowType(t, f, "ParcelNotFound"); len(nf.Fields) != 1 || nf.Fields[0].Name != "id" || !nf.Error {
		t.Errorf("an error's detail type leaves its message out: %+v", nf)
	}
}

const shead = "import = [\"std/types@1\", \"std/sparrow@1\", \"std/spec@1\"]\n"

// More of the projection: Any as the Value union, the primitive types,
// optional and nullable values, generic instances and Patch, sparse
// structs, reserved tags, explicit ids, retired ids, names authors set,
// a name a type already has, and notifications.
func TestSparrowProjectionRules(t *testing.T) {
	m, res := build(t, map[string]string{"index.heron": shead + `
@sparrow("acme.shop", version = "2.1.0", service = "Shop", id = 3)
@retired(types = [64, 65], methods = [0])
shop: api {
  @sparrow(method = "find", id = 7)
  list(cursor?: String): op(Page(Item))
  @sparrow(id = 8)
  get(id: String): op(GetInput)
  @sparrow(id = 9)
  stats(): op(Map(String, Int))
  @sparrow(id = 10)
  ping(): op(Null)
  @sparrow(id = 11)
  touched(at: Timestamp): op
  @sparrow(id = 12)
  update(@tag(3) p: Patch(Item), @tag(1) mode: Mode): op(Item)
}

GetInput: type { id: String }

Page(T: Any): type {
  items: List(T)
  next?: String
}

@id(100)
@sparse
@reserved(1)
Item: type {
  delta: Int
  count: u32
  ratio: f32
  data: Any
  attrs: Map(String, Any)
  took: Duration
  note: String | Null
  price: Decimal
  tags: Set(String)
}

@id(101)
Mode: enum {
  @sigil(70)
  fast: Self
  @sigil(71)
  slow: Self
}
`})
	if res.Diags.HasErrors() {
		t.Fatalf("%s", diags(res))
	}
	f := m.Facets[0]
	if f.Name != "acme.shop" || f.Version != "2.1.0" || f.ID != 16 || f.Services[0].Name != "Shop" || f.Services[0].ID != 3 {
		t.Errorf("facet: %s %s %d %+v", f.Name, f.Version, f.ID, f.Services[0])
	}
	if len(f.Retired) != 3 || !f.Def.IsRetired(schema.SpaceType, 65) || !f.Def.IsRetired(schema.SpaceMethod, 0) {
		t.Errorf("retired: %+v", f.Retired)
	}
	find := sparrowMethod(t, m, "find")
	if find.ID != 7 || find.Output.String() != "PageItem" {
		t.Errorf("find: %d %s", find.ID, find.Output)
	}
	page := sparrowType(t, f, "PageItem")
	if page.ID < 66 || page.ID == 100 || page.Node != nil || sparrowField(t, page, "items").Type.String() != "list<Item>" {
		t.Errorf("a generic instance after the retired ids: %+v", page)
	}
	if get := sparrowMethod(t, m, "get"); get.InputType.Name != "ShopGetInput" {
		t.Errorf("a name a type has: %s", get.InputType.Name)
	}
	if s := sparrowMethod(t, m, "stats"); s.Input.String() != "unit" || s.Output.String() != "map<string, int>" {
		t.Errorf("stats: %s %s", s.Input, s.Output)
	}
	if p := sparrowMethod(t, m, "ping"); p.Notification || p.Output.String() != "unit" {
		t.Error("a Null result is answered")
	}
	if tc := sparrowMethod(t, m, "touched"); !tc.Notification || strings.Join(tc.FlagNames(), ",") != "notification" {
		t.Errorf("an op with no result is a notification: %v", tc.FlagNames())
	}
	up := sparrowMethod(t, m, "update")
	if in := up.InputType; in.Fields[0].Tag != 3 || in.Fields[1].Tag != 1 || in.Fields[1].Type.String() != "Mode" {
		t.Errorf("tagged params: %+v", in.Fields)
	}
	patch := sparrowType(t, f, "PatchItem")
	for _, x := range patch.Fields {
		if !x.Optional {
			t.Errorf("Patch's %s isn't optional", x.Name)
		}
	}
	item := sparrowType(t, f, "Item")
	if item.ID != 100 || !item.Sparse {
		t.Errorf("Item: %d %v", item.ID, item.Sparse)
	}
	for name, want := range map[string]string{"delta": "int", "count": "uint", "ratio": "float32", "data": "Value", "attrs": "map<string, Value>", "took": "int", "note": "optional<string>", "price": "string", "tags": "list<string>"} {
		if x := sparrowField(t, item, name); x.Type.String() != want {
			t.Errorf("%s: %s, want %s", name, x.Type, want)
		}
	}
	if sparrowField(t, item, "delta").Tag != 0 || sparrowField(t, item, "count").Tag != 2 {
		t.Error("tags skip the reserved ones")
	}
	mode := sparrowType(t, f, "Mode")
	if mode.ID != 101 || mode.Values[0].ID != 70 || mode.Values[1].ID != 71 {
		t.Errorf("Mode: %+v", mode)
	}
	// Value and VariantValue: two ids in a row, defined as the helper does.
	v := sparrowType(t, f, "Value")
	vv := f.Def.Type(v.ID + 1)
	if vv == nil || vv.Name != "VariantValue" || f.Def.Type(v.ID).Kind != schema.KindUnion || len(f.Def.Type(v.ID).Fields) != 17 {
		t.Fatalf("Value: %+v %+v", f.Def.Type(v.ID), vv)
	}
	want := rpc.SparrowValueTypes(v.ID)
	if want[0].Fields[14].Type.ID != v.ID+1 || want[1].Fields[1].Type.ID != v.ID || want[0].Fields[13].Type.Kind != schema.RefMap {
		t.Errorf("SparrowValueTypes: %+v", want)
	}
	// Without Any, neither is defined.
	m2, _ := build(t, map[string]string{"index.heron": shead + "@sparrow\nx: api {\n  a(s: String): op(String)\n}\n"})
	for _, x := range m2.Facets[0].Def.Types {
		if x.Name == "Value" {
			t.Error("Value without Any")
		}
	}
	if m2.Facets[0].Name != "x" || m2.Facets[0].Version != rpc.DefaultSparrowVersion || sparrowMethod(t, m2, "a").ID != 0 {
		t.Errorf("defaults: %+v", m2.Facets[0])
	}
}

// Apis naming one facet share it, and facets without an id take the next
// free one from 16.
func TestSparrowSharedFacet(t *testing.T) {
	m, res := build(t, map[string]string{"index.heron": shead + `
@sparrow("acme.a")
one: api {
  a(): op(String)
}

@sparrow("acme.a", version = "2.0.0")
two: api {
  b(): op(String)
}

@sparrow("acme.b", facet_id = 16)
three: api {
  c(): op(String)
}
`})
	if res.Diags.HasErrors() {
		t.Fatalf("%s", diags(res))
	}
	if len(m.Facets) != 2 || m.Facets[0].Name != "acme.b" || m.Facets[1].Name != "acme.a" || m.Facets[1].ID != 17 {
		t.Fatalf("facets: %+v", m.Facets)
	}
	a := m.Facets[1]
	if a.Version != "2.0.0" {
		t.Errorf("a version any api of the facet sets: %s", a.Version)
	}
	if len(a.Services) != 2 || a.Services[1].ID != 1 || sparrowMethod(t, m, "b").ID != 1 {
		t.Errorf("one facet, two services, methods numbered across it: %+v", a.Services)
	}
	s := m.SparrowSchema()
	if s == nil || len(s.Facets) != 2 || s.Validate() != nil {
		t.Fatalf("schema: %+v", s)
	}
}

// What the checker reports about Sparrow bindings.
func TestSparrowDiagnostics(t *testing.T) {
	for src, want := range map[string]string{
		"x: api {\n  @sparrow(id = 1)\n  a(): op\n}\n":                                                                                           "a carries @sparrow, but its api isn't bound to a Sparrow facet",
		"@sparrow(method = \"m\")\nx: api {\n  a(): op\n}\n":                                                                                     "@sparrow on api doesn't take method",
		"@sparrow\nx: api {\n  @sparrow(service = \"S\")\n  a(): op\n}\n":                                                                        "@sparrow on op doesn't take service",
		"@sparrow(\"Acme_Parcels\")\nx: api {\n  a(): op\n}\n":                                                                                   "Acme_Parcels isn't a valid facet name",
		"@sparrow(version = \"one\")\nx: api {\n  a(): op\n}\n":                                                                                  "one isn't a version",
		"@sparrow(\"f\", version = \"1.0\")\nx: api {\n  a(): op\n}\n@sparrow(\"f\", version = \"2.0\")\ny: api {\n  b(): op\n}\n":               "y puts facet f at version 2.0, which x puts at 1.0",
		"@sparrow(facet_id = 3)\nx: api {\n  a(): op\n}\n":                                                                                       "3 can't be the id of facet x",
		"@sparrow(\"f\", facet_id = 20)\nx: api {\n  a(): op\n}\n@sparrow(\"g\", facet_id = 20)\ny: api {\n  b(): op\n}\n":                       "facet g has id 20, which facet f already has",
		"@sparrow\nx: api {\n  @sparrow(id = 1)\n  a(): op\n  @sparrow(id = 1)\n  b(): op\n}\n":                                                  "b has method id 1, which a already has in facet x",
		"@sparrow\n@retired(methods = [1])\nx: api {\n  @sparrow(id = 1)\n  a(): op\n}\n":                                                        "a has method id 1, which facet x retires",
		"@sparrow(\"f\", facet_id = 20)\nx: api {\n  a(): op\n}\n@sparrow(\"f\", facet_id = 21)\ny: api {\n  b(): op\n}\n":                       "y gives facet f the id 21, which it already has as 20",
		"@sparrow\nx: api {\n  @sparrow(id = -1)\n  a(): op\n}\n":                                                                                "-1 can't be the id of method a: ids can't be negative",
		"@sparrow\nx: api {\n  a(s: Stream(String), b: String): op\n}\n":                                                                         "a streams from the client, so b has no place in its call",
		"@sparrow\nx: api {\n  @throws(E)\n  a(): op\n}\nE: type(Error) {\n  message: String\n}\n":                                               "a is a notification, which is never answered, so it can't throw E",
		"@sparrow\nx: api {\n  a(t: T): op\n}\nT: type {\n  @tag(0)\n  a: String\n  b: String\n}\n":                                              "T tags some fields with @tag and not b",
		"@sparrow\nx: api {\n  a(t: T): op\n}\nT: type {\n  @tag(0)\n  a: String\n  @tag(0)\n  b: String\n}\n":                                   "b has tag 0, which a already has in T",
		"@sparrow\nx: api {\n  a(t: T): op\n}\n@reserved(2)\nT: type {\n  @tag(2)\n  a: String\n}\n":                                             "a has tag 2, which T reserves",
		"@sparrow\nx: api {\n  a(t: T): op\n}\nT: type {\n  @tag(-1)\n  a: String\n}\n":                                                          "-1 can't tag a",
		"@sparrow\nx: api {\n  a(t: T): op\n}\n@id(12)\nT: type {\n  a: String\n}\n":                                                             "12 can't be the TypeId of T",
		"@sparrow\nx: api {\n  a(t: T, u: U): op\n}\n@id(64)\nT: type {\n  a: String\n}\n@id(64)\nU: type {\n  b: String\n}\n":                   "U has TypeId 64, which T already has in facet x",
		"@sparrow\n@retired(types = [64])\nx: api {\n  a(t: T): op\n}\n@id(64)\nT: type {\n  a: String\n}\n":                                     "T has TypeId 64, which facet x retires",
		"@sparrow\nx: api {\n  a(t: P(String)): op\n}\n@id(70)\nP(X: Any): type {\n  a: X\n}\n":                                                  "P is generic",
		"@sparrow\nx: api {\n  a(e: E): op\n}\nE: enum {\n  @sigil(64)\n  A: Self\n  B: Self\n}\n":                                               "E gives some values a @sigil and not B",
		"@sparrow\nx: api {\n  a(e: E): op\n}\nE: enum {\n  @sigil(5)\n  A: Self\n}\n":                                                           "5 can't be the sigil of A",
		"@sparrow\nx: api {\n  a(t: String | Int): op\n}\n":                                                                                      "has no Sparrow form: a union expression other than T | Null",
		"@sparrow\nx: api {\n  @throws(E)\n  a(): op(String)\n}\n@sparrow.code(not_found)\n@sigil(70)\nE: type(Error) {\n  message: String\n}\n": "E has a core code, @sparrow.code(not_found), and a sigil of its own",
	} {
		_, res := build(t, map[string]string{"index.heron": shead + src})
		if got := diags(res); !strings.Contains(got, want) {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}
}

// What the linter reports: ids left to the order types and ops are
// reached in when others of the facet are written.
func TestSparrowIDLints(t *testing.T) {
	m, res := build(t, map[string]string{"index.heron": shead + `
@sparrow
x: api {
  @sparrow(id = 5)
  a(t: T): op(U)
  b(): op(String)
}

@id(64)
T: type { a: String }

U: type { b: String }
`})
	if res.Diags.HasErrors() {
		t.Fatalf("%s", diags(res))
	}
	var got []string
	for _, f := range m.Lints {
		got = append(got, f.Message)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{
		"U has no @sparrow.id while other types of facet x do: its TypeId, 65,",
		"b has no id in @sparrow while other methods of facet x do: its method id, 0,",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
	if len(got) != 2 {
		t.Errorf("an input struct isn't reported:\n%s", all)
	}
}

// The generated files: each facet's canonical CBOR, which decodes to the
// facet and hashes to its hash, a JSON rendering, and the schema.
func TestSparrowFiles(t *testing.T) {
	m, _ := parcels(t)
	files, err := m.SparrowFiles()
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string][]byte{}
	var paths []string
	for _, f := range files {
		byPath[f.Path] = f.Content
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, ",") != "acme.parcels.sparrow,acme.parcels.json,schema.sparrow,schema.json" {
		t.Fatalf("files: %v", paths)
	}
	f, err := schema.DecodeFacet(byPath["acme.parcels.sparrow"])
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := f.Hash(); !bytes.Equal(h, m.Facets[0].Hash) {
		t.Error("the file doesn't hash to the facet's hash")
	}
	if canon, _ := f.CanonicalBytes(); !bytes.Equal(canon, byPath["acme.parcels.sparrow"]) {
		t.Error("the file isn't canonical")
	}
	s, err := schema.DecodeSchema(byPath["schema.sparrow"])
	if err != nil || s.Validate() != nil || len(s.Facets) != 1 {
		t.Fatalf("schema: %v", err)
	}
	sh, _ := s.Hash()
	j := string(byPath["acme.parcels.json"])
	for _, want := range []string{`"name": "acme.parcels"`, `"hash": "` + m.Facets[0].HashHex() + `"`, `"name": "getParcel"`, `"output": "stream<ParcelEvent>"`, `"kind": "error"`, `"type": "map<string, string>"`} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %s in\n%s", want, j)
		}
	}
	if !strings.Contains(string(byPath["schema.json"]), hex.EncodeToString(sh)) {
		t.Error("schema.json lacks the schema hash")
	}
}
