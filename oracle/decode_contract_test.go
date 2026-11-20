// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"encoding/json"
	"math"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// One named test per decode row of the spec 5.3 contract table (build doc
// step 4e), each a differential against the vendor through compare.Decoded.

// decNested is a document whose every container level holds an embedded
// document and an array, three levels down, so a row sees the Go type an
// interface{} holder receives at each depth.
func decNested(t testing.TB) []byte {
	return decMarshal(t, vendorbson.D{
		{Key: "s", Value: "top"},
		{Key: "doc", Value: vendorbson.D{
			{Key: "n", Value: int32(1)},
			{Key: "inner", Value: vendorbson.D{{Key: "deep", Value: vendorbson.D{{Key: "x", Value: int64(2)}}}}},
			{Key: "list", Value: vendorbson.A{vendorbson.D{{Key: "in", Value: "array"}}, vendorbson.A{int32(3), vendorbson.D{}}}},
		}},
		{Key: "arr", Value: vendorbson.A{"a", vendorbson.D{{Key: "k", Value: vendorbson.D{{Key: "j", Value: true}}}}, vendorbson.A{vendorbson.A{}}}},
		{Key: "empty", Value: vendorbson.D{}},
	})
}

// decHolder is a struct whose interface{} field resets the decode ancestor
// (row D4).
type decHolder struct {
	V any
	W map[string]any
	L []any
}

// decInlineHolders keeps each unknown key in an inline map of holders, so the
// inline map's type is the ancestor when the holders decode.
type decInlineHolders struct {
	Known string
	Rest  map[string]decHolder `bson:",inline"`
}

type decInlineInterfaces struct {
	Known string
	Rest  map[string]any `bson:",inline"`
}

type decInlineM struct {
	Known string
	Rest  vendorbson.M `bson:",inline"`
}

type decInlineLibM struct {
	Known string
	Rest  bson.M `bson:",inline"`
}

type decInterfaceFields struct {
	V   any
	Doc any
	Arr any
}

// decNamedDocument is a slice of elements the vendor's slice codec reads as
// a document, setting the ancestor to its own type.
type decNamedDocument []vendorprimitive.E

type decLibNamedDocument []primitive.E

// The named document pair is a mirrored type pair for the harness: the
// vendor's slice codec makes decNamedDocument the ancestor, so it is the Go
// type an interface{} below it receives.
func init() {
	decLibraryTypes[reflect.TypeOf(decNamedDocument{})] = reflect.TypeOf(decLibNamedDocument{})
}

type decNamedMap map[string]any

// decInlineDs inlines a map of bson.D, so the inline map is the ancestor
// when each D decodes, and only D's own ancestor keeps the documents inside
// it primitive.D.
type decInlineDs struct {
	Known string
	Rest  map[string]vendorbson.D `bson:",inline"`
}

type decLibInlineDs struct {
	Known string
	Rest  map[string]bson.D `bson:",inline"`
}

func TestDecodeD1TopLevelInterfaceIsD(t *testing.T) {
	nested := decNested(t)
	decMustDecode(t, "D1", nested, decTargetInterface)
	decCheck(t, "D1", nested, decTargetInterface, decSame("*interface{} prefilled", func() any { return map[string]any{"old": 1} }))
	decCheck(t, "D1 empty document", decMarshal(t, vendorbson.D{}), decTargetInterface)
	var vendorTarget any
	if err := vendorbson.Unmarshal(nested, &vendorTarget); err != nil {
		t.Fatal(err)
	}
	var libraryTarget any
	if err := decLibraryDecoder.Unmarshal(nested, &libraryTarget); err != nil {
		t.Fatalf("library: %v", err)
	}
	if _, isD := libraryTarget.(primitive.D); !isD {
		t.Errorf("the library decoded a top-level *interface{} as %T, want primitive.D", libraryTarget)
	}
}

func TestDecodeD2InsideMapIsMap(t *testing.T) {
	nested := decNested(t)
	decMustDecode(t, "D2", nested, decTargetMap)
	decCheck(t, "D2", nested, decTargetMap, decTargetMapValue,
		decSame("*decNamedMap", decZero[decNamedMap]),
		decSame("*map[string]map[string]interface{}", decZero[map[string]map[string]any]),
		decSame("**map[string]interface{}", decZero[*map[string]any]))
	// Only a map of interface{} is the ancestor: a document inside a slice
	// or array of interface{} in a map of slices is primitive.D.
	inSlice := decMarshal(t, vendorbson.D{{Key: "s", Value: vendorbson.A{vendorbson.D{{Key: "x", Value: int32(1)}}}}})
	for _, target := range []decTarget{
		decSame("*map[string][]interface{}", decZero[map[string][]any]),
		decSame("*map[string][1]interface{}", decZero[map[string][1]any]),
	} {
		decMustDecode(t, "D2 map of slices", inSlice, target)
		decCheck(t, "D2 map of slices", inSlice, target)
	}
	var libraryTarget map[string]any
	if err := decLibraryDecoder.Unmarshal(nested, &libraryTarget); err != nil {
		t.Fatalf("library: %v", err)
	}
	if _, isMap := libraryTarget["doc"].(map[string]any)["inner"].(map[string]any); !isMap {
		t.Errorf("doc.inner decoded as %T, want map[string]interface{}", libraryTarget["doc"].(map[string]any)["inner"])
	}
}

func TestDecodeD3InsideBsonMIsM(t *testing.T) {
	nested := decNested(t)
	decMustDecode(t, "D3", nested, decTargetM)
	decCheck(t, "D3", nested, decTargetM, decTargetNilM,
		decTarget{"*map[string]bson.M", func() any { return &map[string]vendorbson.M{} }, func() any { return &map[string]bson.M{} }})
}

func TestDecodeD4StructInterfaceFieldResetsAncestor(t *testing.T) {
	holder := vendorbson.D{
		{Key: "v", Value: vendorbson.D{{Key: "x", Value: vendorbson.D{{Key: "y", Value: int32(1)}}}}},
		{Key: "w", Value: vendorbson.D{{Key: "z", Value: vendorbson.D{}}}},
		{Key: "l", Value: vendorbson.A{vendorbson.D{{Key: "q", Value: "r"}}}},
	}
	inlined := decMarshal(t, vendorbson.D{{Key: "known", Value: "k"}, {Key: "one", Value: holder}, {Key: "two", Value: holder}})
	inlineTarget := decSame("*decInlineHolders", decZero[decInlineHolders])
	decMustDecode(t, "D4", inlined, inlineTarget)
	decCheck(t, "D4 inline map of holders", inlined, inlineTarget)
	decCheck(t, "D4 holder", decMarshal(t, holder),
		decSame("*decHolder", decZero[decHolder]),
		decSame("*map[string]decHolder", decZero[map[string]decHolder]),
		decSame("*interface fields", decZero[decInterfaceFields]))
	var libraryTarget decInlineHolders
	if err := decLibraryDecoder.Unmarshal(inlined, &libraryTarget); err != nil {
		t.Fatalf("library: %v", err)
	}
	if _, isD := libraryTarget.Rest["one"].V.(primitive.D); !isD {
		t.Errorf("an interface{} field inside an inline map of structs decoded as %T, want primitive.D (the struct resets the ancestor)", libraryTarget.Rest["one"].V)
	}
}

func TestDecodeD5InsideInlineMap(t *testing.T) {
	document := decMarshal(t, vendorbson.D{
		{Key: "known", Value: "k"},
		{Key: "doc", Value: vendorbson.D{{Key: "a", Value: vendorbson.D{{Key: "b", Value: int32(1)}}}}},
		{Key: "arr", Value: vendorbson.A{vendorbson.D{{Key: "c", Value: "d"}}}},
		{Key: "scalar", Value: 1.5},
	})
	interfacesTarget := decSame("*decInlineInterfaces", decZero[decInlineInterfaces])
	decMustDecode(t, "D5", document, interfacesTarget)
	decCheck(t, "D5", document, interfacesTarget,
		decTarget{"*inline bson.M", func() any { return &decInlineM{} }, func() any { return &decInlineLibM{} }},
		decSame("*decInlineInterfaces prefilled", func() decInlineInterfaces {
			return decInlineInterfaces{Known: "old", Rest: map[string]any{"kept": int32(9)}}
		}))
	// A pointer passes the inline map's ancestor through to the interface{}.
	throughPointers := decMarshal(t, vendorbson.D{{Key: "known", Value: "k"}, {Key: "doc", Value: vendorbson.D{{Key: "a", Value: "x"}}}})
	pointersTarget := decSame("*decInlinePointers", decZero[decInlinePointers])
	decMustDecode(t, "D5 through a pointer", throughPointers, pointersTarget)
	decCheck(t, "D5 through a pointer", throughPointers, pointersTarget)
}

// decInlinePointers keeps unknown keys as pointers to interface{}.
type decInlinePointers struct {
	Known string
	Rest  map[string]*any `bson:",inline"`
}

// decTruncatedInner tags a struct field truncate, which its own fields
// inherit.
type decTruncatedInner struct {
	Inner struct{ N int32 } `bson:",truncate"`
}

func TestDecodeD6InsideSliceOrD(t *testing.T) {
	nested := decNested(t)
	decMustDecode(t, "D6", nested, decTargetD)
	decCheck(t, "D6", nested, decTargetD, decTargetNilD,
		decTarget{"*decNamedDocument", func() any { return &decNamedDocument{} }, func() any { return &decLibNamedDocument{} }},
		decTarget{"*[]primitive.E", func() any { return &[]vendorprimitive.E{} }, func() any { return &[]primitive.E{} }},
		decSame("*struct{L []interface{}}", decZero[struct{ L []any }]),
		decSame("*struct{Arr []interface{}}", decZero[struct{ Arr []any }]),
		decTarget{"*map[string]bson.D", func() any { return &map[string]vendorbson.D{} }, func() any { return &map[string]bson.D{} }})
	inlineDocuments := decMarshal(t, vendorbson.D{
		{Key: "known", Value: "k"},
		{Key: "one", Value: vendorbson.D{{Key: "x", Value: vendorbson.D{{Key: "y", Value: int32(1)}}}}},
		{Key: "two", Value: vendorbson.D{{Key: "z", Value: vendorbson.A{vendorbson.D{}}}}},
	})
	inlineDsTarget := decTarget{"*decInlineDs", func() any { return &decInlineDs{} }, func() any { return &decLibInlineDs{} }}
	decMustDecode(t, "D6 inline map of bson.D", inlineDocuments, inlineDsTarget)
	decCheck(t, "D6 inline map of bson.D", inlineDocuments, inlineDsTarget)
	// RawValue.Unmarshal: into interface{} primitive.D, into a map
	// map[string]interface{}.
	embedded := decMarshal(t, vendorbson.D{{Key: "a", Value: vendorbson.D{{Key: "b", Value: vendorbson.A{vendorbson.D{}}}}}})
	for _, target := range []decTarget{decTargetInterface, decTargetMap, decTargetD, decTargetM} {
		if divergence := decValueDivergence(0x03, embedded, target); divergence != "" {
			t.Errorf("RawValue.Unmarshal of an embedded document: %s", divergence)
		}
		array := decMarshal(t, vendorbson.D{{Key: "0", Value: vendorbson.D{{Key: "x", Value: int32(1)}}}})
		if divergence := decValueDivergence(0x04, array, target); divergence != "" {
			t.Errorf("RawValue.Unmarshal of an array: %s", divergence)
		}
	}
	var libraryTarget any
	if err := decLibraryDecoder.UnmarshalValue(0x03, embedded, &libraryTarget); err != nil {
		t.Fatalf("library: %v", err)
	}
	if _, isD := libraryTarget.(primitive.D); !isD {
		t.Errorf("RawValue.Unmarshal into interface{} gave %T, want primitive.D", libraryTarget)
	}
}

func TestDecodeD7ArrayIntoInterfaceIsA(t *testing.T) {
	nested := decNested(t)
	for _, target := range []decTarget{decTargetInterface, decTargetMap, decTargetM, decTargetD,
		decSame("*interface fields", decZero[decInterfaceFields])} {
		decCheck(t, "D7", nested, target)
	}
	arrays := decMarshal(t, vendorbson.D{{Key: "arr", Value: vendorbson.A{vendorbson.A{vendorbson.A{int32(1)}}, vendorbson.A{}}}})
	decCheck(t, "D7 nested arrays", arrays, decTargetInterface, decTargetMap, decSame("*interface fields", decZero[decInterfaceFields]))
	for _, valueType := range []byte{0x03, 0x04} {
		if divergence := decValueDivergence(valueType, decMarshal(t, vendorbson.D{{Key: "0", Value: vendorbson.A{int32(1)}}}), decTargetInterface); divergence != "" {
			t.Errorf("RawValue.Unmarshal of type %#x: %s", valueType, divergence)
		}
	}
}

// decNumericKinds are the Go numeric kinds of row I4, each with a named
// variant: the vendor converts a named type's value through its kind.
type (
	decNamedInt8    int8
	decNamedUint16  uint16
	decNamedFloat32 float32
	decNamedInt     int
)

var decNumericTypes = []reflect.Type{
	reflect.TypeOf(int(0)), reflect.TypeOf(int8(0)), reflect.TypeOf(int16(0)), reflect.TypeOf(int32(0)), reflect.TypeOf(int64(0)),
	reflect.TypeOf(uint(0)), reflect.TypeOf(uint8(0)), reflect.TypeOf(uint16(0)), reflect.TypeOf(uint32(0)), reflect.TypeOf(uint64(0)),
	reflect.TypeOf(float32(0)), reflect.TypeOf(float64(0)),
	reflect.TypeOf(decNamedInt8(0)), reflect.TypeOf(decNamedUint16(0)), reflect.TypeOf(decNamedFloat32(0)), reflect.TypeOf(decNamedInt(0)),
	reflect.TypeOf(false), reflect.TypeOf(""), reflect.TypeOf(json.Number("")),
}

// decNumericValues are the BSON values of row I4: every width edge of every
// Go kind, as int32, int64 and double, with the doubles that are not whole,
// not finite or out of every range, and the non-numeric types a numeric
// decoder also reads or refuses.
func decNumericValues() [][]byte {
	var elements [][]byte
	integers := []int64{0, 1, -1, 127, 128, -128, -129, 255, 256, 32767, 32768, -32768, -32769, 65535, 65536,
		math.MaxInt32, math.MinInt32, math.MaxInt32 + 1, math.MinInt32 - 1, math.MaxUint32, math.MaxUint32 + 1,
		1 << 53, 1<<53 + 1, math.MaxInt64, math.MinInt64}
	for _, integer := range integers {
		if integer >= math.MinInt32 && integer <= math.MaxInt32 {
			elements = append(elements, elementOf(0x10, "v", int32Of(int32(integer))))
		}
		elements = append(elements, elementOf(0x12, "v", int64Bytes(integer)), elementOf(0x01, "v", doubleBytes(float64(integer))))
	}
	for _, double := range []float64{0.5, -0.5, 1.5, 0.1, math.Copysign(0, -1), 1 << 63, -(1 << 63), 1 << 64, 3.4e38, 3.5e38, 1e300,
		math.MaxFloat32, math.SmallestNonzeroFloat32, math.SmallestNonzeroFloat64, math.Inf(1), math.Inf(-1), math.NaN(), 16777217} {
		elements = append(elements, elementOf(0x01, "v", doubleBytes(double)))
	}
	elements = append(elements,
		elementOf(0x08, "v", []byte{1}), elementOf(0x08, "v", []byte{0}), elementOf(0x08, "v", []byte{2}),
		elementOf(0x0A, "v", nil), elementOf(0x06, "v", nil), elementOf(0x02, "v", stringValueOf("7")),
		elementOf(0x13, "v", make([]byte, 16)), elementOf(0x09, "v", int64Bytes(5)), elementOf(0x11, "v", int64Bytes(5)),
		elementOf(0x10, "v", []byte{1, 0}), elementOf(0x12, "v", []byte{1, 0, 0}), elementOf(0x01, "v", []byte{1}))
	return elements
}

func TestDecodeI4EveryNumericPair(t *testing.T) {
	pairs, refusals := 0, 0
	for _, goType := range decNumericTypes {
		for _, tag := range []reflect.StructTag{`bson:"v"`, `bson:"v,truncate"`} {
			structType := reflect.StructOf([]reflect.StructField{{Name: "V", Type: goType, Tag: tag}})
			mapType := reflect.MapOf(reflect.TypeOf(""), goType)
			sliceType := reflect.StructOf([]reflect.StructField{{Name: "V", Type: reflect.SliceOf(goType), Tag: tag}})
			targets := []decTarget{decReflectTarget(structType), decReflectTarget(mapType)}
			for _, element := range decNumericValues() {
				document := documentOf(element)
				for _, target := range targets {
					pairs++
					if vendorOutcome(func() error { return vendorbson.Unmarshal(document, target.vendor()) }) != "ok" {
						refusals++
					}
					if divergence := decDivergence(document, target); divergence != "" {
						t.Errorf("%s %s: % x: %s", goType, tag, document, divergence)
					}
				}
				// The same value as an array element, where the slice decoder
				// wraps the error with the position.
				arrayDocument := documentOf(elementOf(0x04, "v", documentOf(append([]byte{element[0], '0'}, element[2:]...))))
				if divergence := decDivergence(arrayDocument, decReflectTarget(sliceType)); divergence != "" {
					t.Errorf("[]%s %s: % x: %s", goType, tag, arrayDocument, divergence)
				}
			}
		}
	}
	inherited := decMarshal(t, vendorbson.D{{Key: "inner", Value: vendorbson.D{{Key: "n", Value: 2.5}}}})
	inheritedTarget := decSame("*decTruncatedInner", decZero[decTruncatedInner])
	decMustDecode(t, "I4 inherited truncate", inherited, inheritedTarget)
	decCheck(t, "I4 inherited truncate", inherited, inheritedTarget)
	if refusals == 0 || refusals == pairs {
		t.Fatalf("%d of %d pairs refused: the table must hold both outcomes", refusals, pairs)
	}
	t.Logf("%d (BSON value, Go kind) pairs, %d refused by the vendor", pairs, refusals)
}

// decReflectTarget is a fresh pointer to a zero goType, the same type on
// both sides.
func decReflectTarget(goType reflect.Type) decTarget {
	newTarget := func() any { return reflect.New(goType).Interface() }
	return decTarget{name: "*" + goType.String(), vendor: newTarget, library: newTarget}
}

type decTimes struct {
	T   time.Time
	P   *time.Time
	Any any
}

func TestDecodeT2TimeIsUTC(t *testing.T) {
	var values [][]byte
	for _, milliseconds := range []int64{0, 1, -1, 999, -999, 1001, -1001, 1793836800123, -62135596800000, -62135596800001,
		253402300799999, 253402300800000, math.MaxInt64, math.MinInt64, math.MaxInt64 / 1000, math.MinInt64 / 1000} {
		values = append(values, elementOf(0x09, "t", int64Bytes(milliseconds)), elementOf(0x12, "t", int64Bytes(milliseconds)))
	}
	for _, text := range []string{"2026-10-24T12:00:00Z", "2026-10-24T12:00:00.123Z", "2026-10-24T12:00:00.123456Z",
		"2026-10-24T12:00:00+02:00", "2026-10-24T12:00:00.5-07:30", "2026-10-24", "not a time", ""} {
		values = append(values, elementOf(0x02, "t", stringValueOf(text)))
	}
	values = append(values,
		elementOf(0x11, "t", int64Bytes(1)), elementOf(0x11, "t", append(int32Of(1), int32Of(1700000000)...)),
		elementOf(0x0A, "t", nil), elementOf(0x06, "t", nil), elementOf(0x10, "t", int32Of(5)),
		elementOf(0x01, "t", doubleBytes(1)), elementOf(0x08, "t", []byte{1}))
	targets := []decTarget{
		decSame("*struct{T time.Time}", decZero[struct{ T time.Time }]),
		decSame("*struct{T *time.Time}", decZero[struct{ T *time.Time }]),
		decSame("*map[string]time.Time", decZero[map[string]time.Time]),
		decSame("*struct{T []time.Time}", decZero[struct{ T []time.Time }]),
		decSame("*decTimes prefilled", func() decTimes {
			local := time.Date(2020, 1, 2, 3, 4, 5, 6, time.FixedZone("here", 3600))
			return decTimes{T: local, P: &local, Any: local}
		}),
		decTargetMap,
	}
	for _, timeElement := range values {
		document := documentOf(timeElement)
		decCheck(t, "T2", document, targets...)
		arrayDocument := documentOf(elementOf(0x04, "t", documentOf(append([]byte{timeElement[0], '0'}, timeElement[2:]...))))
		decCheck(t, "T2 array", arrayDocument, targets[3])
	}
	var decoded struct{ T time.Time }
	if err := decLibraryDecoder.Unmarshal(documentOf(elementOf(0x09, "t", int64Bytes(1793836800123))), &decoded); err != nil {
		t.Fatalf("library: %v", err)
	}
	if decoded.T.Location() != time.UTC {
		t.Errorf("a DateTime decoded into time.Time has location %v, want UTC", decoded.T.Location())
	}
}

type decNamedBytes []byte

func TestDecodeB1ByteSliceSources(t *testing.T) {
	var values [][]byte
	for _, subtype := range []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x80, 0xFF} {
		payload := []byte{1, 2, 3}
		if subtype == 0x02 {
			payload = append(int32Of(3), payload...)
		}
		values = append(values, binaryValueOf(subtype, payload), binaryValueOf(subtype, nil))
	}
	values = append(values, binaryValueOf(0x02, int32Of(0)), binaryValueOf(0x02, []byte{1, 2}))
	var elements [][]byte
	for _, binaryValue := range values {
		elements = append(elements, elementOf(0x05, "x", binaryValue))
	}
	elements = append(elements,
		elementOf(0x02, "x", stringValueOf("text")), elementOf(0x02, "x", stringValueOf("")),
		elementOf(0x0E, "x", stringValueOf("symbol")), elementOf(0x0A, "x", nil), elementOf(0x06, "x", nil),
		elementOf(0x10, "x", int32Of(1)), elementOf(0x04, "x", documentOf(elementOf(0x10, "0", int32Of(7)))),
		elementOf(0x03, "x", documentOf()), elementOf(0x07, "x", make([]byte, 12)))
	targets := []decTarget{
		decSame("*struct{X []byte}", decZero[struct{ X []byte }]),
		decSame("*struct{X decNamedBytes}", decZero[struct{ X decNamedBytes }]),
		decSame("*struct{X [3]byte}", decZero[struct{ X [3]byte }]),
		decSame("*struct{X [5]byte}", decZero[struct{ X [5]byte }]),
		decSame("*struct{X string}", decZero[struct{ X string }]),
		decSame("*struct{X *[]byte}", decZero[struct{ X *[]byte }]),
		decSame("*map[string][]byte", decZero[map[string][]byte]),
		decSame("*struct{X []byte} prefilled", func() struct{ X []byte } { return struct{ X []byte }{X: []byte{9, 9, 9, 9, 9}} }),
		decTarget{"*struct{X primitive.Binary}", func() any { return &struct{ X vendorprimitive.Binary }{} }, func() any { return &struct{ X primitive.Binary }{} }},
		decTarget{"*struct{X primitive.Symbol}", func() any { return &struct{ X vendorprimitive.Symbol }{} }, func() any { return &struct{ X primitive.Symbol }{} }},
		decTargetMap, decTargetD,
	}
	for _, element := range elements {
		decCheck(t, "B1", documentOf(element), targets...)
	}
	// The decoded slice is a copy: changing the input afterwards changes
	// nothing the library returned.
	input := documentOf(elementOf(0x05, "x", binaryValueOf(0x00, []byte{1, 2, 3})), elementOf(0x02, "s", stringValueOf("abc")))
	var copied struct {
		X []byte
		S []byte
	}
	if err := decLibraryDecoder.Unmarshal(input, &copied); err != nil {
		t.Fatalf("library: %v", err)
	}
	for position := range input {
		input[position] = 0xEE
	}
	if string(copied.X) != "\x01\x02\x03" || string(copied.S) != "abc" {
		t.Errorf("the decoded []byte changed with its input: %x %q", copied.X, copied.S)
	}
}

type decPointers struct {
	I   *int32
	S   *decPointee
	PP  **int64
	M   *map[string]any
	Any *any
}

type decPointee struct {
	A string
	B int32
}

func TestDecodeP1PointerAllocationAndNull(t *testing.T) {
	documents := [][]byte{
		decMarshal(t, vendorbson.D{{Key: "i", Value: int32(5)}, {Key: "s", Value: vendorbson.D{{Key: "a", Value: "x"}}}, {Key: "pp", Value: int64(7)},
			{Key: "m", Value: vendorbson.D{{Key: "k", Value: "v"}}}, {Key: "any", Value: vendorbson.D{{Key: "z", Value: int32(1)}}}}),
		decMarshal(t, vendorbson.D{{Key: "i", Value: nil}, {Key: "s", Value: nil}, {Key: "pp", Value: nil}, {Key: "m", Value: nil}, {Key: "any", Value: nil}}),
		documentOf(elementOf(0x06, "i", nil), elementOf(0x06, "s", nil), elementOf(0x06, "pp", nil), elementOf(0x06, "any", nil)),
		decMarshal(t, vendorbson.D{{Key: "s", Value: vendorbson.D{{Key: "b", Value: int32(2)}}}}),
		decMarshal(t, vendorbson.D{{Key: "i", Value: "not a number"}}),
	}
	prefilled := func() decPointers {
		number, wide := int32(1), int64(2)
		widePointer := &wide
		held := any("held")
		return decPointers{I: &number, S: &decPointee{A: "old", B: 9}, PP: &widePointer, M: &map[string]any{"old": true}, Any: &held}
	}
	targets := []decTarget{decSame("*decPointers", decZero[decPointers]), decSame("*decPointers prefilled", prefilled),
		decSame("**decPointee", decZero[*decPointee]), decSame("***decPointee", decZero[**decPointee])}
	for _, document := range documents {
		decCheck(t, "P1", document, targets...)
	}
	// A non-nil pointer is decoded through, not replaced, on both sides.
	vendorTarget, libraryTarget := prefilled(), prefilled()
	vendorPointee, libraryPointee := vendorTarget.S, libraryTarget.S
	if err := vendorbson.Unmarshal(documents[3], &vendorTarget); err != nil {
		t.Fatal(err)
	}
	if err := decLibraryDecoder.Unmarshal(documents[3], &libraryTarget); err != nil {
		t.Fatalf("library: %v", err)
	}
	if (vendorTarget.S == vendorPointee) != (libraryTarget.S == libraryPointee) {
		t.Errorf("pointer reuse: the vendor kept its pointee: %t, the library: %t", vendorTarget.S == vendorPointee, libraryTarget.S == libraryPointee)
	}
}

type decCaseKeys struct {
	Upper string `bson:"Upper"`
	Lower string `bson:"upper"`
	Name  string
	Mixed string `bson:"MiXed"`
}

func TestDecodeN4ExactThenLowercaseKey(t *testing.T) {
	target := decSame("*decCaseKeys", decZero[decCaseKeys])
	for _, key := range []string{"Upper", "upper", "UPPER", "uPPER", "name", "Name", "NAME", "MiXed", "mixed", "MIXED", "unknown", ""} {
		document := decMarshal(t, vendorbson.D{{Key: key, Value: "v"}})
		decMustDecode(t, "N4 "+key, document, target)
		decCheck(t, "N4 key "+key, document, target)
	}
	decCheck(t, "N4 every key", decMarshal(t, vendorbson.D{{Key: "UPPER", Value: "1"}, {Key: "Upper", Value: "2"}, {Key: "upper", Value: "3"}, {Key: "NAME", Value: "4"}}), target)
}

type decDeclaresA struct {
	A int32
}

func TestDecodeN5UnknownKeySkippedUnread(t *testing.T) {
	known := elementOf(0x10, "a", int32Of(1))
	malformedValues := [][]byte{
		elementOf(0x08, "zz", []byte{2}),
		elementOf(0x08, "zz", []byte{0xFF}),
		withByteAt(elementOf(0x02, "zz", stringValueOf("abc")), 11, 'x'),
		elementOf(0x02, "zz", append(int32Of(0), 0)[:4]),
		elementOf(0x03, "zz", []byte{9, 0, 0, 0, 0x08, 'b', 0, 7, 0}),
		elementOf(0x03, "zz", []byte{6, 0, 0, 0, 0xEE, 0}),
		elementOf(0x05, "zz", append(append(int32Of(5), 0x02), append(int32Of(100), 9)...)),
	}
	for _, malformed := range malformedValues {
		document := documentOf(known, malformed)
		declaringTarget := decSame("*decDeclaresA", decZero[decDeclaresA])
		decMustDecode(t, "N5", document, declaringTarget)
		decCheck(t, "N5 struct not declaring the key", document, declaringTarget)
		decCheck(t, "N5 other targets", document, decTargetD, decTargetMap, decTargetM, decTargetInterface)
		if _, err := bson.Validate(document, bson.Limits{Depth: 10}); err == nil {
			t.Errorf("% x: Validate accepts the malformed value the struct decoder skips (spec 6.2, O13 wants it refused)", document)
		}
	}
}

type decRetained struct {
	Kept    string
	Over    string
	Slice   []int32
	Array   [3]int32
	Map     map[string]int32
	Pointer *decPointee
	Any     any
	Doc     vendorbson.D
	Raw     vendorbson.Raw
}

type decLibRetained struct {
	Kept    string
	Over    string
	Slice   []int32
	Array   [3]int32
	Map     map[string]int32
	Pointer *decPointee
	Any     any
	Doc     bson.D
	Raw     bson.Raw
}

func TestDecodeR1NonEmptyTargets(t *testing.T) {
	document := decMarshal(t, vendorbson.D{
		{Key: "over", Value: "new"}, {Key: "slice", Value: vendorbson.A{int32(7)}}, {Key: "array", Value: vendorbson.A{int32(8)}},
		{Key: "map", Value: vendorbson.D{{Key: "b", Value: int32(2)}}}, {Key: "pointer", Value: vendorbson.D{{Key: "b", Value: int32(3)}}},
		{Key: "any", Value: vendorbson.D{{Key: "x", Value: "y"}}}, {Key: "doc", Value: vendorbson.D{{Key: "n", Value: "m"}}},
		{Key: "raw", Value: vendorbson.D{{Key: "r", Value: int32(1)}}},
	})
	vendorPrefill := func() any {
		return &decRetained{Kept: "kept", Over: "old", Slice: []int32{1, 2, 3}, Array: [3]int32{4, 5, 6}, Map: map[string]int32{"a": 1},
			Pointer: &decPointee{A: "old"}, Any: "old", Doc: vendorbson.D{{Key: "old", Value: 1}, {Key: "older", Value: 2}}, Raw: vendorbson.Raw{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17}}
	}
	libraryPrefill := func() any {
		return &decLibRetained{Kept: "kept", Over: "old", Slice: []int32{1, 2, 3}, Array: [3]int32{4, 5, 6}, Map: map[string]int32{"a": 1},
			Pointer: &decPointee{A: "old"}, Any: "old", Doc: bson.D{{Key: "old", Value: 1}, {Key: "older", Value: 2}}, Raw: bson.Raw{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17}}
	}
	prefilled := decTarget{"*decRetained prefilled", vendorPrefill, libraryPrefill}
	decMustDecode(t, "R1", document, prefilled)
	decCheck(t, "R1", document, prefilled,
		decSame("*map[string]interface{} prefilled", func() map[string]any { return map[string]any{"kept": 1, "over": "old"} }),
		decTarget{"*bson.M prefilled", func() any { return &vendorbson.M{"kept": 1} }, func() any { return &bson.M{"kept": 1} }},
		decTarget{"*bson.D prefilled", func() any {
			return &vendorbson.D{{Key: "a", Value: 1}, {Key: "b", Value: 2}, {Key: "c", Value: 3}, {Key: "d", Value: 4}, {Key: "e", Value: 5}, {Key: "f", Value: 6}, {Key: "g", Value: 7}, {Key: "h", Value: 8}, {Key: "i", Value: 9}}
		},
			func() any {
				return &bson.D{{Key: "a", Value: 1}, {Key: "b", Value: 2}, {Key: "c", Value: 3}, {Key: "d", Value: 4}, {Key: "e", Value: 5}, {Key: "f", Value: 6}, {Key: "g", Value: 7}, {Key: "h", Value: 8}, {Key: "i", Value: 9}}
			}},
		decTarget{"*bson.Raw prefilled", func() any { raw := vendorbson.Raw{9, 9}; return &raw }, func() any { raw := bson.Raw{9, 9}; return &raw }},
		decSame("*interface{} prefilled", func() any { return int32(4) }))
	// A slice is reset to length 0 and appended, so its backing array is
	// written through when it is large enough, on both sides.
	vendorTarget, libraryTarget := vendorPrefill().(*decRetained), libraryPrefill().(*decLibRetained)
	vendorBacking, libraryBacking := vendorTarget.Slice, libraryTarget.Slice
	if err := vendorbson.Unmarshal(document, vendorTarget); err != nil {
		t.Fatal(err)
	}
	if err := decLibraryDecoder.Unmarshal(document, libraryTarget); err != nil {
		t.Fatalf("library: %v", err)
	}
	if !reflect.DeepEqual(vendorBacking, libraryBacking) {
		t.Errorf("the prefilled slice's backing array: vendor %v, library %v", vendorBacking, libraryBacking)
	}
}

type decErrorPaths struct {
	A struct {
		B struct {
			C int32
		}
		L []struct{ N int8 }
		M map[string]int16
	}
	Any any
}

func TestDecodeE1ErrorTextCarriesKeyPath(t *testing.T) {
	cases := []vendorbson.D{
		{{Key: "a", Value: vendorbson.D{{Key: "b", Value: vendorbson.D{{Key: "c", Value: "x"}}}}}},
		{{Key: "a", Value: vendorbson.D{{Key: "l", Value: vendorbson.A{vendorbson.D{}, vendorbson.D{{Key: "n", Value: int32(300)}}}}}}},
		{{Key: "a", Value: vendorbson.D{{Key: "m", Value: vendorbson.D{{Key: "k", Value: int32(70000)}}}}}},
		{{Key: "a", Value: vendorbson.D{{Key: "b", Value: "not a document"}}}},
		{{Key: "a", Value: vendorbson.D{{Key: "B", Value: vendorbson.D{{Key: "C", Value: 1.5}}}}}},
	}
	target := decSame("*decErrorPaths", decZero[decErrorPaths])
	for _, errorCase := range cases {
		document := decMarshal(t, errorCase)
		decMustRefuse(t, "E1", document, target)
		decCheck(t, "E1", document, target)
	}
	// Errors under an interface{} holder, a map and bson.D: D's own elements
	// are not wrapped, the elements of a D inside an interface{} are.
	badBoolean := documentOf(elementOf(0x03, "outer", documentOf(elementOf(0x03, "inner", documentOf(elementOf(0x08, "flag", []byte{3}))))))
	badInArray := documentOf(elementOf(0x04, "list", documentOf(elementOf(0x10, "0", int32Of(1)), elementOf(0x08, "1", []byte{3}))))
	for _, document := range [][]byte{badBoolean, badInArray} {
		decMustRefuse(t, "E1 nested", document, decTargetMap)
		decCheck(t, "E1 nested", document, decTargetMap, decTargetM, decTargetD, decTargetInterface, decSame("*decErrorPaths", decZero[decErrorPaths]),
			decSame("*map[string]map[string]interface{}", decZero[map[string]map[string]any]))
	}
	var libraryTarget decErrorPaths
	err := decLibraryDecoder.Unmarshal(decMarshal(t, cases[1]), &libraryTarget)
	if err == nil || !strings.HasPrefix(err.Error(), "error decoding key a.l.1.n: ") {
		t.Errorf("the library's error is %v, want the key path a.l.1.n", err)
	}
}

// decURLs are the type codecs the contract table does not list (step 4c
// report section 11 item 3) but the vendor's registry has.
type decURLs struct {
	U   url.URL
	P   *url.URL
	N   json.Number
	Oid vendorprimitive.ObjectID
}

type decLibURLs struct {
	U   url.URL
	P   *url.URL
	N   json.Number
	Oid primitive.ObjectID
}

func TestDecodeTypeCodecsOutsideTheTable(t *testing.T) {
	target := decTarget{"*decURLs", func() any { return &decURLs{} }, func() any { return &decLibURLs{} }}
	for _, document := range [][]byte{
		decMarshal(t, vendorbson.D{{Key: "u", Value: "https://example.com/a?b=c#d"}, {Key: "p", Value: "rel/path"}, {Key: "n", Value: 1.25}, {Key: "oid", Value: "0123456789abcdef01234567"}}),
		decMarshal(t, vendorbson.D{{Key: "u", Value: "%zz"}}),
		decMarshal(t, vendorbson.D{{Key: "n", Value: int64(-9)}, {Key: "oid", Value: "twelve bytes"}}),
		decMarshal(t, vendorbson.D{{Key: "n", Value: int32(3)}, {Key: "oid", Value: "short"}}),
		decMarshal(t, vendorbson.D{{Key: "n", Value: "text"}}),
		decMarshal(t, vendorbson.D{{Key: "u", Value: nil}, {Key: "n", Value: nil}, {Key: "oid", Value: nil}}),
		decMarshal(t, vendorbson.D{{Key: "oid", Value: vendorprimitive.ObjectID{1, 2, 3}}}),
	} {
		decCheck(t, "type codecs", document, target)
	}
}
