package protocol

import (
	"context"
	"errors"
	"io"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"
)

func pair(t *testing.T, wire Wire, a, b map[string]Handler, limits Limits) (*Conn, *Conn) {
	t.Helper()
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	ca := NewConn(ar, aw, Options{Wire: wire, Handlers: a, Limits: limits})
	cb := NewConn(br, bw, Options{Wire: wire, Handlers: b, Limits: limits})
	go ca.Serve()
	go cb.Serve()
	t.Cleanup(func() { aw.Close(); bw.Close() })
	return ca, cb
}

type echoHost struct{}

func (echoHost) Node(ctx context.Context, p *NodeParams) (*Node, error) {
	return &Node{Path: p.Path, Kind: "types.type"}, nil
}
func (echoHost) Call(ctx context.Context, p *CallParams) (Value, error) {
	switch p.Target {
	case "throw":
		return nil, &Error{Code: CodeThrown, Message: "not found", Type: "NotFound", Data: map[string]any{"message": "not found"}}
	case "slow":
		<-ctx.Done()
		return nil, ctx.Err()
	case "stream":
		for i := 0; i < 3; i++ {
			if err := StreamItem(ctx, i); err != nil {
				return nil, err
			}
		}
		return nil, nil
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

func TestCallsBothWays(t *testing.T) {
	for _, wire := range []Wire{CBOR, JSON} {
		t.Run(wire.Name(), func(t *testing.T) {
			a, b := pair(t, wire, HostHandlers(echoHost{}), HostHandlers(echoHost{}), DefaultLimits())
			ctx := context.Background()
			for _, c := range []*Conn{a, b} {
				n, err := HostClient{c}.Node(ctx, &NodeParams{Path: "store.pets"})
				if err != nil || n.Path != "store.pets" || n.Kind != "types.type" {
					t.Fatalf("node: %v %+v", err, n)
				}
				s, err := HostClient{c}.Secret(ctx, &NameParams{Name: "x"})
				if err != nil || s != "s3cret" {
					t.Fatalf("secret: %v %q", err, s)
				}
			}
		})
	}
}

func TestValuesKeepTheirTypes(t *testing.T) {
	a, _ := pair(t, CBOR, nil, HostHandlers(echoHost{}), DefaultLimits())
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	dec, _ := ParseDecimal("40.00")
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, v := range []any{
		int64(-7), int64(12), int64(13), int64(32), uint64(1) << 63, huge, 1.5, "text", []byte{0, 1, 2}, true, nil,
		dec, when, Duration(1500), Ref("store.pets"), Set{"a", "b"},
		Variant{Name: "card", Value: map[string]any{"last4": "4242"}},
		Prose{"type": "root", "children": []any{}},
		[]any{"x", int64(1)}, map[string]any{"k": "v"},
	} {
		var got any
		err := a.Call(context.Background(), MethodCall, &CallParams{Target: "echo", Args: map[string]Value{"value": v}}, &got)
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

func TestThrownErrorsCarryTheirType(t *testing.T) {
	a, _ := pair(t, CBOR, nil, HostHandlers(echoHost{}), DefaultLimits())
	_, err := HostClient{a}.Call(context.Background(), &CallParams{Target: "throw"})
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeThrown || e.Type != "NotFound" {
		t.Fatalf("got %v", err)
	}
	if m, ok := e.Data.(map[string]any); !ok || m["message"] != "not found" {
		t.Fatalf("data %#v", e.Data)
	}
	err = a.Call(context.Background(), "nope", nil, nil)
	if !errors.As(err, &e) || e.Code != CodeMethodNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestCancellationReachesTheHandler(t *testing.T) {
	a, b := pair(t, CBOR, nil, HostHandlers(echoHost{}), DefaultLimits())
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := HostClient{a}.Call(ctx, &CallParams{Target: "slow"})
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeCancelled {
		t.Fatalf("got %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.mu.Lock()
		n := len(b.serving)
		b.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the handler kept running after $/cancel")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTimeoutFromLimits(t *testing.T) {
	l := DefaultLimits()
	l.TimeoutMs = 50
	a, _ := pair(t, CBOR, nil, HostHandlers(echoHost{}), l)
	_, err := HostClient{a}.Call(context.Background(), &CallParams{Target: "slow"})
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeTimeout {
		t.Fatalf("got %v", err)
	}
}

func TestStreaming(t *testing.T) {
	a, _ := pair(t, CBOR, nil, HostHandlers(echoHost{}), DefaultLimits())
	var items []int64
	err := a.CallStream(context.Background(), MethodCall, &CallParams{Target: "stream"}, nil, func(decode func(any) error) error {
		var i int64
		if err := decode(&i); err != nil {
			return err
		}
		items = append(items, i)
		return nil
	})
	if err != nil || !reflect.DeepEqual(items, []int64{0, 1, 2}) {
		t.Fatalf("%v %v", err, items)
	}
}

func TestRunIDTravels(t *testing.T) {
	a, _ := pair(t, CBOR, nil, HostHandlers(echoHost{}), DefaultLimits())
	got, err := HostClient{a}.Call(WithRun(context.Background(), "run-42"), &CallParams{Target: "run"})
	if err != nil || got != "run-42" {
		t.Fatalf("%v %v", err, got)
	}
}

func TestFrameLimit(t *testing.T) {
	l := DefaultLimits()
	l.MaxFrame = 1024
	a, _ := pair(t, CBOR, nil, HostHandlers(echoHost{}), l)
	_, err := HostClient{a}.Call(context.Background(), &CallParams{Target: "echo", Args: map[string]Value{"value": strings.Repeat("x", 2048)}})
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeLimitExceeded {
		t.Fatalf("got %v", err)
	}
}

func TestClosedConnectionFailsCallsInFlight(t *testing.T) {
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	go io.Copy(io.Discard, br)
	a := NewConn(ar, aw, Options{})
	go a.Serve()
	errc := make(chan error, 1)
	go func() { errc <- a.Call(context.Background(), MethodCall, &CallParams{Target: "x"}, nil) }()
	time.Sleep(20 * time.Millisecond)
	bw.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the call didn't fail")
	}
}

// The edges of the protocol: the frame's header, a method nobody serves, a
// handler that fails, more requests than may be in flight, a payload that
// isn't a message, and the $/log and $/progress notifications.
func TestProtocolEdges(t *testing.T) {
	var b strings.Builder
	if err := WriteFrame(&b, []byte("hi"), 0); err != nil || b.String() != "\x00\x00\x00\x02hi" {
		t.Errorf("frame %q %v", b.String(), err)
	}
	ctx := context.Background()
	release := make(chan struct{})
	handlers := map[string]Handler{
		"fail": func(ctx context.Context, decode func(any) error) (any, error) { return nil, errors.New("boom") },
		"wait": func(ctx context.Context, decode func(any) error) (any, error) { <-release; return "done", nil },
	}
	l := DefaultLimits()
	l.MaxInFlight = 1
	a, _ := pair(t, CBOR, nil, handlers, l)
	codeOf := func(err error) int64 {
		var e *Error
		if errors.As(err, &e) {
			return e.Code
		}
		return 0
	}
	if err := a.Call(ctx, "nope", &Empty{}, nil); codeOf(err) != CodeMethodNotFound {
		t.Errorf("unknown method: %v", err)
	}
	if err := a.Call(ctx, "fail", &Empty{}, nil); codeOf(err) != CodeInternalError || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failing handler: %v", err)
	}
	first := make(chan error, 1)
	go func() { first <- a.Call(ctx, "wait", &Empty{}, nil) }()
	time.Sleep(20 * time.Millisecond)
	if err := a.Call(ctx, "wait", &Empty{}, nil); codeOf(err) != CodeLimitExceeded {
		t.Errorf("over max_in_flight: %v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Errorf("the first request: %v", err)
	}

	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	got := make(chan string, 4)
	c := NewConn(ar, aw, Options{Notifications: map[string]func(func(any) error){
		MethodLog: func(decode func(any) error) {
			var p LogParams
			decode(&p)
			got <- "log " + string(p.Level) + " " + p.Message
		},
		MethodProgress: func(decode func(any) error) {
			var p ProgressParams
			decode(&p)
			got <- "progress " + p.Message
		},
	}})
	go c.Serve()
	t.Cleanup(func() { aw.Close(); bw.Close() })
	go func() {
		WriteFrame(bw, []byte{0xff, 0x00}, 0)
		for _, n := range []any{
			&Message{Method: MethodLog, Params: mustRaw(t, &LogParams{Level: "warning", Message: "careful"})},
			&Message{Method: MethodProgress, Params: mustRaw(t, &ProgressParams{ID: 1, Message: "half"})},
		} {
			p, _ := CBOR.Marshal(n)
			WriteFrame(bw, p, 0)
		}
	}()
	reply, err := ReadFrame(br, 0)
	var m Message
	if err != nil || CBOR.Unmarshal(reply, &m) != nil || m.Error == nil || m.Error.Code != CodeParseError {
		t.Errorf("unreadable payload: %v %+v", err, m.Error)
	}
	for _, want := range []string{"log warning careful", "progress half"} {
		select {
		case g := <-got:
			if g != want {
				t.Errorf("got %q, want %q", g, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no %q", want)
		}
	}
}

func mustRaw(t *testing.T, v any) Raw {
	b, err := CBOR.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return Raw(b)
}
