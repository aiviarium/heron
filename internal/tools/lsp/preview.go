package lsp

import (
	"encoding/json"
	"fmt"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/tools/docs"
)

// previewParams is what heron/preview asks for.
type previewParams struct {
	URI      string   `json:"uri"`
	Position Position `json:"position"`
	Styles   bool     `json:"styles,omitempty"`
}

// previewPage is heron/preview's answer.
type previewPage struct {
	Path   string `json:"path"`
	HTML   string `json:"html"`
	Error  string `json:"error,omitempty"`
	Styles string `json:"styles,omitempty"`
}

// preview answers heron/preview: the node at the position, or the file's
// top-level nodes, rendered by the docs renderer from the open documents.
func (s *Session) preview(params json.RawMessage) (any, error) {
	var p previewParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	id := fileID(p.URI)
	src, ok := s.text(id)
	if !ok {
		return nil, &rpcError{Code: codeInvalidParams, Message: "no file " + p.URI}
	}
	out := &previewPage{}
	if p.Styles {
		for _, a := range docs.Assets() {
			if a.Path == "docs.css" {
				out.Styles = string(a.Content)
			}
		}
	}
	res := s.resultFor(id).completed()
	n := nodeAt(res, id, newLines(src).offset(p.Position))
	if n != nil {
		out.Path = n.Path()
	}
	html, err := renderPreview(s, res, id, n)
	if err != nil {
		out.Error = err.Error()
		return out, nil
	}
	out.HTML = html
	return out, nil
}

// renderPreview renders a node as its docs page shows it, or the file's
// nodes when n is nil; a renderer that panics fails the render.
func renderPreview(s *Session, res *check.Result, file resolve.FileID, n *resolve.Node) (html string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	site := docs.New(res.Tree, res.Checker, docs.Options{Registry: s.opts.Registry})
	var f *docs.Fragment
	if n != nil {
		f, err = site.RenderNode(n)
	} else {
		f, err = site.RenderFile(file)
	}
	if err != nil {
		return "", err
	}
	if f.Aside == "" {
		return f.HTML, nil
	}
	return f.HTML + `<aside class="h-preview-aside">` + f.Aside + `</aside>`, nil
}

// nodeAt returns the innermost node with a kind declared in a file at an
// offset.
func nodeAt(res *check.Result, file resolve.FileID, off int) *resolve.Node {
	var best *resolve.Node
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, c := range n.Children {
			s, e, ok := declaredInFile(c, file)
			if ok && s <= off && off <= e && hasKind(res, c) {
				best = c
			}
			walk(c)
		}
	}
	walk(res.Tree.Root)
	return best
}

// declaredInFile reports whether a node is declared in a file, at what span:
// a node only entered or amended there counts at its first site in it.
func declaredInFile(n *resolve.Node, file resolve.FileID) (int, int, bool) {
	for _, site := range append([]resolve.Site{n.Decl}, n.Sites...) {
		if site.File != nil && site.File.ID == file && site.N != nil {
			s, e := site.Span()
			return s, e, true
		}
	}
	return 0, 0, false
}

func hasKind(res *check.Result, n *resolve.Node) bool {
	c := res.Checker
	return c.KindOf(n) != nil || c.IsEnum(n) || c.IsType(n)
}
