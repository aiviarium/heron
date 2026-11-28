package server

import (
	"net/http"

	"github.com/aiviarium/heron/internal/tools/server/api"
	"github.com/aiviarium/heron/internal/tools/server/route"
)

// service implements the API's ops.
type service struct {
	ws     *Workspace
	agents *agentExport
}

var _ api.Service = (*service)(nil)

// The API's errors, by the id each declares.
func errNoSuchFile(p string) error {
	return route.Coded(http.StatusNotFound, "server.NO_SUCH_FILE", "%s: no such file", p)
}
