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
	"strings"
	"testing"
	"time"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/oracle/compare"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// The Go-value half of step 5a: bson.MarshalExtJSON of Go values, against
// the vendor's MarshalExtJSON of the same (or twin) values, in all four
// modes (canonical or relaxed, escapeHTML or not). The text compares by
// compare.ExtJSON (spec 6.1's rule on text: map levels as sets, everything
// else byte for byte) and the error text exactly. Every encode case of
// steps 4d and 4f goes through here too, from requireSameEncoding.

// extJSONMode is one of MarshalExtJSON's four modes.
type extJSONMode struct {
	canonical  bool
	escapeHTML bool
}

var extJSONModes = []extJSONMode{{true, false}, {true, true}, {false, false}, {false, true}}

func (mode extJSONMode) String() string { return modeLabel(mode.canonical, mode.escapeHTML) }

// libraryMarshalExtJSON returns the library's text, or its error text, or
// the panic it raised as "panic: ...", and the error itself.
func libraryMarshalExtJSON(value any, mode extJSONMode) (text []byte, failure string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			text, failure, err = nil, fmt.Sprintf("panic: %v", recovered), nil
		}
	}()
	text, err = bson.MarshalExtJSON(value, mode.canonical, mode.escapeHTML)
	if err != nil {
		return nil, err.Error(), err
	}
	return text, "", nil
}

func vendorMarshalExtJSON(value any, mode extJSONMode) (text []byte, failure string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			text, failure = nil, fmt.Sprintf("panic: %v", recovered)
		}
	}()
	text, err := vendorbson.MarshalExtJSON(value, mode.canonical, mode.escapeHTML)
	if err != nil {
		return nil, err.Error()
	}
	return text, ""
}

// extJSONVendorPanics counts the cases where the vendor's MarshalExtJSON
// panicked and the library refused with an error, by the vendor's text.
var extJSONVendorPanics = map[string]int{}

// requireSameExtJSON requires, in every mode, the vendor's outcome: the
// same error text, or texts compare.ExtJSON finds equal. The library's
// ceiling refusal is right only for a value whose text nests past it, and
// where the vendor panics the library must return an error.
func requireSameExtJSON(t *testing.T, encodeInput encodeCase) {
	t.Helper()
	for _, mode := range extJSONModes {
		if divergence := extJSONWriteDivergence(encodeInput, mode); divergence != "" {
			t.Errorf("MarshalExtJSON, %s: %s", mode, divergence)
		}
	}
}

// extJSONWriteDivergence describes how the library's MarshalExtJSON of the
// case differs from the vendor's in mode, or returns "".
func extJSONWriteDivergence(encodeInput encodeCase, mode extJSONMode) string {
	libText, libFailure, libErr := libraryMarshalExtJSON(encodeInput.lib, mode)
	if strings.HasPrefix(libFailure, "panic: ") {
		return "the library panics: " + libFailure
	}
	vendorText, vendorFailure := vendorMarshalExtJSON(encodeInput.vendor, mode)
	switch {
	case errors.Is(libErr, bson.ErrNestingTooDeep) && vendorFailure == "":
		if depth := extJSONTextDepth(vendorText); depth <= bson.MaxNestingCeiling {
			return fmt.Sprintf("the library refuses a value %d levels deep at the ceiling: %s", depth, libFailure)
		}
		return ""
	case strings.HasPrefix(vendorFailure, "panic: "):
		// The vendor's slice expressions on a declared length of 0 to 3
		// (docs/core-api.md, "Step 5a core"), and its pointer codec's
		// cached nil encoder once a pointer to a type with no codec has
		// failed in the process (step 4d); any other panic is unruled.
		if !decIsKnownVendorPanic(vendorFailure) && vendorFailure != "panic: interface conversion: interface is nil, not bsoncodec.ValueEncoder" {
			return fmt.Sprintf("the vendor panics with a panic no ruled class names: %s (the library: %s)", vendorFailure, outcomeText(libFailure))
		}
		if libFailure == "" {
			return fmt.Sprintf("the vendor panics (%s), the library writes %s", vendorFailure, libText)
		}
		extJSONVendorPanics[vendorFailure]++
		return ""
	case vendorFailure != "" || libFailure != "":
		if vendorFailure != libFailure {
			return fmt.Sprintf("outcome differs:\n vendor:  %s\n library: %s\n library text: %s", outcomeText(vendorFailure), outcomeText(libFailure), libText)
		}
		return ""
	}
	recorder := &decRecorder{}
	compare.ExtJSON(recorder, encodeInput.vendor, vendorText, libText)
	return strings.Join(recorder.failures, "; ")
}

// extJSONWrapperKeys are the keys of the one-key objects the writer uses for
// a scalar. Such an object is not a level of the document.
var extJSONWrapperKeys = map[string]bool{
	"$numberInt": true, "$numberLong": true, "$numberDouble": true, "$numberDecimal": true,
	"$binary": true, "$date": true, "$oid": true, "$symbol": true, "$code": true,
	"$timestamp": true, "$regularExpression": true, "$dbPointer": true,
	"$undefined": true, "$minKey": true, "$maxKey": true,
}

// extJSONTextDepth is how many levels the text's document nests, the root
// counted as 1: each object and array, except a scalar's wrapper, and for a
// code with scope its scope. A text that is not JSON measures 0.
func extJSONTextDepth(text []byte) int {
	var tree any
	if err := json.Unmarshal(text, &tree); err != nil {
		return 0
	}
	return jsonTreeDepth(tree)
}

func jsonTreeDepth(node any) int {
	switch typed := node.(type) {
	case map[string]any:
		if scope, isScope := typed["$scope"]; isScope && len(typed) == 2 {
			if _, hasCode := typed["$code"]; hasCode {
				return jsonTreeDepth(scope)
			}
		}
		if len(typed) == 1 {
			for key := range typed {
				if extJSONWrapperKeys[key] {
					return 0
				}
			}
		}
		deepest := 0
		for _, member := range typed {
			deepest = max(deepest, jsonTreeDepth(member))
		}
		return deepest + 1
	case []any:
		deepest := 0
		for _, member := range typed {
			deepest = max(deepest, jsonTreeDepth(member))
		}
		return deepest + 1
	}
	return 0
}

// extJSONGoValueCases are the Go values the Extended JSON writer is held to
// beyond the encode cases of step 4: the places where encoding onto the text
// writer differs from encoding to BSON first (docs/core-api.md, "The seam
// for the Go-value wrapper"), the doubles, dates and strings the text
// spells, the top level, and the tree's wire shapes.
func extJSONGoValueCases(t *testing.T) []encodeCase {
	t.Helper()
	objectID := [12]byte{0x50, 0x7f, 0x1f, 0x77, 0xbc, 0xf8, 0x6c, 0xd7, 0x99, 0x43, 0x90, 0x11}
	validDocument := vendorbsoncore.NewDocumentBuilder().AppendString("s", "text").AppendInt32("n", 7).Build()
	nestedDocument := vendorbsoncore.NewDocumentBuilder().AppendDocument("d", validDocument).AppendArray("a",
		vendorbsoncore.NewArrayBuilder().AppendInt64(9).AppendString("x").Build()).Build()
	shortDocument := []byte{0x05, 0x00, 0x00}
	badElement := []byte{0x0c, 0x00, 0x00, 0x00, 0x02, 'k', 0x00, 0x05, 0x00, 0x00, 0x00, 0x00}
	endTooEarly := []byte{0x0a, 0x00, 0x00, 0x00, 0x00, 0x10, 'k', 0x00, 0x01, 0x00}
	declaredZero := []byte{0x00, 0x00, 0x00, 0x00, 0x00}
	array := vendorbsoncore.NewArrayBuilder().AppendInt32(1).AppendString("two").Build()
	record, libRecord := benchdata.Ref450Record(), benchdata.Ref450LibRecord()

	cases := []encodeCase{
		// A zero byte in a key or a regex: the BSON writer refuses it, the
		// text writer does not.
		sameValue("map key with a zero byte", map[string]int32{"a\x00b": 1}),
		twinValues("bson.M key with a zero byte", bson.M{"a\x00b": int32(1), "c": "d"}, vendorbson.M{"a\x00b": int32(1), "c": "d"}),
		twinValues("bson.D key with a zero byte", bson.D{{Key: "a\x00b", Value: int32(1)}}, vendorbson.D{{Key: "a\x00b", Value: int32(1)}}),
		sameValue("nested map key with a zero byte", map[string]any{"outer": map[string]any{"in\x00ner": true}}),
		sameValue("inline map key with a zero byte", extJSONInline{Name: "n", Extra: map[string]any{"z\x00": 1}}),
		twinValues("regex pattern with a zero byte",
			bson.D{{Key: "r", Value: primitive.Regex{Pattern: "a\x00b", Options: "i"}}},
			vendorbson.D{{Key: "r", Value: vendorprimitive.Regex{Pattern: "a\x00b", Options: "i"}}}),
		twinValues("regex options with a zero byte, unsorted",
			bson.D{{Key: "r", Value: primitive.Regex{Pattern: "p", Options: "x\x00mi"}}},
			vendorbson.D{{Key: "r", Value: vendorprimitive.Regex{Pattern: "p", Options: "x\x00mi"}}}),
		twinValues("regex options not UTF-8",
			bson.D{{Key: "r", Value: primitive.Regex{Pattern: "p", Options: "s\xffi"}}},
			vendorbson.D{{Key: "r", Value: vendorprimitive.Regex{Pattern: "p", Options: "s\xffi"}}}),
		// A code with scope's scope is encoded to BSON first by the vendor,
		// so a zero byte in a scope's key is refused there.
		twinValues("scope key with a zero byte",
			bson.D{{Key: "c", Value: primitive.CodeWithScope{Code: "f()", Scope: bson.D{{Key: "k\x00", Value: int32(1)}}}}},
			vendorbson.D{{Key: "c", Value: vendorprimitive.CodeWithScope{Code: "f()", Scope: vendorbson.D{{Key: "k\x00", Value: int32(1)}}}}}),
		twinValues("scope holding a regex with a zero byte",
			bson.D{{Key: "c", Value: primitive.CodeWithScope{Code: "f()", Scope: bson.D{{Key: "r", Value: primitive.Regex{Pattern: "\x00"}}}}}},
			vendorbson.D{{Key: "c", Value: vendorprimitive.CodeWithScope{Code: "f()", Scope: vendorbson.D{{Key: "r", Value: vendorprimitive.Regex{Pattern: "\x00"}}}}}}),
		twinValues("scope that is a map and a raw document",
			bson.D{{Key: "c", Value: primitive.CodeWithScope{Code: "<f>", Scope: map[string]any{"x": "<y>"}}}, {Key: "w", Value: primitive.CodeWithScope{Code: "g()", Scope: bson.Raw(validDocument)}}},
			vendorbson.D{{Key: "c", Value: vendorprimitive.CodeWithScope{Code: "<f>", Scope: map[string]any{"x": "<y>"}}}, {Key: "w", Value: vendorprimitive.CodeWithScope{Code: "g()", Scope: vendorbson.Raw(validDocument)}}}),
		twinValues("scope that is not a document",
			bson.D{{Key: "c", Value: primitive.CodeWithScope{Code: "f()", Scope: int32(1)}}},
			vendorbson.D{{Key: "c", Value: vendorprimitive.CodeWithScope{Code: "f()", Scope: int32(1)}}}),
		twinValues("nil scope",
			bson.D{{Key: "c", Value: primitive.CodeWithScope{Code: "f()"}}},
			vendorbson.D{{Key: "c", Value: vendorprimitive.CodeWithScope{Code: "f()"}}}),

		// Doubles: the text writer's formatting, and the three values relaxed
		// mode wraps.
		sameValue("doubles", map[string]float64{
			"one": 1, "half": 0.5, "negative zero": math.Copysign(0, -1), "nan": math.NaN(),
			"plus infinity": math.Inf(1), "minus infinity": math.Inf(-1), "large": 1e21, "exponent": 1.5e300,
			"small": 1e-7, "tiniest": 5e-324, "largest": math.MaxFloat64, "integral large": 123456789012345680,
		}),
		sameValue("float32", struct {
			F float32 `bson:"f"`
			G float32 `bson:"g"`
		}{F: 0.1, G: float32(math.Inf(-1))}),
		sameValue("doubles in a struct, in order", struct {
			A float64
			B float64
			C float64
		}{A: math.NaN(), B: 2, C: math.Copysign(0, -1)}),

		// Dates: relaxed mode writes a string only for 1970 to 9999.
		sameValue("dates", map[string]time.Time{
			"zero":       {},
			"before":     time.Date(1969, 12, 31, 23, 59, 59, 999_000_000, time.UTC),
			"epoch":      time.Unix(0, 0).UTC(),
			"local zone": time.Date(2026, 10, 24, 12, 30, 0, 123_456_789, time.FixedZone("x", 3600)),
			"last":       time.Date(9999, 12, 31, 23, 59, 59, 999_000_000, time.UTC),
			"after":      time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		}),
		twinValues("date times",
			bson.D{{Key: "min", Value: primitive.DateTime(math.MinInt64)}, {Key: "max", Value: primitive.DateTime(math.MaxInt64)}, {Key: "ms", Value: primitive.DateTime(1)}},
			vendorbson.D{{Key: "min", Value: vendorprimitive.DateTime(math.MinInt64)}, {Key: "max", Value: vendorprimitive.DateTime(math.MaxInt64)}, {Key: "ms", Value: vendorprimitive.DateTime(1)}}),

		// Integers: the encoder's widths, which the text spells differently.
		sameValue("integers", struct {
			Int    int    `bson:"int"`
			Wide   int    `bson:"wide"`
			Small  int8   `bson:"small"`
			Big    int64  `bson:"big"`
			Fits   uint32 `bson:"fits,minsize"`
			Huge   uint64 `bson:"huge"`
			Byte   uint8  `bson:"byte"`
			Signed int16  `bson:"signed"`
		}{Int: 1, Wide: math.MaxInt64, Small: -8, Big: 1, Fits: 7, Huge: math.MaxInt64, Byte: 255, Signed: -300}),
		sameValue("uint64 above int64", map[string]uint64{"too big": math.MaxUint64}),

		// The top level: a value that is not a document fails on the text
		// writer's mode check.
		sameValue("top-level int32", int32(1)),
		sameValue("top-level string", "text"),
		sameValue("top-level slice", []int{1, 2}),
		sameValue("top-level bytes", []byte{1}),
		sameValue("top-level nil", nil),
		sameValue("top-level nil map", map[string]int(nil)),
		sameValue("top-level nil pointer", (*extJSONInline)(nil)),
		sameValue("top-level pointer", &extJSONInline{Name: "p"}),
		sameValue("top-level time", time.Unix(1, 0)),
		twinValues("top-level nil bson.D", bson.D(nil), vendorbson.D(nil)),
		twinValues("top-level bson.A", bson.A{int32(1)}, vendorbson.A{int32(1)}),
		twinValues("top-level code with scope", primitive.CodeWithScope{Code: "f()", Scope: bson.D{}}, vendorprimitive.CodeWithScope{Code: "f()", Scope: vendorbson.D{}}),
		twinValues("top-level javascript", primitive.JavaScript("f()"), vendorprimitive.JavaScript("f()")),
		twinValues("top-level binary", primitive.Binary{Subtype: 4, Data: []byte{1}}, vendorprimitive.Binary{Subtype: 4, Data: []byte{1}}),

		// Byte-backed values are copied from their own bytes, so their error
		// offsets and checks start there.
		twinValues("top-level Raw", bson.Raw(nestedDocument), vendorbson.Raw(nestedDocument)),
		twinValues("nested Raw", bson.D{{Key: "raw", Value: bson.Raw(nestedDocument)}}, vendorbson.D{{Key: "raw", Value: vendorbson.Raw(nestedDocument)}}),
		twinValues("nested Raw, short", bson.D{{Key: "x", Value: int32(1)}, {Key: "raw", Value: bson.Raw(shortDocument)}}, vendorbson.D{{Key: "x", Value: int32(1)}, {Key: "raw", Value: vendorbson.Raw(shortDocument)}}),
		twinValues("nested Raw, bad element", bson.D{{Key: "raw", Value: bson.Raw(badElement)}}, vendorbson.D{{Key: "raw", Value: vendorbson.Raw(badElement)}}),
		twinValues("nested Raw, ends too early", bson.D{{Key: "raw", Value: bson.Raw(endTooEarly)}}, vendorbson.D{{Key: "raw", Value: vendorbson.Raw(endTooEarly)}}),
		twinValues("nested Raw, declared length 0", bson.D{{Key: "raw", Value: bson.Raw(declaredZero)}}, vendorbson.D{{Key: "raw", Value: vendorbson.Raw(declaredZero)}}),
		twinValues("nested nil Raw", bson.D{{Key: "raw", Value: bson.Raw(nil)}}, vendorbson.D{{Key: "raw", Value: vendorbson.Raw(nil)}}),
		twinValues("nested core document", bson.D{{Key: "doc", Value: bsoncore.Document(nestedDocument)}}, vendorbson.D{{Key: "doc", Value: vendorbsoncore.Document(nestedDocument)}}),
		twinValues("nested core array", bson.D{{Key: "arr", Value: bsoncore.Array(array)}}, vendorbson.D{{Key: "arr", Value: vendorbsoncore.Array(array)}}),
		twinValues("nested core array, bad element", bson.D{{Key: "arr", Value: bsoncore.Array(badElement)}}, vendorbson.D{{Key: "arr", Value: vendorbsoncore.Array(badElement)}}),
		twinValues("top-level core array", bsoncore.Array(array), vendorbsoncore.Array(array)),
		twinValues("nested raw values", extJSONRawValues(objectID, validDocument), extJSONVendorRawValues(objectID, validDocument)),
		twinValues("nested raw value, invalid type", bson.D{{Key: "v", Value: bson.RawValue{Type: 0x20, Value: []byte{1}}}}, vendorbson.D{{Key: "v", Value: vendorbson.RawValue{Type: 0x20, Value: []byte{1}}}}),
		twinValues("nested raw value, string too short", bson.D{{Key: "v", Value: bson.RawValue{Type: bson.TypeString, Value: []byte{9, 0, 0, 0, 'a', 0}}}}, vendorbson.D{{Key: "v", Value: vendorbson.RawValue{Type: vendorbson.TypeString, Value: []byte{9, 0, 0, 0, 'a', 0}}}}),
		twinValues("nested raw value, document end early", bson.D{{Key: "v", Value: bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: endTooEarly}}}, vendorbson.D{{Key: "v", Value: vendorbson.RawValue{Type: vendorbson.TypeEmbeddedDocument, Value: endTooEarly}}}),
		twinValues("nested raw value, boolean 2", bson.D{{Key: "v", Value: bson.RawValue{Type: bson.TypeBoolean, Value: []byte{2}}}}, vendorbson.D{{Key: "v", Value: vendorbson.RawValue{Type: vendorbson.TypeBoolean, Value: []byte{2}}}}),
		twinValues("top-level raw value, a document", bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: validDocument}, vendorbson.RawValue{Type: vendorbson.TypeEmbeddedDocument, Value: validDocument}),
		twinValues("top-level raw value, a string", bson.RawValue{Type: bson.TypeString, Value: appendTestString(nil, "s")}, vendorbson.RawValue{Type: vendorbson.TypeString, Value: appendTestString(nil, "s")}),
		twinValues("top-level raw value, a short string", bson.RawValue{Type: bson.TypeString, Value: []byte{1}}, vendorbson.RawValue{Type: vendorbson.TypeString, Value: []byte{1}}),
		twinValues("top-level raw value, an array", bson.RawValue{Type: bson.TypeArray, Value: array}, vendorbson.RawValue{Type: vendorbson.TypeArray, Value: array}),
		twinValues("top-level raw value, an array without a length", bson.RawValue{Type: bson.TypeArray, Value: []byte{1}}, vendorbson.RawValue{Type: vendorbson.TypeArray, Value: []byte{1}}),
		twinValues("top-level raw value, a null", bson.RawValue{Type: bson.TypeNull}, vendorbson.RawValue{Type: vendorbson.TypeNull}),
		twinValues("top-level raw value, a code with scope", bson.RawValue{Type: bson.TypeCodeWithScope, Value: []byte{1, 2, 3}}, vendorbson.RawValue{Type: vendorbson.TypeCodeWithScope, Value: []byte{1, 2, 3}}),
		twinValues("top-level raw value, type 0", bson.RawValue{}, vendorbson.RawValue{}),
		twinValues("top-level raw value, an unknown type", bson.RawValue{Type: 0x30, Value: []byte{1}}, vendorbson.RawValue{Type: 0x30, Value: []byte{1}}),

		// Hooks: MarshalBSONValue's and a nested MarshalBSON's bytes are read
		// with a fresh value reader, a top-level MarshalBSON's as a root.
		sameValue("top-level MarshalBSON", valueHookDocument{Number: 3}),
		sameValue("top-level MarshalBSON, malformed", hookBytes(badElement)),
		sameValue("top-level MarshalBSON, short", hookBytes(shortDocument)),
		sameValue("top-level MarshalBSON, declared length 0", hookBytes(declaredZero)),
		sameValue("nested MarshalBSON", map[string]any{"h": valueHookDocument{Number: 3}}),
		sameValue("nested MarshalBSON, malformed", map[string]any{"h": hookBytes(badElement)}),
		sameValue("nested MarshalBSON, ends too early", map[string]any{"h": hookBytes(endTooEarly)}),
		sameValue("nested MarshalBSON, short", map[string]any{"h": hookBytes(shortDocument)}),
		sameValue("nested MarshalBSON, empty", map[string]any{"h": hookBytes(nil)}),
		twinValues("nested MarshalBSONValue", map[string]any{"v": libValueHook{Text: "<v>"}}, map[string]any{"v": vendorValueHook{Text: "<v>"}}),
		twinValues("nested MarshalBSONValue, type 0", map[string]any{"v": libGarbageValueHook{}}, map[string]any{"v": vendorGarbageValueHook{}}),
		twinValues("top-level MarshalBSONValue", libValueHook{Text: "t"}, vendorValueHook{Text: "t"}),
		twinValues("top-level MarshalBSONValue, a document", libDocumentValueHook{}, vendorDocumentValueHook{}),
		sameValue("ProxyBSON to a map with a zero-byte key", map[string]any{"p": proxyValue{Returns: map[string]int32{"k\x00": 1}}}),
		sameValue("top-level ProxyBSON to a map", proxyValue{Returns: map[string]int32{"k": 1}}),
		sameValue("top-level ProxyBSON to a MarshalBSON", proxyValue{Returns: valueHookDocument{Number: 4}}),
		sameValue("top-level ProxyBSON to a string", proxyValue{Returns: "s"}),

		// Interface values of every type the decoder can produce.
		twinValues("every BSON type", extJSONEveryType(objectID), extJSONVendorEveryType(objectID)),

		// The tree's wire shapes.
		twinValues("Ref450 record", libRecord, record),
		twinValues("Ref450 record pointer", &libRecord, &record),
		sameValue("insert request shape", decInsertRequest{Database: "app", Data: map[string]any{"name": "Ada", "age": int32(36), "<tag>": []any{"&"}}, WriteConcern: "majority"}),
		sameValue("catalog entry shape", decCatalogEntry{}),
	}
	for _, probe := range escapeProbes() {
		cases = append(cases, twinValues(fmt.Sprintf("string %q", probe),
			map[string]any{"s": probe, probe: "key", "j": primitive.JavaScript(probe), "sym": primitive.Symbol(probe)},
			map[string]any{"s": probe, probe: "key", "j": vendorprimitive.JavaScript(probe), "sym": vendorprimitive.Symbol(probe)}))
	}
	return cases
}

// extJSONInline has an inline map after its fields.
type extJSONInline struct {
	Name  string         `bson:"name"`
	Extra map[string]any `bson:",inline"`
}

type libDocumentValueHook struct{}

func (libDocumentValueHook) MarshalBSONValue() (bsontype.Type, []byte, error) {
	return bson.TypeEmbeddedDocument, vendorbsoncore.NewDocumentBuilder().AppendString("k", "v").Build(), nil
}

type vendorDocumentValueHook struct{}

func (vendorDocumentValueHook) MarshalBSONValue() (vendorbsontype.Type, []byte, error) {
	return vendorbson.TypeEmbeddedDocument, vendorbsoncore.NewDocumentBuilder().AppendString("k", "v").Build(), nil
}

// extJSONRawValues is a bson.D of a RawValue of each scalar type and a
// document.
func extJSONRawValues(objectID [12]byte, document []byte) bson.D {
	var values bson.D
	for _, element := range extJSONRawValueElements(objectID, document) {
		values = append(values, bson.E{Key: element.key, Value: bson.RawValue{Type: bsontype.Type(element.valueType), Value: element.valueBytes}})
	}
	return values
}

func extJSONVendorRawValues(objectID [12]byte, document []byte) vendorbson.D {
	var values vendorbson.D
	for _, element := range extJSONRawValueElements(objectID, document) {
		values = append(values, vendorbson.E{Key: element.key, Value: vendorbson.RawValue{Type: vendorbsontype.Type(element.valueType), Value: element.valueBytes}})
	}
	return values
}

type extJSONRawElement struct {
	key        string
	valueType  byte
	valueBytes []byte
}

func extJSONRawValueElements(objectID [12]byte, document []byte) []extJSONRawElement {
	return []extJSONRawElement{
		{"double", 0x01, []byte{0, 0, 0, 0, 0, 0, 0xf0, 0x3f}},
		{"string", 0x02, appendTestString(nil, "<s>")},
		{"document", 0x03, document},
		{"array", 0x04, document},
		{"binary", 0x05, []byte{2, 0, 0, 0, 0x80, 1, 2}},
		{"undefined", 0x06, nil},
		{"object id", 0x07, objectID[:]},
		{"boolean", 0x08, []byte{1}},
		{"date", 0x09, []byte{1, 0, 0, 0, 0, 0, 0, 0}},
		{"null", 0x0A, nil},
		{"regex", 0x0B, []byte("a\x00xi\x00")},
		{"db pointer", 0x0C, append(appendTestString(nil, "db.c"), objectID[:]...)},
		{"javascript", 0x0D, appendTestString(nil, "f()")},
		{"symbol", 0x0E, appendTestString(nil, "sym")},
		{"code with scope", 0x0F, codeWithScopeOf("f()", document)},
		{"int32", 0x10, []byte{7, 0, 0, 0}},
		{"timestamp", 0x11, []byte{1, 0, 0, 0, 2, 0, 0, 0}},
		{"int64", 0x12, []byte{7, 0, 0, 0, 0, 0, 0, 0}},
		{"decimal", 0x13, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x40, 0x30}},
		{"min key", 0xFF, nil},
		{"max key", 0x7F, nil},
	}
}

func extJSONEveryType(objectID [12]byte) bson.D {
	decimal, _ := primitive.ParseDecimal128("1.50")
	return bson.D{
		{Key: "binary", Value: primitive.Binary{Subtype: 0x80, Data: []byte{1, 2}}},
		{Key: "old binary", Value: primitive.Binary{Subtype: 0x02, Data: []byte{3}}},
		{Key: "uuid", Value: primitive.Binary{Subtype: 0x04, Data: make([]byte, 16)}},
		{Key: "oid", Value: primitive.ObjectID(objectID)},
		{Key: "decimal", Value: decimal},
		{Key: "timestamp", Value: primitive.Timestamp{T: math.MaxUint32, I: 1}},
		{Key: "js", Value: primitive.JavaScript("<x>")},
		{Key: "symbol", Value: primitive.Symbol("&")},
		{Key: "pointer", Value: primitive.DBPointer{DB: `a"b`, Pointer: primitive.ObjectID(objectID)}},
		{Key: "min", Value: primitive.MinKey{}},
		{Key: "max", Value: primitive.MaxKey{}},
		{Key: "undefined", Value: primitive.Undefined{}},
		{Key: "null", Value: primitive.Null{}},
		{Key: "nil", Value: nil},
		{Key: "array", Value: bson.A{int32(1), "two", bson.D{{Key: "three", Value: 3.0}}, nil}},
		{Key: "bytes", Value: []byte("bytes")},
		{Key: "empty bytes", Value: []byte{}},
		{Key: "nil bytes", Value: []byte(nil)},
		{Key: "bool", Value: true},
	}
}

func extJSONVendorEveryType(objectID [12]byte) vendorbson.D {
	decimal, _ := vendorprimitive.ParseDecimal128("1.50")
	return vendorbson.D{
		{Key: "binary", Value: vendorprimitive.Binary{Subtype: 0x80, Data: []byte{1, 2}}},
		{Key: "old binary", Value: vendorprimitive.Binary{Subtype: 0x02, Data: []byte{3}}},
		{Key: "uuid", Value: vendorprimitive.Binary{Subtype: 0x04, Data: make([]byte, 16)}},
		{Key: "oid", Value: vendorprimitive.ObjectID(objectID)},
		{Key: "decimal", Value: decimal},
		{Key: "timestamp", Value: vendorprimitive.Timestamp{T: math.MaxUint32, I: 1}},
		{Key: "js", Value: vendorprimitive.JavaScript("<x>")},
		{Key: "symbol", Value: vendorprimitive.Symbol("&")},
		{Key: "pointer", Value: vendorprimitive.DBPointer{DB: `a"b`, Pointer: vendorprimitive.ObjectID(objectID)}},
		{Key: "min", Value: vendorprimitive.MinKey{}},
		{Key: "max", Value: vendorprimitive.MaxKey{}},
		{Key: "undefined", Value: vendorprimitive.Undefined{}},
		{Key: "null", Value: vendorprimitive.Null{}},
		{Key: "nil", Value: nil},
		{Key: "array", Value: vendorbson.A{int32(1), "two", vendorbson.D{{Key: "three", Value: 3.0}}, nil}},
		{Key: "bytes", Value: []byte("bytes")},
		{Key: "empty bytes", Value: []byte{}},
		{Key: "nil bytes", Value: []byte(nil)},
		{Key: "bool", Value: true},
	}
}

// TestMarshalExtJSONMatchesVendorOnGoValues holds bson.MarshalExtJSON to
// the vendor on the cases above, in all four modes.
func TestMarshalExtJSONMatchesVendorOnGoValues(t *testing.T) {
	for _, encodeInput := range extJSONGoValueCases(t) {
		t.Run(encodeInput.name, func(t *testing.T) {
			requireSameExtJSON(t, encodeInput)
		})
	}
	if len(extJSONVendorPanics) > 0 {
		t.Logf("vendor panics the library refused with an error: %v", extJSONVendorPanics)
	}
}

// TestMarshalExtJSONMapsCompareByElementSet is spec 6.1's rule on text: the
// vendor writes a map's entries in Go's random order, the library in
// ascending byte order of the keys (O11), and the two compare as sets at map
// levels and in order everywhere else. The vendor is run until it has
// written two different orders, so the comparison really meets a
// reordering, and the library's text is the same on every run.
func TestMarshalExtJSONMapsCompareByElementSet(t *testing.T) {
	wide := map[string]any{}
	for position := range 12 {
		wide[fmt.Sprintf("key%02d", position)] = map[string]any{"b": int32(position), "a": []any{map[string]int32{"y": 1, "x": 2}}}
	}
	inline := extJSONInline{Name: "first", Extra: map[string]any{"z": 1, "y": 2, "x": 3, "w": 4}}
	for _, encodeInput := range []encodeCase{sameValue("nested maps", wide), sameValue("inline map", inline),
		twinValues("bson.M inside bson.D", bson.D{{Key: "z", Value: bson.M{"q": int32(1), "p": int32(2), "o": int32(3)}}, {Key: "a", Value: int32(0)}},
			vendorbson.D{{Key: "z", Value: vendorbson.M{"q": int32(1), "p": int32(2), "o": int32(3)}}, {Key: "a", Value: int32(0)}})} {
		t.Run(encodeInput.name, func(t *testing.T) {
			for _, mode := range extJSONModes {
				vendorOrders := map[string]bool{}
				for attempt := 0; attempt < 200 && len(vendorOrders) < 2; attempt++ {
					text, failure := vendorMarshalExtJSON(encodeInput.vendor, mode)
					if failure != "" {
						t.Fatalf("%s: the vendor refuses: %s", mode, failure)
					}
					vendorOrders[string(text)] = true
				}
				if len(vendorOrders) < 2 {
					t.Fatalf("%s: the vendor wrote one order 200 times; the set comparison is not exercised", mode)
				}
				libraryTexts := map[string]bool{}
				for range 20 {
					libText, libFailure, _ := libraryMarshalExtJSON(encodeInput.lib, mode)
					if libFailure != "" {
						t.Fatalf("%s: the library refuses: %s", mode, libFailure)
					}
					libraryTexts[string(libText)] = true
					for vendorText := range vendorOrders {
						compare.ExtJSON(t, encodeInput.vendor, []byte(vendorText), libText)
					}
				}
				if len(libraryTexts) != 1 {
					t.Errorf("%s: the library wrote %d different texts for one value", mode, len(libraryTexts))
				}
			}
		})
	}
}

// TestMarshalExtJSONGoValuesRefusePastTheCeiling holds MarshalExtJSON of a
// Go value to the ceiling (spec 5.2): at 200 levels the vendor's text, at
// 201 an error matching ErrNestingTooDeep, through every container, and
// through bytes copied from a Raw, which the text writer opens level by
// level.
func TestMarshalExtJSONGoValuesRefusePastTheCeiling(t *testing.T) {
	for _, through := range []string{"bson.D", "map", "array", "struct", "scope"} {
		t.Run(through, func(t *testing.T) {
			lib, vendor := nestedValues(bson.MaxNestingCeiling, through)
			requireSameExtJSON(t, twinValues("200 levels", lib, vendor))
			lib, vendor = nestedValues(bson.MaxNestingCeiling+1, through)
			for _, mode := range extJSONModes {
				if _, failure := vendorMarshalExtJSON(vendor, mode); failure != "" {
					t.Fatalf("%s: the vendor refuses 201 levels: %s", mode, failure)
				}
				_, _, err := libraryMarshalExtJSON(lib, mode)
				requireNestingRefusal(t, mode.String()+": MarshalExtJSON of 201 levels", err)
			}
		})
	}
	t.Run("a Raw below a map", func(t *testing.T) {
		atCeiling := nestedBytes(bson.MaxNestingCeiling-1, "document")
		requireSameExtJSON(t, twinValues("199 levels in a Raw under the root", bson.D{{Key: "raw", Value: bson.Raw(atCeiling)}}, vendorbson.D{{Key: "raw", Value: vendorbson.Raw(atCeiling)}}))
		pastCeiling := nestedBytes(bson.MaxNestingCeiling, "array")
		for _, mode := range extJSONModes {
			_, _, err := libraryMarshalExtJSON(map[string]any{"raw": bson.Raw(pastCeiling)}, mode)
			requireNestingRefusal(t, mode.String()+": 200 levels in a Raw under the root", err)
		}
	})
	t.Run("a cycle", func(t *testing.T) {
		cycle := map[string]any{}
		cycle["self"] = cycle
		_, _, err := libraryMarshalExtJSON(cycle, extJSONModes[0])
		requireNestingRefusal(t, "a map that holds itself", err)
	})
}
