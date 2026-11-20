// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: ported from document.go; the error types and sentinels are unexported (the inventory lists none of them), the reader constructors are left out, ReadDocument joins it from bsoncore.go, and String and DebugString call the depth-limited renderer in render.go

package bsoncore

import (
	"errors"
	"fmt"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// validationError is the error type returned when validating a document or array.
type validationError string

func (ve validationError) Error() string { return string(ve) }

// newDocumentLengthError creates and returns an error for when the length of a document exceeds the
// bytes available.
func newDocumentLengthError(length, rem int) error {
	return lengthError("document", length, rem)
}

func lengthError(bufferType string, length, rem int) error {
	return validationError(fmt.Sprintf("%v length exceeds available bytes. length=%d remainingBytes=%d",
		bufferType, length, rem))
}

// insufficientBytesError indicates that there were not enough bytes to read the next component.
type insufficientBytesError struct {
	Source    []byte
	Remaining []byte
}

// newInsufficientBytesError creates a new insufficientBytesError with the given Document and
// remaining bytes.
func newInsufficientBytesError(src, rem []byte) insufficientBytesError {
	return insufficientBytesError{Source: src, Remaining: rem}
}

// Error implements the error interface.
func (ibe insufficientBytesError) Error() string {
	return "too few bytes to read next component"
}

// invalidDepthTraversalError is returned when attempting a recursive Lookup when one component of
// the path is neither an embedded document nor an array.
type invalidDepthTraversalError struct {
	Key  string
	Type bsontype.Type
}

func (idte invalidDepthTraversalError) Error() string {
	return fmt.Sprintf(
		"attempt to traverse into %s, but it's type is %s, not %s nor %s",
		idte.Key, idte.Type, bsontype.EmbeddedDocument, bsontype.Array,
	)
}

// errMissingNull is returned when a document or array's last byte is not null.
const errMissingNull validationError = "document or array end is missing null byte"

// errEmptyKey indicates that no key was provided to a Lookup method.
var errEmptyKey = errors.New("empty key provided")

// errElementNotFound indicates that an Element matching a certain condition does not exist.
var errElementNotFound = errors.New("element not found")

// errOutOfBounds indicates that an index provided to access something was invalid.
var errOutOfBounds = errors.New("out of bounds")

// Document is a raw bytes representation of a BSON document.
type Document []byte

// ReadDocument reads the next document from src: the document, the bytes after it and whether the
// bytes held a whole document. If they did not, the document is nil and the rest is src.
func ReadDocument(src []byte) (doc Document, rem []byte, ok bool) { return readLengthBytes(src) }

// Lookup searches the document, potentially recursively, for the given key. If there are multiple
// keys provided, this method will recurse down, as long as the top and intermediate nodes are
// either documents or arrays. If an error occurs or if the value doesn't exist, an empty Value is
// returned.
func (d Document) Lookup(key ...string) Value {
	val, _ := d.LookupErr(key...)
	return val
}

// LookupErr is the same as Lookup, except it returns an error in addition to an empty Value.
func (d Document) LookupErr(key ...string) (Value, error) {
	if len(key) < 1 {
		return Value{}, errEmptyKey
	}
	length, rem, ok := ReadLength(d)
	if !ok {
		return Value{}, newInsufficientBytesError(d, rem)
	}

	length -= 4

	var elem Element
	for length > 1 {
		elem, rem, ok = ReadElement(rem)
		length -= int32(len(elem))
		if !ok {
			return Value{}, newInsufficientBytesError(d, rem)
		}
		// We use `KeyBytes` rather than `Key` to avoid a needless string alloc.
		if string(elem.KeyBytes()) != key[0] {
			continue
		}
		if len(key) > 1 {
			tt := bsontype.Type(elem[0])
			switch tt {
			case bsontype.EmbeddedDocument:
				val, err := elem.Value().Document().LookupErr(key[1:]...)
				if err != nil {
					return Value{}, err
				}
				return val, nil
			case bsontype.Array:
				// Convert to Document to continue Lookup recursion.
				val, err := Document(elem.Value().Array()).LookupErr(key[1:]...)
				if err != nil {
					return Value{}, err
				}
				return val, nil
			default:
				return Value{}, invalidDepthTraversalError{Key: elem.Key(), Type: tt}
			}
		}
		return elem.ValueErr()
	}
	return Value{}, errElementNotFound
}

// Index searches for and retrieves the element at the given index. This method will panic if
// the document is invalid or if the index is out of bounds.
func (d Document) Index(index uint) Element {
	elem, err := d.IndexErr(index)
	if err != nil {
		// Mirrored contract (spec 5.1, S-4): Index panics where the vendor's does; IndexErr is the form that does not.
		panic(err)
	}
	return elem
}

// IndexErr searches for and retrieves the element at the given index.
func (d Document) IndexErr(index uint) (Element, error) {
	return indexErr(d, index)
}

func indexErr(b []byte, index uint) (Element, error) {
	length, rem, ok := ReadLength(b)
	if !ok {
		return nil, newInsufficientBytesError(b, rem)
	}

	length -= 4

	var current uint
	var elem Element
	for length > 1 {
		elem, rem, ok = ReadElement(rem)
		length -= int32(len(elem))
		if !ok {
			return nil, newInsufficientBytesError(b, rem)
		}
		if current != index {
			current++
			continue
		}
		return elem, nil
	}
	return nil, errOutOfBounds
}

// DebugString outputs a human readable version of Document. It will attempt to stringify the
// valid components of the document even if the entire document is not valid. A document nested
// deeper than the library ceiling renders as "<malformed>".
func (d Document) DebugString() string {
	var rendering renderer
	return rendering.refuseIfTooDeep(rendering.documentDebug(d), malformedText)
}

// String outputs an ExtendedJSON version of Document. If the document is not valid, this method
// returns an empty string. So does a document nested deeper than the library ceiling.
func (d Document) String() string {
	var rendering renderer
	return rendering.refuseIfTooDeep(rendering.document(d), "")
}

// Elements returns this document as a slice of elements. The returned slice will contain valid
// elements. If the document is not valid, the elements up to the invalid point will be returned
// along with an error.
func (d Document) Elements() ([]Element, error) {
	length, rem, ok := ReadLength(d)
	if !ok {
		return nil, newInsufficientBytesError(d, rem)
	}

	length -= 4

	var elem Element
	var elems []Element
	for length > 1 {
		elem, rem, ok = ReadElement(rem)
		length -= int32(len(elem))
		if !ok {
			return elems, newInsufficientBytesError(d, rem)
		}
		if err := elem.Validate(); err != nil {
			return elems, err
		}
		elems = append(elems, elem)
	}
	return elems, nil
}

// Values returns this document as a slice of values. The returned slice will contain valid values.
// If the document is not valid, the values up to the invalid point will be returned along with an
// error.
func (d Document) Values() ([]Value, error) {
	return values(d)
}

func values(b []byte) ([]Value, error) {
	length, rem, ok := ReadLength(b)
	if !ok {
		return nil, newInsufficientBytesError(b, rem)
	}

	length -= 4

	var elem Element
	var vals []Value
	for length > 1 {
		elem, rem, ok = ReadElement(rem)
		length -= int32(len(elem))
		if !ok {
			return vals, newInsufficientBytesError(b, rem)
		}
		if err := elem.Value().Validate(); err != nil {
			return vals, err
		}
		vals = append(vals, elem.Value())
	}
	return vals, nil
}

// Validate validates the document and ensures the elements contained within are valid.
func (d Document) Validate() error {
	length, rem, ok := ReadLength(d)
	if !ok {
		return newInsufficientBytesError(d, rem)
	}
	if int(length) > len(d) {
		return newDocumentLengthError(int(length), len(d))
	}
	if length < 5 {
		// The smallest document is its length and its terminator. The vendor
		// indexes d[length-1] here, which panics at length zero.
		return errMissingNull
	}
	if d[length-1] != 0x00 {
		return errMissingNull
	}

	length -= 4
	var elem Element

	for length > 1 {
		elem, rem, ok = ReadElement(rem)
		length -= int32(len(elem))
		if !ok {
			return newInsufficientBytesError(d, rem)
		}
		err := elem.Validate()
		if err != nil {
			return err
		}
	}

	if len(rem) < 1 || rem[0] != 0x00 {
		return errMissingNull
	}
	return nil
}
