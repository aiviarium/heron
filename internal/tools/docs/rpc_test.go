package docs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/gen"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

const parcelsEntry = "../../../examples/parcels/index.heron"

func parcelPages(t *testing.T) map[string]string {
	t.Helper()
	_, f := lensFiles(t, parcelsEntry)
	return f
}

// An op bound to HTTP, gRPC, Connect and JSON-RPC is one page: a switch,
// each binding's header, messages and errors, and its examples per binding.
func TestBindingSwitch(t *testing.T) {
	f := parcelPages(t)
	h := f["parcels.get_parcel.html"]
	mustHave(t, "get_parcel", h,
		`data-island="bindings"`,
		`data-binding-tab="http">HTTP</button>`, `data-binding-tab="grpc">gRPC</button>`, `data-binding-tab="connect">Connect</button>`, `data-binding-tab="jsonrpc">JSON-RPC</button>`,
		`<div class="binding" data-binding="http">`, `<div class="binding" data-binding="grpc" hidden>`,
		// gRPC: the path, the signature with its messages linked, the kind.
		`<code class="path">/acme.parcels.v1.ParcelsService/GetParcel</code>`,
		`<span class="tk-kw">rpc</span> <span class="tk-name">GetParcel</span>`,
		`href="#parcels.get_parcel.grpc.request"`, `<span class="rpc-kind kind-unary"`,
		// The messages, by field number, with JSON names on gRPC.
		`<span class="fnum" title="Field number">1</span> <code class="fname">id</code>`,
		`<code class="fname">parcel</code> <code class="t"><a class="ref code-ref ref-type" href="Parcel.html"`,
		// Metadata from the HTTP header, and as a header over Connect.
		`<code class="fname">tenant</code>`, `<code class="fname">Tenant</code>`,
		// Errors keyed by each binding's code.
		`<code class="status err">NOT_FOUND</code>`, `<code class="status err">not_found</code> <code class="status-code">404</code>`, `<code class="status err">-32004</code>`,
		// The facts a method adds, once.
		`<dt>Idempotency</dt><dd><code>NO_SIDE_EFFECTS</code></dd>`,
		// The JSON-RPC header.
		`<span class="method m-json-rpc">JSON-RPC</span><code class="path">parcels.get</code>`,
	)
	if strings.Count(h, `<dl class="facts">`) != 1 || strings.Count(h, `class="binding-switch"`) != 1 {
		t.Error("the contract's prose and facts are shown once")
	}
	// The examples per binding: calls in each protocol's forms.
	mustHave(t, "get_parcel examples", h,
		`data-binding="grpc" hidden>`, `data-lang="grpcurl"`, `data-lang="bufcurl"`,
		`<span class="tk-kw">grpcurl</span>`, `parcels.example.com:443 acme.parcels.v1.ParcelsService/GetParcel`,
		`-H &#39;tenant: acme&#39;`,
		`buf</span> curl --protocol grpc --schema .heron/proto`,
		`curl</span> --get &#34;https://parcels.example.com/acme.parcels.v1.ParcelsService/GetParcel&#34;`, // GET for no side effects
		`createConnectTransport`, `client.getParcel(`,
		`&#34;method&#34;`, `parcels.get`, `&#34;jsonrpc&#34;`, // the envelope
		`<code class="status err">NOT_FOUND</code></div>`, `type.googleapis.com/acme.parcels.v1.ParcelNotFound`, // a thrown error as a status
		`&#34;not_found&#34;`,                           // Connect's error body
		`https://parcels.example.com/v1/parcels/p_1042`, // the server URL in place of $BASE_URL
	)
	// Try it: Connect and JSON-RPC over HTTP, never gRPC.
	data := regexp.MustCompile(`<script type="application/json" class="tryit-data">(.*?)</script>`).FindAllStringSubmatch(h, -1)
	protocols := map[string]bool{}
	for _, d := range data {
		var v map[string]any
		json.Unmarshal([]byte(d[1]), &v)
		if p, ok := v["protocol"].(string); ok {
			protocols[p] = true
			if v["server"] != "https://parcels.example.com" {
				t.Errorf("Try it's server: %v", v["server"])
			}
		}
	}
	if !protocols["connect"] || !protocols["jsonrpc"] || protocols["grpc"] {
		t.Errorf("Try it for %v", protocols)
	}
}

// A streaming method shows its messages by direction and an example as
// the ordered messages each side sends; Try it only where a browser can.
func TestStreamingMethods(t *testing.T) {
	f := parcelPages(t)
	nav := f["parcels.navigate.html"]
	mustHave(t, "navigate", nav,
		`<span class="tk-kw">stream</span> <a href="#parcels.navigate.grpc.request"`,
		`<span class="rpc-kind kind-bidi"`, `Bidirectional streaming`,
		`<span class="label">Client to server</span> <code>stream NavigateRequest</code>`,
		`<ol class="rpc-seq">`, `Turn right onto Querstraße`, `The locker is on your left`,
		`-d @ parcels.example.com:443`, `createGrpcTransport`, `async function* requests()`,
		`<code class="status err">UNAVAILABLE</code>`,
		`A browser can&#39;t stream from the client`,
	)
	mustNotHave(t, "navigate", nav, `data-tryit`, `data-binding="http"`)
	watch := f["parcels.watch_parcel.html"]
	mustHave(t, "watch_parcel", watch, `<span class="rpc-kind kind-server"`, `<dt>Time limit</dt><dd><code>10m</code></dd>`,
		`application/connect+json`, `printf &#39;\x00\x00\x00\x00\x0f%s&#39;`, `client.watchParcel(`, `"stream":true`)
}

// The service page: its name, protocols and servers, and its methods by
// streaming kind, each with its signature; the navigation lists them with
// their kinds, and an HTTP index only the ops with a route.
func TestServicePage(t *testing.T) {
	f := parcelPages(t)
	h := f["parcels.html"]
	mustHave(t, "parcels", h,
		`<dt>Service</dt><dd><code class="rpc-service">acme.parcels.v1.ParcelsService</code>`,
		`<span class="rpc-protocol">gRPC</span> <span class="rpc-protocol">gRPC-Web</span> <span class="rpc-protocol">Connect</span>`,
		`<dt>Servers</dt><dd><code>https://parcels.example.com</code></dd>`,
		`<p class="rpc-group-head">Unary</p>`, `<p class="rpc-group-head">Server streaming</p>`, `<p class="rpc-group-head">Client streaming</p>`, `<p class="rpc-group-head">Bidirectional streaming</p>`,
		`Navigate(<span class="tk-kw">stream</span> NavigateRequest) <span class="tk-kw">returns</span> (<span class="tk-kw">stream</span> NavigateResponse)`,
		`<p class="rpc-group-head">parcels</p>`, // the JSON-RPC index, by namespace
	)
	idx := regexp.MustCompile(`(?s)<div class="binding" data-binding="http".*?</table>`).FindString(h)
	mustHave(t, "the HTTP index", idx, `/parcels/:id`)
	mustNotHave(t, "the HTTP index", idx, `Upload scans`, `Navigate`)
	nav := regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`).FindString(h)
	mustHave(t, "nav", nav, `<span class="m m-bidi">BIDI</span>`, `<span class="m m-clt">CLT</span>`, `<span class="m m-get">GET</span>`)
}

// A message's type page shows its .proto definition and its JSON form
// beside its declaration.
func TestMessageTypePage(t *testing.T) {
	f := parcelPages(t)
	h := f["Parcel.html"]
	mustHave(t, "Parcel", h,
		`<div class="type-forms" data-island="tabs">`, `>Protobuf</button>`, `>JSON</button>`,
		`acme/parcels/v1/parcels.proto`,
		`<span class="tk-kw">reserved</span> <span class="tk-num">4</span>`,
		`<span class="tk-kw">optional</span> <span class="tk-type">Destination</span> destination <span class="tk-punct">=</span> <span class="tk-num">5</span>`,
		`&#34;weightGrams&#34;`,
	)
	mustHave(t, "an enum", f["ParcelStatus.html"], `PARCEL_STATUS_UNSPECIFIED`, `&#34;PARCEL_STATUS_LABELLED&#34;`)
	mustNotHave(t, "a type no service reaches", f["Locker.html"]+f["Address.html"], "never")
}

// Each api's types follow its ops in the navigation, so an HTTP api's and
// an RPC service's never share a group.
func TestTypesByAPI(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.heron"), []byte(`import = ["std/types@1", "std/http@1", "std/jsonrpc@1"]

@http(base_path = "/api")
web: api {
  @http<get>("/a")
  a(): op(A)
}

@jsonrpc(framing = length_prefixed, encoding = cbor)
wire: api {
  call(@params p: P): op(R)
}

A: type { x: String }
P: type { y: String }
R: type { z: String }
`), 0o644)
	_, f := lensFiles(t, filepath.Join(dir, "index.heron"))
	nav := regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`).FindString(f["web.a.html"])
	webTypes := strings.Index(nav, `Types of Web`)
	wireTypes := strings.Index(nav, `Types of Wire`)
	if webTypes < 0 || wireTypes < 0 || !(strings.Index(nav, `web.a.html`) < webTypes && webTypes < strings.Index(nav, `wire.call.html`) && strings.Index(nav, `wire.call.html`) < wireTypes) {
		t.Errorf("each api's types follow its ops:\n%s", nav)
	}
	if !(strings.Index(nav, `A.html`) < strings.Index(nav, `wire.call.html`) && strings.Index(nav, `P.html`) > wireTypes) {
		t.Errorf("types under the api that names them:\n%s", nav)
	}
	mustHave(t, "wire", f["wire.html"], `length-prefixed frames, CBOR, without the jsonrpc member`)
	mustHave(t, "call", f["wire.call.html"], `one record as the whole of params`, `data-lang="raw"`, `length <span class="tk-num">`)
}

// heron generate proto and openrpc write the description each tool reads,
// on request alone.
func TestRPCGenerators(t *testing.T) {
	id, _ := resolve.Canonical(parcelsEntry)
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatal(res.Diags)
	}
	r := registry.New()
	Register(r)
	dir := t.TempDir()
	if _, err := gen.Run(context.Background(), r, &gen.Input{Tree: res.Tree, Checker: res.Checker, Dir: dir}, []string{"proto", "openrpc"}); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(dir, ".heron/proto/acme/parcels/v1/parcels.proto"))
	if err != nil || !strings.Contains(string(src), "service ParcelsService {") {
		t.Fatalf("proto: %v\n%s", err, src)
	}
	if _, err := os.Stat(filepath.Join(dir, ".heron/proto/buf.yaml")); err != nil {
		t.Error("no buf.yaml")
	}
	b, err := os.ReadFile(filepath.Join(dir, ".heron/openrpc/parcels.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OpenRPC string `json:"openrpc"`
		Servers []struct{ URL string }
		Methods []struct {
			Name           string
			ParamStructure string
			Params         []struct {
				Name     string
				Required bool
			}
			Result *struct{ Name string }
			Errors []struct {
				Code    int
				Message string
			}
			Examples []struct{ Name string }
		}
		Components struct {
			Schemas map[string]any
		}
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OpenRPC != "1.3.2" || len(doc.Servers) != 1 || len(doc.Methods) != 1 {
		t.Fatalf("doc: %+v", doc)
	}
	me := doc.Methods[0]
	if me.Name != "parcels.get" || me.ParamStructure != "by-name" || len(me.Params) != 2 || !me.Params[0].Required || me.Params[1].Required || me.Result == nil {
		t.Errorf("method: %+v", me)
	}
	if len(me.Errors) != 1 || me.Errors[0].Code != -32004 || len(me.Examples) != 1 {
		t.Errorf("errors and examples: %+v", me)
	}
	if doc.Components.Schemas["Parcel"] == nil {
		t.Error("no Parcel schema")
	}
	// Run with no targets, neither is written.
	dir2 := t.TempDir()
	gen.Run(context.Background(), r, &gen.Input{Tree: res.Tree, Checker: res.Checker, Dir: dir2}, nil)
	if _, err := os.Stat(filepath.Join(dir2, ".heron/proto")); err == nil {
		t.Error("proto ran without being asked for")
	}
}

// An api's @keys(camel) names its JSON-RPC params and their fields in lower
// camel case, in the docs and in the OpenRPC document.
func TestJSONRPCNames(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.heron"), []byte(`import = ["std/types@1", "std/jsonrpc@1", "std/json@1"]

@jsonrpc(framing = content_length)
@keys(camel)
ls: api {
  @jsonrpc("textDocument/hover")
  hover(@params p: HoverParams): op(Hover) {
    over_a_name: example {
      p = HoverParams(text_document = "file:///a.heron", line_number = 3)
      returns = Hover(contents = "A name")
    }
  }
  @jsonrpc("textDocument/didClose")
  did_close(text_document: String): op
}

HoverParams: type {
  text_document: String
  @key("line")
  line_number: Int
}

Hover: type { contents: String }
`), 0o644)
	_, f := lensFiles(t, filepath.Join(dir, "index.heron"))
	h := f["ls.hover.html"]
	mustHave(t, "hover", h, `<code class="fname">textDocument</code>`, `&#34;textDocument&#34;`, `&#34;line&#34;`, `Content-Length</span>`)
	mustNotHave(t, "hover", h, `&#34;text_document&#34;`)
	mustHave(t, "did_close", f["ls.did_close.html"], `<code class="fname">textDocument</code>`, `Notification`)
	id, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	res := check.NewSession().Check(id)
	doc := string(OpenRPC(res.Tree, res.Checker)["ls.json"])
	for _, want := range []string{`"name": "textDocument"`, `"name": "line"`, `"value": "file:///a.heron"`, `"contents"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("OpenRPC lacks %s:\n%s", want, doc)
		}
	}
}
