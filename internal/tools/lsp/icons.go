package lsp

import (
	"encoding/base64"
	"regexp"
	"strings"

	"github.com/aiviarium/heron/internal/core/icon"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// An icon mark's string: `@icon("…")` or `@icon(name = "…")`, up to the
// string's opening quote.
var iconMarkBefore = regexp.MustCompile(`@icon\(\s*(?:name\s*=\s*)?$`)

// inIconMark reports whether a string literal is an icon mark's name.
func inIconMark(src string, lit *syntax.Node) bool {
	s, _ := lit.Span()
	return iconMarkBefore.MatchString(src[max(0, s-40):s])
}

// iconItems completes the names of the tree's icons inside an icon mark's
// string, each with its picture.
func (c *completer) iconItems(lit *syntax.Node) []CompletionItem {
	s, _ := lit.Span()
	start := s + 1
	if start > c.off {
		return nil
	}
	rng := c.l.rng(start, c.off)
	res := c.r.completed()
	set := icon.Of(res.Tree, res.Checker)
	var out []CompletionItem
	for _, name := range set.Names() {
		it := CompletionItem{Label: name, Kind: ciValue, Detail: "icon", TextEdit: &TextEdit{Range: rng, NewText: name}}
		if md := iconPreview(set.Get(name)); md != "" {
			it.Documentation = &MarkupContent{Kind: "markdown", Value: md}
		}
		out = append(out, it)
	}
	return out
}

// iconPreview is an icon's picture as Markdown: its SVG as an image.
func iconPreview(ic *icon.Icon) string {
	if ic == nil || ic.Body == "" {
		return ""
	}
	// Drawn in a mid grey, which reads in a light and a dark editor.
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="` + ic.ViewBox + `" width="48" height="48" fill="none" stroke="#888" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + ic.Body + `</svg>`
	return "![" + ic.Name + "](data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)) + ")"
}

// iconHover is the hover of an icon mark's name, or of an icon's
// declaration: the icon's picture and where it's declared.
func iconHover(src string, off int, f *resolve.File, t *resolve.Tree, c *types.Checker) (string, int, int, bool) {
	tok, anc := tokenContaining(f.Tree.Root, off)
	if tok == nil {
		return "", 0, 0, false
	}
	for _, a := range anc {
		if a.Kind != syntax.StringLit || !inIconMark(src, a) {
			continue
		}
		name, _ := resolve.StringValue(a)
		set := icon.Of(t, c)
		ic := set.Get(name)
		s, e := a.Span()
		if ic == nil {
			return "no icon `" + name + "`", s, e, true
		}
		return iconPreview(ic) + "\n\nicon `" + name + "`" + declaredIn(ic), s, e, true
	}
	return "", 0, 0, false
}

func declaredIn(ic *icon.Icon) string {
	if ic.Node == nil || ic.Node.Decl.File == nil {
		return ""
	}
	id := string(ic.Node.Decl.File.ID)
	if strings.HasPrefix(id, "std:") {
		return ", from std/docs"
	}
	return ""
}
