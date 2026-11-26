package lsp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/format"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// position decodes a position request into a file and a byte offset.
func (s *Session) position(params json.RawMessage, p *TextDocumentPositionParams) (resolve.FileID, int, error) {
	if err := decode(params, p); err != nil {
		return "", 0, err
	}
	id := fileID(p.TextDocument.URI)
	src, ok := s.text(id)
	if !ok {
		return "", 0, &rpcError{Code: codeInvalidParams, Message: "no document " + p.TextDocument.URI}
	}
	return id, newLines(src).offset(p.Position), nil
}

func (s *Session) location(f resolve.FileID, start, end int) Location {
	src, _ := s.text(f)
	return Location{URI: s.uriOf(f), Range: newLines(src).rng(start, end)}
}

// --- hover ---

func (s *Session) hover(params json.RawMessage) (any, error) {
	var p TextDocumentPositionParams
	id, off, err := s.position(params, &p)
	if err != nil {
		return nil, err
	}
	t, r := s.targetAt(id, off)
	src, _ := s.text(id)
	l := newLines(src)
	ts, te := 0, 0
	if t.tok != nil {
		ts, te = t.tok.Span()
	}
	rng := l.rng(ts, te)
	switch {
	case t.file != "":
		return Hover{Contents: MarkupContent{Kind: "markdown", Value: "`" + s.displayID(t.file) + "`"}, Range: &rng}, nil
	case t.local != nil:
		kind := "let"
		if t.local.Param {
			kind = "parameter"
		}
		sig := t.local.Name
		if t.local.Site.N != nil {
			sig = strings.TrimSpace(t.local.Site.N.Text())
		}
		return Hover{Contents: MarkupContent{Kind: "markdown", Value: "```heron\n" + sig + "\n```\n\n" + kind}, Range: &rng}, nil
	case t.node == nil:
		return nil, nil
	}
	n := t.node
	var b strings.Builder
	b.WriteString("```heron\n" + signature(n) + "\n```")
	if d := docText(n); d != "" {
		b.WriteString("\n\n" + d)
	}
	var facts []string
	if k := r.res.Checker.KindOf(n); k != nil {
		facts = append(facts, "kind `"+k.Name+"`")
	}
	if p := n.Path(); p != "" {
		facts = append(facts, "`"+p+"`")
	}
	if site, _, _ := declSpan(n); site.File != nil {
		facts = append(facts, s.displayID(site.File.ID))
	}
	if len(facts) > 0 {
		b.WriteString("\n\n" + strings.Join(facts, " · "))
	}
	return Hover{Contents: MarkupContent{Kind: "markdown", Value: b.String()}, Range: &rng}, nil
}

// displayID names a file in hover text: a standard file as std/…, a file
// under a workspace root by its path relative to it, never by where the
// server keeps it.
func (s *Session) displayID(id resolve.FileID) string {
	if id.IsStd() {
		return "std/" + strings.TrimPrefix(string(id), "std:")
	}
	for _, r := range s.roots {
		if rel, err := filepath.Rel(string(r), string(id)); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return string(id)
}

// --- definition and references ---

func (s *Session) definition(params json.RawMessage) (any, error) {
	var p TextDocumentPositionParams
	id, off, err := s.position(params, &p)
	if err != nil {
		return nil, err
	}
	t, _ := s.targetAt(id, off)
	switch {
	case t.file != "":
		return []Location{{URI: s.uriOf(t.file), Range: Range{}}}, nil
	case t.local != nil:
		if t.local.Site.File == nil {
			return nil, nil
		}
		st, e := t.local.Site.Span()
		return []Location{s.location(t.local.Site.File.ID, st, e)}, nil
	case t.node == nil:
		return nil, nil
	}
	site, st, e := declSpan(t.node)
	if site.File == nil {
		return nil, nil
	}
	return []Location{s.location(site.File.ID, st, e)}, nil
}

// occurrence is one word that names a node: its declaration, an amend or
// a reference.
type occurrence struct {
	file       resolve.FileID
	start, end int
	decl       bool
}

// occurrences finds every word that names a node, across the trees of the
// open documents and the local packages they import.
func (s *Session) occurrences(n *resolve.Node) []occurrence {
	var out []occurrence
	seen := map[occurrence]bool{}
	add := func(o occurrence) {
		k := o
		k.decl = false
		if !seen[k] {
			seen[k] = true
			out = append(out, o)
		}
	}
	for _, r := range s.allResults() {
		res := r.completed()
		for p, x := range res.Checker.Refs {
			ids := pathIdents(p)
			for i := range ids {
				if same(ascend(x, i, len(ids)), n) {
					f := fileOfSyntax(r, p)
					if f != "" {
						st, e := ids[i].Span()
						add(occurrence{file: f, start: st, end: e})
					}
				}
			}
		}
		for m, x := range r.sites() {
			p := childOf(m, syntax.Path)
			if p == nil {
				continue
			}
			ids := pathIdents(p)
			for i := range ids {
				if same(ascend(x, i, len(ids)), n) {
					f := fileOfSyntax(r, p)
					if f != "" {
						st, e := ids[i].Span()
						add(occurrence{file: f, start: st, end: e, decl: x == n && i == len(ids)-1})
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].start < out[j].start
	})
	return out
}

// allResults is every current analysis, plus the checks of the local
// packages they import, so references inside those packages count.
func (s *Session) allResults() []*result {
	var out []*result
	seen := map[resolve.FileID]bool{}
	var add func(r *result)
	add = func(r *result) {
		if seen[r.entry] {
			return
		}
		seen[r.entry] = true
		out = append(out, r)
		for _, t := range r.trees() {
			for _, p := range t.Imports {
				if p.Major == 0 && p.Git == nil && p.Tree != nil && p.Tree.Entry != nil {
					add(s.check(p.Tree.Entry.ID))
				}
			}
		}
	}
	entries := make([]resolve.FileID, 0, len(s.results))
	for e := range s.results {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i] < entries[j] })
	for _, e := range entries {
		add(s.results[e])
	}
	return out
}

// fileOfSyntax finds the file of one of a result's trees that holds a
// syntax node.
func fileOfSyntax(r *result, n *syntax.Node) resolve.FileID {
	root := n
	for root.Parent != nil {
		root = root.Parent
	}
	if r.roots == nil {
		r.roots = map[*syntax.Node]resolve.FileID{}
		for _, t := range r.trees() {
			for _, f := range t.Files {
				r.roots[f.Tree.Root] = f.ID
			}
		}
	}
	return r.roots[root]
}

func (s *Session) references(params json.RawMessage) (any, error) {
	var p ReferenceParams
	id, off, err := s.position(params, &p.TextDocumentPositionParams)
	if err != nil {
		return nil, err
	}
	_ = decode(params, &p)
	t, _ := s.targetAt(id, off)
	if t.node == nil {
		return []Location{}, nil
	}
	out := []Location{}
	for _, o := range s.occurrences(t.node) {
		if o.decl && !p.Context.IncludeDeclaration {
			continue
		}
		out = append(out, s.location(o.file, o.start, o.end))
	}
	return out, nil
}

// --- rename ---

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (s *Session) renameTarget(params json.RawMessage, p *TextDocumentPositionParams) (target, resolve.FileID, error) {
	id, off, err := s.position(params, p)
	if err != nil {
		return target{}, "", err
	}
	t, _ := s.targetAt(id, off)
	if t.node == nil || t.tok == nil || t.tok.Kind != syntax.Ident {
		return t, id, &rpcError{Code: codeInvalidParams, Message: "nothing to rename here"}
	}
	site, _, _ := declSpan(t.node)
	if site.File == nil || site.File.ID.IsStd() || resolve.IsBuiltin(t.node) {
		return t, id, &rpcError{Code: codeInvalidParams, Message: t.node.Name + " is declared in a standard package"}
	}
	return t, id, nil
}

func (s *Session) prepareRename(params json.RawMessage) (any, error) {
	var p TextDocumentPositionParams
	t, id, err := s.renameTarget(params, &p)
	if err != nil {
		return nil, err
	}
	src, _ := s.text(id)
	st, e := t.tok.Span()
	return map[string]any{"range": newLines(src).rng(st, e), "placeholder": t.tok.Text}, nil
}

// rename renames a node by its path: its declaration, every amend and
// every reference, links and inserts in prose included, in every file of
// the open trees.
func (s *Session) rename(params json.RawMessage) (any, error) {
	var p RenameParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	if !identRE.MatchString(p.NewName) {
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("%q isn't a name", p.NewName)}
	}
	t, _, err := s.renameTarget(params, &p.TextDocumentPositionParams)
	if err != nil {
		return nil, err
	}
	if t.node.Parent != nil && t.node.Parent.Child(p.NewName) != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("%s already has a member %s", t.node.Parent.Path(), p.NewName)}
	}
	edit := WorkspaceEdit{Changes: map[string][]TextEdit{}}
	for _, o := range s.occurrences(t.node) {
		loc := s.location(o.file, o.start, o.end)
		edit.Changes[loc.URI] = append(edit.Changes[loc.URI], TextEdit{Range: loc.Range, NewText: p.NewName})
	}
	return edit, nil
}

// --- formatting ---

func (s *Session) formatting(params json.RawMessage) (any, error) {
	var p DocumentFormattingParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	id := fileID(p.TextDocument.URI)
	f := s.file(id)
	if f == nil {
		return []TextEdit{}, nil
	}
	if len(f.Tree.Diags) > 0 {
		return []TextEdit{}, nil // never reformat around a syntax error
	}
	out := format.Tree(f.Tree)
	if out == f.Src {
		return []TextEdit{}, nil
	}
	l := newLines(f.Src)
	return []TextEdit{{Range: l.rng(0, len(f.Src)), NewText: out}}, nil
}

// --- symbols and folding ---

const (
	symModule    = 2
	symNamespace = 3
	symClass     = 5
	symMethod    = 6
	symProperty  = 7
	symField     = 8
	symEnum      = 10
	symInterface = 11
	symFunction  = 12
	symVariable  = 13
	symConstant  = 14
	symString    = 15
	symObject    = 19
	symStruct    = 23
	symEvent     = 24
)

func (s *Session) documentSymbols(params json.RawMessage) (any, error) {
	var p DocumentFormattingParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	id := fileID(p.TextDocument.URI)
	r := s.resultFor(id)
	src, _ := s.text(id)
	l := newLines(src)
	c := r.res.Checker
	var walk func(n *resolve.Node) []DocumentSymbol
	walk = func(n *resolve.Node) []DocumentSymbol {
		out := []DocumentSymbol{}
		for _, ch := range n.Children {
			kids := walk(ch)
			if ch.Decl.File == nil || ch.Decl.File.ID != id || ch.Decl.N == nil || ch.Shape == resolve.ShapeMounted {
				out = append(out, kids...)
				continue
			}
			name := ch.Name
			detail := ""
			if k := c.KindOf(ch); k != nil {
				detail = k.Name
			} else if t := c.TypeOf(ch); t != nil {
				detail = t.Name
			}
			if name == "" {
				name = detail
			}
			if name == "" {
				out = append(out, kids...)
				continue
			}
			st, e := ch.Decl.Span()
			_, ns, ne := declSpan(ch)
			kind := symObject
			switch {
			case c.IsKind(ch):
				kind = symClass
			case c.IsEnum(ch):
				kind = symEnum
			case c.IsInterface(ch):
				kind = symInterface
			case c.IsType(ch):
				kind = symStruct
			case detail == "func" || detail == "task" || detail == "op":
				kind = symFunction
			case ch.Shape == resolve.ShapeValue:
				kind = symProperty
			case ch.Shape == resolve.ShapeHeading || detail == "section":
				kind = symNamespace
			}
			if ns < st || ne > e {
				ns, ne = st, st
			}
			out = append(out, DocumentSymbol{Name: name, Detail: detail, Kind: kind, Range: l.rng(st, e), SelectionRange: l.rng(ns, ne), Children: kids})
		}
		return out
	}
	return walk(r.res.Tree.Root), nil
}

func (s *Session) foldingRanges(params json.RawMessage) (any, error) {
	var p DocumentFormattingParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	id := fileID(p.TextDocument.URI)
	f := s.file(id)
	out := []FoldingRange{}
	if f == nil {
		return out, nil
	}
	l := newLines(f.Src)
	f.Tree.Root.Walk(func(n *syntax.Node) bool {
		kind := ""
		switch n.Kind {
		case syntax.StructBody, syntax.ProseBlock, syntax.Code, syntax.MapExpr, syntax.ArrayExpr, syntax.Params, syntax.Args, syntax.ProseLines, syntax.Fence:
		case syntax.Doc:
			kind = "comment"
		default:
			return true
		}
		st, e := n.Span()
		a, b := l.pos(st).Line, l.pos(e).Line
		if e > st && f.Src[e-1] == '\n' {
			b--
		}
		if b > a {
			out = append(out, FoldingRange{StartLine: a, EndLine: b, Kind: kind})
		}
		return true
	})
	return out, nil
}
