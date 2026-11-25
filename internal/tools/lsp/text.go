package lsp

import (
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/aiviarium/heron/internal/core/resolve"
)

// stdScheme names the embedded standard files: heron:/std/doc/package.heron
// is the identity "std:doc/package.heron".
const stdScheme = "heron:/std/"

// fileID turns a document URI into the file's one canonical identity,
// whatever spelling reached it: percent-escapes, a "localhost" host, `.`
// and `..` segments, symlinks and trailing slashes all collapse. Other
// schemes (an untitled buffer) keep the URI as their identity.
func fileID(uri string) resolve.FileID {
	if strings.HasPrefix(uri, stdScheme) {
		return resolve.FileID("std:" + strings.TrimPrefix(uri, stdScheme))
	}
	if strings.HasPrefix(uri, "heron:std/") {
		return resolve.FileID("std:" + strings.TrimPrefix(uri, "heron:std/"))
	}
	u, err := url.Parse(uri)
	if err != nil || !strings.EqualFold(u.Scheme, "file") {
		if !strings.Contains(uri, ":") && filepath.IsAbs(uri) {
			if id, err := resolve.Canonical(uri); err == nil {
				return id
			}
		}
		return resolve.FileID(uri)
	}
	p := u.Path
	if p == "" {
		p = u.Opaque
	}
	if u.Host != "" && u.Host != "localhost" {
		p = "//" + u.Host + p
	}
	id, err := resolve.Canonical(filepath.FromSlash(p))
	if err != nil {
		return resolve.FileID(uri)
	}
	return id
}

// fileURI is the canonical URI of a file identity.
func fileURI(id resolve.FileID) string {
	if id.IsStd() {
		return stdScheme + strings.TrimPrefix(string(id), "std:")
	}
	s := string(id)
	if !filepath.IsAbs(s) {
		return s // a URI kept as its own identity
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(s)}
	return u.String()
}

// lines indexes a text's line starts for conversions between byte offsets
// and LSP positions, whose characters count UTF-16 code units.
type lines struct {
	src    string
	starts []int
}

func newLines(src string) *lines {
	l := &lines{src: src, starts: []int{0}}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			l.starts = append(l.starts, i+1)
		}
	}
	return l
}

// pos converts a byte offset to a position.
func (l *lines) pos(off int) Position {
	if off < 0 {
		off = 0
	}
	if off > len(l.src) {
		off = len(l.src)
	}
	line := sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > off }) - 1
	start := l.starts[line]
	return Position{Line: line, Character: utf16Len(l.src[start:off])}
}

func (l *lines) rng(start, end int) Range { return Range{Start: l.pos(start), End: l.pos(end)} }

// offset converts a position to a byte offset, clamping to the line's end
// and the text's end.
func (l *lines) offset(p Position) int {
	if p.Line < 0 {
		return 0
	}
	if p.Line >= len(l.starts) {
		return len(l.src)
	}
	i := l.starts[p.Line]
	end := len(l.src)
	if p.Line+1 < len(l.starts) {
		end = l.starts[p.Line+1] - 1
	}
	units := 0
	for i < end && units < p.Character {
		r, n := utf8.DecodeRuneInString(l.src[i:])
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
		i += n
	}
	return i
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// applyChange applies one content change to a text.
func applyChange(src string, ch ContentChange) string {
	if ch.Range == nil {
		return ch.Text
	}
	l := newLines(src)
	s, e := l.offset(ch.Range.Start), l.offset(ch.Range.End)
	if e < s {
		s, e = e, s
	}
	return src[:s] + ch.Text + src[e:]
}
