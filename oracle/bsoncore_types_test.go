// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// The differential for bsoncore's Value, Element, Document and Array. Every
// exported method of the vendor's type is called, by reflection, on both
// libraries with the same bytes, and what comes back must be equal: the
// values, the ok flags, the error text, and, when the call panics, that it
// panicked and with what text. The method list comes from the vendor's
// type, so a method the harness has no arguments for fails the run instead
// of being skipped.

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// renderResult writes one returned value so that a library value and the
// vendor's equal one print the same.
func renderResult(result reflect.Value) string {
	resultType := result.Type()
	if resultType.Implements(errorType) {
		if result.IsNil() {
			return "error(nil)"
		}
		return "error(" + result.Interface().(error).Error() + ")"
	}
	switch result.Kind() {
	case reflect.Slice:
		if result.Type().Elem().Kind() == reflect.Uint8 {
			if result.IsNil() {
				return "bytes(nil)"
			}
			return "bytes(" + hex.EncodeToString(result.Bytes()) + ")"
		}
		if result.IsNil() {
			return "list(nil)"
		}
		var items []string
		for position := range result.Len() {
			items = append(items, renderResult(result.Index(position)))
		}
		return "list(" + strings.Join(items, ", ") + ")"
	case reflect.Array:
		var items []byte
		for position := range result.Len() {
			items = append(items, byte(result.Index(position).Uint()))
		}
		return "array(" + hex.EncodeToString(items) + ")"
	case reflect.Struct:
		return renderStruct(result)
	case reflect.Bool:
		return fmt.Sprint(result.Bool())
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("float(%016x)", math.Float64bits(result.Float()))
	case reflect.String:
		return fmt.Sprintf("string(%q)", result.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("int(%d)", result.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("uint(%d)", result.Uint())
	default:
		panic(fmt.Sprintf("renderResult has no case for %s", resultType))
	}
}

func renderStruct(result reflect.Value) string {
	switch result.Type().Name() {
	case "RawValue":
		return fmt.Sprintf("RawValue{%s %s}", renderResult(result.FieldByName("Type")), renderResult(result.FieldByName("Value")))
	case "Value":
		return fmt.Sprintf("Value{%s %s}", renderResult(result.FieldByName("Type")), renderResult(result.FieldByName("Data")))
	case "Time":
		moment := result.Interface()
		unixSeconds := reflect.ValueOf(moment).MethodByName("Unix").Call(nil)[0].Int()
		nanoseconds := reflect.ValueOf(moment).MethodByName("Nanosecond").Call(nil)[0].Int()
		location := reflect.ValueOf(moment).MethodByName("Location").Call(nil)[0].Interface().(fmt.Stringer).String()
		return fmt.Sprintf("Time{%d.%09d %s}", unixSeconds, nanoseconds, location)
	case "Decimal128":
		parts := result.MethodByName("GetBytes").Call(nil)
		return fmt.Sprintf("Decimal128{%016x %016x}", parts[0].Uint(), parts[1].Uint())
	default:
		panic(fmt.Sprintf("renderResult has no struct case for %s", result.Type()))
	}
}

// invoke calls a method and renders every result, or the panic.
func invoke(receiver reflect.Value, methodName string, arguments []reflect.Value) (rendered string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			_, isError := recovered.(error)
			rendered = fmt.Sprintf("panic(error=%t): %v", isError, recovered)
		}
	}()
	var parts []string
	for _, result := range receiver.MethodByName(methodName).Call(arguments) {
		parts = append(parts, renderResult(result))
	}
	return strings.Join(parts, " | ")
}

// vendorPanicsOnBounds is the other allowed difference: the vendor panics on
// a slice bound where it adds a header size to an int32 length and wraps it
// negative (a binary, string or DBPointer length near 2^31), and the library,
// which measures in int64, reads the bytes as malformed without panicking.
func vendorPanicsOnBounds(libraryResult, vendorResult string) bool {
	return strings.HasPrefix(vendorResult, "panic(error=true): runtime error: slice bounds out of range") &&
		!strings.HasPrefix(libraryResult, "panic")
}

// libraryValidateIsStricter is the one allowed difference in Validate: the
// vendor accepts a string of length zero or without its terminator, a
// document shorter than five bytes inside a value, and an old binary whose
// inner length overruns it, and panics on a length that wraps or a zero
// document length. Elements and Values validate as they read, so they share
// the rule. The library refuses them all with an error, because its
// input is untrusted client bytes and a panic closes the connection. The
// reverse (the library accepting what the vendor refuses) is never allowed.
//
// When both refuse, Validate words its error from a different element, and
// Elements and Values return the elements read before it, so when both
// refuse, the library, refusing one element earlier, returns a shorter list.
func libraryValidateIsStricter(methodName, libraryResult, vendorResult string) bool {
	libraryRefuses := strings.Contains(libraryResult, "error(") && !strings.HasSuffix(libraryResult, "error(nil)")
	vendorAccepts := strings.HasSuffix(vendorResult, "error(nil)") || strings.HasPrefix(vendorResult, "panic")
	vendorAlsoRefuses := strings.Contains(vendorResult, "error(") && !vendorAccepts
	return libraryRefuses && (vendorAccepts || vendorAlsoRefuses)
}

// validateFamily are the methods that validate each element as they go, so
// they share Validate's stricter framing.
var validateFamily = map[string]bool{"Validate": true, "Elements": true, "Values": true}

// subject is one receiver, made for both libraries from the same bytes.
type subject struct {
	label    string
	library  reflect.Value
	vendor   reflect.Value
	raw      []byte // the bytes of an Element, Document or Array
	typeByte byte   // a Value's type
	depth    int    // how many levels its rendering opens, by the vendor's own reading
}

// levelsOfDocument counts the levels a rendering of a document opens: itself
// and the deepest document, array or scope below it, reading with the
// vendor's functions along the path the renderings take. It is the test's
// own measure, so the ceiling is applied to what the vendor would render.
func levelsOfDocument(document []byte) int {
	if len(document) < 5 {
		return 0
	}
	length, rest, _ := vendorbsoncore.ReadLength(document)
	length -= 4
	deepest := 0
	for length > 1 {
		element, remaining, ok := vendorbsoncore.ReadElement(rest)
		length -= int32(len(element))
		if !ok {
			break
		}
		rest = remaining
		deepest = max(deepest, levelsOfValue(element.Value()))
	}
	return 1 + deepest
}

func levelsOfValue(value vendorbsoncore.Value) int {
	switch value.Type {
	case vendorbson.TypeEmbeddedDocument:
		if document, ok := value.DocumentOK(); ok {
			return levelsOfDocument(document)
		}
	case vendorbson.TypeArray:
		if array, ok := value.ArrayOK(); ok {
			return levelsOfDocument(array)
		}
	case vendorbson.TypeCodeWithScope:
		if _, scope, ok := value.CodeWithScopeOK(); ok {
			return levelsOfDocument(scope)
		}
	}
	return 0
}

// measured runs one of the harness's own readings of the vendor's bytes. The
// vendor's readers panic on some inputs (a string length of 0x7fffffff
// overflows in readValue); such an input is unmeasurable, depth 0, and the
// methods are still compared one by one, panics included.
func measured(measure func() int) (levels int) {
	defer func() {
		if recover() != nil {
			levels = 0
		}
	}()
	return measure()
}

// ceilingLevels is the library's rendering ceiling (spec 5.2).
const ceilingLevels = 200

// argumentPair is one set of arguments, spelled for each library.
type argumentPair struct{ library, vendor []reflect.Value }

func valuePair(typeByte byte, data []byte) argumentPair {
	return argumentPair{
		library: []reflect.Value{reflect.ValueOf(bsoncore.Value{Type: bsontype.Type(typeByte), Data: data})},
		vendor:  []reflect.Value{reflect.ValueOf(vendorbsoncore.Value{Type: vendorbsontype.Type(typeByte), Data: data})},
	}
}

func stringsPair(keys []string) argumentPair {
	var arguments []reflect.Value
	for _, key := range keys {
		arguments = append(arguments, reflect.ValueOf(key))
	}
	return argumentPair{library: arguments, vendor: arguments}
}

func scalarPair(argument any) argumentPair {
	return argumentPair{library: []reflect.Value{reflect.ValueOf(argument)}, vendor: []reflect.Value{reflect.ValueOf(argument)}}
}

// lookupPaths are the key paths a document is searched with: none, empty,
// absent, each top-level key, each key one level down, and a path through a
// scalar.
func lookupPaths(document []byte) (paths [][]string) {
	paths = [][]string{{}, {""}, {"missing"}, {"missing", "deeper"}}
	// The vendor's Element.Value can panic on a hostile length; the paths
	// found before that point are kept.
	defer func() { _ = recover() }()
	if len(document) < 5 {
		return paths
	}
	seen := map[string]bool{}
	add := func(path ...string) {
		if key := strings.Join(path, "\x00") + fmt.Sprint(len(path)); !seen[key] && len(paths) < 14 {
			seen[key] = true
			paths = append(paths, path)
		}
	}
	rest := document[4:]
	for range 8 {
		element, remaining, ok := vendorbsoncore.ReadElement(rest)
		if !ok {
			break
		}
		rest = remaining
		key := element.Key()
		add(key)
		add(key, "x")
		if t := vendorbsontype.Type(element[0]); t == vendorbson.TypeEmbeddedDocument || t == vendorbson.TypeArray {
			if inner, ok := element.Value().DocumentOK(); ok && len(inner) >= 5 {
				if innerElement, _, ok := vendorbsoncore.ReadElement(inner[4:]); ok {
					add(key, innerElement.Key())
					add(key, innerElement.Key(), "x")
				}
			}
		}
	}
	return paths
}

// argumentsFor returns the argument sets for a method of the named kind.
func argumentsFor(kind, methodName string, subjectOf subject) ([]argumentPair, bool) {
	switch methodName {
	case "Equal":
		if kind == "RawValue" {
			return []argumentPair{
				rawValuePair(subjectOf.typeByte, nil),
				rawValuePair(subjectOf.typeByte, []byte{1}),
				rawValuePair(0x10, int32Of(1)),
				rawValuePair(0x02, stringValueOf("a")),
			}, true
		}
		return []argumentPair{
			valuePair(subjectOf.typeByte, nil),
			valuePair(subjectOf.typeByte, []byte{1}),
			valuePair(0x10, int32Of(1)),
			valuePair(0x02, stringValueOf("a")),
		}, true
	case "Lookup", "LookupErr":
		var pairs []argumentPair
		for _, path := range lookupPaths(subjectOf.raw) {
			pairs = append(pairs, stringsPair(path))
		}
		return pairs, true
	case "Index", "IndexErr":
		var pairs []argumentPair
		for _, position := range []uint{0, 1, 2, 7, 1 << 20} {
			pairs = append(pairs, scalarPair(position))
		}
		return pairs, true
	case "CompareKey":
		pairs := []argumentPair{scalarPair([]byte("a")), scalarPair([]byte("")), scalarPair([]byte(nil)), scalarPair([]byte("a\x00tail"))}
		if len(subjectOf.raw) > 1 {
			if end := strings.IndexByte(string(subjectOf.raw[1:]), 0); end >= 0 {
				pairs = append(pairs, scalarPair(append([]byte(nil), subjectOf.raw[1:end+1]...)),
					scalarPair(append(append([]byte(nil), subjectOf.raw[1:end+1]...), 0)))
			}
		}
		return pairs, true
	}
	return []argumentPair{{}}, true
}

// mismatchLimit caps the failures printed per method: one broken method
// can fail on thousands of inputs.
const mismatchLimit = 4

// bsoncoreRefusals are what bsoncore's renderings give past the ceiling:
// the empty rendering they already use for input they cannot render.
var bsoncoreRefusals = map[string]string{"String": `string("")`, "DebugString": `string("<malformed>")`}

// methodComparison says how one type is compared: the refusal each
// rendering gives past the ceiling, the vendor methods the library builds
// in a later step, each with that step, which are not called, and the
// methods a test of their own compares, each with that test.
type methodComparison struct {
	kind       string
	vendorType reflect.Type
	refusals   map[string]string
	later      map[string]string
	ownTest    map[string]string
}

// compareSubjects runs every method of the vendor's type over the subjects.
func compareSubjects(t *testing.T, kind string, vendorType reflect.Type, subjects []subject) {
	t.Helper()
	compareMethods(t, methodComparison{kind: kind, vendorType: vendorType, refusals: bsoncoreRefusals}, subjects)
}

// compareMethods runs every method of the comparison's vendor type over
// the subjects, but those it lists as arriving later, which the library's
// type must not have yet.
func compareMethods(t *testing.T, comparison methodComparison, subjects []subject) {
	t.Helper()
	kind, vendorType := comparison.kind, comparison.vendorType
	if vendorType.NumMethod() == 0 {
		t.Fatalf("%s: the vendor type has no methods, so the harness compares nothing", kind)
	}
	calls := 0
	for methodIndex := range vendorType.NumMethod() {
		methodName := vendorType.Method(methodIndex).Name
		if step, later := comparison.later[methodName]; later {
			if len(subjects) > 0 && subjects[0].library.MethodByName(methodName).IsValid() {
				t.Errorf("%s.%s is listed as arriving in %s but the library has it; compare it", kind, methodName, step)
			}
			continue
		}
		if _, separate := comparison.ownTest[methodName]; separate {
			if len(subjects) > 0 && !subjects[0].library.MethodByName(methodName).IsValid() {
				t.Errorf("%s.%s: the library's type has no such method", kind, methodName)
			}
			continue
		}
		mismatches := 0
		for _, subjectOf := range subjects {
			if !subjectOf.library.MethodByName(methodName).IsValid() {
				t.Fatalf("%s.%s: the library's type has no such method", kind, methodName)
			}
			pairs, _ := argumentsFor(kind, methodName, subjectOf)
			if wanted := vendorType.Method(methodIndex).Type.NumIn() - 1; wanted > 0 && !vendorType.Method(methodIndex).Type.IsVariadic() && len(pairs[0].vendor) != wanted {
				t.Fatalf("%s.%s takes %d arguments and the harness has no spelling for them", kind, methodName, wanted)
			}
			for _, pair := range pairs {
				calls++
				if refusal, renders := comparison.refusals[methodName]; renders && subjectOf.depth > ceilingLevels {
					// Past the ceiling the library refuses; the vendor renders without limit.
					if libraryResult := invoke(subjectOf.library, methodName, nil); libraryResult != refusal {
						mismatches++
						t.Errorf("%s.%s on %s: %d levels deep, library gave %s, want the refusal %s", kind, methodName, subjectOf.label,
							subjectOf.depth, clipped(libraryResult), refusal)
					}
					continue
				}
				libraryResult := invoke(subjectOf.library, methodName, pair.library)
				vendorResult := invoke(subjectOf.vendor, methodName, pair.vendor)
				if vendorPanicsOnBounds(libraryResult, vendorResult) {
					continue
				}
				if validateFamily[methodName] && libraryValidateIsStricter(methodName, libraryResult, vendorResult) {
					continue
				}
				if libraryResult != vendorResult {
					mismatches++
					if mismatches <= mismatchLimit {
						t.Errorf("%s.%s on %s (arguments %v):\n library %s\n  vendor %s", kind, methodName, subjectOf.label,
							pair.vendor, clipped(libraryResult), clipped(vendorResult))
					}
				}
			}
		}
		if mismatches > mismatchLimit {
			t.Errorf("%s.%s: %d mismatches in all", kind, methodName, mismatches)
		}
	}
	t.Logf("%s: %d subjects, %d methods, %d calls", kind, len(subjects), vendorType.NumMethod()-len(comparison.later), calls)
}

func clipped(text string) string {
	if len(text) > 300 {
		return text[:300] + fmt.Sprintf("...(%d bytes)", len(text))
	}
	return text
}

var (
	inputsOnce   sync.Once
	inputsCached *bsoncoreInputs
	inputsErr    error
)

func sharedBsoncoreInputs(t *testing.T) *bsoncoreInputs {
	t.Helper()
	inputsOnce.Do(func() { inputsCached, inputsErr = buildBsoncoreInputs() })
	if inputsErr != nil {
		t.Fatalf("building the bsoncore inputs: %v", inputsErr)
	}
	return inputsCached
}

func byteSubjects(inputs [][]byte, depthOf func([]byte) int, libraryOf, vendorOf func([]byte) reflect.Value) []subject {
	subjects := make([]subject, 0, len(inputs))
	for _, input := range inputs {
		// Each side gets its own copy with exact capacity, so neither can
		// read bytes the other appended into a shared array.
		libraryBytes, vendorBytes := slices.Clone(input), slices.Clone(input)
		if input == nil {
			libraryBytes, vendorBytes = nil, nil
		}
		subjects = append(subjects, subject{label: truncatedHex(input), library: libraryOf(libraryBytes), vendor: vendorOf(vendorBytes), raw: input, depth: measured(func() int { return depthOf(input) })})
	}
	return subjects
}

// TestBsoncoreTypesMatchVendor is the type differential of step 4b.
func TestBsoncoreTypesMatchVendor(t *testing.T) {
	inputs := sharedBsoncoreInputs(t)
	t.Run("Value", func(t *testing.T) {
		subjects := make([]subject, 0, len(inputs.values))
		for _, vendorValue := range inputs.values {
			// The keyed literal is how server/fieldpath.go and
			// cmd/gen-testvectors construct a Value.
			libraryValue := bsoncore.Value{Type: bsontype.Type(vendorValue.Type), Data: slices.Clone(vendorValue.Data)}
			if vendorValue.Data == nil {
				libraryValue.Data = nil
			}
			subjects = append(subjects, subject{
				label:    fmt.Sprintf("Value{0x%02x, %s}", byte(vendorValue.Type), truncatedHex(vendorValue.Data)),
				library:  reflect.ValueOf(libraryValue),
				vendor:   reflect.ValueOf(vendorbsoncore.Value{Type: vendorValue.Type, Data: slices.Clone(vendorValue.Data)}),
				typeByte: byte(vendorValue.Type),
				depth:    measured(func() int { return levelsOfValue(vendorValue) }),
			})
		}
		compareSubjects(t, "Value", reflect.TypeOf(vendorbsoncore.Value{}), subjects)
	})
	t.Run("Element", func(t *testing.T) {
		compareSubjects(t, "Element", reflect.TypeOf(vendorbsoncore.Element{}), byteSubjects(inputs.elements, func(b []byte) int { return levelsOfValue(vendorbsoncore.Element(b).Value()) },
			func(b []byte) reflect.Value { return reflect.ValueOf(bsoncore.Element(b)) },
			func(b []byte) reflect.Value { return reflect.ValueOf(vendorbsoncore.Element(b)) }))
	})
	t.Run("Document", func(t *testing.T) {
		compareSubjects(t, "Document", reflect.TypeOf(vendorbsoncore.Document{}), byteSubjects(inputs.documents, levelsOfDocument,
			func(b []byte) reflect.Value { return reflect.ValueOf(bsoncore.Document(b)) },
			func(b []byte) reflect.Value { return reflect.ValueOf(vendorbsoncore.Document(b)) }))
	})
	t.Run("Array", func(t *testing.T) {
		arrays := append([][]byte{
			documentOf(elementOf(0x10, "0", int32Of(1)), elementOf(0x02, "1", stringValueOf("v")), elementOf(0x03, "2", documentOf())),
			documentOf(elementOf(0x10, "1", int32Of(1))),
			documentOf(elementOf(0x10, "0", int32Of(1)), elementOf(0x10, "0", int32Of(1))),
			documentOf(elementOf(0x10, "00", int32Of(1))),
			documentOf(elementOf(0x04, "0", documentOf(elementOf(0x10, "0", int32Of(5))))),
		}, inputs.documents...)
		compareSubjects(t, "Array", reflect.TypeOf(vendorbsoncore.Array{}), byteSubjects(arrays, levelsOfDocument,
			func(b []byte) reflect.Value { return reflect.ValueOf(bsoncore.Array(b)) },
			func(b []byte) reflect.Value { return reflect.ValueOf(vendorbsoncore.Array(b)) }))
	})
}

// TestBsoncoreAccessorsPanicLikeTheVendor pins the S-4 contract directly:
// each typed accessor panics on a type mismatch and on short data, with the
// vendor's text, and its OK form returns false instead.
func TestBsoncoreAccessorsPanicLikeTheVendor(t *testing.T) {
	typed := map[string]byte{
		"Double": 0x01, "StringValue": 0x02, "Document": 0x03, "Array": 0x04, "Binary": 0x05, "ObjectID": 0x07,
		"Boolean": 0x08, "DateTime": 0x09, "Time": 0x09, "Regex": 0x0B, "DBPointer": 0x0C, "JavaScript": 0x0D,
		"Symbol": 0x0E, "CodeWithScope": 0x0F, "Int32": 0x10, "Timestamp": 0x11, "Int64": 0x12, "Decimal128": 0x13,
		"AsInt32": 0x10, "AsInt64": 0x12,
	}
	for methodName, rightType := range typed {
		wrongType := byte(0x0A)
		for _, caseOf := range []struct {
			label    string
			typeByte byte
			data     []byte
		}{{"wrong type", wrongType, int32Of(1)}, {"three bytes", rightType, []byte{1, 2, 3}}, {"no data", rightType, nil}} {
			libraryResult := invoke(reflect.ValueOf(bsoncore.Value{Type: bsontype.Type(caseOf.typeByte), Data: caseOf.data}), methodName, nil)
			vendorResult := invoke(reflect.ValueOf(vendorbsoncore.Value{Type: vendorbsontype.Type(caseOf.typeByte), Data: caseOf.data}), methodName, nil)
			if libraryResult != vendorResult {
				t.Errorf("%s, %s:\n library %s\n  vendor %s", methodName, caseOf.label, libraryResult, vendorResult)
			}
			// One byte is a whole boolean, so three bytes only prove a panic for the others.
			if !strings.HasPrefix(vendorResult, "panic(error=true)") && (methodName != "Boolean" || caseOf.label != "three bytes") {
				t.Errorf("%s, %s: the vendor did not panic (%s), so the case proves nothing", methodName, caseOf.label, vendorResult)
			}
			okName := methodName + "OK"
			if okResult := invoke(reflect.ValueOf(bsoncore.Value{Type: bsontype.Type(caseOf.typeByte), Data: caseOf.data}), okName, nil); strings.HasPrefix(okResult, "panic") {
				t.Errorf("%s, %s: %s panicked: %s", methodName, caseOf.label, okName, okResult)
			}
		}
	}
}

// TestBsoncoreStringEscapesLikeTheVendor compares the quoted-string text of
// every single byte, every rune below U+3000 and the separators, through
// each type that quotes a string.
func TestBsoncoreStringEscapesLikeTheVendor(t *testing.T) {
	var probes []string
	for single := range 256 {
		probes = append(probes, string([]byte{byte(single)}), "a"+string([]byte{byte(single)})+"b")
	}
	for point := rune(0); point < 0x3000; point++ {
		probes = append(probes, string(point))
	}
	probes = append(probes, "\u2028", "\u2029", "\ufffd", "\U0010ffff", "\xf4\x90\x80\x80", "\xe2\x80", "x\xe2\x80")
	for _, probe := range probes {
		stringData := stringValueOf(probe)
		for _, caseOf := range []struct {
			typeByte byte
			data     []byte
		}{{0x02, stringData}, {0x0D, stringData}, {0x0E, stringData}, {0x0B, []byte(probe + "\x00ms\x00")}, {0x0C, append(append([]byte(nil), stringData...), make([]byte, 12)...)}} {
			libraryValue := bsoncore.Value{Type: bsontype.Type(caseOf.typeByte), Data: caseOf.data}
			vendorValue := vendorbsoncore.Value{Type: vendorbsontype.Type(caseOf.typeByte), Data: caseOf.data}
			if libraryValue.String() != vendorValue.String() {
				t.Fatalf("type 0x%02x, string %q:\n library %q\n  vendor %q", caseOf.typeByte, probe, libraryValue.String(), vendorValue.String())
			}
			if libraryValue.DebugString() != vendorValue.DebugString() {
				t.Fatalf("type 0x%02x, string %q, DebugString:\n library %q\n  vendor %q", caseOf.typeByte, probe, libraryValue.DebugString(), vendorValue.DebugString())
			}
		}
	}
}

// nestedDocument is a document nested to the given number of levels, the
// root being level 1 and the innermost an empty document. useArrays makes
// every enclosing level an array, with its keys in order.
func nestedDocument(levels int, useArrays bool) []byte {
	current := documentOf()
	for range levels - 1 {
		if useArrays {
			current = documentOf(elementOf(0x04, "0", current))
		} else {
			current = documentOf(elementOf(0x03, "a", current))
		}
	}
	return current
}

// TestBsoncoreStringRefusesPastTheCeiling holds the String and DebugString
// renderings to the 200 ceiling (spec 5.2): a 200-level input renders as
// the vendor renders it, a 201-level input is refused with the empty
// rendering these methods already use for input they cannot render.
func TestBsoncoreStringRefusesPastTheCeiling(t *testing.T) {
	for _, useArrays := range []bool{false, true} {
		for _, levels := range []int{1, 2, 199, 200, 201, 202, 1000} {
			documentBytes := nestedDocument(levels, useArrays)
			libraryDocument, vendorDocument := bsoncore.Document(slices.Clone(documentBytes)), vendorbsoncore.Document(slices.Clone(documentBytes))
			scopeBytes := codeWithScopeOf("f()", documentBytes)
			rendered := map[string][3]string{
				"Document": {libraryDocument.String(), vendorDocument.String(), libraryDocument.DebugString()},
				"Array": {bsoncore.Array(documentBytes).String(), vendorbsoncore.Array(documentBytes).String(),
					bsoncore.Array(documentBytes).DebugString()},
				"Value": {bsoncore.Value{Type: bsontype.EmbeddedDocument, Data: documentBytes}.String(),
					vendorbsoncore.Value{Type: vendorbson.TypeEmbeddedDocument, Data: documentBytes}.String(),
					bsoncore.Value{Type: bsontype.EmbeddedDocument, Data: documentBytes}.DebugString()},
				"Element": {bsoncore.Element(elementOf(0x03, "k", documentBytes)).String(),
					vendorbsoncore.Element(elementOf(0x03, "k", documentBytes)).String(),
					bsoncore.Element(elementOf(0x03, "k", documentBytes)).DebugString()},
			}
			// A code-with-scope's scope is one level, as in the depth scan.
			scopeLibrary := bsoncore.Value{Type: bsontype.CodeWithScope, Data: scopeBytes}
			scopeVendor := vendorbsoncore.Value{Type: vendorbson.TypeCodeWithScope, Data: scopeBytes}
			rendered["CodeWithScope"] = [3]string{scopeLibrary.String(), scopeVendor.String(), scopeLibrary.DebugString()}
			for name, texts := range rendered {
				label := fmt.Sprintf("%s, %d levels, arrays=%t", name, levels, useArrays)
				libraryString, vendorString, libraryDebug := texts[0], texts[1], texts[2]
				if vendorString == "" {
					t.Errorf("%s: the vendor renders nothing, so the case proves nothing", label)
				}
				if levels <= 200 {
					if libraryString != vendorString {
						t.Errorf("%s: String differs from the vendor's", label)
					}
					continue
				}
				if libraryString != "" {
					t.Errorf("%s: String rendered %d bytes past the ceiling, want the empty refusal", label, len(libraryString))
				}
				if libraryDebug != "<malformed>" {
					t.Errorf("%s: DebugString = %.60q past the ceiling, want <malformed>", label, libraryDebug)
				}
			}
			if levels <= 200 {
				for name, vendorDebug := range map[string]string{
					"Document": vendorDocument.DebugString(), "Array": vendorbsoncore.Array(documentBytes).DebugString(),
					"Value":         vendorbsoncore.Value{Type: vendorbson.TypeEmbeddedDocument, Data: documentBytes}.DebugString(),
					"Element":       vendorbsoncore.Element(elementOf(0x03, "k", documentBytes)).DebugString(),
					"CodeWithScope": scopeVendor.DebugString(),
				} {
					libraryDebug := map[string]string{
						"Document": libraryDocument.DebugString(), "Array": bsoncore.Array(documentBytes).DebugString(),
						"Value":         bsoncore.Value{Type: bsontype.EmbeddedDocument, Data: documentBytes}.DebugString(),
						"Element":       bsoncore.Element(elementOf(0x03, "k", documentBytes)).DebugString(),
						"CodeWithScope": scopeLibrary.DebugString(),
					}[name]
					if libraryDebug != vendorDebug {
						t.Errorf("%s, %d levels, arrays=%t: DebugString differs from the vendor's", name, levels, useArrays)
					}
				}
			}
		}
	}
}

// FuzzBsoncoreTypesDifferential runs every method of Document, Array,
// Element and Value on arbitrary bytes, the first byte being the Value's
// type, and compares them with the vendor's.
func FuzzBsoncoreTypesDifferential(f *testing.F) {
	for _, seed := range baseDocuments() {
		f.Add(seed)
	}
	for _, element := range scalarElements() {
		f.Add(element)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		documents := [][]byte{input}
		compareSubjects(t, "Document", reflect.TypeOf(vendorbsoncore.Document{}), byteSubjects(documents, levelsOfDocument,
			func(b []byte) reflect.Value { return reflect.ValueOf(bsoncore.Document(b)) },
			func(b []byte) reflect.Value { return reflect.ValueOf(vendorbsoncore.Document(b)) }))
		compareSubjects(t, "Array", reflect.TypeOf(vendorbsoncore.Array{}), byteSubjects(documents, levelsOfDocument,
			func(b []byte) reflect.Value { return reflect.ValueOf(bsoncore.Array(b)) },
			func(b []byte) reflect.Value { return reflect.ValueOf(vendorbsoncore.Array(b)) }))
		compareSubjects(t, "Element", reflect.TypeOf(vendorbsoncore.Element{}), byteSubjects(documents,
			func(b []byte) int { return levelsOfValue(vendorbsoncore.Element(b).Value()) },
			func(b []byte) reflect.Value { return reflect.ValueOf(bsoncore.Element(b)) },
			func(b []byte) reflect.Value { return reflect.ValueOf(vendorbsoncore.Element(b)) }))
		if len(input) > 0 {
			value := vendorbsoncore.Value{Type: vendorbsontype.Type(input[0]), Data: input[1:]}
			compareSubjects(t, "Value", reflect.TypeOf(vendorbsoncore.Value{}), []subject{{
				label: truncatedHex(input), library: reflect.ValueOf(bsoncore.Value{Type: bsontype.Type(input[0]), Data: slices.Clone(input[1:])}),
				vendor: reflect.ValueOf(value), typeByte: input[0], depth: measured(func() int { return levelsOfValue(value) }),
			}})
		}
	})
}
