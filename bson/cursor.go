// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

import (
	"bytes"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// View is one value inside a Document: its element type and its value
// bytes, which are the document's own, uncopied. The zero View is no value.
// The caller must not modify the bytes.
type View struct {
	valueType  bsontype.Type
	valueBytes []byte
}

// Type is the value's element type, or 0 for the zero View.
func (view View) Type() bsontype.Type {
	return view.valueType
}

// Bytes are the value's bytes as they sit in the document: for a string its
// length prefix, text and terminator, for an embedded document or an array
// the whole of it, length prefix to terminator.
func (view View) Bytes() []byte {
	return view.valueBytes
}

// Cursor reads the elements of a Document in order. It is a value to keep
// on the stack: it allocates nothing, compares keys as bytes, and skips each
// value by its length without reading inside it.
//
//	cursor := document.Cursor()
//	for cursor.Next() {
//		if cursor.KeyEquals("email") { ... cursor.Value() ... }
//	}
//	if err := cursor.Err(); err != nil { ... }
type Cursor struct {
	elements []byte // the elements not read yet
	offset   int    // where elements starts in the document's bytes
	key      []byte
	value    View
	err      error
}

// Cursor starts a cursor before the document's first element. On the zero
// Document, the cursor has no elements and Err matches ErrZeroDocument.
func (document Document) Cursor() Cursor {
	if document.b == nil {
		return Cursor{err: ErrZeroDocument}
	}
	return cursorOver(document.b)
}

// Next moves to the next element, and reports false at the end or at bytes
// that are not an element. A Document from Validate never has such bytes;
// one from TrustStored was framed but not walked, and may. Err tells the
// two ends apart.
func (cursor *Cursor) Next() bool {
	if len(cursor.elements) == 0 {
		cursor.key, cursor.value = nil, View{}
		return false
	}
	key, value, elementLength, reason := readElement(cursor.elements)
	if reason != "" {
		cursor.err = malformedAt(cursor.offset, reason)
		cursor.elements, cursor.key, cursor.value = nil, nil, View{}
		return false
	}
	cursor.key, cursor.value = key, value
	cursor.elements = cursor.elements[elementLength:]
	cursor.offset += elementLength
	return true
}

// Key is the current element's name, without its terminator, uncopied.
func (cursor *Cursor) Key() []byte {
	return cursor.key
}

// KeyEquals reports whether the current element's name is name, byte for
// byte, without allocating.
func (cursor *Cursor) KeyEquals(name string) bool {
	return string(cursor.key) == name
}

// Value is the current element's value.
func (cursor *Cursor) Value() View {
	return cursor.value
}

// Err is nil when Next stopped at the end of the document. Otherwise it is
// a *ValidationError matching ErrMalformedDocument, at the offset of the
// element Next could not read, or ErrZeroDocument for a cursor of the zero
// Document.
func (cursor *Cursor) Err() error {
	return cursor.err
}

// elementsOf is the element bytes of a document or an array, between its
// length prefix and its terminator. Its framing was checked by whoever made
// the bytes a Document or measured them as a value (valueLengthOf), so ok
// is false only for bytes shorter than an empty document.
func elementsOf(documentBytes []byte) ([]byte, bool) {
	if len(documentBytes) < minDocumentLength {
		return nil, false
	}
	return documentBytes[4 : len(documentBytes)-1], true
}

// readElement reads the element at the start of elements: its key, its
// value and its whole length. A non-empty reason means the bytes are not an
// element; reasons are constants, so reading allocates nothing.
func readElement(elements []byte) (key []byte, value View, elementLength int, reason string) {
	valueType := bsontype.Type(elements[0])
	keyLength := bytes.IndexByte(elements[1:], 0)
	if keyLength < 0 {
		return nil, View{}, 0, "an element name has no terminator"
	}
	valueStart := 1 + keyLength + 1
	valueLength, reason := valueLengthOf(elements[valueStart:], valueType)
	if reason != "" {
		return nil, View{}, 0, reason
	}
	elementLength = valueStart + valueLength
	return elements[1 : 1+keyLength], View{valueType: valueType, valueBytes: elements[valueStart:elementLength]}, elementLength, ""
}

// Smallest lengths a length prefix may give: a code-with-scope holds its
// own length, an empty string's length and terminator, and an empty scope.
const (
	minStringLength        = 1
	minCodeWithScopeLength = 4 + 4 + 1 + minDocumentLength
)

// valueLengthOf is how many bytes a value of valueType takes at the start of
// valueBytes, or a reason it does not fit. Lengths are compared against
// what remains, never added to, so no prefix can overflow an int.
func valueLengthOf(valueBytes []byte, valueType bsontype.Type) (int, string) {
	switch valueType {
	case bsontype.Undefined, bsontype.Null, bsontype.MinKey, bsontype.MaxKey:
		return 0, ""
	case bsontype.Boolean:
		return fitsFixed(valueBytes, 1)
	case bsontype.Int32:
		return fitsFixed(valueBytes, 4)
	case bsontype.Double, bsontype.DateTime, bsontype.Timestamp, bsontype.Int64:
		return fitsFixed(valueBytes, 8)
	case bsontype.ObjectID:
		return fitsFixed(valueBytes, 12)
	case bsontype.Decimal128:
		return fitsFixed(valueBytes, 16)
	case bsontype.String, bsontype.JavaScript, bsontype.Symbol:
		return fitsPrefixed(valueBytes, minStringLength, 4, 0)
	case bsontype.DBPointer:
		return fitsPrefixed(valueBytes, minStringLength, 4, 12)
	case bsontype.Binary:
		return fitsPrefixed(valueBytes, 0, 5, 0)
	case bsontype.EmbeddedDocument, bsontype.Array:
		return fitsPrefixed(valueBytes, minDocumentLength, 0, 0)
	case bsontype.CodeWithScope:
		return fitsPrefixed(valueBytes, minCodeWithScopeLength, 0, 0)
	case bsontype.Regex:
		patternLength := bytes.IndexByte(valueBytes, 0)
		if patternLength < 0 {
			return 0, "a regular expression has no terminator"
		}
		optionsLength := bytes.IndexByte(valueBytes[patternLength+1:], 0)
		if optionsLength < 0 {
			return 0, "a regular expression's options have no terminator"
		}
		return patternLength + 1 + optionsLength + 1, ""
	}
	return 0, "the element's type byte is not a BSON type"
}

// fitsFixed is a value of size bytes.
func fitsFixed(valueBytes []byte, size int) (int, string) {
	if len(valueBytes) < size {
		return 0, "a fixed-size value runs past the end of its document"
	}
	return size, ""
}

// fitsPrefixed is a value led by an int32 length of at least minimum, which
// counts the value's own bytes except before (bytes ahead of what the
// length counts, the prefix among them) and after (a fixed tail, as a
// DBPointer's object id).
func fitsPrefixed(valueBytes []byte, minimum, before, after int) (int, string) {
	length, ok := lengthAt(valueBytes, 0)
	if !ok || length < minimum {
		return 0, "a value's length prefix is negative or too small"
	}
	if len(valueBytes)-before-after < length {
		return 0, "a value's length prefix runs past the end of its document"
	}
	return before + length + after, ""
}
