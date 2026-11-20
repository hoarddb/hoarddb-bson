// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsoncodec "go.mongodb.org/mongo-driver/bson/bsoncodec"
	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// The plan encodes and decodes nothing. Each test here holds one of its
// decisions to what the vendor does with the same Go type: the key order and
// presence of a marshalled probe, which Go field a key's value came from or
// went to, the error text of a struct the vendor refuses, and the codec the
// vendor's default registry picks.

func newPlanCache() *plan.Cache {
	return plan.NewCache(plan.BSONTypes{
		Raw:      reflect.TypeOf(bson.Raw(nil)),
		RawValue: reflect.TypeOf(bson.RawValue{}),
	})
}

func planOf(t *testing.T, cache *plan.Cache, goType reflect.Type) *plan.Type {
	t.Helper()
	typePlan, err := cache.Lookup(goType)
	if err != nil {
		t.Fatalf("Lookup(%v): %v", goType, err)
	}
	return typePlan
}

// planFill sets every exported field reachable from target to a non-zero
// value, every string to a text no other field has, and allocates every
// pointer on the way, so a marshalled probe shows every key the plan keeps.
func planFill(target reflect.Value, counter *int) {
	planFillToDepth(target, counter, 0)
}

// planFillToDepth stops allocating pointers past a few levels, so a
// recursive probe type ends.
func planFillToDepth(target reflect.Value, counter *int, depth int) {
	*counter++
	switch target.Kind() {
	case reflect.Struct:
		for position := 0; position < target.NumField(); position++ {
			if target.Type().Field(position).PkgPath == "" {
				planFillToDepth(target.Field(position), counter, depth)
			}
		}
	case reflect.Pointer:
		if depth < 4 {
			target.Set(reflect.New(target.Type().Elem()))
			planFillToDepth(target.Elem(), counter, depth+1)
		}
	case reflect.String:
		target.SetString(fmt.Sprintf("value-%d", *counter))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		target.SetInt(int64(*counter % 100))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		target.SetUint(uint64(*counter % 100))
	case reflect.Float32, reflect.Float64:
		target.SetFloat(float64(*counter) + 0.5)
	case reflect.Bool:
		target.SetBool(true)
	case reflect.Slice:
		target.Set(reflect.MakeSlice(target.Type(), 1, 1))
		planFillToDepth(target.Index(0), counter, depth)
	case reflect.Array:
		for position := 0; position < target.Len(); position++ {
			planFillToDepth(target.Index(position), counter, depth)
		}
	case reflect.Map:
		target.Set(reflect.MakeMap(target.Type()))
		mapKey := reflect.New(target.Type().Key()).Elem()
		planFillToDepth(mapKey, counter, depth)
		mapValue := reflect.New(target.Type().Elem()).Elem()
		planFillToDepth(mapValue, counter, depth)
		target.SetMapIndex(mapKey, mapValue)
	case reflect.Interface:
		if target.NumMethod() == 0 {
			target.Set(reflect.ValueOf(fmt.Sprintf("value-%d", *counter)))
		}
	}
}

// planValueIsEmpty says whether omitempty drops a value, for the builtin
// kinds the probes use: a struct is never empty, an interface or pointer only
// when nil, a string, slice, map or array when it has length 0.
func planValueIsEmpty(fieldValue reflect.Value) bool {
	switch fieldValue.Kind() {
	case reflect.Struct:
		return false
	case reflect.Interface, reflect.Pointer:
		return fieldValue.IsNil()
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return fieldValue.Len() == 0
	}
	return fieldValue.IsZero()
}

func vendorElementsOf(t *testing.T, encoded []byte) []vendorbson.RawElement {
	t.Helper()
	elements, err := vendorbson.Raw(encoded).Elements()
	if err != nil {
		t.Fatalf("reading the vendor's bytes: %v", err)
	}
	return elements
}

// planStructFieldAt walks a plan field's index path through the struct
// types, as the vendor's inline index does.
func planStructFieldAt(goType reflect.Type, index []int) reflect.StructField {
	var structField reflect.StructField
	for _, position := range index {
		if goType.Kind() == reflect.Pointer {
			goType = goType.Elem()
		}
		structField = goType.Field(position)
		goType = structField.Type
	}
	return structField
}

// comparePlanEncodesLikeTheVendor marshals a filled and a zero value of the
// probe with the vendor and holds the plan's layout to both: the keys in
// order, the Go field each string value came from, the omitempty and
// through-pointer decisions, minsize on int64 fields, and the tag flags of
// every field. A struct the plan refuses must be refused by the vendor with
// the same text, on encode and on decode.
func comparePlanEncodesLikeTheVendor(t *testing.T, cache *plan.Cache, probeType reflect.Type) {
	t.Helper()
	typePlan := planOf(t, cache, probeType)
	counter := 0
	filled := reflect.New(probeType).Elem()
	planFill(filled, &counter)
	encoded, vendorErr := vendorbson.Marshal(filled.Interface())
	if typePlan.Err != nil {
		if vendorErr == nil {
			t.Fatalf("the plan refuses %v (%v); the vendor marshals it", probeType, typePlan.Err)
		}
		if vendorErr.Error() != typePlan.Err.Error() {
			t.Errorf("refusal text of %v:\n plan   %q\n vendor %q", probeType, typePlan.Err, vendorErr)
		}
		decodeErr := vendorbson.Unmarshal([]byte{5, 0, 0, 0, 0}, reflect.New(probeType).Interface())
		if decodeErr == nil || decodeErr.Error() != typePlan.Err.Error() {
			t.Errorf("decode refusal text of %v:\n plan   %q\n vendor %v", probeType, typePlan.Err, decodeErr)
		}
		return
	}
	if typePlan.Struct == nil {
		t.Fatalf("%v: no layout and no error", probeType)
	}
	if vendorErr != nil {
		t.Fatalf("the vendor refuses %v (%v); the plan has a layout", probeType, vendorErr)
	}

	planKeys := []string{}
	var keptFields []plan.Field
	for _, field := range typePlan.Struct.Fields {
		if field.OmitEmpty && planValueIsEmpty(filled.FieldByIndex(field.Index)) {
			continue
		}
		planKeys = append(planKeys, field.Key)
		keptFields = append(keptFields, field)
	}
	if typePlan.Struct.InlineMap != nil {
		inlineMap := filled.Field(typePlan.Struct.InlineMapIndex)
		for _, mapKey := range inlineMap.MapKeys() {
			planKeys = append(planKeys, mapKey.String())
		}
	}
	vendorKeys := []string{}
	elements := vendorElementsOf(t, encoded)
	for _, element := range elements {
		vendorKeys = append(vendorKeys, element.Key())
	}
	if strings.Join(planKeys, ",") != strings.Join(vendorKeys, ",") {
		t.Fatalf("keys of a filled %v:\n plan   %q\n vendor %q", probeType, planKeys, vendorKeys)
	}

	for position, field := range keptFields {
		fieldValue := filled.FieldByIndex(field.Index)
		vendorValue := elements[position].Value()
		if fieldValue.Kind() == reflect.String && vendorValue.StringValue() != fieldValue.String() {
			t.Errorf("%v key %q: the plan reads field %v (%q); the vendor wrote %q",
				probeType, field.Key, field.Index, fieldValue.String(), vendorValue.StringValue())
		}
		if fieldValue.Kind() == reflect.Int64 {
			wantType := vendorbson.TypeInt64
			if field.MinSize {
				wantType = vendorbson.TypeInt32
			}
			if vendorValue.Type != wantType {
				t.Errorf("%v key %q: plan MinSize %t; the vendor wrote %v", probeType, field.Key, field.MinSize, vendorValue.Type)
			}
		}
		vendorTags, err := vendorbsoncodec.DefaultStructTagParser(planStructFieldAt(probeType, field.Index)) //nolint:staticcheck // the parser the vendor's struct codec uses
		if err != nil {
			t.Fatalf("vendor tag parser: %v", err)
		}
		if vendorTags.Name != field.Key || vendorTags.OmitEmpty != field.OmitEmpty ||
			vendorTags.MinSize != field.MinSize || vendorTags.Truncate != field.Truncate {
			t.Errorf("%v key %q: plan flags %+v; the vendor's tags %+v", probeType, field.Key, field, vendorTags)
		}
	}

	zeroEncoded, err := vendorbson.Marshal(reflect.Zero(probeType).Interface())
	if err != nil {
		t.Fatalf("the vendor refuses a zero %v: %v", probeType, err)
	}
	wantZeroKeys := []string{}
	zero := reflect.Zero(probeType)
	for _, field := range typePlan.Struct.Fields {
		if field.ThroughPointer || (field.OmitEmpty && planValueIsEmpty(zero.FieldByIndex(field.Index))) {
			continue
		}
		wantZeroKeys = append(wantZeroKeys, field.Key)
	}
	zeroKeys := []string{}
	for _, element := range vendorElementsOf(t, zeroEncoded) {
		zeroKeys = append(zeroKeys, element.Key())
	}
	if strings.Join(wantZeroKeys, ",") != strings.Join(zeroKeys, ",") {
		t.Errorf("keys of a zero %v:\n plan   %q\n vendor %q", probeType, wantZeroKeys, zeroKeys)
	}
}

// Row N1: unexported fields are skipped, an unexported embedded struct too,
// even when tagged inline. Row N2: an untagged field takes its lower-cased
// Go name. Row N3: a json tag is ignored.
type namingEmbedded struct {
	Inside string
}

type NamingEmbeddedPointer struct {
	Pointed string
}

type namingHidden struct {
	Hidden string
}

type NamingExported struct {
	Exported string
}

type namingProbe struct {
	UserName       string
	ID             int
	HTTPServer     string
	Ünïcode        string
	lowerHidden    string //nolint:unused // an unexported field the plan skips (row N1)
	Custom         string `bson:"custom_key"`
	Skipped        string `bson:"-"`
	Dash           string `bson:"-,omitempty"`
	Empty          string `bson:""`
	OmitMe         string `bson:",omitempty"`
	JSONOnly       string `json:"json_only,omitempty"`
	JSONDash       string `json:"-"`
	Both           string `bson:"both_bson" json:"both_json"`
	Count64        int64  `bson:",minsize"`
	Plain64        int64
	Ratio          float64 `bson:",truncate"`
	NamingExported         // embedded without inline: a sub-document
	*NamingEmbeddedPointer
	namingHidden   `bson:",inline"`
	namingEmbedded                // unexported embedded: skipped
	Nested         NamingExported `bson:",omitempty"`
	Data           map[string]interface{}
	Any            interface{} `bson:",omitempty"`
	Fixed          [2]int      `bson:",omitempty"`
	NoLength       [0]int      `bson:",omitempty"`
	List           []string    `bson:",omitempty"`
	Pointer        *string     `bson:",omitempty"`
	Flag           bool        `bson:",omitempty"`
	Unsigned       uint32      `bson:",omitempty"`
}

// insertRequestShape is server.InsertRequest (hoardDB server/proto.go) with
// builtin types only. Its zero value encodes "writeconcern" and "key": "".
type insertRequestShape struct {
	Database     string                 `json:"database"`
	Bucket       string                 `json:"bucket"`
	Key          string                 `json:"key,omitempty"`
	Data         map[string]interface{} `json:"data"`
	WriteConcern string                 `json:"write_concern,omitempty"`
	Admitted     bool                   `bson:"admitted,omitempty" json:"admitted,omitempty"`
	RepairBound  uint64                 `bson:"repair_bound,omitempty" json:"repair_bound,omitempty"`
	RepairRead   *repairReadShape       `bson:"repair_read,omitempty" json:"repair_read,omitempty"`
}

type repairReadShape struct {
	Owner       string `bson:"owner"`
	Incarnation uint64 `bson:"incarnation"`
	Term        uint64 `bson:"term"`
	Through     uint64 `bson:"through"`
}

func TestPlanNamesFieldsLikeTheVendor(t *testing.T) {
	cache := newPlanCache()
	for _, probe := range []any{namingProbe{}, insertRequestShape{}, repairReadShape{}, NamingExported{}} {
		probeType := reflect.TypeOf(probe)
		t.Run(probeType.String(), func(t *testing.T) {
			comparePlanEncodesLikeTheVendor(t, cache, probeType)
		})
	}
	t.Run("server.InsertRequest keys", func(t *testing.T) {
		insertPlan := planOf(t, cache, reflect.TypeOf(insertRequestShape{}))
		got := []string{}
		for _, field := range insertPlan.Struct.Fields {
			if !field.OmitEmpty {
				got = append(got, field.Key)
			}
		}
		want := "database,bucket,key,data,writeconcern"
		if strings.Join(got, ",") != want {
			t.Errorf("keys a zero InsertRequest encodes: %q, want %q", got, want)
		}
	})
}

// Row L1. The probes inline at several depths, through values and
// pointers, with duplicated keys resolved by depth or refused.
type DeepLevel struct {
	Shared   string
	DeepOnly string
}

type MiddleLevel struct {
	Shared     string
	MiddleOnly string
	DeepLevel  `bson:",inline"`
}

type dominanceProbe struct {
	First       string
	MiddleLevel `bson:",inline"`
	Shared      string
	Last        string
}

type BranchA struct {
	X     string
	OnlyA string
}

type BranchB struct {
	X     string
	OnlyB string
}

type duplicateAtSameDepth struct {
	BranchA `bson:",inline"`
	BranchB `bson:",inline"`
}

type duplicateResolvedByShallower struct {
	BranchA `bson:",inline"`
	BranchB `bson:",inline"`
	X       string
}

type duplicateTopLevel struct {
	A string `bson:"same"`
	B string `bson:"same"`
}

type taggedInner struct {
	Label string `bson:"name"`
	Other string
}

type taggedDeeperLoses struct {
	Inner taggedInner `bson:",inline"`
	Name  string
}

type pathThroughDeep struct {
	Deep DeepLevel `bson:",inline"`
}

type pathShallow struct {
	Shared string
}

type deeperBranches struct {
	P pathThroughDeep `bson:",inline"`
	Q pathShallow     `bson:",inline"`
}

type inlinePointerProbe struct {
	Before       string
	*MiddleLevel `bson:",inline"`
	After        string
}

type inlineOrderProbe struct {
	A   string
	Mid MiddleLevel `bson:",inline"`
	Z   string
}

type InlineMapHolder struct {
	Name  string
	Extra map[string]string `bson:",inline"`
}

type nestedInlineMap struct {
	Top    string
	Holder InlineMapHolder `bson:",inline"`
}

type twoInlineMaps struct {
	A map[string]string `bson:",inline"`
	B map[string]string `bson:",inline"`
}

type planKeyName string

type namedKeyInlineMap struct {
	A map[planKeyName]string `bson:",inline"`
}

type intKeyInlineMap struct {
	A map[int]string `bson:",inline"`
}

type inlineString struct {
	S string `bson:",inline"`
}

type inlinePointerToString struct {
	S *string `bson:",inline"`
}

type inlineInterface struct {
	I interface{} `bson:",inline"`
}

type inlinePointerToMap struct {
	M *map[string]string `bson:",inline"`
}

type inlineBadInner struct {
	Name  string
	Inner duplicateAtSameDepth `bson:",inline"`
}

type dashNamedInline struct {
	Name string
	S    string `bson:"-,inline"`
}

type holdsBadInnerByPointer struct {
	Name  string
	Inner *duplicateAtSameDepth
}

// ValueThenPointer is inlined by value and inlines DeepLevel through a
// pointer, so the pointer is one level down the inline path.
type ValueThenPointer struct {
	B *DeepLevel `bson:",inline"`
	Y string
}

type throughValueThenPointer struct {
	Q ValueThenPointer `bson:",inline"`
	Z string
}

func TestPlanInlineDominance(t *testing.T) {
	cache := newPlanCache()
	for _, probe := range []any{
		dominanceProbe{}, duplicateAtSameDepth{}, duplicateResolvedByShallower{}, duplicateTopLevel{},
		taggedDeeperLoses{}, deeperBranches{}, inlinePointerProbe{}, inlineOrderProbe{},
		InlineMapHolder{}, nestedInlineMap{}, twoInlineMaps{}, namedKeyInlineMap{}, intKeyInlineMap{},
		inlineString{}, inlinePointerToString{}, inlineInterface{}, inlinePointerToMap{}, inlineBadInner{},
		dashNamedInline{}, MiddleLevel{}, throughValueThenPointer{},
	} {
		probeType := reflect.TypeOf(probe)
		t.Run(probeType.String(), func(t *testing.T) {
			comparePlanEncodesLikeTheVendor(t, cache, probeType)
		})
	}
	t.Run("a bad type reached through a pointer fails only when reached", func(t *testing.T) {
		holderPlan := planOf(t, cache, reflect.TypeOf(holdsBadInnerByPointer{}))
		if holderPlan.Err != nil {
			t.Fatalf("the holder got the inner type's error: %v", holderPlan.Err)
		}
		if _, err := vendorbson.Marshal(holdsBadInnerByPointer{}); err != nil {
			t.Fatalf("the vendor refuses the holder with a nil pointer: %v", err)
		}
		innerErr := holderPlan.Struct.Fields[1].Type.Elem.Err
		_, vendorErr := vendorbson.Marshal(holdsBadInnerByPointer{Inner: &duplicateAtSameDepth{}})
		if innerErr == nil || vendorErr == nil || innerErr.Error() != vendorErr.Error() {
			t.Errorf("inner refusal: plan %v, vendor %v", innerErr, vendorErr)
		}
	})
	t.Run("inlined fields record the pointer on their path", func(t *testing.T) {
		pointerPlan := planOf(t, cache, reflect.TypeOf(inlinePointerProbe{}))
		for _, field := range pointerPlan.Struct.Fields {
			wantThrough := len(field.Index) > 1
			if field.ThroughPointer != wantThrough {
				t.Errorf("field %q at %v: ThroughPointer %t", field.Key, field.Index, field.ThroughPointer)
			}
		}
		// The pointer is one level down: Q is a value, Q.B a pointer.
		deeperPlan := planOf(t, cache, reflect.TypeOf(throughValueThenPointer{}))
		for _, field := range deeperPlan.Struct.Fields {
			wantThrough := len(field.Index) == 3
			if field.ThroughPointer != wantThrough {
				t.Errorf("throughValueThenPointer field %q at %v: ThroughPointer %t", field.Key, field.Index, field.ThroughPointer)
			}
		}
	})
}

// Rows N4 and N5: the decoder looks a key up exactly, then lower-cased, and
// skips a key no field has (or hands it to the inline map).
type lookupProbe struct {
	Name   string
	Upper  string `bson:"Upper"`
	Lower  string `bson:"upper"`
	UserID string
	Ünï    string
	Inner  MiddleLevel `bson:",inline"`
}

type lookupWithInlineMap struct {
	Name  string
	Extra map[string]string `bson:",inline"`
}

func TestPlanFieldLookupMatchesTheVendor(t *testing.T) {
	cache := newPlanCache()
	const marker = "decoded-marker"
	keys := []string{"name", "Name", "NAME", "Upper", "upper", "UPPER", "uPPer", "userid", "UserID", "USERID",
		"ünï", "ÜNÏ", "shared", "SHARED", "middleonly", "deeponly", "DeepOnly", "missing", "", "Missing"}
	for _, probe := range []any{lookupProbe{}, lookupWithInlineMap{}} {
		probeType := reflect.TypeOf(probe)
		probePlan := planOf(t, cache, probeType)
		for _, key := range keys {
			document, err := vendorbson.Marshal(vendorbson.D{{Key: key, Value: marker}})
			if err != nil {
				t.Fatalf("building the input: %v", err)
			}
			target := reflect.New(probeType)
			if err := vendorbson.Unmarshal(document, target.Interface()); err != nil {
				t.Fatalf("%v key %q: the vendor refuses the decode: %v", probeType, key, err)
			}
			var vendorIndex []int
			for _, index := range planStringFieldPaths(probeType, nil) {
				if target.Elem().FieldByIndex(index).String() == marker {
					vendorIndex = index
				}
			}
			field, found := probePlan.Struct.FieldByKey(key)
			switch {
			case found && vendorIndex == nil:
				t.Errorf("%v key %q: the plan finds field %v; the vendor set no field", probeType, key, field.Index)
			case !found && vendorIndex != nil:
				t.Errorf("%v key %q: the plan finds nothing; the vendor set field %v", probeType, key, vendorIndex)
			case found && fmt.Sprint(field.Index) != fmt.Sprint(vendorIndex):
				t.Errorf("%v key %q: the plan finds field %v; the vendor set %v", probeType, key, field.Index, vendorIndex)
			}
			if !found && probePlan.Struct.InlineMap != nil {
				extra := target.Elem().Field(probePlan.Struct.InlineMapIndex)
				if extra.MapIndex(reflect.ValueOf(key)).String() != marker {
					t.Errorf("%v key %q: no field, and the vendor did not put it in the inline map", probeType, key)
				}
			}
		}
	}
}

// planStringFieldPaths lists the index path of every exported string field
// of a struct type, inside nested struct fields too.
func planStringFieldPaths(goType reflect.Type, prefix []int) [][]int {
	var paths [][]int
	for position := 0; position < goType.NumField(); position++ {
		structField := goType.Field(position)
		if structField.PkgPath != "" {
			continue
		}
		index := append(append([]int{}, prefix...), position)
		switch structField.Type.Kind() {
		case reflect.String:
			paths = append(paths, index)
		case reflect.Struct:
			paths = append(paths, planStringFieldPaths(structField.Type, index)...)
		}
	}
	return paths
}

// vendorCodecName names the codec the vendor's registry returned: the
// method of a function codec, the type of a codec value, and both halves of
// a codec chosen by addressability.
func vendorCodecName(codec reflect.Value) string {
	for codec.Kind() == reflect.Interface {
		codec = codec.Elem()
	}
	if !codec.IsValid() || (codec.Kind() == reflect.Pointer && codec.IsNil()) {
		return "<none>"
	}
	switch {
	case codec.Kind() == reflect.Func:
		fullName := runtime.FuncForPC(codec.Pointer()).Name()
		return strings.TrimSuffix(fullName[strings.LastIndex(fullName, ".")+1:], "-fm")
	case codec.Kind() == reflect.Struct && codec.Type().Name() == "decodeAdapter":
		return vendorCodecName(codec.Field(0))
	case codec.Kind() == reflect.Pointer && strings.HasPrefix(codec.Type().Elem().Name(), "condAddr"):
		return "condAddr(" + vendorCodecName(codec.Elem().Field(0)) + "," + vendorCodecName(codec.Elem().Field(1)) + ")"
	}
	typeName := codec.Type().String()
	return typeName[strings.LastIndex(typeName, ".")+1:]
}

// planCodecNames gives the vendor's name for each plan codec; {dir} is
// Encode or Decode.
var planCodecNames = map[plan.Codec]string{
	plan.NoCodec:              "<none>",
	plan.BooleanCodec:         "Boolean{dir}Value",
	plan.IntCodec:             "Int{dir}Value",
	plan.UintCodec:            "UIntCodec",
	plan.FloatCodec:           "Float{dir}Value",
	plan.StringCodec:          "StringCodec",
	plan.ArrayCodec:           "Array{dir}Value",
	plan.SliceCodec:           "SliceCodec",
	plan.MapCodec:             "MapCodec",
	plan.StructCodec:          "StructCodec",
	plan.PointerCodec:         "PointerCodec",
	plan.EmptyInterfaceCodec:  "EmptyInterfaceCodec",
	plan.ByteSliceCodec:       "ByteSliceCodec",
	plan.TimeCodec:            "TimeCodec",
	plan.CoreArrayCodec:       "ArrayCodec",
	plan.CoreDocumentCodec:    "CoreDocument{dir}Value",
	plan.ObjectIDCodec:        "ObjectID{dir}Value",
	plan.Decimal128Codec:      "Decimal128{dir}Value",
	plan.JSONNumberCodec:      "JSONNumber{dir}Value",
	plan.URLCodec:             "URL{dir}Value",
	plan.JavaScriptCodec:      "JavaScript{dir}Value",
	plan.SymbolCodec:          "Symbol{dir}Value",
	plan.BinaryCodec:          "Binary{dir}Value",
	plan.UndefinedCodec:       "Undefined{dir}Value",
	plan.DateTimeCodec:        "DateTime{dir}Value",
	plan.NullCodec:            "Null{dir}Value",
	plan.RegexCodec:           "Regex{dir}Value",
	plan.DBPointerCodec:       "DBPointer{dir}Value",
	plan.TimestampCodec:       "Timestamp{dir}Value",
	plan.MinKeyCodec:          "MinKey{dir}Value",
	plan.MaxKeyCodec:          "MaxKey{dir}Value",
	plan.CodeWithScopeCodec:   "CodeWithScope{dir}Value",
	plan.RawCodec:             "Raw{dir}Value",
	plan.RawValueCodec:        "RawValue{dir}Value",
	plan.DCodec:               "D{dir}Value",
	plan.ValueMarshalerHook:   "ValueMarshaler{dir}Value",
	plan.MarshalerHook:        "Marshaler{dir}Value",
	plan.ProxyHook:            "Proxy{dir}Value",
	plan.ValueUnmarshalerHook: "ValueUnmarshaler{dir}Value",
	plan.UnmarshalerHook:      "Unmarshaler{dir}Value",
}

func planChoiceName(t *testing.T, choice plan.Choice, direction string) string {
	t.Helper()
	nameOf := func(codec plan.Codec) string {
		name, known := planCodecNames[codec]
		if !known {
			t.Fatalf("codec %d has no vendor name in the test's table", codec)
		}
		return strings.ReplaceAll(name, "{dir}", direction)
	}
	if choice.AddressedOnly {
		return "condAddr(" + nameOf(choice.Codec) + "," + nameOf(choice.Otherwise) + ")"
	}
	return nameOf(choice.Codec)
}

// Row H1's shapes: hooks on the value or only on the pointer receiver, and
// the combinations whose fallback the vendor picks by its hook order.
type valueHookMarshaler struct{}

func (valueHookMarshaler) MarshalBSON() ([]byte, error) { return nil, nil }

type pointerHookMarshaler struct{}

func (*pointerHookMarshaler) MarshalBSON() ([]byte, error) { return nil, nil }

type valueHookProxy struct{}

func (valueHookProxy) ProxyBSON() (interface{}, error) { return nil, nil }

type pointerHookProxy struct{}

func (*pointerHookProxy) ProxyBSON() (interface{}, error) { return nil, nil }

type proxyThenPointerMarshaler struct{}

func (proxyThenPointerMarshaler) ProxyBSON() (interface{}, error) { return nil, nil }
func (*proxyThenPointerMarshaler) MarshalBSON() ([]byte, error)   { return nil, nil }

type libValueMarshaler struct{}

func (libValueMarshaler) MarshalBSONValue() (bsontype.Type, []byte, error) { return 0, nil, nil }

type vendorValueMarshaler struct{}

func (vendorValueMarshaler) MarshalBSONValue() (vendorbsontype.Type, []byte, error) {
	return 0, nil, nil
}

type libPointerValueMarshalerAndMarshaler struct{}

func (*libPointerValueMarshalerAndMarshaler) MarshalBSONValue() (bsontype.Type, []byte, error) {
	return 0, nil, nil
}
func (libPointerValueMarshalerAndMarshaler) MarshalBSON() ([]byte, error) { return nil, nil }

type vendorPointerValueMarshalerAndMarshaler struct{}

func (*vendorPointerValueMarshalerAndMarshaler) MarshalBSONValue() (vendorbsontype.Type, []byte, error) {
	return 0, nil, nil
}
func (vendorPointerValueMarshalerAndMarshaler) MarshalBSON() ([]byte, error) { return nil, nil }

type valueHookUnmarshaler struct{}

func (valueHookUnmarshaler) UnmarshalBSON([]byte) error { return nil }

type pointerHookUnmarshaler struct{ Field string }

func (*pointerHookUnmarshaler) UnmarshalBSON([]byte) error { return nil }

type libPointerValueUnmarshaler struct{}

func (*libPointerValueUnmarshaler) UnmarshalBSONValue(bsontype.Type, []byte) error { return nil }

type vendorPointerValueUnmarshaler struct{}

func (*vendorPointerValueUnmarshaler) UnmarshalBSONValue(vendorbsontype.Type, []byte) error {
	return nil
}

// Both decode hooks on the value: the vendor's order picks
// ValueUnmarshaler.
type libBothDecodeHooks struct{}

func (libBothDecodeHooks) UnmarshalBSONValue(bsontype.Type, []byte) error { return nil }
func (libBothDecodeHooks) UnmarshalBSON([]byte) error                     { return nil }

type vendorBothDecodeHooks struct{}

func (vendorBothDecodeHooks) UnmarshalBSONValue(vendorbsontype.Type, []byte) error { return nil }
func (vendorBothDecodeHooks) UnmarshalBSON([]byte) error                           { return nil }

type hookedFunc func()

func (*hookedFunc) MarshalBSON() ([]byte, error) { return nil, nil }

type hookedInt int

func (*hookedInt) UnmarshalBSON([]byte) error { return nil }

type marshalerInterface interface {
	MarshalBSON() ([]byte, error)
}

type planNamedBytes []byte

type planNamedString string

type planByte byte

type planNamedTime time.Time

type libNamedObjectID primitive.ObjectID

type vendorNamedObjectID vendorprimitive.ObjectID

type libNamedD []primitive.E

type vendorNamedD []vendorprimitive.E

// planTypePairs pairs each library type with the vendor type it stands for.
// A type built from builtins only is its own pair.
func planTypePairs() [][2]reflect.Type {
	same := func(sample any) [2]reflect.Type {
		goType := reflect.TypeOf(sample)
		return [2]reflect.Type{goType, goType}
	}
	sameType := func(goType reflect.Type) [2]reflect.Type { return [2]reflect.Type{goType, goType} }
	pair := func(library, vendor any) [2]reflect.Type {
		return [2]reflect.Type{reflect.TypeOf(library), reflect.TypeOf(vendor)}
	}
	return [][2]reflect.Type{
		same(true), same(0), same(int8(0)), same(int16(0)), same(int32(0)), same(int64(0)),
		same(uint(0)), same(uint8(0)), same(uint16(0)), same(uint32(0)), same(uint64(0)), same(uintptr(0)),
		same(float32(0)), same(float64(0)), same(complex64(0)), same(complex128(0)), same(""),
		same([3]int{}), same([16]byte{}), same([0]byte{}), same([]int{}), same([]byte{}), same(planNamedBytes{}),
		same([]uint8{}), same([]planByte{}), same([4]planByte{}), same(map[string]int{}), same(map[int]string{}), same(struct{}{}), same(new(int)), same(new(*int)),
		sameType(reflect.TypeOf((*interface{})(nil)).Elem()), sameType(reflect.TypeOf((*error)(nil)).Elem()),
		sameType(reflect.TypeOf((*marshalerInterface)(nil)).Elem()),
		same(make(chan int)), same(func() {}), same(unsafe.Pointer(nil)), same(planNamedString("")),
		same(time.Time{}), same(&time.Time{}), same(planNamedTime{}), same([]time.Time{}),
		same(json.Number("")), same(url.URL{}), same(&url.URL{}),
		same(valueHookMarshaler{}), same(&valueHookMarshaler{}), same(pointerHookMarshaler{}), same(&pointerHookMarshaler{}),
		same(valueHookProxy{}), same(pointerHookProxy{}), same(&pointerHookProxy{}), same(proxyThenPointerMarshaler{}),
		same(valueHookUnmarshaler{}), same(pointerHookUnmarshaler{}), same(&pointerHookUnmarshaler{}),
		same(hookedFunc(nil)), same(new(hookedFunc)), same(hookedInt(0)), same(new(hookedInt)),
		pair(libValueMarshaler{}, vendorValueMarshaler{}),
		pair(&libValueMarshaler{}, &vendorValueMarshaler{}),
		pair(libPointerValueMarshalerAndMarshaler{}, vendorPointerValueMarshalerAndMarshaler{}),
		pair(&libPointerValueMarshalerAndMarshaler{}, &vendorPointerValueMarshalerAndMarshaler{}),
		pair(libPointerValueUnmarshaler{}, vendorPointerValueUnmarshaler{}),
		pair(libBothDecodeHooks{}, vendorBothDecodeHooks{}),
		pair(&libPointerValueUnmarshaler{}, &vendorPointerValueUnmarshaler{}),
		pair(primitive.Binary{}, vendorprimitive.Binary{}),
		pair(primitive.Undefined{}, vendorprimitive.Undefined{}),
		pair(primitive.ObjectID{}, vendorprimitive.ObjectID{}),
		pair(primitive.DateTime(0), vendorprimitive.DateTime(0)),
		pair(primitive.Null{}, vendorprimitive.Null{}),
		pair(primitive.Regex{}, vendorprimitive.Regex{}),
		pair(primitive.CodeWithScope{}, vendorprimitive.CodeWithScope{}),
		pair(primitive.DBPointer{}, vendorprimitive.DBPointer{}),
		pair(primitive.JavaScript(""), vendorprimitive.JavaScript("")),
		pair(primitive.Symbol(""), vendorprimitive.Symbol("")),
		pair(primitive.Timestamp{}, vendorprimitive.Timestamp{}),
		pair(primitive.Decimal128{}, vendorprimitive.Decimal128{}),
		pair(primitive.MinKey{}, vendorprimitive.MinKey{}),
		pair(primitive.MaxKey{}, vendorprimitive.MaxKey{}),
		pair(primitive.D{}, vendorprimitive.D{}),
		pair(primitive.E{}, vendorprimitive.E{}),
		pair(primitive.M{}, vendorprimitive.M{}),
		pair(primitive.A{}, vendorprimitive.A{}),
		pair([]primitive.E{}, []vendorprimitive.E{}),
		pair([2]primitive.E{}, [2]vendorprimitive.E{}),
		pair(libNamedD{}, vendorNamedD{}),
		pair(&primitive.ObjectID{}, &vendorprimitive.ObjectID{}),
		pair([]primitive.ObjectID{}, []vendorprimitive.ObjectID{}),
		pair(libNamedObjectID{}, vendorNamedObjectID{}),
		pair(bson.Raw{}, vendorbson.Raw{}),
		pair(bson.RawValue{}, vendorbson.RawValue{}),
		pair(bson.RawElement{}, vendorbson.RawElement{}),
		pair(&bson.RawValue{}, &vendorbson.RawValue{}),
		pair(bsoncore.Document{}, vendorbsoncore.Document{}),
		pair(bsoncore.Array{}, vendorbsoncore.Array{}),
		pair(bsoncore.Value{}, vendorbsoncore.Value{}),
		pair(bsoncore.Element{}, vendorbsoncore.Element{}),
	}
}

// Rows H1, F1, P1, T1, T2 and B1, the codec half: the codec the vendor's
// default registry picks for each type, with the addressability rule of
// a hook found only on the pointer receiver; and the per-type flags the
// encoders read instead of asking reflect on every value.
func TestPlanChoosesTheVendorsCodecs(t *testing.T) {
	cache := newPlanCache()
	vendorZeroer := reflect.TypeOf((*vendorbsoncodec.Zeroer)(nil)).Elem()
	vendorE := reflect.TypeOf(vendorprimitive.E{})
	vendorD := reflect.TypeOf(vendorprimitive.D{})
	byteType := reflect.TypeOf(byte(0))
	// NewRegistry builds the registry bson.Marshal and bson.Unmarshal use
	// (DefaultRegistry is a NewRegistry).
	vendorRegistry := vendorbson.NewRegistry()
	for _, typePair := range planTypePairs() {
		libraryType, vendorType := typePair[0], typePair[1]
		typePlan := planOf(t, cache, libraryType)
		vendorEncoder, _ := vendorRegistry.LookupEncoder(vendorType)
		vendorDecoder, _ := vendorRegistry.LookupDecoder(vendorType)
		if got, want := planChoiceName(t, typePlan.Encode, "Encode"), vendorCodecName(reflect.ValueOf(&vendorEncoder).Elem()); got != want {
			t.Errorf("%v encode: plan %s, vendor %s", libraryType, got, want)
		}
		if got, want := planChoiceName(t, typePlan.Decode, "Decode"), vendorCodecName(reflect.ValueOf(&vendorDecoder).Elem()); got != want {
			t.Errorf("%v decode: plan %s, vendor %s", libraryType, got, want)
		}
		if want := vendorType.Implements(vendorZeroer); typePlan.Zeroer != want {
			t.Errorf("%v: plan Zeroer %t, the vendor's Zeroer check %t", libraryType, typePlan.Zeroer, want)
		}
		listKind := vendorType.Kind() == reflect.Slice || vendorType.Kind() == reflect.Array
		if want := listKind && vendorType.Elem() == byteType; typePlan.ElemIsByte != want {
			t.Errorf("%v: plan ElemIsByte %t, want %t", libraryType, typePlan.ElemIsByte, want)
		}
		if want := listKind && vendorType.Elem() == vendorE; typePlan.ElemIsE != want {
			t.Errorf("%v: plan ElemIsE %t, want %t", libraryType, typePlan.ElemIsE, want)
		}
		if want := vendorType.Kind() == reflect.Slice && vendorType.ConvertibleTo(vendorD); typePlan.ConvertibleToD != want {
			t.Errorf("%v: plan ConvertibleToD %t, want %t", libraryType, typePlan.ConvertibleToD, want)
		}
	}
}

// Map keys (row F1): which of the vendor's key rules applies to a key type,
// observed by marshalling a one-entry map and decoding a one-key document.
type planTextKey int

func (planTextKey) MarshalText() ([]byte, error) { return []byte("marshal-text"), nil }
func (key *planTextKey) UnmarshalText([]byte) error {
	*key = 42
	return nil
}

type planHookKey int

func (planHookKey) MarshalKey() (string, error) { return "marshal-key", nil }
func (key *planHookKey) UnmarshalKey(string) error {
	*key = 43
	return nil
}

type planBothKey int

func (planBothKey) MarshalKey() (string, error)  { return "marshal-key", nil }
func (planBothKey) MarshalText() ([]byte, error) { return []byte("marshal-text"), nil }
func (key *planBothKey) UnmarshalKey(string) error {
	*key = 43
	return nil
}
func (key *planBothKey) UnmarshalText([]byte) error {
	*key = 42
	return nil
}

type planStringTextKey string

func (planStringTextKey) MarshalText() ([]byte, error) { return []byte("marshal-text"), nil }
func (key *planStringTextKey) UnmarshalText([]byte) error {
	*key = "from-text"
	return nil
}

type planPointerTextKey struct{ Number int }

func (*planPointerTextKey) MarshalText() ([]byte, error) { return []byte("marshal-text"), nil }

// planEncodedKey is what the plan's key rule says the vendor writes for a
// key, or the error it says the vendor returns.
func planEncodedKey(rule plan.KeyCodec, mapKey reflect.Value) (string, bool) {
	keyHook := func(candidate reflect.Value) (string, bool) {
		if marshaler, ok := candidate.Interface().(interface{ MarshalKey() (string, error) }); ok {
			text, err := marshaler.MarshalKey()
			return text, err == nil
		}
		if marshaler, ok := candidate.Interface().(interface{ MarshalText() ([]byte, error) }); ok {
			text, err := marshaler.MarshalText()
			return string(text), err == nil
		}
		return "", false
	}
	switch rule {
	case plan.StringKey:
		return mapKey.String(), true
	case plan.IntKey:
		return strconv.FormatInt(mapKey.Int(), 10), true
	case plan.UintKey:
		return strconv.FormatUint(mapKey.Uint(), 10), true
	case plan.KeyHookKey:
		text, err := mapKey.Interface().(interface{ MarshalKey() (string, error) }).MarshalKey()
		return text, err == nil
	case plan.TextHookKey:
		text, err := mapKey.Interface().(interface{ MarshalText() ([]byte, error) }).MarshalText()
		return string(text), err == nil
	case plan.DynamicKey:
		return keyHook(mapKey.Elem())
	}
	return "", false
}

// planDecodedKey is the key the plan's rule says the vendor decodes "7" to.
func planDecodedKey(rule plan.KeyCodec, keyType reflect.Type) (reflect.Value, bool) {
	switch rule {
	case plan.StringKey:
		return reflect.ValueOf("7").Convert(keyType), true
	case plan.IntKey:
		return reflect.ValueOf(int64(7)).Convert(keyType), true
	case plan.UintKey:
		return reflect.ValueOf(uint64(7)).Convert(keyType), true
	case plan.KeyHookKey:
		decoded := reflect.New(keyType)
		err := decoded.Interface().(interface{ UnmarshalKey(string) error }).UnmarshalKey("7")
		return decoded.Elem(), err == nil
	case plan.TextHookKey:
		decoded := reflect.New(keyType)
		err := decoded.Interface().(interface{ UnmarshalText([]byte) error }).UnmarshalText([]byte("7"))
		return decoded.Elem(), err == nil
	}
	return reflect.Value{}, false
}

func TestPlanMapKeyCodecsMatchTheVendor(t *testing.T) {
	cache := newPlanCache()
	for _, sampleKey := range []any{
		"k", planNamedString("k"), 5, int8(5), uint16(5), uintptr(5), 1.5, true, [2]int{1, 2},
		planTextKey(5), planHookKey(5), planBothKey(5), planStringTextKey("k"), &planPointerTextKey{},
	} {
		checkMapKeyRule(t, cache, reflect.ValueOf(sampleKey))
	}
	interfaceKeyType := reflect.TypeOf((*interface{})(nil)).Elem()
	for _, dynamicKey := range []any{"k", planTextKey(5), planHookKey(5), 5} {
		interfaceKey := reflect.New(interfaceKeyType).Elem()
		interfaceKey.Set(reflect.ValueOf(dynamicKey))
		checkMapKeyRule(t, cache, interfaceKey)
	}
}

func checkMapKeyRule(t *testing.T, cache *plan.Cache, sampleKey reflect.Value) {
	t.Helper()
	keyType := sampleKey.Type()
	mapType := reflect.MapOf(keyType, reflect.TypeOf(""))
	mapPlan := planOf(t, cache, mapType)

	oneEntry := reflect.MakeMap(mapType)
	oneEntry.SetMapIndex(sampleKey, reflect.ValueOf("v"))
	encoded, vendorErr := vendorbson.Marshal(oneEntry.Interface())
	wantKey, wantEncoded := planEncodedKey(mapPlan.KeyEncode, sampleKey)
	switch {
	case wantEncoded && vendorErr != nil:
		t.Errorf("%v key %v: plan rule %v writes %q; the vendor refuses: %v", keyType, sampleKey, mapPlan.KeyEncode, wantKey, vendorErr)
	case !wantEncoded && vendorErr == nil:
		t.Errorf("%v key %v: plan rule %v refuses; the vendor writes %q", keyType, sampleKey, mapPlan.KeyEncode, vendorElementsOf(t, encoded)[0].Key())
	case wantEncoded && vendorElementsOf(t, encoded)[0].Key() != wantKey:
		t.Errorf("%v key %v: plan rule %v writes %q; the vendor wrote %q", keyType, sampleKey, mapPlan.KeyEncode, wantKey, vendorElementsOf(t, encoded)[0].Key())
	}

	document, err := vendorbson.Marshal(vendorbson.D{{Key: "7", Value: "v"}})
	if err != nil {
		t.Fatalf("building the input: %v", err)
	}
	target := reflect.New(mapType)
	decodeErr := vendorbson.Unmarshal(document, target.Interface())
	wantDecoded, wantDecodes := planDecodedKey(mapPlan.KeyDecode, keyType)
	switch {
	case wantDecodes && decodeErr != nil:
		t.Errorf("%v: plan decode rule %v accepts \"7\"; the vendor refuses: %v", keyType, mapPlan.KeyDecode, decodeErr)
	case !wantDecodes && decodeErr == nil:
		t.Errorf("%v: plan decode rule %v refuses \"7\"; the vendor decodes it", keyType, mapPlan.KeyDecode)
	case wantDecodes:
		decodedKeys := target.Elem().MapKeys()
		if len(decodedKeys) != 1 || !reflect.DeepEqual(decodedKeys[0].Interface(), wantDecoded.Interface()) {
			t.Errorf("%v: plan decode rule %v gives %v; the vendor decoded %v", keyType, mapPlan.KeyDecode, wantDecoded, decodedKeys)
		}
	}
}

// A struct whose ordinary field leads to a struct that inlines it back. The
// vendor lays each out on its own, so neither is refused, in either lookup
// order. The second shape reaches the inlining struct from inside an
// inlined struct's ordinary field.
type orderA struct {
	N string
	F *orderB
}

type orderB struct {
	A orderA `bson:",inline"`
	M string
}

type cycleOuter struct {
	I cycleInner `bson:",inline"`
	P string
}

type cycleInner struct {
	Q string
	F *cycleBack
}

type cycleBack struct {
	O cycleOuter `bson:",inline"`
	R string
}

func TestPlanLayoutIgnoresLookupOrder(t *testing.T) {
	for _, shapes := range [][]reflect.Type{
		{reflect.TypeOf(orderA{}), reflect.TypeOf(orderB{})},
		{reflect.TypeOf(cycleOuter{}), reflect.TypeOf(cycleInner{}), reflect.TypeOf(cycleBack{})},
	} {
		for first := range shapes {
			cache := newPlanCache()
			order := append([]reflect.Type{shapes[first]}, append(append([]reflect.Type{}, shapes[:first]...), shapes[first+1:]...)...)
			for _, goType := range order {
				if typePlan := planOf(t, cache, goType); typePlan.Err != nil {
					t.Errorf("lookup order %v: %v refused: %v", order, goType, typePlan.Err)
				}
			}
			for _, goType := range order {
				t.Run(fmt.Sprintf("%v after %v", goType, order[0]), func(t *testing.T) {
					comparePlanEncodesLikeTheVendor(t, cache, goType)
				})
			}
		}
	}
}
