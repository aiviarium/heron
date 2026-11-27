package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/aiviarium/sparrow-rpc/go/cbor"
	"github.com/aiviarium/sparrow-rpc/go/schema"
	"github.com/aiviarium/sparrow-rpc/go/session"
	"github.com/aiviarium/sparrow-rpc/go/status"
	"github.com/aiviarium/sparrow-rpc/go/transport"
	"github.com/aiviarium/sparrow-rpc/go/value"
	"github.com/aiviarium/sparrow-rpc/go/wire"
)

// Error is a failed call: what a handler returns to answer with Sparrow's
// Error, and what a caller gets back. Code is the protocol's code, carried
// in the Error's details beside the Sparrow status it maps to.
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

// ErrClosed is returned for calls in flight when the session ends, such
// as when the peer process crashes.
var ErrClosed = errors.New("the connection to the extension closed")

// The Sparrow status each code is answered with, from the protocol's
// errors table; a status without details reads back as the first code
// that has it.
var codeStatus = []struct {
	code   int64
	status status.Code
}{
	{CodeParseError, status.ProtocolError},
	{CodeInvalidParams, status.InvalidArgument},
	{CodeInvalidRequest, status.InvalidArgument},
	{CodeMethodNotFound, status.Unimplemented},
	{CodeInternalError, status.Internal},
	{CodeCancelled, status.Cancelled},
	{CodeForeignError, status.Unknown},
	{CodeThrown, status.Aborted},
	{CodeLimitExceeded, status.ResourceExhausted},
	{CodeLimitExceeded, status.MessageTooLarge},
	{CodeTimeout, status.DeadlineExceeded},
}

func statusOf(code int64) status.Code {
	for _, c := range codeStatus {
		if c.code == code {
			return c.status
		}
	}
	return status.Unknown
}

func codeOf(s status.Code) int64 {
	for _, c := range codeStatus {
		if c.status == s {
			return c.code
		}
	}
	return CodeInternalError
}

// RunKey is the metadata key that carries a run's id.
const RunKey = "heron.run"

type ctxKey int

const (
	runKey ctxKey = iota
	callKey
)

// WithRun returns a context whose calls carry a run id.
func WithRun(ctx context.Context, run string) context.Context {
	return context.WithValue(ctx, runKey, run)
}

// RunOf returns the run id a call was made with.
func RunOf(ctx context.Context) string {
	s, _ := ctx.Value(runKey).(string)
	return s
}

// served is what a handler's context carries of the call it serves.
type served struct {
	sc     *session.ServerCall
	b      *binder
	method *schema.Method
}

// StreamItem sends one item of a handler's streamed result, in a handler
// of a method whose result is a Stream. Its End follows the handler's
// return.
func StreamItem(ctx context.Context, item any) error {
	s, _ := ctx.Value(callKey).(*served)
	if s == nil {
		return errors.New("protocol: StreamItem outside a handler")
	}
	if !s.method.Output.IsStream() {
		return fmt.Errorf("protocol: %s doesn't stream its result", s.method.Name)
	}
	x, err := s.b.encode(*s.method.Output.Elem, reflect.ValueOf(item))
	if err != nil {
		return err
	}
	return s.sc.Send(ctx, x)
}

// Progress reports a handler's progress to its caller as Sparrow's
// Progress: done of total, which may be 0 for unknown, and a note.
func Progress(ctx context.Context, done, total int64, note string) error {
	s, _ := ctx.Value(callKey).(*served)
	if s == nil {
		return errors.New("protocol: Progress outside a handler")
	}
	var tp *uint64
	if total > 0 {
		t := uint64(total)
		tp = &t
	}
	var np *string
	if note != "" {
		np = &note
	}
	return s.sc.Progress(ctx, uint64(done), tp, np)
}

// DefaultLimits are the limits a host offers when an extension declares
// none of its own.
func DefaultLimits() Limits {
	return Limits{MaxMessage: 16 << 20, MaxInFlight: 64, TimeoutMs: int64((30 * time.Second) / time.Millisecond)}
}

// Options configure a connection.
type Options struct {
	Limits Limits
	// Handlers serve this side's methods, by name.
	Handlers map[string]Handler
	// Notifications are called, in order, for each notification this
	// side's facet declares, such as the host's log.
	Notifications map[string]func(decode func(any) error)
	// HandshakeTimeout bounds the Sparrow handshake; 0 is 30 s.
	HandshakeTimeout time.Duration
}

// Conn is one side of the protocol: a Sparrow session that publishes this
// side's facet, serves its methods and calls the peer's.
type Conn struct {
	opts  Options
	sess  *session.Session
	local *schema.Facet
	peer  *schema.Facet
	lb    *binder
	pb    *binder

	mu     sync.Mutex
	notify sync.Mutex
}

// Accept runs heron's side of a session on conn, the pipe of a process it
// started: it publishes heron.host in Welcome and requires the process's
// Hello to publish heron.extension. It returns once the handshake is done.
func Accept(ctx context.Context, conn transport.Conn, opts Options) (*Conn, error) {
	return open(ctx, conn, opts, wire.Server)
}

// Dial runs an extension's side of a session on conn, such as
// pipe.Stdio(): it publishes heron.extension in Hello and calls heron.host.
func Dial(ctx context.Context, conn transport.Conn, opts Options) (*Conn, error) {
	return open(ctx, conn, opts, wire.Client)
}

func open(ctx context.Context, tc transport.Conn, opts Options, role wire.Role) (*Conn, error) {
	if opts.Limits.MaxMessage == 0 {
		lim := DefaultLimits()
		lim.Files, lim.Network, lim.Commands = opts.Limits.Files, opts.Limits.Network, opts.Limits.Commands
		opts.Limits = lim
	}
	localName, peerName := FacetHost, FacetExtension
	if role == wire.Client {
		localName, peerName = FacetExtension, FacetHost
	}
	local, err := FacetDef(localName)
	if err != nil {
		return nil, err
	}
	c := &Conn{opts: opts, local: local, lb: newBinder(local)}
	sc := &schema.Schema{Format: 2, Name: localName, Version: Version, Facets: []schema.Facet{*local}, Annotations: schema.Annotations{}}
	var params wire.Params
	params.Set(wire.ParamMaxMessageSize, uint64(max64(opts.Limits.MaxMessage, 16384)))
	params.Set(wire.ParamMaxConcurrentCalls, uint64(max64(opts.Limits.MaxInFlight, 1)))
	mux := session.NewMux()
	mux.HandleFacet(local.ID, session.HandlerFunc(c.serve))
	cfg := session.Config{
		Schema:  sc,
		Params:  params,
		Handler: mux,
		// Both ends of a pipe are one machine's processes: the facets are
		// as large as the messages say, and the idle timer never fires on
		// an extension that's busy computing.
		MaxClientSchemaSize: -1,
	}
	if role == wire.Server {
		cfg.AcceptClientSchema = func(_ context.Context, _ string, s *schema.Schema) error {
			for i := range s.Facets {
				if s.Facets[i].Name == FacetExtension {
					return nil
				}
			}
			return &session.Error{Code: status.SchemaMismatch, Message: "the process's Hello doesn't publish " + FacetExtension}
		}
	}
	t := opts.HandshakeTimeout
	if t == 0 {
		t = 30 * time.Second
	}
	hctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	var sess *session.Session
	if role == wire.Server {
		sess, err = session.NewServer(hctx, tc, cfg)
	} else {
		sess, err = session.NewClient(hctx, tc, cfg)
	}
	if err != nil {
		_ = tc.Close(status.Unavailable, "handshake failed")
		return nil, err
	}
	c.sess = sess
	peer := sess.PeerFacet(peerName)
	if peer == nil {
		sess.GoAway(status.SchemaMismatch, "no facet "+peerName, nil)
		return nil, fmt.Errorf("the peer doesn't publish %s", peerName)
	}
	c.peer, c.pb = peer, newBinder(peer)
	return c, nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// FacetDef returns the facet heron.extension or heron.host, as the
// messages define it.
func FacetDef(name string) (*schema.Facet, error) {
	var b []byte
	switch name {
	case FacetExtension:
		b = facetExtensionCBOR
	case FacetHost:
		b = facetHostCBOR
	default:
		return nil, fmt.Errorf("no facet %s", name)
	}
	return schema.DecodeFacet(b)
}

// Session returns the Sparrow session.
func (c *Conn) Session() *session.Session { return c.sess }

// Done is closed when the session has ended.
func (c *Conn) Done() <-chan struct{} { return c.sess.Done() }

// Err returns why the session ended.
func (c *Conn) Err() error { return c.sess.Err() }

// Close ends the session with GoAway.
func (c *Conn) Close() { c.sess.GoAway(status.GoingAway, "closed", nil) }

func (c *Conn) method(name string) (*schema.Method, error) {
	for i := range c.peer.Services {
		for j := range c.peer.Services[i].Methods {
			if m := &c.peer.Services[i].Methods[j]; m.Name == name {
				return m, nil
			}
		}
	}
	return nil, &Error{Code: CodeMethodNotFound, Message: fmt.Sprintf("method %q isn't served", name)}
}

func (c *Conn) callOptions(ctx context.Context) *session.CallOptions {
	// Over the peer's max_in_flight a call fails at once with the
	// limit-exceeded code rather than waiting for a slot.
	o := &session.CallOptions{NoWait: true}
	if run := RunOf(ctx); run != "" {
		o.Metadata = wire.Metadata{wire.TextKey(RunKey): cbor.AppendText(nil, run)}
	}
	return o
}

func (c *Conn) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); !ok {
		if t := c.opts.Limits.TimeoutMs; t > 0 {
			return context.WithTimeout(ctx, time.Duration(t)*time.Millisecond)
		}
	}
	return ctx, func() {}
}

// Call calls a method of the peer's facet and decodes its result into
// result, which may be nil. Cancelling ctx sends Cancel; a context
// without a deadline gets the limits' timeout as the call's deadline.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	m, err := c.method(method)
	if err != nil {
		return err
	}
	if m.Output.IsStream() {
		// A stream read as one value: its items as a list.
		var items []any
		err := c.CallStream(ctx, method, params, func(decode func(any) error) error {
			var v any
			if err := decode(&v); err != nil {
				return err
			}
			items = append(items, v)
			return nil
		})
		if err == nil && result != nil {
			reflect.ValueOf(result).Elem().Set(reflect.ValueOf(any(items)))
		}
		return err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	in, err := c.pb.encode(m.Input, reflect.ValueOf(params))
	if err != nil {
		return &Error{Code: CodeInvalidParams, Message: fmt.Sprintf("%s: %v", method, err)}
	}
	out, err := c.sess.Unary(ctx, c.peer.ID, m.ID, in, c.callOptions(ctx))
	if err != nil {
		return c.failure(ctx, method, err)
	}
	if result != nil {
		if err := c.pb.decode(m.Output, out, reflect.ValueOf(result).Elem()); err != nil {
			return &Error{Code: CodeParseError, Message: fmt.Sprintf("%s: %v", method, err)}
		}
	}
	return nil
}

// CallStream calls a method whose result streams; item is called with
// each item, in order, before CallStream returns.
func (c *Conn) CallStream(ctx context.Context, method string, params any, item func(decode func(any) error) error) error {
	m, err := c.method(method)
	if err != nil {
		return err
	}
	if !m.Output.IsStream() {
		return fmt.Errorf("protocol: %s doesn't stream its result", method)
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	in, err := c.pb.encode(m.Input, reflect.ValueOf(params))
	if err != nil {
		return &Error{Code: CodeInvalidParams, Message: fmt.Sprintf("%s: %v", method, err)}
	}
	cc, err := c.sess.Call(ctx, c.peer.ID, m.ID, in, c.callOptions(ctx))
	if err != nil {
		return c.failure(ctx, method, err)
	}
	for {
		x, err := cc.Recv(ctx)
		if errors.Is(err, io.EOF) {
			if err := cc.Wait(ctx); err != nil {
				return c.failure(ctx, method, err)
			}
			return nil
		}
		if err != nil {
			return c.failure(ctx, method, err)
		}
		if item != nil {
			if err := item(func(v any) error { return c.pb.decode(*m.Output.Elem, x, reflect.ValueOf(v).Elem()) }); err != nil {
				_ = cc.Cancel(status.Cancelled)
				return err
			}
		}
	}
}

// Notify sends a notification the peer's facet declares, such as log.
func (c *Conn) Notify(method string, params any) error {
	m, err := c.method(method)
	if err != nil {
		return err
	}
	in, err := c.pb.encode(m.Input, reflect.ValueOf(params))
	if err != nil {
		return err
	}
	// Notifications are sent in order.
	c.notify.Lock()
	defer c.notify.Unlock()
	return c.sess.Notify(context.Background(), c.peer.ID, m.ID, in)
}

// failure turns a failed call into what callers check: an *Error with the
// protocol's code, or ErrClosed once the session has ended.
func (c *Conn) failure(ctx context.Context, method string, err error) error {
	if ce := ctx.Err(); ce != nil {
		if errors.Is(ce, context.DeadlineExceeded) {
			return &Error{Code: CodeTimeout, Message: fmt.Sprintf("%s timed out", method)}
		}
		return &Error{Code: CodeCancelled, Message: fmt.Sprintf("%s was cancelled", method)}
	}
	var se *session.Error
	if !errors.As(err, &se) {
		return err
	}
	if len(se.Details) > 0 {
		if e := c.details(se); e != nil {
			return e
		}
	}
	select {
	case <-c.sess.Done():
		if !se.Remote || se.Code == status.Unavailable || se.Code == status.GoingAway {
			return fmt.Errorf("%w: %v", ErrClosed, c.sess.Err())
		}
	default:
	}
	return &Error{Code: codeOf(se.Code), Message: se.Message}
}

// details reads an Error's details, ErrorDetails as a dynamic value of
// either facet.
func (c *Conn) details(se *session.Error) *Error {
	for _, b := range []*binder{c.pb, c.lb} {
		id, ok := b.typeNamed("ErrorDetails")
		if !ok {
			continue
		}
		codec, err := value.New(b.f)
		if err != nil {
			continue
		}
		x, err := codec.DecodeInner(schema.Named(schema.TypeDynamic), se.Details)
		if err != nil {
			continue
		}
		d, ok := x.(value.Dynamic)
		if !ok || d.Type != id {
			continue
		}
		var ed ErrorDetails
		if err := b.decode(schema.Named(id), d.Value, reflect.ValueOf(&ed).Elem()); err != nil {
			continue
		}
		return &Error{Code: ed.Code, Message: se.Message, Type: ed.Type, Data: ed.Data}
	}
	return nil
}

// sparrowError is a handler's failure as Sparrow's Error, with
// ErrorDetails.
func (c *Conn) sparrowError(err error) *session.Error {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: CodeInternalError, Message: err.Error()}
	}
	out := &session.Error{Code: statusOf(e.Code), Message: e.Message}
	if id, ok := c.lb.typeNamed("ErrorDetails"); ok {
		if st, err := c.lb.encode(schema.Named(id), reflect.ValueOf(&ErrorDetails{Code: e.Code, Type: e.Type, Data: e.Data})); err == nil {
			if codec, err := value.New(c.local); err == nil {
				if raw, err := codec.EncodeInner(schema.Named(schema.TypeDynamic), value.Dynamic{Type: id, Value: st}); err == nil {
					out.Details = raw
				}
			}
		}
	}
	return out
}

// serve answers a call of this side's facet with its handler.
func (c *Conn) serve(ctx context.Context, sc *session.ServerCall) (any, error) {
	m := sc.Method()
	if run, ok := sc.Metadata()[wire.TextKey(RunKey)]; ok {
		var s string
		if v, err := cbor.Decode(run, cbor.Options{}); err == nil {
			s, _ = v.(string)
		}
		if s != "" {
			ctx = WithRun(ctx, s)
		}
	}
	ctx = context.WithValue(ctx, callKey, &served{sc: sc, b: c.lb, method: m})
	in, err := sc.Input()
	if err != nil {
		return nil, c.sparrowError(&Error{Code: CodeInvalidParams, Message: fmt.Sprintf("%s: %v", m.Name, err)})
	}
	decode := func(v any) error {
		if err := c.lb.decode(m.Input, in, reflect.ValueOf(v).Elem()); err != nil {
			return &Error{Code: CodeInvalidParams, Message: fmt.Sprintf("%s: %v", m.Name, err)}
		}
		return nil
	}
	if m.IsNotification() {
		if f := c.opts.Notifications[m.Name]; f != nil {
			f(decode)
		}
		return nil, nil
	}
	h := c.opts.Handlers[m.Name]
	if h == nil {
		return nil, c.sparrowError(&Error{Code: CodeMethodNotFound, Message: fmt.Sprintf("method %q isn't served", m.Name)})
	}
	res, err := h(ctx, decode)
	if ctx.Err() != nil && err == nil {
		err = &Error{Code: CodeCancelled, Message: "cancelled"}
	}
	if err != nil {
		return nil, c.sparrowError(err)
	}
	if m.Output.IsStream() {
		return nil, nil
	}
	out, err := c.lb.encode(m.Output, reflect.ValueOf(res))
	if err != nil {
		return nil, c.sparrowError(&Error{Code: CodeInternalError, Message: fmt.Sprintf("%s: the result: %v", m.Name, err)})
	}
	if out == nil {
		out = zeroOf(m.Output)
	}
	return out, nil
}
