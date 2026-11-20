// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/oracle/compare"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// The step 4e differential harness: the same bytes go into a vendor target
// and into the library's mirror of it, and the outcomes, error texts and
// decoded values must agree. compare.Decoded needs one Go type on both
// sides, so the vendor's value is first converted into the library's type
// tree, keeping every container type an interface{} holder received.

// decLibraryDecoder is the library decoder under test: the public entry
// points of step 4f, in the shape of internal/decode's Decoder that the
// step 4e tests were written against.
var decLibraryDecoder publicDecoder

// publicDecoder calls bson.Unmarshal and RawValue.Unmarshal.
type publicDecoder struct{}

// Unmarshal is bson.Unmarshal.
func (publicDecoder) Unmarshal(documentBytes []byte, target any) error {
	return bson.Unmarshal(documentBytes, target)
}

// UnmarshalValue is RawValue.Unmarshal of a value of valueType in
// valueBytes.
func (publicDecoder) UnmarshalValue(valueType bsontype.Type, valueBytes []byte, target any) error {
	return bson.RawValue{Type: valueType, Value: valueBytes}.Unmarshal(target)
}

// decLibraryTypes maps each vendor type the decoder can produce to the
// library's mirror of it.
var decLibraryTypes = map[reflect.Type]reflect.Type{
	reflect.TypeOf(vendorprimitive.D{}):             reflect.TypeOf(primitive.D{}),
	reflect.TypeOf(vendorprimitive.M{}):             reflect.TypeOf(primitive.M{}),
	reflect.TypeOf(vendorprimitive.A{}):             reflect.TypeOf(primitive.A{}),
	reflect.TypeOf(vendorprimitive.E{}):             reflect.TypeOf(primitive.E{}),
	reflect.TypeOf(vendorprimitive.Binary{}):        reflect.TypeOf(primitive.Binary{}),
	reflect.TypeOf(vendorprimitive.ObjectID{}):      reflect.TypeOf(primitive.ObjectID{}),
	reflect.TypeOf(vendorprimitive.DateTime(0)):     reflect.TypeOf(primitive.DateTime(0)),
	reflect.TypeOf(vendorprimitive.Decimal128{}):    reflect.TypeOf(primitive.Decimal128{}),
	reflect.TypeOf(vendorprimitive.Undefined{}):     reflect.TypeOf(primitive.Undefined{}),
	reflect.TypeOf(vendorprimitive.Null{}):          reflect.TypeOf(primitive.Null{}),
	reflect.TypeOf(vendorprimitive.Regex{}):         reflect.TypeOf(primitive.Regex{}),
	reflect.TypeOf(vendorprimitive.DBPointer{}):     reflect.TypeOf(primitive.DBPointer{}),
	reflect.TypeOf(vendorprimitive.JavaScript("")):  reflect.TypeOf(primitive.JavaScript("")),
	reflect.TypeOf(vendorprimitive.Symbol("")):      reflect.TypeOf(primitive.Symbol("")),
	reflect.TypeOf(vendorprimitive.CodeWithScope{}): reflect.TypeOf(primitive.CodeWithScope{}),
	reflect.TypeOf(vendorprimitive.Timestamp{}):     reflect.TypeOf(primitive.Timestamp{}),
	reflect.TypeOf(vendorprimitive.MinKey{}):        reflect.TypeOf(primitive.MinKey{}),
	reflect.TypeOf(vendorprimitive.MaxKey{}):        reflect.TypeOf(primitive.MaxKey{}),
	reflect.TypeOf(vendorbson.Raw(nil)):             reflect.TypeOf(bson.Raw(nil)),
	reflect.TypeOf(vendorbson.RawValue{}):           reflect.TypeOf(bson.RawValue{}),
	reflect.TypeOf(vendorbsoncore.Document(nil)):    reflect.TypeOf(bsoncore.Document(nil)),
	reflect.TypeOf(vendorbsoncore.Array(nil)):       reflect.TypeOf(bsoncore.Array(nil)),
	reflect.TypeOf(vendorbsontype.Type(0)):          reflect.TypeOf(bsontype.Type(0)),
}

var (
	decVendorDecimalType  = reflect.TypeOf(vendorprimitive.Decimal128{})
	decVendorRawValueType = reflect.TypeOf(vendorbson.RawValue{})
)

// decLibraryTypeOf is the library type a vendor-decoded value of vendorType
// becomes: the mirror of a vendor type, the same type for every other named
// type, and an unnamed composite rebuilt around its converted parts.
func decLibraryTypeOf(vendorType reflect.Type) reflect.Type {
	if libraryType, mirrored := decLibraryTypes[vendorType]; mirrored {
		return libraryType
	}
	if vendorType.Name() != "" {
		return vendorType
	}
	switch vendorType.Kind() {
	case reflect.Slice:
		return reflect.SliceOf(decLibraryTypeOf(vendorType.Elem()))
	case reflect.Array:
		return reflect.ArrayOf(vendorType.Len(), decLibraryTypeOf(vendorType.Elem()))
	case reflect.Pointer:
		return reflect.PointerTo(decLibraryTypeOf(vendorType.Elem()))
	case reflect.Map:
		return reflect.MapOf(decLibraryTypeOf(vendorType.Key()), decLibraryTypeOf(vendorType.Elem()))
	}
	return vendorType
}

// decNeedsConversion reports whether a value of goType can hold a vendor
// type: a vendor type itself, an interface, or a composite of either.
func decNeedsConversion(goType reflect.Type, visiting map[reflect.Type]bool) bool {
	if _, mirrored := decLibraryTypes[goType]; mirrored {
		return true
	}
	if visiting[goType] {
		return false
	}
	visiting[goType] = true
	defer delete(visiting, goType)
	switch goType.Kind() {
	case reflect.Interface:
		return true
	case reflect.Slice, reflect.Array, reflect.Pointer:
		return decNeedsConversion(goType.Elem(), visiting)
	case reflect.Map:
		return decNeedsConversion(goType.Key(), visiting) || decNeedsConversion(goType.Elem(), visiting)
	case reflect.Struct:
		for fieldIndex := 0; fieldIndex < goType.NumField(); fieldIndex++ {
			if decNeedsConversion(goType.Field(fieldIndex).Type, visiting) {
				return true
			}
		}
	}
	return false
}

// decToLibrary converts a vendor-decoded value into libraryType, walking
// both in step. A struct pair is converted field by field, by index.
func decToLibrary(vendorValue reflect.Value, libraryType reflect.Type) (reflect.Value, error) {
	if !vendorValue.IsValid() {
		return reflect.Zero(libraryType), nil
	}
	vendorType := vendorValue.Type()
	if vendorType == libraryType && !decNeedsConversion(vendorType, map[reflect.Type]bool{}) {
		return vendorValue, nil
	}
	converted := reflect.New(libraryType).Elem()
	switch vendorType {
	case decVendorDecimalType:
		high, low := vendorValue.Interface().(vendorprimitive.Decimal128).GetBytes()
		converted.Set(reflect.ValueOf(primitive.NewDecimal128(high, low)))
		return converted, nil
	case decVendorRawValueType:
		rawValue := vendorValue.Interface().(vendorbson.RawValue)
		converted.Set(reflect.ValueOf(bson.RawValue{Type: bsontype.Type(rawValue.Type), Value: rawValue.Value}))
		return converted, nil
	}
	switch libraryType.Kind() {
	case reflect.Interface:
		if vendorValue.IsNil() {
			return converted, nil
		}
		held := vendorValue.Elem()
		heldConverted, err := decToLibrary(held, decLibraryTypeOf(held.Type()))
		if err != nil {
			return converted, err
		}
		converted.Set(heldConverted)
	case reflect.Pointer:
		if vendorValue.IsNil() {
			return converted, nil
		}
		pointee, err := decToLibrary(vendorValue.Elem(), libraryType.Elem())
		if err != nil {
			return converted, err
		}
		converted.Set(reflect.New(libraryType.Elem()))
		converted.Elem().Set(pointee)
	case reflect.Slice:
		if vendorValue.IsNil() {
			return converted, nil
		}
		converted.Set(reflect.MakeSlice(libraryType, vendorValue.Len(), vendorValue.Len()))
		if err := decConvertElements(vendorValue, converted); err != nil {
			return converted, err
		}
	case reflect.Array:
		if err := decConvertElements(vendorValue, converted); err != nil {
			return converted, err
		}
	case reflect.Map:
		if vendorValue.IsNil() {
			return converted, nil
		}
		converted.Set(reflect.MakeMapWithSize(libraryType, vendorValue.Len()))
		mapRange := vendorValue.MapRange()
		for mapRange.Next() {
			convertedKey, err := decToLibrary(mapRange.Key(), libraryType.Key())
			if err != nil {
				return converted, err
			}
			convertedEntry, err := decToLibrary(mapRange.Value(), libraryType.Elem())
			if err != nil {
				return converted, fmt.Errorf("[%v]: %w", mapRange.Key(), err)
			}
			converted.SetMapIndex(convertedKey, convertedEntry)
		}
	case reflect.Struct:
		if vendorType.NumField() != libraryType.NumField() {
			return converted, fmt.Errorf("struct %s has %d fields, its library mirror %s has %d", vendorType, vendorType.NumField(), libraryType, libraryType.NumField())
		}
		for fieldIndex := 0; fieldIndex < libraryType.NumField(); fieldIndex++ {
			if !libraryType.Field(fieldIndex).IsExported() {
				if !vendorValue.Field(fieldIndex).IsZero() {
					return converted, fmt.Errorf("%s.%s is unexported and set; the harness cannot convert it", vendorType, libraryType.Field(fieldIndex).Name)
				}
				continue
			}
			convertedField, err := decToLibrary(vendorValue.Field(fieldIndex), libraryType.Field(fieldIndex).Type)
			if err != nil {
				return converted, fmt.Errorf(".%s: %w", libraryType.Field(fieldIndex).Name, err)
			}
			converted.Field(fieldIndex).Set(convertedField)
		}
	default:
		converted.Set(vendorValue.Convert(libraryType))
	}
	return converted, nil
}

func decConvertElements(vendorSequence, librarySequence reflect.Value) error {
	for position := 0; position < vendorSequence.Len(); position++ {
		convertedElement, err := decToLibrary(vendorSequence.Index(position), librarySequence.Type().Elem())
		if err != nil {
			return fmt.Errorf("[%d]: %w", position, err)
		}
		librarySequence.Index(position).Set(convertedElement)
	}
	return nil
}

// decTarget is one target type a decode site uses, as a pair of factories
// for the vendor's form and the library's. Each call returns a fresh
// target: a pointer, or a map for the vendor's map-by-value form, prefilled
// where the row tests decoding into a non-empty target.
type decTarget struct {
	name    string
	vendor  func() any
	library func() any
}

// decSame is a target whose Go type holds no vendor type, so both libraries
// decode into the same type.
func decSame[T any](name string, prefill func() T) decTarget {
	return decTarget{
		name:    name,
		vendor:  func() any { target := prefill(); return &target },
		library: func() any { target := prefill(); return &target },
	}
}

func decZero[T any]() T {
	var zero T
	return zero
}

// decRecorder collects compare.Decoded's report instead of failing a test,
// so the caller can label it with the input.
type decRecorder struct {
	testing.TB
	failures []string
}

func (recorder *decRecorder) Helper() {}

func (recorder *decRecorder) Errorf(format string, args ...any) {
	recorder.failures = append(recorder.failures, fmt.Sprintf(format, args...))
}

// decLibraryOutcome runs the library's call, reducing a panic to an outcome
// that never matches the vendor's: the library must not panic.
func decLibraryOutcome(libraryCall func() error) (outcome string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = fmt.Sprintf("LIBRARY PANIC: %v", recovered)
			err = errors.New(outcome)
		}
	}()
	if err := libraryCall(); err != nil {
		return err.Error(), err
	}
	return "ok", nil
}

// decDivergence decodes documentBytes into fresh targets of both forms and describes
// the first difference: the outcome, the error text, or the decoded value,
// which is compared on errors too (the partly filled target). A vendor
// panic is not a value to compare: the library must refuse the input with
// an error instead, and decVendorPanics counts it.
func decDivergence(documentBytes []byte, target decTarget) string {
	return decDivergenceOf(target, func() int { return readableDepth(documentBytes, 1) }, func(vendorTarget any) error {
		return vendorbson.Unmarshal(documentBytes, vendorTarget)
	}, func(libraryTarget any) error {
		return decLibraryDecoder.Unmarshal(documentBytes, libraryTarget)
	})
}

// decValueDivergence is decDivergence for RawValue.Unmarshal: a value of
// valueType in valueBytes.
func decValueDivergence(valueType byte, valueBytes []byte, target decTarget) string {
	return decDivergenceOf(target, func() int { return readableValueDepth(valueType, valueBytes) }, func(vendorTarget any) error {
		return vendorbson.RawValue{Type: vendorbsontype.Type(valueType), Value: valueBytes}.Unmarshal(vendorTarget)
	}, func(libraryTarget any) error {
		return decLibraryDecoder.UnmarshalValue(bsontype.Type(valueType), valueBytes, libraryTarget)
	})
}

// decVendorPanics counts inputs the vendor panicked on during a test run, by
// the panic's text with its numbers replaced; the library refused each with
// an error (decDivergenceOf).
var decVendorPanics = map[string]int{}

var decPanicDigits = regexp.MustCompile(`-?[0-9]+`)

// The vendor panics the library refuses with an error (docs/core-api.md,
// step 4e). Any other vendor panic is a divergence nobody has ruled on.
var (
	decSliceBoundsPanic = regexp.MustCompile(`^panic: runtime error: slice bounds out of range \[([0-9]*):(-?[0-9]+)\]$`)
	decNegativeIndex    = regexp.MustCompile(`^panic: runtime error: index out of range \[-[0-9]+\]$`)
	decNamedPanics      = []*regexp.Regexp{
		regexp.MustCompile(`^panic: reflect: Elem of invalid type \S+$`),
		regexp.MustCompile(`^panic: interface conversion: interface is nil, not bsoncodec\.ValueDecoder$`),
		regexp.MustCompile(`^panic: reflect: call of reflect\.Value\.Type on zero Value$`),
		// Step 5's Go-value half: a null text root into a map passed by
		// value, which the vendor's map codec sets to nil.
		regexp.MustCompile(`^panic: reflect: reflect\.Value\.Set using unaddressable value$`),
	}
)

// decIsKnownVendorPanic reports whether vendorText is one of the classes:
// a slice whose end a negative length put before its start (or below 0), an
// index at a negative read position, getInlineField's reflect.Type.Elem, the
// pointer codec's cached nil decoder, or RawValue.Unmarshal's nil pointer.
func decIsKnownVendorPanic(vendorText string) bool {
	if bounds := decSliceBoundsPanic.FindStringSubmatch(vendorText); bounds != nil {
		end, err := strconv.Atoi(bounds[2])
		if err != nil {
			return false
		}
		if bounds[1] == "" {
			return end < 0
		}
		start, err := strconv.Atoi(bounds[1])
		return err == nil && end < start
	}
	if decNegativeIndex.MatchString(vendorText) {
		return true
	}
	for _, named := range decNamedPanics {
		if named.MatchString(vendorText) {
			return true
		}
	}
	return false
}

// decNeverEnds is the library's refusal of a document whose negative value
// length loops the read back to an element it read before. The vendor reads
// such a document for ever (TestDecodeVendorPanicsAreErrors), so the vendor
// is not called on it.
const decNeverEnds = "so the document never ends"

// decDivergenceOf is decDivergence over two calls. levels measures the
// input's nesting: the library's ceiling refusal (spec 5.2) is a
// divergence only for an input that does not nest past the ceiling.
func decDivergenceOf(target decTarget, levels func() int, vendorCall, libraryCall func(any) error) string {
	vendorTarget, libraryTarget := target.vendor(), target.library()
	libraryText, libraryErr := decLibraryOutcome(func() error { return libraryCall(libraryTarget) })
	if strings.HasPrefix(libraryText, "LIBRARY PANIC") {
		return fmt.Sprintf("%s: %s", target.name, libraryText)
	}
	if errors.Is(libraryErr, bson.ErrNestingTooDeep) {
		if depth := levels(); depth <= bson.MaxNestingCeiling {
			return fmt.Sprintf("%s: the library refuses an input %d levels deep at the ceiling: %s", target.name, depth, libraryText)
		}
		decVendorPanics["past the ceiling (not compared)"]++
		return ""
	}
	if strings.HasSuffix(libraryText, decNeverEnds) {
		decVendorPanics["the vendor never returns"]++
		return ""
	}
	vendorText := vendorOutcome(func() error { return vendorCall(vendorTarget) })
	if strings.HasPrefix(vendorText, "panic: ") {
		if !decIsKnownVendorPanic(vendorText) {
			return fmt.Sprintf("%s: the vendor panics with a panic no step 4e class names: %s (the library: %s)", target.name, vendorText, libraryText)
		}
		if libraryErr == nil {
			return fmt.Sprintf("%s: the vendor panics (%s), the library accepts", target.name, vendorText)
		}
		decVendorPanics[decPanicDigits.ReplaceAllString(vendorText, "N")]++
		return ""
	}
	if vendorText != libraryText {
		return fmt.Sprintf("%s: outcome %q (vendor) vs %q (library)", target.name, vendorText, libraryText)
	}
	return decValueDifference(target.name, vendorTarget, libraryTarget)
}

// decValueDifference compares a vendor-decoded target with the library's.
func decValueDifference(label string, vendorTarget, libraryTarget any) string {
	converted, err := decToLibrary(reflect.ValueOf(vendorTarget), reflect.TypeOf(libraryTarget))
	if err != nil {
		return fmt.Sprintf("%s: converting the vendor's value: %v", label, err)
	}
	recorder := &decRecorder{}
	compare.Decoded(recorder, converted.Interface(), libraryTarget)
	if len(recorder.failures) > 0 {
		return fmt.Sprintf("%s: %s", label, strings.Join(recorder.failures, "; "))
	}
	return ""
}

// decCheck fails t when documentBytes decodes differently into any of targets.
func decCheck(t *testing.T, label string, documentBytes []byte, targets ...decTarget) {
	t.Helper()
	for _, target := range targets {
		if divergence := decDivergence(documentBytes, target); divergence != "" {
			t.Errorf("%s: % x: %s", label, documentBytes, divergence)
		}
	}
}

// decMustDecode fails t unless the vendor decodes documentBytes into target without
// an error, so a row's positive case cannot pass by both sides refusing it.
func decMustDecode(t *testing.T, label string, documentBytes []byte, target decTarget) {
	t.Helper()
	if outcome := vendorOutcome(func() error { return vendorbson.Unmarshal(documentBytes, target.vendor()) }); outcome != "ok" {
		t.Fatalf("%s: the vendor refuses the row's input into %s: %s", label, target.name, outcome)
	}
}

// decMustRefuse fails t unless the vendor refuses documentBytes into target, for a
// row's error case.
func decMustRefuse(t *testing.T, label string, documentBytes []byte, target decTarget) {
	t.Helper()
	if outcome := vendorOutcome(func() error { return vendorbson.Unmarshal(documentBytes, target.vendor()) }); outcome == "ok" {
		t.Fatalf("%s: the vendor accepts the row's error input into %s", label, target.name)
	}
}

// decMarshal is a vendor encode of a test document, failing t on error.
func decMarshal(t testing.TB, document any) []byte {
	t.Helper()
	encoded, err := vendorbson.Marshal(document)
	if err != nil {
		t.Fatalf("encoding a test document: %v", err)
	}
	return encoded
}

// The document-level targets every decode site class uses (spec 9.1 row 2),
// empty.
var (
	decTargetMap       = decSame("map[string]interface{}", decZero[map[string]any])
	decTargetInterface = decSame("interface{}", decZero[any])
	decTargetD         = decTarget{"bson.D", func() any { return &vendorbson.D{} }, func() any { return &bson.D{} }}
	decTargetNilD      = decTarget{"nil bson.D", func() any { var target vendorbson.D; return &target }, func() any { var target bson.D; return &target }}
	decTargetM         = decTarget{"bson.M", func() any { return &vendorbson.M{} }, func() any { return &bson.M{} }}
	decTargetNilM      = decTarget{"nil bson.M", func() any { var target vendorbson.M; return &target }, func() any { var target bson.M; return &target }}
	decTargetMapValue  = decTarget{"map[string]interface{} by value", func() any { return map[string]any{} }, func() any { return map[string]any{} }}
	decTargetRaw       = decTarget{"bson.Raw", func() any { var target vendorbson.Raw; return &target }, func() any { var target bson.Raw; return &target }}
	decTargetRawValue  = decTarget{"bson.RawValue", func() any { var target vendorbson.RawValue; return &target }, func() any { var target bson.RawValue; return &target }}
	decTargetCoreDoc   = decTarget{"bsoncore.Document", func() any { var target vendorbsoncore.Document; return &target }, func() any { var target bsoncore.Document; return &target }}
)

// decDocumentTargets are the targets a whole document is decoded into at
// the tree's sites, plus the ones the fuzz may reach.
func decDocumentTargets() []decTarget {
	return []decTarget{
		decTargetMap, decTargetInterface, decTargetD, decTargetNilD, decTargetM, decTargetNilM,
		decTargetMapValue, decTargetRaw, decTargetRawValue, decTargetCoreDoc,
	}
}
