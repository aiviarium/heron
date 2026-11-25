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
	"github.com/aiviarium/heron/internal/tools/server/internal/sample"
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
	if err := s.Mount(sample.Extension()); err != nil {
		t.Fatal(err)
	}
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
	off := strings.Index(string(src), "test: task")
	var pv api.Preview
	post("/api/v1/preview", api.PreviewRequest{File: "services/store/tasks.heron", Offset: int64(off), Overlays: map[string]string{}}, &pv)
	if pv.Path != "store_svc.test" || pv.Kind != "task" || !strings.Contains(pv.HTML, "test") {
		t.Errorf("preview: %+v", pv)
	}
	// The sample extension's provider answers beside the built-in one.
	found := false
	for _, r := range pv.Results.Results {
		found = found || r.Source == "sample"
	}
	if !found {
		t.Errorf("results: %+v", pv.Results)
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

func TestExtensionRoutesAreMounted(t *testing.T) {
	ts, s := petstore(t)
	if code, body := get(t, ts.URL+"/api/v1/sample/echo/ab?times=3"); code != 200 || !strings.Contains(body, `"ababab"`) {
		t.Errorf("sample op: %d %q", code, body)
	}
	if code, _ := get(t, ts.URL+"/api/v1/sample/echo/ab?times=x"); code != 400 {
		t.Errorf("bad param: %d", code)
	}
	if err := s.Mount(sample.Extension()); err == nil {
		t.Error("mounting one route twice was accepted")
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
