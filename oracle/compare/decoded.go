// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package compare

import (
	"fmt"
	"math"
	"math/cmplx"
	"reflect"
	"sort"
	"testing"
)

// Decoded reports to tb when the library's decoded value is not
// reflect.DeepEqual to the vendor's, naming the first differing path. The Go
// type inside every interface{} holder is part of the comparison, so
// map[string]interface{} against primitive.M (spec §5.3, D2 and D3) and a nil
// slice against an empty one are differences, although each pair prints the
// same. Two NaNs with the same bits are equal, which reflect.DeepEqual denies.
func Decoded(tb testing.TB, vendorValue, libValue any) {
	tb.Helper()
	if reflect.DeepEqual(vendorValue, libValue) {
		return
	}
	difference := firstDecodedDifference("$", reflect.ValueOf(vendorValue), reflect.ValueOf(libValue))
	if difference == nil {
		// reflect.DeepEqual calls a NaN different from itself, so a value
		// holding one can leave DeepEqual false with nothing to locate. For
		// every other value, the walk mirrors DeepEqual; this keeps a gap in
		// the walk from hiding a difference DeepEqual found.
		if !containsNaN(reflect.ValueOf(vendorValue)) {
			difference = fmt.Errorf("$: reflect.DeepEqual reports a difference the path walk did not locate: %#v (vendor) vs %#v (library)", vendorValue, libValue)
		} else {
			return
		}
	}
	tb.Errorf("compare.Decoded: %v", difference)
}

// containsNaN reports whether a NaN float or complex is anywhere inside
// goValue, reading through interfaces, pointers, slices, arrays, maps and
// exported and unexported struct fields alike.
func containsNaN(goValue reflect.Value) bool {
	if !goValue.IsValid() {
		return false
	}
	switch goValue.Kind() {
	case reflect.Float32, reflect.Float64:
		return math.IsNaN(goValue.Float())
	case reflect.Complex64, reflect.Complex128:
		return cmplx.IsNaN(goValue.Complex())
	case reflect.Interface, reflect.Pointer:
		return !goValue.IsNil() && containsNaN(goValue.Elem())
	case reflect.Slice, reflect.Array:
		for position := 0; position < goValue.Len(); position++ {
			if containsNaN(goValue.Index(position)) {
				return true
			}
		}
	case reflect.Map:
		for _, mapKey := range goValue.MapKeys() {
			if containsNaN(mapKey) || containsNaN(goValue.MapIndex(mapKey)) {
				return true
			}
		}
	case reflect.Struct:
		for fieldIndex := 0; fieldIndex < goValue.NumField(); fieldIndex++ {
			if containsNaN(goValue.Field(fieldIndex)) {
				return true
			}
		}
	}
	return false
}

// firstDecodedDifference walks two values the way reflect.DeepEqual does and
// returns the first difference, or nil.
func firstDecodedDifference(path string, vendorValue, libValue reflect.Value) error {
	if !vendorValue.IsValid() || !libValue.IsValid() {
		if vendorValue.IsValid() != libValue.IsValid() {
			return fmt.Errorf("%s: %s (vendor) vs %s (library)", path, describeValue(vendorValue), describeValue(libValue))
		}
		return nil
	}
	if vendorValue.Type() != libValue.Type() {
		return fmt.Errorf("%s: Go type %s (vendor) vs %s (library)", path, vendorValue.Type(), libValue.Type())
	}
	switch vendorValue.Kind() {
	case reflect.Interface:
		return interfaceDifference(path, vendorValue, libValue)
	case reflect.Pointer:
		if vendorValue.IsNil() || libValue.IsNil() {
			return nilDifference(path, vendorValue, libValue)
		}
		return firstDecodedDifference(path, vendorValue.Elem(), libValue.Elem())
	case reflect.Slice:
		if vendorValue.IsNil() || libValue.IsNil() {
			return nilDifference(path, vendorValue, libValue)
		}
		return sequenceDifference(path, vendorValue, libValue)
	case reflect.Array:
		return sequenceDifference(path, vendorValue, libValue)
	case reflect.Map:
		if vendorValue.IsNil() || libValue.IsNil() {
			return nilDifference(path, vendorValue, libValue)
		}
		return mapDifference(path, vendorValue, libValue)
	case reflect.Struct:
		return structDifference(path, vendorValue, libValue)
	}
	if !leafEqual(vendorValue, libValue) {
		return fmt.Errorf("%s: %v (vendor) vs %v (library)", path, vendorValue, libValue)
	}
	return nil
}

func describeValue(goValue reflect.Value) string {
	if !goValue.IsValid() {
		return "no value"
	}
	if canBeNil(goValue.Kind()) && goValue.IsNil() {
		return "nil " + goValue.Type().String()
	}
	if goValue.Kind() == reflect.Slice || goValue.Kind() == reflect.Map {
		return fmt.Sprintf("non-nil %s of length %d", goValue.Type(), goValue.Len())
	}
	return fmt.Sprintf("%s %v", goValue.Type(), goValue)
}

func canBeNil(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return true
	}
	return false
}

// nilDifference handles a pair where at least one side is nil: equal only if
// both are, as reflect.DeepEqual has it.
func nilDifference(path string, vendorValue, libValue reflect.Value) error {
	if vendorValue.IsNil() == libValue.IsNil() {
		return nil
	}
	return fmt.Errorf("%s: %s (vendor) vs %s (library)", path, describeValue(vendorValue), describeValue(libValue))
}

// interfaceDifference checks the Go type an interface{} holder holds before
// its value: the D1-D7 contracts are about that type.
func interfaceDifference(path string, vendorHolder, libHolder reflect.Value) error {
	if vendorHolder.IsNil() || libHolder.IsNil() {
		return nilDifference(path, vendorHolder, libHolder)
	}
	vendorHeld, libHeld := vendorHolder.Elem(), libHolder.Elem()
	if vendorHeld.Type() != libHeld.Type() {
		return fmt.Errorf("%s: the %s holder holds %s (vendor) vs %s (library)", path, vendorHolder.Type(), vendorHeld.Type(), libHeld.Type())
	}
	return firstDecodedDifference(path, vendorHeld, libHeld)
}

func sequenceDifference(path string, vendorSequence, libSequence reflect.Value) error {
	if vendorSequence.Len() != libSequence.Len() {
		return fmt.Errorf("%s: length %d (vendor) vs %d (library)", path, vendorSequence.Len(), libSequence.Len())
	}
	for position := 0; position < vendorSequence.Len(); position++ {
		elementPath := fmt.Sprintf("%s[%d]", path, position)
		if err := firstDecodedDifference(elementPath, vendorSequence.Index(position), libSequence.Index(position)); err != nil {
			return err
		}
	}
	return nil
}

func mapDifference(path string, vendorMap, libMap reflect.Value) error {
	if vendorMap.Len() != libMap.Len() {
		return fmt.Errorf("%s: %d entries (vendor) vs %d (library)", path, vendorMap.Len(), libMap.Len())
	}
	// Sorted, so the first difference reported is the same on every run.
	vendorKeys := vendorMap.MapKeys()
	sort.Slice(vendorKeys, func(left, right int) bool {
		return fmt.Sprint(vendorKeys[left]) < fmt.Sprint(vendorKeys[right])
	})
	for _, mapKey := range vendorKeys {
		entryPath := fmt.Sprintf("%s[%v]", path, mapKey)
		if mapKey.Kind() == reflect.String {
			entryPath = fmt.Sprintf("%s[%q]", path, mapKey.String())
		}
		libEntry := libMap.MapIndex(mapKey)
		if !libEntry.IsValid() {
			return fmt.Errorf("%s: the key is in the vendor's map but not the library's", entryPath)
		}
		if err := firstDecodedDifference(entryPath, vendorMap.MapIndex(mapKey), libEntry); err != nil {
			return err
		}
	}
	return nil
}

func structDifference(path string, vendorStruct, libStruct reflect.Value) error {
	structType := vendorStruct.Type()
	for fieldIndex := 0; fieldIndex < structType.NumField(); fieldIndex++ {
		fieldPath := path + "." + structType.Field(fieldIndex).Name
		if err := firstDecodedDifference(fieldPath, vendorStruct.Field(fieldIndex), libStruct.Field(fieldIndex)); err != nil {
			return err
		}
	}
	return nil
}

// leafEqual compares the kinds reflect.DeepEqual compares with ==, except that
// two floats with the same bit pattern are equal when they are NaN. A decode
// of a BSON NaN is a faithful decode, and == would call it a difference from
// itself. NaN is still unequal to every number and to a NaN with other bits,
// which encodes to other bytes. It reads through the kind getters, never
// Interface, so it also works on unexported fields such as time.Time's.
func leafEqual(vendorValue, libValue reflect.Value) bool {
	switch vendorValue.Kind() {
	case reflect.Bool:
		return vendorValue.Bool() == libValue.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return vendorValue.Int() == libValue.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return vendorValue.Uint() == libValue.Uint()
	case reflect.Float32:
		return floatEqual(vendorValue.Float(), libValue.Float(), 32)
	case reflect.Float64:
		return floatEqual(vendorValue.Float(), libValue.Float(), 64)
	case reflect.Complex64:
		return complexEqual(vendorValue.Complex(), libValue.Complex(), 32)
	case reflect.Complex128:
		return complexEqual(vendorValue.Complex(), libValue.Complex(), 64)
	case reflect.String:
		return vendorValue.String() == libValue.String()
	case reflect.Chan, reflect.UnsafePointer:
		return vendorValue.Pointer() == libValue.Pointer()
	default: // reflect.Func, the only kind left: equal only when both are nil
		return vendorValue.IsNil() && libValue.IsNil()
	}
}

// floatEqual is == plus: two NaNs are equal when their bits are. A float32
// widened to float64 keeps its NaN payload bits shifted but distinct, so
// comparing the widened bits is exact for both widths.
func floatEqual(vendorFloat, libFloat float64, bitSize int) bool {
	if math.IsNaN(vendorFloat) && math.IsNaN(libFloat) {
		return nanBits(vendorFloat, bitSize) == nanBits(libFloat, bitSize)
	}
	return vendorFloat == libFloat
}

func complexEqual(vendorComplex, libComplex complex128, bitSize int) bool {
	return floatEqual(real(vendorComplex), real(libComplex), bitSize/2) &&
		floatEqual(imag(vendorComplex), imag(libComplex), bitSize/2)
}

func nanBits(nan float64, bitSize int) uint64 {
	if bitSize == 32 {
		return uint64(math.Float32bits(float32(nan)))
	}
	return math.Float64bits(nan)
}
