// Package server is the shell of `heron serve`: it serves the spec browser,
// the agent export and the API, whose routes come from code apigen
// generates from spec_server.
package server

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/tools/server/api"
	"github.com/aiviarium/heron/internal/tools/server/route"
)

// Renderer renders a node, or a file's top-level nodes, of a checked tree
// as the docs render them.
type Renderer interface {
	RenderNode(res *check.Result, n *resolve.Node) (string, error)
	RenderFile(res *check.Result, file resolve.FileID) (string, error)
}

// Shell is the server shell.
type Shell struct {
	// Renderer renders the preview; nil uses the generic node view.
	Renderer Renderer
}

// Register adds `heron serve` to the registry and returns its shell.
func Register(r *registry.Registry) *Shell {
	s := &Shell{Renderer: DocsRenderer{Registry: r}}
	r.MustAddCommand(registry.Command{
		Name:    "serve",
		Summary: "Serve the spec browser, rendered live, and the preview",
		Usage:   "[--addr host:port] [entry point]",
		Run: func(env *registry.Env, args []string) int {
			return s.command(env, args, r)
		},
	})
	return s
}

func (s *Shell) command(env *registry.Env, args []string, r *registry.Registry) int {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(env.Stderr)
	addr := fl.String("addr", "127.0.0.1:7340", "the address to listen on; port 0 picks a free one")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	entry := "."
	if fl.NArg() > 0 {
		entry = fl.Arg(0)
	}
	if !filepath.IsAbs(entry) {
		entry = filepath.Join(env.Dir, entry)
	}
	w, err := OpenWorkspace(entry, r)
	if err != nil {
		fmt.Fprintln(env.Stderr, "heron serve:", err)
		return 2
	}
	h, err := s.Handler(w)
	if err != nil {
		fmt.Fprintln(env.Stderr, "heron serve:", err)
		return 1
	}
	if !loopback(*addr) {
		fmt.Fprintf(env.Stderr, "heron serve: warning: %s isn't a loopback address; the server has no authentication, and anyone who reaches it can read the workspace\n", *addr)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(env.Stderr, "heron serve:", err)
		return 1
	}
	fmt.Fprintf(env.Stdout, "http://%s/\n", ln.Addr())
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(env.Stderr, "heron serve:", err)
		return 1
	}
	return 0
}

// Handler returns the server for a workspace: the API's routes, the spec
// browser and the agent export, with / redirecting to the spec browser.
func (s *Shell) Handler(w *Workspace) (http.Handler, error) {
	mux := http.NewServeMux()
	svc := &service{shell: s, ws: w, agents: &agentExport{ws: w}}
	routes := api.Routes(svc)
	for _, rt := range routes {
		mux.HandleFunc(rt.Pattern(), rt.Handler)
	}
	mux.HandleFunc("/api/", func(rw http.ResponseWriter, r *http.Request) {
		route.WriteError(rw, route.Coded(http.StatusNotFound, "server.NO_OP", "no op at %s %s", r.Method, r.URL.Path))
	})
	served := map[string]bool{}
	for _, rt := range routes {
		served[rt.Op] = true
	}
	mux.Handle("GET "+DocsPrefix, gzipped(&specBrowser{ws: w, served: served}))
	mux.Handle("GET "+AgentsPrefix, gzipped(svc.agents))
	mux.HandleFunc("GET /{$}", func(rw http.ResponseWriter, r *http.Request) {
		http.Redirect(rw, r, DocsPrefix, http.StatusFound)
	})
	return guardOrigins(mux), nil
}
