// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: the unexported readers Value, Element and Document need, ported from bsoncore.go; the vendor exports most of them, the inventory lists none, so they are unexported here

package bsoncore

import (
	"bytes"
	"encoding/binary"
	"math"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// readTypedValue reads the next value of type t from src (the vendor's ReadValue).
func readTypedValue(src []byte, t bsontype.Type) (Value, []byte, bool) {
	data, rem, ok := readValue(src, t)
	if !ok {
		return Value{}, src, false
	}
	return Value{Type: t, Data: data}, rem, true
}

func readDouble(src []byte) (float64, []byte, bool) {
	bits, src, ok := readu64(src)
	if !ok {
		return 0, src, false
	}
	return math.Float64frombits(bits), src, true
}

func readString(src []byte) (string, []byte, bool) {
	return readstring(src)
}

func readArray(src []byte) (arr Array, rem []byte, ok bool) { return readLengthBytes(src) }

func readObjectID(src []byte) (primitive.ObjectID, []byte, bool) {
	if len(src) < 12 {
		return primitive.ObjectID{}, src, false
	}
	var oid primitive.ObjectID
	copy(oid[:], src[0:12])
	return oid, src[12:], true
}

func readBoolean(src []byte) (bool, []byte, bool) {
	if len(src) < 1 {
		return false, src, false
	}

	return src[0] == 0x01, src[1:], true
}

func readDateTime(src []byte) (int64, []byte, bool) { return ReadInt64(src) }

func readRegex(src []byte) (pattern, options string, rem []byte, ok bool) {
	pattern, rem, ok = readcstring(src)
	if !ok {
		return "", "", src, false
	}
	options, rem, ok = readcstring(rem)
	if !ok {
		return "", "", src, false
	}
	return pattern, options, rem, true
}

func readDBPointer(src []byte) (ns string, oid primitive.ObjectID, rem []byte, ok bool) {
	ns, rem, ok = readstring(src)
	if !ok {
		return "", primitive.ObjectID{}, src, false
	}
	oid, rem, ok = readObjectID(rem)
	if !ok {
		return "", primitive.ObjectID{}, src, false
	}
	return ns, oid, rem, true
}

func readJavaScript(src []byte) (js string, rem []byte, ok bool) { return readstring(src) }

func readSymbol(src []byte) (symbol string, rem []byte, ok bool) { return readstring(src) }

func readCodeWithScope(src []byte) (code string, scope []byte, rem []byte, ok bool) {
	length, rem, ok := ReadLength(src)
	if !ok || len(src) < int(length) {
		return "", nil, src, false
	}

	code, rem, ok = readstring(rem)
	if !ok {
		return "", nil, src, false
	}

	scope, rem, ok = ReadDocument(rem)
	if !ok {
		return "", nil, src, false
	}
	return code, scope, rem, true
}

func readTimestamp(src []byte) (t, i uint32, rem []byte, ok bool) {
	i, rem, ok = readu32(src)
	if !ok {
		return 0, 0, src, false
	}
	t, rem, ok = readu32(rem)
	if !ok {
		return 0, 0, src, false
	}
	return t, i, rem, true
}

func readDecimal128(src []byte) (primitive.Decimal128, []byte, bool) {
	l, rem, ok := readu64(src)
	if !ok {
		return primitive.Decimal128{}, src, false
	}

	h, rem, ok := readu64(rem)
	if !ok {
		return primitive.Decimal128{}, src, false
	}

	return primitive.NewDecimal128(h, l), rem, true
}

// valueLength will determine the length of the next value contained in src as if it
// is type t. The returned bool will be false if there are not enough bytes in src for
// a value of type t.
//
// The length is an int64: a declared length near the int32 maximum plus its
// own header bytes would wrap a negative int32 and slip past a bounds check.
func valueLength(src []byte, t bsontype.Type) (int64, bool) {
	var length int64
	ok := true
	switch t {
	case bsontype.Array, bsontype.EmbeddedDocument, bsontype.CodeWithScope:
		length, ok = declaredLength(src, 0)
	case bsontype.Binary:
		length, ok = declaredLength(src, 4+1) // binary length + subtype byte
	case bsontype.Boolean:
		length = 1
	case bsontype.DBPointer:
		length, ok = declaredLength(src, 4+12) // string length + ObjectID length
	case bsontype.DateTime, bsontype.Double, bsontype.Int64, bsontype.Timestamp:
		length = 8
	case bsontype.Decimal128:
		length = 16
	case bsontype.Int32:
		length = 4
	case bsontype.JavaScript, bsontype.String, bsontype.Symbol:
		length, ok = declaredLength(src, 4)
	case bsontype.MaxKey, bsontype.MinKey, bsontype.Null, bsontype.Undefined:
		length = 0
	case bsontype.ObjectID:
		length = 12
	case bsontype.Regex:
		regex := bytes.IndexByte(src, 0x00)
		if regex < 0 {
			ok = false
			break
		}
		pattern := bytes.IndexByte(src[regex+1:], 0x00)
		if pattern < 0 {
			ok = false
			break
		}
		length = int64(regex) + 1 + int64(pattern) + 1
	default:
		ok = false
	}

	return length, ok
}

// declaredLength reads the length prefix at the start of src and adds the
// extra bytes the value carries beyond it, without wrapping.
func declaredLength(src []byte, extra int64) (int64, bool) {
	length, _, ok := ReadLength(src)
	return int64(length) + extra, ok
}

func readValue(src []byte, t bsontype.Type) ([]byte, []byte, bool) {
	length, ok := valueLength(src, t)
	if !ok || length < 0 || length > int64(len(src)) {
		return nil, src, false
	}

	return src[:length], src[length:], true
}

func readu32(src []byte) (uint32, []byte, bool) {
	if len(src) < 4 {
		return 0, src, false
	}

	return binary.LittleEndian.Uint32(src), src[4:], true
}

func readu64(src []byte) (uint64, []byte, bool) {
	if len(src) < 8 {
		return 0, src, false
	}

	return binary.LittleEndian.Uint64(src), src[8:], true
}

func readcstring(src []byte) (string, []byte, bool) {
	idx := bytes.IndexByte(src, 0x00)
	if idx < 0 {
		return "", src, false
	}
	return string(src[:idx]), src[idx+1:], true
}

func readstring(src []byte) (string, []byte, bool) {
	l, rem, ok := ReadLength(src)
	if !ok {
		return "", src, false
	}
	if len(src[4:]) < int(l) || l == 0 {
		return "", src, false
	}

	return string(rem[:l-1]), rem[l:], true
}

// readLengthBytes attempts to read a length and that number of bytes. This
// function requires that the length include the four bytes for itself.
func readLengthBytes(src []byte) ([]byte, []byte, bool) {
	l, _, ok := ReadLength(src)
	if !ok {
		return nil, src, false
	}
	if l < 4 {
		return nil, src, false
	}
	if len(src) < int(l) {
		return nil, src, false
	}
	return src[:l], src[l:], true
}
