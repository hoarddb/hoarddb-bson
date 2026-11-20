// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/oracle/compare"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
)

// Step 4f: the public entry points, bson.Marshal, bson.MarshalValue,
// bson.Unmarshal, RawValue.Unmarshal and Document.Decode, against the
// vendor in the same process. The encode and decode harnesses
// (encode_harness_test.go, decode_harness_test.go) call the entry points
// too, so every contract-row test of steps 4d and 4e runs through them.

// entryRecord is a struct of builtins both libraries decode into.
type entryRecord struct {
	Name  string         `bson:"name"`
	Count int32          `bson:"count"`
	Tags  []string       `bson:"tags"`
	Inner map[string]any `bson:"inner"`
	When  time.Time      `bson:"when"`
	Rest  any            `bson:"rest"`
}

// entryTargets are the targets the build doc names for Unmarshal: Raw,
// RawValue, D, M, interface{} and structs.
func entryTargets() []decTarget {
	return []decTarget{
		decTargetRaw, decTargetRawValue, decTargetD, decTargetM, decTargetInterface, decTargetMap,
		decSame("struct of builtins", decZero[entryRecord]),
		decSame("struct of builtins, prefilled", func() entryRecord {
			return entryRecord{Name: "kept", Count: 9, Tags: []string{"x", "y", "z"}, Inner: map[string]any{"old": 1}}
		}),
		{"Ref450 struct", func() any { return &benchdata.Record{} }, func() any { return &benchdata.LibRecord{} }},
	}
}

// entryDocuments are encoded documents of every element type, nested, and
// framings the decoder must refuse.
func entryDocuments(t *testing.T) map[string][]byte {
	t.Helper()
	objectID := vendorprimitive.ObjectID{0x65, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	decimal, err := vendorprimitive.ParseDecimal128("12.5E-3")
	if err != nil {
		t.Fatal(err)
	}
	documents := map[string][]byte{
		"Ref450": decRef450(t),
		"record": decMarshal(t, vendorbson.D{
			{Key: "name", Value: "n"}, {Key: "count", Value: int32(3)}, {Key: "tags", Value: vendorbson.A{"a", "b"}},
			{Key: "inner", Value: vendorbson.D{{Key: "deep", Value: vendorbson.D{{Key: "x", Value: int64(1)}}}, {Key: "list", Value: vendorbson.A{vendorbson.D{}, vendorbson.A{}}}}},
			{Key: "when", Value: vendorprimitive.DateTime(1_700_000_000_123)}, {Key: "rest", Value: vendorbson.D{{Key: "k", Value: true}}},
		}),
		"every type": decMarshal(t, vendorbson.D{
			{Key: "double", Value: 1.5}, {Key: "string", Value: "s"}, {Key: "document", Value: vendorbson.D{{Key: "a", Value: int32(1)}}},
			{Key: "array", Value: vendorbson.A{int32(1), "two"}}, {Key: "binary", Value: vendorprimitive.Binary{Subtype: 4, Data: []byte{1, 2}}},
			{Key: "undefined", Value: vendorprimitive.Undefined{}}, {Key: "objectid", Value: objectID}, {Key: "bool", Value: true},
			{Key: "datetime", Value: vendorprimitive.DateTime(-1)}, {Key: "null", Value: nil}, {Key: "regex", Value: vendorprimitive.Regex{Pattern: "a+", Options: "i"}},
			{Key: "dbpointer", Value: vendorprimitive.DBPointer{DB: "db.c", Pointer: objectID}}, {Key: "javascript", Value: vendorprimitive.JavaScript("f()")},
			{Key: "symbol", Value: vendorprimitive.Symbol("sym")}, {Key: "codewithscope", Value: vendorprimitive.CodeWithScope{Code: "g()", Scope: vendorbson.D{{Key: "v", Value: int32(2)}}}},
			{Key: "int32", Value: int32(-7)}, {Key: "timestamp", Value: vendorprimitive.Timestamp{T: 1, I: 2}}, {Key: "int64", Value: int64(1) << 40},
			{Key: "decimal", Value: decimal}, {Key: "minkey", Value: vendorprimitive.MinKey{}}, {Key: "maxkey", Value: vendorprimitive.MaxKey{}},
		}),
		"empty":          documentOf(),
		"repeated key":   documentOf(elementOf(0x10, "a", int32Of(1)), elementOf(0x10, "a", int32Of(2))),
		"boolean byte 2": documentOf(elementOf(0x08, "flag", []byte{2})),
	}
	for position, framing := range framingSeeds() {
		documents[fmt.Sprintf("framing %d", position)] = framing
	}
	return documents
}

// TestEntryPointsMatchVendor is the build doc's step 4f table row: Marshal,
// Unmarshal into the named targets, MarshalValue (type and bytes), and
// RawValue.Unmarshal, each against the vendor.
func TestEntryPointsMatchVendor(t *testing.T) {
	t.Run("Marshal", func(t *testing.T) {
		libRecord, vendorRecord := benchdata.Ref450LibRecord(), benchdata.Ref450Record()
		requireEncodesLikeVendor(t,
			twinValues("Ref450 struct", libRecord, vendorRecord),
			twinValues("Ref450 struct pointer", &libRecord, &vendorRecord),
			twinValues("Ref450 bson.D", libraryTwin(t, benchdata.Ref450D()), benchdata.Ref450D()),
			sameValue("map", map[string]any{"b": int32(1), "a": []any{"x", map[string]any{"z": nil, "y": 2.5}}}),
			sameValue("struct of builtins", entryRecord{Name: "n", Tags: []string{}, Inner: map[string]any{"k": "v"}, When: time.UnixMilli(5).UTC()}),
			twinValues("top-level Raw", bson.Raw(decRef450(t)), vendorbson.Raw(decRef450(t))),
			sameValue("top-level MarshalBSON", valueHookDocument{Number: 4}),
			// Spec Z1 at the top level (step 4d report, section 8 item 2): the
			// vendor refuses a nil with its TransitionError text, except a nil
			// map, which becomes an empty document. A nil Raw and a zero
			// RawValue are errors.
			sameValue("nil map", map[string]any(nil)),
			sameValue("nil pointer", (*entryRecord)(nil)),
			sameValue("nil slice", []string(nil)),
			sameValue("nil []byte", []byte(nil)),
			twinValues("nil bson.D", bson.D(nil), vendorbson.D(nil)),
			twinValues("nil Raw", bson.Raw(nil), vendorbson.Raw(nil)),
			twinValues("zero RawValue", bson.RawValue{}, vendorbson.RawValue{}),
			sameValue("untyped nil", nil),
			sameValue("a string", "not a document"),
		)
		requireVendorOutcome(t, map[string]any(nil), "")
		requireVendorOutcome(t, []string(nil), "WriteNull can only write while positioned on a Element or Value but is positioned on a TopLevel")
	})

	t.Run("Unmarshal", func(t *testing.T) {
		for name, documentBytes := range entryDocuments(t) {
			decCheck(t, name, documentBytes, entryTargets()...)
		}
		decMustDecode(t, "Ref450", decRef450(t), entryTargets()[len(entryTargets())-1])
	})

	t.Run("MarshalValue", func(t *testing.T) {
		objectID := primitive.ObjectID{0x65, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
		for _, valueCase := range []encodeCase{
			sameValue("int32", int32(7)),
			sameValue("int64", int64(-1)),
			sameValue("int that fits int32", 5),
			sameValue("int past int32", 1<<40),
			sameValue("uint8", uint8(200)),
			sameValue("uint64 past int64", uint64(1)<<63),
			sameValue("float64", 2.5),
			sameValue("string", "text"),
			sameValue("string with a zero byte", "a\x00b"),
			sameValue("bool", true),
			sameValue("untyped nil", nil),
			sameValue("nil pointer", (*int)(nil)),
			sameValue("nil map", map[string]int(nil)),
			sameValue("nil slice", []int(nil)),
			sameValue("nil []byte", []byte(nil)),
			sameValue("empty []byte", []byte{}),
			sameValue("[]byte", []byte{1, 2, 3}),
			sameValue("one-key map", map[string]int{"a": 1}),
			sameValue("[]string", []string{"a", "b"}),
			sameValue("struct", entryRecord{Name: "n"}),
			sameValue("struct pointer", &entryRecord{Name: "p"}),
			sameValue("time.Time", time.Date(2026, 10, 24, 1, 2, 3, 999_999_999, time.UTC)),
			sameValue("func has no encoder", func() {}),
			twinValues("bson.D", primitive.D{{Key: "a", Value: int32(1)}, {Key: "b", Value: primitive.A{"x"}}}, vendorprimitive.D{{Key: "a", Value: int32(1)}, {Key: "b", Value: vendorprimitive.A{"x"}}}),
			twinValues("bson.A", primitive.A{int32(1), "two"}, vendorprimitive.A{int32(1), "two"}),
			twinValues("ObjectID", objectID, vendorprimitive.ObjectID(objectID)),
			twinValues("DateTime", primitive.DateTime(12), vendorprimitive.DateTime(12)),
			twinValues("Decimal128", primitive.NewDecimal128(1, 2), vendorprimitive.NewDecimal128(1, 2)),
			twinValues("Raw", bson.Raw(documentOf(elementOf(0x10, "a", int32Of(1)))), vendorbson.Raw(documentOf(elementOf(0x10, "a", int32Of(1))))),
			twinValues("RawValue", bson.RawValue{Type: bsontype.Int32, Value: int32Of(3)}, vendorbson.RawValue{Type: vendorbson.TypeInt32, Value: int32Of(3)}),
			twinValues("zero RawValue", bson.RawValue{}, vendorbson.RawValue{}),
			sameValue("MarshalBSON", valueHookDocument{Number: 2}),
			sameValue("MarshalBSON returning malformed bytes", hookBytes{9, 0, 0, 0, 0x10, 'a', 0, 1, 0}),
			sameValue("MarshalBSON returning an error", failingHook{}),
			twinValues("MarshalBSONValue", libValueHook{Text: "v"}, vendorValueHook{Text: "v"}),
			twinValues("MarshalBSONValue writing type 0", libGarbageValueHook{}, vendorGarbageValueHook{}),
			twinValues("both hooks: MarshalBSON wins", libBothHooks{Number: 6}, vendorBothHooks{Number: 6}),
		} {
			t.Run(valueCase.name, func(t *testing.T) { requireSameMarshalValue(t, valueCase) })
		}
	})

	t.Run("RawValue.Unmarshal", func(t *testing.T) {
		nested := decMarshal(t, vendorbson.D{{Key: "a", Value: vendorbson.D{{Key: "b", Value: vendorbson.A{int32(1), vendorbson.D{}}}}}})
		values := []struct {
			name       string
			valueType  byte
			valueBytes []byte
		}{
			{"document", 0x03, nested},
			{"array", 0x04, documentOf(elementOf(0x03, "0", nested), elementOf(0x02, "1", stringValueOf("s")))},
			{"int32", 0x10, int32Of(-4)},
			{"string", 0x02, stringValueOf("text")},
			{"double", 0x01, []byte{0, 0, 0, 0, 0, 0, 0xf8, 0x3f}},
			{"null", 0x0A, nil},
			{"binary subtype 0", 0x05, binaryValueOf(0, []byte{7})},
			{"short int32", 0x10, []byte{1, 2}},
			{"type 0", 0x00, nil},
		}
		targets := []decTarget{
			decTargetInterface, decTargetMap, decTargetD, decTargetM, decTargetRaw, decTargetRawValue,
			decSame("int64", decZero[int64]), decSame("string", decZero[string]), decSame("[]byte", decZero[[]byte]),
			decSame("struct of builtins", decZero[entryRecord]),
		}
		for _, value := range values {
			for _, target := range targets {
				if divergence := decValueDivergence(value.valueType, value.valueBytes, target); divergence != "" {
					t.Errorf("%s: %s", value.name, divergence)
				}
			}
		}
		// Spec D6: into interface{} a document is primitive.D, into a map a
		// map[string]interface{}.
		var intoInterface any
		if err := (bson.RawValue{Type: bsontype.EmbeddedDocument, Value: nested}).Unmarshal(&intoInterface); err != nil {
			t.Fatal(err)
		}
		if _, isD := intoInterface.(primitive.D); !isD {
			t.Errorf("RawValue.Unmarshal into interface{} gave %T, want primitive.D", intoInterface)
		}
		var intoMap map[string]any
		if err := (bson.RawValue{Type: bsontype.EmbeddedDocument, Value: nested}).Unmarshal(&intoMap); err != nil {
			t.Fatal(err)
		}
		if _, isMap := intoMap["a"].(map[string]any); !isMap {
			t.Errorf("RawValue.Unmarshal into a map gave %T inside, want map[string]interface {}", intoMap["a"])
		}
	})

	t.Run("RawValue.Unmarshal into a nil pointer", func(t *testing.T) {
		// Ruling O17: the vendor panics, the library returns Unmarshal's text
		// for the same mistake.
		vendorText := vendorOutcome(func() error {
			return vendorbson.RawValue{Type: vendorbson.TypeInt32, Value: int32Of(1)}.Unmarshal((*int32)(nil))
		})
		if vendorText != "panic: reflect: call of reflect.Value.Type on zero Value" {
			t.Fatalf("the vendor no longer panics on a nil pointer: %s", vendorText)
		}
		err := bson.RawValue{Type: bsontype.Int32, Value: int32Of(1)}.Unmarshal((*int32)(nil))
		if err == nil || err.Error() != "cannot Decode to nil value" {
			t.Errorf("RawValue.Unmarshal into a nil pointer: %v, want cannot Decode to nil value", err)
		}
	})
}

// requireSameMarshalValue requires both MarshalValues to fail with the same
// text, or to give the same type and value bytes (a document compared with
// compare.Encoded, so a map level is a set).
func requireSameMarshalValue(t *testing.T, valueCase encodeCase) {
	t.Helper()
	vendorType, vendorBytes, vendorFailure := vendorMarshalValue(valueCase.vendor)
	libType, libBytes, libFailure := libraryMarshalValue(valueCase.lib)
	switch {
	case strings.HasPrefix(libFailure, bson.ErrNestingTooDeep.Error()) && vendorFailure == "":
		if depth := readableValueDepth(byte(vendorType), vendorBytes); depth <= bson.MaxNestingCeiling {
			t.Errorf("the library refuses a value %d levels deep at the ceiling: %s", depth, libFailure)
		}
	case vendorFailure != "" || libFailure != "":
		if vendorFailure != libFailure {
			t.Errorf("outcome differs:\n vendor:  %s\n library: %s", outcomeText(vendorFailure), outcomeText(libFailure))
		}
	case byte(vendorType) != byte(libType):
		t.Errorf("type %s (vendor) vs %s (library)", vendorType, libType)
	case vendorType == vendorbson.TypeEmbeddedDocument && vendorbson.Unmarshal(vendorBytes, &vendorbson.D{}) == nil:
		compare.Encoded(t, valueCase.vendor, vendorBytes, libBytes)
	case !bytes.Equal(vendorBytes, libBytes):
		t.Errorf("value bytes differ:\n vendor:  % x\n library: % x", vendorBytes, libBytes)
	}
}

func libraryMarshalValue(value any) (valueType bsontype.Type, valueBytes []byte, failure string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			valueType, valueBytes, failure = 0, nil, fmt.Sprintf("panic: %v", recovered)
		}
	}()
	valueType, valueBytes, err := bson.MarshalValue(value)
	if err != nil {
		return 0, nil, err.Error()
	}
	return valueType, valueBytes, ""
}

func vendorMarshalValue(value any) (valueType vendorbsontype.Type, valueBytes []byte, failure string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			valueType, valueBytes, failure = 0, nil, fmt.Sprintf("panic: %v", recovered)
		}
	}()
	valueType, valueBytes, err := vendorbson.MarshalValue(value)
	if err != nil {
		return 0, nil, err.Error()
	}
	return valueType, valueBytes, ""
}

// entryNode is a recursive struct, nested by its pointer field.
type entryNode struct {
	Next *entryNode `bson:"next,omitempty"`
}

// nestedValues returns twin Go values that encode to a document levels deep,
// the root counted as 1, nested through the named container: bson.D, a map,
// an array (a []interface{} below the root), a recursive struct, or a
// code-with-scope's scope.
func nestedValues(levels int, through string) (lib, vendor any) {
	switch through {
	case "bson.D":
		lib, vendor = primitive.D{}, vendorprimitive.D{}
		for range levels - 1 {
			lib, vendor = primitive.D{{Key: "a", Value: lib}}, vendorprimitive.D{{Key: "a", Value: vendor}}
		}
		return lib, vendor
	case "map":
		var value any = map[string]any{}
		for range levels - 1 {
			value = map[string]any{"a": value}
		}
		return value, value
	case "array":
		var value any = []any{}
		for range levels - 2 {
			value = []any{value}
		}
		return map[string]any{"a": value}, map[string]any{"a": value}
	case "struct":
		node := &entryNode{}
		for range levels - 1 {
			node = &entryNode{Next: node}
		}
		return node, node
	case "scope":
		lib, vendor = primitive.D{}, vendorprimitive.D{}
		for range levels - 1 {
			lib = primitive.D{{Key: "c", Value: primitive.CodeWithScope{Code: "f()", Scope: lib}}}
			vendor = vendorprimitive.D{{Key: "c", Value: vendorprimitive.CodeWithScope{Code: "f()", Scope: vendor}}}
		}
		return lib, vendor
	}
	panic("nestedValues: unknown container " + through)
}

// nestedBytes is a document levels deep, the root counted as 1, nested
// through embedded documents, arrays or code-with-scope scopes.
func nestedBytes(levels int, through string) []byte {
	if through == "scope" {
		current := documentOf()
		for range levels - 1 {
			current = documentOf(elementOf(0x0F, "c", codeWithScopeOf("f()", current)))
		}
		return current
	}
	return nestedDocument(levels, through == "array")
}

// requireNestingRefusal fails t unless err matches ErrNestingTooDeep.
func requireNestingRefusal(t *testing.T, label string, err error) {
	t.Helper()
	if !errors.Is(err, bson.ErrNestingTooDeep) {
		t.Errorf("%s: %v, want an error matching bson.ErrNestingTooDeep", label, err)
	}
}

// TestEntryPointsRefuseDeeperThanTheCeiling holds the entry points that
// recurse to MaxNestingCeiling (spec 5.2, call C2): at 200 levels each does
// exactly what the vendor does; at 201 each refuses with an error matching
// ErrNestingTooDeep where the vendor goes on. The root is level 1, and a
// document, an array and a code-with-scope's scope each add one.
func TestEntryPointsRefuseDeeperThanTheCeiling(t *testing.T) {
	if bson.MaxNestingCeiling != 200 {
		t.Fatalf("MaxNestingCeiling is %d, the spec's is 200", bson.MaxNestingCeiling)
	}
	for _, through := range []string{"bson.D", "map", "array", "struct", "scope"} {
		t.Run("Marshal through "+through, func(t *testing.T) {
			lib, vendor := nestedValues(bson.MaxNestingCeiling, through)
			requireSameEncoding(t, twinValues("200 levels", lib, vendor))
			lib, vendor = nestedValues(bson.MaxNestingCeiling+1, through)
			requireVendorOutcome(t, vendor, "")
			_, err := bson.Marshal(lib)
			requireNestingRefusal(t, "Marshal of 201 levels", err)
		})
	}
	for _, through := range []string{"bson.D", "array", "scope"} {
		t.Run("MarshalValue through "+through, func(t *testing.T) {
			// A value's own document is level 1, as RawValue counts it.
			lib, vendor := nestedValues(bson.MaxNestingCeiling, through)
			requireSameMarshalValue(t, twinValues("200 levels", lib, vendor))
			lib, vendor = nestedValues(bson.MaxNestingCeiling+1, through)
			if _, _, failure := vendorMarshalValue(vendor); failure != "" {
				t.Fatalf("the vendor refuses 201 levels: %s", failure)
			}
			_, _, err := bson.MarshalValue(lib)
			requireNestingRefusal(t, "MarshalValue of 201 levels", err)
		})
	}
	descending := []decTarget{decTargetMap, decTargetInterface, decTargetD, decTargetM, decTargetNilD}
	for _, through := range []string{"document", "array", "scope"} {
		t.Run("Unmarshal through "+through, func(t *testing.T) {
			atCeiling, pastCeiling := nestedBytes(bson.MaxNestingCeiling, through), nestedBytes(bson.MaxNestingCeiling+1, through)
			for _, target := range descending {
				decMustDecode(t, "200 levels", atCeiling, target)
				decCheck(t, "200 levels", atCeiling, target)
				decMustDecode(t, "201 levels", pastCeiling, target)
				requireNestingRefusal(t, "Unmarshal of 201 levels into "+target.name, bson.Unmarshal(pastCeiling, target.library()))
			}
		})
		t.Run("RawValue.Unmarshal through "+through, func(t *testing.T) {
			// The value's document is level 1: 200 levels below the RawValue.
			atCeiling, pastCeiling := nestedBytes(bson.MaxNestingCeiling, through), nestedBytes(bson.MaxNestingCeiling+1, through)
			for _, target := range descending[:4] {
				if divergence := decValueDivergence(0x03, atCeiling, target); divergence != "" {
					t.Errorf("200 levels: %s", divergence)
				}
				requireNestingRefusal(t, "RawValue.Unmarshal of 201 levels into "+target.name,
					bson.RawValue{Type: bsontype.EmbeddedDocument, Value: pastCeiling}.Unmarshal(target.library()))
			}
		})
		t.Run("Document.Decode through "+through, func(t *testing.T) {
			// TrustStored does not measure, so a stored document deeper than
			// the ceiling reaches Decode, which refuses it as Unmarshal does.
			document, err := bson.TrustStored(nestedBytes(bson.MaxNestingCeiling+1, through))
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range descending {
				requireNestingRefusal(t, "Document.Decode of 201 levels into "+target.name, document.Decode(target.library()))
			}
		})
	}
	t.Run("the harness measures deep inputs as the decoder reads them", func(t *testing.T) {
		// FuzzDecodeDifferential's first run met the vectors' 1,000-level
		// array, which the library refuses; the harness must count it as
		// past the ceiling, not as the 132 levels a bsoncore walk reaches.
		for _, through := range []string{"document", "array", "scope"} {
			thousand := nestedBytes(1000, through)
			if depth := readableDepth(thousand, 1); depth <= bson.MaxNestingCeiling {
				t.Errorf("%s: 1,000 levels measure as %d", through, depth)
			}
			decCheck(t, "1,000 levels through "+through, thousand, descending...)
		}
		// The verifier's FuzzDecodeDifferential input 790ba12f68645090
		// (committed as a seed): 1,000 levels under "a", level 74's declared
		// length negative (f5 1c 00 fa). The decoder descends past it, so
		// the walk must too.
		negativeAt74 := nestedBytes(1000, "document")
		copy(negativeAt74[7*73:], []byte{0xf5, 0x1c, 0x00, 0xfa})
		if depth := readableDepth(negativeAt74, 1); depth <= bson.MaxNestingCeiling {
			t.Errorf("1,000 levels with a negative length at level 74 measure as %d", depth)
		}
		decCheck(t, "1,000 levels, a negative length at level 74", negativeAt74, descending...)
		if err := bson.Unmarshal(negativeAt74, &map[string]any{}); !errors.Is(err, bson.ErrNestingTooDeep) {
			t.Errorf("1,000 levels, a negative length at level 74: %v, want the ceiling", err)
		}
		if depth := readableDepth(nestedBytes(bson.MaxNestingCeiling, "array"), 1); depth != bson.MaxNestingCeiling {
			t.Errorf("200 levels measure as %d", depth)
		}
	})
	t.Run("a level-201 header the vendor cannot read is the vendor's error", func(t *testing.T) {
		// The ceiling is checked once the container's length is read, so
		// bytes that end inside the 201st level's length give the vendor's
		// error. RawValue.Unmarshal's input ends exactly there.
		var cutShort []byte
		for range bson.MaxNestingCeiling {
			cutShort = append(cutShort, int32Of(1000)...)
			cutShort = append(cutShort, 0x03, 'a', 0)
		}
		cutShort = append(cutShort, 7, 0)
		for _, target := range descending[:4] {
			if divergence := decValueDivergence(0x03, cutShort, target); divergence != "" {
				t.Errorf("%s", divergence)
			}
			if err := (bson.RawValue{Type: bsontype.EmbeddedDocument, Value: cutShort}).Unmarshal(target.library()); err == nil || errors.Is(err, bson.ErrNestingTooDeep) {
				t.Errorf("into %s: %v, want the vendor's read error", target.name, err)
			}
		}
		// On encode a zero byte in the key of the 201st level is refused
		// as the vendor refuses it: the header comes before the level.
		lib, vendor := primitive.D{{Key: "bad\x00key", Value: primitive.D{}}}, vendorprimitive.D{{Key: "bad\x00key", Value: vendorprimitive.D{}}}
		for range bson.MaxNestingCeiling - 1 {
			lib, vendor = primitive.D{{Key: "a", Value: lib}}, vendorprimitive.D{{Key: "a", Value: vendor}}
		}
		requireVendorOutcome(t, vendor, "BSON element key cannot contain null bytes")
		requireSameEncoding(t, twinValues("zero-byte key opening level 201", lib, vendor))
	})
	t.Run("sibling levels do not add up", func(t *testing.T) {
		// 500 empty documents side by side are 2 levels, not 501.
		var libSiblings, vendorSiblings []any
		for range 500 {
			libSiblings, vendorSiblings = append(libSiblings, primitive.D{}), append(vendorSiblings, vendorprimitive.D{})
		}
		requireSameEncoding(t, twinValues("500 sibling documents", map[string]any{"s": libSiblings}, map[string]any{"s": vendorSiblings}))
		requireSameMarshalValue(t, twinValues("500 sibling documents", libSiblings, vendorSiblings))
		decCheck(t, "500 sibling documents", decMarshal(t, map[string]any{"s": vendorSiblings}), descending...)
	})
	t.Run("Unmarshal opens no level it does not decode", func(t *testing.T) {
		// The ceiling bounds the decoder's recursion. A target that copies
		// the document's bytes (Raw, RawValue) or skips the deep key (a
		// struct that does not declare it, spec row N5) opens no level, so it
		// accepts 201 levels, as the vendor does.
		pastCeiling := nestedBytes(bson.MaxNestingCeiling+1, "document")
		decCheck(t, "201 levels", pastCeiling, decTargetRaw, decTargetRawValue, decTargetCoreDoc, decSame("struct without the key", decZero[struct{ B int32 }]))
	})
	t.Run("an error the vendor meets first stays the vendor's", func(t *testing.T) {
		// A malformed boolean at level 100, read before the deep key: the
		// vendor's error, not the ceiling's.
		deepPart := nestedBytes(bson.MaxNestingCeiling-98, "document")
		malformedFirst := documentOf(elementOf(0x08, "flag", []byte{7}), elementOf(0x03, "a", deepPart))
		for range 98 {
			malformedFirst = documentOf(elementOf(0x03, "a", malformedFirst))
		}
		decMustRefuse(t, "malformed before the ceiling", malformedFirst, decTargetMap)
		decCheck(t, "malformed before the ceiling", malformedFirst, descending...)
		// A key with a zero byte at level 100 of a 201-level value.
		lib, vendor := nestedValues(bson.MaxNestingCeiling-99, "bson.D")
		lib, vendor = primitive.D{{Key: "bad\x00key", Value: int32(1)}, {Key: "a", Value: lib}}, vendorprimitive.D{{Key: "bad\x00key", Value: int32(1)}, {Key: "a", Value: vendor}}
		for range 99 {
			lib, vendor = primitive.D{{Key: "a", Value: lib}}, vendorprimitive.D{{Key: "a", Value: vendor}}
		}
		requireSameEncoding(t, twinValues("zero-byte key before the ceiling", lib, vendor))
	})
}

// entryChildEnvironment names the vendor call TestEntryVendorChild makes in
// a re-executed test binary.
const entryChildEnvironment = "HOARDDB_ENTRY_VENDOR_CHILD"

// entryChildStack is the child's stack limit: low, so the vendor's
// recursion on a cycle dies in milliseconds instead of at the 1 GB default.
const entryChildStack = 16 << 20

// TestEntryVendorChild is the child half of TestMarshalRefusesACycle. It
// does nothing in a normal run.
func TestEntryVendorChild(t *testing.T) {
	switch os.Getenv(entryChildEnvironment) {
	case "":
		t.Skip("run by TestMarshalRefusesACycle in a child process")
	case "cycle":
		debug.SetMaxStack(entryChildStack)
		cycle := &entryNode{}
		cycle.Next = cycle
		_, err := vendorbson.Marshal(cycle)
		fmt.Println("VENDOR RETURNED:", err)
	default:
		t.Fatalf("unknown child mode %q", os.Getenv(entryChildEnvironment))
	}
}

// TestMarshalRefusesACycle pins ruling O17 for a value that contains
// itself: the vendor recurses until the goroutine stack overflows, a fatal
// error no recover catches; the library stops at the ceiling with an error.
func TestMarshalRefusesACycle(t *testing.T) {
	child := exec.Command(os.Args[0], "-test.run=^TestEntryVendorChild$", "-test.count=1")
	child.Env = append(os.Environ(), entryChildEnvironment+"=cycle")
	output, err := child.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "goroutine stack exceeds") {
		t.Fatalf("the vendor's encode of a cycle no longer overflows the stack (%v):\n%s", err, output)
	}
	cycle := &entryNode{}
	cycle.Next = cycle
	_, err = bson.Marshal(cycle)
	requireNestingRefusal(t, "Marshal of a pointer cycle", err)
	selfMap := map[string]any{}
	selfMap["self"] = selfMap
	_, err = bson.Marshal(selfMap)
	requireNestingRefusal(t, "Marshal of a map that holds itself", err)
	_, _, err = bson.MarshalValue(selfMap)
	requireNestingRefusal(t, "MarshalValue of a map that holds itself", err)
}

// TestDocumentDecodeSkipsValidation holds Document.Decode to Unmarshal on
// valid input, shows that it does not validate again, and that it refuses
// the zero Document.
func TestDocumentDecodeSkipsValidation(t *testing.T) {
	for name, documentBytes := range entryDocuments(t) {
		document, err := bson.Validate(documentBytes, bson.Limits{Depth: bson.MaxNestingCeiling})
		if err != nil {
			continue
		}
		for _, target := range entryTargets() {
			decodedTarget, unmarshalledTarget := target.library(), target.library()
			decodeErr, unmarshalErr := document.Decode(decodedTarget), bson.Unmarshal(documentBytes, unmarshalledTarget)
			if fmt.Sprint(decodeErr) != fmt.Sprint(unmarshalErr) {
				t.Errorf("%s into %s: Decode gave %v, Unmarshal %v", name, target.name, decodeErr, unmarshalErr)
				continue
			}
			recorder := &decRecorder{}
			compare.Decoded(recorder, unmarshalledTarget, decodedTarget)
			if len(recorder.failures) > 0 {
				t.Errorf("%s into %s: %s", name, target.name, strings.Join(recorder.failures, "; "))
			}
		}
	}
	// A stored document with a boolean byte of 2 under a key the struct
	// does not declare: Validate refuses it, and Decode, which does not
	// validate, skips the key unread, as Unmarshal does (spec row N5).
	malformed := documentOf(elementOf(0x10, "b", int32Of(3)), elementOf(0x08, "undeclared", []byte{2}))
	if _, err := bson.Validate(malformed, bson.Limits{Depth: 2}); err == nil {
		t.Fatal("Validate accepts a boolean byte of 2")
	}
	stored, err := bson.TrustStored(malformed)
	if err != nil {
		t.Fatal(err)
	}
	var declared struct{ B int32 }
	if err := stored.Decode(&declared); err != nil || declared.B != 3 {
		t.Errorf("Decode of a stored document validated again: %v, B=%d", err, declared.B)
	}
	var zero bson.Document
	if err := zero.Decode(&declared); !errors.Is(err, bson.ErrZeroDocument) {
		t.Errorf("Decode of the zero Document: %v, want an error matching ErrZeroDocument", err)
	}
}

// TestEntryPointsRoundTrip marshals values with each library and decodes
// the bytes back with the same library, into the value's own type and into
// the document targets; the decoded values must agree.
func TestEntryPointsRoundTrip(t *testing.T) {
	libRecord, vendorRecord := benchdata.Ref450LibRecord(), benchdata.Ref450Record()
	values := []struct {
		name        string
		lib, vendor any
		own         decTarget
	}{
		{"Ref450 struct", libRecord, vendorRecord, decTarget{"Ref450 struct", func() any { return &benchdata.Record{} }, func() any { return &benchdata.LibRecord{} }}},
		{"struct of builtins", entryRecord{Name: "r", Count: -2, Tags: []string{"t"}, Inner: map[string]any{"a": int64(1)}, When: time.UnixMilli(77).UTC(), Rest: "x"},
			entryRecord{Name: "r", Count: -2, Tags: []string{"t"}, Inner: map[string]any{"a": int64(1)}, When: time.UnixMilli(77).UTC(), Rest: "x"},
			decSame("struct of builtins", decZero[entryRecord])},
		{"Ref450 bson.D", libraryTwin(t, benchdata.Ref450D()), benchdata.Ref450D(), decTargetD},
	}
	for _, value := range values {
		libBytes, err := bson.Marshal(value.lib)
		if err != nil {
			t.Fatalf("%s: %v", value.name, err)
		}
		vendorBytes, err := vendorbson.Marshal(value.vendor)
		if err != nil {
			t.Fatalf("%s: %v", value.name, err)
		}
		for _, target := range append([]decTarget{value.own}, decTargetMap, decTargetInterface, decTargetD, decTargetM, decTargetRaw) {
			vendorTarget, libraryTarget := target.vendor(), target.library()
			vendorErr, libraryErr := vendorbson.Unmarshal(vendorBytes, vendorTarget), bson.Unmarshal(libBytes, libraryTarget)
			if fmt.Sprint(vendorErr) != fmt.Sprint(libraryErr) {
				t.Errorf("%s into %s: vendor %v, library %v", value.name, target.name, vendorErr, libraryErr)
				continue
			}
			if divergence := decValueDifference(value.name+" into "+target.name, vendorTarget, libraryTarget); divergence != "" {
				t.Error(divergence)
			}
		}
	}
}

// Types first seen by TestEntryPointsConcurrently, so their plans compile
// while the goroutines race to use them.
type (
	entryConcurrentLeaf struct {
		Label string            `bson:"label"`
		Score float64           `bson:"score"`
		Under *entryConcurrentL `bson:"under,omitempty"`
	}
	entryConcurrentL struct {
		Leaves []entryConcurrentLeaf `bson:"leaves"`
		Index  map[string]int32      `bson:"index"`
	}
)

// TestEntryPointsConcurrently runs Marshal, MarshalValue, Unmarshal and
// RawValue.Unmarshal from 32 goroutines at once, starting on types no plan
// has been compiled for, under -race in the gates: every result must equal
// the vendor's.
func TestEntryPointsConcurrently(t *testing.T) {
	tree := entryConcurrentL{
		Leaves: []entryConcurrentLeaf{{Label: "a", Score: 1, Under: &entryConcurrentL{Index: map[string]int32{"x": 1}}}, {Label: "b"}},
		Index:  map[string]int32{"only": 2},
	}
	vendorBytes, err := vendorbson.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	vendorValueType, vendorValueBytes, err := vendorbson.MarshalValue(tree.Leaves)
	if err != nil {
		t.Fatal(err)
	}
	var vendorDecoded entryConcurrentL
	if err := vendorbson.Unmarshal(vendorBytes, &vendorDecoded); err != nil {
		t.Fatal(err)
	}
	const goroutines = 32
	var group sync.WaitGroup
	failures := make(chan string, goroutines)
	for range goroutines {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 50 {
				libBytes, err := bson.Marshal(tree)
				if err != nil || !bytes.Equal(libBytes, vendorBytes) {
					failures <- fmt.Sprintf("Marshal: %v, % x", err, libBytes)
					return
				}
				valueType, valueBytes, err := bson.MarshalValue(tree.Leaves)
				if err != nil || byte(valueType) != byte(vendorValueType) || !bytes.Equal(valueBytes, vendorValueBytes) {
					failures <- fmt.Sprintf("MarshalValue: %v, %s, % x", err, valueType, valueBytes)
					return
				}
				var decoded entryConcurrentL
				if err := bson.Unmarshal(libBytes, &decoded); err != nil || !reflect.DeepEqual(decoded, vendorDecoded) {
					failures <- fmt.Sprintf("Unmarshal: %v, %+v", err, decoded)
					return
				}
				var leaves []entryConcurrentLeaf
				if err := (bson.RawValue{Type: valueType, Value: valueBytes}).Unmarshal(&leaves); err != nil || !reflect.DeepEqual(leaves, vendorDecoded.Leaves) {
					failures <- fmt.Sprintf("RawValue.Unmarshal: %v, %+v", err, leaves)
					return
				}
			}
		}()
	}
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}

// readableDepth is how deep documentBytes nests as far as it can be read,
// the document itself being level, and stops counting one past the
// ceiling. It reads as the decoder's reader descends, element after
// element in order, a container's declared length not checked until its
// terminator, so that a fuzzed input's depth can be told without a
// decoder; the first byte it cannot read ends the walk.
func readableDepth(documentBytes []byte, level int) int {
	walk := depthWalk{input: documentBytes}
	walk.document(4, level)
	return walk.deepest
}

// readableValueDepth is readableDepth for one value, whose document, array
// or code-with-scope scope is level 1; a scalar is level 0.
func readableValueDepth(valueType byte, valueBytes []byte) int {
	walk := depthWalk{input: valueBytes}
	walk.value(0, valueType, 1)
	return walk.deepest
}

// depthWalk is readableDepth's reader.
type depthWalk struct {
	input   []byte
	deepest int
}

// document reads the elements of the document whose elements start at
// position, which is level, and returns the position after its terminator,
// or -1 where the bytes run out.
func (walk *depthWalk) document(position, level int) int {
	walk.deepest = max(walk.deepest, level)
	if level > bson.MaxNestingCeiling || position > len(walk.input) {
		return -1
	}
	for position >= 0 && position < len(walk.input) {
		elementType := walk.input[position]
		if elementType == 0 {
			return position + 1
		}
		keyEnd := bytes.IndexByte(walk.input[position+1:], 0)
		if keyEnd < 0 {
			return -1
		}
		position = walk.value(position+1+keyEnd+1, elementType, level+1)
	}
	return -1
}

// value reads the value of valueType at position, whose document, array or
// scope would be level, and returns the position after it, or -1.
func (walk *depthWalk) value(position int, valueType byte, level int) int {
	lengthAt := func(at int) int {
		if at < 0 || at+4 > len(walk.input) {
			return -1
		}
		return int(int32(binary.LittleEndian.Uint32(walk.input[at:])))
	}
	skip := func(length int) int {
		if length < 0 || position+length > len(walk.input) {
			return -1
		}
		return position + length
	}
	switch valueType {
	case 0x03, 0x04:
		// The reader reads a container's length on descent and checks it
		// only at the terminator, so a negative or wrong length does not
		// stop the descent; only missing length bytes do.
		if position < 0 || position+4 > len(walk.input) {
			return -1
		}
		return walk.document(position+4, level)
	case 0x0F:
		// The reader's checks before it opens the scope
		// (reader.go:ReadCodeWithScope): the total and code lengths are
		// there, the code length is positive and its bytes are there, the
		// scope's length is there, and the total equals the parts.
		if position < 0 || position+8 > len(walk.input) {
			return -1
		}
		totalLength, codeLength := lengthAt(position), lengthAt(position+4)
		scopeAt := position + 8 + codeLength
		if codeLength <= 0 || scopeAt+4 > len(walk.input) {
			return -1
		}
		if scopeLength := lengthAt(scopeAt); totalLength != 4+codeLength+4+scopeLength {
			return -1
		}
		return walk.document(scopeAt+4, level)
	case 0x01, 0x09, 0x11, 0x12:
		return skip(8)
	case 0x10:
		return skip(4)
	case 0x08:
		return skip(1)
	case 0x07:
		return skip(12)
	case 0x13:
		return skip(16)
	case 0x06, 0x0A, 0x7F, 0xFF:
		return position
	case 0x02, 0x0D, 0x0E:
		return skip(4 + max(lengthAt(position), -4))
	case 0x05:
		return skip(5 + max(lengthAt(position), -5))
	case 0x0C:
		return skip(16 + max(lengthAt(position), -16))
	case 0x0B:
		patternEnd := bytes.IndexByte(walk.input[min(position, len(walk.input)):], 0)
		if patternEnd < 0 {
			return -1
		}
		optionsEnd := bytes.IndexByte(walk.input[position+patternEnd+1:], 0)
		if optionsEnd < 0 {
			return -1
		}
		return position + patternEnd + 1 + optionsEnd + 1
	}
	return -1
}

// TestEntryPointAllocsAtMostVendor holds MarshalValue and RawValue.Unmarshal
// to spec 9.2's allocation rule, as TestMarshalAllocsAtMostVendor and
// TestDecodeAllocsAtMostVendor hold Marshal and Unmarshal: no more
// allocations than the vendor on the same input, measured here.
func TestEntryPointAllocsAtMostVendor(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts change under -race")
	}
	libRecord, vendorRecord := benchdata.Ref450LibRecord(), benchdata.Ref450Record()
	ref450 := decRef450(t)
	rows := []struct {
		name            string
		library, vendor func()
	}{
		{"MarshalValue of Ref450", func() { _, _, _ = bson.MarshalValue(libRecord) }, func() { _, _, _ = vendorbson.MarshalValue(vendorRecord) }},
		{"MarshalValue of an int32", func() { _, _, _ = bson.MarshalValue(int32(7)) }, func() { _, _, _ = vendorbson.MarshalValue(int32(7)) }},
		{"RawValue.Unmarshal of Ref450 into its struct", func() {
			var record benchdata.LibRecord
			_ = bson.RawValue{Type: bsontype.EmbeddedDocument, Value: ref450}.Unmarshal(&record)
		}, func() {
			var record benchdata.Record
			_ = vendorbson.RawValue{Type: vendorbson.TypeEmbeddedDocument, Value: ref450}.Unmarshal(&record)
		}},
		{"Document.Decode of Ref450 into its struct (Unmarshal for the vendor)", func() {
			document, _ := bson.TrustStored(ref450)
			var record benchdata.LibRecord
			_ = document.Decode(&record)
		}, func() {
			var record benchdata.Record
			_ = vendorbson.Unmarshal(ref450, &record)
		}},
	}
	for _, row := range rows {
		vendorAllocs, libAllocs := testing.AllocsPerRun(200, row.vendor), testing.AllocsPerRun(200, row.library)
		t.Logf("%s: library %.0f allocations, vendor %.0f", row.name, libAllocs, vendorAllocs)
		if libAllocs > vendorAllocs {
			t.Errorf("%s: the library allocates %.0f times per call, the vendor %.0f", row.name, libAllocs, vendorAllocs)
		}
	}
}
