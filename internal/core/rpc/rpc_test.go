package rpc_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/rpc"
)

// build checks a tree of files and projects it.
func build(t *testing.T, files map[string]string) (*rpc.Model, *check.Result) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(src), 0o644)
	}
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	res := check.NewSession().Check(id)
	return rpc.Build(res.Tree, res.Checker), res
}

func parcels(t *testing.T) (*rpc.Model, *check.Result) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "examples", "parcels")
	files := map[string]string{}
	for _, f := range []string{"index.heron", "model.heron"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		files[f] = string(b)
	}
	m, res := build(t, files)
	if res.Diags.HasErrors() {
		t.Fatalf("the parcels example doesn't check: %v", res.Diags)
	}
	return m, res
}

func diags(res *check.Result) string {
	var b strings.Builder
	for _, d := range res.Diags {
		b.WriteString(d.Code + ": " + d.Message + "\n")
	}
	return b.String()
}

func method(t *testing.T, m *rpc.Model, name string) *rpc.Method {
	t.Helper()
	for _, s := range m.Services {
		for _, me := range s.Methods {
			if me.Name == name {
				return me
			}
		}
	}
	t.Fatalf("no method %s", name)
	return nil
}

func field(t *testing.T, msg *rpc.Message, name string) *rpc.Field {
	t.Helper()
	for _, f := range msg.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("%s has no field %s", msg.Name, name)
	return nil
}

func message(t *testing.T, m *rpc.Model, pkg, name string) *rpc.Message {
	t.Helper()
	for _, msg := range m.Packages[pkg].Messages {
		if msg.Name == name {
			return msg
		}
	}
	t.Fatalf("no message %s", name)
	return nil
}

// The service, its methods and their streaming kinds, requests, responses,
// metadata, idempotency, time limits and errors.
func TestServiceProjection(t *testing.T) {
	m, _ := parcels(t)
	if len(m.Services) != 1 {
		t.Fatalf("services: %d", len(m.Services))
	}
	s := m.Services[0]
	if s.FullName() != "acme.parcels.v1.ParcelsService" || strings.Join(s.Protocols, ",") != "grpc,grpc_web,connect" || s.Servers[0] != "https://parcels.example.com" {
		t.Errorf("service: %s %v %v", s.FullName(), s.Protocols, s.Servers)
	}
	kinds := map[string]rpc.Kind{"GetParcel": rpc.Unary, "CreateLabel": rpc.Unary, "WatchParcel": rpc.ServerStreaming, "UploadScans": rpc.ClientStreaming, "Navigate": rpc.BidiStreaming}
	for name, k := range kinds {
		if me := method(t, m, name); me.Kind != k {
			t.Errorf("%s: %v, want %v", name, me.Kind, k)
		}
	}
	get := method(t, m, "GetParcel")
	if get.Path() != "/acme.parcels.v1.ParcelsService/GetParcel" || get.Idempotency != "NO_SIDE_EFFECTS" {
		t.Errorf("GetParcel: %s %s", get.Path(), get.Idempotency)
	}
	if get.Request.Name != "GetParcelRequest" || len(get.Request.Fields) != 1 || get.Request.Fields[0].Name != "id" {
		t.Errorf("request: %+v", get.Request)
	}
	if len(get.Metadata) != 1 || get.Metadata[0].Name != "tenant" || get.Metadata[0].Param != "tenant" {
		t.Errorf("metadata: %+v", get.Metadata)
	}
	if f := get.Response.Fields[0]; get.Response.Name != "GetParcelResponse" || f.Name != "parcel" || f.Number != 1 || f.Message == nil || f.Message.Name != "Parcel" {
		t.Errorf("response: %+v", get.Response.Fields[0])
	}
	if len(get.Errors) != 1 || get.Errors[0].GRPC != "NOT_FOUND" || get.Errors[0].JSONRPC != "-32004" || get.Errors[0].Status != "404" || get.Errors[0].Message != "No parcel has this id." {
		t.Errorf("errors: %+v", get.Errors)
	}
	if d := get.Errors[0].Detail; d == nil || d.FullName() != "acme.parcels.v1.ParcelNotFound" || len(d.Fields) != 1 || d.Fields[0].Name != "id" {
		t.Errorf("the error's detail message: %+v", d)
	}
	watch := method(t, m, "WatchParcel")
	if watch.Timeout != "10m" || watch.Response.Fields[0].Name != "parcel_event" || !watch.Response.Fields[0].Stream {
		t.Errorf("WatchParcel: %q %+v", watch.Timeout, watch.Response.Fields[0])
	}
	up := method(t, m, "UploadScans")
	if f := up.Request.Fields[0]; f.Name != "scans" || f.Label == "repeated" || !f.Stream || f.Message.Name != "Scan" {
		t.Errorf("a client stream's element: %+v", f)
	}
	if !strings.Contains(method(t, m, "Navigate").Signature(), "rpc Navigate(stream NavigateRequest) returns (stream NavigateResponse)") {
		t.Errorf("signature: %s", method(t, m, "Navigate").Signature())
	}
	if rpc.ConnectCode("CANCELLED") != "canceled" || rpc.ConnectCode("NOT_FOUND") != "not_found" || rpc.ConnectStatus("NOT_FOUND") != 404 || rpc.ConnectStatus("UNAVAILABLE") != 503 || rpc.ConnectStatus("DATA_LOSS") != 500 {
		t.Error("Connect's codes and statuses")
	}
}

// Messages: explicit and declaration-order numbers, reserved numbers,
// scalar and well-known types, maps, enums with their zero value, unions
// as a oneof, and JSON names.
func TestMessages(t *testing.T) {
	m, _ := parcels(t)
	p := message(t, m, "acme.parcels.v1", "Parcel")
	if f := field(t, p, "destination"); f.Number != 5 || f.Label != "optional" || f.Message.Name != "Destination" {
		t.Errorf("destination: %+v", f)
	}
	if f := field(t, p, "weight_grams"); f.Type != "int32" || f.JSON != "weightGrams" {
		t.Errorf("weight_grams: %+v", f)
	}
	if f := field(t, p, "labels"); f.Type != "map<string, string>" || f.Number != 6 {
		t.Errorf("labels: %+v", f)
	}
	if len(p.Reserved) != 1 || p.Reserved[0] != 4 {
		t.Errorf("reserved: %v", p.Reserved)
	}
	ev := message(t, m, "acme.parcels.v1", "ParcelEvent")
	if f := field(t, ev, "at"); f.Number != 3 || f.Type != "google.protobuf.Timestamp" {
		t.Errorf("at: %+v", f)
	}
	if f := field(t, message(t, m, "acme.parcels.v1", "ScanSummary"), "accepted"); f.Type != "int64" {
		t.Errorf("Int is int64: %+v", f)
	}
	d := message(t, m, "acme.parcels.v1", "Destination")
	if d.Oneof != "destination" || len(d.Fields) != 2 || d.Fields[1].Name != "locker" || d.Fields[1].Number != 2 {
		t.Errorf("a union: %+v", d)
	}
	e := m.EnumOf(p.Fields[1].Enum.Node)
	if e.Values[0].Name != "PARCEL_STATUS_UNSPECIFIED" || e.Values[0].Number != 0 || e.Values[2].Name != "PARCEL_STATUS_IN_TRANSIT" || e.Values[2].Number != 2 || e.Values[2].Variant != "IN_TRANSIT" {
		t.Errorf("enum: %+v", e.Values)
	}
	if m.MessageOf(p.Node) != p {
		t.Error("MessageOf")
	}
	pkg := m.Packages["acme.parcels.v1"]
	if strings.Join(pkg.Imports(), ",") != "google/protobuf/timestamp.proto" {
		t.Errorf("imports: %v", pkg.Imports())
	}
}

const head = "import = [\"std/types@1\", \"std/proto@1\"]\n"

// More of the projection: generic instances, the well-known types, the
// integer encodings, a message name a type already has, and the service
// and method names authors set.
func TestProjectionRules(t *testing.T) {
	m, res := build(t, map[string]string{"index.heron": head + `
@proto("acme.shop.v1", service = "Shop")
shop: api {
  @proto(method = "Find")
  list(cursor?: String): op(Page(Item))
  get(id: String, @metadata("x-key") key: Bytes): op(GetRequest)
  stats(): op(Map(String, Int))
  ping(): op
}

GetRequest: type { id: String }

Page(T: Any): type {
  items: List(T)
  next?: String
}

Item: type {
  @field<sint>(1)
  delta: Int
  @field<fixed>(2)
  count: u32
  @field(3)
  data: Any
  @field(4)
  attrs: Map(String, Any)
  @field(5)
  took: Duration
  @field(6)
  note: String | Null
}
`})
	if res.Diags.HasErrors() {
		t.Fatalf("%s", diags(res))
	}
	s := m.Services[0]
	if s.Name != "Shop" || s.Methods[0].Name != "Find" {
		t.Errorf("names: %s %s", s.Name, s.Methods[0].Name)
	}
	find := s.Methods[0]
	if f := find.Response.Fields[0]; f.Name != "page_item" || f.Message.Name != "PageItem" {
		t.Errorf("a generic instance: %+v", f)
	}
	get := s.Methods[1]
	if get.Request.Name != "ShopGetRequest" || get.Response.Name != "GetResponse" {
		t.Errorf("a name a type has: %s %s", get.Request.Name, get.Response.Name)
	}
	if get.Metadata[0].Name != "x-key-bin" || !get.Metadata[0].Binary {
		t.Errorf("binary metadata: %+v", get.Metadata)
	}
	if f := s.Methods[2].Response.Fields[0]; f.Name != "entries" || f.Type != "map<string, int64>" {
		t.Errorf("a map result: %+v", f)
	}
	if len(s.Methods[3].Response.Fields) != 0 || len(s.Methods[3].Request.Fields) != 0 {
		t.Error("an op with no result answers with an empty message")
	}
	item := message(t, m, "acme.shop.v1", "Item")
	for name, want := range map[string]string{"delta": "sint64", "count": "fixed32", "data": "google.protobuf.Value", "attrs": "google.protobuf.Struct", "took": "google.protobuf.Duration", "note": "google.protobuf.StringValue"} {
		if f := field(t, item, name); f.Type != want {
			t.Errorf("%s: %s, want %s", name, f.Type, want)
		}
	}
}

// What the checker reports about gRPC bindings.
func TestProtoDiagnostics(t *testing.T) {
	for src, want := range map[string]string{
		"x: api {\n  @proto(method = \"X\")\n  a(): op\n}\n":                                                                    "a carries @proto, but its api names no protobuf package",
		"@proto(\"a.v1\", method = \"X\")\nx: api {\n  a(): op\n}\n":                                                            "@proto on api doesn't take method",
		"@proto(\"a.v1\")\nx: api {\n  @proto(service = \"S\")\n  a(): op\n}\n":                                                 "@proto on op doesn't take service",
		"@proto(\"Acme-Parcels\")\nx: api {\n  a(): op\n}\n":                                                                    "Acme-Parcels isn't a protobuf package",
		"@proto(\"a.v1\")\nx: api {\n  a(s: Stream(String), b: String): op\n}\n":                                                "a streams from the client, so b can only travel as @metadata",
		"@proto(\"a.v1\")\nx: api {\n  a(t: T): op\n}\nT: type {\n  @field(1)\n  a: String\n  b: String\n}\n":                   "T numbers some fields with @field and not b",
		"@proto(\"a.v1\")\nx: api {\n  a(t: T): op\n}\nT: type {\n  @field(1)\n  a: String\n  @field(1)\n  b: String\n}\n":      "b has number 1, which a already has in T",
		"@proto(\"a.v1\")\nx: api {\n  a(t: T): op\n}\nT: type {\n  @field(19000)\n  a: String\n}\n":                            "19000 can't number a",
		"@proto(\"a.v1\")\nx: api {\n  a(t: T): op\n}\n@reserved(2)\nT: type {\n  @field(2)\n  a: String\n}\n":                  "a has number 2, which T reserves",
		"@proto(\"a.v1\")\nx: api {\n  a(t: T): op\n}\nE: enum {\n  A: Self\n  @field(1)\n  B: Self\n}\nT: type {\n  e: E\n}\n": "E numbers some fields with @field and not A",
		"@proto(\"a.v1\")\nx: api {\n  a(t: List(List(String))): op\n}\n":                                                       "has no protobuf form: a list of lists or maps",
		"@proto(\"a.v1\")\nx: api {\n  a(t: Map(Float, String)): op\n}\n":                                                       "a map keyed by anything but a string",
		"@proto(\"a.v1\")\nx: api {\n  a(t: String | Int): op\n}\n":                                                             "a union expression other than T | Null",
		"@proto(\"a.v1\")\nx: api {\n  a(u: U): op\n}\nU: union {\n  l: List(String)\n}\n":                                      "a list or map can't be a variant of a union",
	} {
		_, res := build(t, map[string]string{"index.heron": head + src})
		if got := diags(res); !strings.Contains(got, want) {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}
}

const jhead = "import = [\"std/types@1\", \"std/jsonrpc@1\"]\n"

// JSON-RPC methods: names, params by name, by position and whole,
// notifications, null results, framing and encoding.
func TestJSONRPC(t *testing.T) {
	m, res := build(t, map[string]string{"index.heron": jhead + `
@jsonrpc(framing = content_length, batch = true)
ls: api {
  @jsonrpc("textDocument/hover")
  hover(@params p: P): op(String)
  shutdown(): op(Null)
  @jsonrpc("exit")
  exit(): op
  @jsonrpc(params = by_position)
  move(a: Int, b: Int): op(Int)
}

other: api {
  @jsonrpc
  lone(x: String): op(String)
  plain(): op
}

P: type { uri: String }
`})
	if res.Diags.HasErrors() {
		t.Fatalf("%s", diags(res))
	}
	if len(m.JSONAPIs) != 2 {
		t.Fatalf("apis: %d", len(m.JSONAPIs))
	}
	ls := m.JSONAPIs[0]
	if ls.Framing != "content_length" || ls.Encoding != "json" || !ls.Batch {
		t.Errorf("api: %+v", ls)
	}
	hover, shutdown, exit, move := ls.Methods[0], ls.Methods[1], ls.Methods[2], ls.Methods[3]
	if hover.Name != "textDocument/hover" || hover.Whole == nil || hover.Notification {
		t.Errorf("hover: %+v", hover)
	}
	if shutdown.Name != "shutdown" || !shutdown.NullResult || shutdown.Notification {
		t.Errorf("shutdown: %+v", shutdown)
	}
	if !exit.Notification {
		t.Error("an op with no result is a notification")
	}
	if !move.ByPosition || len(move.Params) != 2 {
		t.Errorf("move: %+v", move)
	}
	other := m.JSONAPIs[1]
	if len(other.Methods) != 1 || other.Methods[0].Name != "lone" || other.Framing != "http" {
		t.Errorf("an op bound alone: %+v", other)
	}
}

// What the checker reports about JSON-RPC bindings.
func TestJSONRPCDiagnostics(t *testing.T) {
	for src, want := range map[string]string{
		"@jsonrpc\nx: api {\n  a(): op(String)\n  @jsonrpc(\"a\")\n  b(): op(String)\n}\n":         "b is the JSON-RPC method a, which a already is",
		"@jsonrpc(\"m\")\nx: api {\n  a(): op\n}\n":                                                "@jsonrpc on api doesn't take method",
		"x: api {\n  @jsonrpc(batch = true)\n  a(): op\n}\n":                                       "@jsonrpc on op doesn't take batch",
		"@jsonrpc\nx: api {\n  a(): op(Stream(String))\n}\n":                                       "a streams, and a JSON-RPC method answers once",
		"@jsonrpc\nx: api {\n  @throws(E)\n  a(): op\n}\nE: type(Error) {\n  message: String\n}\n": "a is a notification, which is never answered, so it can't throw E",
		"@jsonrpc\nx: api {\n  a(@params p: P, q: String): op\n}\nP: type { u: String }\n":         "@params p must be the only parameter of a, of a record type",
		"@jsonrpc\nx: api {\n  a(@params p: String): op\n}\n":                                      "@params p must be the only parameter of a",
	} {
		_, res := build(t, map[string]string{"index.heron": jhead + src})
		if got := diags(res); !strings.Contains(got, want) {
			t.Errorf("%q: got %q, want %q", src, got, want)
		}
	}
}

// What the linter reports: errors without a code for their binding and
// JSON-RPC codes in the reserved range.
func TestCodeLints(t *testing.T) {
	m, res := build(t, map[string]string{"index.heron": "import = [\"std/types@1\", \"std/proto@1\", \"std/jsonrpc@1\"]\n" + `
@proto("a.v1")
x: api {
  @jsonrpc
  @throws(Bare, Reserved, Server)
  a(): op(String)
}

Bare: type(Error) { message: String }

@jsonrpc.code(-32500)
@proto.status(INTERNAL)
Reserved: type(Error) { message: String }

@jsonrpc.code(-32050)
@proto.status(INTERNAL)
Server: type(Error) { message: String }
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
		"Bare is thrown by a, a gRPC method, and has neither @status nor @proto.status: it's answered as UNKNOWN",
		"Bare is thrown by a, a JSON-RPC method, and has neither @status nor @code",
		"-32500 is in JSON-RPC's reserved range",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
	if strings.Contains(all, "-32050") || len(got) != 3 {
		t.Errorf("a server code is allowed, and nothing else is reported:\n%s", all)
	}
}

// An unqualified annotation two imported packages declare is ambiguous,
// but one the core declares is the core's: `@status` is always the
// error's HTTP status, and std/proto's is written @proto.status.
func TestAmbiguousCode(t *testing.T) {
	_, res := build(t, map[string]string{"index.heron": "import = [\"std/types@1\", \"std/proto@1\", \"std/sparrow@1\"]\n@reserved(2)\nE: type(Error) { message: String }\n"})
	if got := diags(res); !strings.Contains(got, "@reserved is declared by more than one imported package: write @proto.reserved or @sparrow.reserved") {
		t.Errorf("got %q", got)
	}
	_, res = build(t, map[string]string{"index.heron": "import = [\"std/types@1\", \"std/proto@1\", \"std/sparrow@1\"]\n@status(404)\n@proto.status(INVALID_ARGUMENT)\nE: type(Error) { message: String }\n"})
	if res.Diags.HasErrors() {
		t.Errorf("the core's @status beside @proto.status: %s", diags(res))
	}
	_, res = build(t, map[string]string{"index.heron": head + "@proto.status(NOT_FOUND)\nE: type(Error) { message: String }\n"})
	if res.Diags.HasErrors() {
		t.Errorf("one package: %s", diags(res))
	}
}

// The codes an error's @status derives on each surface, by the tables of
// the Specifications tab, and the override of a surface in their place.
func TestDerivedCodes(t *testing.T) {
	for _, c := range []struct {
		status, grpc, sparrow string
		jsonrpc, exit         int
	}{
		{"400", "INVALID_ARGUMENT", "invalid_argument", -32000, 2},
		{"401", "UNAUTHENTICATED", "unauthenticated", -32001, 1},
		{"403", "PERMISSION_DENIED", "permission_denied", -32003, 1},
		{"404", "NOT_FOUND", "not_found", -32004, 1},
		{"409", "ALREADY_EXISTS", "already_exists", -32009, 1},
		{"422", "FAILED_PRECONDITION", "failed_precondition", -32022, 1},
		{"429", "RESOURCE_EXHAUSTED", "resource_exhausted", -32029, 1},
		{"499", "CANCELLED", "cancelled", -32099, 1},
		{"500", "INTERNAL", "internal", -32603, 1},
		{"501", "UNIMPLEMENTED", "unimplemented", -32601, 1},
		{"502", "INTERNAL", "internal", -32603, 1},
		{"503", "UNAVAILABLE", "unavailable", -32603, 1},
		{"504", "DEADLINE_EXCEEDED", "deadline_exceeded", -32603, 1},
		{"302", "UNKNOWN", "unknown", -32603, 1},
	} {
		if g := rpc.GRPCCode(c.status); g != c.grpc {
			t.Errorf("%s: gRPC %s, want %s", c.status, g, c.grpc)
		}
		if g := rpc.SparrowCodeName(c.status); g != c.sparrow {
			t.Errorf("%s: Sparrow %s, want %s", c.status, g, c.sparrow)
		}
		if g, _ := rpc.JSONRPCCode(c.status); g != c.jsonrpc {
			t.Errorf("%s: JSON-RPC %d, want %d", c.status, g, c.jsonrpc)
		}
		if g, _ := rpc.ExitCode(c.status); g != c.exit {
			t.Errorf("%s: exit %d, want %d", c.status, g, c.exit)
		}
	}
	if rpc.GRPCCode("") != "" {
		t.Error("no status derives no code")
	}
	m, res := build(t, map[string]string{"index.heron": "import = [\"std/types@1\", \"std/proto@1\", \"std/jsonrpc@1\"]\n@proto(\"a.v1\")\nx: api {\n  @jsonrpc\n  @throws(Gone, Odd)\n  a(): op(String)\n}\n@status(404)\nGone: type(Error) { message: String }\n@status(422)\n@proto.status(INVALID_ARGUMENT)\n@jsonrpc.code(-32602)\nOdd: type(Error) { message: String }\n"})
	if res.Diags.HasErrors() {
		t.Fatal(diags(res))
	}
	var errs []rpc.Error
	if mm := m.Method(res.Tree.Find("x.a")); mm != nil {
		errs = mm.Errors
	}
	if len(errs) != 2 {
		t.Fatalf("errors %v", errs)
	}
	if e := errs[0]; e.GRPC != "NOT_FOUND" || e.GRPCExplicit || e.JSONRPC != "-32004" || e.JSONRPCExplicit {
		t.Errorf("derived: %+v", e)
	}
	if e := errs[1]; e.GRPC != "INVALID_ARGUMENT" || !e.GRPCExplicit || e.JSONRPC != "-32602" || !e.JSONRPCExplicit {
		t.Errorf("explicit: %+v", e)
	}
}

// The .proto files: one per package, beside a buf.yaml, the service with
// its streams and idempotency levels, then the messages and enums; buf
// builds and lints them when it's installed.
func TestProtoFiles(t *testing.T) {
	m, _ := parcels(t)
	files := m.ProtoFiles()
	if len(files) != 2 || files[0].Path != "acme/parcels/v1/parcels.proto" || files[1].Path != "buf.yaml" {
		t.Fatalf("files: %v", files)
	}
	if rpc.FilePath("acme.payments") != "acme/payments/payments.proto" {
		t.Error(rpc.FilePath("acme.payments"))
	}
	src := string(files[0].Content)
	for _, want := range []string{
		"syntax = \"proto3\";\n\npackage acme.parcels.v1;\n\nimport \"google/protobuf/timestamp.proto\";",
		"// Tracks parcels from the label to the doorstep.\nservice ParcelsService {",
		"  rpc GetParcel(GetParcelRequest) returns (GetParcelResponse) {\n    option idempotency_level = NO_SIDE_EFFECTS;\n  }",
		"  rpc WatchParcel(WatchParcelRequest) returns (stream WatchParcelResponse);",
		"  rpc UploadScans(stream UploadScansRequest) returns (UploadScansResponse);",
		"message Parcel {\n  reserved 4;\n  string id = 1;",
		"  optional Destination destination = 5;",
		"message Destination {\n  oneof destination {\n    Address address = 1;\n    Locker locker = 2;\n  }\n}",
		"enum ParcelStatus {\n  PARCEL_STATUS_UNSPECIFIED = 0;\n  PARCEL_STATUS_LABELLED = 1;",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing %q in\n%s", want, src)
		}
	}
	buf, err := exec.LookPath("buf")
	if err != nil {
		t.Log("buf isn't installed: the files aren't built or linted")
		return
	}
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, f.Path)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, f.Content, 0o644)
	}
	for _, args := range [][]string{{"build"}, {"lint"}} {
		cmd := exec.Command(buf, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("buf %s: %v\n%s", args[0], err, out)
		}
	}
}

// An example's stream is a list of the stream's messages, in order, and
// every op has a streaming kind, whatever binds it.
func TestStreamExamples(t *testing.T) {
	src := "import = [\"std/types@1\"]\nP: type { x: Int }\nsvc: api {\n  nav(ps: Stream(P)): op(Stream(P)) {\n    ok: example {\n      ps = [P(x = 1), P(x = 2)]\n      returns = [P(x = 3)]\n    }\n    bad: example {\n      ps = P(x = 1)\n    }\n    worse: example {\n      ps = [\"x\"]\n    }\n  }\n  one(p: P): op(P)\n}\n"
	_, res := build(t, map[string]string{"index.heron": src})
	got := diags(res)
	if strings.Count(got, "expected a list of the stream's messages") != 1 || !strings.Contains(got, "expected P, got a string") {
		t.Errorf("got %q", got)
	}
	var nav, one *resolve.Node
	res.Tree.Walk(func(n *resolve.Node) {
		switch n.Name {
		case "nav":
			nav = n
		case "one":
			one = n
		}
	})
	if rpc.StreamKind(res.Checker, nav) != rpc.BidiStreaming || rpc.StreamKind(res.Checker, one) != rpc.Unary {
		t.Error("streaming kinds")
	}
}
