package eval_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/eval"
	"github.com/aiviarium/heron/internal/core/registry"
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
