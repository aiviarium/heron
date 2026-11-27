// Package protocol is the extension protocol: the messages and methods
// generated from the spec package's language/interop/protocol_messages.heron,
// as the Sparrow facets heron.extension and heron.host, the values that
// cross the boundary as their Heron types, in the Value union, and the
// Sparrow session that carries them over a process's standard input and
// output (the pipe binding).
package protocol

//go:generate go run ./internal/protogen -root ../../../.. -go protocol_gen.go -ts ../../../../web/runtime/src/protocol.gen.ts

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Value is a Heron value as it crosses the boundary: nil, bool, int64,
// uint64 or *big.Int, float64, string, []byte, Decimal, time.Time,
// Duration, []any, Set, map[string]any, Variant, Ref or Prose.
type Value = any

// Variant is a value of a union: the variant's name and its value.
type Variant struct {
	_     struct{} `cbor:",toarray"`
	Name  string
	Value any
}

// Ref is a reference to a node, by path.
type Ref string

// Duration is a length of time in milliseconds.
type Duration int64

// Prose is an mdast tree, from its root node.
type Prose map[string]any

// Set is a set of values.
type Set []any

// Decimal is a decimal fraction: Mantissa × 10^Exp.
type Decimal struct {
	_        struct{} `cbor:",toarray"`
	Exp      int64
	Mantissa *big.Int
}

// ParseDecimal reads a decimal such as "40.00" or "-1.5".
func ParseDecimal(s string) (Decimal, error) {
	exp := int64(0)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		exp = -int64(len(s) - i - 1)
		s = s[:i] + s[i+1:]
	}
	m, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return Decimal{}, fmt.Errorf("not a decimal: %q", s)
	}
	return Decimal{Exp: exp, Mantissa: m}, nil
}

// String formats a decimal such as "40.00".
func (d Decimal) String() string {
	if d.Mantissa == nil {
		return "0"
	}
	s := new(big.Int).Abs(d.Mantissa).String()
	sign := ""
	if d.Mantissa.Sign() < 0 {
		sign = "-"
	}
	if d.Exp >= 0 {
		return sign + s + strings.Repeat("0", int(d.Exp))
	}
	n := int(-d.Exp)
	for len(s) <= n {
		s = "0" + s
	}
	return sign + s[:len(s)-n] + "." + s[len(s)-n:]
}

// In JSON, as `heron check --json` and logs print values, the typed values
// are objects with a "$" key.

func (v Variant) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"$variant": v.Name, "value": v.Value})
}

func (r Ref) MarshalJSON() ([]byte, error) { return json.Marshal(map[string]any{"$ref": string(r)}) }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"$duration": int64(d)})
}

func (p Prose) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"$prose": map[string]any(p)})
}

func (s Set) MarshalJSON() ([]byte, error) { return json.Marshal(map[string]any{"$set": []any(s)}) }

func (d Decimal) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"$decimal": d.String()})
}
