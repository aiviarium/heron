package eval_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/helightdev/specs-v0/internal/core/eval"
	"github.com/helightdev/specs-v0/internal/core/registry"
)

const transportSrc = `import = ["std/schema@1", "std/types@1", "std/apis@1"]

wire: package(version = "1.0") {
  carrier: transport
  over_wire: annotation(on = [types.api, types.op])
}

twin: package(version = "1.0") {
  left: transport
  right: transport
  over_twin: annotation(on = [types.api, types.op])
}

@wire.over_wire
stock: api {
  count(sku: String): op(Int)
  @twin.over_twin
  both(sku: String): op(Int)
}

plain: api {
  count(sku: String): op(Int)
}

bare: package(version = "1.0") {
  over_bare: annotation(on = [types.op])
}

ghost: package(version = "1.0") {
  phantom: transport
  over_ghost: annotation(on = [types.op])
}

loose: api {
  @bare.over_bare
  count(sku: String): op(Int)
  @ghost.over_ghost
  haunted(sku: String): op(Int)
}

look(sku: String): func(Int) do {
  return stock.count(sku = sku)
}
`

func TestOpCallsGoThroughTheBoundTransport(t *testing.T) {
	r := registry.New()
	var sent []string
	if err := r.SetTransport(registry.Transport{Path: "wire.carrier", Send: func(ctx context.Context, op string, req any) (any, error) {
		sku := req.(map[string]any)["sku"].(string)
		sent = append(sent, op+" "+sku)
		if sku == "gone" {
			return nil, &registry.Thrown{Type: "NotFound", Fields: map[string]any{"message": "no " + sku}}
		}
		return int64(len(sku)), nil
	}}); err != nil {
		t.Fatal(err)
	}
	h := load(t, transportSrc, r)
	want(t, h.mustCall("look", map[string]eval.Value{"sku": "kib-1"}), int64(5))
	want(t, sent, []string{"stock.count kib-1"})

	var foreign []eval.Event
	for _, e := range h.events {
		if e.Kind == eval.EventForeign {
			foreign = append(foreign, e)
		}
	}
	if len(foreign) != 1 || foreign[0].Name != "wire.carrier.send" || foreign[0].Args["op"] != "stock.count" || foreign[0].Value != int64(5) {
		t.Errorf("foreign events: %+v", foreign)
	}

	_, err := h.call("look", map[string]eval.Value{"sku": "gone"})
	var th *eval.Throw
	if !errors.As(err, &th) || th.Value.Type != "NotFound" {
		t.Errorf("a thrown error from the transport: %v", err)
	}

	for path, msg := range map[string]string{
		"plain.count":   "has no binding",
		"stock.both":    "several transports (twin.left, twin.right, wire.carrier)",
		"loose.count":   "is bound by @bare.over_bare, but no transport of those packages is declared",
		"loose.haunted": "is carried by transport ghost.phantom, but nothing implements it",
	} {
		_, err := h.call(path, map[string]eval.Value{"sku": "x"})
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: want an error saying %q, got %v", path, msg, err)
		}
	}
}

func TestPureForeignFuncsAtEvaluationTime(t *testing.T) {
	r := registry.New()
	calls := map[string]int{}
	foreign := func(name string) {
		r.MustAddFunc(registry.Func{Name: name, Foreign: "test", Call: func(_ context.Context, c *registry.FuncCall) (any, error) {
			calls[name]++
			return strings.ToUpper(c.Args["text"].(string)), nil
		}})
	}
	foreign("shout")
	foreign("noisy")
	h := load(t, `shout(text: String): func<pure>(String)
noisy(text: String): func(String)

a: String = shout(text = "hi")
b: String = shout(text = "hi")
c: String = noisy(text = "hi")

both: func(String) do {
  return "$a $b"
}

bad: func(String) do {
  return c
}

direct: func(String) do {
  return noisy(text = "yo")
}
`, r)
	want(t, h.mustCall("both", nil), "HI HI")
	if calls["shout"] != 1 {
		t.Errorf("the pure func ran %d times for equal inputs; its cache should serve the second", calls["shout"])
	}
	if _, err := h.call("bad", nil); err == nil || !strings.Contains(err.Error(), "isn't <pure>") {
		t.Errorf("an impure foreign func while evaluating: %v", err)
	}
	want(t, h.mustCall("direct", nil), "YO")
}

func TestCallPathServesForeignCallbacks(t *testing.T) {
	r := registry.New()
	// A foreign func that calls back into Heron from another goroutine, as
	// the protocol's handler does, while the machine waits for it.
	r.MustAddFunc(registry.Func{Name: "outer", Foreign: "test", Call: func(ctx context.Context, c *registry.FuncCall) (any, error) {
		m := eval.MachineOf(ctx)
		type res struct {
			v   any
			err error
		}
		ch := make(chan res)
		go func() {
			v, err := m.CallPath(context.Background(), "label", map[string]any{"n": int64(3)})
			ch <- res{v, err}
		}()
		got := <-ch
		if got.err != nil {
			return nil, got.err
		}
		go func() {
			_, err := m.CallPath(context.Background(), "fail", nil)
			ch <- res{nil, err}
		}()
		if th := (<-ch).err; !errorsAsThrown(th) {
			return nil, errors.New("fail didn't come back as a thrown value")
		}
		if _, err := m.CallPath(context.Background(), "a_task", nil); err == nil {
			return nil, errors.New("a task was callable from foreign code")
		}
		return got.v, nil
	}})
	r.MustAddFunc(registry.Func{Name: "fail", Call: func(context.Context, *registry.FuncCall) (any, error) {
		return nil, &registry.Thrown{Type: "Oops", Fields: map[string]any{"message": "no"}}
	}})
	h := load(t, `import = ["std/types@1"]

outer: func(String)

label(n: Int): func(String) = "n=$n"

fail: func(String)

run_me: func(String) do {
  return outer()
}

a_task: task do {
  log("x")
}
`, r)
	want(t, h.mustCall("run_me", nil), "n=3")
}

func errorsAsThrown(err error) bool {
	var th *registry.Thrown
	return errors.As(err, &th) && th.Type == "Oops"
}

// A pure func's failure isn't kept, so the next call with the same inputs
// reaches the foreign code again, and emit works only inside a macro.
func TestPureFailuresAreNotCached(t *testing.T) {
	r := registry.New()
	calls := 0
	r.MustAddFunc(registry.Func{Name: "flaky", Foreign: "test", Call: func(_ context.Context, c *registry.FuncCall) (any, error) {
		calls++
		if calls == 1 {
			return nil, &registry.Thrown{Type: "Busy", Fields: map[string]any{"message": "busy"}}
		}
		return "ok", nil
	}})
	h := load(t, `flaky(text: String): func<pure>(String)

twice: func(List(Any)) do {
  let first = attempt(flaky(text = "a"))
  return [first.error.message, flaky(text = "a"), flaky(text = "a")]
}

stray: func do {
  emit("x: Int")
}
`, r)
	want(t, h.mustCall("twice", nil), []eval.Value{"busy", "ok", "ok"})
	if calls != 2 {
		t.Errorf("the foreign code ran %d times; want 2, the failure uncached and the success cached", calls)
	}
	if _, err := h.call("stray", nil); err == nil || !strings.Contains(err.Error(), "only inside a macro") {
		t.Errorf("emit outside a macro: %v", err)
	}
}

// A func bound to a module whose kind no host serves fails as it's called.
func TestForeignFuncWithoutAHost(t *testing.T) {
	h := load(t, `import = ["std/js@1"]

tools: module = js("./tools/orders.ts")

export_orders(pet_id: Int): func(Any) = tools.exportOrders
`, registry.New())
	if _, err := h.call("export_orders", map[string]eval.Value{"pet_id": int64(1)}); err == nil || !strings.Contains(err.Error(), "no host for js modules is registered") {
		t.Errorf("got %v", err)
	}
}
