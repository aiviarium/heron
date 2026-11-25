// Package eval interprets the `do` code of funcs, macros, tasks and hooks,
// as the Scripting tab describes it. It does no I/O of its own: processes,
// files, the environment and the clock come in through Caps, and foreign
// functions through the registry, so a run is replayable from what those
// returned. Package eval/host supplies the real capabilities.
package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/resolve"
)

// Value is a Heron value at run time. It is one of:
//
//   - nil for null
//   - bool, int64, float64 and string
//   - []Value for a list or set
//   - map[string]Value for a map or an untyped record
//   - *Record for a value of a declared type, such as a Process or an error
//   - Text for a typed string, such as an `sh` embed
//   - Ref for a node used as a value, such as a task named in an argument
//   - TypeValue for a type in an argument, as in json.decode(List(Pet), …)
//
// Enum variants are their names as strings. Plain converts a value to the
// form that crosses the foreign boundary.
type Value = any

// Record is a value of a declared type: its name and its members.
type Record struct {
	Type   string
	Fields map[string]Value
}

// Text is a typed string, such as (sh)"…" or (json)"""…""": the language
// it is written in and its text, with inserted values already made safe.
type Text struct {
	Lang string
	Text string
}

// Ref is a node of the tree used as a value.
type Ref struct {
	Node *resolve.Node
}

// TypeValue is a type written where a value stands.
type TypeValue struct {
	Node *resolve.Node // the type's declaration; a builtin for List and Map
	Args []TypeValue
}

// Name is the type's name as written, such as List(Pet).
func (t TypeValue) Name() string {
	if t.Node == nil {
		return "Any"
	}
	if len(t.Args) == 0 {
		return t.Node.Name
	}
	parts := make([]string, len(t.Args))
	for i, a := range t.Args {
		parts[i] = a.Name()
	}
	return t.Node.Name + "(" + strings.Join(parts, ", ") + ")"
}

// Str is a value's string form: what an interpolation inserts and what a
// process argument receives.
func Str(v Value) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case Text:
		return v.Text
	case Ref:
		return v.Node.Path()
	case TypeValue:
		return v.Name()
	default:
		b, err := json.Marshal(Plain(v))
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}

// Plain converts a value to plain Go data: records become maps, typed
// strings strings and refs their node paths, as the foreign boundary and
// JSON output see them.
func Plain(v Value) any {
	switch v := v.(type) {
	case []Value:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = Plain(x)
		}
		return out
	case map[string]Value:
		out := make(map[string]any, len(v))
		for k, x := range v {
			out[k] = Plain(x)
		}
		return out
	case *Record:
		out := make(map[string]any, len(v.Fields))
		for k, x := range v.Fields {
			out[k] = Plain(x)
		}
		return out
	case Text:
		return v.Text
	case Ref:
		return v.Node.Path()
	case TypeValue:
		return v.Name()
	default:
		return v
	}
}

// FromPlain reads plain Go data, as a foreign function returns it, as a
// value: integral numbers become int64 and other numbers float64.
func FromPlain(v any) Value {
	switch v := v.(type) {
	case []any:
		out := make([]Value, len(v))
		for i, x := range v {
			out[i] = FromPlain(x)
		}
		return out
	case map[string]any:
		out := make(map[string]Value, len(v))
		for k, x := range v {
			out[k] = FromPlain(x)
		}
		return out
	case int:
		return int64(v)
	case int32:
		return int64(v)
	case uint64:
		if v <= math.MaxInt64 {
			return int64(v)
		}
		return float64(v)
	case float32:
		return float64(v)
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return int64(v)
		}
		return v
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		f, _ := v.Float64()
		return f
	default:
		return v
	}
}

// TypeName is the name of a value's type, as errors and `is` read it.
func TypeName(v Value) string {
	switch v := v.(type) {
	case nil:
		return "Null"
	case bool:
		return "Bool"
	case int64:
		return "Int"
	case float64:
		return "Float"
	case string:
		return "String"
	case Text:
		return v.Lang
	case []Value:
		return "List"
	case map[string]Value:
		return "Map"
	case *Record:
		return v.Type
	case Ref:
		return "Ref"
	case TypeValue:
		return "Type"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// Equal compares two values structurally; an Int equals the Float of the
// same number, and a typed string equals its text.
func Equal(a, b Value) bool {
	if fa, ok := num(a); ok {
		if fb, ok := num(b); ok {
			return fa == fb
		}
		return false
	}
	if t, ok := a.(Text); ok {
		a = t.Text
	}
	if t, ok := b.(Text); ok {
		b = t.Text
	}
	switch a := a.(type) {
	case nil:
		return b == nil
	case bool, string:
		return a == b
	case []Value:
		bl, ok := b.([]Value)
		if !ok || len(a) != len(bl) {
			return false
		}
		for i := range a {
			if !Equal(a[i], bl[i]) {
				return false
			}
		}
		return true
	case map[string]Value:
		bm, ok := b.(map[string]Value)
		return ok && equalFields(a, bm)
	case *Record:
		br, ok := b.(*Record)
		return ok && a.Type == br.Type && equalFields(a.Fields, br.Fields)
	case Ref:
		br, ok := b.(Ref)
		return ok && a.Node == br.Node
	case TypeValue:
		bt, ok := b.(TypeValue)
		return ok && a.Name() == bt.Name()
	default:
		return false
	}
}

func equalFields(a, b map[string]Value) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || !Equal(v, w) {
			return false
		}
	}
	return true
}

func num(v Value) (float64, bool) {
	switch v := v.(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

// sortedKeys returns a map's keys in order, so iteration is deterministic.
func sortedKeys(m map[string]Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
