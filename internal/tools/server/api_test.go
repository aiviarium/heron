package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiviarium/heron/internal/core/cli"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/tools/docs"
	"github.com/aiviarium/heron/internal/tools/server"
	"github.com/aiviarium/heron/internal/tools/server/api"
)

// scratch serves a workspace in a fresh folder.
func scratch(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.heron", "import = [\"std/doc@1\"]\n\nintro: section {\n  | Hi.\n}\n")
	r := registry.New()
	cli.Register(r)
	docs.Register(r)
	s := &server.Shell{Renderer: server.DocsRenderer{Registry: r}}
	w, err := server.OpenWorkspace(dir, r)
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.Handler(w)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts, dir
}

// do sends a request and decodes the JSON answer; it returns the status.
func do(t *testing.T, ts *httptest.Server, method, path string, body, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, rd)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

// A preview's position counts UTF-16 columns, as the protocol does.
func TestPreviewPosition(t *testing.T) {
	ts, _ := scratch(t)
	text := "import = [\"std/doc@1\"]\n\n// é😀\nintro: section {\n  | Hi.\n}\n"
	var pv api.Preview
	do(t, ts, "POST", "/api/v1/preview", api.PreviewRequest{File: "index.heron", Position: &api.Position{Line: 3, Character: 2}, Overlays: map[string]string{"index.heron": text}}, &pv)
	if pv.Path != "intro" {
		t.Errorf("preview at 3:2: %+v", pv)
	}
}

// A page of another origin may not call the API; the server's own may.
func TestOrigins(t *testing.T) {
	ts, _ := scratch(t)
	call := func(origin string) *http.Response {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/agents", nil)
		req.Header.Set("Origin", origin)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := call("https://evil.example"); res.StatusCode != 403 || res.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("another origin: %d %v", res.StatusCode, res.Header)
	}
	if res := call(ts.URL); res.StatusCode != 200 {
		t.Errorf("the server's own origin: %d", res.StatusCode)
	}
}

// The docs' assets go gzip-compressed to a client that accepts it.
func TestCompressedAssets(t *testing.T) {
	ts, _ := scratch(t)
	tr := &http.Transport{DisableCompression: true}
	req, _ := http.NewRequest("GET", ts.URL+"/docs/_assets/docs.css", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("docs.css: %d %v", res.StatusCode, res.Header)
	}
}

// A path under /api/ that no op declares answers no op.
func TestNoOp(t *testing.T) {
	ts, _ := scratch(t)
	var e map[string]string
	if st := do(t, ts, "GET", "/api/v1/private", nil, &e); st != 404 || e["code"] != "server.NO_OP" || e["error"] != "no op at GET /api/v1/private" {
		t.Errorf("%d %v", st, e)
	}
	for _, p := range []string{"/api/v1/private", "/api/v1/agents"} {
		res, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if ct := res.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s answers %s", p, ct)
		}
	}
}
