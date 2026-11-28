package lsp

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
	"github.com/aiviarium/heron/std"
)

// completion offers what fits where the cursor is, from the schemas in
// scope: a body's fields, kinds and types after `:`, annotations after
// `@`, flags after `<`, members after `.`, packages and files in import
// and include strings, and in a kind's references the nodes of the kinds
// it declares they name. In prose it completes only after `$`, in a link's
// `(#…)` and after `<`; a plain word never completes.
func (s *Session) completion(params json.RawMessage) (any, error) {
	var p CompletionParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	id := fileID(p.TextDocument.URI)
	src, ok := s.text(id)
	if !ok {
		return nil, &rpcError{Code: codeInvalidParams, Message: "no document " + p.TextDocument.URI}
	}
	l := newLines(src)
	off := l.offset(p.Position)
	f := s.file(id)
	if f == nil {
		return CompletionList{Items: []CompletionItem{}}, nil
	}
	r := s.resultFor(id)
	c := &completer{s: s, r: r, c: r.completed().Checker, f: f, src: src, off: off, l: l}
	items := c.items()
	// Never an empty label, never twice the same one.
	out := []CompletionItem{}
	seen := map[string]bool{}
	for _, it := range items {
		if strings.TrimSpace(it.Label) == "" || seen[it.Label] {
			continue
		}
		seen[it.Label] = true
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SortText+out[i].Label < out[j].SortText+out[j].Label })
	return CompletionList{Items: out}, nil
}

type completer struct {
	s   *Session
	r   *result
	c   *types.Checker
	f   *resolve.File
	src string
	off int
	l   *lines
}

func isWordByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

var linkDest = regexp.MustCompile(`\]\(#([A-Za-z0-9_.]*)$`)

// shortLinkStart is a short link being typed: `[`, a backtick or not, and
// a path so far.
var shortLinkStart = regexp.MustCompile(`(?:^|[^\\\]])\[` + "`?" + `([A-Za-z0-9_.]*)$`)

// closesShortLink reports whether what follows the cursor's word ends a
// short link, `]` not followed by `(`, or the line: `[the order](#x)`'s
// text is a link's words, not a name.
func (c *completer) closesShortLink() bool {
	o := c.off
	for o < len(c.src) && (isWordByte(c.src[o]) || c.src[o] == '.' || c.src[o] == '`') {
		o++
	}
	if o >= len(c.src) || c.src[o] == '\n' || c.src[o] == '\r' {
		return true
	}
	return c.src[o] == ']' && (o+1 >= len(c.src) || c.src[o+1] != '(')
}

func (c *completer) items() []CompletionItem {
	// The word being typed, and what stands before it.
	ps := c.off
	for ps > 0 && isWordByte(c.src[ps-1]) {
		ps--
	}
	prefix := c.src[ps:c.off]
	lineStart := strings.LastIndexByte(c.src[:ps], '\n') + 1
	before := c.src[lineStart:ps]
	_, anc := tokenContaining(c.f.Tree.Root, max(c.off-1, 0))
	edit := func(label string) *TextEdit {
		return &TextEdit{Range: c.l.rng(ps, c.off), NewText: label}
	}
	_ = prefix

	// Strings of import and include directives.
	for i, a := range anc {
		if a.Kind != syntax.StringLit {
			continue
		}
		if dir := directiveOf(anc[i:]); dir != "" {
			return c.directiveItems(dir, a)
		}
		if inIconMark(c.src, a) {
			return c.iconItems(a)
		}
		return nil
	}

	if inProse(anc) {
		trimmed := strings.TrimRight(before, " ")
		switch {
		case strings.HasSuffix(before, "$") || strings.HasSuffix(before, "${"):
			return c.names(c.scope(anc), func(resolve.Binding) bool { return true }, edit)
		case linkDest.MatchString(c.src[lineStart:c.off]):
			m := linkDest.FindStringSubmatch(c.src[lineStart:c.off])[1]
			return c.pathItems(anc, m, edit)
		case shortLinkStart.MatchString(c.src[lineStart:c.off]) && c.closesShortLink():
			m := shortLinkStart.FindStringSubmatch(c.src[lineStart:c.off])[1]
			return c.pathItems(anc, m, edit)
		case strings.HasSuffix(before, "<") || strings.HasSuffix(before, "</"):
			return c.names(c.scope(anc), func(b resolve.Binding) bool { return b.Node != nil && c.c.IsKind(b.Node) }, edit)
		}
		_ = trimmed
		return nil
	}

	trimmed := strings.TrimRight(before, " \t")
	switch {
	case strings.HasSuffix(before, "@"):
		return c.names(c.scope(anc), func(b resolve.Binding) bool { return b.Node != nil && c.c.IsAnnotation(b.Node) }, edit)
	case strings.HasSuffix(before, "<") || inFlags(anc):
		return c.names(c.scope(anc), func(b resolve.Binding) bool { return b.Node != nil && c.c.IsFlag(b.Node) }, edit)
	case strings.HasSuffix(before, "."):
		head := before[:len(before)-1]
		hs := len(head)
		for hs > 0 && (isWordByte(head[hs-1]) || head[hs-1] == '.') {
			hs--
		}
		return c.pathItems(anc, head[hs:]+".", edit)
	case strings.HasSuffix(trimmed, ":") || inType(anc):
		parent := c.enclosing(anc)
		return c.names(c.scope(anc), func(b resolve.Binding) bool {
			if b.Node == nil {
				return false
			}
			if c.c.IsKind(b.Node) {
				return parent == nil || parent.Shape == resolve.ShapeRoot || c.c.Open(parent) || c.c.Accepts(parent, b.Node) || !c.c.StructureOnly(b.Node)
			}
			return c.c.IsType(b.Node) || resolve.IsBuiltin(b.Node)
		}, edit)
	case strings.TrimSpace(before) == "":
		// The start of a member: the fields the enclosing node's schema
		// declares, then the names visible here.
		var out []CompletionItem
		if n := c.enclosing(anc); n != nil {
			for _, fd := range c.c.Fields(n) {
				it := CompletionItem{Label: fd.Name, Kind: ciField, Detail: fd.TypeName(), SortText: "0", TextEdit: edit(fd.Name)}
				if fd.Decl != nil {
					if d := docText(fd.Decl); d != "" {
						it.Documentation = &MarkupContent{Kind: "markdown", Value: d}
					}
				}
				out = append(out, it)
			}
		}
		return append(out, c.names(c.scope(anc), func(b resolve.Binding) bool {
			return b.Local != nil || b.Node != nil && (c.c.IsKind(b.Node) || !c.isMeta(b.Node))
		}, edit)...)
	}
	if kinds, types, ok := c.referenceKinds(anc); ok {
		// A positional reference of a kind that declares the kinds its
		// references name: those nodes, and the sections a path to one
		// passes through; and types too where an op's result type may
		// come first.
		return c.names(c.scope(anc), func(b resolve.Binding) bool {
			if b.Node == nil {
				return false
			}
			if types && c.c.IsType(b.Node) {
				return true
			}
			k := c.c.KindOf(b.Node)
			if k == nil {
				return !c.isMeta(b.Node) && !c.c.IsKind(b.Node) && !c.c.IsType(b.Node) && len(b.Node.Children) > 0
			}
			if k.Name == "section" {
				return true
			}
			for x, i := k, 0; x != nil && i < 8; x, i = c.c.Extends(x), i+1 {
				if c.c.Fits(x, kinds) {
					return true
				}
			}
			return false
		}, edit)
	}
	return c.names(c.scope(anc), func(b resolve.Binding) bool { return b.Local != nil || b.Node != nil && !c.isMeta(b.Node) }, edit)
}

// referenceKinds are the kinds and categories a reference may name where
// the cursor stands among the positional arguments of a kind slot whose
// kind declares `references`, and whether a type fits there too, as an
// op's result type does; ok is false anywhere else.
func (c *completer) referenceKinds(anc []*syntax.Node) (kinds []*resolve.Node, types bool, ok bool) {
	for i, a := range anc {
		switch a.Kind {
		case syntax.NamedArg:
			return nil, false, false
		case syntax.Args:
			if i+1 >= len(anc) || anc[i+1].Kind != syntax.NamedType && anc[i+1].Kind != syntax.Invoke {
				return nil, false, false
			}
			p := childOf(anc[i+1], syntax.Path)
			if p == nil {
				return nil, false, false
			}
			k := c.c.Refs[p]
			if k == nil {
				segs := resolve.PathSegs(p)
				if sc := c.scope(anc); sc != nil && len(segs) > 0 {
					sc.Each(segs[len(segs)-1], c.f, -1, func(b resolve.Binding) bool {
						if b.Node != nil && c.c.IsKind(b.Node) {
							k = b.Node
							return false
						}
						return true
					})
				}
			}
			if k == nil || !c.c.IsKind(k) {
				return nil, false, false
			}
			kinds, ok := c.c.ReferenceKinds(k)
			if !ok {
				return nil, false, false
			}
			// A type fits where an optional parameter that takes no
			// reference comes first, as an op's result type.
			ps := c.c.Params(k)
			pos := 0
			for _, x := range (ast.Args{N: a}).Args() {
				if _, isPos := x.(ast.PositionalArg); !isPos {
					continue
				}
				if _, e := x.Syntax().Span(); e < c.off {
					pos++
				}
			}
			types := pos == 0 && len(ps) > 0 && ps[0].Optional && !ps[0].Variadic && ps[0].Name == "returns"
			return kinds, types, true
		}
	}
	return nil, false, false
}

// isMeta reports whether a node is a schema declaration rather than a
// value an expression names.
func (c *completer) isMeta(n *resolve.Node) bool {
	return c.c.IsFlag(n) || c.c.IsAnnotation(n) || c.c.IsCategory(n)
}

func inProse(anc []*syntax.Node) bool {
	for _, a := range anc {
		switch a.Kind {
		case syntax.Insert, syntax.StringLit:
			return false
		case syntax.ProseLines, syntax.ProseBlock, syntax.Doc, syntax.ProseFile, syntax.ProseLine, syntax.DocLine:
			return true
		case syntax.Code, syntax.StructBody, syntax.HeronFence:
			return false
		}
	}
	return false
}

func inFlags(anc []*syntax.Node) bool {
	return len(anc) > 0 && anc[0].Kind == syntax.Flags
}

func inType(anc []*syntax.Node) bool {
	for _, a := range anc {
		switch a.Kind {
		case syntax.NamedType:
			return true
		case syntax.Path:
			continue
		default:
			return false
		}
	}
	return false
}

// directiveOf names the directive a string is an entry of: "import" or
// "include".
func directiveOf(anc []*syntax.Node) string {
	for _, a := range anc {
		if a.Kind == syntax.Set {
			if p := childOf(a, syntax.Path); p != nil {
				switch t := resolve.PathText(p); t {
				case "import", "include":
					return t
				}
			}
			return ""
		}
	}
	return ""
}

// enclosing returns the node whose body holds the cursor.
func (c *completer) enclosing(anc []*syntax.Node) *resolve.Node {
	sites := c.r.sites()
	for _, a := range anc {
		if !isMember(a.Kind) && a.Kind != syntax.Invoke {
			continue
		}
		body := childOf(a, syntax.StructBody)
		if body == nil {
			continue
		}
		s, e := body.Span()
		if c.off <= s || c.off >= e {
			continue
		}
		if n := sites[a]; n != nil {
			return n
		}
	}
	if sc := c.r.res.Tree.FileScope(c.f.ID); sc != nil && sc.Node != nil {
		return sc.Node
	}
	return c.r.res.Tree.Root
}

func (c *completer) scope(anc []*syntax.Node) *resolve.Scope {
	return c.s.scopeAt(c.r, c.f, anc)
}

// names lists the names visible from a scope that pass a filter.
func (c *completer) names(sc *resolve.Scope, keep func(resolve.Binding) bool, edit func(string) *TextEdit) []CompletionItem {
	if sc == nil {
		return nil
	}
	var out []CompletionItem
	sc.Names(func(name string, b resolve.Binding) {
		if b.Local != nil && b.Local.Site.File == c.f && b.Local.Offset > c.off {
			return
		}
		if !keep(b) {
			return
		}
		out = append(out, c.item(name, b, edit))
	})
	return out
}

func (c *completer) item(name string, b resolve.Binding, edit func(string) *TextEdit) CompletionItem {
	it := CompletionItem{Label: name, TextEdit: edit(name), SortText: "1"}
	if b.Local != nil {
		it.Kind, it.Detail = ciVariable, "let"
		if b.Local.Param {
			it.Detail = "parameter"
		}
		it.SortText = "0"
		return it
	}
	n := b.Node
	switch {
	case c.c.IsKind(n):
		it.Kind, it.Detail = ciClass, "kind"
	case c.c.IsAnnotation(n):
		it.Kind, it.Detail = ciEvent, "annotation"
	case c.c.IsFlag(n):
		it.Kind, it.Detail = ciTypeParam, "flag"
	case c.c.IsEnum(n):
		it.Kind, it.Detail = ciEnum, "enum"
	case c.c.IsInterface(n):
		it.Kind, it.Detail = ciInterface, "interface"
	case c.c.IsType(n) || resolve.IsBuiltin(n):
		it.Kind, it.Detail = ciStruct, "type"
	default:
		it.Kind = ciReference
		if k := c.c.KindOf(n); k != nil {
			it.Detail = k.Name
			switch k.Name {
			case "func", "task", "op", "macro":
				it.Kind = ciFunction
			case "section":
				it.Kind = ciModule
			}
		} else if t := c.c.TypeOf(n); t != nil {
			it.Kind, it.Detail = ciValue, t.Name
		}
		it.SortText = "0"
	}
	if d := docText(n); d != "" {
		it.Documentation = &MarkupContent{Kind: "markdown", Value: d}
	}
	return it
}

// pathItems completes a dotted path: the children of what its head names,
// or the names in scope when it has no dot yet.
func (c *completer) pathItems(anc []*syntax.Node, path string, edit func(string) *TextEdit) []CompletionItem {
	sc := c.scope(anc)
	segs := strings.Split(path, ".")
	if len(segs) <= 1 {
		return c.names(sc, func(b resolve.Binding) bool { return b.Node != nil && !c.isMeta(b.Node) }, edit)
	}
	segs = segs[:len(segs)-1]
	if sc == nil {
		return nil
	}
	b := sc.Lookup(segs[0], c.f, -1)
	n := b.Node
	for _, sg := range segs[1:] {
		if n == nil {
			break
		}
		n = n.Child(sg)
	}
	if n == nil {
		return nil
	}
	var out []CompletionItem
	for _, ch := range n.AllChildren() {
		if ch.Name != "" {
			out = append(out, c.item(ch.Name, resolve.Binding{Node: ch}, edit))
		}
	}
	for _, fd := range c.c.Fields(n) {
		if n.Child(fd.Name) == nil {
			out = append(out, CompletionItem{Label: fd.Name, Kind: ciField, Detail: fd.TypeName(), TextEdit: edit(fd.Name), SortText: "1"})
		}
	}
	return out
}

// directiveItems completes an import or include string: the standard
// packages and the files and folders beside the importing file.
func (c *completer) directiveItems(dir string, lit *syntax.Node) []CompletionItem {
	s, _ := lit.Span()
	start := s + 1
	for start < len(c.src) && start < c.off && c.src[start] == '"' {
		start++
	}
	if start > c.off {
		return nil
	}
	typed := c.src[start:c.off]
	rng := c.l.rng(start, c.off)
	var out []CompletionItem
	if dir == "import" && !strings.HasPrefix(typed, ".") && !strings.HasPrefix(typed, "/") {
		entries, _ := fs.ReadDir(std.FS, ".")
		for _, e := range entries {
			if e.IsDir() {
				label := "std/" + e.Name() + "@1"
				out = append(out, CompletionItem{Label: label, Kind: ciModule, Detail: "standard package", TextEdit: &TextEdit{Range: rng, NewText: label}})
			}
		}
		return out
	}
	if c.f.ID.IsStd() || !filepath.IsAbs(string(c.f.ID)) {
		return nil
	}
	// Files and folders relative to the importing file's own directory.
	base := filepath.Dir(string(c.f.ID))
	relDir := typed
	if i := strings.LastIndexByte(typed, '/'); i >= 0 {
		relDir = typed[:i+1]
	} else {
		relDir = ""
	}
	if relDir == "" {
		for _, p := range []string{"./", "../"} {
			out = append(out, CompletionItem{Label: p, Kind: ciFolder, TextEdit: &TextEdit{Range: rng, NewText: p}})
		}
		return out
	}
	entries, err := os.ReadDir(filepath.Join(base, filepath.FromSlash(relDir)))
	if err != nil {
		return nil
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		label := relDir + name
		kind := ciFile
		if e.IsDir() {
			label += "/"
			kind = ciFolder
		} else if !strings.HasSuffix(name, ".heron") && !strings.HasSuffix(name, ".heron.md") {
			continue
		}
		out = append(out, CompletionItem{Label: label, Kind: kind, TextEdit: &TextEdit{Range: rng, NewText: label}})
	}
	return out
}
