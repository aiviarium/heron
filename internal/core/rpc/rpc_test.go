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

const jhead = "import = [\"std/types@1\", \"std/jsonrpc@1\", \"std/spec@1\"]\n"

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
@proto.code(INTERNAL)
Reserved: type(Error) { message: String }

@jsonrpc.code(-32050)
@proto.code(INTERNAL)
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
		"Bare is thrown by a, a gRPC method, and has no @code: it's answered as UNKNOWN",
		"Bare is thrown by a, a JSON-RPC method, and has no @code",
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

// An unqualified annotation two imported packages declare is ambiguous.
func TestAmbiguousCode(t *testing.T) {
	_, res := build(t, map[string]string{"index.heron": "import = [\"std/types@1\", \"std/proto@1\", \"std/jsonrpc@1\"]\n@code(NOT_FOUND)\nE: type(Error) { message: String }\n"})
	if got := diags(res); !strings.Contains(got, "@code is declared by more than one imported package: write @jsonrpc.code or @proto.code") {
		t.Errorf("got %q", got)
	}
	_, res = build(t, map[string]string{"index.heron": head + "@code(NOT_FOUND)\nE: type(Error) { message: String }\n"})
	if res.Diags.HasErrors() {
		t.Errorf("one package: %s", diags(res))
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
