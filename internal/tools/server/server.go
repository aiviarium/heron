// Package server is the shell of `heron serve`: it serves the frontend, the
// editor's language server socket and the API, whose routes come from code
// apigen generates from spec_server.
package server

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/tools/server/api"
	"github.com/aiviarium/heron/internal/tools/server/route"
)

// LSP serves one language-server session over a connection of whole
// messages until it ends. LanguageServer gives the lsp package's; the
// server opens one per socket.
type LSP func(ctx context.Context, root string, c MessageConn) error

// Renderer renders a node, or a file's top-level nodes, of a checked tree
// as the docs render them.
type Renderer interface {
	RenderNode(res *check.Result, n *resolve.Node) (string, error)
	RenderFile(res *check.Result, file resolve.FileID) (string, error)
}

// Shell is the server shell.
type Shell struct {
	// LSP opens the language server for /lsp; nil answers 503.
	LSP LSP
	// Renderer renders the preview; nil uses the generic node view.
	Renderer Renderer
	// Frontend is the built frontend; nil uses the embedded one.
	Frontend fs.FS
	// AllowOrigins are the origins besides the server's own whose pages may
	// call the API.
	AllowOrigins []string
}

// Register adds `heron serve` to the registry and returns its shell.
func Register(r *registry.Registry) *Shell {
	s := &Shell{Renderer: DocsRenderer{Registry: r}, LSP: LanguageServer(r)}
	r.MustAddCommand(registry.Command{
		Name:    "serve",
		Summary: "Serve the spec browser, the editor and the language server",
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
	var origins originList
	fl.Var(&origins, "allow-origin", "an origin whose pages may call the API, such as https://vscode.dev; may be given more than once")
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
	s.AllowOrigins = origins
	h, err := s.Handler(w)
	if err != nil {
		fmt.Fprintln(env.Stderr, "heron serve:", err)
		return 1
	}
	if !loopback(*addr) {
		fmt.Fprintf(env.Stderr, "heron serve: warning: %s isn't a loopback address; the server has no authentication, and anyone who reaches it can read and change the workspace\n", *addr)
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

// Handler returns the server for a workspace: the API's routes and the
// frontend, whose unknown paths serve
// index.html so the app's own routes load.
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
	front := s.Frontend
	if front == nil {
		front = Frontend()
	}
	served := map[string]bool{}
	for _, rt := range routes {
		served[rt.Op] = true
	}
	mux.Handle("GET "+DocsPrefix, gzipped(&specBrowser{ws: w, served: served}))
	mux.Handle("GET "+AgentsPrefix, gzipped(svc.agents))
	mux.Handle("/", gzipped(spa(front)))
	return guardOrigins(mux, s.AllowOrigins), nil
}

// spa serves the frontend's files and index.html for every other path.
func spa(front fs.FS) http.Handler {
	files := http.FileServerFS(front)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(front, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				if gz, err := fs.ReadFile(front, p+".gz"); err == nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
					// The file's sibling, compressed when the frontend was built.
					if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
						w.Header().Set("Content-Type", ct)
					}
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Add("Vary", "Accept-Encoding")
					_, _ = w.Write(gz)
					return
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		b, err := fs.ReadFile(front, "index.html")
		if err != nil {
			http.Error(w, "the frontend isn't built into this binary", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	})
}

var _ = os.Getwd
