// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package bsoncore appends and reads BSON elements on byte slices, with no
// reflection and no validation. Every exported name mirrors one the API
// inventory lists, with the same signature and the same output for the
// same input, including the inputs it panics on (spec section 5.1). The one
// exception is Array, which no call site names but Value.Array returns, so
// Value cannot be mirrored whole without it. Code that needs a checked
// document uses bson.Builder or bson.Validate.
package bsoncore

import (
	"encoding/binary"
	"errors"
	"strconv"
	"strings"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/wire"
)

// invalidKeyPanicMessage is AppendHeader's panic value, the vendor's text,
// because a recovered panic is an output too.
const invalidKeyPanicMessage = "BSON element keys cannot contain null bytes"

// lengthBytes is the size of a BSON length prefix.
const lengthBytes = 4

// AppendHeader appends an element's type byte and its key, with the key's
// zero terminator.
//
// It panics if key contains a zero byte, which a cstring cannot hold. That
// panic is the mirrored contract (spec 5.1: same inputs, same outputs), so
// it is kept: the caller guarantees the key. Code that cannot guarantee its
// keys uses bson.Builder, whose methods return bson.ErrInvalidKey instead.
func AppendHeader(dst []byte, t bsontype.Type, key string) []byte {
	if strings.IndexByte(key, 0) >= 0 {
		panic(invalidKeyPanicMessage)
	}
	dst = append(dst, byte(t))
	dst = append(dst, key...)
	return append(dst, 0)
}

// AppendDoubleElement appends a double element.
func AppendDoubleElement(dst []byte, key string, f float64) []byte {
	return wire.AppendDouble(AppendHeader(dst, bsontype.Double, key), f)
}

// AppendStringElement appends a string element. The value is not checked:
// it may hold zero bytes and invalid UTF-8.
func AppendStringElement(dst []byte, key, val string) []byte {
	return wire.AppendString(AppendHeader(dst, bsontype.String, key), val)
}

// AppendDocumentStart reserves a document's length prefix and returns the
// index it starts at, for AppendDocumentEnd.
func AppendDocumentStart(dst []byte) (index int32, b []byte) {
	return reserveLength(dst)
}

// AppendDocumentEnd appends a document's terminator and writes its length
// at index, which AppendDocumentStart returned. An index with fewer than 4
// bytes after it is an error. A negative index panics on the slice bounds,
// as the vendor's does.
func AppendDocumentEnd(dst []byte, index int32) ([]byte, error) {
	if int(index) > len(dst)-lengthBytes {
		return dst, errors.New("not enough bytes available after index to write length")
	}
	dst = append(dst, 0)
	documentLength := int32(len(dst[index:]))
	wire.PutLength(dst, int(index), documentLength)
	return dst, nil
}

// AppendDocumentElement appends an embedded-document element whose value
// is doc, verbatim and unchecked.
func AppendDocumentElement(dst []byte, key string, doc []byte) []byte {
	return append(AppendHeader(dst, bsontype.EmbeddedDocument, key), doc...)
}

// BuildDocument appends a document made of elems, each a whole element,
// in order and unchecked.
func BuildDocument(dst []byte, elems ...[]byte) []byte {
	index, dst := reserveLength(dst)
	for _, element := range elems {
		dst = append(dst, element...)
	}
	dst = append(dst, 0)
	wire.PutLength(dst, int(index), int32(len(dst[index:])))
	return dst
}

// AppendArrayStart reserves an array's length prefix and returns the index
// it starts at, for AppendArrayEnd.
func AppendArrayStart(dst []byte) (index int32, b []byte) {
	return reserveLength(dst)
}

// AppendArrayEnd is AppendDocumentEnd for an array: an array is framed as a
// document.
func AppendArrayEnd(dst []byte, index int32) ([]byte, error) {
	return AppendDocumentEnd(dst, index)
}

// AppendArrayElement appends an array element whose value is arr, verbatim
// and unchecked.
func AppendArrayElement(dst []byte, key string, arr []byte) []byte {
	return append(AppendHeader(dst, bsontype.Array, key), arr...)
}

// BuildArray appends an array of values, keyed "0", "1", ... in order.
// Each value's type and bytes are written as they are, unchecked.
func BuildArray(dst []byte, values ...Value) []byte {
	index, dst := reserveLength(dst)
	for position, arrayValue := range values {
		// The key is the decimal position, written in place: it never holds a
		// zero byte, so AppendHeader's check has nothing to refuse.
		dst = append(dst, byte(arrayValue.Type))
		dst = strconv.AppendInt(dst, int64(position), 10)
		dst = append(dst, 0)
		dst = append(dst, arrayValue.Data...)
	}
	dst = append(dst, 0)
	wire.PutLength(dst, int(index), int32(len(dst[index:])))
	return dst
}

// AppendBinaryElement appends a binary element. Subtype 0x02 carries the
// deprecated inner length.
func AppendBinaryElement(dst []byte, key string, subtype byte, b []byte) []byte {
	return wire.AppendBinary(AppendHeader(dst, bsontype.Binary, key), subtype, b)
}

// ReadBinary reads a binary value: its subtype, its payload and the bytes
// after it. A subtype 0x02 value's payload is bounded by its inner length.
// If the bytes cannot hold the value, ok is false and rem is src.
func ReadBinary(src []byte) (subtype byte, bin []byte, rem []byte, ok bool) {
	length, rem, ok := ReadLength(src)
	if !ok || len(rem) < 1 {
		return 0, nil, src, false
	}
	subtype, rem = rem[0], rem[1:]
	if len(rem) < int(length) {
		return 0, nil, src, false
	}
	if subtype == 0x02 {
		// The vendor checks the outer length against the bytes, then reads
		// only the inner one: an outer length that disagrees with it is not
		// refused here.
		length, rem, ok = ReadLength(rem)
		if !ok || len(rem) < int(length) {
			return 0, nil, src, false
		}
	}
	return subtype, rem[:length], rem[length:], true
}

// AppendBooleanElement appends a boolean element: one byte, 0x01 for true and
// 0x00 for false.
func AppendBooleanElement(dst []byte, key string, value bool) []byte {
	dst = AppendHeader(dst, bsontype.Boolean, key)
	if value {
		return append(dst, 0x01)
	}
	return append(dst, 0x00)
}

// ReadBoolean reads a boolean value: true only for the byte 0x01, as the
// vendor's does. With no bytes, ok is false and the bytes are returned as
// they are.
func ReadBoolean(src []byte) (value bool, rem []byte, ok bool) { return readBoolean(src) }

// AppendObjectIDElement appends an ObjectID element.
func AppendObjectIDElement(dst []byte, key string, oid primitive.ObjectID) []byte {
	return append(AppendHeader(dst, bsontype.ObjectID, key), oid[:]...)
}

// AppendDateTimeElement appends a DateTime element: dt is milliseconds
// since the Unix epoch.
func AppendDateTimeElement(dst []byte, key string, dt int64) []byte {
	return wire.AppendInt64(AppendHeader(dst, bsontype.DateTime, key), dt)
}

// AppendInt32Element appends an int32 element.
func AppendInt32Element(dst []byte, key string, i32 int32) []byte {
	return wire.AppendInt32(AppendHeader(dst, bsontype.Int32, key), i32)
}

// ReadInt32 reads a little-endian int32. With fewer than 4 bytes, ok is
// false and the bytes are returned as they are.
func ReadInt32(src []byte) (int32, []byte, bool) {
	if len(src) < 4 {
		return 0, src, false
	}
	return int32(binary.LittleEndian.Uint32(src)), src[4:], true
}

// AppendInt64Element appends an int64 element.
func AppendInt64Element(dst []byte, key string, i64 int64) []byte {
	return wire.AppendInt64(AppendHeader(dst, bsontype.Int64, key), i64)
}

// ReadInt64 reads a little-endian int64. With fewer than 8 bytes, ok is
// false and the bytes are returned as they are.
func ReadInt64(src []byte) (int64, []byte, bool) {
	if len(src) < 8 {
		return 0, src, false
	}
	return int64(binary.LittleEndian.Uint64(src)), src[8:], true
}

// ReadLength reads a BSON length. With fewer than 4 bytes, it returns 0,
// src and false. A negative length returns that length, the bytes after
// it, and false: the vendor advances past a negative length while
// refusing it, and so does this.
func ReadLength(src []byte) (int32, []byte, bool) {
	length, rem, ok := ReadInt32(src)
	if length < 0 {
		return length, rem, false
	}
	return length, rem, ok
}

// reserveLength appends a zeroed length prefix and returns its index.
func reserveLength(dst []byte) (int32, []byte) {
	return int32(len(dst)), append(dst, 0, 0, 0, 0)
}
