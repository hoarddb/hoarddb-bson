// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"testing"
	"time"
	"unsafe"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
)

// One test per encode row of spec 5.3's contract table (C13), each a
// differential against the vendor through compare.Encoded.

// Row Z1: a nil pointer, slice, map, []byte or interface without omitempty
// is BSON null; at the top level the vendor refuses all of them but a nil
// map, which it writes as an empty document.
type nilsInner struct {
	Name string
}

type nilsProbe struct {
	Pointer    *int
	Slice      []int
	Map        map[string]int
	Bytes      []byte
	Named      namedBytes
	Any        interface{}
	Stringer   fmt.Stringer
	Struct     *nilsInner
	Pointers   []*int
	Values     map[string]*int
	Interfaces []interface{}
	Array      [2]*int
}

type namedBytes []byte

func TestEncodeZ1NilsEncodeAsNull(t *testing.T) {
	var nilInt *int
	filled := nilsProbe{
		Pointers:   []*int{nil, nil},
		Values:     map[string]*int{"a": nil, "b": nil},
		Interfaces: []interface{}{nil, nilInt, []int(nil), map[string]int(nil)},
		Any:        nilInt,
	}
	requireVendorOutcome(t, (*int)(nil), "WriteNull can only write while positioned on a Element or Value but is positioned on a TopLevel")
	requireVendorOutcome(t, map[string]int(nil), "")
	requireEncodesLikeVendor(t,
		sameValue("zero struct", nilsProbe{}),
		sameValue("nil elements", filled),
		sameValue("pointer to zero struct", &nilsProbe{}),
		sameValue("map of nils", map[string]any{"a": nil, "b": []int(nil), "c": map[string]int(nil), "d": nilInt, "e": []byte(nil), "f": (*nilsInner)(nil)}),
		sameValue("top-level nil map", map[string]int(nil)),
		sameValue("top-level nil pointer", nilInt),
		sameValue("top-level nil struct pointer", (*nilsProbe)(nil)),
		sameValue("top-level nil slice", []int(nil)),
		sameValue("top-level nil", nil),
		twinValues("top-level nil D", bson.D(nil), vendorbson.D(nil)),
		twinValues("D of nils", bson.D{{Key: "a", Value: nil}, {Key: "b", Value: nilInt}, {Key: "c", Value: bson.D(nil)}, {Key: "d", Value: bson.A(nil)}},
			vendorbson.D{{Key: "a", Value: nil}, {Key: "b", Value: nilInt}, {Key: "c", Value: vendorbson.D(nil)}, {Key: "d", Value: vendorbson.A(nil)}}),
		twinValues("nil M inside", map[string]any{"m": bson.M(nil)}, map[string]any{"m": vendorbson.M(nil)}),
	)
}

// Row Z2: an empty non-nil []byte is Binary subtype 0 of length 0.
func TestEncodeZ2EmptyByteSliceIsBinaryZero(t *testing.T) {
	encoded := requireVendorOutcome(t, struct{ B []byte }{B: []byte{}}, "")
	if value := encodedElement(t, encoded, "b"); value.Type != vendorbson.TypeBinary || len(value.Data) != 5 || value.Data[4] != 0x00 {
		t.Fatalf("the vendor wrote %s % x for an empty []byte, want Binary subtype 0 of length 0", value.Type, value.Data)
	}
	requireEncodesLikeVendor(t,
		sameValue("field", struct{ B []byte }{B: []byte{}}),
		sameValue("named field", struct{ B namedBytes }{B: namedBytes{}}),
		sameValue("zero-length array", struct{ B [0]byte }{}),
		sameValue("map value", map[string]any{"b": []byte{}}),
		sameValue("slice element", struct{ L [][]byte }{L: [][]byte{{}, nil}}),
		sameValue("omitempty drops it", struct {
			B []byte `bson:",omitempty"`
			N int
		}{B: []byte{}}),
		twinValues("Binary zero", struct{ B primitive.Binary }{}, struct{ B vendorprimitive.Binary }{}),
		twinValues("Binary zero omitempty", struct {
			B primitive.Binary `bson:",omitempty"`
		}{}, struct {
			B vendorprimitive.Binary `bson:",omitempty"`
		}{}),
	)
}

// Row Z3: omitempty asks IsZero where the type has it, a pointer's
// included; a struct without IsZero is never empty; a non-nil pointer to a
// zero value is empty only when the element type has IsZero.
type zeroerValue struct{ Number int }

func (zero zeroerValue) IsZero() bool { return zero.Number == 7 }

type zeroerPointer struct{ Number int }

func (zero *zeroerPointer) IsZero() bool { return zero == nil || zero.Number == 7 }

type plainStruct struct{ Number int }

type zeroerStringer struct{ Number int }

func (zero zeroerStringer) String() string { return "zero-stringer" }
func (zero zeroerStringer) IsZero() bool   { return zero.Number == 0 }

type omitProbe[ObjectID, RawValue, Binary, Decimal any] struct {
	Time       time.Time      `bson:",omitempty"`
	TimePtr    *time.Time     `bson:",omitempty"`
	IntPtr     *int           `bson:",omitempty"`
	ObjectID   ObjectID       `bson:",omitempty"`
	RawValue   RawValue       `bson:",omitempty"`
	Binary     Binary         `bson:",omitempty"`
	Decimal    Decimal        `bson:",omitempty"`
	Plain      plainStruct    `bson:",omitempty"`
	PlainPtr   *plainStruct   `bson:",omitempty"`
	ZeroV      zeroerValue    `bson:",omitempty"`
	ZeroVPtr   *zeroerValue   `bson:",omitempty"`
	ZeroP      zeroerPointer  `bson:",omitempty"`
	ZeroPPtr   *zeroerPointer `bson:",omitempty"`
	Stringer   fmt.Stringer   `bson:",omitempty"`
	Any        interface{}    `bson:",omitempty"`
	Float      float64        `bson:",omitempty"`
	Int        int            `bson:",omitempty"`
	Uint       uint16         `bson:",omitempty"`
	Bool       bool           `bson:",omitempty"`
	String     string         `bson:",omitempty"`
	Slice      []int          `bson:",omitempty"`
	Map        map[string]int `bson:",omitempty"`
	Array      [2]int         `bson:",omitempty"`
	EmptyArray [0]int         `bson:",omitempty"`
	Last       string
}

type libOmitProbe = omitProbe[primitive.ObjectID, bson.RawValue, primitive.Binary, primitive.Decimal128]
type vendorOmitProbe = omitProbe[vendorprimitive.ObjectID, vendorbson.RawValue, vendorprimitive.Binary, vendorprimitive.Decimal128]

func omitPairs() []encodeCase {
	var zeroTime time.Time
	zeroInt := 0
	seven := zeroerValue{Number: 7}
	sevenPointer := zeroerPointer{Number: 7}
	nonZero := func(probe *libOmitProbe, vendorProbe *vendorOmitProbe) {
		probe.ObjectID, vendorProbe.ObjectID = primitive.ObjectID{1}, vendorprimitive.ObjectID{1}
		probe.RawValue, vendorProbe.RawValue = bson.RawValue{Type: bson.TypeNull}, vendorbson.RawValue{Type: vendorbson.TypeNull}
		probe.Binary, vendorProbe.Binary = primitive.Binary{Subtype: 0x80}, vendorprimitive.Binary{Subtype: 0x80}
		probe.Decimal, vendorProbe.Decimal = primitive.NewDecimal128(1, 1), vendorprimitive.NewDecimal128(1, 1)
	}
	var pointersToZero libOmitProbe
	var vendorPointersToZero vendorOmitProbe
	for _, probe := range []any{&pointersToZero, &vendorPointersToZero} {
		switch typed := probe.(type) {
		case *libOmitProbe:
			typed.TimePtr, typed.IntPtr, typed.PlainPtr = &zeroTime, &zeroInt, &plainStruct{}
			typed.ZeroVPtr, typed.ZeroPPtr = &seven, &sevenPointer
			typed.Stringer, typed.Any, typed.Float = zeroerStringer{}, zeroTime, math.Copysign(0, -1)
		case *vendorOmitProbe:
			typed.TimePtr, typed.IntPtr, typed.PlainPtr = &zeroTime, &zeroInt, &plainStruct{}
			typed.ZeroVPtr, typed.ZeroPPtr = &seven, &sevenPointer
			typed.Stringer, typed.Any, typed.Float = zeroerStringer{}, zeroTime, math.Copysign(0, -1)
		}
	}
	filled, vendorFilled := pointersToZero, vendorPointersToZero
	nonZero(&filled, &vendorFilled)
	filled.ZeroV, vendorFilled.ZeroV = zeroerValue{Number: 1}, zeroerValue{Number: 1}
	filled.Stringer, vendorFilled.Stringer = zeroerStringer{Number: 2}, zeroerStringer{Number: 2}
	filled.Float, vendorFilled.Float = math.NaN(), math.NaN()
	return []encodeCase{
		twinValues("zero", libOmitProbe{}, vendorOmitProbe{}),
		twinValues("pointer to zero", &libOmitProbe{}, &vendorOmitProbe{}),
		twinValues("pointers to zero values", pointersToZero, vendorPointersToZero),
		twinValues("addressable pointers to zero values", &pointersToZero, &vendorPointersToZero),
		twinValues("non-zero", filled, vendorFilled),
		twinValues("IsZero true on the value", libOmitProbe{ZeroV: seven, Time: time.Unix(0, 0)}, vendorOmitProbe{ZeroV: seven, Time: time.Unix(0, 0)}),
	}
}

// noCodecOmitted holds a zero func behind omitempty: the vendor refuses
// the struct anyway.
type noCodecOmitted struct {
	Name string
	F    func() `bson:",omitempty"`
}

type noCodecBehindNilInline struct {
	Name  string
	Inner *noCodecOmitted `bson:",inline"`
}

func TestEncodeZ3OmitemptyHonoursZeroer(t *testing.T) {
	var zeroTime time.Time
	encoded := requireVendorOutcome(t, struct {
		T *time.Time `bson:",omitempty"`
		N *int       `bson:",omitempty"`
	}{T: &zeroTime, N: new(int)}, "")
	if _, err := vendorbson.Raw(encoded).LookupErr("t"); err == nil {
		t.Fatalf("the vendor kept a pointer to the zero time under omitempty: % x", encoded)
	}
	requireEncodesLikeVendor(t, omitPairs()...)
	requireEncodesLikeVendor(t,
		sameValue("no codec under omitempty", noCodecOmitted{}),
		sameValue("chan under omitempty", struct {
			C chan int `bson:",omitempty"`
		}{}),
		sameValue("complex under omitempty", struct {
			C complex128 `bson:",omitempty"`
		}{}),
		sameValue("uintptr under omitempty", struct {
			U uintptr `bson:",omitempty"`
		}{}),
		sameValue("unsafe.Pointer under omitempty", struct {
			P unsafe.Pointer `bson:",omitempty"`
		}{}),
		sameValue("no codec behind a nil inline pointer", noCodecBehindNilInline{Name: "n"}),
		sameValue("pointer to a no-codec type", struct{ F *func() }{}),
		sameValue("non-nil pointer to a no-codec type", struct{ F *func() }{F: new(func())}),
	)
}

// Row I1: int8, int16 and int32 are int32; int is int32 when it fits and
// int64 when it does not.
type namedInt int
type namedInt16 int16

type smallInts struct {
	I8      int8
	I16     int16
	I32     int32
	I       int
	Named   namedInt
	Named16 namedInt16
	MinSize int `bson:",minsize"`
}

func TestEncodeI1SmallIntsAsInt32(t *testing.T) {
	encoded := requireVendorOutcome(t, struct{ I int }{I: math.MaxInt32 + 1}, "")
	if value := encodedElement(t, encoded, "i"); value.Type != vendorbson.TypeInt64 {
		t.Fatalf("the vendor wrote %s for an int past MaxInt32, want int64", value.Type)
	}
	requireEncodesLikeVendor(t,
		sameValue("zero", smallInts{}),
		sameValue("maximums", smallInts{I8: math.MaxInt8, I16: math.MaxInt16, I32: math.MaxInt32, I: math.MaxInt32, Named: math.MaxInt32, Named16: math.MaxInt16, MinSize: math.MaxInt32}),
		sameValue("minimums", smallInts{I8: math.MinInt8, I16: math.MinInt16, I32: math.MinInt32, I: math.MinInt32, Named: math.MinInt32, Named16: math.MinInt16, MinSize: math.MinInt32}),
		sameValue("int past int32", smallInts{I: math.MaxInt32 + 1, Named: math.MinInt32 - 1, MinSize: math.MaxInt64}),
		sameValue("int extremes", smallInts{I: math.MaxInt64, Named: math.MinInt64}),
		sameValue("in containers", map[string]any{"slice": []int{1, math.MaxInt32 + 1}, "map": map[string]int8{"a": -1}, "any": []any{int16(3), 7, math.MinInt32 - 1}}),
		sameValue("top-level int", 7),
		sameValue("top-level int64", int64(1)<<40),
	)
}

// Row I2: int64 is int64, and int32 only under minsize when it fits;
// minsize holds for everything inside the field it tags.
type minSizeInner struct {
	N int64
	U uint32
}

type minSizeProbe[D any] struct {
	Plain     int64
	MinSize   int64          `bson:",minsize"`
	Large     int64          `bson:",minsize"`
	Nested    minSizeInner   `bson:",minsize"`
	Slice     []int64        `bson:",minsize"`
	Map       map[string]any `bson:",minsize"`
	Any       interface{}    `bson:",minsize"`
	Pointer   *int64         `bson:",minsize"`
	Document  D              `bson:",minsize"`
	Number    json.Number    `bson:",minsize"`
	Unsigned  uint64         `bson:",minsize"`
	NotNested minSizeInner
}

func TestEncodeI2Int64(t *testing.T) {
	five := int64(5)
	lib := minSizeProbe[bson.D]{
		Plain: 1, MinSize: 2, Large: math.MaxInt64, Nested: minSizeInner{N: 3, U: 4}, Slice: []int64{5, math.MinInt64},
		Map: map[string]any{"n": int64(6), "deep": map[string]any{"n": int64(7)}}, Any: int64(8), Pointer: &five,
		Document: bson.D{{Key: "n", Value: int64(9)}}, Number: "10", Unsigned: 11, NotNested: minSizeInner{N: 12, U: 13},
	}
	vendor := minSizeProbe[vendorbson.D]{
		Plain: lib.Plain, MinSize: lib.MinSize, Large: lib.Large, Nested: lib.Nested, Slice: lib.Slice,
		Map: lib.Map, Any: lib.Any, Pointer: lib.Pointer,
		Document: vendorbson.D{{Key: "n", Value: int64(9)}}, Number: lib.Number, Unsigned: lib.Unsigned, NotNested: lib.NotNested,
	}
	encoded := requireVendorOutcome(t, vendor, "")
	if value := encodedElement(t, encoded, "plain"); value.Type != vendorbson.TypeInt64 {
		t.Fatalf("the vendor wrote %s for a small int64, want int64", value.Type)
	}
	requireEncodesLikeVendor(t,
		twinValues("zero but a valid number", minSizeProbe[bson.D]{Number: "0"}, minSizeProbe[vendorbson.D]{Number: "0"}),
		twinValues("filled", lib, vendor),
		twinValues("addressable", &lib, &vendor),
		sameValue("int64 extremes", map[string]int64{"max": math.MaxInt64, "min": math.MinInt64, "zero": 0}),
	)
}

// Row I3: uint8 and uint16 are int32; uint32, uint and uint64 are int64
// (int32 under minsize when they fit), and above MaxInt64 an error.
type unsignedProbe struct {
	U8      uint8
	U16     uint16
	U32     uint32
	U       uint
	U64     uint64
	Min32   uint32 `bson:",minsize"`
	MinU    uint   `bson:",minsize"`
	Min64   uint64 `bson:",minsize"`
	Named   namedUint
	Pointer *uint64
}

type namedUint uint64

func TestEncodeI3UnsignedWidthsAndOverflow(t *testing.T) {
	encoded := requireVendorOutcome(t, struct{ U uint32 }{U: 1}, "")
	if value := encodedElement(t, encoded, "u"); value.Type != vendorbson.TypeInt64 {
		t.Fatalf("the vendor wrote %s for a small uint32, want int64", value.Type)
	}
	requireVendorOutcome(t, struct{ U uint64 }{U: math.MaxUint64}, "18446744073709551615 overflows int64")
	overflow := uint64(math.MaxInt64) + 1
	requireEncodesLikeVendor(t,
		sameValue("zero", unsignedProbe{}),
		sameValue("maximums that fit", unsignedProbe{U8: math.MaxUint8, U16: math.MaxUint16, U32: math.MaxUint32, U: math.MaxInt64, U64: math.MaxInt64, Min32: math.MaxInt32, MinU: math.MaxInt32, Min64: math.MaxInt32, Named: math.MaxInt64}),
		sameValue("minsize past int32", unsignedProbe{Min32: math.MaxInt32 + 1, MinU: math.MaxUint32, Min64: math.MaxInt64}),
		sameValue("uint64 overflow", unsignedProbe{U64: overflow}),
		sameValue("uint overflow", unsignedProbe{U: math.MaxUint}),
		sameValue("named overflow", unsignedProbe{Named: math.MaxUint64}),
		sameValue("minsize overflow", unsignedProbe{Min64: math.MaxUint64}),
		sameValue("pointer overflow", unsignedProbe{Pointer: &overflow}),
		sameValue("overflow in a map", map[string]any{"u": overflow}),
		sameValue("top-level overflow before the top-level refusal", overflow),
		sameValue("top-level uint8", uint8(1)),
		sameValue("uintptr field", struct{ P uintptr }{}),
		sameValue("uintptr in an interface", map[string]any{"p": uintptr(1)}),
		sameValue("empty map of uintptr", map[string]uintptr{}),
		sameValue("empty slice of uintptr", struct{ L []uintptr }{L: []uintptr{}}),
	)
}

// Row T1: time.Time is a DateTime of the millisecond floor of Unix time,
// the zero time included.
type namedTime time.Time

func TestEncodeT1TimeMillisecondFloor(t *testing.T) {
	encoded := requireVendorOutcome(t, struct{ T time.Time }{T: time.Unix(1, 1_999_999)}, "")
	if value := encodedElement(t, encoded, "t"); value.Type != vendorbson.TypeDateTime || value.DateTime() != 1001 {
		t.Fatalf("the vendor wrote %s %d for 1.001999999s, want DateTime 1001", value.Type, value.DateTime())
	}
	zone := time.FixedZone("test", -7*3600)
	instants := map[string]time.Time{
		"zero":               {},
		"epoch":              time.Unix(0, 0),
		"sub-millisecond":    time.Unix(0, 999_999),
		"before the epoch":   time.Unix(-1, 1),
		"before, rounding":   time.Unix(-2, 999_999_999),
		"in a zone":          time.Date(2026, 10, 24, 12, 30, 15, 123_456_789, zone),
		"far future":         time.Date(9999, 12, 31, 23, 59, 59, 999_999_999, time.UTC),
		"past int64 millis":  time.Date(300_000_000, 1, 1, 0, 0, 0, 0, time.UTC),
		"monotonic reading":  time.Now(),
		"year one, last ns":  time.Date(1, 1, 1, 0, 0, 0, 999_999_999, time.UTC),
		"before year one":    time.Date(-100, 1, 1, 0, 0, 0, 1, time.UTC),
		"local of the epoch": time.Unix(0, 5_000_000).Local(),
	}
	var cases []encodeCase
	for _, name := range sortedTimeNames(instants) {
		instant := instants[name]
		cases = append(cases, sameValue(name, struct {
			T time.Time
			P *time.Time
			A interface{}
			L []time.Time
			M map[string]time.Time
		}{T: instant, P: &instant, A: instant, L: []time.Time{instant}, M: map[string]time.Time{"t": instant}}))
	}
	requireEncodesLikeVendor(t, cases...)
	requireEncodesLikeVendor(t,
		sameValue("named time is a struct", struct{ T namedTime }{T: namedTime(time.Unix(5, 0))}),
		sameValue("top-level time", time.Unix(5, 0)),
		twinValues("DateTime", struct{ D primitive.DateTime }{D: -5}, struct{ D vendorprimitive.DateTime }{D: -5}),
	)
}

func sortedTimeNames(instants map[string]time.Time) []string {
	named := make(map[string]any, len(instants))
	for name := range instants {
		named[name] = nil
	}
	return sortedNames(named)
}

// Row B1 (encode): []byte is Binary subtype 0; a byte slice or array type
// is Binary too, but an element type named on byte is an array of int32.
type namedByte byte
type byteArray [4]byte

func TestEncodeB1ByteSliceAsGenericBinary(t *testing.T) {
	encoded := requireVendorOutcome(t, struct{ B []byte }{B: []byte("ab")}, "")
	if value := encodedElement(t, encoded, "b"); value.Type != vendorbson.TypeBinary || value.Data[4] != 0x00 {
		t.Fatalf("the vendor wrote %s % x for []byte, want Binary subtype 0", value.Type, value.Data)
	}
	payload := []byte{0x00, 0xff, 0x10, 0x00}
	requireEncodesLikeVendor(t,
		sameValue("[]byte", struct{ B []byte }{B: payload}),
		sameValue("named []byte", struct{ B namedBytes }{B: namedBytes(payload)}),
		sameValue("[4]byte", struct{ B byteArray }{B: byteArray{1, 2, 3, 4}}),
		sameValue("addressable [4]byte", &struct{ B byteArray }{B: byteArray{1, 2, 3, 4}}),
		sameValue("[]namedByte", struct{ B []namedByte }{B: []namedByte{1, 2}}),
		sameValue("[2]namedByte", struct{ B [2]namedByte }{B: [2]namedByte{1, 2}}),
		sameValue("[]byte in an interface", map[string]any{"b": payload}),
		sameValue("top-level []byte", payload),
		sameValue("top-level array", byteArray{}),
	)
	var cases []encodeCase
	for _, subtype := range []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x80, 0xff} {
		name := fmt.Sprintf("Binary subtype %#x", subtype)
		cases = append(cases, twinValues(name,
			struct{ B primitive.Binary }{B: primitive.Binary{Subtype: subtype, Data: payload}},
			struct{ B vendorprimitive.Binary }{B: vendorprimitive.Binary{Subtype: subtype, Data: payload}}))
	}
	requireEncodesLikeVendor(t, cases...)
}

// Row F1: interface fields and map values dispatch on the dynamic type;
// map keys follow the vendor's rules, which accept more than strings.
type stringerFunc func()

func (stringerFunc) String() string { return "func" }

type plainStringer struct{ Number int }

func (stringer plainStringer) String() string { return "plain" }

type keyByValue string

func (key keyByValue) MarshalKey() (string, error) { return "km:" + string(key), nil }

type keyByPointer struct{ Name string }

func (key *keyByPointer) MarshalKey() (string, error) { return "kp:" + key.Name, nil }

type textKey int

func (key textKey) MarshalText() ([]byte, error) {
	return []byte(fmt.Sprintf("text-%d", int(key))), nil
}

type textPointerKey struct{ Name string }

func (key *textPointerKey) MarshalText() ([]byte, error) { return []byte("tp:" + key.Name), nil }

type failingKey int

func (failingKey) MarshalKey() (string, error) { return "", errors.New("the key refuses itself") }

type failingTextKey int

func (failingTextKey) MarshalText() ([]byte, error) {
	return nil, errors.New("the text refuses itself")
}

type bothKey int

func (bothKey) MarshalKey() (string, error)  { return "key-first", nil }
func (bothKey) MarshalText() ([]byte, error) { return []byte("text-second"), nil }

type stringTextKey string

func (stringTextKey) MarshalText() ([]byte, error) { return []byte("never"), nil }

type interfaceProbe struct {
	Any      interface{}
	Stringer fmt.Stringer
	Err      error
}

func TestEncodeF1InterfaceAndMapKeys(t *testing.T) {
	requireVendorOutcome(t, map[interface{}]int{"a": 1}, "unsupported key type: interface {}")
	requireEncodesLikeVendor(t,
		sameValue("zero", interfaceProbe{}),
		sameValue("dynamic values", interfaceProbe{Any: map[string]any{"n": 1, "s": []any{"x", 2.5, true}}, Stringer: plainStringer{Number: 3}, Err: errors.New("e")}),
		sameValue("dynamic pointers", interfaceProbe{Any: &plainStringer{Number: 4}, Stringer: &plainStringer{Number: 5}}),
		sameValue("typed nil in an interface", interfaceProbe{Any: (*plainStringer)(nil), Stringer: (*plainStringer)(nil)}),
		sameValue("dynamic func", interfaceProbe{Any: func() {}}),
		sameValue("dynamic func with a method", interfaceProbe{Stringer: stringerFunc(func() {})}),
		sameValue("dynamic chan in a map", map[string]any{"c": make(chan int)}),
		sameValue("non-empty interface map values", map[string]fmt.Stringer{"a": plainStringer{}, "b": nil}),
		sameValue("non-empty interface slice", struct{ L []fmt.Stringer }{L: []fmt.Stringer{plainStringer{}, nil}}),
		sameValue("map of func", map[string]func(){}),
		sameValue("slice of func", struct{ L []func() }{L: []func(){}}),
		sameValue("array of func", struct{ L [0]func() }{}),
		sameValue("pointer to interface{}", struct{ P *interface{} }{P: pointerToAny(5)}),
		sameValue("pointer to nil interface{}", struct{ P *interface{} }{P: pointerToAny(nil)}),
	)
	pointerName := &keyByPointer{Name: "p"}
	textPointer := &textPointerKey{Name: "t"}
	requireEncodesLikeVendor(t,
		sameValue("named string keys", map[planKeyName]int{"b": 1, "a": 2}),
		sameValue("int keys", map[int]string{-1: "a", 10: "b", 9: "c", math.MinInt64: "d"}),
		sameValue("int8 keys", map[int8]bool{-128: true, 127: false}),
		sameValue("uint64 keys", map[uint64]int{math.MaxUint64: 1, 0: 2}),
		sameValue("uintptr keys", map[uintptr]int{7: 1}),
		sameValue("KeyMarshaler by value", map[keyByValue]int{"a": 1, "b": 2}),
		sameValue("KeyMarshaler by pointer", map[*keyByPointer]int{pointerName: 1}),
		sameValue("nil KeyMarshaler pointer", map[*keyByPointer]int{nil: 1}),
		sameValue("TextMarshaler", map[textKey]int{1: 1, 20: 2}),
		sameValue("TextMarshaler by pointer", map[*textPointerKey]int{textPointer: 1}),
		sameValue("nil TextMarshaler pointer", map[*textPointerKey]int{nil: 1}),
		sameValue("KeyMarshaler before TextMarshaler", map[bothKey]int{1: 1}),
		sameValue("string kind before TextMarshaler", map[stringTextKey]int{"s": 1}),
		sameValue("failing KeyMarshaler", map[failingKey]int{1: 1}),
		sameValue("failing TextMarshaler", map[failingTextKey]int{1: 1}),
		sameValue("interface keys holding hooks", map[interface{}]int{textKey(3): 1, keyByValue("k"): 2}),
		sameValue("interface key holding a string", map[interface{}]int{"a": 1}),
		sameValue("interface key holding an int", map[interface{}]int{5: 1}),
		sameValue("nil interface key", map[interface{}]int{nil: 1}),
		sameValue("non-empty interface key", map[fmt.Stringer]int{plainStringer{}: 1}),
		sameValue("float keys", map[float64]int{1.5: 1}),
		sameValue("bool keys", map[bool]int{true: 1}),
		sameValue("array keys", map[[2]int]int{{1, 2}: 1}),
		sameValue("struct keys", map[plainStruct]int{{Number: 1}: 1}),
		sameValue("empty map of unsupported keys", map[float64]int{}),
		sameValue("map key in a struct", struct{ M map[int]int }{M: map[int]int{3: 4}}),
	)
	// The vendor's pointer codec names the element type the first time a
	// pointer type fails and the pointer type after that; both are its
	// outcomes, and the library must give one of them.
	pointerToInterface := struct{ P *fmt.Stringer }{P: new(fmt.Stringer)}
	_, firstText := vendorEncode(pointerToInterface)
	_, laterText := vendorEncode(pointerToInterface)
	_, libText := libraryEncode(pointerToInterface)
	if libText != firstText && libText != laterText {
		t.Errorf("pointer to an interface: library %q, vendor %q then %q", libText, firstText, laterText)
	}
}

func pointerToAny(value any) *interface{} { return &value }

// Rows N1, N2, N3: unexported fields are skipped, an untagged field is the
// lower-cased Go name, and a json tag is ignored.
func TestEncodeN1N2N3FieldNames(t *testing.T) {
	encoded := requireVendorOutcome(t, insertRequestShape{}, "")
	if _, err := vendorbson.Raw(encoded).LookupErr("writeconcern"); err != nil {
		t.Fatalf("the vendor did not write writeconcern for a zero InsertRequest: %v", err)
	}
	var cases []encodeCase
	for _, probe := range []any{namingProbe{}, insertRequestShape{}, repairReadShape{}, NamingExported{}} {
		cases = append(cases, sameValue(fmt.Sprintf("zero %T", probe), probe))
		filled := planFilled(probe)
		cases = append(cases, sameValue(fmt.Sprintf("filled %T", probe), filled.Interface()),
			sameValue(fmt.Sprintf("addressable %T", probe), filled.Addr().Interface()))
	}
	requireEncodesLikeVendor(t, cases...)
	requireEncodesLikeVendor(t,
		sameValue("unexported only", struct{ hidden, other string }{hidden: "h"}),
		sameValue("zero-byte key", struct {
			A string `bson:"a\x00b"`
		}{}),
		sameValue("zero-byte map key", map[string]int{"a\x00": 1}),
	)
}

// Row L1: inline flattening with the vendor's dominance rule; the inline
// map's entries after the fields; refusals at encode time.
type inlineMapAfter struct {
	Name  string
	Count int
	Extra map[string]interface{} `bson:",inline"`
}

type inlineMapOfFunc struct {
	Name  string
	Extra map[string]func() `bson:",inline"`
}

type unexportedInline struct {
	Name           string
	namingEmbedded `bson:",inline"`
}

func TestEncodeL1Inline(t *testing.T) {
	requireVendorOutcome(t, inlineMapAfter{Extra: map[string]interface{}{"name": 1}}, "Key name of inlined map conflicts with a struct field name")
	var cases []encodeCase
	for _, probe := range []any{
		dominanceProbe{}, duplicateAtSameDepth{}, duplicateResolvedByShallower{}, duplicateTopLevel{},
		taggedDeeperLoses{}, deeperBranches{}, inlinePointerProbe{}, inlineOrderProbe{},
		InlineMapHolder{}, nestedInlineMap{}, twoInlineMaps{}, namedKeyInlineMap{}, intKeyInlineMap{},
		inlineString{}, inlinePointerToString{}, inlineInterface{}, inlinePointerToMap{}, inlineBadInner{},
		dashNamedInline{}, holdsBadInnerByPointer{}, throughValueThenPointer{}, unexportedInline{},
	} {
		cases = append(cases, sameValue(fmt.Sprintf("zero %T", probe), probe))
		filled := planFilled(probe)
		cases = append(cases, sameValue(fmt.Sprintf("filled %T", probe), filled.Interface()))
	}
	requireEncodesLikeVendor(t, cases...)
	requireEncodesLikeVendor(t,
		sameValue("inline map after the fields", inlineMapAfter{Name: "n", Count: 2, Extra: map[string]interface{}{"z": 1, "a": "x", "Name": true}}),
		sameValue("nil inline map", inlineMapAfter{Name: "n"}),
		sameValue("inline map collision", inlineMapAfter{Extra: map[string]interface{}{"count": 1}}),
		sameValue("inline map of func, nil", inlineMapOfFunc{}),
		sameValue("nil inline pointer skips its fields", inlinePointerProbe{Before: "b", After: "a"}),
		sameValue("bad struct reached through a nil pointer", holdsBadInnerByPointer{Name: "n"}),
		sameValue("bad struct reached through a pointer", holdsBadInnerByPointer{Inner: &duplicateAtSameDepth{}}),
	)
}

// planFilled returns an addressable copy of probe with every field set.
func planFilled(probe any) reflect.Value {
	filled := reflect.New(reflect.TypeOf(probe)).Elem()
	counter := 0
	planFill(filled, &counter)
	return filled
}

// Row H1: MarshalBSON, MarshalBSONValue and ProxyBSON, found on the value or
// only on the pointer, which is used when the value is addressable.
type valueHookDocument struct{ Number int32 }

func (hook valueHookDocument) MarshalBSON() ([]byte, error) {
	return vendorbson.Marshal(vendorbson.D{{Key: "value_hook", Value: hook.Number}})
}

type pointerHookDocument struct{ Number int32 }

func (hook *pointerHookDocument) MarshalBSON() ([]byte, error) {
	if hook == nil {
		return vendorbson.Marshal(vendorbson.D{{Key: "nil_receiver", Value: true}})
	}
	return vendorbson.Marshal(vendorbson.D{{Key: "pointer_hook", Value: hook.Number}})
}

type hookBytes []byte

func (hook hookBytes) MarshalBSON() ([]byte, error) { return hook, nil }

type failingHook struct{}

func (failingHook) MarshalBSON() ([]byte, error) { return nil, errors.New("the hook refuses") }

type libValueHook struct{ Text string }

func (hook libValueHook) MarshalBSONValue() (bsontype.Type, []byte, error) {
	return bson.TypeString, appendTestString(nil, hook.Text), nil
}

type vendorValueHook struct{ Text string }

func (hook vendorValueHook) MarshalBSONValue() (vendorbsontype.Type, []byte, error) {
	return vendorbson.TypeString, appendTestString(nil, hook.Text), nil
}

type libPointerValueHook struct{ Text string }

func (hook *libPointerValueHook) MarshalBSONValue() (bsontype.Type, []byte, error) {
	return bson.TypeString, appendTestString(nil, "pointer "+hook.Text), nil
}

type vendorPointerValueHook struct{ Text string }

func (hook *vendorPointerValueHook) MarshalBSONValue() (vendorbsontype.Type, []byte, error) {
	return vendorbson.TypeString, appendTestString(nil, "pointer "+hook.Text), nil
}

type libGarbageValueHook struct{}

func (libGarbageValueHook) MarshalBSONValue() (bsontype.Type, []byte, error) {
	return 0x00, []byte{0xde, 0xad}, nil
}

type vendorGarbageValueHook struct{}

func (vendorGarbageValueHook) MarshalBSONValue() (vendorbsontype.Type, []byte, error) {
	return 0x00, []byte{0xde, 0xad}, nil
}

type libBothHooks struct{ Number int32 }

func (hook libBothHooks) MarshalBSONValue() (bsontype.Type, []byte, error) {
	return bson.TypeInt32, []byte{byte(hook.Number), 0, 0, 0}, nil
}
func (hook libBothHooks) MarshalBSON() ([]byte, error) { return valueHookDocument(hook).MarshalBSON() }

type vendorBothHooks struct{ Number int32 }

func (hook vendorBothHooks) MarshalBSONValue() (vendorbsontype.Type, []byte, error) {
	return vendorbson.TypeInt32, []byte{byte(hook.Number), 0, 0, 0}, nil
}
func (hook vendorBothHooks) MarshalBSON() ([]byte, error) {
	return valueHookDocument(hook).MarshalBSON()
}

type proxyValue struct{ Returns any }

func (proxy proxyValue) ProxyBSON() (interface{}, error) { return proxy.Returns, nil }

type proxyPointer struct{ Number int }

func (proxy *proxyPointer) ProxyBSON() (interface{}, error) {
	return fmt.Sprintf("proxied %d", proxy.Number), nil
}

type failingProxy struct{}

func (failingProxy) ProxyBSON() (interface{}, error) { return nil, errors.New("the proxy refuses") }

// proxyThenPointerHook has ProxyBSON on the value and MarshalBSON on the
// pointer: addressable, the pointer's hook; not, the value's.
type proxyThenPointerHook struct{ Number int32 }

func (hook proxyThenPointerHook) ProxyBSON() (interface{}, error) { return "proxied value", nil }
func (hook *proxyThenPointerHook) MarshalBSON() ([]byte, error) {
	return (*pointerHookDocument)(nil).MarshalBSON()
}

type hookInterface interface {
	MarshalBSON() ([]byte, error)
}

type hookHolder struct {
	Value        valueHookDocument
	ValuePtr     *valueHookDocument
	Pointer      pointerHookDocument
	PointerPtr   *pointerHookDocument
	Slice        []pointerHookDocument
	Map          map[string]pointerHookDocument
	Array        [1]pointerHookDocument
	Interface    hookInterface
	Proxy        *proxyValue
	ProxyPointer proxyPointer
	Mixed        proxyThenPointerHook
}

func filledHookHolder() hookHolder {
	return hookHolder{
		Value: valueHookDocument{Number: 1}, ValuePtr: &valueHookDocument{Number: 2},
		Pointer: pointerHookDocument{Number: 3}, PointerPtr: &pointerHookDocument{Number: 4},
		Slice: []pointerHookDocument{{Number: 5}}, Map: map[string]pointerHookDocument{"m": {Number: 6}},
		Array: [1]pointerHookDocument{{Number: 7}}, Interface: valueHookDocument{Number: 8},
		Proxy: &proxyValue{Returns: 9}, ProxyPointer: proxyPointer{Number: 10}, Mixed: proxyThenPointerHook{Number: 11},
	}
}

func TestEncodeH1MarshalBSONReceivers(t *testing.T) {
	holder := filledHookHolder()
	nonAddressed := requireVendorOutcome(t, holder, "")
	addressed := requireVendorOutcome(t, &holder, "")
	if encodedElement(t, nonAddressed, "pointer").Type == encodedElement(t, addressed, "pointer").Type &&
		string(encodedElement(t, nonAddressed, "pointer").Data) == string(encodedElement(t, addressed, "pointer").Data) {
		t.Fatalf("the vendor encoded a pointer-receiver hook field the same way addressable or not")
	}
	requireEncodesLikeVendor(t,
		sameValue("zero holder", hookHolder{}),
		sameValue("addressable zero holder", &hookHolder{}),
		sameValue("holder", holder),
		sameValue("addressable holder", &holder),
		sameValue("top-level value hook", valueHookDocument{Number: 1}),
		sameValue("top-level pointer to value hook", &valueHookDocument{Number: 1}),
		sameValue("top-level pointer hook by value", pointerHookDocument{Number: 2}),
		sameValue("top-level pointer hook by pointer", &pointerHookDocument{Number: 2}),
		sameValue("top-level nil pointer hook", (*pointerHookDocument)(nil)),
		sameValue("hook error", map[string]any{"h": failingHook{}}),
		sameValue("top-level hook error", failingHook{}),
		sameValue("proxy error", map[string]any{"p": failingProxy{}}),
		sameValue("proxy of nil", map[string]any{"p": proxyValue{}}),
		sameValue("proxy of a pointer", map[string]any{"p": proxyValue{Returns: &plainStruct{Number: 1}}}),
		sameValue("proxy of a struct", map[string]any{"p": proxyValue{Returns: plainStruct{Number: 2}}}),
		sameValue("proxy of a func", map[string]any{"p": proxyValue{Returns: func() {}}}),
		sameValue("proxy of a proxy", map[string]any{"p": proxyValue{Returns: proxyValue{Returns: "deep"}}}),
		sameValue("top-level proxy of a struct", proxyValue{Returns: plainStruct{Number: 3}}),
		sameValue("top-level proxy of an int", proxyValue{Returns: 3}),
		sameValue("nil hook interface in a map", map[string]hookInterface{"n": nil}),
		sameValue("hook slice elements are addressable", struct{ L []pointerHookDocument }{L: []pointerHookDocument{{Number: 1}}}),
		sameValue("hook interface holding a nil pointer-receiver hook", map[string]hookInterface{"n": (*pointerHookDocument)(nil)}),
		twinValues("pointer hook in a D element is not addressable", bson.D{{Key: "h", Value: pointerHookDocument{Number: 1}}}, vendorbson.D{{Key: "h", Value: pointerHookDocument{Number: 1}}}),
	)
	// MarshalBSON bytes are copied unchecked below the top level, and checked
	// element by element at the top level.
	malformed := map[string][]byte{
		"short":                        {1, 2},
		"length past the end":          {9, 0, 0, 0, 0},
		"negative length":              {0, 0, 0, 0x80, 0},
		"no terminator":                {5, 0, 0, 0, 0x0a},
		"only a length":                {4, 0, 0, 0},
		"early terminator":             {7, 0, 0, 0, 0, 1, 2},
		"key without end":              {7, 0, 0, 0, 0x0a, 'a', 'b'},
		"value too short":              {9, 0, 0, 0, 0x10, 'a', 0, 1, 0},
		"unknown type":                 {8, 0, 0, 0, 0x20, 'a', 0, 0},
		"trailing bytes":               {5, 0, 0, 0, 0, 0xff, 0xff},
		"nested document copied as is": {13, 0, 0, 0, 0x03, 'd', 0, 5, 0, 0, 0, 0xff, 0},
	}
	for _, name := range sortedByteNames(malformed) {
		requireEncodesLikeVendor(t,
			sameValue("top-level "+name, hookBytes(malformed[name])),
			sameValue("nested "+name, map[string]any{"h": hookBytes(malformed[name])}))
	}
	requireEncodesLikeVendor(t,
		twinValues("value marshaler field", struct{ V libValueHook }{V: libValueHook{Text: "v"}}, struct{ V vendorValueHook }{V: vendorValueHook{Text: "v"}}),
		twinValues("value marshaler nil pointer", struct{ V *libValueHook }{}, struct{ V *vendorValueHook }{}),
		twinValues("pointer value marshaler, addressable", &struct{ V libPointerValueHook }{V: libPointerValueHook{Text: "p"}}, &struct{ V vendorPointerValueHook }{V: vendorPointerValueHook{Text: "p"}}),
		twinValues("pointer value marshaler, not addressable", struct{ V libPointerValueHook }{V: libPointerValueHook{Text: "p"}}, struct{ V vendorPointerValueHook }{V: vendorPointerValueHook{Text: "p"}}),
		twinValues("top-level value marshaler", libValueHook{Text: "t"}, vendorValueHook{Text: "t"}),
		twinValues("value marshaler of type 0", map[string]any{"g": libGarbageValueHook{}}, map[string]any{"g": vendorGarbageValueHook{}}),
		twinValues("value marshaler before marshaler", map[string]any{"b": libBothHooks{Number: 4}}, map[string]any{"b": vendorBothHooks{Number: 4}}),
		twinValues("top-level: marshaler before value marshaler", libBothHooks{Number: 4}, vendorBothHooks{Number: 4}),
	)
}

func sortedByteNames(named map[string][]byte) []string {
	names := make(map[string]any, len(named))
	for name := range named {
		names[name] = nil
	}
	return sortedNames(names)
}

// TestEncodeMapsInSortedKeyOrder is O11: the library writes a map's entries
// in ascending byte order of their encoded keys, at every map level, so its
// output is deterministic and one the vendor could have produced.
func TestEncodeMapsInSortedKeyOrder(t *testing.T) {
	keys := []string{"b", "a", "c", "aa", "B", "ä", "10", "9", "", "a\xff", "z", "_id"}
	flat := map[string]any{}
	for position, key := range keys {
		flat[key] = position
	}
	nested := map[string]any{
		"outer": map[string]any{"y": 1, "x": map[string]int{"q": 1, "p": 2}},
		"list":  []any{map[string]int{"d": 1, "c": 2}, flat},
		"b":     true,
	}
	libNested := map[string]any{"in d": bson.D{{Key: "z", Value: nested}, {Key: "a", Value: bson.M{"k": 1, "j": 2}}}}
	vendorNested := map[string]any{"in d": vendorbson.D{{Key: "z", Value: nested}, {Key: "a", Value: vendorbson.M{"k": 1, "j": 2}}}}
	inline := inlineMapAfter{Name: "n", Extra: map[string]interface{}{"z": 1, "m": 2, "a": 3}}
	hookKeys := map[textKey]int{100: 1, 9: 2, 10: 3}
	intKeys := map[int]int{100: 1, 9: 2, 10: 3, -1: 4}
	requireEncodesLikeVendor(t,
		sameValue("flat", flat),
		twinValues("nested", libNested, vendorNested),
		sameValue("inline map", inline),
		sameValue("text keys", hookKeys),
		sameValue("int keys", intKeys),
	)
	for _, sortedCase := range []struct {
		name  string
		value any
	}{
		{"flat", flat}, {"nested", libNested}, {"inline map", inline}, {"text keys", hookKeys}, {"int keys", intKeys},
	} {
		first, failure := libraryEncode(sortedCase.value)
		if failure != "" {
			t.Fatalf("%s: %s", sortedCase.name, failure)
		}
		requireSortedMapLevels(t, sortedCase.name, sortedCase.value, first)
		for run := 0; run < 50; run++ {
			again, _ := libraryEncode(sortedCase.value)
			if string(again) != string(first) {
				t.Fatalf("%s: run %d encoded differently:\n % x\n % x", sortedCase.name, run, first, again)
			}
		}
	}
}

func TestMarshalOutputSurvivesBufferReuse(t *testing.T) {
	kept, failure := libraryEncode(benchdata.Ref450LibRecord())
	if failure != "" {
		t.Fatal(failure)
	}
	want := append([]byte(nil), kept...)
	for run := 0; run < 1000; run++ {
		other := map[string]any{"run": run, "filler": fmt.Sprintf("%0*d", run%600, run), "list": []int{run, run + 1}}
		if _, failure := libraryEncode(other); failure != "" {
			t.Fatal(failure)
		}
	}
	if string(kept) != string(want) {
		t.Fatalf("a document the caller kept changed after 1,000 more encodes:\n kept now: % x\n was:      % x", kept, want)
	}
}

// Every type codec of the vendor's default registry that the contract
// table does not name: the primitive types, json.Number, url.URL, bsoncore
// and bson's raw types, in a field, in a map, and at the top level.
func TestEncodeTypeCodecsMatchTheVendor(t *testing.T) {
	objectID := vendorprimitive.ObjectID{0x65, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	decimal, err := vendorprimitive.ParseDecimal128("-1234.5678E-90")
	if err != nil {
		t.Fatal(err)
	}
	scope := vendorbson.D{{Key: "x", Value: int32(1)}}
	scalars := map[string]any{
		"objectid":        objectID,
		"decimal":         decimal,
		"javascript":      vendorprimitive.JavaScript("function() {}"),
		"symbol":          vendorprimitive.Symbol("sym"),
		"undefined":       vendorprimitive.Undefined{},
		"datetime":        vendorprimitive.DateTime(-62135596800000),
		"null":            vendorprimitive.Null{},
		"regex":           vendorprimitive.Regex{Pattern: "^a.*", Options: "xsmi"},
		"regex unsorted":  vendorprimitive.Regex{Pattern: "p", Options: "zyxa\xffé"},
		"regex zero byte": vendorprimitive.Regex{Pattern: "a\x00", Options: "i"},
		"dbpointer":       vendorprimitive.DBPointer{DB: "db.coll", Pointer: objectID},
		"timestamp":       vendorprimitive.Timestamp{T: 0xdeadbeef, I: 7},
		"minkey":          vendorprimitive.MinKey{},
		"maxkey":          vendorprimitive.MaxKey{},
		"cws":             vendorprimitive.CodeWithScope{Code: "x", Scope: scope},
		"cws map scope":   vendorprimitive.CodeWithScope{Code: "x", Scope: map[string]int{"b": 1}},
		"cws nil scope":   vendorprimitive.CodeWithScope{Code: "x"},
		"cws int scope":   vendorprimitive.CodeWithScope{Code: "x", Scope: int32(1)},
		"cws nil map":     vendorprimitive.CodeWithScope{Code: "x", Scope: map[string]int(nil)},
		"cws nil D":       vendorprimitive.CodeWithScope{Code: "x", Scope: vendorbson.D(nil)},
		"cws raw":         vendorprimitive.CodeWithScope{Code: "x", Scope: vendorbson.Raw{5, 0, 0, 0, 0}},
		"raw":             vendorbson.Raw{12, 0, 0, 0, 0x08, 'b', 0, 1, 0x0a, 'n', 0, 0},
		"raw nil":         vendorbson.Raw(nil),
		"raw short":       vendorbson.Raw{4, 0, 0},
		"raw trailing":    vendorbson.Raw{5, 0, 0, 0, 0, 0xff},
		"rawvalue":        vendorbson.RawValue{Type: vendorbson.TypeInt32, Value: []byte{1, 0, 0, 0}},
		"rawvalue zero":   vendorbson.RawValue{},
		"rawvalue bad":    vendorbson.RawValue{Type: 0x20, Value: []byte{1}},
		"rawvalue short":  vendorbson.RawValue{Type: vendorbson.TypeInt64, Value: []byte{1}},
		"core document":   vendorbsoncore.Document{12, 0, 0, 0, 0x08, 'b', 0, 1, 0x0a, 'n', 0, 0},
		"core doc nil":    vendorbsoncore.Document(nil),
		"core array":      vendorbsoncore.Array{16, 0, 0, 0, 0x08, 'x', 0, 1, 0x0a, 'y', 'y', 0, 0x0a, '7', 0, 0},
		"core array bad":  vendorbsoncore.Array{9, 0, 0, 0},
	}
	var cases []encodeCase
	for _, name := range sortedNames(scalars) {
		vendorValue := scalars[name]
		libValue := libraryTwin(t, vendorValue)
		cases = append(cases,
			// One key, so that bytes a type copies unchecked, which only a raw
			// comparison can judge, come in one order.
			twinValues(name+" in a map", map[string]any{"v": libValue}, map[string]any{"v": vendorValue}),
			twinValues(name+" in a struct", struct {
				V any
				W int
			}{V: libValue, W: 1}, struct {
				V any
				W int
			}{V: vendorValue, W: 1}),
			twinValues(name+" in a D", bson.D{{Key: "v", Value: libValue}}, vendorbson.D{{Key: "v", Value: vendorValue}}),
			twinValues(name+" at the top level", libValue, vendorValue),
		)
	}
	requireEncodesLikeVendor(t, cases...)
	location, err := url.Parse("https://user:pw@example.com:8080/a%20b?q=1&r=%2F#frag")
	if err != nil {
		t.Fatal(err)
	}
	requireEncodesLikeVendor(t,
		sameValue("url", struct{ U url.URL }{U: *location}),
		sameValue("url addressable", &struct{ U url.URL }{U: *location}),
		sameValue("url pointer", struct{ U *url.URL }{U: location}),
		sameValue("zero url", struct{ U url.URL }{}),
		sameValue("top-level url", *location),
		sameValue("json numbers", map[string]any{"int": json.Number("42"), "neg": json.Number("-7"), "float": json.Number("1.5"), "big": json.Number("9223372036854775808"), "exp": json.Number("1e3")}),
		sameValue("json number empty", map[string]any{"n": json.Number("")}),
		sameValue("json number text", map[string]any{"n": json.Number("abc")}),
		sameValue("json number out of range", map[string]any{"n": json.Number("1e400")}),
		sameValue("top-level json number", json.Number("3")),
		sameValue("float32", struct{ F float32 }{F: 1.1}),
		sameValue("special floats", map[string]float64{"nan": math.NaN(), "inf": math.Inf(1), "-inf": math.Inf(-1), "-0": math.Copysign(0, -1), "max": math.MaxFloat64, "tiny": math.SmallestNonzeroFloat64}),
		sameValue("strings with zero bytes", map[string]string{"s": "a\x00b", "u": "\xff\xfe"}),
		sameValue("bool", struct{ T, F bool }{T: true}),
		sameValue("top-level string", "s"),
		sameValue("top-level bool", true),
		sameValue("top-level float", 1.5),
	)
}

// TestEncodeTreeShapesLikeTheVendor encodes copies of the field shapes of
// structs the hoardDB tree encodes: request and response wire structs,
// catalog records with their hook and recursion.
type getResponseShape[Raw any] struct {
	Found   bool   `bson:"found" json:"found"`
	Data    Raw    `bson:"data,omitempty" json:"data,omitempty"`
	Message string `bson:"message" json:"message,omitempty"`
}

type popResponseShape[Raw any] struct {
	Found             bool   `bson:"found" json:"found"`
	Items             []Raw  `bson:"items,omitempty" json:"items,omitempty"`
	Message           string `bson:"message" json:"message,omitempty"`
	CopiesConfirmed   int    `bson:"copies_confirmed,omitempty" json:"copies_confirmed,omitempty"`
	ReplicationFactor int    `bson:"replication_factor,omitempty" json:"replication_factor,omitempty"`
	WriteOutcome      string `bson:"write_outcome,omitempty" json:"write_outcome,omitempty"`
	Pending           int64  `bson:"pending,omitempty" json:"pending,omitempty"`
}

type catalogIndexShape struct {
	Field              string   `bson:"field,omitempty"`
	Type               string   `bson:"type,omitempty"`
	ID                 int32    `bson:"id"`
	Name               string   `bson:"name"`
	Fields             []string `bson:"fields,omitempty"`
	Unique             bool     `bson:"unique,omitempty"`
	ExpireAfterSeconds *int64   `bson:"expire_after_seconds,omitempty"`
	State              string   `bson:"state,omitempty"`
}

type bucketTypeShape string

type catalogEntryShape struct {
	database           string
	ID                 int64               `bson:"id"`
	Slot               int32               `bson:"slot"`
	Type               bucketTypeShape     `bson:"type"`
	Indices            []catalogIndexShape `bson:"indices,omitempty"`
	CreatedAt          time.Time           `bson:"created_at"`
	NextIndexID        *int32              `bson:"next_index_id,omitempty"`
	NextStoreID        int32               `bson:"next_store_id,omitempty"`
	DatabaseGeneration int64               `bson:"db_gen,omitempty"`
	Fields             []catalogFieldShape `bson:"fields,omitempty"`
	FieldTypes         string              `bson:"field_types,omitempty"`
}

type catalogTypeShape struct {
	Type    string              `bson:"type"`
	Members []catalogFieldShape `bson:"members,omitempty"`
	Elem    *catalogTypeShape   `bson:"elem,omitempty"`
}

// catalogFieldShape is server.catalogField: it embeds an unexported struct,
// which the codec skips, so it encodes through a value-receiver MarshalBSON
// over a flat record. Each library's side encodes the record with itself.
type catalogFieldShape struct {
	Name string
	catalogTypeShape
	Required          bool
	encodeWithLibrary bool
}

type catalogFieldRecordShape struct {
	Name     string              `bson:"name"`
	Type     string              `bson:"type"`
	Members  []catalogFieldShape `bson:"members,omitempty"`
	Elem     *catalogTypeShape   `bson:"elem,omitempty"`
	Required bool                `bson:"required,omitempty"`
}

func (field catalogFieldShape) MarshalBSON() ([]byte, error) {
	record := catalogFieldRecordShape{Name: field.Name, Type: field.Type, Members: field.Members, Elem: field.Elem, Required: field.Required}
	if field.encodeWithLibrary {
		return bson.Marshal(record)
	}
	return vendorbson.Marshal(record)
}

type fieldDeclarationShape struct {
	Name     string                  `bson:"name"`
	Type     string                  `bson:"type"`
	Required bool                    `bson:"required,omitempty"`
	Members  []fieldDeclarationShape `bson:"members,omitempty"`
	Elem     *fieldDeclarationShape  `bson:"elem,omitempty"`
}

func catalogEntryOf(encodeWithLibrary bool) catalogEntryShape {
	expiry, nextIndex := int64(3600), int32(2)
	member := catalogFieldShape{Name: "city", catalogTypeShape: catalogTypeShape{Type: "string"}, Required: true, encodeWithLibrary: encodeWithLibrary}
	return catalogEntryShape{
		database: "app", ID: 7, Slot: 3, Type: "hash",
		Indices:   []catalogIndexShape{{Name: "by_city", Fields: []string{"city"}, ExpireAfterSeconds: &expiry}, {ID: 1, Name: "u", Unique: true, State: "building"}},
		CreatedAt: time.Date(2026, 10, 24, 1, 2, 3, 4_000_000, time.UTC), NextIndexID: &nextIndex, DatabaseGeneration: 9,
		Fields: []catalogFieldShape{
			{Name: "address", catalogTypeShape: catalogTypeShape{Type: "object", Members: []catalogFieldShape{member}}, encodeWithLibrary: encodeWithLibrary},
			{Name: "tags", catalogTypeShape: catalogTypeShape{Type: "array", Elem: &catalogTypeShape{Type: "string"}}, encodeWithLibrary: encodeWithLibrary},
		},
		FieldTypes: "warn",
	}
}

func TestEncodeTreeShapesLikeTheVendor(t *testing.T) {
	document := vendorbson.Raw{12, 0, 0, 0, 0x10, 'n', 0, 1, 0, 0, 0, 0}
	declaration := fieldDeclarationShape{Name: "a", Type: "object", Members: []fieldDeclarationShape{{Name: "b", Type: "array", Elem: &fieldDeclarationShape{Type: "int"}}}}
	insert := insertRequestShape{Database: "app", Bucket: "users", Data: map[string]interface{}{"name": "n", "age": 3, "tags": []any{"a"}}, RepairRead: &repairReadShape{Owner: "o", Term: 2}}
	requireEncodesLikeVendor(t,
		sameValue("insert request", insert),
		sameValue("insert request pointer", &insert),
		twinValues("get response", getResponseShape[bson.Raw]{Found: true, Data: bson.Raw(document)}, getResponseShape[vendorbson.Raw]{Found: true, Data: document}),
		twinValues("get response empty", getResponseShape[bson.Raw]{}, getResponseShape[vendorbson.Raw]{}),
		twinValues("pop response", popResponseShape[bson.Raw]{Found: true, Items: []bson.Raw{bson.Raw(document), bson.Raw(document)}, Pending: 2},
			popResponseShape[vendorbson.Raw]{Found: true, Items: []vendorbson.Raw{document, document}, Pending: 2}),
		twinValues("catalog entry", catalogEntryOf(true), catalogEntryOf(false)),
		sameValue("field declaration", declaration),
		sameValue("reason reply", struct {
			Reason  uint8  `bson:"reason"`
			Message string `bson:"message,omitempty"`
		}{Reason: 3}),
	)
}

// TestMarshalAllocsAtMostVendor is spec 9.2's struct-marshal row: the
// library allocates no more than the vendor to encode Ref450, measured here
// on the same input. The pointer, bson.D and map forms are held to the same
// bound.
func TestMarshalAllocsAtMostVendor(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts change under -race")
	}
	libRecord, vendorRecord := benchdata.Ref450LibRecord(), benchdata.Ref450Record()
	smallMap := map[string]any{"a": 1, "b": "x", "c": 2.5}
	for _, row := range []struct {
		name        string
		lib, vendor any
	}{
		{"Ref450 struct", libRecord, vendorRecord},
		{"Ref450 struct pointer", &libRecord, &vendorRecord},
		{"Ref450 bson.D", libraryTwin(t, benchdata.Ref450D()), benchdata.Ref450D()},
		{"three-key map", smallMap, smallMap},
	} {
		libBytes, libFailure := libraryEncode(row.lib)
		vendorBytes, vendorFailure := vendorEncode(row.vendor)
		if libFailure != "" || vendorFailure != "" || string(libBytes) != string(vendorBytes) && row.name != "three-key map" {
			t.Fatalf("%s: the two encodings differ (library %q, vendor %q)", row.name, libFailure, vendorFailure)
		}
		vendorAllocs := testing.AllocsPerRun(200, func() { _, _ = vendorbson.Marshal(row.vendor) })
		libAllocs := testing.AllocsPerRun(200, func() { _, _ = bson.Marshal(row.lib) })
		t.Logf("%s: library %.0f allocations, vendor %.0f", row.name, libAllocs, vendorAllocs)
		if libAllocs > vendorAllocs {
			t.Errorf("%s: the library allocates %.0f times per encode, the vendor %.0f", row.name, libAllocs, vendorAllocs)
		}
	}
}

// TestEncodeRefusesWhereTheVendorPanics lists the inputs on which the
// vendor's encoder panics. The library returns an error for each: a panic
// in an encode path would crash the server (AGENTS.md slop rule 1), and a
// panic is no outcome a caller can match.
func TestEncodeRefusesWhereTheVendorPanics(t *testing.T) {
	stringPastMaxInt32 := []byte{12, 0, 0, 0, 0x02, 'a', 0, 0xfe, 0xff, 0xff, 0x7f, 0}
	for _, panicking := range []encodeCase{
		// copier.go slices the document to its declared length minus 4.
		sameValue("top-level MarshalBSON declaring 3 bytes", hookBytes{3, 0, 0, 0}),
		sameValue("top-level MarshalBSON declaring 0 bytes", hookBytes{0, 0, 0, 0, 0}),
		// A value method called through a nil pointer: at the top level, in a
		// hook interface, and as an interface map key.
		sameValue("top-level nil pointer to a value hook", (*valueHookDocument)(nil)),
		sameValue("hook interface holding a nil pointer to a value hook", map[string]hookInterface{"n": (*valueHookDocument)(nil)}),
		sameValue("interface key holding a nil pointer to a value KeyMarshaler", map[interface{}]int{(*keyByValue)(nil): 1}),
		sameValue("interface key holding a nil pointer to a value TextMarshaler", map[interface{}]int{(*textKey)(nil): 1}),
		// ProxyEncodeValue takes the type of a nil pointer's element.
		sameValue("ProxyBSON returning a nil pointer", map[string]any{"p": proxyValue{Returns: (*int)(nil)}}),
		// bsoncore's valueLength adds 4 to a length near MaxInt32.
		twinValues("raw string length past MaxInt32", bson.Raw(stringPastMaxInt32), vendorbson.Raw(stringPastMaxInt32)),
	} {
		t.Run(panicking.name, func(t *testing.T) {
			if _, vendorFailure := vendorEncode(panicking.vendor); len(vendorFailure) < 6 || vendorFailure[:6] != "panic:" {
				t.Fatalf("the vendor no longer panics: %s", outcomeText(vendorFailure))
			}
			_, libFailure := libraryEncode(panicking.lib)
			if libFailure == "" || len(libFailure) >= 6 && libFailure[:6] == "panic:" {
				t.Errorf("the library's outcome is %s, want an error", outcomeText(libFailure))
			}
		})
	}
}
