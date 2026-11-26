package docs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/gen"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// `heron generate asyncapi` writes one AsyncAPI 3.0 document per api: a
// channel per op at its full path, send and receive operations, its
// errors as messages and its types as JSON Schemas; and it runs only when
// asked for.
func TestAsyncAPI(t *testing.T) {
	id, _ := resolve.Canonical(kite)
	res := check.NewSession().Check(id)
	docs := AsyncAPI(res.Tree, res.Checker)
	b, ok := docs["kite.store_api.json"]
	if !ok {
		t.Fatalf("no document: %v", docs)
	}
	var d struct {
		AsyncAPI string `json:"asyncapi"`
		Channels map[string]struct {
			Address  string
			Messages map[string]map[string]string
		}
		Operations map[string]struct {
			Action   string
			Messages []map[string]string
		}
		Components struct {
			Messages map[string]struct {
				Payload     map[string]any
				Description string
			}
			Schemas map[string]struct {
				Type       string
				Required   []string
				Properties map[string]map[string]any
				Enum       []string
			}
		}
	}
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if d.AsyncAPI != "3.0.0" {
		t.Errorf("asyncapi %q", d.AsyncAPI)
	}
	if c := d.Channels["get_note"]; c.Address != "/v1/notes/:id" || c.Messages["response"]["$ref"] != "#/components/messages/get_note.response" {
		t.Errorf("get_note channel: %+v", c)
	}
	if op := d.Operations["put_note.receive"]; op.Action != "receive" || len(op.Messages) != 2 {
		t.Errorf("put_note.receive: %+v", op)
	}
	if m := d.Components.Messages["error.note_locked"]; m.Description != "HTTP status 423" || m.Payload["properties"].(map[string]any)["code"].(map[string]any)["const"] != "store.NOTE_LOCKED" {
		t.Errorf("note_locked: %+v", m)
	}
	note := d.Components.Schemas["Note"]
	if note.Type != "object" || len(note.Required) != 3 || note.Properties["updated_at"]["format"] != "date-time" {
		t.Errorf("Note: %+v", note)
	}
	if note.Properties["path"]["description"] == nil {
		t.Errorf("Note.path has no description: %+v", note.Properties["path"])
	}
	// Run as one of every target, it writes nothing.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.heron"), []byte("import = [\"std/types@1\", \"std/http@1\"]\n\n@http(base_path = \"/v1\")\nsvc: api {\n  @http<get>(\"/a\")\n  a(): op(String)\n}\n"), 0o644)
	eid, _ := resolve.Canonical(filepath.Join(dir, "index.heron"))
	r2 := check.NewSession().Check(eid)
	reg := registry.New()
	Register(reg)
	if _, err := gen.Run(context.Background(), reg, &gen.Input{Tree: r2.Tree, Checker: r2.Checker, Dir: dir}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".heron/asyncapi")); !os.IsNotExist(err) {
		t.Error("asyncapi ran as one of every target")
	}
	if _, err := gen.Run(context.Background(), reg, &gen.Input{Tree: r2.Tree, Checker: r2.Checker, Dir: dir}, []string{"asyncapi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".heron/asyncapi/svc.json")); err != nil {
		t.Error("asked for, asyncapi wrote nothing")
	}
}
