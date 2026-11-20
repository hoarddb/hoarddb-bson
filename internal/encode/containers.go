// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsoncodec/struct_codec.go (EncodeValue, isEmpty, fieldByIndexErr), bson/bsoncodec/map_codec.go (EncodeValue, mapEncodeValue, encodeKey), bson/bsoncodec/slice_codec.go and the ArrayEncodeValue, encodeElement and lookupElementEncoder of bson/bsoncodec/default_value_encoders.go; rewritten over the plan's struct layouts, and a map's entries are written in ascending byte order of their encoded keys (spec 6.1, O11) where the vendor iterates in Go's map order.

package encode

import (
	"cmp"
	"encoding"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
)

// elementTarget is the vendor's lookupElementEncoder for one element of a
// slice, an array, a map or a struct: an element whose static type has no
// codec and is an interface is encoded through the type it holds, and holds
// nothing when the interface is nil.
func (encoding *writer) elementTarget(elemPlan *plan.Type, elem reflect.Value) (*plan.Type, reflect.Value, bool, error) {
	if elemPlan.Encode.Codec != plan.NoCodec || elem.Kind() != reflect.Interface {
		return elemPlan, elem, true, nil
	}
	dynamic := elem.Elem()
	if !dynamic.IsValid() {
		return nil, dynamic, false, nil
	}
	dynamicPlan, err := encoding.cache.Lookup(dynamic.Type())
	if err != nil {
		return nil, dynamic, false, err
	}
	return dynamicPlan, dynamic, true, nil
}

// requireElementCodec is the check the vendor's slice, array and map codecs
// make once per value, before any element: an element type with no codec
// is an error unless it is an interface, which is dispatched per element.
func requireElementCodec(elemPlan *plan.Type) error {
	if elemPlan.Encode.Codec == plan.NoCodec && elemPlan.GoType.Kind() != reflect.Interface {
		return noEncoderError{goType: elemPlan.GoType}
	}
	return nil
}

// encodeElementAt writes one container element at slot: null for an
// interface holding nothing, else the value through its plan.
func (encoding *writer) encodeElementAt(elemPlan *plan.Type, elem reflect.Value, at slot, minSize bool) error {
	targetPlan, target, present, err := encoding.elementTarget(elemPlan, elem)
	if err != nil {
		return err
	}
	if !present {
		return encoding.writeEmpty(at, bsontype.Null, "WriteNull")
	}
	return encoding.encodeValue(targetPlan, target, at, minSize)
}

// encodeStruct is the vendor's StructCodec.EncodeValue: the layout's fields
// in order, each one skipped when an inline pointer on its path is nil or
// when omitempty finds it empty, then the inline map's entries.
func (encoding *writer) encodeStruct(typePlan *plan.Type, val reflect.Value, at slot, minSize bool) error {
	if typePlan.Err != nil {
		return typePlan.Err
	}
	start, err := encoding.startDocument(at)
	if err != nil {
		return err
	}
	layout := typePlan.Struct
	for position := range layout.Fields {
		field := &layout.Fields[position]
		fieldValue, reachable := fieldAt(val, field.Index)
		if !reachable {
			continue
		}
		if err := encoding.encodeField(field, fieldValue, minSize); err != nil {
			return err
		}
	}
	if layout.InlineMap != nil {
		return encoding.mapEntries(layout.InlineMap, val.Field(layout.InlineMapIndex), start, minSize, layout)
	}
	return encoding.end(start)
}

// fieldAt is reflect.Value.FieldByIndex, reporting a nil struct pointer on
// the path instead of panicking; the vendor recovers that panic and skips
// the field.
func fieldAt(structValue reflect.Value, index []int) (reflect.Value, bool) {
	fieldValue := structValue.Field(index[0])
	for _, position := range index[1:] {
		if fieldValue.Kind() == reflect.Pointer {
			if fieldValue.IsNil() {
				return reflect.Value{}, false
			}
			fieldValue = fieldValue.Elem()
		}
		fieldValue = fieldValue.Field(position)
	}
	return fieldValue, true
}

func (encoding *writer) encodeField(field *plan.Field, fieldValue reflect.Value, minSize bool) error {
	targetPlan, target, present, err := encoding.elementTarget(field.Type, fieldValue)
	if err != nil {
		return err
	}
	at := element(field.Key)
	if !present {
		if field.OmitEmpty {
			return nil
		}
		return encoding.writeEmpty(at, bsontype.Null, "WriteNull")
	}
	// The vendor refuses a field with no codec when it reaches it, before
	// omitempty is asked, so a zero value of such a type fails too.
	if targetPlan.Encode.Codec == plan.NoCodec {
		return noEncoderError{goType: target.Type()}
	}
	if field.OmitEmpty && isEmpty(targetPlan, target) {
		return nil
	}
	return encoding.encodeValue(targetPlan, target, at, minSize || field.MinSize)
}

// isEmpty is the vendor's omitempty test, with OmitZeroStruct off. An
// interface that still is one here (interface{}, or one with a hook) is
// empty only when nil. A type with IsZero decides for itself, except a nil
// pointer, which is empty. A struct without IsZero is never empty.
func isEmpty(typePlan *plan.Type, val reflect.Value) bool {
	kind := val.Kind()
	if kind == reflect.Interface {
		return val.IsNil()
	}
	if typePlan.Zeroer && (kind != reflect.Pointer || !val.IsNil()) {
		return zeroerOf(val).IsZero()
	}
	switch kind {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return val.Len() == 0
	case reflect.Struct:
		return false
	}
	return val.IsZero()
}

// zeroerOf boxes val as a Zeroer, through its address when it has one so
// that the value is not copied onto the heap; a value method is in the
// pointer's method set too.
func zeroerOf(val reflect.Value) plan.Zeroer {
	if val.CanAddr() && val.Kind() != reflect.Pointer {
		return val.Addr().Interface().(plan.Zeroer)
	}
	return val.Interface().(plan.Zeroer)
}

// encodeMap is the vendor's MapCodec.EncodeValue: a nil map is null, except
// at the top level, where the vendor's null write fails and it writes an
// empty document instead.
func (encoding *writer) encodeMap(typePlan *plan.Type, val reflect.Value, at slot, minSize bool) error {
	if val.IsNil() && !at.isTopLevel() {
		return encoding.writeEmpty(at, bsontype.Null, "WriteNull")
	}
	start, err := encoding.startDocument(at)
	if err != nil {
		return err
	}
	return encoding.mapEntries(typePlan, val, start, minSize, nil)
}

// mapEntry is one map entry waiting to be written in key order.
type mapEntry struct {
	key   string
	value reflect.Value
}

// mapEntries writes a map's entries into the document opened at start, in
// ascending byte order of their encoded keys, and closes it. inlinedInto is
// the struct whose inline map this is: a key one of its fields has is the
// vendor's error.
//
// Every key is encoded before any value is written. The vendor encodes key
// and value entry by entry in Go's random map order, so when more than one
// entry fails, which error it returns is random; the one returned here is
// always one it can return.
func (encoding *writer) mapEntries(typePlan *plan.Type, val reflect.Value, start int, minSize bool, inlinedInto *plan.Struct) error {
	elemPlan := typePlan.Elem
	if err := requireElementCodec(elemPlan); err != nil {
		return err
	}
	first := len(encoding.entries)
	defer func() { encoding.entries = encoding.entries[:first] }()
	iterator := val.MapRange()
	for iterator.Next() {
		key, err := encodeKey(typePlan, iterator.Key())
		if err != nil {
			return err
		}
		encoding.entries = append(encoding.entries, mapEntry{key: key, value: iterator.Value()})
	}
	slices.SortFunc(encoding.entries[first:], func(left, right mapEntry) int {
		return cmp.Compare(left.key, right.key)
	})
	count := len(encoding.entries) - first
	for position := 0; position < count; position++ {
		// Indexed afresh each time: a nested map appends to entries and may
		// move it.
		entry := encoding.entries[first+position]
		if inlinedInto != nil && hasExactKey(inlinedInto, entry.key) {
			return fmt.Errorf("Key %s of inlined map conflicts with a struct field name", entry.key) //nolint:staticcheck // ST1005: the vendor's text, which callers see
		}
		if err := encoding.encodeElementAt(elemPlan, entry.value, element(entry.key), minSize); err != nil {
			return err
		}
	}
	return encoding.end(start)
}

// hasExactKey reports whether a field of layout has exactly key, which the
// vendor's collision check asks of its field map (no lower-case fallback).
func hasExactKey(layout *plan.Struct, key string) bool {
	field, found := layout.FieldByKey(key)
	return found && field.Key == key
}

var (
	keyMarshalerType  = reflect.TypeOf((*plan.KeyMarshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

// encodeKey is the vendor's MapCodec.encodeKey under the plan's key rule.
func encodeKey(typePlan *plan.Type, key reflect.Value) (string, error) {
	switch typePlan.KeyEncode {
	case plan.StringKey:
		return key.String(), nil
	case plan.IntKey:
		return strconv.FormatInt(key.Int(), 10), nil
	case plan.UintKey:
		return strconv.FormatUint(key.Uint(), 10), nil
	case plan.KeyHookKey:
		if key.Kind() == reflect.Pointer && key.IsNil() {
			return "", nil
		}
		return key.Interface().(plan.KeyMarshaler).MarshalKey()
	case plan.TextHookKey:
		if key.Kind() == reflect.Pointer && key.IsNil() {
			return "", nil
		}
		return marshalTextKey(key.Interface().(encoding.TextMarshaler))
	case plan.DynamicKey:
		return encodeDynamicKey(key)
	}
	return "", fmt.Errorf("unsupported key type: %v", key.Type())
}

func marshalTextKey(textMarshaler encoding.TextMarshaler) (string, error) {
	keyText, err := textMarshaler.MarshalText()
	if err != nil {
		return "", err
	}
	return string(keyText), nil
}

// encodeDynamicKey encodes a key of an interface key type. The vendor checks
// the hooks on the value the interface holds, but the kind of the key
// itself, which is Interface: so a hook key is encoded, through its hook
// even when it is a nil pointer (an error here where the method has a value
// receiver and the vendor's call panics), and every other key is
// unsupported, a string included.
func encodeDynamicKey(key reflect.Value) (string, error) {
	dynamic := key.Elem()
	if dynamic.IsValid() {
		if dynamic.Type().Implements(keyMarshalerType) {
			if err := refuseNilValueReceiver(dynamic, keyMarshalerType); err != nil {
				return "", err
			}
			return dynamic.Interface().(plan.KeyMarshaler).MarshalKey()
		}
		if dynamic.Type().Implements(textMarshalerType) {
			if err := refuseNilValueReceiver(dynamic, textMarshalerType); err != nil {
				return "", err
			}
			return marshalTextKey(dynamic.Interface().(encoding.TextMarshaler))
		}
	}
	return "", fmt.Errorf("unsupported key type: %v", key.Type())
}

// encodeSlice is the vendor's SliceCodec.EncodeValue: nil is null (refused
// at the top level, unlike a nil map), a byte element is Binary, a slice
// convertible to primitive.D a document, anything else an array.
func (encoding *writer) encodeSlice(typePlan *plan.Type, val reflect.Value, at slot, minSize bool) error {
	if val.IsNil() {
		return encoding.writeEmpty(at, bsontype.Null, "WriteNull")
	}
	if typePlan.ElemIsByte {
		return encoding.writeBinary(at, 0x00, val.Bytes())
	}
	if typePlan.ConvertibleToD {
		return encoding.encodeElements(val, at, minSize)
	}
	return encoding.encodeSequence(typePlan, val, at, minSize)
}

// encodeArray is the vendor's ArrayEncodeValue for a Go array: an array of
// primitive.E is a document, an array of byte is Binary, anything else an
// array.
func (encoding *writer) encodeArray(typePlan *plan.Type, val reflect.Value, at slot, minSize bool) error {
	if typePlan.ElemIsE {
		return encoding.encodeElements(val, at, minSize)
	}
	if typePlan.ElemIsByte {
		payload := make([]byte, val.Len())
		for position := range payload {
			payload[position] = byte(val.Index(position).Uint())
		}
		return encoding.writeBinary(at, 0x00, payload)
	}
	return encoding.encodeSequence(typePlan, val, at, minSize)
}

// encodeSequence writes a slice or an array as a BSON array.
func (encoding *writer) encodeSequence(typePlan *plan.Type, val reflect.Value, at slot, minSize bool) error {
	start, err := encoding.startArray(at)
	if err != nil {
		return err
	}
	elemPlan := typePlan.Elem
	if err := requireElementCodec(elemPlan); err != nil {
		return err
	}
	for position := 0; position < val.Len(); position++ {
		if err := encoding.encodeElementAt(elemPlan, val.Index(position), arrayElement(position), minSize); err != nil {
			return err
		}
	}
	return encoding.end(start)
}

// encodeElements writes a sequence of primitive.E as a document, each
// element as the vendor's encodeElement does: a nil value is null, and any
// other value is encoded through its dynamic type.
func (encoding *writer) encodeElements(val reflect.Value, at slot, minSize bool) error {
	start, err := encoding.startDocument(at)
	if err != nil {
		return err
	}
	for position := 0; position < val.Len(); position++ {
		documentElement := val.Index(position)
		elementAt := element(documentElement.Field(0).String())
		elementValue := documentElement.Field(1)
		if elementValue.IsNil() {
			if err := encoding.writeEmpty(elementAt, bsontype.Null, "WriteNull"); err != nil {
				return err
			}
			continue
		}
		if err := encoding.encodeDynamic(elementValue.Elem(), elementAt, minSize); err != nil {
			return err
		}
	}
	return encoding.end(start)
}
