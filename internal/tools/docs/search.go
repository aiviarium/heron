package docs

import (
	"encoding/json"
	"strings"

	"github.com/aiviarium/heron/internal/core/resolve"
)

// The search index and the hover cards are built at generation time and
// written as one script, data.js, so search works from plain files.

// entry is one searchable node.
type entry struct {
	Title string `json:"t"`
	Kind  string `json:"k"`
	Path  string `json:"p"`
	URL   string `json:"u"`
	Page  string `json:"g,omitempty"`
	Text  string `json:"x,omitempty"`
}

// card is a hover card: a term's definition or a type's signature.
type card struct {
	Title string `json:"t"`
	Kind  string `json:"k"`
	HTML  string `json:"h"`
	URL   string `json:"u"`
}

func (s *Site) hasCard(n *resolve.Node) bool {
	return own(n) && (s.kindPath(n) == "doc.term" || s.isTypeDecl(n))
}

func (s *Site) card(n *resolve.Node) card {
	c := card{Title: displayName(s, n), URL: s.URL(n)}
	if k := s.Checker.KindOf(n); k != nil {
		c.Kind = k.Name
	}
	if s.kindPath(n) == "doc.term" {
		c.Title = termHeading(s, n)
		c.HTML = s.proseHTML(n)
	} else {
		c.HTML = `<pre><code>` + s.signature(n) + `</code></pre>`
		if d := s.proseHTML(n); d != "" {
			c.HTML += d
		}
	}
	return c
}

// Search returns the search index: every documented node with an anchor,
// by name and text.
func (s *Site) Search() []entry {
	var out []entry
	add := func(n *resolve.Node, title string) {
		u := s.URL(n)
		if u == "" {
			return
		}
		e := entry{Title: title, Path: n.Path(), URL: u}
		if k := s.Checker.KindOf(n); k != nil {
			e.Kind = k.Name
		} else if n.Parent != nil && s.isTypeDecl(n.Parent) {
			e.Kind = "field"
		} else if n.Shape == resolve.ShapeScoped {
			e.Kind = "section"
		}
		if p := s.anchorOf[n]; p != nil && p.Node != n {
			e.Page = p.Title
		}
		text := s.plainText(n)
		if s.kindPath(n) == "spec.rule" || s.kindPath(n) == "spec.quality" {
			text = strings.Join(strings.Fields(stripTags(s.sentence(n))), " ") + " " + text
		}
		if len(text) > 240 {
			text = text[:240]
		}
		e.Text = strings.TrimSpace(text)
		out = append(out, e)
	}
	s.Tree.Walk(func(n *resolve.Node) {
		if !own(n) || s.hidden(n) || n.Name == "" || n.Shape == resolve.ShapeSet || n.Shape == resolve.ShapeValue && !(n.Parent != nil && s.isTypeDecl(n.Parent)) {
			return
		}
		switch k := s.kindPath(n); {
		case k == "doc.term":
			add(n, termHeading(s, n))
		case k == "core.task" || k == "spec.project" || k == "types.example":
			add(n, displayName(s, n))
		case k != "" || s.pageOf[n] != nil || n.Parent != nil && s.isTypeDecl(n.Parent):
			title := displayName(s, n)
			if n.Parent != nil && s.isTypeDecl(n.Parent) {
				title = n.Parent.Name + "." + n.Name
			}
			add(n, title)
		}
	})
	return out
}

// dataJS writes the search index and the hover cards.
func (s *Site) dataJS() []byte {
	cards := map[string]card{}
	s.Tree.Walk(func(n *resolve.Node) {
		if s.hasCard(n) {
			cards[n.Path()] = s.card(n)
		}
	})
	idx, _ := json.Marshal(s.Search())
	cj, _ := json.Marshal(cards)
	strs, _ := json.Marshal(islandStrings())
	return []byte("window.__HERON__={search:" + string(idx) + ",cards:" + string(cj) + ",strings:" + string(strs) + "};\n")
}
