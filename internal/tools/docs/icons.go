package docs

import (
	"github.com/aiviarium/heron/internal/core/icon"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// Icons: a node's own `@icon("…")`, or its kind's, drawn inline from the
// icons the tree's packages declare. Each page carries the icons it shows
// and data.js the ones search shows, and no other.

// The theme's own icons: one per callout and per conformance status, the
// same on every site, by the names std/docs's set gives them.
var themeIcons = map[string]string{
	"note":      "info",
	"tip":       "lightbulb",
	"warning":   "triangle-alert",
	"danger":    "octagon-alert",
	"proven":    "circle-check",
	"planned":   "circle-dashed",
	"untested":  "circle-help",
	"missing":   "circle-x",
	"practice":  "users",
	"delivered": "circle-check",
}

func (s *Site) iconSet() *icon.Set {
	if s.icons == nil {
		s.icons = icon.Of(s.Tree, s.Checker)
	}
	return s.icons
}

// namedIcon is the icon of a name as inline SVG, or "".
func (s *Site) namedIcon(name, class string) string {
	if name == "" {
		return ""
	}
	return s.iconSet().Get(name).SVG(class)
}

// themeIcon is the theme's icon for a callout's or a status's name.
func (s *Site) themeIcon(key string) string {
	return s.namedIcon(themeIcons[key], "icon")
}

// ownIcon is the icon a node's own mark names, as the navigation shows it.
func (s *Site) ownIcon(n *resolve.Node) string {
	if n == nil {
		return ""
	}
	name, _, ok := icon.Mark(s.Checker, n)
	if !ok {
		return ""
	}
	return s.namedIcon(name, "icon")
}

// nodeIcon is the icon a node shows: its own mark's, or its kind's.
func (s *Site) nodeIcon(n *resolve.Node, class string) string {
	if n == nil {
		return ""
	}
	return s.namedIcon(icon.Name(s.Checker, n), class)
}

// iconName is the name of the icon a node shows, or "".
func (s *Site) iconName(n *resolve.Node) string {
	if n == nil || s.iconSet().Get(icon.Name(s.Checker, n)) == nil {
		return ""
	}
	return icon.Name(s.Checker, n)
}

// nodeByAnchor finds the node an anchor on the site names.
func (s *Site) nodeByAnchor(id string) *resolve.Node {
	if s.anchors == nil {
		s.anchors = map[string]*resolve.Node{}
		s.Tree.Walk(func(n *resolve.Node) {
			if own(n) && n.Name != "" {
				if _, ok := s.anchors[Anchor(n)]; !ok {
					s.anchors[Anchor(n)] = n
				}
			}
		})
	}
	return s.anchors[id]
}
