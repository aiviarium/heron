package server_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/aiviarium/heron/internal/core/cli"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/tools/docs"
	"github.com/aiviarium/heron/internal/tools/server"
	"github.com/aiviarium/heron/internal/tools/server/api"
)

// scratch serves a workspace in a fresh folder, with a README and a file
// the tree doesn't include beside its entry point.
func scratch(t *testing.T, allow ...string) (*httptest.Server, string) {
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
	write("README.md", "# readme\n")
	write("notes/later.heron", "x: section {}\n")
	write(".git/HEAD", "ref\n")
	r := registry.New()
	cli.Register(r)
	docs.Register(r)
	s := &server.Shell{Renderer: server.DocsRenderer{Registry: r}, AllowOrigins: allow, Frontend: fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><title>app</title>")},
		"assets/app.js":    {Data: []byte(strings.Repeat("console.log(1);\n", 100))},
		"assets/app.js.gz": {Data: gz(strings.Repeat("console.log(1);\n", 100))},
	}}
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

func gz(s string) []byte {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write([]byte(s))
	zw.Close()
	return b.Bytes()
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

// list_files lists every file and folder, whether the tree reaches it or
// not, without .git, each with its size and time.
func TestListFiles(t *testing.T) {
	ts, _ := scratch(t)
	var fl api.FileList
	if st := do(t, ts, "GET", "/api/v1/files", nil, &fl); st != 200 {
		t.Fatalf("status %d", st)
	}
	got := map[string]api.FileEntry{}
	for _, e := range fl.Entries {
		got[e.Path] = e
	}
	if e := got["README.md"]; e.Size != 9 || e.InTree || e.Mtime == "" {
		t.Errorf("README.md: %+v", e)
	}
	if e := got["index.heron"]; !e.InTree {
		t.Errorf("index.heron isn't in the tree: %+v", e)
	}
	if e := got["notes"]; !e.Dir {
		t.Errorf("notes: %+v", e)
	}
	if _, ok := got["notes/later.heron"]; !ok {
		t.Error("a file the tree doesn't include is missing")
	}
	for p := range got {
		if strings.HasPrefix(p, ".git") {
			t.Errorf("%s is listed", p)
		}
	}
	var one api.FileList
	do(t, ts, "GET", "/api/v1/files?dir=notes", nil, &one)
	if len(one.Entries) != 1 || one.Entries[0].Path != "notes/later.heron" {
		t.Errorf("notes: %+v", one.Entries)
	}
}

// read and write answer the size, the time and the entity tag; a write
// with a stale if_match changes nothing and answers file changed.
func TestConditionalWrite(t *testing.T) {
	ts, dir := scratch(t)
	var fc api.FileContent
	do(t, ts, "GET", "/api/v1/file?path=README.md", nil, &fc)
	if fc.Text != "# readme\n" || fc.Encoding != "utf-8" || fc.Size != 9 || fc.Etag == "" || fc.Mtime == "" {
		t.Fatalf("read: %+v", fc)
	}
	tag := fc.Etag
	var sv api.Saved
	if st := do(t, ts, "POST", "/api/v1/file", api.WriteRequest{Path: "README.md", Text: "# one\n", IfMatch: &tag}, &sv); st != 200 || sv.Etag == tag || sv.Size != 6 {
		t.Fatalf("write: %d %+v", st, sv)
	}
	var e map[string]string
	if st := do(t, ts, "POST", "/api/v1/file", api.WriteRequest{Path: "README.md", Text: "# two\n", IfMatch: &tag}, &e); st != http.StatusPreconditionFailed || e["code"] != "server.FILE_CHANGED" || e["error"] != "README.md changed since it was read" {
		t.Fatalf("stale write: %d %v", st, e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "README.md")); string(b) != "# one\n" {
		t.Errorf("the stale write changed the file: %q", b)
	}
	empty := ""
	if st := do(t, ts, "POST", "/api/v1/file", api.WriteRequest{Path: "README.md", Text: "x", IfMatch: &empty}, nil); st != http.StatusPreconditionFailed {
		t.Errorf("an empty if_match on an existing file: %d", st)
	}
	if st := do(t, ts, "POST", "/api/v1/file", api.WriteRequest{Path: "new/deep.txt", Text: "x", IfMatch: &empty}, nil); st != 200 {
		t.Errorf("an empty if_match on a new file: %d", st)
	}
	if st := do(t, ts, "GET", "/api/v1/file?path=../x", nil, &e); st != 400 || e["code"] != "server.NOT_IN_WORKSPACE" {
		t.Errorf("outside: %d %v", st, e)
	}
}

// Binary content travels as base64 both ways.
func TestBinaryContent(t *testing.T) {
	ts, dir := scratch(t)
	raw := []byte{0x89, 'P', 'N', 'G', 0, 0xff}
	b64 := "base64"
	if st := do(t, ts, "POST", "/api/v1/file", api.WriteRequest{Path: "logo.png", Text: base64.StdEncoding.EncodeToString(raw), Encoding: &b64}, nil); st != 200 {
		t.Fatalf("write: %d", st)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "logo.png")); !bytes.Equal(b, raw) {
		t.Fatalf("on disk: %v", b)
	}
	var fc api.FileContent
	do(t, ts, "GET", "/api/v1/file?path=logo.png", nil, &fc)
	if fc.Encoding != "base64" || fc.Text != base64.StdEncoding.EncodeToString(raw) {
		t.Errorf("read: %+v", fc)
	}
	do(t, ts, "GET", "/api/v1/file?path=README.md&encoding=base64", nil, &fc)
	if fc.Encoding != "base64" {
		t.Errorf("asked for base64: %+v", fc)
	}
}

// delete, rename and make_dir change the workspace, refusing what doesn't
// exist and a target that does.
func TestFileChanges(t *testing.T) {
	ts, dir := scratch(t)
	var e map[string]string
	if st := do(t, ts, "POST", "/api/v1/dir", api.PathRequest{Path: "a/b"}, nil); st != 200 {
		t.Fatalf("make_dir: %d", st)
	}
	if st, err := os.Stat(filepath.Join(dir, "a/b")); err != nil || !st.IsDir() {
		t.Fatal("no folder a/b")
	}
	var sv api.Saved
	if st := do(t, ts, "POST", "/api/v1/file/rename", api.RenameRequest{From: "README.md", To: "a/b/README.md"}, &sv); st != 200 || sv.Path != "a/b/README.md" || sv.Size != 9 {
		t.Fatalf("rename: %d %+v", st, sv)
	}
	if st := do(t, ts, "POST", "/api/v1/file/rename", api.RenameRequest{From: "index.heron", To: "a/b/README.md"}, &e); st != 409 || e["code"] != "server.FILE_EXISTS" {
		t.Errorf("rename onto a file: %d %v", st, e)
	}
	if st := do(t, ts, "DELETE", "/api/v1/file?path=a/b/README.md", nil, nil); st != 200 {
		t.Errorf("delete: %d", st)
	}
	if st := do(t, ts, "DELETE", "/api/v1/file?path=a/b/README.md", nil, &e); st != 404 || e["code"] != "server.NO_SUCH_FILE" {
		t.Errorf("delete again: %d %v", st, e)
	}
}

// watch opens with ready, then sends each change made by anyone, and a
// problems event after it.
func TestWatch(t *testing.T) {
	ts, dir := scratch(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	next := func() api.WorkspaceEvent {
		t.Helper()
		for sc.Scan() {
			if l := sc.Text(); strings.HasPrefix(l, "data: ") {
				var e api.WorkspaceEvent
				json.Unmarshal([]byte(l[6:]), &e)
				return e
			}
		}
		t.Fatal("the stream ended")
		return api.WorkspaceEvent{}
	}
	if e := next(); e.Type != "ready" {
		t.Fatalf("first event %+v", e)
	}
	// Someone else writes a file.
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644)
	e := next()
	if e.Type != "file" || *e.Path != "other.txt" || *e.Change != "created" {
		t.Fatalf("event %+v", e)
	}
	if e := next(); e.Type != "problems" {
		t.Errorf("after a change: %+v", e)
	}
	os.Remove(filepath.Join(dir, "other.txt"))
	if e := next(); e.Type != "file" || *e.Change != "removed" {
		t.Errorf("event %+v", e)
	}
}

// The standard files are listed and read through the API.
func TestStdSources(t *testing.T) {
	ts, _ := scratch(t)
	var sf api.StdFiles
	do(t, ts, "GET", "/api/v1/std", nil, &sf)
	found := false
	for _, f := range sf.Files {
		found = found || f == "std/doc/package.heron"
	}
	if !found {
		t.Fatalf("std/doc/package.heron isn't listed: %v", sf.Files)
	}
	var fc api.FileContent
	if st := do(t, ts, "GET", "/api/v1/std/file?path=std/doc/package.heron", nil, &fc); st != 200 || !strings.Contains(fc.Text, "section") {
		t.Errorf("std source: %d %q", st, fc.Text)
	}
	if st := do(t, ts, "GET", "/api/v1/std/file?path=std/../../etc/passwd", nil, nil); st != 404 {
		t.Errorf("a path out of std: %d", st)
	}
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

// The workspace names the root URI the language server's URIs start with.
func TestRootURI(t *testing.T) {
	ts, dir := scratch(t)
	var w api.Workspace
	do(t, ts, "GET", "/api/v1/workspace", nil, &w)
	real, _ := filepath.EvalSymlinks(dir)
	if !strings.HasPrefix(w.RootUri, "file://") || !strings.HasSuffix(w.RootUri, "/") || !strings.Contains(w.RootUri, filepath.Base(real)) {
		t.Errorf("root_uri %q", w.RootUri)
	}
}

// A page of another origin may call the API only once allowed, and its
// preflight gets the private network header.
func TestOrigins(t *testing.T) {
	ts, _ := scratch(t, "https://vscode.dev")
	call := func(method, origin string, hdr map[string]string) *http.Response {
		req, _ := http.NewRequest(method, ts.URL+"/api/v1/workspace", nil)
		req.Header.Set("Origin", origin)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := call("GET", "https://evil.example", nil); res.StatusCode != 403 {
		t.Errorf("another origin: %d", res.StatusCode)
	}
	if res := call("GET", ts.URL, nil); res.StatusCode != 200 {
		t.Errorf("the server's own origin: %d", res.StatusCode)
	}
	res := call("GET", "https://vscode.dev", nil)
	if res.StatusCode != 200 || res.Header.Get("Access-Control-Allow-Origin") != "https://vscode.dev" {
		t.Errorf("an allowed origin: %d %v", res.StatusCode, res.Header)
	}
	res = call("OPTIONS", "https://vscode.dev", map[string]string{"Access-Control-Request-Method": "POST", "Access-Control-Request-Private-Network": "true"})
	if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Private-Network") != "true" {
		t.Errorf("preflight: %d %v", res.StatusCode, res.Header)
	}
}

// Static files go gzip-compressed, from a .gz sibling where there is one.
func TestCompressedAssets(t *testing.T) {
	ts, _ := scratch(t)
	tr := &http.Transport{DisableCompression: true}
	req, _ := http.NewRequest("GET", ts.URL+"/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.Header.Get("Content-Encoding") != "gzip" || !bytes.Equal(b, gz(strings.Repeat("console.log(1);\n", 100))) {
		t.Errorf("app.js: %v", res.Header)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/docs/_assets/docs.css", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err = tr.RoundTrip(req)
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
	for _, p := range []string{"/api/v1/private", "/api/v1/workspace"} {
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
