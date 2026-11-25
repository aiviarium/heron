// Package route is what generated API code and the server shell share: a
// mounted op, and the helpers its generated handler uses to read
// parameters and write results. Generated code for the base API and for
// every later unit's ops builds on it, so the shell mounts them alike.
package route

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// Route is one op mounted on the server.
type Route struct {
	Op      string // the op's path in the API tree, such as spec_server.preview
	Method  string // GET, POST, …; empty for a WebSocket
	Path    string // the full path with :params, such as /api/v1/records/:id
	Stream  bool   // the op returns a stream, sent as server-sent events
	Socket  bool   // the op is a WebSocket
	Handler http.HandlerFunc
}

// Pattern is the route's pattern for net/http's ServeMux.
func (r Route) Pattern() string {
	p := ""
	for i := 0; i < len(r.Path); i++ {
		if r.Path[i] == ':' {
			j := i + 1
			for j < len(r.Path) && r.Path[j] != '/' {
				j++
			}
			p += "{" + r.Path[i+1:j] + "}"
			i = j - 1
			continue
		}
		p += string(r.Path[i])
	}
	if r.Method == "" {
		return "GET " + p
	}
	return r.Method + " " + p
}

// Error is an error with an HTTP status, returned by a handler.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// Errorf returns an *Error with a status.
func Errorf(status int, format string, args ...any) error {
	return &Error{Status: status, Message: fmt.Sprintf(format, args...)}
}

// WriteJSON writes a value as a 200 response.
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes an error as JSON with its status.
func WriteError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var e *Error
	if errors.As(err, &e) {
		status = e.Status
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// DecodeBody reads a JSON body into v.
func DecodeBody(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return Errorf(http.StatusBadRequest, "invalid body: %v", err)
	}
	return nil
}

// Int parses an integer parameter.
func Int(name, s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, Errorf(http.StatusBadRequest, "%s: not an integer: %q", name, s)
	}
	return n, nil
}

// Bool parses a boolean parameter.
func Bool(name, s string) (bool, error) {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false, Errorf(http.StatusBadRequest, "%s: not a boolean: %q", name, s)
	}
	return b, nil
}

// Events writes server-sent events, one JSON value each.
type Events struct {
	w       http.ResponseWriter
	started bool
}

// NewEvents prepares w for a stream.
func NewEvents(w http.ResponseWriter) *Events { return &Events{w: w} }

// Send writes one event and flushes it.
func (e *Events) Send(v any) error {
	if !e.started {
		e.w.Header().Set("Content-Type", "text/event-stream")
		e.w.Header().Set("Cache-Control", "no-cache")
		e.w.WriteHeader(http.StatusOK)
		e.started = true
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(e.w, "data: %s\n\n", b); err != nil {
		return err
	}
	if f, ok := e.w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// Fail ends a stream with an error: as a response before anything was
// sent, or as an `error` event after.
func (e *Events) Fail(err error) {
	if !e.started {
		WriteError(e.w, err)
		return
	}
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Fprintf(e.w, "event: error\ndata: %s\n\n", b)
	if f, ok := e.w.(http.Flusher); ok {
		f.Flush()
	}
}
