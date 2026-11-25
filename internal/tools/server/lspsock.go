package server

import (
	"context"
	"io"
	"net/http"

	"github.com/coder/websocket"
	"github.com/helightdev/specs-v0/internal/core/registry"
	"github.com/helightdev/specs-v0/internal/tools/lsp"
)

// MessageConn carries whole JSON-RPC messages, one per socket frame; it is
// the language server's lsp.Conn.
type MessageConn interface {
	ReadMessage() ([]byte, error)
	WriteMessage([]byte) error
}

// LanguageServer is the LSP the shell opens on /lsp: one lsp session per
// socket, over the workspace.
func LanguageServer(r *registry.Registry) LSP {
	return func(ctx context.Context, root string, c MessageConn) error {
		return lsp.NewSession(c, lsp.Options{Registry: r, Dir: root}).Run(ctx)
	}
}

// serveLSP accepts the editor's socket and runs one language-server
// session over it until either side closes.
func serveLSP(w http.ResponseWriter, r *http.Request, serve LSP, root string) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(64 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	err = serve(ctx, root, &socketConn{ctx: ctx, c: c})
	if err != nil && ctx.Err() == nil {
		c.Close(websocket.StatusInternalError, truncate(err.Error(), 120))
		return
	}
	c.Close(websocket.StatusNormalClosure, "")
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// socketConn is a MessageConn over a websocket: each text frame is one
// message, with no headers.
type socketConn struct {
	ctx context.Context
	c   *websocket.Conn
}

func (s *socketConn) ReadMessage() ([]byte, error) {
	for {
		typ, msg, err := s.c.Read(s.ctx)
		if err != nil {
			return nil, io.EOF
		}
		if typ == websocket.MessageText {
			return msg, nil
		}
	}
}

func (s *socketConn) WriteMessage(b []byte) error {
	return s.c.Write(s.ctx, websocket.MessageText, b)
}
