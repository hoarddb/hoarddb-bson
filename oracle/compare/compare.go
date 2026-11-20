// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package compare holds the differential helpers that judge the library
// against the vendor: Encoded for bytes (spec §6.1) and Decoded for decoded Go
// values (spec §6.2). Each reports only the first difference, with its path.
package compare

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
)

// Encoded reports to tb the first difference between the vendor's and the
// library's encoding of goValue, both of which must be BSON documents.
//
// It walks goValue beside the two encodings. A level that comes from a Go map
// (or from a struct's inline map, after the struct's own fields) compares as a
// set of elements, because the vendor's map order is random; every other level
// (struct, bson.D, slice, array) compares element by element in order. Where
// the Go value does not say what a level is, for example behind a MarshalBSON
// method, the level compares byte for byte, which can only be stricter.
//
// Comparing by decoding is not an option: it would hide a reordered struct
// field or a changed integer width.
func Encoded(tb testing.TB, goValue any, vendorBytes, libBytes []byte) {
	tb.Helper()
	if err := vendorbsoncore.Document(vendorBytes).Validate(); err != nil {
		tb.Errorf("compare.Encoded(%T): the vendor bytes are not a valid document: %v", goValue, err)
		return
	}
	if err := vendorbsoncore.Document(libBytes).Validate(); err != nil {
		tb.Errorf("compare.Encoded(%T): the library bytes are not a valid document: %v", goValue, err)
		return
	}
	if err := compareDocuments("$", documentShapeOf(reflect.ValueOf(goValue), false), vendorBytes, libBytes); err != nil {
		tb.Errorf("compare.Encoded(%T): %v", goValue, err)
	}
}

// documentShape says how one document level of an encoding compares and which
// Go value each of its elements came from.
type documentShape struct {
	isArray bool
	// orderedPrefix returns how many leading elements, given by their keys,
	// compare in order; the rest compare as a set keyed by element name.
	orderedPrefix func(keys []string) int
	// childValue returns the Go value behind an element, or the zero Value
	// when the Go side does not say, which makes the element compare raw.
	childValue func(key string, position int) reflect.Value
}

func allInOrder(keys []string) int { return len(keys) }

func noChildValue(string, int) reflect.Value { return reflect.Value{} }

// opaqueShape compares a level strictly in order with no Go guidance.
func opaqueShape(isArray bool) documentShape {
	return documentShape{isArray: isArray, orderedPrefix: allInOrder, childValue: noChildValue}
}

func compareDocuments(path string, shape documentShape, vendorDoc, libDoc []byte) error {
	vendorElements, err := vendorbsoncore.Document(vendorDoc).Elements()
	if err != nil {
		return fmt.Errorf("%s: the vendor document is malformed: %w", path, err)
	}
	libElements, err := vendorbsoncore.Document(libDoc).Elements()
	if err != nil {
		return fmt.Errorf("%s: the library document is malformed: %w", path, err)
	}
	vendorPrefix := shape.orderedPrefix(elementKeys(vendorElements))
	libPrefix := shape.orderedPrefix(elementKeys(libElements))
	if err := compareInOrder(path, shape, vendorElements[:vendorPrefix], libElements[:libPrefix]); err != nil {
		return err
	}
	return compareAsSet(path, shape, vendorElements[vendorPrefix:], libElements[libPrefix:])
}

func elementKeys(elements []vendorbsoncore.Element) []string {
	keys := make([]string, len(elements))
	for position, element := range elements {
		keys[position] = element.Key()
	}
	return keys
}

func compareInOrder(path string, shape documentShape, vendorElements, libElements []vendorbsoncore.Element) error {
	if len(vendorElements) != len(libElements) {
		return fmt.Errorf("%s: %d elements in order (vendor) vs %d (library): vendor keys %q, library keys %q",
			path, len(vendorElements), len(libElements), elementKeys(vendorElements), elementKeys(libElements))
	}
	for position, vendorElement := range vendorElements {
		key := vendorElement.Key()
		if libKey := libElements[position].Key(); libKey != key {
			return fmt.Errorf("%s: element %d is %q (vendor) vs %q (library): vendor keys %q, library keys %q",
				path, position, key, libKey, elementKeys(vendorElements), elementKeys(libElements))
		}
		childPath := shape.childPath(path, key)
		if err := compareValues(childPath, shape.childValue(key, position), vendorElement.Value(), libElements[position].Value()); err != nil {
			return err
		}
	}
	return nil
}

// elementsByKey indexes a set level, refusing a repeated key: a map level
// never holds one.
func elementsByKey(path, side string, elements []vendorbsoncore.Element) (map[string]vendorbsoncore.Element, error) {
	indexed := make(map[string]vendorbsoncore.Element, len(elements))
	for _, element := range elements {
		if _, repeated := indexed[element.Key()]; repeated {
			return nil, fmt.Errorf("%s: the %s map level repeats the key %q", path, side, element.Key())
		}
		indexed[element.Key()] = element
	}
	return indexed, nil
}

func compareAsSet(path string, shape documentShape, vendorElements, libElements []vendorbsoncore.Element) error {
	if _, err := elementsByKey(path, "vendor", vendorElements); err != nil {
		return err
	}
	libByKey, err := elementsByKey(path, "library", libElements)
	if err != nil {
		return err
	}
	if len(vendorElements) != len(libElements) {
		return fmt.Errorf("%s: %d map entries (vendor) vs %d (library): vendor keys %q, library keys %q",
			path, len(vendorElements), len(libElements), elementKeys(vendorElements), elementKeys(libElements))
	}
	for position, vendorElement := range vendorElements {
		key := vendorElement.Key()
		libElement, found := libByKey[key]
		if !found {
			return fmt.Errorf("%s: map key %q is in the vendor encoding but not the library's (library keys %q)",
				path, key, elementKeys(libElements))
		}
		if err := compareValues(shape.childPath(path, key), shape.childValue(key, position), vendorElement.Value(), libElement.Value()); err != nil {
			return err
		}
	}
	return nil
}

func (shape documentShape) childPath(path, key string) string {
	if shape.isArray {
		return path + "[" + key + "]"
	}
	return path + "." + key
}

func compareValues(path string, goValue reflect.Value, vendorValue, libValue vendorbsoncore.Value) error {
	if vendorValue.Type != libValue.Type {
		return fmt.Errorf("%s: BSON type %s (vendor) vs %s (library)", path, vendorValue.Type, libValue.Type)
	}
	switch vendorValue.Type {
	case vendorbson.TypeEmbeddedDocument:
		return compareDocuments(path, documentShapeOf(goValue, false), vendorValue.Data, libValue.Data)
	case vendorbson.TypeArray:
		return compareDocuments(path, documentShapeOf(goValue, true), vendorValue.Data, libValue.Data)
	}
	if !bytes.Equal(vendorValue.Data, libValue.Data) {
		return fmt.Errorf("%s: %s value %x (vendor) vs %x (library)", path, vendorValue.Type, vendorValue.Data, libValue.Data)
	}
	return nil
}

// documentShapeOf reads the shape of a document (or, with isArray, an array)
// level from the Go value it was encoded from.
func documentShapeOf(goValue reflect.Value, isArray bool) documentShape {
	goValue = concreteValue(goValue)
	if !goValue.IsValid() || hasCustomEncoding(goValue.Type()) {
		return opaqueShape(isArray)
	}
	switch kind := goValue.Kind(); {
	case isArray && (kind == reflect.Slice || kind == reflect.Array) && goValue.Type().Elem().Kind() != reflect.Uint8:
		return sequenceShape(goValue, true, func(element reflect.Value) reflect.Value { return element })
	case isArray:
		return opaqueShape(true)
	case kind == reflect.Map:
		return mapShape(goValue)
	case kind == reflect.Struct:
		return structShape(goValue)
	case kind == reflect.Slice && isKeyValueStruct(goValue.Type().Elem()):
		// A bson.D (vendor or library): an ordered document whose element i
		// holds D[i].Value.
		return sequenceShape(goValue, false, func(element reflect.Value) reflect.Value { return element.Field(1) })
	}
	return opaqueShape(false)
}

// concreteValue follows pointers and interfaces to the value they hold,
// stopping at a type that encodes itself. A nil one yields the zero Value.
func concreteValue(goValue reflect.Value) reflect.Value {
	for goValue.IsValid() && (goValue.Kind() == reflect.Pointer || goValue.Kind() == reflect.Interface) {
		if goValue.IsNil() || hasCustomEncoding(goValue.Type()) {
			return goValue
		}
		goValue = goValue.Elem()
	}
	return goValue
}

// hasCustomEncoding reports a type whose encoding its own method decides; its
// levels compare raw. Matching by method name covers both libraries'
// Marshaler and ValueMarshaler interfaces.
func hasCustomEncoding(goType reflect.Type) bool {
	for _, methodName := range []string{"MarshalBSON", "MarshalBSONValue"} {
		if _, found := goType.MethodByName(methodName); found {
			return true
		}
		if goType.Kind() != reflect.Pointer {
			if _, found := reflect.PointerTo(goType).MethodByName(methodName); found {
				return true
			}
		}
	}
	return false
}

// isKeyValueStruct recognises the element type of a bson.D, primitive.E, in
// either library, by its shape rather than its package.
func isKeyValueStruct(goType reflect.Type) bool {
	return goType.Kind() == reflect.Struct && goType.NumField() == 2 &&
		goType.Field(0).Name == "Key" && goType.Field(0).Type.Kind() == reflect.String &&
		goType.Field(1).Name == "Value" && goType.Field(1).Type.Kind() == reflect.Interface
}

func sequenceShape(sequence reflect.Value, isArray bool, elementValue func(reflect.Value) reflect.Value) documentShape {
	return documentShape{
		isArray:       isArray,
		orderedPrefix: allInOrder,
		childValue: func(_ string, position int) reflect.Value {
			if position >= sequence.Len() {
				return reflect.Value{}
			}
			return elementValue(sequence.Index(position))
		},
	}
}

func mapShape(goMap reflect.Value) documentShape {
	entriesByKey := make(map[string]reflect.Value, goMap.Len())
	for _, mapKey := range goMap.MapKeys() {
		entriesByKey[mapKeyString(mapKey)] = goMap.MapIndex(mapKey)
	}
	// Extended JSON text spells each byte of a key that is not UTF-8 as
	// U+FFFD, so ExtJSON reads the key back in that form; index it too,
	// where no key already has that spelling.
	for _, mapKey := range goMap.MapKeys() {
		if textKey := textSpelling(mapKeyString(mapKey)); textKey != mapKeyString(mapKey) {
			if _, taken := entriesByKey[textKey]; !taken {
				entriesByKey[textKey] = goMap.MapIndex(mapKey)
			}
		}
	}
	return documentShape{
		orderedPrefix: func([]string) int { return 0 },
		childValue:    func(key string, _ int) reflect.Value { return entriesByKey[key] },
	}
}

// mapKeyString renders a map key as the element name the vendor writes for
// it. A key it renders differently (a TextMarshaler, say) misses the lookup
// and its element compares raw.
func mapKeyString(mapKey reflect.Value) string {
	if mapKey.Kind() == reflect.String {
		return mapKey.String()
	}
	return fmt.Sprint(mapKey.Interface())
}

// textSpelling is key as Extended JSON text reads back: every byte that does
// not start a valid UTF-8 sequence becomes U+FFFD.
func textSpelling(key string) string {
	if utf8.ValidString(key) {
		return key
	}
	var spelled strings.Builder
	for position := 0; position < len(key); {
		character, width := utf8.DecodeRuneInString(key[position:])
		spelled.WriteRune(character)
		position += width
	}
	return spelled.String()
}
