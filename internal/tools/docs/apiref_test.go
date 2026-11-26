package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// The api page groups the ops with one line each, says the wire format and
// lists the types and errors the ops name.
func TestAPILanding(t *testing.T) {
	_, f := lensFiles(t, kite)
	h := f["dev/kite.store_api.html"]
	mustHave(t, "api page", h,
		`<dt>Wire format</dt><dd>JSON over HTTP; streams as server-sent events; sockets as websocket frames</dd>`,
		`<span class="op-line">Returns one note, with the time it last changed.</span>`,
		`<span class="op-line">Lists every note.</span>`,
		`<span class="badge badge-deprecated">Deprecated</span>`,
		`id="kite.store_api.types"`, `href="kite.Note.html"`, `kite.note_locked"`)
}

// An op page links what it realizes by name, marks deprecation, shows an
// error node with its status, code, title and fields, and an op without an
// example shows its messages as JSON built from their types.
func TestOpPageOutcomes(t *testing.T) {
	_, f := lensFiles(t, kite)
	get := f["dev/kite.store_api.get_note.html"]
	mustHave(t, "get_note", get, `<dt>Realizes</dt><dd><a class="ref" href="index.html#kite.gone_notes">Gone notes</a></dd>`)
	mustNotHave(t, "get_note", get, `(gone_notes)`)
	put := f["dev/kite.store_api.put_note.html"]
	mustHave(t, "put_note", put,
		`<code class="status err">423</code>`, `<code class="err-code">store.NOTE_LOCKED</code>`,
		`<p class="err-title"><span class="ph">‹id›</span> is being written by another sync</p>`,
		`<span class="label">Fields</span> <code>id</code> <code class="t">String</code>`,
		// No example: the body as JSON from Note's fields.
		`&#34;updated_at&#34;`)
	list := f["dev/kite.store_api.list_notes.html"]
	mustHave(t, "list_notes", list, `<span class="badge badge-deprecated">Deprecated</span>`)
	nav := regexp.MustCompile(`(?s)<nav class="sidebar".*?</nav>`).FindString(list)
	mustHave(t, "nav", nav, `<span class="nav-deprecated"`)
}

// The server's own API offers Try it only where that server serves the
// page, through the generated client.
func TestServedTryIt(t *testing.T) {
	dir := t.TempDir()
	src := `import = ["std/types@1", "std/http@1"]

@http(base_path = "/api/v1")
spec_server: api {
  @http<get>("/workspace")
  workspace(): op(Workspace) {
    | The workspace.
  }
}

Workspace: type {
  name: String
}
`
	p := filepath.Join(dir, "index.heron")
	os.WriteFile(p, []byte(src), 0o644)
	id, _ := resolve.Canonical(p)
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatalf("%v", res.Diags)
	}
	page := func(served map[string]bool) string {
		s := New(res.Tree, res.Checker, Options{ServedOps: served})
		files, err := s.Files()
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if f.Path == "spec_server.workspace.html" {
				return string(f.Content)
			}
		}
		t.Fatal("no op page")
		return ""
	}
	static := page(nil)
	mustNotHave(t, "static build", static, `data-tryit`)
	served := page(map[string]bool{"spec_server.workspace": true})
	mustHave(t, "served", served, `data-tryit`, `"client":"workspace"`)
}
