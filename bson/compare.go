// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"math"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// sortClass is a value's class in the unindexed sort: the classes order
// before any value inside them does (spec 6.3).
type sortClass uint8

const (
	classNumber sortClass = iota + 1
	classString
	classDate
	classBoolean
	classDocument
	classArray
	classOther
	classNull
)

// sortKey is what the order reads of one value: its class, its type byte
// inside the other class, and its value where the class compares values.
type sortKey struct {
	class     sortClass
	otherType bsontype.Type
	isDouble  bool
	integer   int64 // an int32 or int64, a date's milliseconds, a boolean as 0 or 1
	double    float64
	text      []byte
}

// Compare orders two values as the unindexed sort does, on their bytes: it
// returns -1 when left sorts first, 1 when right does, and 0 when they tie.
//
// Values order by class first: numbers, strings, dates, booleans,
// documents, arrays, every other type, then null or missing (the zero
// View). Inside a class:
//   - int32, int64 and double compare by their exact value, so 5, 5 and
//     5.0 tie, -0 ties with 0, and an int64 past 2^53 is never rounded. NaN
//     sorts below every other number and ties with every NaN.
//   - strings compare byte by byte, without collation or UTF-8 decoding.
//   - dates compare by their milliseconds, and false sorts before true.
//   - documents tie with documents and arrays with arrays, whatever they
//     hold; null ties with missing.
//   - every other type sorts by its type byte and ties with its own type.
//     Decimal128 is one of them: all Decimal128 values tie, never ordered
//     by number.
//
// Compare allocates nothing.
func Compare(left, right View) int {
	return compareKeys(keyOf(left), keyOf(right))
}

// Literal is a value written in Go rather than read from a document, to
// compare a View against with CompareLiteral. The zero Literal is null.
type Literal struct {
	key sortKey
}

// Int32Literal is the 32-bit integer number.
func Int32Literal(number int32) Literal {
	return Literal{sortKey{class: classNumber, integer: int64(number)}}
}

// Int64Literal is the 64-bit integer number.
func Int64Literal(number int64) Literal {
	return Literal{sortKey{class: classNumber, integer: number}}
}

// DoubleLiteral is the double number, NaN and the infinities included.
func DoubleLiteral(number float64) Literal {
	return Literal{sortKey{class: classNumber, isDouble: true, double: number}}
}

// StringLiteral is the string text. It copies text once, here, so that
// comparing against it allocates nothing.
func StringLiteral(text string) Literal {
	return Literal{sortKey{class: classString, text: []byte(text)}}
}

// DateTimeLiteral is the date at milliseconds since the Unix epoch.
func DateTimeLiteral(milliseconds int64) Literal {
	return Literal{sortKey{class: classDate, integer: milliseconds}}
}

// BooleanLiteral is the boolean flag.
func BooleanLiteral(flag bool) Literal {
	if flag {
		return Literal{sortKey{class: classBoolean, integer: 1}}
	}
	return Literal{sortKey{class: classBoolean}}
}

// NullLiteral is null, which ties with a missing value.
func NullLiteral() Literal {
	return Literal{sortKey{class: classNull}}
}

// CompareLiteral orders view against literal exactly as Compare orders view
// against a View holding the literal's value. It allocates nothing.
func CompareLiteral(view View, literal Literal) int {
	literalKey := literal.key
	if literalKey.class == 0 {
		literalKey.class = classNull
	}
	return compareKeys(keyOf(view), literalKey)
}

// keyOf reads a View's sort key. A View's bytes are the cursor's, measured
// for its type, so each fixed-size read has its bytes and a string has its
// length prefix and terminator.
func keyOf(view View) sortKey {
	valueBytes := view.valueBytes
	switch view.valueType {
	case bsontype.Int32:
		return sortKey{class: classNumber, integer: int64(int32(binary.LittleEndian.Uint32(valueBytes)))}
	case bsontype.Int64:
		return sortKey{class: classNumber, integer: int64(binary.LittleEndian.Uint64(valueBytes))}
	case bsontype.Double:
		return sortKey{class: classNumber, isDouble: true, double: math.Float64frombits(binary.LittleEndian.Uint64(valueBytes))}
	case bsontype.String:
		return sortKey{class: classString, text: valueBytes[4 : len(valueBytes)-1]}
	case bsontype.DateTime:
		return sortKey{class: classDate, integer: int64(binary.LittleEndian.Uint64(valueBytes))}
	case bsontype.Boolean:
		// Only 0x01 is true, as a decoder reads it; Validate refuses any
		// other byte but 0x00, and a TrustStored document was not walked.
		if valueBytes[0] == 1 {
			return sortKey{class: classBoolean, integer: 1}
		}
		return sortKey{class: classBoolean}
	case bsontype.EmbeddedDocument:
		return sortKey{class: classDocument}
	case bsontype.Array:
		return sortKey{class: classArray}
	case bsontype.Null, 0:
		return sortKey{class: classNull}
	}
	return sortKey{class: classOther, otherType: view.valueType}
}

// compareKeys is the order on sort keys: class, then what the class
// compares.
func compareKeys(left, right sortKey) int {
	if left.class != right.class {
		return cmp.Compare(left.class, right.class)
	}
	switch left.class {
	case classNumber:
		return compareNumbers(left, right)
	case classString:
		return bytes.Compare(left.text, right.text)
	case classDate, classBoolean:
		return cmp.Compare(left.integer, right.integer)
	case classOther:
		return cmp.Compare(left.otherType, right.otherType)
	}
	return 0
}

// compareNumbers compares two numbers by exact value, NaN lowest.
func compareNumbers(left, right sortKey) int {
	switch {
	case !left.isDouble && !right.isDouble:
		return cmp.Compare(left.integer, right.integer)
	case left.isDouble && right.isDouble:
		// cmp.Compare puts NaN below every number and ties NaN with NaN,
		// and ties -0 with 0: the order the sort keeps.
		return cmp.Compare(left.double, right.double)
	case left.isDouble:
		return -compareIntegerWithDouble(right.integer, left.double)
	}
	return compareIntegerWithDouble(left.integer, right.double)
}

// compareIntegerWithDouble compares an int64 with a double exactly. Turning
// the integer into a double would round it past 2^53 and tie 2^53+1 with
// 2^53, so the double is split at its integer part instead.
func compareIntegerWithDouble(integer int64, double float64) int {
	switch {
	case math.IsNaN(double):
		return 1
	case double < -0x1p63:
		return 1
	case double >= 0x1p63:
		return -1
	}
	// The double is now in [-2^63, 2^63), so its integer part fits an int64.
	whole := math.Trunc(double)
	if order := cmp.Compare(integer, int64(whole)); order != 0 {
		return order
	}
	// Equal integer parts: the fraction decides.
	return cmp.Compare(whole, double)
}
