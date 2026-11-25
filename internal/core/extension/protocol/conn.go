package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Error is an error response, or what a handler returns to send one.
type Error struct {
	Code    int64
	Message string
	Type    string // the Heron type of a thrown value
	Data    Value
}

func (e *Error) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("%s: %s", e.Type, e.Message)
	}
	return e.Message
}

// Errorf returns an *Error with a code.
func Errorf(code int64, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrClosed is returned for calls in flight when the connection ends, such
// as when the peer process crashes.
var ErrClosed = errors.New("the connection to the extension closed")

type ctxKey int

const (
	runKey ctxKey = iota
	streamKey
)

// WithRun returns a context whose requests carry a run id.
func WithRun(ctx context.Context, run string) context.Context {
	return context.WithValue(ctx, runKey, run)
}

// RunOf returns the run id a request was sent with.
func RunOf(ctx context.Context) string {
	s, _ := ctx.Value(runKey).(string)
	return s
}

// StreamItem sends one item of a handler's streamed result. The response
// the handler returns ends the stream.
func StreamItem(ctx context.Context, item any) error {
	s, _ := ctx.Value(streamKey).(func(any) error)
	if s == nil {
		return errors.New("protocol: StreamItem outside a handler")
	}
	return s(item)
}

// Options configure a connection.
type Options struct {
	Wire     Wire
	Limits   Limits
	Handlers map[string]Handler
	// Notifications are called, in order, for each notification other than
	// the protocol's own `$/cancel` and `$/stream`.
	Notifications map[string]func(decode func(any) error)
}

type pending struct {
	done   chan *Message
	stream func(decode func(any) error) error
}

// Conn is one side of a connection: it sends requests and notifications,
// and serves the methods in its handlers.
type Conn struct {
	opts Options
	r    io.Reader
	w    io.Writer

	wmu sync.Mutex

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]*pending
	serving  map[int64]context.CancelFunc
	closed   bool
	closeErr error
	done     chan struct{}
}

// NewConn returns a connection over a reader and writer, such as a child
// process's stdout and stdin. Serve must run for anything to arrive.
func NewConn(r io.Reader, w io.Writer, opts Options) *Conn {
	if opts.Wire == nil {
		opts.Wire = CBOR
	}
	if opts.Limits.MaxFrame == 0 {
		opts.Limits = DefaultLimits()
	}
	return &Conn{opts: opts, r: r, w: w, pending: map[int64]*pending{}, serving: map[int64]context.CancelFunc{}, done: make(chan struct{})}
}

// SetLimits changes the limits, as after a handshake.
func (c *Conn) SetLimits(l Limits) {
	c.mu.Lock()
	c.opts.Limits = l
	c.mu.Unlock()
}

func (c *Conn) limits() Limits {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opts.Limits
}

// Done is closed when the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns why the connection ended.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeErr
}

func (c *Conn) send(m *Message) error {
	data, err := c.opts.Wire.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return WriteFrame(c.w, data, int(c.limits().MaxFrame))
}

func (c *Conn) encode(v any) (Raw, error) {
	if v == nil {
		return nil, nil
	}
	data, err := c.opts.Wire.Marshal(v)
	return Raw(data), err
}

func (c *Conn) decoder(r Raw) func(any) error {
	return func(v any) error {
		if r.IsNull() {
			return nil
		}
		return c.opts.Wire.Unmarshal(r, v)
	}
}

// Call sends a request and decodes its result into result, which may be
// nil. Cancelling ctx sends `$/cancel`; a context without a deadline gets
// the declared timeout.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	return c.CallStream(ctx, method, params, result, nil)
}

// CallStream is Call for a method whose result streams: item is called
// with each `$/stream` item before the response arrives.
func (c *Conn) CallStream(ctx context.Context, method string, params, result any, item func(decode func(any) error) error) error {
	if _, ok := ctx.Deadline(); !ok {
		if t := c.limits().TimeoutMs; t > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(t)*time.Millisecond)
			defer cancel()
		}
	}
	raw, err := c.encode(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrClosed, c.closeErr)
	}
	c.nextID++
	id := c.nextID
	p := &pending{done: make(chan *Message, 1), stream: item}
	c.pending[id] = p
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	if err := c.send(&Message{ID: &id, Method: method, Params: raw, Run: RunOf(ctx)}); err != nil {
		var big ErrFrameTooLarge
		if errors.As(err, &big) {
			return &Error{Code: CodeLimitExceeded, Message: big.Error()}
		}
		return err
	}
	select {
	case m := <-p.done:
		if m == nil {
			return fmt.Errorf("%w: %v", ErrClosed, c.Err())
		}
		if m.Error != nil {
			return &Error{Code: m.Error.Code, Message: m.Error.Message, Type: m.Error.Type, Data: m.Error.Data}
		}
		if result != nil {
			return c.decoder(m.Result)(result)
		}
		return nil
	case <-ctx.Done():
		_ = c.Notify(MethodCancel, &CancelParams{ID: id})
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &Error{Code: CodeTimeout, Message: fmt.Sprintf("%s timed out", method)}
		}
		return &Error{Code: CodeCancelled, Message: fmt.Sprintf("%s was cancelled", method)}
	}
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	raw, err := c.encode(params)
	if err != nil {
		return err
	}
	return c.send(&Message{Method: method, Params: raw})
}

// Serve reads frames until the reader ends or a frame can't be read, and
// returns why. Every call in flight then fails with ErrClosed.
func (c *Conn) Serve() error {
	var err error
	for {
		var data []byte
		data, err = ReadFrame(c.r, int(c.limits().MaxFrame))
		if err != nil {
			break
		}
		var m Message
		if uerr := c.opts.Wire.Unmarshal(data, &m); uerr != nil {
			_ = c.send(&Message{Error: &ErrorObject{Code: CodeParseError, Message: uerr.Error()}})
			continue
		}
		c.dispatch(&m)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	c.mu.Lock()
	c.closed = true
	c.closeErr = err
	for _, p := range c.pending {
		close(p.done)
	}
	c.pending = map[int64]*pending{}
	for _, cancel := range c.serving {
		cancel()
	}
	c.mu.Unlock()
	close(c.done)
	return err
}

func (c *Conn) dispatch(m *Message) {
	switch {
	case m.ID != nil && m.Method == "":
		c.mu.Lock()
		p := c.pending[*m.ID]
		c.mu.Unlock()
		if p != nil {
			p.done <- m
		}
	case m.ID != nil:
		c.serve(m)
	case m.Method == MethodCancel:
		var p CancelParams
		if c.decoder(m.Params)(&p) == nil {
			c.mu.Lock()
			if cancel := c.serving[p.ID]; cancel != nil {
				cancel()
			}
			c.mu.Unlock()
		}
	case m.Method == MethodStream:
		var p struct {
			ID   int64 `cbor:"id" json:"id"`
			Item Raw   `cbor:"item" json:"item"`
		}
		if c.decoder(m.Params)(&p) == nil {
			c.mu.Lock()
			pd := c.pending[p.ID]
			c.mu.Unlock()
			if pd != nil && pd.stream != nil {
				_ = pd.stream(c.decoder(p.Item))
			}
		}
	default:
		if f := c.opts.Notifications[m.Method]; f != nil {
			f(c.decoder(m.Params))
		}
	}
}

func (c *Conn) serve(m *Message) {
	id := *m.ID
	h := c.opts.Handlers[m.Method]
	if h == nil {
		_ = c.send(&Message{ID: &id, Error: &ErrorObject{Code: CodeMethodNotFound, Message: fmt.Sprintf("method %q isn't served", m.Method)}})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	if m.Run != "" {
		ctx = WithRun(ctx, m.Run)
	}
	ctx = context.WithValue(ctx, streamKey, func(item any) error {
		return c.Notify(MethodStream, &struct {
			ID   int64 `cbor:"id" json:"id"`
			Item any   `cbor:"item" json:"item"`
		}{id, item})
	})
	c.mu.Lock()
	if max := c.opts.Limits.MaxInFlight; max > 0 && int64(len(c.serving)) >= max {
		c.mu.Unlock()
		cancel()
		_ = c.send(&Message{ID: &id, Error: &ErrorObject{Code: CodeLimitExceeded, Message: fmt.Sprintf("more than %d requests in flight", max)}})
		return
	}
	c.serving[id] = cancel
	c.mu.Unlock()
	go func() {
		defer cancel()
		res, err := h(ctx, c.decoder(m.Params))
		if ctx.Err() != nil && err == nil {
			err = &Error{Code: CodeCancelled, Message: "cancelled"}
		}
		out := &Message{ID: &id}
		if err != nil {
			var e *Error
			if !errors.As(err, &e) {
				e = &Error{Code: CodeInternalError, Message: err.Error()}
			}
			out.Error = &ErrorObject{Code: e.Code, Message: e.Message, Type: e.Type, Data: e.Data}
		} else {
			raw, eerr := c.encode(res)
			if eerr != nil {
				out.Error = &ErrorObject{Code: CodeInternalError, Message: eerr.Error()}
			} else {
				out.Result = raw
				if out.Result == nil {
					out.Result = Raw(nil)
				}
			}
		}
		// The request stops counting toward max_in_flight before its
		// response is sent, so the caller's next request is never refused
		// for it.
		c.mu.Lock()
		delete(c.serving, id)
		c.mu.Unlock()
		if serr := c.send(out); serr != nil {
			var big ErrFrameTooLarge
			if errors.As(serr, &big) {
				_ = c.send(&Message{ID: &id, Error: &ErrorObject{Code: CodeLimitExceeded, Message: "result: " + big.Error()}})
			}
		}
	}()
}
