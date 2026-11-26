package lsp

import (
	"encoding/json"
	"strings"

	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

func tokenType(name string) int {
	for i, t := range tokenTypes {
		if t == name {
			return i
		}
	}
	return -1
}

const (
	modDeclaration    = 1 << 0
	modDocumentation  = 1 << 3
	modDefaultLibrary = 1 << 4
)

// semanticTokens classifies the words of a file from the resolved tree, on
// top of the static grammar: what a path names decides its colour.
func (s *Session) semanticTokens(params json.RawMessage) (any, error) {
	var p SemanticTokensParams
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	id := fileID(p.TextDocument.URI)
	f := s.file(id)
	if f == nil {
		return SemanticTokens{Data: []uint32{}}, nil
	}
	r := s.resultFor(id)
	c := r.completed().Checker
	sites := r.sites()
	l := newLines(f.Src)
	var data []uint32
	prevLine, prevChar := 0, 0
	emit := func(start, end, typ, mods int) {
		if typ < 0 || end <= start {
			return
		}
		// Tokens don't span lines: split at each newline.
		for start < end {
			nl := strings.IndexByte(f.Src[start:end], '\n')
			stop := end
			if nl >= 0 {
				stop = start + nl
			}
			if stop > start {
				ps := l.pos(start)
				length := utf16Len(f.Src[start:stop])
				dl := ps.Line - prevLine
				dc := ps.Character
				if dl == 0 {
					dc -= prevChar
				}
				data = append(data, uint32(dl), uint32(dc), uint32(length), uint32(typ), uint32(mods))
				prevLine, prevChar = ps.Line, ps.Character
			}
			if nl < 0 {
				break
			}
			start = stop + 1
		}
	}
	var walk func(n *syntax.Node, inDoc bool)
	walk = func(n *syntax.Node, inDoc bool) {
		if n.Kind == syntax.Doc {
			inDoc = true
		}
		for _, ch := range n.Children {
			switch ch := ch.(type) {
			case *syntax.Node:
				walk(ch, inDoc)
			case *syntax.Token:
				st, e := ch.Span()
				typ, mods := classify(ch, n, c, sites)
				if inDoc && typ < 0 && !ch.Kind.IsTrivia() {
					typ, mods = tokenType("comment"), modDocumentation
				}
				emit(st, e, typ, mods)
			}
		}
	}
	walk(f.Tree.Root, false)
	if data == nil {
		data = []uint32{}
	}
	return SemanticTokens{Data: data}, nil
}

func classify(t *syntax.Token, parent *syntax.Node, c *types.Checker, sites map[*syntax.Node]*resolve.Node) (int, int) {
	switch t.Kind {
	case syntax.Comment:
		return tokenType("comment"), 0
	case syntax.DocPrefix:
		return tokenType("comment"), modDocumentation
	case syntax.Number:
		return tokenType("number"), 0
	case syntax.KwTrue, syntax.KwFalse, syntax.KwLet, syntax.KwDo, syntax.KwIf, syntax.KwElse, syntax.KwFor,
		syntax.KwIn, syntax.KwReturn, syntax.KwNot, syntax.KwAnd, syntax.KwOr, syntax.KwIs:
		return tokenType("keyword"), 0
	case syntax.Quote, syntax.StringText, syntax.Escape, syntax.Raw:
		return tokenType("string"), 0
	case syntax.HeadingMark, syntax.Pipe:
		return tokenType("keyword"), 0
	case syntax.At:
		return tokenType("decorator"), 0
	case syntax.Dollar, syntax.DollarBrace, syntax.Hash, syntax.LinkOpen, syntax.LinkMid, syntax.LinkClose, syntax.LinkTick, syntax.ShortLinkEnd,
		syntax.TagOpen, syntax.TagCloseOpen, syntax.TagEnd, syntax.TagSelfEnd, syntax.LBrace2, syntax.RBrace2:
		return tokenType("operator"), 0
	case syntax.CodeSpan, syntax.FenceLine:
		return tokenType("string"), 0
	case syntax.Url:
		return tokenType("string"), 0
	case syntax.Ident:
	default:
		return -1, 0
	}
	switch parent.Kind {
	case syntax.Flags:
		return tokenType("modifier"), 0
	case syntax.Param:
		return tokenType("parameter"), modDeclaration
	case syntax.NamedArg:
		return tokenType("parameter"), 0
	case syntax.Let, syntax.LetStmt, syntax.ForExpr:
		return tokenType("variable"), modDeclaration
	case syntax.TagAttr:
		return tokenType("property"), 0
	case syntax.FieldExpr:
		return tokenType("property"), 0
	case syntax.Path:
	default:
		return tokenType("variable"), 0
	}
	ctx := parent.Parent
	if ctx != nil && ctx.Kind == syntax.Annotation {
		return tokenType("decorator"), 0
	}
	mods := 0
	n := c.Refs[parent]
	if n == nil && ctx != nil && isMember(ctx.Kind) {
		n = sites[ctx]
		mods = modDeclaration
	}
	if n == nil {
		if ctx != nil && (ctx.Kind == syntax.Insert || ctx.Kind == syntax.PathExpr) {
			return tokenType("variable"), 0
		}
		if ctx != nil && ctx.Kind == syntax.NamedType {
			return tokenType("type"), 0
		}
		return tokenType("property"), mods
	}
	// The segment's own node, for a dotted path.
	ids := pathIdents(parent)
	for i, x := range ids {
		if x == t {
			n = ascend(n, i, len(ids))
		}
	}
	if n == nil {
		return tokenType("property"), mods
	}
	if site := n.Decl; site.File != nil && site.File.ID.IsStd() {
		mods |= modDefaultLibrary
	}
	switch {
	case c.IsKind(n):
		return tokenType("class"), mods
	case c.IsEnum(n):
		return tokenType("enum"), mods
	case c.IsInterface(n):
		return tokenType("interface"), mods
	case c.IsType(n) || resolve.IsBuiltin(n):
		return tokenType("type"), mods
	case c.IsAnnotation(n):
		return tokenType("decorator"), mods
	case c.IsFlag(n):
		return tokenType("modifier"), mods
	}
	if k := c.KindOf(n); k != nil {
		switch k.Name {
		case "func", "task", "op", "macro", "hook":
			return tokenType("function"), mods
		case "section", "package", "api":
			return tokenType("namespace"), mods
		}
	}
	if n.Parent != nil && c.IsEnum(n.Parent) {
		return tokenType("enumMember"), mods
	}
	return tokenType("property"), mods
}
