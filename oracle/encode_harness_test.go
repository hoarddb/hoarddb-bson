// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
	"github.com/hoarddb/hoarddb-bson/oracle/compare"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// The encode differentials of step 4d. The library side is the public
// entry, bson.Marshal (step 4f), over the encoder of internal/encode.
// Each case gives both libraries the same Go value where its types are
// builtins, and twin values (the library's primitive, bson and hook types on
// one side, the vendor's on the other) where they are not.

// libraryPlans is a plan cache of the harness's own, read to learn a
// struct's layout (unsortedMapLevel); the encoder under test uses package
// bson's.
var libraryPlans = plan.NewCache(plan.BSONTypes{
	Raw:      reflect.TypeOf(bson.Raw(nil)),
	RawValue: reflect.TypeOf(bson.RawValue{}),
})

// encodeCase is one input. lib is what the library encodes, vendor what the
// vendor encodes: the same value unless a type differs between them.
type encodeCase struct {
	name   string
	lib    any
	vendor any
}

func sameValue(name string, value any) encodeCase {
	return encodeCase{name: name, lib: value, vendor: value}
}

func twinValues(name string, lib, vendor any) encodeCase {
	return encodeCase{name: name, lib: lib, vendor: vendor}
}

// libraryEncode returns the library's bytes, or its error text, or the panic
// it raised: a panic is a failure the test reports, never a crash.
func libraryEncode(value any) (encoded []byte, failure string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			encoded, failure = nil, fmt.Sprintf("panic: %v", recovered)
		}
	}()
	encoded, err := bson.Marshal(value)
	if err != nil {
		return nil, err.Error()
	}
	return encoded, ""
}

func vendorEncode(value any) (encoded []byte, failure string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			encoded, failure = nil, fmt.Sprintf("panic: %v", recovered)
		}
	}()
	encoded, err := vendorbson.Marshal(value)
	if err != nil {
		return nil, err.Error()
	}
	return encoded, ""
}

// requireEncodesLikeVendor runs each case as a subtest of requireSameEncoding
// and of requireSameExtJSON (step 5a's Go-value half): every encode case is
// also an Extended JSON case, in all four modes.
func requireEncodesLikeVendor(t *testing.T, cases ...encodeCase) {
	t.Helper()
	for _, encodeInput := range cases {
		t.Run(encodeInput.name, func(t *testing.T) {
			requireSameEncoding(t, encodeInput)
			requireSameExtJSON(t, encodeInput)
		})
	}
}

// requireSameEncoding requires that both libraries fail with the same text,
// or both succeed and compare.Encoded finds no difference. Bytes a hook
// wrote unchecked, which the vendor cannot decode back, must be equal byte
// for byte.
func requireSameEncoding(t *testing.T, encodeInput encodeCase) {
	t.Helper()
	vendorBytes, vendorFailure := vendorEncode(encodeInput.vendor)
	libBytes, libFailure := libraryEncode(encodeInput.lib)
	switch {
	case strings.HasPrefix(libFailure, bson.ErrNestingTooDeep.Error()) && vendorFailure == "":
		// The library's ceiling (spec 5.2), which the vendor does not have:
		// right only for a value that nests past it.
		if depth := readableDepth(vendorBytes, 1); depth <= bson.MaxNestingCeiling {
			t.Errorf("the library refuses a value %d levels deep at the ceiling: %s", depth, libFailure)
		}
	case vendorFailure != "" || libFailure != "":
		if vendorFailure != libFailure {
			t.Errorf("outcome differs:\n vendor:  %s\n library: %s\n library bytes: % x", outcomeText(vendorFailure), outcomeText(libFailure), libBytes)
		}
	case vendorbson.Unmarshal(vendorBytes, &vendorbson.D{}) != nil:
		if !bytes.Equal(vendorBytes, libBytes) {
			t.Errorf("unchecked hook bytes differ:\n vendor:  % x\n library: % x", vendorBytes, libBytes)
		}
	default:
		compare.Encoded(t, encodeInput.vendor, vendorBytes, libBytes)
	}
}

func outcomeText(failure string) string {
	if failure == "" {
		return "(encoded)"
	}
	return failure
}

// requireVendorOutcome asserts what the vendor itself does with a case, so
// a test that names a vendor rule fails if the vendor does not follow it.
func requireVendorOutcome(t *testing.T, value any, wantFailure string) []byte {
	t.Helper()
	encoded, failure := vendorEncode(value)
	if failure != wantFailure {
		t.Fatalf("the vendor's outcome for %T is %s, want %s", value, outcomeText(failure), outcomeText(wantFailure))
	}
	return encoded
}

// encodedElement returns the first element of an encoded document, which the
// single-field probes put their value in.
func encodedElement(t *testing.T, encoded []byte, key string) vendorbsoncore.Value {
	t.Helper()
	found, err := vendorbsoncore.Document(encoded).LookupErr(key)
	if err != nil {
		t.Fatalf("key %q in % x: %v", key, encoded, err)
	}
	return found
}

// libraryTwin converts a value built from the vendor's types (as the vendor
// decodes into bson.D, bson.M and interface{}) to the same value in the
// library's types. Builtins pass through.
func libraryTwin(tb testing.TB, vendorValue any) any {
	tb.Helper()
	switch typed := vendorValue.(type) {
	case nil, string, bool, int32, int64, float64, int, []byte:
		return typed
	case vendorprimitive.D:
		if typed == nil {
			return primitive.D(nil)
		}
		document := make(primitive.D, len(typed))
		for position, vendorElement := range typed {
			document[position] = primitive.E{Key: vendorElement.Key, Value: libraryTwin(tb, vendorElement.Value)}
		}
		return document
	case vendorprimitive.M:
		if typed == nil {
			return primitive.M(nil)
		}
		document := make(primitive.M, len(typed))
		for key, entry := range typed {
			document[key] = libraryTwin(tb, entry)
		}
		return document
	case map[string]any:
		if typed == nil {
			return map[string]any(nil)
		}
		document := make(map[string]any, len(typed))
		for key, entry := range typed {
			document[key] = libraryTwin(tb, entry)
		}
		return document
	case vendorprimitive.A:
		if typed == nil {
			return primitive.A(nil)
		}
		array := make(primitive.A, len(typed))
		for position, entry := range typed {
			array[position] = libraryTwin(tb, entry)
		}
		return array
	case []any:
		if typed == nil {
			return []any(nil)
		}
		array := make([]any, len(typed))
		for position, entry := range typed {
			array[position] = libraryTwin(tb, entry)
		}
		return array
	}
	return libraryScalarTwin(tb, vendorValue)
}

func libraryScalarTwin(tb testing.TB, vendorValue any) any {
	tb.Helper()
	switch typed := vendorValue.(type) {
	case vendorprimitive.ObjectID:
		return primitive.ObjectID(typed)
	case vendorprimitive.DateTime:
		return primitive.DateTime(typed)
	case vendorprimitive.Decimal128:
		high, low := typed.GetBytes()
		return primitive.NewDecimal128(high, low)
	case vendorprimitive.Binary:
		return primitive.Binary{Subtype: typed.Subtype, Data: typed.Data}
	case vendorprimitive.Regex:
		return primitive.Regex{Pattern: typed.Pattern, Options: typed.Options}
	case vendorprimitive.DBPointer:
		return primitive.DBPointer{DB: typed.DB, Pointer: primitive.ObjectID(typed.Pointer)}
	case vendorprimitive.Timestamp:
		return primitive.Timestamp{T: typed.T, I: typed.I}
	case vendorprimitive.JavaScript:
		return primitive.JavaScript(typed)
	case vendorprimitive.Symbol:
		return primitive.Symbol(typed)
	case vendorprimitive.CodeWithScope:
		return primitive.CodeWithScope{Code: primitive.JavaScript(typed.Code), Scope: libraryTwin(tb, typed.Scope)}
	case vendorprimitive.Null:
		return primitive.Null{}
	case vendorprimitive.Undefined:
		return primitive.Undefined{}
	case vendorprimitive.MinKey:
		return primitive.MinKey{}
	case vendorprimitive.MaxKey:
		return primitive.MaxKey{}
	case vendorbson.Raw:
		return bson.Raw(typed)
	case vendorbson.RawValue:
		return bson.RawValue{Type: bsontype.Type(typed.Type), Value: typed.Value}
	case vendorbsoncore.Document:
		return bsoncore.Document(typed)
	case vendorbsoncore.Array:
		return bsoncore.Array(typed)
	}
	if reflect.TypeOf(vendorValue).PkgPath() == "" {
		// A composite of builtins (map[string]int, say) is the same value in
		// both libraries.
		return vendorValue
	}
	tb.Fatalf("libraryTwin: no library twin for %T", vendorValue)
	return nil
}

func sortedNames(named map[string]any) []string {
	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// appendTestString appends a BSON string value's bytes.
func appendTestString(dst []byte, text string) []byte {
	length := len(text) + 1
	dst = append(dst, byte(length), byte(length>>8), byte(length>>16), byte(length>>24))
	dst = append(dst, text...)
	return append(dst, 0)
}

// requireSortedMapLevels fails when a document level that comes from a Go
// map, or the inline map part of a struct level, is not in ascending byte
// order of its keys (O11).
func requireSortedMapLevels(t *testing.T, name string, goValue any, encoded []byte) {
	t.Helper()
	if divergence := unsortedMapLevel("$", reflect.ValueOf(goValue), encoded); divergence != "" {
		t.Errorf("%s: %s", name, divergence)
	}
}

func unsortedMapLevel(path string, goValue reflect.Value, document []byte) string {
	for goValue.IsValid() && (goValue.Kind() == reflect.Pointer || goValue.Kind() == reflect.Interface) {
		goValue = goValue.Elem()
	}
	if !goValue.IsValid() {
		return ""
	}
	elements, err := vendorbsoncore.Document(document).Elements()
	if err != nil {
		return fmt.Sprintf("%s: %v", path, err)
	}
	childOf := func(int, string) reflect.Value { return reflect.Value{} }
	sortedFrom := len(elements)
	switch goValue.Kind() {
	case reflect.Map:
		sortedFrom = 0
		if goValue.Type().Key().Kind() == reflect.String {
			childOf = func(_ int, key string) reflect.Value {
				return goValue.MapIndex(reflect.ValueOf(key).Convert(goValue.Type().Key()))
			}
		}
	case reflect.Slice, reflect.Array:
		if goValue.Type().Elem().Kind() == reflect.Struct && goValue.Type().Elem().NumField() == 2 && goValue.Type().Elem().Field(0).Name == "Key" {
			childOf = func(position int, _ string) reflect.Value { return goValue.Index(position).Field(1) }
		} else if goValue.Type().Elem().Kind() != reflect.Uint8 {
			childOf = func(position int, _ string) reflect.Value { return goValue.Index(position) }
		}
	case reflect.Struct:
		typePlan, err := libraryPlans.Lookup(goValue.Type())
		if err != nil || typePlan.Struct == nil {
			return ""
		}
		for position, documentElement := range elements {
			if _, isField := typePlan.Struct.FieldByKey(documentElement.Key()); !isField && typePlan.Struct.InlineMap != nil {
				sortedFrom = position
				break
			}
		}
		if typePlan.Struct.InlineMap != nil {
			inlineMap := goValue.Field(typePlan.Struct.InlineMapIndex)
			childOf = func(_ int, key string) reflect.Value { return inlineMap.MapIndex(reflect.ValueOf(key)) }
		}
	}
	for position := sortedFrom + 1; position < len(elements); position++ {
		if elements[position-1].Key() > elements[position].Key() {
			return fmt.Sprintf("%s: map keys %q then %q are not in ascending byte order", path, elements[position-1].Key(), elements[position].Key())
		}
	}
	for position, documentElement := range elements {
		value := documentElement.Value()
		if value.Type != vendorbson.TypeEmbeddedDocument && value.Type != vendorbson.TypeArray {
			continue
		}
		if divergence := unsortedMapLevel(path+"."+documentElement.Key(), childOf(position, documentElement.Key()), value.Data); divergence != "" {
			return divergence
		}
	}
	return ""
}
