package docs

import (
	"strconv"
	"strings"

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

// siteIcons are the icons of the site's own pages, by their layouts.
var siteIcons = map[string]string{
	"features":    "puzzle",
	"diagnostics": "circle-alert",
	"glossary":    "book-a",
	"gaps":        "circle-dashed",
}

// navBlank keeps an icon's space before a name in the navigation.
const navBlank = `<span class="icon nav-blank" aria-hidden="true"></span>`

// navIcon is the icon an entry of the navigation shows before its name: its
// node's own mark's, at any depth; an authored page's kind's, as its title
// shows it; or the icon of one of the site's own pages. Any other kind's
// icon never shows there.
func (s *Site) navIcon(q *Page) string {
	if ic, ok := s.navIcons[q]; ok {
		return ic
	}
	if s.navIcons == nil {
		s.navIcons = map[*Page]string{}
	}
	ic := s.pageNavIcon(q)
	s.navIcons[q] = ic
	return ic
}

func (s *Site) pageNavIcon(q *Page) string {
	if q.Layout == "op" {
		return ""
	}
	if q.Node != nil {
		if ic := s.ownIcon(q.Node); ic != "" {
			return ic
		}
		if authoredKinds[s.kindPath(q.Node)] && s.kindIcons() {
			return s.namedIcon(icon.Name(s.Checker, q.Node), "icon")
		}
		return ""
	}
	if q.icon != "" {
		return q.icon
	}
	return s.namedIcon(siteIcons[q.Layout], "icon")
}

// anyNavIcon reports whether an entry of a list of the navigation shows an
// icon.
func (s *Site) anyNavIcon(ps []*Page) bool {
	for _, q := range ps {
		if s.navIcon(q) != "" {
			return true
		}
	}
	return false
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

// nongoalMark is the icon of the goal kind beside a nongoal's kind, struck
// through by a diagonal from corner to corner, or "" where no icon is set.
func (s *Site) nongoalMark(n *resolve.Node) string {
	k := s.Checker.KindOf(n)
	if k == nil || k.Parent == nil {
		return ""
	}
	goal := k.Parent.Child("goal")
	if goal == nil {
		return ""
	}
	name, _, ok := icon.Mark(s.Checker, goal)
	ic := s.iconSet().Get(name)
	if !ok || ic == nil || ic.Body == "" {
		return ""
	}
	struck := *ic
	struck.Body += strike(ic.ViewBox)
	return struck.SVG("icon kind-mark nongoal-mark")
}

// strike is a line across a viewBox, from a twelfth in at its top left to
// a twelfth in at its bottom right, as a "-off" icon draws one.
func strike(viewBox string) string {
	f := strings.Fields(viewBox)
	if len(f) != 4 {
		return ""
	}
	var v [4]float64
	for i, x := range f {
		n, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return ""
		}
		v[i] = n
	}
	num := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	x0, y0, w, h := v[0], v[1], v[2], v[3]
	return `<path d="M` + num(x0+w/12) + ` ` + num(y0+h/12) + `L` + num(x0+w*11/12) + ` ` + num(y0+h*11/12) + `"/>`
}
