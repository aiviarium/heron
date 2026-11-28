package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/tools/server/api"
	"github.com/aiviarium/heron/internal/tools/server/route"
)

// Each error the API declares goes out with its status and its id as the
// body's code.
func TestErrorCodes(t *testing.T) {
	w, err := OpenWorkspace("../../../examples/petstore", registry.New())
	if err != nil {
		t.Fatal(err)
	}
	svc := &service{shell: &Shell{}, ws: w, agents: &agentExport{ws: w}}
	code := func(err error) (int, string) {
		var e *route.Error
		if !errors.As(err, &e) {
			t.Fatalf("not an API error: %v", err)
		}
		return e.Status, e.Code
	}
	_, err = svc.Preview(context.Background(), &api.PreviewRequest{File: "/etc/passwd"})
	if st, c := code(err); st != http.StatusBadRequest || c != "server.NOT_IN_WORKSPACE" || err.Error() != "/etc/passwd is not a path in the workspace" {
		t.Errorf("not in workspace: %d %s %v", st, c, err)
	}
	_, err = svc.AgentExport(context.Background(), "nothing.md")
	if st, c := code(err); st != http.StatusNotFound || c != "server.NO_SUCH_FILE" || err.Error() != "nothing.md: no such file" {
		t.Errorf("no such file: %d %s %v", st, c, err)
	}
}
