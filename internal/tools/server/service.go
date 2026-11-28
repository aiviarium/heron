package server

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/aiviarium/heron/internal/tools/server/api"
	"github.com/aiviarium/heron/internal/tools/server/route"
)

// service implements the API's ops.
type service struct {
	shell  *Shell
	ws     *Workspace
	agents *agentExport
}

var _ api.Service = (*service)(nil)

// The API's errors, by the id each declares.
func errNotInWorkspace(p string) error {
	return route.Coded(http.StatusBadRequest, "server.NOT_IN_WORKSPACE", "%s is not a path in the workspace", p)
}

func errNoSuchFile(p string) error {
	return route.Coded(http.StatusNotFound, "server.NO_SUCH_FILE", "%s: no such file", p)
}

func (s *service) Preview(ctx context.Context, req *api.PreviewRequest) (*api.Preview, error) {
	id, err := s.ws.Abs(req.File)
	if err != nil {
		return nil, errNotInWorkspace(req.File)
	}
	var out *api.Preview
	err = s.ws.Analyze(req.Overlays, func(a *Analysis) error {
		off := 0
		if req.Offset != nil {
			off = int(*req.Offset)
		}
		if req.Position != nil {
			text, ok := req.Overlays[req.File]
			if !ok {
				b, _ := os.ReadFile(string(id))
				text = string(b)
			}
			off = utf16Offset(text, int(req.Position.Line), int(req.Position.Character))
		}
		out = a.preview(s.shell.Renderer, id, off)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// utf16Offset turns a 0-based line and a 0-based UTF-16 column into a byte
// offset of text, clamped to the line.
func utf16Offset(text string, line, char int) int {
	off := 0
	for i := 0; i < line; i++ {
		j := strings.IndexByte(text[off:], '\n')
		if j < 0 {
			return len(text)
		}
		off += j + 1
	}
	n := 0
	for i, r := range text[off:] {
		if n >= char || r == '\n' {
			return off + i
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return len(text)
}
