package lsp_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/tools/lsp/lsptest"
)

const indexSrc = `import = ["std/doc@1", "std/types@1"]
include = ["./model.heron"]

let greeting = "hi"

/// The guide to the store.
guide: section {
  | Read [the order](#Order) and $greeting.
}
`

const modelSrc = `/// An order someone places.
Order: type {
  /// Its id.
  id: String
}
`

type fixture struct {
	c                  *lsptest.Client
	dir                string
	indexURI, modelURI string
}

func newFixture(t *testing.T) *fixture {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.heron"), indexSrc)
	write(t, filepath.Join(dir, "model.heron"), modelSrc)
	c := start(t, dir)
	f := &fixture{c: c, dir: dir, indexURI: lsptest.FileURI(filepath.Join(dir, "index.heron")), modelURI: lsptest.FileURI(filepath.Join(dir, "model.heron"))}
	c.Open(f.indexURI, indexSrc)
	noDiagnostics(t, c, "fixture")
	return f
}

func pos(uri string, p lsptest.Position) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": p}
}

type location struct {
	URI   string        `json:"uri"`
	Range lsptest.Range `json:"range"`
}

func TestHoverShowsDocs(t *testing.T) {
	f := newFixture(t)
	var h struct {
		Contents struct{ Value string }
	}
	if err := f.c.Call("textDocument/hover", pos(f.indexURI, lsptest.At(indexSrc, "Order", 0, 1)), &h); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Order: type", "An order someone places.", "kind `type`", " · model.heron"} {
		if !strings.Contains(h.Contents.Value, want) {
			t.Errorf("hover lacks %q:\n%s", want, h.Contents.Value)
		}
	}
	// A file is named relative to the workspace, never by where the
	// server keeps it.
	if strings.Contains(h.Contents.Value, f.dir) {
		t.Errorf("hover names the server's folder:\n%s", h.Contents.Value)
	}
	// On a local, the let.
	if err := f.c.Call("textDocument/hover", pos(f.indexURI, lsptest.At(indexSrc, "$greeting", 0, 2)), &h); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Contents.Value, `let greeting = "hi"`) {
		t.Errorf("hover on a local: %s", h.Contents.Value)
	}
}

func TestDefinitionAcrossFilesAndFromImportStrings(t *testing.T) {
	f := newFixture(t)
	var locs []location
	if err := f.c.Call("textDocument/definition", pos(f.indexURI, lsptest.At(indexSrc, "#Order", 0, 2)), &locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 || locs[0].URI != f.modelURI || locs[0].Range.Start.Line != 1 {
		t.Fatalf("definition of a link: %+v", locs)
	}
	// From the include string to the file.
	if err := f.c.Call("textDocument/definition", pos(f.indexURI, lsptest.At(indexSrc, "model.heron", 0, 2)), &locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 || locs[0].URI != f.modelURI {
		t.Fatalf("definition of an include string: %+v", locs)
	}
	// From an import string into the standard package.
	if err := f.c.Call("textDocument/definition", pos(f.indexURI, lsptest.At(indexSrc, "std/doc@1", 0, 3)), &locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 || locs[0].URI != "heron:/std/doc/package.heron" {
		t.Fatalf("definition of an import string: %+v", locs)
	}
	var src struct{ Text string }
	if err := f.c.Call("heron/source", map[string]any{"uri": locs[0].URI}, &src); err != nil || !strings.Contains(src.Text, "section") {
		t.Fatalf("heron/source: %v %q", err, src.Text)
	}
	// A URI that names no file: an invalid-parameters error, as the
	// language server API's no_file says.
	if err := f.c.Call("heron/source", map[string]any{"uri": "heron:/std/nothing.heron"}, &src); err == nil || !strings.Contains(err.Error(), "no file heron:/std/nothing.heron") || !strings.Contains(err.Error(), "-32602") {
		t.Fatalf("heron/source of no file: %v", err)
	}
	// A kind in the standard package.
	if err := f.c.Call("textDocument/definition", pos(f.indexURI, lsptest.At(indexSrc, "section", 0, 2)), &locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 || locs[0].URI != "heron:/std/doc/package.heron" {
		t.Fatalf("definition of a kind: %+v", locs)
	}
}

func TestReferencesAndRename(t *testing.T) {
	f := newFixture(t)
	var locs []location
	params := pos(f.modelURI, lsptest.At(modelSrc, "Order", 0, 0))
	params["context"] = map[string]any{"includeDeclaration": true}
	if err := f.c.Call("textDocument/references", params, &locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 2 {
		t.Fatalf("references: %+v", locs)
	}
	var edit struct {
		Changes map[string][]struct {
			Range   lsptest.Range `json:"range"`
			NewText string        `json:"newText"`
		} `json:"changes"`
	}
	p := pos(f.indexURI, lsptest.At(indexSrc, "#Order", 0, 3))
	p["newName"] = "Purchase"
	if err := f.c.Call("textDocument/rename", p, &edit); err != nil {
		t.Fatal(err)
	}
	if len(edit.Changes[f.indexURI]) != 1 || len(edit.Changes[f.modelURI]) != 1 {
		t.Fatalf("rename edits: %+v", edit.Changes)
	}
	apply := func(src string, edits []struct {
		Range   lsptest.Range `json:"range"`
		NewText string        `json:"newText"`
	}) string {
		sort.Slice(edits, func(i, j int) bool { return edits[i].Range.Start.Line > edits[j].Range.Start.Line })
		lines := strings.Split(src, "\n")
		for _, e := range edits {
			l := lines[e.Range.Start.Line]
			lines[e.Range.Start.Line] = l[:e.Range.Start.Character] + e.NewText + l[e.Range.End.Character:]
		}
		return strings.Join(lines, "\n")
	}
	newIndex, newModel := apply(indexSrc, edit.Changes[f.indexURI]), apply(modelSrc, edit.Changes[f.modelURI])
	if !strings.Contains(newIndex, "(#Purchase)") || !strings.Contains(newModel, "Purchase: type") {
		t.Fatalf("renamed:\n%s\n%s", newIndex, newModel)
	}
	f.c.Replace(f.indexURI, newIndex)
	f.c.Open(f.modelURI, newModel)
	noDiagnostics(t, f.c, "after the rename")

	// A standard declaration can't be renamed.
	p = pos(f.indexURI, lsptest.At(newIndex, "section", 0, 1))
	p["newName"] = "chapter"
	if err := f.c.Call("textDocument/rename", p, &edit); err == nil {
		t.Fatal("renaming std's section should fail")
	}
}

type completionList struct {
	Items []struct {
		Label  string `json:"label"`
		Detail string `json:"detail"`
	} `json:"items"`
}

func (l completionList) labels() []string {
	var out []string
	for _, it := range l.Items {
		out = append(out, it.Label)
	}
	return out
}

func (l completionList) has(s string) bool {
	for _, it := range l.Items {
		if it.Label == s {
			return true
		}
	}
	return false
}

func complete(t *testing.T, c *lsptest.Client, uri string, p lsptest.Position, trigger string) completionList {
	t.Helper()
	params := pos(uri, p)
	if trigger != "" {
		params["context"] = map[string]any{"triggerKind": 2, "triggerCharacter": trigger}
	} else {
		params["context"] = map[string]any{"triggerKind": 1}
	}
	var l completionList
	if err := c.Call("textDocument/completion", params, &l); err != nil {
		t.Fatal(err)
	}
	for _, it := range l.Items {
		if strings.TrimSpace(it.Label) == "" {
			t.Fatalf("an empty label: %+v", l.Items)
		}
	}
	return l
}

func TestCompletion(t *testing.T) {
	f := newFixture(t)
	// A plain word in prose never completes.
	if l := complete(t, f.c, f.indexURI, lsptest.At(indexSrc, "Read", 0, 2), ""); len(l.Items) != 0 {
		t.Errorf("a plain prose word completed: %v", l.labels())
	}
	if l := complete(t, f.c, f.indexURI, lsptest.At(indexSrc, "the order", 0, 4), ""); len(l.Items) != 0 {
		t.Errorf("link text completed: %v", l.labels())
	}
	// After $ in prose: the names in scope.
	src := strings.Replace(indexSrc, "and $greeting.", "and $", 1)
	f.c.Replace(f.indexURI, src)
	if l := complete(t, f.c, f.indexURI, lsptest.At(src, "and $", 0, 5), "$"); !l.has("greeting") || !l.has("guide") {
		t.Errorf("after $: %v", l.labels())
	}
	// In a link's destination: node paths.
	src = strings.Replace(indexSrc, "(#Order)", "(#O)", 1)
	f.c.Replace(f.indexURI, src)
	if l := complete(t, f.c, f.indexURI, lsptest.At(src, "(#O", 0, 3), ""); !l.has("Order") {
		t.Errorf("in a link: %v", l.labels())
	}
	if l := complete(t, f.c, f.indexURI, lsptest.At(src, "(#O", 0, 2), "#"); !l.has("Order") {
		t.Errorf("after #: %v", l.labels())
	}
	// In a short link: node paths, and the names below a path.
	src = strings.Replace(indexSrc, "and $greeting.", "and [Ord].", 1)
	f.c.Replace(f.indexURI, src)
	if l := complete(t, f.c, f.indexURI, lsptest.At(src, "[Ord]", 0, 4), ""); !l.has("Order") {
		t.Errorf("in a short link: %v", l.labels())
	}
	src = strings.Replace(indexSrc, "and $greeting.", "and [`Order.", 1)
	f.c.Replace(f.indexURI, src)
	if l := complete(t, f.c, f.indexURI, lsptest.At(src, "[`Order.", 0, 8), "."); !l.has("id") {
		t.Errorf("in a short link's path: %v", l.labels())
	}
	// Kinds and types after a colon.
	src = indexSrc + "intro: \n"
	f.c.Replace(f.indexURI, src)
	if l := complete(t, f.c, f.indexURI, lsptest.At(src, "intro: ", 0, 7), ":"); !l.has("section") || !l.has("String") {
		t.Errorf("after a colon: %v", l.labels())
	}
	// Annotations after @.
	src = indexSrc + "@\nintro: section\n"
	f.c.Replace(f.indexURI, src)
	if l := complete(t, f.c, f.indexURI, lsptest.At(src, "@\n", 0, 1), "@"); !l.has("lint") {
		t.Errorf("after @: %v", l.labels())
	}
	// Members after a dot.
	src = indexSrc + "x: String = Order.\n"
	f.c.Replace(f.indexURI, src)
	if l := complete(t, f.c, f.indexURI, lsptest.At(src, "Order.\n", 0, 6), "."); !l.has("id") {
		t.Errorf("after a dot: %v", l.labels())
	}
	// Standard packages in an import string.
	src = strings.Replace(indexSrc, `"std/types@1"]`, `"std/types@1", "s"]`, 1)
	f.c.Replace(f.indexURI, src)
	if l := complete(t, f.c, f.indexURI, lsptest.At(src, `"s"]`, 0, 2), ""); !l.has("std/spec@1") {
		t.Errorf("in an import: %v", l.labels())
	}
	// Files beside the importing file in an include string.
	src = strings.Replace(indexSrc, `"./model.heron"`, `"./"`, 1)
	f.c.Replace(f.indexURI, src)
	if l := complete(t, f.c, f.indexURI, lsptest.At(src, `"./"`, 0, 3), "/"); !l.has("./model.heron") {
		t.Errorf("in an include: %v", l.labels())
	}
	// The fields a body's schema declares.
	src = strings.Replace(modelSrc, "  id: String\n", "  id: String\n  \n", 1)
	f.c.Open(f.modelURI, src)
	l := complete(t, f.c, f.modelURI, lsptest.At(src, "  \n", 0, 2), "")
	if len(l.Items) == 0 {
		t.Errorf("a member start completes nothing")
	}
}

func TestFormattingTokensSymbolsFolding(t *testing.T) {
	f := newFixture(t)
	messy := strings.Replace(indexSrc, "  | Read", "      | Read", 1)
	f.c.Replace(f.indexURI, messy)
	var edits []struct {
		NewText string `json:"newText"`
	}
	if err := f.c.Call("textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": f.indexURI}, "options": map[string]any{}}, &edits); err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 || edits[0].NewText != indexSrc {
		t.Fatalf("formatting: %+v", edits)
	}
	f.c.Replace(f.indexURI, indexSrc)
	var toks struct{ Data []int }
	if err := f.c.Call("textDocument/semanticTokens/full", map[string]any{"textDocument": map[string]any{"uri": f.indexURI}}, &toks); err != nil {
		t.Fatal(err)
	}
	if len(toks.Data) == 0 || len(toks.Data)%5 != 0 {
		t.Fatalf("semantic tokens: %v", toks.Data)
	}
	var syms []struct {
		Name     string
		Children []struct{ Name string }
	}
	if err := f.c.Call("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": f.indexURI}}, &syms); err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 || syms[0].Name != "guide" {
		t.Fatalf("symbols: %+v", syms)
	}
	var folds []struct{ StartLine, EndLine int }
	if err := f.c.Call("textDocument/foldingRange", map[string]any{"textDocument": map[string]any{"uri": f.indexURI}}, &folds); err != nil {
		t.Fatal(err)
	}
	if len(folds) == 0 {
		t.Fatal("no folding ranges")
	}
}

// Definition and hover on a short link's path answer for the node it
// resolves to, by suffix too.
func TestShortLinkDefinitionAndHover(t *testing.T) {
	const src = `import = ["std/doc@1"]

checkout: section {
  /// The sum of an order's lines.
  total: term {{ The sum of an order's lines. }}
}

refunds: section {
  | A refund never exceeds the [total], nor [` + "`checkout.total`" + `].
}
`
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.heron"), src)
	c := start(t, dir)
	uri := lsptest.FileURI(filepath.Join(dir, "index.heron"))
	c.Open(uri, src)
	noDiagnostics(t, c, "short links")
	for _, at := range []lsptest.Position{lsptest.At(src, "[total]", 0, 2), lsptest.At(src, "checkout.total`", 0, 10)} {
		var locs []location
		if err := c.Call("textDocument/definition", pos(uri, at), &locs); err != nil {
			t.Fatal(err)
		}
		if len(locs) != 1 || locs[0].Range.Start.Line != 4 {
			t.Fatalf("definition of a short link at %+v: %+v", at, locs)
		}
		var h struct {
			Contents struct{ Value string }
		}
		if err := c.Call("textDocument/hover", pos(uri, at), &h); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(h.Contents.Value, "The sum of an order's lines.") {
			t.Errorf("hover on a short link: %s", h.Contents.Value)
		}
	}
}

// Prose is authored text: the semantic tokens of a prose file's text,
// its heading and its elements' words are never keywords, variables,
// numbers or types; Markdown's marks are operators.
func TestProseSemanticTokens(t *testing.T) {
	const idx = `import = ["std/doc@1"]

include = ["./guide.heron.md"]
`
	const md = `# Refunds for orders {#refunds}

Refunds are asked for if the order is paid, and then in 2 days.

<warning @lens="user">It can't be undone for in let.</warning>
`
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.heron"), idx)
	write(t, filepath.Join(dir, "guide.heron.md"), md)
	c := start(t, dir)
	uri := lsptest.FileURI(filepath.Join(dir, "guide.heron.md"))
	c.Open(uri, md)
	var toks struct{ Data []int }
	if err := c.Call("textDocument/semanticTokens/full", map[string]any{"textDocument": map[string]any{"uri": uri}}, &toks); err != nil {
		t.Fatal(err)
	}
	// The server's legend: type 1 is "type", 8 "variable", 15 "keyword"
	// and 19 "number".
	code := map[int]string{1: "type", 8: "variable", 15: "keyword", 19: "number"}
	line := 0
	for i := 0; i+4 < len(toks.Data); i += 5 {
		line += toks.Data[i]
		if k, ok := code[toks.Data[i+3]]; ok {
			t.Errorf("line %d: a %s token in prose", line, k)
		}
	}
}

// Definition and hover on a kind element's name answer for its kind, and
// on a `<ref to="…">` target for the node it resolves to.
func TestElementDefinitionAndHover(t *testing.T) {
	const src = `import = ["std/doc@1"]

checkout: section {
  /// The sum of an order's lines.
  total: term {{ The sum of an order's lines. }}
}

refunds: section {
  | A refund never exceeds the <ref to="total"/>. <warning>It can't be undone.</warning>
}
`
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.heron"), src)
	c := start(t, dir)
	uri := lsptest.FileURI(filepath.Join(dir, "index.heron"))
	c.Open(uri, src)
	noDiagnostics(t, c, "elements")
	var locs []location
	if err := c.Call("textDocument/definition", pos(uri, lsptest.At(src, `"total"`, 0, 2)), &locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 || locs[0].Range.Start.Line != 4 {
		t.Fatalf("definition of a ref target: %+v", locs)
	}
	if err := c.Call("textDocument/definition", pos(uri, lsptest.At(src, "<warning>", 0, 2)), &locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 || locs[0].URI != "heron:/std/doc/package.heron" {
		t.Fatalf("definition of an element's name: %+v", locs)
	}
	var h struct {
		Contents struct{ Value string }
	}
	if err := c.Call("textDocument/hover", pos(uri, lsptest.At(src, `"total"`, 0, 2)), &h); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Contents.Value, "The sum of an order's lines.") {
		t.Errorf("hover on a ref target: %s", h.Contents.Value)
	}
}
