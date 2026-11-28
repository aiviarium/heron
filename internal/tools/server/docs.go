package server

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/tools/docs"
)

// DocsPrefix is where the spec browser is served.
const DocsPrefix = "/docs/"

func docsSite(res *check.Result, r *registry.Registry, served map[string]bool) *docs.Site {
	return docs.New(res.Tree, res.Checker, docs.Options{
		Registry:  r,
		Href:      func(file string) string { return DocsPrefix + file },
		Assets:    DocsPrefix + "_assets/",
		ServedOps: served,
	})
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
				serveCached(w, r, a.Type, a.Content)
				return
			}
		}
	}
	var body []byte
	var ctype string
	err := b.ws.Analyze(func(a *Analysis) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.res != a.Res {
			b.res, b.site = a.Res, docsSite(a.Res, b.ws.Reg, b.served)
		}
		if p == "_assets/data.js" {
			body, ctype = b.site.DataJS(), "text/javascript; charset=utf-8"
			return nil
		}
		if name, ok := strings.CutPrefix(p, "_assets/"); ok {
			// A theme package's files.
			for _, a := range b.site.Assets() {
				if a.Path == name {
					body, ctype = a.Content, a.Type
				}
			}
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
	serveCached(w, r, ctype, body)
}

// serveCached writes a docs response with an ETag of its content, so a
// browser revalidates it with a 304 rather than fetching it again. An
// asset requested with its content's version (?v=) never changes at that
// URL, so it's kept for good.
func serveCached(w http.ResponseWriter, r *http.Request, ctype string, body []byte) {
	sum := sha256.Sum256(body)
	// Weak: the same content is sent gzipped or not.
	tag := `W/"` + hex.EncodeToString(sum[:12]) + `"`
	h := w.Header()
	h.Set("ETag", tag)
	if r.URL.Query().Get("v") != "" && strings.Contains(r.URL.Path, "/_assets/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, tag[2:]) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", ctype)
	_, _ = w.Write(body)
}
