// Package icon is the Go side of icons: std/docs's `svg_icon` nodes, the
// `@icon("name")` marks on nodes and kinds, and the checks on them. The
// checker, the docs generator and the language server read the same
// model, so a name the checker accepts is one the docs can draw.
package icon

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/types"
	"github.com/aiviarium/heron/std"
)

// The std declarations the model reads.
const (
	KindIcon = "docs.svg_icon"
)

// Icon is an icon a package declares.
type Icon struct {
	Name string
	Node *resolve.Node
	// ViewBox and Body are the SVG's viewBox and its inner markup, or ""
	// with Err set when the file can't be read as one SVG element.
	ViewBox string
	Body    string
	Err     string
}

// SVG is the icon as inline SVG in currentColor, sized to the text and
// hidden from assistive technology.
func (i *Icon) SVG(class string) string {
	if i == nil || i.Body == "" {
		return ""
	}
	return `<svg class="` + class + `" viewBox="` + i.ViewBox + `" width="1em" height="1em" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">` + i.Body + `</svg>`
}

// NameOf is an icon node's name: its node's name with each `_` as `-`.
func NameOf(n *resolve.Node) string { return strings.ReplaceAll(n.Name, "_", "-") }

// Set is the icons a tree can name.
type Set struct {
	byName map[string]*Icon
}

// Names returns the icons' names, sorted.
func (s *Set) Names() []string {
	var out []string
	for n := range s.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Get returns the icon of a name, or nil.
func (s *Set) Get(name string) *Icon {
	if s == nil {
		return nil
	}
	return s.byName[name]
}

// The set of the tree read last: the checker, the docs and the language
// server ask for one tree's icons many times in a row.
var (
	mu       sync.Mutex
	lastTree *resolve.Tree
	lastSet  *Set
)

// Of returns the icons a tree can name: the tree's own and those of every
// package it imports, a tree's own shadowing an imported one.
func Of(t *resolve.Tree, c *types.Checker) *Set {
	mu.Lock()
	defer mu.Unlock()
	if t == lastTree && lastSet != nil {
		return lastSet
	}
	s := &Set{byName: map[string]*Icon{}}
	add := func(n *resolve.Node, own bool) {
		if !isIcon(c, n) {
			return
		}
		name := NameOf(n)
		if old := s.byName[name]; old != nil && !own {
			return
		}
		s.byName[name] = read(n)
	}
	var walk func(n *resolve.Node, own bool)
	walk = func(n *resolve.Node, own bool) {
		add(n, own)
		for _, ch := range n.Children {
			walk(ch, own)
		}
	}
	for _, p := range t.Imports {
		if p.Tree != nil {
			walk(p.Tree.Root, false)
		}
	}
	walk(t.Root, true)
	lastTree, lastSet = t, s
	return s
}

// isIcon reports whether a node is an `icon`.
func isIcon(c *types.Checker, n *resolve.Node) bool {
	if n == nil || n.Name == "" {
		return false
	}
	if c != nil && project.KindPath(c, n) == KindIcon {
		return true
	}
	// A package's node the tree's checker hasn't classified: by the kind
	// its declaration names.
	return declKind(n) == "svg_icon" && n.Child("file") != nil
}

var declKindRe = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_]*\s*:\s*([A-Za-z_][A-Za-z0-9_.]*)`)

func declKind(n *resolve.Node) string {
	if n.Decl.N == nil {
		return ""
	}
	m := declKindRe.FindStringSubmatch(n.Decl.N.Text())
	if m == nil {
		return ""
	}
	k := m[1]
	if i := strings.LastIndex(k, "."); i >= 0 {
		k = k[i+1:]
	}
	return k
}

func fieldString(n *resolve.Node, name string) string {
	f := n.Child(name)
	if f == nil || f.Value == nil {
		return ""
	}
	v, _ := resolve.StringValue(f.Value.Syntax())
	return v
}

func read(n *resolve.Node) *Icon {
	ic := &Icon{Name: NameOf(n), Node: n}
	rel := fieldString(n, "file")
	if rel == "" {
		ic.Err = "it has no file"
		return ic
	}
	b, err := readFile(n, rel)
	if err != nil {
		ic.Err = "its file " + rel + " can't be read"
		return ic
	}
	vb, body, err := parseSVG(b)
	if err != nil {
		ic.Err = "its file " + rel + " " + err.Error()
		return ic
	}
	ic.ViewBox, ic.Body = vb, body
	return ic
}

// readFile reads a path relative to the file that declares a node: from
// the embedded standard packages, or from disk.
func readFile(n *resolve.Node, rel string) ([]byte, error) {
	if n.Decl.File == nil {
		return nil, fs.ErrNotExist
	}
	id := n.Decl.File.ID
	if id.IsStd() {
		dir := strings.TrimPrefix(id.Dir(), "std:")
		return fs.ReadFile(std.FS, path.Clean(path.Join(dir, rel)))
	}
	if filepath.IsAbs(rel) {
		return os.ReadFile(rel)
	}
	return os.ReadFile(filepath.Join(id.Dir(), filepath.FromSlash(rel)))
}

// parseSVG reads one SVG element: its viewBox and its inner markup.
func parseSVG(b []byte) (viewBox, body string, err error) {
	d := xml.NewDecoder(strings.NewReader(string(b)))
	for {
		tok, err := d.Token()
		if err != nil {
			return "", "", fmt.Errorf("isn't an SVG element")
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "svg" {
			return "", "", fmt.Errorf("isn't an SVG element")
		}
		for _, a := range se.Attr {
			if a.Name.Local == "viewBox" {
				viewBox = a.Value
			}
		}
		if viewBox == "" {
			return "", "", fmt.Errorf("has no viewBox")
		}
		break
	}
	s := string(b)
	open := strings.Index(s, "<svg")
	start := strings.Index(s[open:], ">")
	end := strings.LastIndex(s, "</svg>")
	if open < 0 || start < 0 || end < 0 {
		return "", "", fmt.Errorf("isn't an SVG element")
	}
	body = strings.TrimSpace(s[open+start+1 : end])
	// One line, without the indentation, so it inlines compactly.
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return viewBox, strings.Join(lines, ""), nil
}

// Mark returns the name a node's own `@icon("…")` gives, and where it's
// written.
func Mark(c *types.Checker, n *resolve.Node) (string, resolve.Site, bool) {
	for _, a := range n.Annotations {
		if name, ok := markName(c, a); ok {
			return name, a, true
		}
	}
	return "", resolve.Site{}, false
}

func markName(c *types.Checker, a resolve.Site) (string, bool) {
	an := ast.Annotation{N: a.N}
	if an.N == nil || an.Path().N == nil {
		return "", false
	}
	if d := c.Refs[an.Path().N]; d != nil {
		if p := d.Path(); p != "core.icon" && p != "schema.icon" {
			return "", false
		}
	} else if segs := resolve.PathSegs(an.Path().N); len(segs) != 1 || segs[0] != "icon" {
		return "", false
	}
	v, ok := project.ArgString(an.Args(), 0, "name")
	return v, ok
}

// Name returns the icon a node shows: its own mark's, or else its kind's.
func Name(c *types.Checker, n *resolve.Node) string {
	if v, _, ok := Mark(c, n); ok {
		return v
	}
	for k, i := c.KindOf(n), 0; k != nil && i < 8; k, i = c.Extends(k), i+1 {
		if v, _, ok := Mark(c, k); ok {
			return v
		}
	}
	return ""
}

// Check reports the tree's own icon marks that name no icon, and the
// tree's own icons whose SVG can't be read.
func Check(t *resolve.Tree, c *types.Checker) diag.List {
	var ds diag.List
	var set *Set
	t.Walk(func(n *resolve.Node) {
		if n.Decl.File == nil || n.Decl.File.ID.IsStd() || project.PackageOf(n) != nil {
			return
		}
		if set == nil {
			set = Of(t, c)
		}
		if isIcon(c, n) {
			if ic := set.Get(NameOf(n)); ic != nil && ic.Node == n && ic.Err != "" {
				errAt(&ds, n.Decl, "icon", "%s: %s", ic.Name, ic.Err)
			}
		}
		name, site, ok := Mark(c, n)
		if !ok || set.Get(name) != nil {
			return
		}
		hint := ""
		if near := Nearest(set.Names(), name, 3); len(near) > 0 {
			hint = "; did you mean " + strings.Join(near, ", ") + "?"
		} else if len(set.byName) == 0 {
			hint = "; no package the tree imports declares icons: import std/docs for its set"
		}
		errAt(&ds, site, "icon", "no icon %s%s", name, hint)
	})
	return ds
}

// Nearest returns up to n names closest to a name by edit distance.
func Nearest(names []string, name string, n int) []string {
	type cand struct {
		s string
		d int
	}
	var cs []cand
	for _, x := range names {
		d := distance(x, name)
		if d <= max(2, len(name)/3) || strings.Contains(x, name) || strings.Contains(name, x) {
			cs = append(cs, cand{x, d})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].d < cs[j].d || cs[i].d == cs[j].d && cs[i].s < cs[j].s })
	var out []string
	for i := 0; i < len(cs) && i < n; i++ {
		out = append(out, cs[i].s)
	}
	return out
}

func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func errAt(ds *diag.List, s resolve.Site, code, format string, args ...any) {
	start, end := s.Span()
	file := ""
	if s.File != nil {
		file = string(s.File.ID)
	}
	ds.Errorf(file, start, end, code, "%s", fmt.Sprintf(format, args...))
}
