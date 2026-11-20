// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsoncodec/bsoncodec.go (ValueDecoderError), struct_codec.go (DecodeError, newDecodeError), registry.go (ErrNoDecoder, ErrNoTypeMapEntry), default_value_decoders.go (decodeBinaryError, errCannotTruncate) and bson/decoder.go (ErrDecodeToNil): the types are unexported, none being in the inventory, and keep the vendor's Error text and Unwrap chain.

package decode

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// errDecodeToNil is the vendor's ErrDecodeToNil.
var errDecodeToNil = errors.New("cannot Decode to nil value")

// errCannotTruncate refuses a double with a fraction for an integer or a
// float32 that would lose precision, unless the field is tagged truncate.
var errCannotTruncate = errors.New("float64 can only be truncated to a lower precision type when truncation is enabled")

// valueDecoderError is the vendor's ValueDecoderError: a decoder handed a
// Go value it cannot set or does not decode. typeNames are the vendor's
// type names, which for the hook interfaces are its bsoncodec names.
type valueDecoderError struct {
	name      string
	typeNames []string
	kinds     []reflect.Kind
	received  reflect.Value
}

func (refusal valueDecoderError) Error() string {
	typeKinds := make([]string, 0, len(refusal.typeNames)+len(refusal.kinds))
	typeKinds = append(typeKinds, refusal.typeNames...)
	for _, kind := range refusal.kinds {
		if kind == reflect.Map {
			typeKinds = append(typeKinds, "map[string]*")
			continue
		}
		typeKinds = append(typeKinds, kind.String())
	}
	received := refusal.received.Kind().String()
	if refusal.received.IsValid() {
		received = refusal.received.Type().String()
	}
	return fmt.Sprintf("%s can only decode valid and settable %s, but got %s", refusal.name, strings.Join(typeKinds, ", "), received)
}

// decodeError is the vendor's DecodeError: an error inside a document,
// with the keys down to it, innermost first.
type decodeError struct {
	keys    []string
	wrapped error
}

func (refusal *decodeError) Unwrap() error {
	return refusal.wrapped
}

func (refusal *decodeError) Error() string {
	keyPath := make([]string, 0, len(refusal.keys))
	for position := len(refusal.keys) - 1; position >= 0; position-- {
		keyPath = append(keyPath, refusal.keys[position])
	}
	return fmt.Sprintf("error decoding key %s: %v", strings.Join(keyPath, "."), refusal.wrapped)
}

// newDecodeError adds key to the path of the decodeError in original's
// chain, and returns that decodeError, dropping whatever wraps it, as the
// vendor does; an error with none in its chain gets a new one.
func newDecodeError(key string, original error) error {
	var refusal *decodeError
	if !errors.As(original, &refusal) {
		return &decodeError{keys: []string{key}, wrapped: original}
	}
	refusal.keys = append(refusal.keys, key)
	return refusal
}

// noDecoderError is the vendor's ErrNoDecoder.
type noDecoderError struct {
	goType reflect.Type
}

func (refusal noDecoderError) Error() string {
	return "no decoder found for " + refusal.goType.String()
}

// noTypeMapEntryError is the vendor's ErrNoTypeMapEntry: an element type
// an interface{} cannot hold.
type noTypeMapEntryError struct {
	valueType bsontype.Type
}

func (refusal noTypeMapEntryError) Error() string {
	return "no type map entry found for " + refusal.valueType.String()
}

// decodeBinaryError refuses a binary subtype other than 0x00 and 0x02 for a
// string, a []byte or a symbol.
type decodeBinaryError struct {
	subtype  byte
	typeName string
}

func (refusal decodeBinaryError) Error() string {
	return fmt.Sprintf("only binary values with subtype 0x00 or 0x02 can be decoded into %s, but got subtype %v", refusal.typeName, refusal.subtype)
}
