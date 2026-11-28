package docs

import (
	"strings"

	uistrings "github.com/aiviarium/heron/web/strings"
)

// TagCall is a markup tag being rendered: its rendered content and its
// attributes. A renderer registered for a tag's kind sees it on Ctx.Tag,
// with Ctx.Node nil.
type TagCall struct {
	Kind  string
	Body  string
	Attrs map[string]string
	Block bool
}

type tagFunc func(body string, attrs map[string]string) (string, bool)

// tagRenderer returns how a tag of a kind renders: a registered renderer
// first, then the built-in components of std/doc.
func (s *Site) tagRenderer(kind string) tagFunc {
	if r := s.Opts.Registry; r != nil {
		if x := r.Renderer(kind); x != nil && x.Name != "docs" {
			return func(body string, attrs map[string]string) (string, bool) {
				c := s.newCtx(nil, nil, 2)
				c.Tag = &TagCall{Kind: kind, Body: body, Attrs: attrs}
				if err := x.Render(c); err != nil {
					return `<span class="ref-broken">` + esc(err.Error()) + `</span>`, false
				}
				return c.out.String(), c.Tag.Block
			}
		}
	}
	short := kind
	if i := strings.LastIndex(kind, "."); i >= 0 {
		short = kind[i+1:]
	}
	switch short {
	case "note", "tip", "warning", "danger":
		k := short
		return func(body string, a map[string]string) (string, bool) {
			kk := k
			if v := a["kind"]; v != "" {
				kk = strings.ToLower(v)
			}
			return `<aside class="callout callout-` + attr(kk) + `">` + s.themeIcon(kk) + `<div class="prose"><p>` + body + `</p></div></aside>`, true
		}
	case "kbd":
		return func(body string, _ map[string]string) (string, bool) { return `<kbd>` + body + `</kbd>`, false }
	case "em":
		return func(body string, _ map[string]string) (string, bool) { return `<em>` + body + `</em>`, false }
	case "strong":
		return func(body string, _ map[string]string) (string, bool) { return `<strong>` + body + `</strong>`, false }
	case "strike":
		return func(body string, _ map[string]string) (string, bool) { return `<s>` + body + `</s>`, false }
	case "span":
		return func(body string, a map[string]string) (string, bool) {
			id := ""
			if n := a["name"]; n != "" {
				id = ` id="` + attr(n) + `"`
			}
			return `<span` + id + `>` + body + `</span>`, false
		}
	case "details":
		return func(body string, _ map[string]string) (string, bool) {
			return `<details class="details"><div class="prose"><p>` + body + `</p></div></details>`, true
		}
	}
	if strings.HasPrefix(kind, "doc.") {
		// std/doc's kinds a Markdown form builds render as that form does.
		switch short {
		case "p":
			return func(body string, _ map[string]string) (string, bool) { return `<p>` + body + `</p>`, true }
		case "code":
			return func(body string, a map[string]string) (string, bool) {
				if a["lang"] != "" || a["title"] != "" {
					return s.codeBlock(a["lang"], a["title"], htmlUnescape(stripTags(body))), true
				}
				return `<code>` + body + `</code>`, false
			}
		case "quote":
			return func(body string, _ map[string]string) (string, bool) {
				return `<blockquote><p>` + body + `</p></blockquote>`, true
			}
		case "list":
			return func(body string, a map[string]string) (string, bool) {
				if a["ordered"] == "true" {
					return `<ol>` + body + `</ol>`, true
				}
				return `<ul>` + body + `</ul>`, true
			}
		case "item":
			return func(body string, _ map[string]string) (string, bool) { return `<li>` + body + `</li>`, true }
		case "table":
			return func(body string, _ map[string]string) (string, bool) {
				return `<div class="table-wrap"><table>` + body + `</table></div>`, true
			}
		case "row":
			return func(body string, _ map[string]string) (string, bool) { return `<tr>` + body + `</tr>`, true }
		case "cell":
			return func(body string, _ map[string]string) (string, bool) { return `<td>` + body + `</td>`, true }
		case "divider":
			return func(string, map[string]string) (string, bool) { return `<hr>`, true }
		case "link":
			return func(body string, a map[string]string) (string, bool) {
				return `<a class="ext" href="` + attr(a["uri"]) + `">` + body + `</a>`, false
			}
		}
	}
	return nil
}

// islandStrings are the catalogue strings the islands show.
func islandStrings() map[string]string {
	out := map[string]string{}
	for k, v := range uistrings.All() {
		if strings.HasPrefix(k, "docs.island.") {
			out[strings.TrimPrefix(k, "docs.island.")] = v
		}
	}
	return out
}
