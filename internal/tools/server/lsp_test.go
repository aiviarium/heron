package server_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/aiviarium/heron/internal/core/cli"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/tools/server"
	"github.com/coder/websocket"
)

// The real language server answers on the socket, one session per socket.
func TestRealLanguageServerOnTheSocket(t *testing.T) {
	r := registry.New()
	cli.Register(r)
	w, err := server.OpenWorkspace("../../../examples/petstore", r)
	if err != nil {
		t.Fatal(err)
	}
	h, err := (&server.Shell{LSP: server.LanguageServer(r), Frontend: fstest.MapFS{}}).Handler(w)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/v1/lsp", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":null,"capabilities":{}}}`
	if err := c.Write(ctx, websocket.MessageText, []byte(init)); err != nil {
		t.Fatal(err)
	}
	for {
		_, got, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		_ = json.Unmarshal(got, &m)
		if m.ID != nil && *m.ID == 1 {
			if !strings.Contains(string(m.Result), "capabilities") {
				t.Fatalf("initialize: %s", got)
			}
			return
		}
	}
}
