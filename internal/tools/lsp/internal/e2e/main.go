// Command e2e drives `heron lsp` over stdio through a scripted LSP client
// on a copy of the sample repository: Done means #1 (diagnostics,
// completion, hover, definition and rename) and #3 (Vale alerts on the
// right words with their fixes and vocabulary actions, and the spec-aware
// lints flagging unlinked terms). scripts/e2e-lsp.sh runs it.
//
//	e2e <heron binary> <copy of examples/petstore>
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/helightdev/specs-v0/internal/tools/lsp/lsptest"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: e2e <heron> <sample>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "e2e-lsp:", err)
		os.Exit(1)
	}
	fmt.Println("e2e-lsp: ok")
}

type step struct {
	c    *lsptest.Client
	root string
	docs map[string]string // open documents by URI
}

func (s *step) path(rel string) string { return filepath.Join(s.root, rel) }
func (s *step) uri(rel string) string  { return lsptest.FileURI(s.path(rel)) }

func (s *step) open(rel string) error {
	b, err := os.ReadFile(s.path(rel))
	if err != nil {
		return err
	}
	s.docs[s.uri(rel)] = string(b)
	return s.c.Open(s.uri(rel), string(b))
}

func (s *step) replace(rel, text string) error {
	s.docs[s.uri(rel)] = text
	return s.c.Replace(s.uri(rel), text)
}

func (s *step) clean(when string) error {
	if err := s.c.Sync(); err != nil {
		return err
	}
	if all := s.c.AllDiagnostics(); len(all) > 0 {
		return fmt.Errorf("%s: unexpected diagnostics: %v", when, all)
	}
	return nil
}

func pos(uri string, p lsptest.Position) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": p}
}

type textEdit struct {
	Range   lsptest.Range `json:"range"`
	NewText string        `json:"newText"`
}

// apply applies edits to a text, for ASCII positions.
func apply(src string, edits []textEdit) string {
	sort.Slice(edits, func(i, j int) bool {
		a, b := edits[i].Range.Start, edits[j].Range.Start
		return a.Line > b.Line || a.Line == b.Line && a.Character > b.Character
	})
	lines := strings.Split(src, "\n")
	for _, e := range edits {
		if e.Range.Start.Line != e.Range.End.Line {
			continue
		}
		l := lines[e.Range.Start.Line]
		lines[e.Range.Start.Line] = l[:e.Range.Start.Character] + e.NewText + l[e.Range.End.Character:]
	}
	return strings.Join(lines, "\n")
}

func run(heron, root string) error {
	cmd := exec.Command(heron, "lsp")
	cmd.Dir = os.TempDir() // relative imports must not depend on the cwd
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Process.Kill()
	s := &step{c: lsptest.New(stdout, stdin), root: root, docs: map[string]string{}}
	c := s.c
	if err := c.Initialize(root); err != nil {
		return err
	}

	// Every file opens clean.
	var files []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && (info.Name() == "node_modules" || info.Name() == ".vale") {
			return filepath.SkipDir
		}
		if err == nil && strings.HasSuffix(p, ".heron") {
			rel, _ := filepath.Rel(root, p)
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	for _, f := range files {
		if err := s.open(f); err != nil {
			return err
		}
	}
	if err := s.clean("after opening " + fmt.Sprint(len(files)) + " files"); err != nil {
		return err
	}

	// Diagnostics: a break in model.heron shows in api.heron, which uses it.
	model, api := "specs/store/model.heron", "specs/store/api.heron"
	modelSrc := s.docs[s.uri(model)]
	s.replace(model, strings.Replace(modelSrc, "OrderRequest: type", "OrderReq: type", 1))
	c.Sync()
	ds := c.Diagnostics(s.uri(api))
	if len(ds) == 0 || ds[0].Source != "heron check" || !strings.Contains(ds[0].Message, "OrderRequest") {
		return fmt.Errorf("a break in model.heron should show in api.heron: %v", ds)
	}
	s.replace(model, modelSrc)
	if err := s.clean("after the fix"); err != nil {
		return err
	}

	// Completion: annotations after @, names after $, nothing on a plain word.
	apiSrc := s.docs[s.uri(api)]
	type list struct {
		Items []struct{ Label string } `json:"items"`
	}
	has := func(l list, label string) bool {
		for _, it := range l.Items {
			if it.Label == label {
				return true
			}
		}
		return false
	}
	complete := func(src, at string, n, plus int, trig string) (list, error) {
		p := pos(s.uri(api), lsptest.At(src, at, n, plus))
		if trig != "" {
			p["context"] = map[string]any{"triggerKind": 2, "triggerCharacter": trig}
		}
		var l list
		err := c.Call("textDocument/completion", p, &l)
		return l, err
	}
	edited := strings.Replace(apiSrc, "  @http<get>(\"/pets/:pet_id\")", "  @\n  @http<get>(\"/pets/:pet_id\")", 1)
	s.replace(api, edited)
	if l, err := complete(edited, "  @\n", 0, 3, "@"); err != nil || !has(l, "http") || !has(l, "throws") {
		return fmt.Errorf("completion after @: %v %v", l, err)
	}
	s.replace(api, apiSrc)
	if l, err := complete(apiSrc, "Places an order", 0, 3, ""); err != nil || len(l.Items) != 0 {
		return fmt.Errorf("a plain prose word completed: %v %v", l, err)
	}

	// Hover shows the /// docs; definition goes across files.
	var h struct{ Contents struct{ Value string } }
	if err := c.Call("textDocument/hover", pos(s.uri(api), lsptest.At(apiSrc, "op(Order)", 0, 4)), &h); err != nil {
		return err
	}
	if !strings.Contains(h.Contents.Value, "An order for one pet") {
		return fmt.Errorf("hover on Order: %q", h.Contents.Value)
	}
	type location struct {
		URI   string        `json:"uri"`
		Range lsptest.Range `json:"range"`
	}
	var locs []location
	if err := c.Call("textDocument/definition", pos(s.uri(api), lsptest.At(apiSrc, "op(Order)", 0, 4)), &locs); err != nil {
		return err
	}
	if len(locs) != 1 || locs[0].URI != s.uri(model) || locs[0].Range.Start.Line != lsptest.At(modelSrc, "Order: type", 0, 0).Line {
		return fmt.Errorf("definition of Order: %+v", locs)
	}
	indexSrc := s.docs[s.uri("specs/store/index.heron")]
	if err := c.Call("textDocument/definition", pos(s.uri("specs/store/index.heron"), lsptest.At(indexSrc, "./api.heron", 0, 3)), &locs); err != nil {
		return err
	}
	if len(locs) != 1 || locs[0].URI != s.uri(api) {
		return fmt.Errorf("definition of an include string: %+v", locs)
	}

	// Rename Order to Purchase everywhere, apply it, and nothing breaks.
	var we struct {
		Changes map[string][]textEdit `json:"changes"`
	}
	p := pos(s.uri(api), lsptest.At(apiSrc, "op(Order)", 0, 4))
	p["newName"] = "Purchase"
	if err := c.Call("textDocument/rename", p, &we); err != nil {
		return err
	}
	if len(we.Changes) < 2 {
		return fmt.Errorf("rename touched %d files: %+v", len(we.Changes), we.Changes)
	}
	for uri, edits := range we.Changes {
		next := apply(s.docs[uri], edits)
		s.docs[uri] = next
		c.Replace(uri, next)
	}
	if err := s.clean("after renaming Order"); err != nil {
		return err
	}
	if !strings.Contains(s.docs[s.uri(api)], "op(Purchase)") || !strings.Contains(s.docs[s.uri(model)], "Purchase: type") {
		return fmt.Errorf("the rename didn't reach both files")
	}
	apiSrc = s.docs[s.uri(api)]

	// Vale: alerts on the right words, never in code or links, with fixes
	// and vocabulary actions.
	line := "    | Places an order for an [available](#orders.available) pet, subject to the [ordering rules](#orders.ordering)."
	valeLine := line + "\n    | We utilize the thign, not `utilize` or [utilize](#orders.ordering)."
	edited = strings.Replace(apiSrc, line, valeLine, 1)
	s.replace(api, edited)
	c.Sync()
	var vale []lsptest.Diagnostic
	for _, d := range c.Diagnostics(s.uri(api)) {
		if d.Source == "Vale" {
			vale = append(vale, d)
		}
	}
	at := func(word string) lsptest.Position { return lsptest.At(edited, word, 0, 0) }
	if len(vale) != 2 || vale[0].Range.Start != at("utilize the") || vale[1].Range.Start != at("thign") ||
		vale[0].Code != "Vale.Petstore.Simple" || vale[1].Code != "Vale.Vale.Spelling" {
		return fmt.Errorf("Vale alerts: %v", vale)
	}
	type action struct {
		Title string
		Edit  *struct {
			Changes map[string][]textEdit `json:"changes"`
		}
		Command *struct {
			Command   string
			Arguments []any
		}
	}
	actions := func(d lsptest.Diagnostic) ([]action, error) {
		var as []action
		err := c.Call("textDocument/codeAction", map[string]any{"textDocument": map[string]any{"uri": s.uri(api)},
			"range": d.Range, "context": map[string]any{"diagnostics": []any{}}}, &as)
		return as, err
	}
	as, err := actions(vale[0])
	if err != nil || len(as) == 0 || as[0].Title != "Replace with use" || as[0].Edit == nil {
		return fmt.Errorf("Vale's fix: %+v %v", as, err)
	}
	as, err = actions(vale[1])
	var accept *action
	for i := range as {
		if strings.HasPrefix(as[i].Title, `Accept "thign"`) {
			accept = &as[i]
		}
	}
	if err != nil || accept == nil || accept.Command == nil {
		return fmt.Errorf("vocabulary actions: %+v %v", as, err)
	}
	if err := c.Call("workspace/executeCommand", map[string]any{"command": accept.Command.Command, "arguments": accept.Command.Arguments}, nil); err != nil {
		return err
	}
	c.Sync()
	for _, d := range c.Diagnostics(s.uri(api)) {
		if d.Code == "Vale.Vale.Spelling" {
			return fmt.Errorf("an accepted word is still flagged: %v", d)
		}
	}
	b, _ := os.ReadFile(s.path(".vale/styles/config/vocabularies/Petstore/accept.txt"))
	if !strings.Contains(string(b), "thign\n") {
		return fmt.Errorf("accept.txt: %q", b)
	}

	// The spec-aware lints flag an unlinked term, with a fix that links it.
	termLine := line + "\n    | Only an available pet is held."
	edited = strings.Replace(apiSrc, line, termLine, 1)
	s.replace(api, edited)
	c.Sync()
	var term *lsptest.Diagnostic
	for _, d := range c.Diagnostics(s.uri(api)) {
		if d.Code == "Spec.UnlinkedTerm" {
			d := d
			term = &d
		}
	}
	if term == nil || term.Range.Start != lsptest.At(edited, "available pet is", 0, 0) || term.Source != "Spec" {
		return fmt.Errorf("the unlinked term: %v", c.Diagnostics(s.uri(api)))
	}
	as, err = actions(*term)
	if err != nil || len(as) == 0 || as[0].Edit == nil {
		return fmt.Errorf("the term's fix: %+v %v", as, err)
	}
	for uri, edits := range as[0].Edit.Changes {
		next := apply(s.docs[uri], edits)
		s.docs[uri] = next
		c.Replace(uri, next)
	}
	if err := s.clean("after linking the term"); err != nil {
		return err
	}
	return c.Shutdown()
}
