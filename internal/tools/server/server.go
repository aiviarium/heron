// Package server is the shell of `heron serve`: it serves the frontend, the
// editor's language server socket and the API, and mounts the ops that
// other units declare on `spec_server`. Each unit's routes come from code
// apigen generates from its Heron files; the shell only mounts them.
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
	"sync"
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

// Results gives the latest results for the nodes under a path ("" for all),
// such as a task's last run. Units add providers; the run records are the
// built-in one.
type Results func(ctx context.Context, w *Workspace, path string) []api.NodeResult

// Extension is what a unit mounts on the shell: the routes apigen generated
// from its ops on spec_server, and its result providers.
type Extension struct {
	Name    string
	Routes  []route.Route
	Results []Results
}

// Shell is the server shell. Units mount extensions on it before it runs.
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

	mu   sync.Mutex
	exts []Extension
}

// Mount adds a unit's extension. Two routes with one pattern are an error.
func (s *Shell) Mount(e Extension) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, x := range s.exts {
		for _, r := range x.Routes {
			seen[r.Pattern()] = true
		}
	}
	for _, r := range e.Routes {
		if seen[r.Pattern()] {
			return fmt.Errorf("server: %s mounts %s, which is already mounted", e.Name, r.Pattern())
		}
	}
	s.exts = append(s.exts, e)
	return nil
}

// Register adds `heron serve` to the registry and returns the shell that
// later units mount their extensions on.
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

// Handler returns the server for a workspace: the base API, every mounted
// extension's routes, and the frontend, whose unknown paths serve
// index.html so the app's own routes load.
func (s *Shell) Handler(w *Workspace) (http.Handler, error) {
	mux := http.NewServeMux()
	svc := &service{shell: s, ws: w}
	s.mu.Lock()
	exts := append([]Extension{{Name: "base", Routes: api.Routes(svc)}}, s.exts...)
	s.mu.Unlock()
	svc.exts = exts
	seen := map[string]string{}
	for _, e := range exts {
		for _, rt := range e.Routes {
			p := rt.Pattern()
			if prev, dup := seen[p]; dup {
				return nil, fmt.Errorf("server: %s and %s both mount %s", prev, e.Name, p)
			}
			seen[p] = e.Name
			mux.HandleFunc(p, rt.Handler)
		}
	}
	mux.HandleFunc("/api/", func(rw http.ResponseWriter, r *http.Request) {
		route.WriteError(rw, route.Coded(http.StatusNotFound, "server.NO_OP", "no op at %s %s", r.Method, r.URL.Path))
	})
	front := s.Frontend
	if front == nil {
		front = Frontend()
	}
	served := map[string]bool{}
	for _, e := range exts {
		for _, rt := range e.Routes {
			served[rt.Op] = true
		}
	}
	mux.Handle("GET "+DocsPrefix, gzipped(&specBrowser{ws: w, served: served}))
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
