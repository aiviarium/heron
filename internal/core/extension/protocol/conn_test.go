package protocol

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aiviarium/sparrow/go/cbor"
	"github.com/aiviarium/sparrow/go/schema"
	"github.com/aiviarium/sparrow/go/transport/pipe"
	"github.com/aiviarium/sparrow/go/value"
	"github.com/aiviarium/sparrow/go/wire"
)

// pair connects heron's side, serving host, to an extension's side,
// serving ext, over the pipe binding.
func pair(t *testing.T, host, ext map[string]Handler, limits Limits) (*Conn, *Conn) {
	t.Helper()
	return pairWith(t, Options{Handlers: host, Limits: limits}, Options{Handlers: ext, Limits: limits})
}

func pairWith(t *testing.T, hopts, eopts Options) (*Conn, *Conn) {
	t.Helper()
	a, b := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type res struct {
		c   *Conn
		err error
	}
	hc := make(chan res, 1)
	go func() {
		c, err := Accept(ctx, pipe.New(a, nil), hopts)
		hc <- res{c, err}
	}()
	e, err := Dial(ctx, pipe.New(b, nil), eopts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	h := <-hc
	if h.err != nil {
		t.Fatalf("accept: %v", h.err)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	return h.c, e
}

type echoHost struct{}

func (echoHost) Node(ctx context.Context, p *NodeParams) (*Node, error) {
	if p.Path == "nothing" {
		return nil, nil
	}
	return &Node{Path: p.Path, Kind: "types.type", Children: []Node{{Path: p.Path + ".id", Name: "id"}}}, nil
}
func (echoHost) Call(ctx context.Context, p *CallParams) (Value, error) {
	switch p.Target {
	case "throw":
		return nil, &Error{Code: CodeThrown, Message: "not found", Type: "NotFound", Data: map[string]any{"message": "not found"}}
	case "slow":
		<-ctx.Done()
		return nil, ctx.Err()
	case "run":
		return RunOf(ctx), nil
	}
	return p.Args["value"], nil
}
func (echoHost) Resource(ctx context.Context, p *NameParams) (Value, error) { return p.Name, nil }
func (echoHost) Secret(ctx context.Context, p *NameParams) (string, error)  { return "s3cret", nil }
func (echoHost) Temp(ctx context.Context, p *NameParams) (string, error) {
	return "/tmp/" + p.Name, nil
}

// ext is an extension that streams, echoes and answers initialize.
func extHandlers() map[string]Handler {
	return map[string]Handler{
		MethodInitialize: func(ctx context.Context, decode func(any) error) (any, error) {
			var p InitializeParams
			if err := decode(&p); err != nil {
				return nil, err
			}
			out := &InitializeResult{Protocol: p.Protocol, Extension: PeerInfo{Name: "ext", Version: "1"}}
			for _, d := range p.Declarations {
				out.Implements = append(out.Implements, Implemented{Path: d.Path, Point: d.Point})
			}
			return out, nil
		},
		MethodCall: func(ctx context.Context, decode func(any) error) (any, error) {
			var p CallParams
			if err := decode(&p); err != nil {
				return nil, err
			}
			return p.Args["value"], nil
		},
		MethodCallStream: func(ctx context.Context, decode func(any) error) (any, error) {
			for i := 0; i < 3; i++ {
				if err := StreamItem(ctx, int64(i)); err != nil {
					return nil, err
				}
			}
			return nil, nil
		},
	}
}

// Each side publishes its facet and calls the other's: the extension
// heron's host, heron the extension's.
func TestCallsBothWays(t *testing.T) {
	h, e := pair(t, HostHandlers(echoHost{}), extHandlers(), DefaultLimits())
	ctx := context.Background()
	n, err := HostClient{e}.Node(ctx, &NodeParams{Path: "store.pets"})
	if err != nil || n.Path != "store.pets" || n.Kind != "types.type" || len(n.Children) != 1 || n.Children[0].Name != "id" {
		t.Fatalf("node: %v %+v", err, n)
	}
	if n, err := (HostClient{e}).Node(ctx, &NodeParams{Path: "nothing"}); err != nil || n.Path != "" {
		t.Fatalf("no node: %v %+v", err, n)
	}
	s, err := HostClient{e}.Secret(ctx, &NameParams{Name: "x"})
	if err != nil || s != "s3cret" {
		t.Fatalf("secret: %v %q", err, s)
	}
	res, err := ExtensionClient{h}.Initialize(ctx, &InitializeParams{Protocol: Version, Root: "/r",
		Declarations: []Declaration{{Path: "a.f", Point: PointFunc}, {Path: "a.l", Point: PointLint}}, Limits: DefaultLimits()})
	if err != nil || res.Protocol != Version || len(res.Implements) != 2 || res.Implements[1].Point != PointLint {
		t.Fatalf("initialize: %v %+v", err, res)
	}
	// Calls are numbered as Sparrow does: the extension, the client, even;
	// heron, the server, odd.
	if h.Session().Role() != wire.Server || e.Session().Role() != wire.Client {
		t.Fatal("heron is the server, the process the client")
	}
}

// Both facets are generated from the messages, valid, and the ones each
// side publishes.
func TestFacets(t *testing.T) {
	h, e := pair(t, nil, nil, DefaultLimits())
	for _, c := range []struct {
		conn       *Conn
		local, far string
	}{{h, FacetHost, FacetExtension}, {e, FacetExtension, FacetHost}} {
		if c.conn.local.Name != c.local || c.conn.peer.Name != c.far || c.conn.peer.ID != schema.FirstFreeFacetID {
			t.Errorf("%s sees %s at %d", c.conn.local.Name, c.conn.peer.Name, c.conn.peer.ID)
		}
	}
	for _, name := range []string{FacetExtension, FacetHost} {
		f, err := FacetDef(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if f.Type(f.Types[0].ID).Name != "Value" {
			t.Errorf("%s: Value isn't first", name)
		}
	}
}

func TestValuesKeepTheirTypes(t *testing.T) {
	_, e := pair(t, HostHandlers(echoHost{}), nil, DefaultLimits())
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	dec, _ := ParseDecimal("40.00")
	when := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	for _, v := range []any{
		int64(-7), int64(12), int64(13), int64(32), uint64(1) << 63, huge, new(big.Int).Neg(huge), 1.5, "text", []byte{0, 1, 2}, true, nil,
		dec, when, Duration(1500), Ref("store.pets"), Set{"a", "b"},
		Variant{Name: "card", Value: map[string]any{"last4": "4242"}},
		Prose{"type": "root", "children": []any{}},
		[]any{"x", int64(1)}, map[string]any{"k": "v"},
	} {
		var got any
		err := e.Call(context.Background(), MethodCall, &CallParams{Target: "echo", Args: map[string]Value{"value": v}}, &got)
		if err != nil {
			t.Fatalf("%T: %v", v, err)
		}
		if d, ok := v.(Decimal); ok {
			if g, ok := got.(Decimal); !ok || g.String() != d.String() {
				t.Fatalf("decimal came back as %#v", got)
			}
			continue
		}
		if tm, ok := v.(time.Time); ok {
			if g, ok := got.(time.Time); !ok || !g.Equal(tm) {
				t.Fatalf("time came back as %#v", got)
			}
			continue
		}
		if !reflect.DeepEqual(normal(got), normal(v)) {
			t.Fatalf("%T %#v came back as %T %#v", v, v, got, got)
		}
	}
}

func normal(v any) any {
	switch v := v.(type) {
	case uint64:
		if v < 1<<63 {
			return int64(v)
		}
		return new(big.Int).SetUint64(v).String()
	case *big.Int:
		return v.String()
	}
	return v
}

// Each Heron value is its own variant of the Value union, so Sparrow's
// tags 1 and 37 are the only ones on the wire.
func TestCBORTags(t *testing.T) {
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	dec, _ := ParseDecimal("40.00")
	cases := []struct {
		v   any
		tag uint64
	}{
		{nil, vNull}, {true, vBool}, {int64(-3), vInt}, {uint64(1) << 63, vUint}, {huge, vBigint}, {new(big.Int).Neg(huge), vBigint},
		{2.5, vFloat}, {"s", vString}, {[]byte{1}, vBytes}, {dec, vDecimal},
		{time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), vTime}, {Duration(1500), vDuration},
		{[]any{int64(1)}, vList}, {Set{"a"}, vSet}, {map[string]any{"a": int64(1)}, vMap},
		{Variant{Name: "card", Value: int64(1)}, vVariant}, {Ref("store.pets"), vRef}, {Prose{"type": "root"}, vProse},
	}
	f, _ := FacetDef(FacetHost)
	codec, err := value.New(f)
	if err != nil {
		t.Fatal(err)
	}
	vid, _ := newBinder(f).typeNamed("Value")
	for _, c := range cases {
		u, err := encodeValue(c.v)
		if err != nil || u.Tag != c.tag {
			t.Errorf("%T: variant %d %v, want %d", c.v, u.Tag, err, c.tag)
			continue
		}
		b, err := codec.EncodeInner(schema.Named(vid), u)
		if err != nil {
			t.Errorf("%T: %v", c.v, err)
			continue
		}
		if err := cbor.Validate(b, cbor.Options{}); err != nil {
			t.Errorf("%T: not Sparrow's CBOR: %v", c.v, err)
		}
		x, err := codec.DecodeInner(schema.Named(vid), b)
		if err != nil {
			t.Errorf("%T: %v", c.v, err)
			continue
		}
		back, err := decodeValue(x)
		if err != nil {
			t.Errorf("%T: %v", c.v, err)
			continue
		}
		if c.v != nil && reflect.TypeOf(back) != reflect.TypeOf(c.v) && !(c.tag == vUint) {
			t.Errorf("%T came back as %T", c.v, back)
		}
	}
	if s, _ := encodeValue(dec); s.Value != "40.00" {
		t.Errorf("decimal %v", s.Value)
	}
}

// A map's entries are written in the order of their keys, so equal values
// encode to equal bytes whatever order a Go map yields them in.
func TestCanonicalMapOrder(t *testing.T) {
	f, _ := FacetDef(FacetHost)
	codec, _ := value.New(f)
	vid, _ := newBinder(f).typeNamed("Value")
	var first []byte
	for i := 0; i < 20; i++ {
		u, _ := encodeValue(map[string]any{"c": 3, "a": 1, "b": 2})
		b, err := codec.EncodeInner(schema.Named(vid), u)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = b
			if bytes.Index(b, []byte("a")) > bytes.Index(b, []byte("c")) {
				t.Fatalf("keys out of order: %x", b)
			}
		} else if !bytes.Equal(b, first) {
			t.Fatalf("got %x, want %x", b, first)
		}
	}
}

func TestThrownErrorsCarryTheirType(t *testing.T) {
	_, e := pair(t, HostHandlers(echoHost{}), nil, DefaultLimits())
	_, err := HostClient{e}.Call(context.Background(), &CallParams{Target: "throw"})
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != CodeThrown || pe.Type != "NotFound" || pe.Message != "not found" {
		t.Fatalf("got %v", err)
	}
	if m, ok := pe.Data.(map[string]any); !ok || m["message"] != "not found" {
		t.Fatalf("data %#v", pe.Data)
	}
	err = e.Call(context.Background(), "nope", nil, nil)
	if !errors.As(err, &pe) || pe.Code != CodeMethodNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestCancellationReachesTheHandler(t *testing.T) {
	stopped := make(chan struct{})
	host := map[string]Handler{
		MethodCall: func(ctx context.Context, decode func(any) error) (any, error) {
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		},
	}
	_, e := pair(t, host, nil, DefaultLimits())
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := HostClient{e}.Call(ctx, &CallParams{Target: "slow"})
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != CodeCancelled {
		t.Fatalf("got %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler kept running after Cancel")
	}
}

func TestTimeoutFromLimits(t *testing.T) {
	l := DefaultLimits()
	l.TimeoutMs = 50
	_, e := pair(t, HostHandlers(echoHost{}), nil, l)
	_, err := HostClient{e}.Call(context.Background(), &CallParams{Target: "slow"})
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != CodeTimeout {
		t.Fatalf("got %v", err)
	}
}

// A streamed result is call_stream's Items; a plain call of it reads them
// as a list.
func TestStreaming(t *testing.T) {
	h, _ := pair(t, nil, extHandlers(), DefaultLimits())
	var items []int64
	err := ExtensionClient{h}.CallStream(context.Background(), &CallParams{Target: "stream"}, func(v Value) error {
		items = append(items, v.(int64))
		return nil
	})
	if err != nil || !reflect.DeepEqual(items, []int64{0, 1, 2}) {
		t.Fatalf("%v %v", err, items)
	}
	var all any
	if err := h.Call(context.Background(), MethodCallStream, &CallParams{Target: "stream"}, &all); err != nil || !reflect.DeepEqual(all, []any{int64(0), int64(1), int64(2)}) {
		t.Fatalf("%v %#v", err, all)
	}
}

func TestRunIDTravels(t *testing.T) {
	_, e := pair(t, HostHandlers(echoHost{}), nil, DefaultLimits())
	got, err := HostClient{e}.Call(WithRun(context.Background(), "run-42"), &CallParams{Target: "run"})
	if err != nil || got != "run-42" {
		t.Fatalf("%v %v", err, got)
	}
}

func TestFrameLimit(t *testing.T) {
	l := DefaultLimits()
	l.MaxMessage = 16384
	_, e := pair(t, HostHandlers(echoHost{}), nil, l)
	_, err := HostClient{e}.Call(context.Background(), &CallParams{Target: "echo", Args: map[string]Value{"value": strings.Repeat("x", 20000)}})
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != CodeLimitExceeded {
		t.Fatalf("got %v", err)
	}
	// The session survives a refused message.
	if s, err := (HostClient{e}).Secret(context.Background(), &NameParams{Name: "x"}); err != nil || s != "s3cret" {
		t.Fatalf("after: %v %q", err, s)
	}
}

func TestClosedConnectionFailsCallsInFlight(t *testing.T) {
	entered := make(chan struct{})
	host := map[string]Handler{
		MethodCall: func(ctx context.Context, decode func(any) error) (any, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	a, b := net.Pipe()
	ctx := context.Background()
	hc := make(chan *Conn, 1)
	go func() {
		c, _ := Accept(ctx, pipe.New(a, nil), Options{Handlers: host})
		hc <- c
	}()
	e, err := Dial(ctx, pipe.New(b, nil), Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := <-hc
	errc := make(chan error, 1)
	go func() { errc <- e.Call(ctx, MethodCall, &CallParams{Target: "x"}, nil) }()
	<-entered
	a.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call didn't fail")
	}
	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("heron's side didn't end")
	}
}

// The edges of the protocol: a method nobody serves, a handler that fails,
// more calls than may be in flight, the log notification and Progress.
func TestProtocolEdges(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	logs := make(chan string, 4)
	host := map[string]Handler{
		MethodSecret: func(ctx context.Context, decode func(any) error) (any, error) { return nil, errors.New("boom") },
		MethodTemp: func(ctx context.Context, decode func(any) error) (any, error) {
			_ = Progress(ctx, 1, 2, "half")
			<-release
			return "done", nil
		},
	}
	l := DefaultLimits()
	l.MaxInFlight = 1
	_, e := pairWith(t, Options{Handlers: host, Limits: l, Notifications: map[string]func(func(any) error){
		MethodLog: func(decode func(any) error) {
			var p LogParams
			_ = decode(&p)
			logs <- string(p.Level) + " " + p.Message
		},
	}}, Options{Limits: l})
	codeOf := func(err error) int64 {
		var pe *Error
		if errors.As(err, &pe) {
			return pe.Code
		}
		return 0
	}
	if err := e.Call(ctx, MethodNode, &NodeParams{Path: "x"}, nil); codeOf(err) != CodeMethodNotFound {
		t.Errorf("unserved method: %v", err)
	}
	if err := e.Call(ctx, MethodSecret, &NameParams{}, nil); codeOf(err) != CodeInternalError || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failing handler: %v", err)
	}
	first := make(chan error, 1)
	go func() { first <- e.Call(ctx, MethodTemp, &NameParams{}, nil) }()
	time.Sleep(50 * time.Millisecond)
	if err := e.Call(ctx, MethodTemp, &NameParams{}, nil); codeOf(err) != CodeLimitExceeded {
		t.Errorf("over max_in_flight: %v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Errorf("the first call: %v", err)
	}
	if err := NotifyLog(e, &LogParams{Level: SeverityWarning, Message: "careful"}); err != nil {
		t.Fatal(err)
	}
	select {
	case g := <-logs:
		if g != "warning careful" {
			t.Errorf("got %q", g)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no log")
	}
}

// A process that doesn't speak Sparrow is refused in the handshake.
func TestUnreadableMessage(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go func() {
		_, _ = b.Write(wire.AppendFrame(nil, []byte{0xff, 0x00}))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Accept(ctx, pipe.New(a, nil), Options{HandshakeTimeout: 2 * time.Second}); err == nil {
		t.Fatal("accepted a peer that sent no Hello")
	}
}
