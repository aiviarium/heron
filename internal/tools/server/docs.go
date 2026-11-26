package server

import (
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/tools/docs"
)

// DocsPrefix is where the spec browser is served; the preview's links and
// assets point into it.
const DocsPrefix = "/docs/"

func docsSite(res *check.Result, r *registry.Registry, served map[string]bool) *docs.Site {
	return docs.New(res.Tree, res.Checker, docs.Options{
		Registry:  r,
		Href:      func(file string) string { return DocsPrefix + file },
		Assets:    DocsPrefix + "_assets/",
		ServedOps: served,
	})
}

// DocsRenderer renders the preview through the docs renderer, so a node
// looks as it does in the generated docs.
type DocsRenderer struct{ Registry *registry.Registry }

func fragment(f *docs.Fragment) string {
	if f.Aside == "" {
		return f.HTML
	}
	return f.HTML + `<aside class="h-preview-aside">` + f.Aside + `</aside>`
}

// RenderNode renders one node as its docs page or section.
func (d DocsRenderer) RenderNode(res *check.Result, n *resolve.Node) (string, error) {
	f, err := docsSite(res, d.Registry, nil).RenderNode(n)
	if err != nil {
		return "", err
	}
	return fragment(f), nil
}

// RenderFile renders every top-level node of a file.
func (d DocsRenderer) RenderFile(res *check.Result, file resolve.FileID) (string, error) {
	f, err := docsSite(res, d.Registry, nil).RenderFile(file)
	if err != nil {
		return "", err
	}
	return fragment(f), nil
}

// specBrowser serves the docs of the tree on disk, rendered live: the same
// renderer as `heron generate docs`, under DocsPrefix.
type specBrowser struct {
	ws *Workspace
	// served are the ops the server routes, whose pages offer Try it.
	served map[string]bool

	mu   sync.Mutex
	res  *check.Result
	site *docs.Site
}

func (b *specBrowser) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(r.URL.Path, DocsPrefix)), "/")
	if strings.HasPrefix(p, "_assets/") {
		name := strings.TrimPrefix(p, "_assets/")
		for _, a := range docs.Assets() {
			if a.Path == name {
				w.Header().Set("Content-Type", a.Type)
				_, _ = w.Write(a.Content)
				return
			}
		}
	}
	var body []byte
	var ctype string
	err := b.ws.Analyze(nil, func(a *Analysis) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.res != a.Res {
			b.res, b.site = a.Res, docsSite(a.Res, b.ws.Reg, b.served)
		}
		if p == "_assets/data.js" {
			body, ctype = b.site.DataJS(), "text/javascript; charset=utf-8"
			return nil
		}
		if p == "" || p == "." {
			p = "index.html"
		}
		pg := b.site.PageByFile(p)
		if pg == nil {
			return nil
		}
		h, err := b.site.RenderPage(pg)
		body, ctype = []byte(h), "text/html; charset=utf-8"
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if body == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}
