// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// The differential for bson.Raw, bson.RawElement and bson.RawValue (build
// doc step 4a). It is the bsoncore harness of step 4b over the bson types:
// every exported method of the vendor's type is called by reflection on both
// libraries with the same bytes, and the values, ok flags, error text and
// panics must be equal. Past the 200 ceiling the library's renderings refuse
// with the ErrNestingTooDeep text instead (spec 5.2).

// nestingRefusal is what the bson package's renderings give for input
// nested deeper than bson.MaxNestingCeiling: the sentinel's own text, so the
// refusal says what it is and cannot be mistaken for a rendering.
var nestingRefusal = fmt.Sprintf("<%v: more than %d levels>", bson.ErrNestingTooDeep, bson.MaxNestingCeiling)

// renderedRefusal is nestingRefusal as invoke prints a returned string.
var renderedRefusal = fmt.Sprintf("string(%q)", nestingRefusal)

// The vendor methods the library's types do not have, which are not called
// here: RawValue's two registry forms, kept out by step 4f. RawValue.Unmarshal
// is compared in oracle/entrypoints_test.go. RawElement.String renders through the Extended JSON writer of step 5a,
// and is compared by its own test (TestRawElementStringMatchesVendor),
// because where the vendor's panics the library renders "<malformed>".
var (
	rawValueMethodsLater = map[string]string{
		// Step 4f: not mirrored. Their parameters are a *bsoncodec.DecodeContext
		// and a *bsoncodec.Registry, which spec call C7 leaves out, and the
		// inventory has no reference to either method.
		"UnmarshalWithContext":  "kept out (step 4f): takes a bsoncodec.DecodeContext",
		"UnmarshalWithRegistry": "kept out (step 4f): takes a bsoncodec.Registry",
	}
	rawElementMethodsLater = map[string]string{}
)

var (
	rawComparison        = methodComparison{kind: "Raw", vendorType: reflect.TypeOf(vendorbson.Raw{}), refusals: map[string]string{"String": renderedRefusal}}
	rawElementComparison = methodComparison{kind: "RawElement", vendorType: reflect.TypeOf(vendorbson.RawElement{}),
		refusals: map[string]string{"DebugString": renderedRefusal}, later: rawElementMethodsLater,
		ownTest: map[string]string{"String": "TestRawElementStringMatchesVendor"}}
	rawValueComparison = methodComparison{kind: "RawValue", vendorType: reflect.TypeOf(vendorbson.RawValue{}),
		refusals: map[string]string{"String": renderedRefusal, "DebugString": renderedRefusal}, later: rawValueMethodsLater,
		ownTest: map[string]string{"Unmarshal": "TestEntryPointsMatchVendor"}}
)

func rawValuePair(typeByte byte, data []byte) argumentPair {
	return argumentPair{
		library: []reflect.Value{reflect.ValueOf(bson.RawValue{Type: bsontype.Type(typeByte), Value: data})},
		vendor:  []reflect.Value{reflect.ValueOf(vendorbson.RawValue{Type: vendorbsontype.Type(typeByte), Value: data})},
	}
}

func rawSubjects(documents [][]byte) []subject {
	return byteSubjects(documents, levelsOfDocument,
		func(b []byte) reflect.Value { return reflect.ValueOf(bson.Raw(b)) },
		func(b []byte) reflect.Value { return reflect.ValueOf(vendorbson.Raw(b)) })
}

func rawElementSubjects(elements [][]byte) []subject {
	return byteSubjects(elements, func(b []byte) int { return levelsOfValue(vendorbsoncore.Element(b).Value()) },
		func(b []byte) reflect.Value { return reflect.ValueOf(bson.RawElement(b)) },
		func(b []byte) reflect.Value { return reflect.ValueOf(vendorbson.RawElement(b)) })
}

func rawValueSubject(typeByte byte, data []byte) subject {
	libraryData, vendorData := slices.Clone(data), slices.Clone(data)
	if data == nil {
		libraryData, vendorData = nil, nil
	}
	return subject{
		label:    fmt.Sprintf("RawValue{0x%02x, %s}", typeByte, truncatedHex(data)),
		library:  reflect.ValueOf(bson.RawValue{Type: bsontype.Type(typeByte), Value: libraryData}),
		vendor:   reflect.ValueOf(vendorbson.RawValue{Type: vendorbsontype.Type(typeByte), Value: vendorData}),
		typeByte: typeByte,
		depth: measured(func() int {
			return levelsOfValue(vendorbsoncore.Value{Type: vendorbsontype.Type(typeByte), Data: data})
		}),
	}
}

// deepThenMalformed is a document 201 levels deep whose root then holds an
// element that cannot be read: the vendor renders nothing for it, and the
// library refuses it as too deep, because the depth is met first.
func deepThenMalformed() []byte {
	document := documentOf(elementOf(0x03, "deep", nestedDocument(200, false)), elementOf(0x02, "bad", int32Of(1<<20)))
	return document
}

// shallowThenDeep holds a shallow element before a 201-level one, so the
// ceiling is met in the second element of the root, not the first.
func shallowThenDeep() []byte {
	return documentOf(elementOf(0x03, "flat", documentOf(elementOf(0x10, "x", int32Of(1)))), elementOf(0x04, "deep", nestedDocument(200, true)))
}

// rawDocuments are the documents of the Raw differential: the bsoncore
// corpus (hand-stated documents, the v1.0 corpus and vectors, damaged
// copies), the generated documents of the 2b lookup differential, and the
// depth edges.
func rawDocuments(t *testing.T) [][]byte {
	t.Helper()
	documents := append([][]byte{}, sharedBsoncoreInputs(t).documents...)
	documents = append(documents, generatedLookupDocuments()...)
	for _, useArrays := range []bool{false, true} {
		for _, levels := range []int{199, 200, 201, 202, 1000} {
			documents = append(documents, nestedDocument(levels, useArrays))
		}
	}
	return append(documents, deepThenMalformed(), shallowThenDeep())
}

// rawElements are the bsoncore corpus's elements and the elements of the
// generated and deep documents.
func rawElements(t *testing.T, documents [][]byte) [][]byte {
	t.Helper()
	elements := append([][]byte{}, sharedBsoncoreInputs(t).elements...)
	for _, document := range documents[len(sharedBsoncoreInputs(t).documents):] {
		rest := document[4:]
		for range 16 {
			element, remaining, ok := vendorbsoncore.ReadElement(rest)
			if !ok {
				break
			}
			elements = append(elements, slices.Clone(element))
			rest = remaining
		}
	}
	for _, levels := range []int{200, 201} {
		elements = append(elements, elementOf(0x03, "k", nestedDocument(levels, false)), elementOf(0x04, "k", nestedDocument(levels, true)))
	}
	return elements
}

// TestRawMethodsMatchVendor runs Lookup, LookupErr, Elements, Values,
// Index, IndexErr, Validate and String of Raw, and every method of
// RawElement but String, against the vendor's over the corpus.
func TestRawMethodsMatchVendor(t *testing.T) {
	documents := rawDocuments(t)
	t.Run("Raw", func(t *testing.T) {
		compareMethods(t, rawComparison, rawSubjects(documents))
	})
	t.Run("RawElement", func(t *testing.T) {
		compareMethods(t, rawElementComparison, rawElementSubjects(rawElements(t, documents)))
	})
}

// TestRawValueAccessorsMatchVendor runs every RawValue method but the three
// Unmarshal forms, the typed accessors with their OK forms included, on
// every element's bytes under every type byte, against the vendor's.
func TestRawValueAccessorsMatchVendor(t *testing.T) {
	inputs := sharedBsoncoreInputs(t)
	subjects := make([]subject, 0, len(inputs.values)+4)
	for _, vendorValue := range inputs.values {
		subjects = append(subjects, rawValueSubject(byte(vendorValue.Type), vendorValue.Data))
	}
	for _, levels := range []int{200, 201} {
		subjects = append(subjects, rawValueSubject(0x03, nestedDocument(levels, false)), rawValueSubject(0x04, nestedDocument(levels, true)),
			rawValueSubject(0x0F, codeWithScopeOf("f()", nestedDocument(levels, false))))
	}
	compareMethods(t, rawValueComparison, subjects)
}

// panicTypeOf calls a method with no arguments and returns the type name of
// the value it panics with, or "" when it returns.
func panicTypeOf(receiver reflect.Value, methodName string, arguments []reflect.Value) (typeName string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			typeName = reflect.TypeOf(recovered).Kind().String() + " " + reflect.TypeOf(recovered).Name()
		}
	}()
	receiver.MethodByName(methodName).Call(arguments)
	return ""
}

// TestRawValueAccessorsPanicLikeTheVendor pins S-4 for RawValue and
// Raw.Index: each panics where the vendor's does, with the same text and the
// same recovered value type (the library's error types are unexported, so
// the name matches up to its first letter's case), and each OK form returns
// false instead.
func TestRawValueAccessorsPanicLikeTheVendor(t *testing.T) {
	typed := map[string]byte{
		"Double": 0x01, "StringValue": 0x02, "Document": 0x03, "Array": 0x04, "Binary": 0x05, "ObjectID": 0x07,
		"Boolean": 0x08, "DateTime": 0x09, "Time": 0x09, "Regex": 0x0B, "DBPointer": 0x0C, "JavaScript": 0x0D,
		"Symbol": 0x0E, "CodeWithScope": 0x0F, "Int32": 0x10, "Timestamp": 0x11, "Int64": 0x12, "Decimal128": 0x13,
		"AsInt32": 0x10, "AsInt64": 0x12,
	}
	panicked := 0
	for methodName, rightType := range typed {
		for _, caseOf := range []struct {
			label    string
			typeByte byte
			data     []byte
		}{{"wrong type", 0x0A, int32Of(1)}, {"three bytes", rightType, []byte{1, 2, 3}}, {"no data", rightType, nil}} {
			library := reflect.ValueOf(bson.RawValue{Type: bsontype.Type(caseOf.typeByte), Value: caseOf.data})
			vendor := reflect.ValueOf(vendorbson.RawValue{Type: vendorbsontype.Type(caseOf.typeByte), Value: caseOf.data})
			libraryResult, vendorResult := invoke(library, methodName, nil), invoke(vendor, methodName, nil)
			if libraryResult != vendorResult {
				t.Errorf("RawValue.%s, %s:\n library %s\n  vendor %s", methodName, caseOf.label, libraryResult, vendorResult)
			}
			// One byte is a whole boolean, so three bytes only prove a panic for the others.
			if methodName == "Boolean" && caseOf.label == "three bytes" {
				continue
			}
			if !strings.HasPrefix(vendorResult, "panic(error=true)") {
				t.Errorf("RawValue.%s, %s: the vendor did not panic (%s), so the case proves nothing", methodName, caseOf.label, vendorResult)
			}
			panicked++
			libraryType, vendorType := panicTypeOf(library, methodName, nil), panicTypeOf(vendor, methodName, nil)
			if libraryType == "" || !strings.EqualFold(libraryType, vendorType) {
				t.Errorf("RawValue.%s, %s: the library panics with %q, the vendor with %q", methodName, caseOf.label, libraryType, vendorType)
			}
			if okResult := invoke(library, methodName+"OK", nil); strings.HasPrefix(okResult, "panic") {
				t.Errorf("RawValue.%s, %s: %sOK panicked: %s", methodName, caseOf.label, methodName, okResult)
			}
		}
	}
	for _, document := range [][]byte{documentOf(), documentOf(elementOf(0x10, "a", int32Of(1))), {5, 0, 0, 0}, nil} {
		for _, position := range []uint{0, 1, 9} {
			arguments := []reflect.Value{reflect.ValueOf(position)}
			library, vendor := reflect.ValueOf(bson.Raw(slices.Clone(document))), reflect.ValueOf(vendorbson.Raw(slices.Clone(document)))
			if libraryResult, vendorResult := invoke(library, "Index", arguments), invoke(vendor, "Index", arguments); libraryResult != vendorResult {
				t.Errorf("Raw.Index(%d) on % x:\n library %s\n  vendor %s", position, document, libraryResult, vendorResult)
			}
			if libraryType, vendorType := panicTypeOf(library, "Index", arguments), panicTypeOf(vendor, "Index", arguments); !strings.EqualFold(libraryType, vendorType) {
				t.Errorf("Raw.Index(%d) on % x: the library panics with %q, the vendor with %q", position, document, libraryType, vendorType)
			}
		}
	}
	t.Logf("%d panicking accessor cases", panicked)
}

// linearlyNestedDocument is nestedDocument built in one pass, for depths
// where nesting by copying would be quadratic.
func linearlyNestedDocument(levels int) []byte {
	const levelOverhead = 8 // length prefix, type byte, key "a", its terminator, and the document's terminator
	document := make([]byte, 0, levels*levelOverhead)
	for level := range levels - 1 {
		document = binary.LittleEndian.AppendUint32(document, uint32(5+levelOverhead*(levels-1-level)))
		document = append(document, 0x03, 'a', 0)
	}
	document = append(document, 5, 0, 0, 0, 0)
	for range levels - 1 {
		document = append(document, 0)
	}
	return document
}

// TestRawStringRefusesPastTheCeiling holds the bson renderings to the 200
// ceiling (spec 5.2, call C2): at 200 levels they render as the vendor's do,
// and past it they return the ErrNestingTooDeep refusal, never bsoncore's
// empty safety net.
func TestRawStringRefusesPastTheCeiling(t *testing.T) {
	if !strings.Contains(nestingRefusal, bson.ErrNestingTooDeep.Error()) {
		t.Fatalf("the refusal %q does not carry the sentinel's text", nestingRefusal)
	}
	for _, useArrays := range []bool{false, true} {
		for _, levels := range []int{1, 2, 199, 200, 201, 202, 1000} {
			documentBytes := nestedDocument(levels, useArrays)
			valueType := byte(0x03)
			if useArrays {
				valueType = 0x04
			}
			elementBytes := elementOf(valueType, "k", documentBytes)
			scopeBytes := codeWithScopeOf("f()", nestedDocument(levels, false))
			renderings := []struct {
				name             string
				library, vendor  string
				vendorIsRendered bool
			}{
				{"Raw.String", bson.Raw(documentBytes).String(), vendorbson.Raw(documentBytes).String(), true},
				{"RawValue.String", bson.RawValue{Type: bsontype.Type(valueType), Value: documentBytes}.String(),
					vendorbson.RawValue{Type: vendorbsontype.Type(valueType), Value: documentBytes}.String(), true},
				{"RawValue.DebugString", bson.RawValue{Type: bsontype.Type(valueType), Value: documentBytes}.DebugString(),
					vendorbson.RawValue{Type: vendorbsontype.Type(valueType), Value: documentBytes}.DebugString(), true},
				{"RawValue.String of a code-with-scope", bson.RawValue{Type: bsontype.CodeWithScope, Value: scopeBytes}.String(),
					vendorbson.RawValue{Type: vendorbson.TypeCodeWithScope, Value: scopeBytes}.String(), true},
				{"RawElement.DebugString", bson.RawElement(elementBytes).DebugString(), vendorbson.RawElement(elementBytes).DebugString(), true},
			}
			for _, rendering := range renderings {
				label := fmt.Sprintf("%s, %d levels, arrays=%t", rendering.name, levels, useArrays)
				if rendering.vendor == "" || strings.Contains(rendering.vendor, "<malformed>") {
					t.Errorf("%s: the vendor renders %q, so the case proves nothing", label, clipped(rendering.vendor))
				}
				if levels <= bson.MaxNestingCeiling {
					if rendering.library != rendering.vendor {
						t.Errorf("%s: library %q differs from the vendor's %q", label, clipped(rendering.library), clipped(rendering.vendor))
					}
					continue
				}
				if rendering.library != nestingRefusal {
					t.Errorf("%s: library rendered %q, want the refusal %q", label, clipped(rendering.library), nestingRefusal)
				}
			}
		}
	}
	for name, documentBytes := range map[string][]byte{
		"201 levels, then an unreadable element": deepThenMalformed(),
		"a shallow element, then 201 levels":     shallowThenDeep(),
		"100,000 levels":                         linearlyNestedDocument(100_000),
	} {
		if rendered := bson.Raw(documentBytes).String(); rendered != nestingRefusal {
			t.Errorf("Raw.String, %s: %q, want the refusal", name, clipped(rendered))
		}
	}
	if levels := levelsOfDocument(linearlyNestedDocument(300)); levels != 300 {
		t.Fatalf("linearlyNestedDocument(300) opens %d levels", levels)
	}
	if rendered := bson.Raw(linearlyNestedDocument(200)).String(); rendered != vendorbson.Raw(linearlyNestedDocument(200)).String() {
		t.Errorf("Raw.String of the linear 200-level document differs from the vendor's")
	}
}

// FuzzRawDifferential runs every method of Raw, RawElement and RawValue on
// arbitrary bytes, the first byte being the RawValue's type, against the
// vendor's.
func FuzzRawDifferential(f *testing.F) {
	for _, seed := range baseDocuments() {
		f.Add(seed)
	}
	for _, element := range scalarElements() {
		f.Add(element)
	}
	f.Add(shallowThenDeep())
	f.Fuzz(func(t *testing.T, input []byte) {
		compareMethods(t, rawComparison, rawSubjects([][]byte{input}))
		compareMethods(t, rawElementComparison, rawElementSubjects([][]byte{input}))
		if len(input) > 0 {
			compareMethods(t, rawValueComparison, []subject{rawValueSubject(input[0], input[1:])})
		}
	})
}
