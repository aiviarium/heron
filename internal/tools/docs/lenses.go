package docs

import (
	"strings"

	"github.com/aiviarium/heron/internal/core/types"

	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// A site renders one lens of the tree: what the lens hides has no page,
// no anchor and no search entry, what it sums up renders as one line, and
// a link to a node the lens hides goes to the first lens that shows it.

// lensOf is the lens a site renders; nil renders everything, as explore.
func (s *Site) lensOf() *lens.Lens { return s.Opts.Lens }

// explore reports whether the site shows everything.
func (s *Site) explore() bool { return s.Opts.Lens.Explore() }

// mode is how the site's lens renders a node by its own kind and @lens.
func (s *Site) mode(n *resolve.Node) lens.Mode {
	if s.explore() || n == nil {
		return lens.Show
	}
	if m, ok := s.modes[n]; ok {
		return m
	}
	m := s.Opts.Lens.Mode(s.Checker, n)
	s.modes[n] = m
	return m
}

// marked reports whether the lens marks on and above a node leave it,
// with its subtree, out of the lens.
func (s *Site) marked(n *resolve.Node) bool {
	if s.explore() || n == nil {
		return false
	}
	return s.Opts.Lens.Marked(s.Checker, n)
}

// kept reports whether the lens renders a node itself, in full or as a
// summary line.
func (s *Site) kept(n *resolve.Node) bool { return s.mode(n) != lens.Hide }

// placed reports whether a node has a place in the lens: it's kept, or it
// holds a node that is, and then shows as that node's heading and page
// without its own prose.
func (s *Site) placed(n *resolve.Node) bool {
	if s.explore() || n == nil {
		return true
	}
	if v, ok := s.places[n]; ok {
		return v
	}
	s.places[n] = false
	v := false
	if !s.marked(n) {
		if s.kept(n) {
			v = true
		} else {
			for _, ch := range n.Children {
				if ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue || !own(ch) {
					continue
				}
				if s.placed(ch) {
					v = true
					break
				}
			}
		}
	}
	s.places[n] = v
	return v
}

// hidden reports whether a node has no place in the lens.
func (s *Site) hidden(n *resolve.Node) bool { return !s.placed(n) }

// summarized reports whether the lens sums a node up in one line.
func (s *Site) summarized(n *resolve.Node) bool { return s.mode(n) == lens.Summarize }

// runShown reports whether one prose run of a node shows in the lens: a
// run marked for the lens shows, one marked for others doesn't, and an
// unmarked one shows with its node when the lens keeps the node.
func (s *Site) runShown(n *resolve.Node, site resolve.Site) bool {
	if s.explore() {
		return true
	}
	r := lens.ProseRestriction(s.Checker, site)
	if r != nil {
		return s.Opts.Lens.Allows(r) && (n == nil || !s.marked(n))
	}
	return n == nil || s.kept(n)
}

// authored reports whether a node is a guide, how-to, concept or tutorial.
func (s *Site) authored(n *resolve.Node) bool { return lens.IsAuthored(s.Checker, n) }

// hasContent reports whether a node has a place in the lens.
func (s *Site) hasContent(n *resolve.Node) bool { return s.placed(n) }

// lensTag is the title of the lens a link leads to, when it's another.
func (s *Site) lensTag(n *resolve.Node) string {
	if s.lenses == nil || s.kept(n) && s.anchorOf[n] != nil {
		return ""
	}
	if o := s.siblingFor(n); o != nil {
		return o.Opts.Lens.Title
	}
	return ""
}

// lensRel is the path from one lens's folder to another's: up to the
// site's root, then down into the other's prefix.
func lensRel(from, to *lens.Lens) string {
	var b strings.Builder
	if from != nil && from.Prefix != "" {
		for range strings.Split(from.Prefix, "/") {
			b.WriteString("../")
		}
	}
	if to != nil && to.Prefix != "" {
		b.WriteString(to.Prefix + "/")
	}
	return b.String()
}

// siblingFor is the first other lens of the site's list that renders a
// node itself.
func (s *Site) siblingFor(n *resolve.Node) *Site {
	if s.lenses == nil {
		return nil
	}
	for _, o := range s.lenses.ordered {
		if o != s && o.anchorOf[n] != nil && o.kept(n) {
			return o
		}
	}
	return nil
}

// siblingURL is the link to a node the site's lens leaves out, in the
// first lens of the site's list that renders it.
func (s *Site) siblingURL(n *resolve.Node) string {
	if o := s.siblingFor(n); o != nil {
		return lensRel(s.Opts.Lens, o.Opts.Lens) + o.localURL(n)
	}
	return ""
}

// lensHref is the link from a page to the same place in another lens:
// the node's page there, the page it's shown on, a gathered page of the
// same name, or that lens's home.
func (s *Site) lensHref(p *Page, o *Site) string {
	rel := lensRel(s.Opts.Lens, o.Opts.Lens)
	if p.Node != nil && p != s.Home {
		if u := o.localURL(p.Node); u != "" {
			return rel + u
		}
		for a := p.Node.Parent; a != nil; a = a.Parent {
			if q := o.pageOf[a]; q != nil && q != o.Home {
				return rel + o.href(q.File)
			}
		}
	}
	if q := o.PageByFile(p.File); q != nil {
		return rel + o.href(q.File)
	}
	return rel + o.href(o.Home.File)
}

// Lenses is every lens of a site, each rendered as a site of its own from
// the same tree, so a page can switch lens and a link can go to a lens
// that shows what this one hides.
type Lenses struct {
	Model *lens.Site
	// sites has the default lens first; ordered is the docs block's
	// order, which the switcher and cross-lens links follow.
	sites   []*Site
	ordered []*Site
}

// Sites returns the lens sites, the default one first.
func (l *Lenses) Sites() []*Site { return l.sites }

// Default returns the site of the default lens.
func (l *Lenses) Default() *Site { return l.sites[0] }

// NewLenses prepares a site per lens the tree's docs block chooses, the
// default one first; without a choice, explore alone.
func NewLenses(t *resolve.Tree, c *types.Checker, opts Options) *Lenses {
	model := lens.SiteOf(t, c)
	ls := &Lenses{Model: model}
	order := []*lens.Lens{model.Default}
	for _, l := range model.Lenses {
		if l != model.Default {
			order = append(order, l)
		}
	}
	byLens := map[*lens.Lens]*Site{}
	for _, l := range order {
		o := opts
		o.Lens = l
		o.Assets = lensRel(l, nil) + "_assets/"
		o.Data = "data.js"
		s := New(t, c, o)
		s.lenses = ls
		ls.sites = append(ls.sites, s)
		byLens[l] = s
	}
	for _, l := range model.Lenses {
		ls.ordered = append(ls.ordered, byLens[l])
	}
	return ls
}

// Files renders every lens: the default one at the root with the shared
// assets, each other under its prefix with its own search index.
func (l *Lenses) Files() ([]File, error) {
	var out []File
	for i, s := range l.sites {
		files, err := s.files(i == 0)
		if err != nil {
			return nil, err
		}
		pre := ""
		if s.Opts.Lens != nil && s.Opts.Lens.Prefix != "" {
			pre = s.Opts.Lens.Prefix + "/"
		}
		for _, f := range files {
			f.Path = pre + f.Path
			out = append(out, f)
		}
	}
	return out, nil
}

// markTags are the titles of the lenses a mark names, as explore shows
// them beside a marked node or prose run.
func (s *Site) markTags(r []*resolve.Node) string {
	if !s.explore() || r == nil {
		return ""
	}
	var b strings.Builder
	for _, l := range r {
		b.WriteString(` <span class="lens-mark">` + esc(lens.Read(s.Checker, l).Title) + `</span>`)
	}
	return b.String()
}

// kindTag is a node's kind and its lens marks beside its name, in
// explore.
func (s *Site) kindTag(n *resolve.Node) string {
	if !s.explore() || n == nil || s.Opts.Lens == nil {
		return ""
	}
	out := ""
	if k := s.Checker.KindOf(n); k != nil {
		out = ` <span class="kind">` + esc(k.Name) + `</span>`
	}
	return out + s.markTags(lens.Restriction(s.Checker, n))
}
