package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/lint"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// Options configure a session.
type Options struct {
	// Registry holds the lints and prose providers; required.
	Registry *registry.Registry
	// Dir is the root used when the client names no workspace.
	Dir string
	// Debounce is how long an edit waits before the open documents are
	// rechecked; 0 means 150ms. Every request rechecks first if needed, so
	// answers always reflect the latest text.
	Debounce time.Duration
	// Log receives a trace of errors, if set.
	Log io.Writer
}

// Session is one client's language server.
type Session struct {
	conn Conn
	opts Options

	chk   *check.Session
	prose *lint.ProseCache
	roots []resolve.FileID

	docs map[resolve.FileID]*document
	// owners maps each open document to the entry point whose tree it's
	// checked in.
	owners  map[resolve.FileID]resolve.FileID
	results map[resolve.FileID]*result
	// diags holds the last diagnostics of every file, with their fixes,
	// for code actions.
	diags     map[resolve.FileID]diag.List
	published map[string]bool

	dirty       bool
	initialized bool
	shutdown    bool
	watch       bool // the client registers file watchers dynamically
	nextID      int
	in          chan inbound
}

type inbound struct {
	msg *message
	err error
}

// document is an open document: its text is the loader's overlay for its
// identity, never a second copy of the file.
type document struct {
	id      resolve.FileID
	uris    []string // the URIs the client opened it by, first one first
	version int
	text    string
}

// result is the analysis of one entry point.
type result struct {
	entry    resolve.FileID
	res      *check.Result
	complete bool
	siteMap  map[*syntax.Node]*resolve.Node
	roots    map[*syntax.Node]resolve.FileID
}

// NewSession returns a session over a connection.
func NewSession(c Conn, o Options) *Session {
	if o.Debounce == 0 {
		o.Debounce = 150 * time.Millisecond
	}
	if o.Registry == nil {
		o.Registry = registry.New()
	}
	return &Session{
		conn: c, opts: o,
		chk:       check.NewSession(),
		prose:     lint.NewProseCache(),
		docs:      map[resolve.FileID]*document{},
		owners:    map[resolve.FileID]resolve.FileID{},
		results:   map[resolve.FileID]*result{},
		diags:     map[resolve.FileID]diag.List{},
		published: map[string]bool{},
		in:        make(chan inbound, 64),
	}
}

// Serve runs a session over a stream with Content-Length framing, as
// `heron lsp` does on stdio.
func Serve(ctx context.Context, rw io.ReadWriter, o Options) error {
	return NewSession(StreamConn(rw), o).Run(ctx)
}

// Run serves messages until `exit`, the end of the connection or the
// context's end.
func (s *Session) Run(ctx context.Context) error {
	go func() {
		for {
			b, err := s.conn.ReadMessage()
			if err != nil {
				s.in <- inbound{err: err}
				return
			}
			var m message
			if err := json.Unmarshal(b, &m); err != nil {
				s.reply(nil, nil, &rpcError{Code: codeParseError, Message: err.Error()})
				continue
			}
			s.in <- inbound{msg: &m}
		}
	}()
	var timer *time.Timer
	var tick <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick:
			tick = nil
			s.flush()
		case in := <-s.in:
			if in.err != nil {
				if in.err == io.EOF {
					return nil
				}
				return in.err
			}
			if in.msg.Method == "exit" {
				return nil
			}
			s.handle(in.msg)
			if s.dirty && tick == nil {
				if timer == nil {
					timer = time.NewTimer(s.opts.Debounce)
				} else {
					timer.Reset(s.opts.Debounce)
				}
				tick = timer.C
			}
		}
	}
}

func (s *Session) logf(format string, args ...any) {
	if s.opts.Log != nil {
		fmt.Fprintf(s.opts.Log, "heron lsp: "+format+"\n", args...)
	}
}

func (s *Session) send(m *message) {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		s.logf("marshal: %v", err)
		return
	}
	if err := s.conn.WriteMessage(b); err != nil {
		s.logf("write: %v", err)
	}
}

func (s *Session) reply(id json.RawMessage, result any, err *rpcError) {
	if id == nil {
		id = json.RawMessage("null")
	}
	m := &message{ID: id, Error: err}
	if err == nil {
		b, _ := json.Marshal(result)
		m.Result = b
	}
	s.send(m)
}

func (s *Session) notify(method string, params any) {
	b, _ := json.Marshal(params)
	s.send(&message{Method: method, Params: b})
}

func (s *Session) request(method string, params any) {
	s.nextID++
	b, _ := json.Marshal(params)
	s.send(&message{ID: json.RawMessage(fmt.Sprint(s.nextID)), Method: method, Params: b})
}

// handler answers a request; a nil error with a nil result replies null.
type handler func(s *Session, params json.RawMessage) (any, error)

var requests = map[string]handler{
	"initialize":                       (*Session).initialize,
	"shutdown":                         func(s *Session, _ json.RawMessage) (any, error) { s.shutdown = true; return nil, nil },
	"textDocument/completion":          (*Session).completion,
	"textDocument/hover":               (*Session).hover,
	"textDocument/definition":          (*Session).definition,
	"textDocument/references":          (*Session).references,
	"textDocument/prepareRename":       (*Session).prepareRename,
	"textDocument/rename":              (*Session).rename,
	"textDocument/formatting":          (*Session).formatting,
	"textDocument/semanticTokens/full": (*Session).semanticTokens,
	"textDocument/codeAction":          (*Session).codeAction,
	"textDocument/documentSymbol":      (*Session).documentSymbols,
	"textDocument/foldingRange":        (*Session).foldingRanges,
	"workspace/executeCommand":         (*Session).executeCommand,
	"heron/source":                     (*Session).source,
	"heron/preview":                    (*Session).preview,
}

var notifications = map[string]func(s *Session, params json.RawMessage){
	"initialized":                     (*Session).didInitialize,
	"textDocument/didOpen":            (*Session).didOpen,
	"textDocument/didChange":          (*Session).didChange,
	"textDocument/didClose":           (*Session).didClose,
	"textDocument/didSave":            func(s *Session, _ json.RawMessage) { s.dirty = true },
	"workspace/didChangeWatchedFiles": (*Session).didChangeWatchedFiles,
}

func (s *Session) handle(m *message) {
	if m.Method == "" {
		return // a response to one of our requests
	}
	if m.ID == nil {
		if f := notifications[m.Method]; f != nil && (s.initialized || m.Method == "initialized") {
			f(s, m.Params)
		}
		return
	}
	h := requests[m.Method]
	if h == nil {
		s.reply(m.ID, nil, &rpcError{Code: codeMethodNotFound, Message: "no method " + m.Method})
		return
	}
	if !s.initialized && m.Method != "initialize" {
		s.reply(m.ID, nil, &rpcError{Code: codeNotInitialized, Message: "the server isn't initialized"})
		return
	}
	if m.Method != "initialize" && m.Method != "shutdown" {
		s.flush() // answers reflect the latest text
	}
	res, err := func() (res any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("internal error in %s: %v", m.Method, r)
			}
		}()
		return h(s, m.Params)
	}()
	if err != nil {
		code := codeInternal
		if e, ok := err.(*rpcError); ok {
			code = e.Code
		}
		s.logf("%s: %v", m.Method, err)
		s.reply(m.ID, nil, &rpcError{Code: code, Message: err.Error()})
		return
	}
	s.reply(m.ID, res, nil)
}

func decode(params json.RawMessage, v any) error {
	if err := json.Unmarshal(params, v); err != nil {
		return &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	return nil
}

// --- lifecycle ---

// Semantic token legend.
var tokenTypes = []string{"namespace", "type", "class", "enum", "interface", "struct", "typeParameter",
	"parameter", "variable", "property", "enumMember", "event", "function", "method", "macro",
	"keyword", "modifier", "comment", "string", "number", "regexp", "operator", "decorator"}
var tokenModifiers = []string{"declaration", "definition", "readonly", "documentation", "defaultLibrary"}

func (s *Session) initialize(params json.RawMessage) (any, error) {
	var p struct {
		InitializeParams
		Capabilities struct {
			Workspace struct {
				DidChangeWatchedFiles struct {
					DynamicRegistration bool `json:"dynamicRegistration"`
				} `json:"didChangeWatchedFiles"`
			} `json:"workspace"`
		} `json:"capabilities"`
	}
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	s.watch = p.Capabilities.Workspace.DidChangeWatchedFiles.DynamicRegistration
	for _, f := range p.WorkspaceFolders {
		s.roots = append(s.roots, fileID(f.URI))
	}
	if len(s.roots) == 0 && p.RootURI != "" {
		s.roots = append(s.roots, fileID(p.RootURI))
	}
	if len(s.roots) == 0 && p.RootPath != "" {
		if id, err := resolve.Canonical(p.RootPath); err == nil {
			s.roots = append(s.roots, id)
		}
	}
	if len(s.roots) == 0 && s.opts.Dir != "" {
		if id, err := resolve.Canonical(s.opts.Dir); err == nil {
			s.roots = append(s.roots, id)
		}
	}
	s.initialized = true
	return map[string]any{
		"capabilities": map[string]any{
			"positionEncoding": "utf-16",
			"textDocumentSync": map[string]any{"openClose": true, "change": 2, "save": map[string]any{"includeText": false}},
			"completionProvider": map[string]any{
				// Characters where a completion makes sense; plain words in
				// prose never trigger one.
				"triggerCharacters": []string{".", ":", "@", "<", "$", "#", "\"", "/", "{"},
			},
			"hoverProvider":              true,
			"definitionProvider":         true,
			"referencesProvider":         true,
			"renameProvider":             map[string]any{"prepareProvider": true},
			"documentFormattingProvider": true,
			"documentSymbolProvider":     true,
			"foldingRangeProvider":       true,
			"codeActionProvider":         map[string]any{"codeActionKinds": []string{"quickfix"}},
			"executeCommandProvider":     map[string]any{"commands": commandNames()},
			"semanticTokensProvider": map[string]any{
				"legend": map[string]any{"tokenTypes": tokenTypes, "tokenModifiers": tokenModifiers},
				"full":   true,
			},
		},
		"serverInfo": map[string]any{"name": "heron"},
	}, nil
}

func (s *Session) didInitialize(json.RawMessage) {
	if !s.watch {
		return
	}
	s.request("client/registerCapability", map[string]any{
		"registrations": []map[string]any{{
			"id": "heron-files", "method": "workspace/didChangeWatchedFiles",
			"registerOptions": map[string]any{"watchers": []map[string]any{
				{"globPattern": "**/*.heron"}, {"globPattern": "**/*.heron.md"},
				{"globPattern": "**/.vale.ini"}, {"globPattern": "**/*.txt"}, {"globPattern": "**/*.yml"},
			}},
		}},
	})
}

// --- documents ---

func (s *Session) didOpen(params json.RawMessage) {
	var p DidOpenParams
	if decode(params, &p) != nil {
		return
	}
	id := fileID(p.TextDocument.URI)
	d := s.docs[id]
	if d == nil {
		d = &document{id: id}
		s.docs[id] = d
	}
	if !contains(d.uris, p.TextDocument.URI) {
		d.uris = append(d.uris, p.TextDocument.URI)
	}
	d.version, d.text = p.TextDocument.Version, p.TextDocument.Text
	s.chk.Loader.Overlay[id] = d.text
	delete(s.owners, id)
	s.dirty = true
}

func (s *Session) didChange(params json.RawMessage) {
	var p DidChangeParams
	if decode(params, &p) != nil {
		return
	}
	d := s.docs[fileID(p.TextDocument.URI)]
	if d == nil {
		return
	}
	for _, ch := range p.ContentChanges {
		d.text = applyChange(d.text, ch)
	}
	d.version = p.TextDocument.Version
	s.chk.Loader.Overlay[d.id] = d.text
	s.dirty = true
}

func (s *Session) didClose(params json.RawMessage) {
	var p DidCloseParams
	if decode(params, &p) != nil {
		return
	}
	id := fileID(p.TextDocument.URI)
	d := s.docs[id]
	if d == nil {
		return
	}
	for i, u := range d.uris {
		if u == p.TextDocument.URI {
			d.uris = append(d.uris[:i], d.uris[i+1:]...)
			break
		}
	}
	if len(d.uris) > 0 {
		return // still open under another spelling
	}
	delete(s.docs, id)
	delete(s.owners, id)
	delete(s.chk.Loader.Overlay, id) // the file on disk is the content again
	s.dirty = true
}

func (s *Session) didChangeWatchedFiles(json.RawMessage) {
	// The loader rereads every file that isn't open and reparses what
	// changed; every open document is rechecked, so importers update.
	s.owners = map[resolve.FileID]resolve.FileID{}
	s.dirty = true
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// text returns a file's current text: the open document, or the file as
// last loaded, or the disk.
func (s *Session) text(id resolve.FileID) (string, bool) {
	if d := s.docs[id]; d != nil {
		return d.text, true
	}
	if f, err := s.chk.Loader.Load(id); err == nil {
		return f.Src, true
	}
	return "", false
}

func (s *Session) file(id resolve.FileID) *resolve.File {
	f, err := s.chk.Loader.Load(id)
	if err != nil {
		return nil
	}
	return f
}

// --- analysis ---

// owner finds the entry point an open document is checked in: of the
// index.heron files in its folder and the folders above it (up to the
// workspace root), the outermost whose tree includes the document, or the
// document itself.
func (s *Session) owner(id resolve.FileID) resolve.FileID {
	if e, ok := s.owners[id]; ok {
		return e
	}
	e := s.findOwner(id)
	s.owners[id] = e
	return e
}

func (s *Session) findOwner(id resolve.FileID) resolve.FileID {
	if id.IsStd() || !filepath.IsAbs(string(id)) {
		return id
	}
	var root resolve.FileID
	for _, r := range s.roots {
		if within(id, r) && len(r) > len(root) {
			root = r
		}
	}
	var candidates []resolve.FileID
	for dir := filepath.Dir(string(id)); ; dir = filepath.Dir(dir) {
		idx := resolve.FileID(filepath.Join(dir, "index.heron"))
		if _, open := s.docs[idx]; open || exists(string(idx)) {
			candidates = append(candidates, idx)
		}
		if (root != "" && dir == string(root)) || filepath.Dir(dir) == dir {
			break
		}
	}
	for i := len(candidates) - 1; i >= 0; i-- {
		c := candidates[i]
		if c == id {
			return id
		}
		res := s.check(c)
		for _, f := range res.res.Tree.Files {
			if f.ID == id {
				return c
			}
		}
	}
	return id
}

func within(id, root resolve.FileID) bool {
	return id == root || strings.HasPrefix(string(id), string(root)+string(filepath.Separator))
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// check checks an entry point once per analysis.
func (s *Session) check(entry resolve.FileID) *result {
	if r := s.results[entry]; r != nil {
		return r
	}
	r := &result{entry: entry, res: s.chk.Check(entry)}
	s.results[entry] = r
	return r
}

// complete makes the checker's facts cover the whole tree, for queries.
func (r *result) completed() *check.Result {
	if !r.complete {
		r.res.Complete()
		r.complete = true
	}
	return r.res
}

// flush rechecks every open document if anything changed, and publishes
// the diagnostics.
func (s *Session) flush() {
	if !s.dirty {
		return
	}
	s.dirty = false
	s.results = map[resolve.FileID]*result{}
	entries := map[resolve.FileID]bool{}
	for id := range s.docs {
		entries[s.owner(id)] = true
	}
	// An owner whose tree no longer includes the document is found again.
	for id := range s.docs {
		e := s.owners[id]
		if e == id {
			continue
		}
		if !treeHas(s.check(e).res, id) {
			delete(s.owners, id)
			entries[s.owner(id)] = true
		}
	}
	byFile := map[resolve.FileID]diag.List{}
	files := map[resolve.FileID]bool{}
	for e := range entries {
		r := s.check(e)
		ds := append(diag.List(nil), r.res.Diags...)
		for i := range ds {
			ds[i].Code = "check:" + ds[i].Code
		}
		ds = append(ds, lint.RunWith(s.opts.Registry, r.res, s.prose)...)
		r.complete = true
		for _, f := range treeFiles(r.res.Tree) {
			files[f] = true
		}
		for _, d := range ds {
			id := resolve.FileID(d.File)
			if id == "" || id.IsStd() {
				continue
			}
			byFile[id] = append(byFile[id], d)
			files[id] = true
		}
	}
	for id := range s.docs {
		files[id] = true
	}
	s.diags = map[resolve.FileID]diag.List{}
	now := map[string]bool{}
	ids := make([]resolve.FileID, 0, len(files))
	for id := range files {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		ds := byFile[id].Sort()
		s.diags[id] = ds
		uris := []string{fileURI(id)}
		var version *int
		if d := s.docs[id]; d != nil {
			uris = d.uris
			v := d.version
			version = &v
		}
		src, _ := s.text(id)
		l := newLines(src)
		out := make([]Diagnostic, 0, len(ds))
		for _, d := range ds {
			out = append(out, s.toDiagnostic(l, d))
		}
		for _, u := range uris {
			if len(out) == 0 && !s.published[u] {
				continue
			}
			now[u] = len(out) > 0
			s.notify("textDocument/publishDiagnostics", PublishDiagnosticsParams{URI: u, Version: version, Diagnostics: out})
		}
	}
	for u, had := range s.published {
		if _, ok := now[u]; !ok && had {
			s.notify("textDocument/publishDiagnostics", PublishDiagnosticsParams{URI: u, Diagnostics: []Diagnostic{}})
		}
	}
	s.published = map[string]bool{}
	for u, had := range now {
		if had {
			s.published[u] = true
		}
	}
}

func treeHas(r *check.Result, id resolve.FileID) bool {
	for _, f := range r.Tree.Files {
		if f.ID == id {
			return true
		}
	}
	return false
}

// treeFiles lists a tree's own files and those of the local packages it
// imports, which aren't embedded.
func treeFiles(t *resolve.Tree) []resolve.FileID {
	var out []resolve.FileID
	seen := map[*resolve.Tree]bool{}
	var walk func(t *resolve.Tree)
	walk = func(t *resolve.Tree) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		for _, f := range t.Files {
			if !f.ID.IsStd() {
				out = append(out, f.ID)
			}
		}
		for _, p := range t.Imports {
			if p.Major == 0 && p.Git == nil {
				walk(p.Tree)
			}
		}
	}
	walk(t)
	return out
}

// source names where a diagnostic came from: the type checker, a lint, or
// a prose provider such as Vale.
func (s *Session) diagSource(code string) (src, name string) {
	if c, ok := strings.CutPrefix(code, "check:"); ok {
		return "heron check", c
	}
	if p, _, ok := strings.Cut(code, "."); ok {
		for _, pp := range s.opts.Registry.ProseProviders() {
			if pp.Name == p {
				return p, code
			}
		}
	}
	return "heron lint", code
}

func (s *Session) toDiagnostic(l *lines, d diag.Diagnostic) Diagnostic {
	src, code := s.diagSource(d.Code)
	out := Diagnostic{Range: l.rng(d.Start, d.End), Severity: severity(d.Severity), Code: code, Source: src, Message: d.Message}
	for _, r := range d.Related {
		rl := l
		if r.File != d.File {
			t, _ := s.text(resolve.FileID(r.File))
			rl = newLines(t)
		}
		out.RelatedInformation = append(out.RelatedInformation, DiagnosticRelatedInformation{
			Location: Location{URI: s.uriOf(resolve.FileID(r.File)), Range: rl.rng(r.Start, r.End)}, Message: r.Message})
	}
	return out
}

// uriOf is the URI to report a file by: the one the client opened it with,
// or its canonical URI.
func (s *Session) uriOf(id resolve.FileID) string {
	if d := s.docs[id]; d != nil && len(d.uris) > 0 {
		return d.uris[0]
	}
	return fileURI(id)
}

func severity(s diag.Severity) int {
	switch s {
	case diag.Error:
		return 1
	case diag.Warning:
		return 2
	default:
		return 4
	}
}

// resultFor returns the analysis whose tree holds a file: its owner's if
// it's open, or any current analysis that includes it.
func (s *Session) resultFor(id resolve.FileID) *result {
	if _, open := s.docs[id]; open {
		return s.check(s.owner(id))
	}
	for _, r := range s.results {
		for _, f := range treeFiles(r.res.Tree) {
			if f == id {
				return r
			}
		}
	}
	return s.check(s.findOwner(id))
}

// source answers heron/source: the text of a file by URI, such as an
// embedded standard file, so an editor can open it read-only.
func (s *Session) source(params json.RawMessage) (any, error) {
	var p TextDocumentIdentifier
	if err := decode(params, &p); err != nil {
		return nil, err
	}
	t, ok := s.text(fileID(p.URI))
	if !ok {
		return nil, &rpcError{Code: codeInvalidParams, Message: "no file " + p.URI}
	}
	return map[string]string{"text": t}, nil
}
