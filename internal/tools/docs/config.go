package docs

import (
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// Config is what a tree's `docs { … }` block (std/docs) sets: the theme,
// the layout of each section, and the slots.
type Config struct {
	// Node is the docs node, or nil when the tree doesn't import std/docs.
	Node *resolve.Node
	// Theme is the theme package as written, such as
	// "npm:@acme/heron-theme@2" or "./theme".
	Theme string
	// ThemeDir is the folder of the file that names the theme, which a
	// relative theme and node_modules are found from.
	ThemeDir string
	// Layouts maps a section, api or scope to the layout its page takes.
	Layouts map[*resolve.Node]string
	Slots   []*Slot
	// Depth is how many levels of sections get pages of their own.
	Depth int
}

// Slot is a `slot(name, of?)` of the docs block: components a place shows.
type Slot struct {
	Name string
	// Of is the kind path whose renderer the slot is in, or "" for a
	// place on every page.
	Of   string
	Node *resolve.Node
}

// Layouts a `layout(name)` can choose; "docs" is the default section
// layout.
var layoutNames = map[string]string{
	"docs":      "section",
	"section":   "section",
	"api":       "api",
	"changelog": "changelog",
	"landing":   "landing",
}

// Slot places on every page; a kind's slots are "before", "after" and any
// name its renderer places with Ctx.Slot.
var pageSlots = map[string]bool{"header": true, "sidebar": true, "before": true, "after": true}

// readConfig reads the docs node of a tree.
func (s *Site) readConfig() {
	s.Config = Config{Layouts: map[*resolve.Node]string{}, Depth: 2}
	d := s.Tree.Root.Child("docs")
	if d == nil || s.kindPath(d) != "docs.site" {
		return
	}
	s.Config.Node = d
	for _, c := range d.Children {
		switch {
		case c.Name == "theme" && c.Value != nil:
			s.Config.Theme, _ = resolve.StringValue(c.Value.Syntax())
			if c.Decl.File != nil {
				s.Config.ThemeDir = c.Decl.File.ID.Dir()
			}
		case c.Name == "depth" && c.Value != nil:
			if v, err := strconv.Atoi(strings.TrimSpace(c.Value.Syntax().Text())); err == nil && v > 0 {
				s.Config.Depth = v
			}
		case s.kindPath(c) == "docs.layout":
			name, _ := argString(kindArgs(c), 0, "name")
			l := layoutNames[name]
			if l == "" {
				continue
			}
			sec := c.Child("sections")
			if sec == nil {
				continue
			}
			arr, ok := sec.Value.(ast.ArrayExpr)
			if !ok {
				continue
			}
			for _, e := range arr.Exprs() {
				if t := s.refTarget(e.Syntax(), sec.ValueSite); t != nil {
					s.Config.Layouts[t] = l
				}
			}
		case s.kindPath(c) == "docs.slot":
			args := kindArgs(c)
			name, _ := argString(args, 0, "name")
			sl := &Slot{Name: name, Node: c}
			if e := namedArg(args, "of"); e != nil {
				if k := s.refTarget(e.Syntax(), c.Decl); k != nil {
					sl.Of = k.Path()
				}
			}
			if name == "" || sl.Of == "" && !pageSlots[name] {
				continue
			}
			s.Config.Slots = append(s.Config.Slots, sl)
		}
	}
}

// layoutOf returns the layout a section's page takes: its own from the
// docs block, or that of the nearest ancestor that sets one.
func (s *Site) layoutOf(n *resolve.Node, def string) string {
	if l := s.Config.Layouts[n]; l != "" {
		return l
	}
	return def
}

// slots returns the slots of a name: a page's (of "") or a kind's.
func (s *Site) slots(name, of string) []*Slot {
	var out []*Slot
	for _, sl := range s.Config.Slots {
		if sl.Name == name && sl.Of == of {
			out = append(out, sl)
		}
	}
	return out
}

// slotHTML renders the components of the slots of a name, each through
// the renderer of its kind, wrapped in an element the theme can place.
func (s *Site) slotHTML(c *Ctx, name, of string) string {
	ss := s.slots(name, of)
	if len(ss) == 0 {
		return ""
	}
	d := s.newCtx(nil, nil, 3)
	if c != nil {
		d = c.at(nil, c.Level+1)
		d.out = &strings.Builder{}
	}
	cls := "slot slot-" + attr(name)
	d.W(`<div class="`, cls, `"`)
	if of != "" {
		d.W(` data-of="`, attr(of), `"`)
	}
	d.W(`>`)
	for _, sl := range ss {
		if h := s.proseHTML(sl.Node); h != "" {
			d.W(`<div class="prose">`, h, `</div>`)
		}
		for _, ch := range sl.Node.Children {
			if ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue {
				continue
			}
			if err := d.at(ch, d.Level).render(); err != nil {
				d.W(`<span class="ref-broken">`, esc(err.Error()), `</span>`)
			}
		}
	}
	d.W(`</div>`)
	return d.out.String()
}

// inConfig reports whether a node is written inside the docs block.
func (s *Site) inConfig(n *resolve.Node) bool {
	if s.Config.Node == nil {
		return false
	}
	for x := n; x != nil; x = x.Parent {
		if x == s.Config.Node {
			return true
		}
	}
	return false
}
