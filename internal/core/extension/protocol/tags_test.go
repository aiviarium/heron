package protocol

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"
	"time"
)

// Each Heron value goes on the CBOR wire under its tag: the standard tags
// where they exist, and the Heron tags 1214603777 to 1214603780 for the
// rest. On the JSON wire, the same values are objects with a "$" key.
func TestCBORTags(t *testing.T) {
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	dec, _ := ParseDecimal("40.00")
	cases := []struct {
		v      any
		prefix string // the tag's head, in hex
		json   string
	}{
		{time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "c0", `"2026-01-02T03:04:05Z"`},
		{huge, "c2", ``},
		{new(big.Int).Neg(huge), "c3", ``},
		{dec, "c4", `{"$decimal":"40.00"}`},
		{Set{"a"}, "d90102", `{"$set":["a"]}`},
		{Variant{Name: "card", Value: int64(1)}, "da48656201", `{"$variant":"card","value":1}`},
		{Duration(1500), "da48656202", `{"$duration":1500}`},
		{Prose{"type": "root"}, "da48656203", `{"$prose":{"type":"root"}}`},
		{Ref("store.pets"), "da48656204", `{"$ref":"store.pets"}`},
	}
	for _, c := range cases {
		b, err := CBOR.Marshal(c.v)
		if err != nil {
			t.Fatalf("%T: %v", c.v, err)
		}
		want, _ := hex.DecodeString(c.prefix)
		if !bytes.HasPrefix(b, want) {
			t.Errorf("%T: CBOR starts %x, want the tag %s", c.v, b, c.prefix)
		}
		if c.json == "" {
			continue
		}
		j, err := JSON.Marshal(c.v)
		if err != nil || string(j) != c.json {
			t.Errorf("%T: JSON %s %v, want %s", c.v, j, err, c.json)
		}
	}
}

// Map keys are written in canonical order, so equal values encode to
// equal bytes whatever order a Go map yields them in.
func TestCanonicalMapOrder(t *testing.T) {
	want, _ := hex.DecodeString("a3616101616202616303")
	for i := 0; i < 20; i++ {
		b, err := CBOR.Marshal(map[string]any{"c": 3, "a": 1, "b": 2})
		if err != nil || !bytes.Equal(b, want) {
			t.Fatalf("got %x %v, want %x", b, err, want)
		}
	}
}
