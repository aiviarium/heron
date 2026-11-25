package prose

import (
	"html"
	"strings"
)

// SourceRange maps a range of a region's text to the file's bytes. Exact is
// false when either end falls inside a joined line break, where characters
// don't map one to one.
func SourceRange(r *Region, start, end int) (s, e int, exact bool) {
	if len(r.Map) == 0 {
		return r.Start, r.End, false
	}
	exact = true
	s, ok := srcAt(r, start, false)
	exact = exact && ok
	if end <= start {
		return s, s, exact
	}
	e, ok = srcAt(r, end-1, true)
	return s, e, exact && ok
}

// srcAt maps a character offset to its source byte: the byte itself, or,
// with after, the byte past it.
func srcAt(r *Region, at int, after bool) (int, bool) {
	for _, seg := range r.Map {
		if at < seg.At || at >= seg.At+seg.Len {
			continue
		}
		if seg.Exact && seg.Len == seg.SrcLen {
			off := seg.Src + at - seg.At
			if after {
				off++
			}
			return off, true
		}
		if after {
			return seg.Src + seg.SrcLen, false
		}
		return seg.Src, false
	}
	last := r.Map[len(r.Map)-1]
	if at >= last.At+last.Len {
		return last.Src + last.SrcLen, false
	}
	return r.Map[0].Src, false
}

// Masked reports whether a range of a region's text overlaps a masked span.
func Masked(r *Region, start, end int) bool {
	for _, m := range r.Masks {
		if start < m.End && end > m.Start {
			return true
		}
	}
	return false
}

// HTML is the projection of regions as one HTML document, for linters such
// as Vale that read markup: headings, paragraphs, lists, quotes and code
// blocks keep their elements, and masked spans become <code>.
type HTML struct {
	Text string
	// Segs maps ranges of Text to region text, in order.
	Segs []HTMLSeg
}

// HTMLSeg maps Text[At:At+Len] to Regions[Region].Text[Pos:Pos+N]; an
// escaped character is one N for several bytes.
type HTMLSeg struct {
	At, Len     int
	Region, Pos int
	N           int
}

// RenderHTML renders regions as one HTML document.
func RenderHTML(regions []*Region) *HTML {
	h := &HTML{}
	var b strings.Builder
	write := func(s string) { b.WriteString(s) }
	text := func(ri, from, to int) {
		r := regions[ri]
		for i := from; i < to; {
			j := i
			for j < to && !needsEscape(r.Text[j]) {
				j++
			}
			if j > i {
				h.Segs = append(h.Segs, HTMLSeg{At: b.Len(), Len: j - i, Region: ri, Pos: i, N: j - i})
				write(r.Text[i:j])
				i = j
				continue
			}
			esc := html.EscapeString(r.Text[i : i+1])
			h.Segs = append(h.Segs, HTMLSeg{At: b.Len(), Len: len(esc), Region: ri, Pos: i, N: 1})
			write(esc)
			i++
		}
	}
	write("<html><body>\n")
	list := ""
	for ri, r := range regions {
		inList := r.Block == "item" && ri > 0 && regions[ri-1].Block == "item" && regions[ri-1].Group == r.Group && regions[ri-1].Ordered == r.Ordered
		if list != "" && !inList {
			write("</" + list + ">\n")
			list = ""
		}
		open, close := "<p>", "</p>"
		switch r.Block {
		case "heading":
			lv := r.Level
			if lv < 1 || lv > 6 {
				lv = 1
			}
			tag := "h" + string(rune('0'+lv))
			open, close = "<"+tag+">", "</"+tag+">"
		case "item":
			if list == "" {
				list = "ul"
				if r.Ordered {
					list = "ol"
				}
				write("<" + list + ">\n")
			}
			open, close = "<li>", "</li>"
		case "quote":
			open, close = "<blockquote><p>", "</p></blockquote>"
		case "code":
			open, close = "<pre><code>", "</code></pre>"
		}
		write(open)
		pos := 0
		for _, m := range r.Masks {
			if r.Block == "code" {
				break
			}
			text(ri, pos, m.Start)
			write("<code>")
			text(ri, m.Start, m.End)
			write("</code>")
			pos = m.End
		}
		text(ri, pos, len(r.Text))
		write(close + "\n")
	}
	if list != "" {
		write("</" + list + ">\n")
	}
	write("</body></html>\n")
	h.Text = b.String()
	return h
}

func needsEscape(c byte) bool { return c == '<' || c == '>' || c == '&' || c == '"' || c == '\'' }

// Locate maps an offset of the HTML text to a region and an offset of its
// text; ok is false for markup the renderer added.
func (h *HTML) Locate(off int) (region, pos int, ok bool) {
	lo, hi := 0, len(h.Segs)
	for lo < hi {
		m := (lo + hi) / 2
		if h.Segs[m].At+h.Segs[m].Len <= off {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo == len(h.Segs) || h.Segs[lo].At > off {
		return 0, 0, false
	}
	s := h.Segs[lo]
	if s.N == s.Len {
		return s.Region, s.Pos + off - s.At, true
	}
	return s.Region, s.Pos, true
}
