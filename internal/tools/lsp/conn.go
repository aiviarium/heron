// Package lsp is Heron's language server as a library: a Session speaks
// LSP's JSON-RPC over a Conn, and `heron lsp` serves one over stdio. It
// builds on unit 1's check session (per-node memoization, the loader's
// overlay), its lints and prose providers, the formatter and the resolved
// tree.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Conn carries whole JSON-RPC messages, one per call.
type Conn interface {
	// ReadMessage returns the next message; io.EOF ends the session.
	ReadMessage() ([]byte, error)
	// WriteMessage sends one message. The session calls it from one
	// goroutine at a time.
	WriteMessage([]byte) error
}

// StreamConn frames messages with Content-Length headers, as LSP does
// over stdio.
func StreamConn(rw io.ReadWriter) Conn {
	return &streamConn{r: bufio.NewReader(rw), w: rw}
}

type streamConn struct {
	r  *bufio.Reader
	w  io.Writer
	mu sync.Mutex
}

func (c *streamConn) ReadMessage() ([]byte, error) {
	length := -1
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				return nil, io.EOF
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if length < 0 {
				continue
			}
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q", v)
			}
			length = n
		}
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *streamConn) WriteMessage(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := fmt.Fprintf(c.w, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err := c.w.Write(b)
	return err
}

// message is any JSON-RPC message: a request (ID and Method), a
// notification (Method only) or a response (ID with Result or Error).
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
	codeNotInitialized = -32002
)
