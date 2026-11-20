// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: ported from element.go; the error types and sentinels are unexported (the inventory lists none of them), ReadElement joins it from bsoncore.go, and String and DebugString call the depth-limited renderer in render.go

package bsoncore

import (
	"bytes"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// malformedElementError represents a class of errors that Element methods return.
type malformedElementError string

func (mee malformedElementError) Error() string { return string(mee) }

// errElementMissingKey is returned when an Element is missing a key.
const errElementMissingKey malformedElementError = "element is missing key"

// errElementMissingType is returned when an Element is missing a type.
const errElementMissingType malformedElementError = "element is missing type"

// Element is a raw bytes representation of a BSON element.
type Element []byte

// Key returns the key for this element. If the element is not valid, this method returns an empty
// string. If knowing if the element is valid is important, use KeyErr.
func (e Element) Key() string {
	key, _ := e.KeyErr()
	return key
}

// KeyBytes returns the key for this element as a []byte. If the element is not valid, this method
// returns an empty string. If knowing if the element is valid is important, use KeyErr. This method
// will not include the null byte at the end of the key in the slice of bytes.
func (e Element) KeyBytes() []byte {
	key, _ := e.KeyBytesErr()
	return key
}

// KeyErr returns the key for this element, returning an error if the element is not valid.
func (e Element) KeyErr() (string, error) {
	key, err := e.KeyBytesErr()
	return string(key), err
}

// KeyBytesErr returns the key for this element as a []byte, returning an error if the element is
// not valid.
func (e Element) KeyBytesErr() ([]byte, error) {
	if len(e) == 0 {
		return nil, errElementMissingType
	}
	idx := bytes.IndexByte(e[1:], 0x00)
	if idx == -1 {
		return nil, errElementMissingKey
	}
	return e[1 : idx+1], nil
}

// Validate ensures the element is a valid BSON element.
func (e Element) Validate() error {
	if len(e) < 1 {
		return errElementMissingType
	}
	idx := bytes.IndexByte(e[1:], 0x00)
	if idx == -1 {
		return errElementMissingKey
	}
	return Value{Type: bsontype.Type(e[0]), Data: e[idx+2:]}.Validate()
}

// CompareKey will compare this element's key to key. This method makes it easy to compare keys
// without needing to allocate a string. The key may be null terminated. If a valid key cannot be
// read this method will return false.
func (e Element) CompareKey(key []byte) bool {
	if len(e) < 2 {
		return false
	}
	idx := bytes.IndexByte(e[1:], 0x00)
	if idx == -1 {
		return false
	}
	if index := bytes.IndexByte(key, 0x00); index > -1 {
		key = key[:index]
	}
	return bytes.Equal(e[1:idx+1], key)
}

// Value returns the value of this element. If the element is not valid, this method returns an
// empty Value. If knowing if the element is valid is important, use ValueErr.
func (e Element) Value() Value {
	val, _ := e.ValueErr()
	return val
}

// ValueErr returns the value for this element, returning an error if the element is not valid.
func (e Element) ValueErr() (Value, error) {
	if len(e) == 0 {
		return Value{}, errElementMissingType
	}
	idx := bytes.IndexByte(e[1:], 0x00)
	if idx == -1 {
		return Value{}, errElementMissingKey
	}

	val, rem, exists := readTypedValue(e[idx+2:], bsontype.Type(e[0]))
	if !exists {
		return Value{}, newInsufficientBytesError(e, rem)
	}
	return val, nil
}

// String implements the fmt.String interface. The output will be in extended JSON format. An
// element nested deeper than the library ceiling renders as the empty string.
func (e Element) String() string {
	var rendering renderer
	return rendering.refuseIfTooDeep(rendering.element(e), "")
}

// DebugString outputs a human readable version of Element. It will attempt to stringify the
// valid components of the element even if the entire element is not valid.
func (e Element) DebugString() string {
	var rendering renderer
	return rendering.refuseIfTooDeep(rendering.elementDebug(e), malformedText)
}

// ReadElement reads the next element from src: the element, the bytes after it and whether the
// bytes held a whole element. If they did not, the element is nil and the rest is src.
func ReadElement(src []byte) (Element, []byte, bool) {
	if len(src) < 1 {
		return nil, src, false
	}
	t := bsontype.Type(src[0])
	idx := bytes.IndexByte(src[1:], 0x00)
	if idx == -1 {
		return nil, src, false
	}
	length, ok := valueLength(src[idx+2:], t) // We add 2 here because we called IndexByte with src[1:]
	if !ok {
		return nil, src, false
	}
	elemLength := int64(1+idx+1) + length
	if elemLength < 0 || elemLength > int64(len(src)) {
		return nil, src, false
	}
	return src[:elemLength], src[elemLength:], true
}
