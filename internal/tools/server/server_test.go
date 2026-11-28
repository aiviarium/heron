package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/aiviarium/heron/internal/core/cli"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/tools/docs"
	"github.com/aiviarium/heron/internal/tools/server"
	"github.com/aiviarium/heron/internal/tools/server/api"
	"github.com/coder/websocket"
)

// echoLSP is a test double for the language server: it answers every
// message with the same message.
func echoLSP(ctx context.Context, root string, c server.MessageConn) error {
	for {
		m, err := c.ReadMessage()
		if err != nil {
			return nil
		}
		if err := c.WriteMessage(m); err != nil {
			return err
		}
	}
}

func petstore(t *testing.T) (*httptest.Server, *server.Shell) {
	t.Helper()
	r := registry.New()
	cli.Register(r)
	docs.Register(r)
	s := &server.Shell{LSP: echoLSP, Renderer: server.DocsRenderer{Registry: r}, Frontend: fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>app</title>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}}
	w, err := server.OpenWorkspace("../../../examples/petstore", r)
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler(w)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, s
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestFrontendFallsBackToIndex(t *testing.T) {
	ts, _ := petstore(t)
	for _, p := range []string{"/", "/edit/specs/store/api.heron", "/anything/else"} {
		if code, body := get(t, ts.URL+p); code != 200 || !strings.Contains(body, "<title>app</title>") {
			t.Errorf("%s: %d %q", p, code, body)
		}
	}
	if _, body := get(t, ts.URL+"/assets/app.js"); body != "console.log(1)" {
		t.Errorf("asset: %q", body)
	}
	if code, body := get(t, ts.URL+"/api/v1/nothing"); code != 404 || !strings.Contains(body, `"error"`) {
		t.Errorf("unknown op: %d %q", code, body)
	}
}

func TestBaseOps(t *testing.T) {
	ts, _ := petstore(t)
	c := api.Routes(nil)
	if len(c) == 0 {
		t.Fatal("no routes generated")
	}
	_, body := get(t, ts.URL+"/api/v1/workspace")
	var ws api.Workspace
	if err := json.Unmarshal([]byte(body), &ws); err != nil {
		t.Fatal(err, body)
	}
	if ws.Entry != "index.heron" || len(ws.Files) < 4 || ws.Files[0] != "index.heron" {
		t.Errorf("workspace: %+v", ws)
	}
	post := func(path string, v any, out any) {
		b, _ := json.Marshal(v)
		res, err := http.Post(ts.URL+path, "application/json", strings.NewReader(string(b)))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(path, err)
		}
	}
	var probs api.Problems
	post("/api/v1/problems", api.TreeRequest{Overlays: map[string]string{}}, &probs)
	if len(probs.Problems) != 0 {
		t.Errorf("the sample has problems: %+v", probs.Problems)
	}
	broken := map[string]string{"services/store/tasks.heron": "store_svc.{\n  build: task do {\n    nope()\n  }\n}\n"}
	post("/api/v1/problems", api.TreeRequest{Overlays: broken}, &probs)
	if len(probs.Problems) == 0 || probs.Problems[0].File != "services/store/tasks.heron" || probs.Problems[0].Line != 3 {
		t.Errorf("overlay problems: %+v", probs.Problems)
	}
	var out api.Outline
	post("/api/v1/outline", api.OutlineRequest{File: "services/store/tasks.heron", Overlays: map[string]string{}}, &out)
	if len(out.Nodes) < 3 || !out.Nodes[len(out.Nodes)-1].Runnable {
		t.Errorf("outline: %+v", out.Nodes)
	}
	src, _ := os.ReadFile("../../../examples/petstore/services/store/tasks.heron")
	off := int64(strings.Index(string(src), "test: task"))
	var pv api.Preview
	post("/api/v1/preview", api.PreviewRequest{File: "services/store/tasks.heron", Offset: &off, Overlays: map[string]string{}}, &pv)
	if pv.Path != "store_svc.test" || pv.Kind != "task" || !strings.Contains(pv.HTML, "test") {
		t.Errorf("preview: %+v", pv)
	}
}

func TestSpecBrowser(t *testing.T) {
	ts, _ := petstore(t)
	code, body := get(t, ts.URL+"/docs/")
	if code != 200 || !strings.Contains(body, "/docs/_assets/docs.css") {
		t.Errorf("docs index: %d %.300q", code, body)
	}
	if code, body := get(t, ts.URL+"/docs/_assets/data.js"); code != 200 || !strings.Contains(body, "__HERON__") {
		t.Errorf("data.js: %d %.100q", code, body)
	}
	if code, _ := get(t, ts.URL+"/docs/_assets/docs.css"); code != 200 {
		t.Errorf("docs.css: %d", code)
	}
}

// The agent export of the served tree is under /agents/, its index at the
// folder itself, and through agent_export: every file, or one by its path,
// and no such file for a path the export lacks.
func TestAgentExportServed(t *testing.T) {
	ts, _ := petstore(t)
	res, err := http.Get(ts.URL + "/agents/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/markdown") || !strings.Contains(string(body), "## How to read these files") {
		t.Errorf("/agents/: %d %s %.200q", res.StatusCode, res.Header.Get("Content-Type"), body)
	}
	if code, body := get(t, ts.URL+"/agents/llms.txt"); code != 200 || !strings.Contains(body, "](INDEX.md)") {
		t.Errorf("llms.txt: %d %.200q", code, body)
	}
	if code, _ := get(t, ts.URL+"/agents/nothing.md"); code != 404 {
		t.Errorf("a missing file: %d", code)
	}
	var all api.GeneratedFiles
	code, raw := get(t, ts.URL+"/api/v1/agents")
	if code != 200 || json.Unmarshal([]byte(raw), &all) != nil || len(all.Files) < 3 || all.Files[0].Path != "INDEX.md" || all.Files[1].Path != "llms.txt" {
		t.Fatalf("agent_export: %d %.300q", code, raw)
	}
	code, raw = get(t, ts.URL+"/api/v1/agents?path="+all.Files[2].Path)
	var one api.GeneratedFiles
	if code != 200 || json.Unmarshal([]byte(raw), &one) != nil || len(one.Files) != 1 || one.Files[0].Text != all.Files[2].Text {
		t.Errorf("agent_export one file: %d %.300q", code, raw)
	}
	if code, raw := get(t, ts.URL+"/api/v1/agents?path=nothing.md"); code != 404 || !strings.Contains(raw, "server.NO_SUCH_FILE") {
		t.Errorf("agent_export of a missing file: %d %.300q", code, raw)
	}
}

// The docs under `heron serve` are cached as a static site's are: an asset
// at its content's version is kept for good, and everything else is
// revalidated with its ETag, so moving between pages fetches only the page.
func TestSpecBrowserCaching(t *testing.T) {
	ts, _ := petstore(t)
	head := func(url, etag string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", url, nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return res
	}
	_, page := get(t, ts.URL+"/docs/")
	i := strings.Index(page, "/docs/_assets/docs.css?v=")
	if i < 0 {
		t.Fatalf("the stylesheet's URL has no version: %.400q", page)
	}
	versioned := page[i : i+strings.IndexByte(page[i:], '"')]
	if res := head(ts.URL+versioned, ""); res.StatusCode != 200 || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("%s: %d %q", versioned, res.StatusCode, res.Header.Get("Cache-Control"))
	}
	for _, p := range []string{"/docs/", "/docs/_assets/data.js", "/docs/_assets/docs.css"} {
		res := head(ts.URL+p, "")
		tag := res.Header.Get("ETag")
		if res.StatusCode != 200 || tag == "" || res.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %d etag %q cache %q", p, res.StatusCode, tag, res.Header.Get("Cache-Control"))
			continue
		}
		if again := head(ts.URL+p, tag); again.StatusCode != http.StatusNotModified {
			t.Errorf("%s with its ETag: %d, not 304", p, again.StatusCode)
		}
	}
}

func TestLSPSocket(t *testing.T) {
	ts, _ := petstore(t)
	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/v1/lsp", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	for i, msg := range []string{`{"jsonrpc":"2.0","id":1,"method":"initialize"}`, `{"jsonrpc":"2.0","method":"x","params":"é"}`} {
		if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatal(err)
		}
		_, got, err := c.Read(ctx)
		if err != nil || string(got) != msg {
			t.Errorf("%d: %q %v", i, got, err)
		}
	}
}

func TestGeneratedRoutesMatchTheHeron(t *testing.T) {
	want := map[string]string{
		"spec_server.preview":    "POST /api/v1/preview",
		"spec_server.results":    "GET /api/v1/results",
		"spec_server.get_record": "GET /api/v1/records/{id}",
		"spec_server.lsp":        "GET /api/v1/lsp",
	}
	for _, r := range api.Routes(nil) {
		if p, ok := want[r.Op]; ok && p != r.Pattern() {
			t.Errorf("%s: %s, want %s", r.Op, r.Pattern(), p)
		}
		delete(want, r.Op)
	}
	if len(want) != 0 {
		t.Errorf("ops missing: %v", want)
	}
	_ = filepath.Join
}
