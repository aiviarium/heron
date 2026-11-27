package protocol

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/aiviarium/sparrow-rpc/go/cbor"
	"github.com/aiviarium/sparrow-rpc/go/schema"
	"github.com/aiviarium/sparrow-rpc/go/value"
)

// The Value union's variants, by tag, as data_mapping's table gives them.
const (
	vNull uint64 = iota
	vBool
	vInt
	vUint
	vBigint
	vFloat
	vString
	vBytes
	vDecimal
	vTime
	vDuration
	vList
	vSet
	vMap
	vVariant
	vRef
	vProse
)

// binder converts between the generated Go types and the representation
// of Sparrow's value package, by the types of one facet: a struct's Go
// fields are its fields by tag, in order; an enum's Go string is its
// value's name; Any is the Value union.
type binder struct {
	f       *schema.Facet
	valueID uint64
}

func newBinder(f *schema.Facet) *binder {
	b := &binder{f: f, valueID: math.MaxUint64}
	for i := range f.Types {
		if f.Types[i].Name == "Value" && f.Types[i].Kind == schema.KindUnion {
			b.valueID = f.Types[i].ID
		}
	}
	return b
}

func (b *binder) typeNamed(name string) (uint64, bool) {
	for i := range b.f.Types {
		if b.f.Types[i].Name == name {
			return b.f.Types[i].ID, true
		}
	}
	return 0, false
}

// encode returns v, a Go value, as type t.
func (b *binder) encode(t schema.TypeRef, v reflect.Value) (any, error) {
	for v.IsValid() && v.Kind() == reflect.Interface {
		if v.IsNil() {
			v = reflect.Value{}
			break
		}
		v = v.Elem()
	}
	if t.Kind == schema.RefNamed && t.ID == b.valueID {
		if !v.IsValid() {
			return value.Union{Tag: vNull}, nil
		}
		return encodeValue(v.Interface())
	}
	if !v.IsValid() {
		return nil, nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}
	switch t.Kind {
	case schema.RefOptional:
		if v.IsZero() {
			return nil, nil
		}
		return b.encode(*t.Elem, v)
	case schema.RefList:
		if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
			return nil, fmt.Errorf("%s is not a list", v.Type())
		}
		out := make([]any, v.Len())
		for i := range out {
			x, err := b.encode(*t.Elem, v.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	case schema.RefMap:
		if v.Kind() != reflect.Map {
			return nil, fmt.Errorf("%s is not a map", v.Type())
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		out := make(cbor.Map, 0, len(keys))
		for _, k := range keys {
			x, err := b.encode(*t.Elem, v.MapIndex(k))
			if err != nil {
				return nil, err
			}
			out = append(out, cbor.Pair{Key: k.String(), Value: x})
		}
		return out, nil
	case schema.RefNamed:
	default:
		return nil, fmt.Errorf("type %v isn't a value", t)
	}
	if t.ID < schema.FirstFreeTypeID {
		switch t.ID {
		case schema.TypeString:
			return v.String(), nil
		case schema.TypeInt:
			return v.Int(), nil
		case schema.TypeBool:
			return v.Bool(), nil
		case schema.TypeFloat64:
			return v.Float(), nil
		case schema.TypeBytes:
			return v.Bytes(), nil
		case schema.TypeUnit:
			return nil, nil
		}
		return nil, fmt.Errorf("built-in type %d isn't used by the protocol", t.ID)
	}
	td := b.f.Type(t.ID)
	if td == nil {
		return nil, fmt.Errorf("no type %d in %s", t.ID, b.f.Name)
	}
	switch td.Kind {
	case schema.KindEnum:
		name := v.String()
		for _, id := range td.Values {
			if s := b.f.Sigil(id); s != nil && strings.TrimPrefix(s.Name, td.Name+".") == name {
				return id, nil
			}
		}
		return nil, fmt.Errorf("%q is not a %s", name, td.Name)
	case schema.KindStruct:
		if v.Kind() != reflect.Struct {
			return nil, fmt.Errorf("%s is not a %s", v.Type(), td.Name)
		}
		out := value.Struct{}
		fields := exported(v.Type())
		for _, fd := range td.Fields {
			if int(fd.Tag) >= len(fields) {
				continue
			}
			fv := v.FieldByIndex(fields[fd.Tag].Index)
			if fd.Optional() && fv.IsZero() {
				continue
			}
			x, err := b.encode(fd.Type, fv)
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", td.Name, fd.Name, err)
			}
			if x == nil && !fd.Optional() {
				x = zeroOf(fd.Type)
			}
			if x != nil {
				out[fd.Tag] = x
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("type %s isn't used by the protocol", td.Name)
}

// zeroOf is a required field's value when its Go value is nil: an empty
// list or map.
func zeroOf(t schema.TypeRef) any {
	switch t.Kind {
	case schema.RefList:
		return []any{}
	case schema.RefMap:
		return cbor.Map{}
	}
	return nil
}

func exported(t reflect.Type) []reflect.StructField {
	var out []reflect.StructField
	for i := 0; i < t.NumField(); i++ {
		if f := t.Field(i); f.IsExported() {
			out = append(out, f)
		}
	}
	return out
}

// decode stores x, a decoded value of type t, in v.
func (b *binder) decode(t schema.TypeRef, x any, v reflect.Value) error {
	if t.Kind == schema.RefNamed && t.ID == b.valueID {
		val, err := decodeValue(x)
		if err != nil {
			return err
		}
		if val == nil {
			v.Set(reflect.Zero(v.Type()))
			return nil
		}
		rv := reflect.ValueOf(val)
		if !rv.Type().AssignableTo(v.Type()) {
			if rv.Type().ConvertibleTo(v.Type()) {
				v.Set(rv.Convert(v.Type()))
				return nil
			}
			return fmt.Errorf("a %T can't be read into %s", val, v.Type())
		}
		v.Set(rv)
		return nil
	}
	if x == nil {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		p := reflect.New(v.Type().Elem())
		if err := b.decode(t, x, p.Elem()); err != nil {
			return err
		}
		v.Set(p)
		return nil
	}
	if v.Kind() == reflect.Interface && v.NumMethod() == 0 {
		// A typed result read into an any, as a caller that doesn't know
		// the type: the plain Go form.
		v.Set(reflect.ValueOf(plain(x)))
		return nil
	}
	switch t.Kind {
	case schema.RefOptional:
		return b.decode(*t.Elem, x, v)
	case schema.RefList:
		xs, ok := x.([]any)
		if !ok {
			return fmt.Errorf("want a list, have %T", x)
		}
		s := reflect.MakeSlice(v.Type(), len(xs), len(xs))
		for i, e := range xs {
			if err := b.decode(*t.Elem, e, s.Index(i)); err != nil {
				return err
			}
		}
		v.Set(s)
		return nil
	case schema.RefMap:
		m, ok := x.(cbor.Map)
		if !ok {
			return fmt.Errorf("want a map, have %T", x)
		}
		out := reflect.MakeMapWithSize(v.Type(), len(m))
		for _, p := range m {
			k, _ := p.Key.(string)
			e := reflect.New(v.Type().Elem()).Elem()
			if err := b.decode(*t.Elem, p.Value, e); err != nil {
				return err
			}
			out.SetMapIndex(reflect.ValueOf(k).Convert(v.Type().Key()), e)
		}
		v.Set(out)
		return nil
	case schema.RefNamed:
	default:
		return fmt.Errorf("type %v isn't a value", t)
	}
	if t.ID < schema.FirstFreeTypeID {
		rv := reflect.ValueOf(x)
		if !rv.Type().ConvertibleTo(v.Type()) {
			return fmt.Errorf("a %T can't be read into %s", x, v.Type())
		}
		v.Set(rv.Convert(v.Type()))
		return nil
	}
	td := b.f.Type(t.ID)
	if td == nil {
		return fmt.Errorf("no type %d in %s", t.ID, b.f.Name)
	}
	switch td.Kind {
	case schema.KindEnum:
		id, _ := x.(uint64)
		if s := b.f.Sigil(id); s != nil {
			v.SetString(strings.TrimPrefix(s.Name, td.Name+"."))
		}
		return nil
	case schema.KindStruct:
		st, ok := x.(value.Struct)
		if !ok {
			return fmt.Errorf("want a %s, have %T", td.Name, x)
		}
		fields := exported(v.Type())
		for _, fd := range td.Fields {
			e, ok := st[fd.Tag]
			if !ok || int(fd.Tag) >= len(fields) {
				continue
			}
			if err := b.decode(fd.Type, e, v.FieldByIndex(fields[fd.Tag].Index)); err != nil {
				return fmt.Errorf("%s.%s: %w", td.Name, fd.Name, err)
			}
		}
		return nil
	}
	return fmt.Errorf("type %s isn't used by the protocol", td.Name)
}

// plain is a decoded value in plain Go: maps with text keys as
// map[string]any, struct fields by tag.
func plain(x any) any {
	switch x := x.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = plain(e)
		}
		return out
	case cbor.Map:
		out := map[string]any{}
		for _, p := range x {
			out[fmt.Sprint(p.Key)] = plain(p.Value)
		}
		return out
	case value.Union:
		if v, err := decodeValue(x); err == nil {
			return v
		}
	}
	return x
}

// encodeValue returns a Heron value as the Value union.
func encodeValue(x any) (value.Union, error) {
	u := func(tag uint64, v any) (value.Union, error) { return value.Union{Tag: tag, Value: v}, nil }
	switch x := x.(type) {
	case nil:
		return u(vNull, nil)
	case bool:
		return u(vBool, x)
	case int:
		return u(vInt, int64(x))
	case int64:
		return u(vInt, x)
	case int32:
		return u(vInt, int64(x))
	case uint64:
		if x > math.MaxInt64 {
			return u(vUint, x)
		}
		return u(vInt, int64(x))
	case *big.Int:
		switch {
		case x.IsInt64():
			return u(vInt, x.Int64())
		case x.IsUint64():
			return u(vUint, x.Uint64())
		}
		return u(vBigint, x.String())
	case float64:
		return u(vFloat, x)
	case float32:
		return u(vFloat, float64(x))
	case string:
		return u(vString, x)
	case []byte:
		return u(vBytes, x)
	case Decimal:
		return u(vDecimal, x.String())
	case time.Time:
		return u(vTime, x.UTC().Format(time.RFC3339Nano))
	case Duration:
		return u(vDuration, int64(x))
	case Ref:
		return u(vRef, string(x))
	case Variant:
		inner, err := encodeValue(x.Value)
		if err != nil {
			return value.Union{}, err
		}
		return u(vVariant, value.Struct{0: x.Name, 1: inner})
	case Set:
		l, err := encodeList([]any(x))
		if err != nil {
			return value.Union{}, err
		}
		return u(vSet, l)
	case []any:
		l, err := encodeList(x)
		if err != nil {
			return value.Union{}, err
		}
		return u(vList, l)
	case Prose:
		m, err := encodeMap(map[string]any(x))
		if err != nil {
			return value.Union{}, err
		}
		return u(vProse, m)
	case map[string]any:
		m, err := encodeMap(x)
		if err != nil {
			return value.Union{}, err
		}
		return u(vMap, m)
	}
	// Other slices and maps with text keys, by reflection.
	rv := reflect.ValueOf(x)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		l := make([]any, rv.Len())
		for i := range l {
			l[i] = rv.Index(i).Interface()
		}
		return encodeValue(l)
	case reflect.Map:
		if rv.Type().Key().Kind() == reflect.String {
			m := map[string]any{}
			for _, k := range rv.MapKeys() {
				m[k.String()] = rv.MapIndex(k).Interface()
			}
			return encodeValue(m)
		}
	case reflect.String:
		return u(vString, rv.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return u(vInt, rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return encodeValue(rv.Uint())
	case reflect.Float32, reflect.Float64:
		return u(vFloat, rv.Float())
	case reflect.Bool:
		return u(vBool, rv.Bool())
	case reflect.Pointer:
		if rv.IsNil() {
			return u(vNull, nil)
		}
		return encodeValue(rv.Elem().Interface())
	}
	return value.Union{}, fmt.Errorf("a %T has no Heron value", x)
}

func encodeList(xs []any) ([]any, error) {
	out := make([]any, len(xs))
	for i, e := range xs {
		v, err := encodeValue(e)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// encodeMap writes a map's entries in the order of their keys, so equal
// values encode to equal bytes.
func encodeMap(m map[string]any) (cbor.Map, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(cbor.Map, 0, len(keys))
	for _, k := range keys {
		v, err := encodeValue(m[k])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out = append(out, cbor.Pair{Key: k, Value: v})
	}
	return out, nil
}

// decodeValue reads the Value union back as a Heron value.
func decodeValue(x any) (any, error) {
	if x == nil {
		return nil, nil
	}
	un, ok := x.(value.Union)
	if !ok {
		return nil, fmt.Errorf("want a Value, have %T", x)
	}
	switch un.Tag {
	case vNull:
		return nil, nil
	case vBool, vInt, vFloat, vBytes:
		return un.Value, nil
	case vUint:
		n, _ := un.Value.(uint64)
		return new(big.Int).SetUint64(n), nil
	case vString:
		return un.Value, nil
	case vBigint:
		s, _ := un.Value.(string)
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("not an integer: %q", s)
		}
		return n, nil
	case vDecimal:
		s, _ := un.Value.(string)
		return ParseDecimal(s)
	case vTime:
		s, _ := un.Value.(string)
		return time.Parse(time.RFC3339Nano, s)
	case vDuration:
		n, _ := un.Value.(int64)
		return Duration(n), nil
	case vRef:
		s, _ := un.Value.(string)
		return Ref(s), nil
	case vVariant:
		st, _ := un.Value.(value.Struct)
		name, _ := st[0].(string)
		inner, err := decodeValue(st[1])
		if err != nil {
			return nil, err
		}
		return Variant{Name: name, Value: inner}, nil
	case vList, vSet:
		xs, _ := un.Value.([]any)
		out := make([]any, len(xs))
		for i, e := range xs {
			v, err := decodeValue(e)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		if un.Tag == vSet {
			return Set(out), nil
		}
		return out, nil
	case vMap, vProse:
		m, _ := un.Value.(cbor.Map)
		out := make(map[string]any, len(m))
		for _, p := range m {
			k, _ := p.Key.(string)
			v, err := decodeValue(p.Value)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		if un.Tag == vProse {
			return Prose(out), nil
		}
		return out, nil
	}
	return nil, fmt.Errorf("a Value with variant %d this side doesn't know", un.Tag)
}
