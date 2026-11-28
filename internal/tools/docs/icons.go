package docs

import (
	"github.com/aiviarium/heron/internal/core/icon"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// Icons: a node's own `@icon("…")`, or its kind's, drawn inline from the
// icons the tree's packages declare. Each page carries the icons it shows
// and data.js the ones search shows, and no other.

// The theme's own icons: one per callout, the same on every site, by the
// names std/docs's set gives them.
var themeIcons = map[string]string{
	"note":    "info",
	"tip":     "lightbulb",
	"warning": "triangle-alert",
	"danger":  "octagon-alert",
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

// nodeIcon is the icon a node shows: its own mark's, or its kind's. In a
// lens that names kinds by their icons, it stands for the kind's name:
// labelled with it, and shown as a tooltip.
func (s *Site) nodeIcon(n *resolve.Node, class string) string {
	if n == nil {
		return ""
	}
	name, _, own := icon.Mark(s.Checker, n)
	if !own {
		if !s.kindIcons() {
			return ""
		}
		name = icon.Name(s.Checker, n)
	}
	ic := s.iconSet().Get(name)
	if k := s.kindName(n); k != "" {
		return ic.Labelled(class, k)
	}
	return ic.SVG(class)
}

// kindIcons reports whether the lens marks a node's kind with its icon.
func (s *Site) kindIcons() bool { return s.Opts.Lens == nil || !s.Opts.Lens.NoKindIcons }

// gutterLink is the link to a node's anchor that stands in the gutter
// beside a node a page defines inline: the icon it shows, muted, with its
// kind and name as tooltip and label, or a quiet "#" where the lens marks
// no kind or the node shows no icon.
func (s *Site) gutterLink(n *resolve.Node, href string) string {
	name := displayName(s, n)
	k := s.kindName(n)
	tip := name
	if k != "" {
		tip = k + " · " + name
	}
	if s.kindIcons() {
		if ic := s.iconSet().Get(icon.Name(s.Checker, n)); ic != nil && ic.Body != "" {
			return `<a class="rule-link gutter-mark" href="` + attr(href) + `" title="` + attr(tip) + `" aria-label="` + attr(tip) + `">` + ic.SVG("icon") + `</a>`
		}
	}
	return `<a class="rule-link" href="` + attr(href) + `" aria-label="` + attr(name) + `">#</a>`
}

// kindName is the name of a node's kind, or "".
func (s *Site) kindName(n *resolve.Node) string {
	if k := s.Checker.KindOf(n); k != nil {
		return k.Name
	}
	return ""
}

// kindMark names a node's kind before its name: its icon, labelled with
// the kind's name, where it shows one. No page names a kind in words.
// pre is what goes before the name, post what goes after.
func (s *Site) kindMark(n *resolve.Node) (pre, post string) {
	k := s.kindName(n)
	if k == "" {
		return "", ""
	}
	if !s.kindIcons() {
		return "", ""
	}
	if ic := s.iconSet().Get(icon.Name(s.Checker, n)); ic != nil && ic.Body != "" {
		return ic.Labelled("icon kind-mark", k), ""
	}
	return "", ""
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
