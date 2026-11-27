// Package lsptest is a scripted LSP client for tests and the end-to-end
// check of `heron lsp`: it sends requests and notifications over a
// connection, and keeps the latest diagnostics published for each URI.
package lsptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client is a scripted LSP client.
type Client struct {
	w  io.Writer
	r  *bufio.Reader
	mu sync.Mutex
	// wmu serialises writes. It isn't mu: a write can block until the
	// server reads, and the server reads only once the reader has taken
	// what it wrote, which needs mu.
	wmu sync.Mutex
	id  int
	// waiting maps request ids to their reply channels.
	waiting map[int]chan Message
	diags   map[string][]Diagnostic
	// Published counts diagnostics notifications, for tests.
	Published int
	versions  map[string]int
	closed    chan struct{}
	// Timeout bounds each request; zero means 30s.
	Timeout time.Duration
}

// Message is a JSON-RPC message.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Diagnostic is a published diagnostic.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"`
	Code     string `json:"code"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%d:%d %s [%s %s]", d.Range.Start.Line+1, d.Range.Start.Character+1, d.Message, d.Source, d.Code)
}

// New starts a client reading from r and writing to w with Content-Length
// framing.
func New(r io.Reader, w io.Writer) *Client {
	c := &Client{w: w, r: bufio.NewReader(r), waiting: map[int]chan Message{}, diags: map[string][]Diagnostic{},
		versions: map[string]int{}, closed: make(chan struct{})}
	go c.read()
	return c
}

func (c *Client) read() {
	defer close(c.closed)
	for {
		length := -1
		for {
			line, err := c.r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				if length >= 0 {
					break
				}
				continue
			}
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, "Content-Length") {
				length, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return
		}
		var m Message
		if json.Unmarshal(buf, &m) != nil {
			continue
		}
		c.mu.Lock()
		switch {
		case m.Method == "textDocument/publishDiagnostics":
			var p struct {
				URI         string       `json:"uri"`
				Diagnostics []Diagnostic `json:"diagnostics"`
			}
			json.Unmarshal(m.Params, &p)
			c.diags[p.URI] = p.Diagnostics
			c.Published++
		case m.Method != "" && m.ID != nil:
			// A request from the server, such as client/registerCapability.
			go c.send(Message{ID: m.ID, Result: json.RawMessage("null")})
		case m.Method == "":
			id, _ := strconv.Atoi(string(m.ID))
			if ch := c.waiting[id]; ch != nil {
				delete(c.waiting, id)
				ch <- m
			}
		}
		c.mu.Unlock()
	}
}

func (c *Client) send(m Message) error {
	m.JSONRPC = "2.0"
	b, _ := json.Marshal(m)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return err
}

// Call sends a request and decodes its result into out.
func (c *Client) Call(method string, params, out any) error {
	c.mu.Lock()
	c.id++
	id := c.id
	ch := make(chan Message, 1)
	c.waiting[id] = ch
	c.mu.Unlock()
	b, _ := json.Marshal(params)
	if err := c.send(Message{ID: json.RawMessage(strconv.Itoa(id)), Method: method, Params: b}); err != nil {
		return err
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return fmt.Errorf("%s: %s (%d)", method, m.Error.Message, m.Error.Code)
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-c.closed:
		return fmt.Errorf("%s: the server closed the connection", method)
	case <-time.After(timeout):
		return fmt.Errorf("%s: no reply in %v", method, timeout)
	}
}

// Notify sends a notification.
func (c *Client) Notify(method string, params any) error {
	b, _ := json.Marshal(params)
	return c.send(Message{Method: method, Params: b})
}

// Initialize starts the session with a workspace folder.
func (c *Client) Initialize(root string) error {
	var res json.RawMessage
	params := map[string]any{"processId": nil, "rootUri": FileURI(root),
		"workspaceFolders": []map[string]any{{"uri": FileURI(root), "name": filepath.Base(root)}},
		"capabilities":     map[string]any{"workspace": map[string]any{"didChangeWatchedFiles": map[string]any{"dynamicRegistration": true}}}}
	if err := c.Call("initialize", params, &res); err != nil {
		return err
	}
	return c.Notify("initialized", map[string]any{})
}

// Open opens a document by URI with its text.
func (c *Client) Open(uri, text string) error {
	c.mu.Lock()
	c.versions[uri] = 1
	c.mu.Unlock()
	return c.Notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
		"uri": uri, "languageId": "heron", "version": 1, "text": text}})
}

// Replace replaces a document's whole text.
func (c *Client) Replace(uri, text string) error {
	c.mu.Lock()
	c.versions[uri]++
	v := c.versions[uri]
	c.mu.Unlock()
	return c.Notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": v},
		"contentChanges": []map[string]any{{"text": text}}})
}

// Edit replaces a range of a document.
func (c *Client) Edit(uri string, r Range, text string) error {
	c.mu.Lock()
	c.versions[uri]++
	v := c.versions[uri]
	c.mu.Unlock()
	return c.Notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": v},
		"contentChanges": []map[string]any{{"range": r, "text": text}}})
}

// Close closes a document.
func (c *Client) Close(uri string) error {
	return c.Notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
}

// FilesChanged tells the server files changed on disk.
func (c *Client) FilesChanged(uris ...string) error {
	var ch []map[string]any
	for _, u := range uris {
		ch = append(ch, map[string]any{"uri": u, "type": 2})
	}
	return c.Notify("workspace/didChangeWatchedFiles", map[string]any{"changes": ch})
}

// Sync waits until the server has rechecked everything sent so far and
// published the diagnostics: every request rechecks first.
func (c *Client) Sync() error {
	return c.Call("workspace/executeCommand", map[string]any{"command": "heron.sync", "arguments": []any{}}, nil)
}

// Diagnostics returns the latest diagnostics published for a URI.
func (c *Client) Diagnostics(uri string) []Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Diagnostic(nil), c.diags[uri]...)
}

// AllDiagnostics returns every URI's latest non-empty diagnostics.
func (c *Client) AllDiagnostics() map[string][]Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string][]Diagnostic{}
	for u, d := range c.diags {
		if len(d) > 0 {
			out[u] = append([]Diagnostic(nil), d...)
		}
	}
	return out
}

// Shutdown ends the session.
func (c *Client) Shutdown() error {
	if err := c.Call("shutdown", nil, nil); err != nil {
		return err
	}
	return c.Notify("exit", nil)
}

// FileURI is a path's file URI.
func FileURI(p string) string {
	abs, _ := filepath.Abs(p)
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String()
}

// At returns the position of the n-th (from 0) occurrence of sub in text,
// plus an offset in bytes into it, as an LSP position (ASCII text).
func At(text, sub string, n, plus int) Position {
	off := -1
	from := 0
	for i := 0; i <= n; i++ {
		k := strings.Index(text[from:], sub)
		if k < 0 {
			return Position{-1, -1}
		}
		off = from + k
		from = off + 1
	}
	off += plus
	line := strings.Count(text[:off], "\n")
	col := off - (strings.LastIndexByte(text[:off], '\n') + 1)
	return Position{Line: line, Character: col}
}
