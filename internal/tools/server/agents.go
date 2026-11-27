package server

import (
	"context"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/tools/docs"
	"github.com/aiviarium/heron/internal/tools/server/api"
)

// AgentsPrefix is where the agent export of the served tree is served.
const AgentsPrefix = "/agents/"

// agentExport is the agent export of the workspace's latest check, written
// once per check and shared by the /agents/ pages and the agent_export op.
type agentExport struct {
	ws *Workspace

	mu    sync.Mutex
	res   *check.Result
	files []docs.AgentFile
	err   error
}

// current returns the export of the workspace's tree as it stands.
func (x *agentExport) current() ([]docs.AgentFile, error) {
	var files []docs.AgentFile
	err := x.ws.Analyze(nil, func(a *Analysis) error {
		x.mu.Lock()
		defer x.mu.Unlock()
		if x.res != a.Res {
			x.res = a.Res
			x.files, x.err = docs.Agents(a.Res.Tree, a.Res.Checker, docs.AgentOptions{})
		}
		files = x.files
		return x.err
	})
	return files, err
}

func (x *agentExport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(r.URL.Path, AgentsPrefix)), "/")
	if p == "" || p == "." {
		p = docs.AgentIndex
	}
	files, err := x.current()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, f := range files {
		if f.Path == p {
			ctype := "text/markdown; charset=utf-8"
			if p == docs.LLMsTxt {
				ctype = "text/plain; charset=utf-8"
			}
			serveCached(w, r, ctype, f.Content)
			return
		}
	}
	http.NotFound(w, r)
}

// AgentExport answers agent_export: every file of the export, or the one
// at path.
func (s *service) AgentExport(ctx context.Context, p string) (*api.GeneratedFiles, error) {
	files, err := s.agents.current()
	if err != nil {
		return nil, err
	}
	out := &api.GeneratedFiles{Files: []api.GeneratedFile{}}
	for _, f := range files {
		if p == "" || f.Path == p {
			out.Files = append(out.Files, api.GeneratedFile{Path: f.Path, Text: string(f.Content)})
		}
	}
	if p != "" && len(out.Files) == 0 {
		return nil, errNoSuchFile(p)
	}
	return out, nil
}
