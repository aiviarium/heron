package docs

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Diagrams are laid out here at generation time and written as SVG whose
// colours are the theme's tokens (classes the stylesheet colours), so they
// read without a script; the diagram island adds pan, zoom, highlighting
// and opening a node on click.

// graph is a laid-out directed graph.
type graph struct {
	nodes []*gnode
	edges []*gedge
}

type gnode struct {
	id, label, href string
	cls             string // "initial", "terminal" or ""
	layer, row      int
	x, y, w, h      float64
}

type gedge struct {
	from, to *gnode
	label    string   // the transition's name
	sub      []string // the ops that perform it
	href     string
	cls      string // "unperformed" or ""
}

const (
	boxH     = 32.0
	layerGap = 150.0
	rowGap   = 64.0
	pad      = 24.0
	charW    = 7.4
)

func textW(s string) float64 { return float64(utf8.RuneCountInString(s))*charW + 28 }

// layout places nodes in layers by distance from the roots, rows in order
// of appearance.
func (g *graph) layout(roots []*gnode) (w, h float64) {
	out := map[*gnode][]*gnode{}
	for _, e := range g.edges {
		out[e.from] = append(out[e.from], e.to)
	}
	for _, n := range g.nodes {
		n.layer = -1
	}
	queue := append([]*gnode(nil), roots...)
	for _, r := range roots {
		r.layer = 0
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, m := range out[n] {
			if m.layer < 0 {
				m.layer = n.layer + 1
				queue = append(queue, m)
			}
		}
	}
	maxLayer := 0
	for _, n := range g.nodes {
		if n.layer > maxLayer {
			maxLayer = n.layer
		}
	}
	for _, n := range g.nodes {
		if n.layer < 0 {
			maxLayer++
			n.layer = maxLayer
		}
	}
	// Column widths and rows.
	colW := make([]float64, maxLayer+1)
	rows := make([]int, maxLayer+1)
	for _, n := range g.nodes {
		n.w = textW(n.label)
		n.h = boxH
		if n.w > colW[n.layer] {
			colW[n.layer] = n.w
		}
		n.row = rows[n.layer]
		rows[n.layer]++
	}
	maxRows := 0
	for _, r := range rows {
		if r > maxRows {
			maxRows = r
		}
	}
	x := pad + 18
	colX := make([]float64, maxLayer+1)
	for i := range colW {
		colX[i] = x
		x += colW[i] + layerGap
	}
	w = x - layerGap + pad
	h = pad*2 + float64(maxRows)*boxH + float64(maxRows-1)*rowGap
	for _, e := range g.edges {
		if e.to.layer <= e.from.layer && e.from != e.to {
			h += 52
			break
		}
	}
	for _, n := range g.nodes {
		// Centre each column's rows vertically.
		offset := (float64(maxRows-rows[n.layer]) * (boxH + rowGap)) / 2
		n.x = colX[n.layer] + (colW[n.layer]-n.w)/2
		n.y = pad + offset + float64(n.row)*(boxH+rowGap)
	}
	return w, h
}

// svg writes the laid-out graph.
func (g *graph) svg(id, title string, w, h float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="graph" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" role="img" aria-labelledby="%s-title" data-graph="%s">`, w, h, w, h, attr(id), attr(id))
	fmt.Fprintf(&b, `<title id="%s-title">%s</title>`, attr(id), esc(title))
	b.WriteString(`<defs><marker id="` + attr(id) + `-arrow" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0 L8 4 L0 8 z" class="arrowhead"/></marker>`)
	b.WriteString(`<marker id="` + attr(id) + `-arrow-warn" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0 L8 4 L0 8 z" class="arrowhead warn"/></marker></defs>`)
	// Edges first, so boxes sit on top.
	pairs := map[[2]*gnode]int{}
	for _, e := range g.edges {
		k := [2]*gnode{e.from, e.to}
		if e.from == e.to {
			continue
		}
		idx := pairs[k]
		pairs[k]++
		g.edge(&b, id, e, idx)
	}
	for _, n := range g.nodes {
		cls := "state"
		if n.cls != "" {
			cls += " " + n.cls
		}
		open, closeA := "", ""
		if n.href != "" {
			open, closeA = `<a href="`+attr(n.href)+`">`, `</a>`
		}
		fmt.Fprintf(&b, `<g class="%s" data-node="%s">%s`, cls, attr(n.id), open)
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="%s"/>`, n.x, n.y, n.w, n.h, radiusFor(n))
		if n.cls == "terminal" {
			fmt.Fprintf(&b, `<rect class="inner" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="4"/>`, n.x+3, n.y+3, n.w-6, n.h-6)
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" dominant-baseline="central">%s</text>`, n.x+n.w/2, n.y+n.h/2, esc(n.label))
		b.WriteString(closeA + `</g>`)
		if n.cls == "initial" {
			// The initial state is entered from a dot.
			fmt.Fprintf(&b, `<circle class="start" cx="%.1f" cy="%.1f" r="5"/><path class="edge" d="M%.1f %.1f H%.1f" marker-end="url(#%s-arrow)"/>`, n.x-22, n.y+n.h/2, n.x-17, n.y+n.h/2, n.x-2, attr(id))
		}
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func radiusFor(n *gnode) string {
	if n.cls == "initial" {
		return "16"
	}
	return "6"
}

// edge draws one edge: forward edges as curves from the right side to the
// left side, edges back or within a layer as arcs below the boxes.
func (g *graph) edge(b *strings.Builder, id string, e *gedge, idx int) {
	cls := "edge"
	marker := id + "-arrow"
	if e.cls != "" {
		cls += " " + e.cls
		marker = id + "-arrow-warn"
	}
	f, t := e.from, e.to
	var d string
	var lx, ly float64
	if t.layer > f.layer {
		x1, y1 := f.x+f.w, f.y+f.h/2
		x2, y2 := t.x, t.y+t.h/2
		y1 -= float64(idx) * 8
		mx := (x1 + x2) / 2
		d = fmt.Sprintf("M%.1f %.1f C%.1f %.1f %.1f %.1f %.1f %.1f", x1, y1, mx, y1, mx, y2, x2, y2)
		lx, ly = mx, (y1+y2)/2-8-13*float64(len(e.sub))
	} else {
		x1, y1 := f.x+f.w/2, f.y+f.h
		x2, y2 := t.x+t.w/2, t.y+t.h
		dip := 34.0 + float64(idx)*14
		bottom := maxf(y1, y2) + dip
		d = fmt.Sprintf("M%.1f %.1f C%.1f %.1f %.1f %.1f %.1f %.1f", x1, y1, x1, bottom, x2, bottom, x2, y2+2)
		lx, ly = (x1+x2)/2, bottom-dip/4+2
	}
	open, closeA := "", ""
	if e.href != "" {
		open, closeA = `<a href="`+attr(e.href)+`">`, `</a>`
	}
	fmt.Fprintf(b, `<g class="link" data-from="%s" data-to="%s"><path class="%s" d="%s" marker-end="url(#%s)"/>%s`, attr(f.id), attr(t.id), cls, d, attr(marker), open)
	fmt.Fprintf(b, `<text class="edge-label%s" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, map[bool]string{true: " warn", false: ""}[e.cls != ""], lx, ly, esc(e.label))
	for i, s := range e.sub {
		y := ly + 13 + float64(i)*13
		fmt.Fprintf(b, `<text class="edge-sub" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, lx, y, esc(s))
	}
	b.WriteString(closeA + `</g>`)
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// lifecycleSVG draws a lifecycle as a state diagram.
func (s *Site) lifecycleSVG(l *lifecycle, id string) string {
	g := &graph{}
	byName := map[string]*gnode{}
	variantHref := func(v string) string {
		if l.field != nil {
			if et := s.Checker.TypeOf(l.field); et != nil {
				if vn := et.Child(v); vn != nil {
					return s.URL(vn)
				}
			}
		}
		return ""
	}
	for _, st := range l.states {
		n := &gnode{id: st, label: st, href: variantHref(st)}
		if st == l.initial {
			n.cls = "initial"
		} else if l.terminal[st] {
			n.cls = "terminal"
		}
		byName[st] = n
		g.nodes = append(g.nodes, n)
	}
	for _, t := range l.transitions {
		f, to := byName[t.from], byName[t.to]
		if f == nil || to == nil {
			continue
		}
		e := &gedge{from: f, to: to, label: t.name, href: "#" + Anchor(t.node)}
		for _, op := range t.ops {
			e.sub = append(e.sub, op.Name)
		}
		if len(t.ops) == 0 {
			e.cls = "unperformed"
		}
		g.edges = append(g.edges, e)
	}
	var roots []*gnode
	if r := byName[l.initial]; r != nil {
		roots = append(roots, r)
	} else if len(g.nodes) > 0 {
		roots = append(roots, g.nodes[0])
	}
	w, h := g.layout(roots)
	return g.svg(id, displayName(s, l.node), w, h)
}
