package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	svc := &service{shell: &Shell{}, ws: w}
	code := func(err error) (int, string) {
		var e *route.Error
		if !errors.As(err, &e) {
			t.Fatalf("not an API error: %v", err)
		}
		return e.Status, e.Code
	}
	// A task already running.
	runs.Store("store_svc.test", true)
	defer runs.Delete("store_svc.test")
	err = svc.RunTask(context.Background(), &api.RunRequest{Path: "store_svc.test"}, func(*api.RunEvent) error { return nil })
	if st, c := code(err); st != http.StatusConflict || c != "server.ALREADY_RUNNING" || err.Error() != "store_svc.test is already running" {
		t.Errorf("already running: %d %s %v", st, c, err)
	}
	// No such record.
	_, err = svc.GetRecord(context.Background(), "nope")
	if st, c := code(err); st != http.StatusNotFound || c != "server.NO_RECORD" {
		t.Errorf("no record: %d %s", st, c)
	}
	// No language server.
	rec := httptest.NewRecorder()
	svc.Lsp(rec, httptest.NewRequest("GET", "/api/v1/lsp", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "{\"code\":\"server.NO_LANGUAGE_SERVER\",\"error\":\"no language server is installed\"}\n" {
		t.Errorf("no language server: %d %s", rec.Code, rec.Body.String())
	}
}
