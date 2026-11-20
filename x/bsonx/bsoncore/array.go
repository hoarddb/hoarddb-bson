// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: ported from array.go; the reader constructor is left out, the length error is unexported, and String and DebugString call the depth-limited renderer in render.go

package bsoncore

import "fmt"

// newArrayLengthError creates and returns an error for when the length of an array exceeds the
// bytes available.
func newArrayLengthError(length, rem int) error {
	return lengthError("array", length, rem)
}

// Array is a raw bytes representation of a BSON array.
type Array []byte

// Index searches for and retrieves the value at the given index. This method will panic if
// the array is invalid or if the index is out of bounds.
func (a Array) Index(index uint) Value {
	value, err := a.IndexErr(index)
	if err != nil {
		// Mirrored contract (spec 5.1, S-4): Index panics where the vendor's does; IndexErr is the form that does not.
		panic(err)
	}
	return value
}

// IndexErr searches for and retrieves the value at the given index.
func (a Array) IndexErr(index uint) (Value, error) {
	elem, err := indexErr(a, index)
	if err != nil {
		return Value{}, err
	}
	return elem.Value(), err
}

// DebugString outputs a human readable version of Array. It will attempt to stringify the
// valid components of the array even if the entire array is not valid. An array nested deeper
// than the library ceiling renders as "<malformed>".
func (a Array) DebugString() string {
	var rendering renderer
	return rendering.refuseIfTooDeep(rendering.arrayDebug(a), malformedText)
}

// String outputs an ExtendedJSON version of Array. If the Array is not valid, this method
// returns an empty string. So does an array nested deeper than the library ceiling.
func (a Array) String() string {
	var rendering renderer
	return rendering.refuseIfTooDeep(rendering.array(a), "")
}

// Values returns this array as a slice of values. The returned slice will contain valid values.
// If the array is not valid, the values up to the invalid point will be returned along with an
// error.
func (a Array) Values() ([]Value, error) {
	return values(a)
}

// Validate validates the array and ensures the elements contained within are valid.
func (a Array) Validate() error {
	length, rem, ok := ReadLength(a)
	if !ok {
		return newInsufficientBytesError(a, rem)
	}
	if int(length) > len(a) {
		return newArrayLengthError(int(length), len(a))
	}
	if length < 5 {
		// The smallest document is its length and its terminator. The vendor
		// indexes a[length-1] here, which panics at length zero.
		return errMissingNull
	}
	if a[length-1] != 0x00 {
		return errMissingNull
	}

	length -= 4
	var elem Element

	var keyNum int64
	for length > 1 {
		elem, rem, ok = ReadElement(rem)
		length -= int32(len(elem))
		if !ok {
			return newInsufficientBytesError(a, rem)
		}

		// validate element
		err := elem.Validate()
		if err != nil {
			return err
		}

		// validate keys increase numerically
		if fmt.Sprint(keyNum) != elem.Key() {
			return fmt.Errorf("array key %q is out of order or invalid", elem.Key())
		}
		keyNum++
	}

	if len(rem) < 1 || rem[0] != 0x00 {
		return errMissingNull
	}
	return nil
}
