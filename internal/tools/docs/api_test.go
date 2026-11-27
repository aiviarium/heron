package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// opSrc is an api with every part an op page shows: parameters in each
// location, a cross-cutting annotation, errors from the op, its api and
// an interface's implementors, and streams.
const opSrc = `import = ["std/schema@1", "std/types@1", "std/http@1", "std/ws@1"]

limits: package(version = "1.0") {
  rate_limit(per_minute: Int): annotation(on = [types.api, types.op])
  idempotent: annotation(on = [types.op])
}

Refund: type { id: String }
Event: type { id: String }

StoreError: interface(Error)

@status(404)
NotFound: type(StoreError) { message: String }

@status(409)
Conflict: type(StoreError) { message: String }

@status(422)
Invalid: type(Error) { message: String }

@http(base_path = "/v1")
@limits.rate_limit(per_minute = 60)
@throws(Invalid)
refunds: api {
  @http<post>("/refunds/:id")
  @limits.idempotent
  @throws(StoreError)
  create(@path id: String, @query dry?: Bool, @header("Idempotency-Key") key: String, @body refund: Refund): op(Refund)

  @http<get>("/refunds/:id/events")
  watch(@path id: String): op(Stream(Event))

  @http<put>("/refunds/:id/note")
  note(@path id: String, body: String): op(Refund)

  @ws("/chat")
  chat(messages: Stream(Event)): op(Stream(Event))

  @http<post>("/ping")
  ping(): op
}

@ws("/live")
live: api {
  @ws("/live/talk")
  talk(messages: Stream(Event)): op(Stream(Event))
  hear(): op(Stream(Event))
}
`

func TestOpPageParts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte(opSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s := site(t, filepath.Join(dir, "index.heron"))
	page := func(file string) string {
		t.Helper()
		p := s.PageByFile(file)
		if p == nil {
			t.Fatalf("no page %s", file)
		}
		h, err := s.RenderPage(p)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	for file, wants := range map[string][]string{
		"refunds.create.html": {
			`<span class="base">/v1</span>/refunds/:id`, // the api's base path before the op's
			"Path parameters", "Query parameters", "Headers", "Body", // a group per location
			"Idempotency-Key",          // a header by its name
			"Rate limit", "per_minute", // the api's cross-cutting annotation
			"Idempotent", "@idempotent", // the op's, with no arguments
			`<code class="status err">404</code>`, // an interface's implementors, by status
			`<code class="status err">409</code>`,
			`<code class="status err">422</code>`, // the api's @throws
			`<code class="status ok">200</code>`,  // the success response
		},
		"refunds.note.html":  {"Parameters"}, // a parameter named body is just a parameter
		"refunds.watch.html": {"Messages", "Server to client", "Server-sent events"},
		"refunds.chat.html":  {"Client to server", "Server to client", "WebSocket messages"},
	} {
		h := page(file)
		for _, want := range wants {
			if !strings.Contains(h, want) {
				t.Errorf("%s lacks %q", file, want)
			}
		}
	}
	if h := page("refunds.ping.html"); strings.Contains(h, `class="status ok"`) {
		t.Error("an op with no result type shows a success response")
	}
	if h := page("live.talk.html"); !strings.Contains(h, `<code class="path">/live/talk</code>`) {
		t.Error("an op's own socket doesn't replace its api's")
	}
	if h := page("live.hear.html"); !strings.Contains(h, `<code class="path">/live</code>`) {
		t.Error("an op without @ws doesn't open its api's socket")
	}
	if h := page("refunds.note.html"); strings.Contains(h, `id="refunds.note.body"`) {
		t.Error("a scalar parameter named body is shown as the request body")
	}
}
