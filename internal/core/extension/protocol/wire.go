package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/fxamacker/cbor/v2"
)

// Wire is an encoding of frames: CBOR, or JSON for reading traffic while
// debugging (`--wire json`).
type Wire interface {
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// Raw is a value left encoded in the connection's wire encoding, such as a
// message's params until a handler decodes them into its own type.
type Raw []byte

func (r Raw) MarshalCBOR() ([]byte, error) {
	if len(r) == 0 {
		return []byte{0xf6}, nil
	}
	return r, nil
}

func (r *Raw) UnmarshalCBOR(data []byte) error {
	*r = append((*r)[:0], data...)
	return nil
}

func (r Raw) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}
	return r, nil
}

func (r *Raw) UnmarshalJSON(data []byte) error {
	*r = append((*r)[:0], data...)
	return nil
}

// IsNull reports whether the value is absent or null.
func (r Raw) IsNull() bool {
	// Not trimmed: CBOR's small integers are bytes such as 0x0c that read
	// as whitespace.
	return len(r) == 0 || bytes.Equal(r, []byte{0xf6}) || bytes.Equal(r, []byte{0xf7}) || bytes.Equal(r, []byte("null"))
}

type cborWire struct {
	enc cbor.EncMode
	dec cbor.DecMode
}

// CBOR is the default wire.
var CBOR Wire = newCBOR()

// JSON is the debugging wire. Tagged values are written as objects with a
// "$" key, such as {"$ref": "store.pets"}, and read back as plain maps.
var JSON Wire = jsonWire{}

func newCBOR() *cborWire {
	tags := cbor.NewTagSet()
	add := func(v any, num uint64) {
		if err := tags.Add(cbor.TagOptions{EncTag: cbor.EncTagRequired, DecTag: cbor.DecTagRequired}, reflect.TypeOf(v), num); err != nil {
			panic(err)
		}
	}
	add(Variant{}, TagVariant)
	add(Duration(0), TagDuration)
	add(Prose{}, TagProse)
	add(Ref(""), TagRef)
	add(Set{}, TagSet)
	add(Decimal{}, TagDecimal)
	enc, err := cbor.EncOptions{
		Time:          cbor.TimeRFC3339Nano,
		TimeTag:       cbor.EncTagRequired,
		BigIntConvert: cbor.BigIntConvertShortest,
		NilContainers: cbor.NilContainerAsEmpty,
		Sort:          cbor.SortCanonical,
	}.EncModeWithTags(tags)
	if err != nil {
		panic(err)
	}
	dec, err := cbor.DecOptions{
		DefaultMapType:    reflect.TypeOf(map[string]any(nil)),
		TimeTag:           cbor.DecTagOptional,
		BigIntDec:         cbor.BigIntDecodePointer,
		IntDec:            cbor.IntDecConvertSignedOrBigInt,
		MaxNestedLevels:   256,
		MaxArrayElements:  1 << 24,
		MaxMapPairs:       1 << 24,
		ExtraReturnErrors: cbor.ExtraDecErrorNone,
	}.DecModeWithTags(tags)
	if err != nil {
		panic(err)
	}
	return &cborWire{enc: enc, dec: dec}
}

func (w *cborWire) Name() string                       { return "cbor" }
func (w *cborWire) Marshal(v any) ([]byte, error)      { return w.enc.Marshal(v) }
func (w *cborWire) Unmarshal(data []byte, v any) error { return w.dec.Unmarshal(data, v) }

type jsonWire struct{}

func (jsonWire) Name() string                  { return "json" }
func (jsonWire) Marshal(v any) ([]byte, error) { return json.Marshal(v) }
func (jsonWire) Unmarshal(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return d.Decode(v)
}

// WireByName returns the wire named "cbor" or "json".
func WireByName(name string) (Wire, error) {
	switch name {
	case "", "cbor":
		return CBOR, nil
	case "json":
		return JSON, nil
	}
	return nil, fmt.Errorf("unknown wire %q: use cbor or json", name)
}

// DefaultLimits are the limits a host offers when an extension declares
// none of its own.
func DefaultLimits() Limits {
	return Limits{MaxFrame: 16 << 20, MaxInFlight: 64, TimeoutMs: int64((30 * time.Second) / time.Millisecond)}
}

// ErrFrameTooLarge is returned for a frame over the declared limit.
type ErrFrameTooLarge struct{ Size, Max int }

func (e ErrFrameTooLarge) Error() string {
	return fmt.Sprintf("frame of %d bytes is over the limit of %d", e.Size, e.Max)
}

// WriteFrame writes one frame: its length as a 4-byte big-endian integer,
// then the payload.
func WriteFrame(w io.Writer, payload []byte, max int) error {
	if max > 0 && len(payload) > max {
		return ErrFrameTooLarge{len(payload), max}
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(append(hdr[:], payload...)); err != nil {
		return err
	}
	return nil
}

// ReadFrame reads one frame. A frame over max is an error, and the payload
// is never read into memory.
func ReadFrame(r io.Reader, max int) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint32(hdr[:]))
	if max > 0 && n > max {
		return nil, ErrFrameTooLarge{n, max}
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
