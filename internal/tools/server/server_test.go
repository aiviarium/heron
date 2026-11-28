package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiviarium/heron/internal/core/cli"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/tools/docs"
	"github.com/aiviarium/heron/internal/tools/server"
	"github.com/aiviarium/heron/internal/tools/server/api"
)

func petstore(t *testing.T) (*httptest.Server, *server.Shell) {
	t.Helper()
	r := registry.New()
	cli.Register(r)
	docs.Register(r)
	s := &server.Shell{}
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

// / redirects to the spec browser, and a path outside /api/, /docs/ and
// /agents/ answers not found.
func TestRootRedirects(t *testing.T) {
	ts, _ := petstore(t)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/docs/" {
		t.Errorf("/: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	for _, p := range []string{"/edit/specs/store/api.heron", "/assets/app.js"} {
		if code, _ := get(t, ts.URL+p); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
	if code, body := get(t, ts.URL+"/api/v1/nothing"); code != 404 || !strings.Contains(body, `"error"`) {
		t.Errorf("unknown op: %d %q", code, body)
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

func TestGeneratedRoutesMatchTheHeron(t *testing.T) {
	want := map[string]string{
		"spec_server.agent_export": "GET /api/v1/agents",
	}
	for _, r := range api.Routes(nil) {
		if p, ok := want[r.Op]; ok && p != r.Pattern() {
			t.Errorf("%s: %s, want %s", r.Op, r.Pattern(), p)
		}
		if _, ok := want[r.Op]; !ok {
			t.Errorf("an op the test doesn't know: %s", r.Op)
		}
		delete(want, r.Op)
	}
	if len(want) != 0 {
		t.Errorf("ops missing: %v", want)
	}
}

// syncBuffer is a buffer the command writes while the test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// `heron serve` listens on --addr, port 0 picking a free one, prints the
// address, and serves the entry point's tree; an unknown option or a
// missing entry point exits 2, and an address it can't listen on 1.
func TestServeCommand(t *testing.T) {
	r := registry.New()
	cli.Register(r)
	docs.Register(r)
	server.Register(r)
	serve := r.Command("serve")
	run := func(args ...string) (int, string) {
		var out, errb syncBuffer
		code := serve.Run(&registry.Env{Dir: "../../..", Stdout: &out, Stderr: &errb}, args)
		return code, errb.String()
	}
	if code, _ := run("--nope"); code != 2 {
		t.Errorf("an unknown option: %d", code)
	}
	if code, msg := run("examples/nowhere"); code != 2 || !strings.Contains(msg, "no entry point") {
		t.Errorf("a missing entry point: %d %q", code, msg)
	}
	if code, _ := run("--addr", "256.0.0.1:1", "examples/petstore"); code != 1 {
		t.Errorf("an address it can't listen on: %d", code)
	}
	var out, errb syncBuffer
	go serve.Run(&registry.Env{Dir: "../../..", Stdout: &out, Stderr: &errb}, []string{"--addr", "127.0.0.1:0", "examples/petstore"})
	var url string
	for i := 0; i < 200 && url == ""; i++ {
		url = strings.TrimSpace(out.String())
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/") || strings.HasSuffix(url, ":0/") {
		t.Fatalf("the printed address: %q %s", url, errb.String())
	}
	if code, body := get(t, url+"docs/"); code != 200 || !strings.Contains(body, "/docs/_assets/docs.css") {
		t.Errorf("the served tree: %d %.200q", code, body)
	}
}
