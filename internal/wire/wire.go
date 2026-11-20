// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package wire appends BSON value bytes. It is shared by the mirrored
// bsoncore functions and bson.Builder, so the two cannot drift apart. It
// checks nothing: its callers decide what is valid.
package wire

import (
	"encoding/binary"
	"math"
)

// AppendInt32 appends a little-endian int32, the form of every BSON length.
func AppendInt32(dst []byte, number int32) []byte {
	return binary.LittleEndian.AppendUint32(dst, uint32(number))
}

// AppendInt64 appends a little-endian int64 (an int64 or a DateTime value).
func AppendInt64(dst []byte, number int64) []byte {
	return binary.LittleEndian.AppendUint64(dst, uint64(number))
}

// AppendDouble appends an IEEE 754 binary64, bit for bit, so every NaN
// payload and the sign of zero survive.
func AppendDouble(dst []byte, number float64) []byte {
	return binary.LittleEndian.AppendUint64(dst, math.Float64bits(number))
}

// AppendString appends a BSON string: its length with the terminator
// counted, its bytes, and a zero byte.
func AppendString(dst []byte, text string) []byte {
	dst = AppendInt32(dst, int32(len(text)+1))
	dst = append(dst, text...)
	return append(dst, 0)
}

// binaryOldSubtype is the deprecated subtype whose payload carries a second
// length prefix inside the first.
const binaryOldSubtype = 0x02

// AppendBinary appends a BSON binary: its length, its subtype and its
// payload. Subtype 0x02 nests a second length, and its outer length counts
// those 4 bytes.
func AppendBinary(dst []byte, subtype byte, payload []byte) []byte {
	if subtype == binaryOldSubtype {
		dst = AppendInt32(dst, int32(len(payload)+4))
		dst = append(dst, subtype)
		dst = AppendInt32(dst, int32(len(payload)))
		return append(dst, payload...)
	}
	dst = AppendInt32(dst, int32(len(payload)))
	dst = append(dst, subtype)
	return append(dst, payload...)
}

// PutLength writes length as a little-endian int32 at dst[index:].
func PutLength(dst []byte, index int, length int32) {
	binary.LittleEndian.PutUint32(dst[index:], uint32(length))
}
