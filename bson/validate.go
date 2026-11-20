// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
//
// Derived from hoardDB's server/nesting_depth.go, licensed to this library
// under Apache-2.0 by its copyright holder (ruling O16, 2026-10-24).

package bson

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/internal/nesting"
)

// MaxNestingCeiling is the deepest any document may nest in this library,
// the root counted as level 1. No Limits may exceed it, and Marshal,
// MarshalValue, Unmarshal, RawValue.Unmarshal and Document.Decode refuse a
// level past it.
const MaxNestingCeiling = nesting.Ceiling

// Limits are the bounds one call site checks a document against. They are
// chosen per site, because what a site may accept differs (spec 5.2).
type Limits struct {
	// Depth is how many levels the document may nest: the root is level 1,
	// and each embedded document, array and code-with-scope scope inside it
	// adds one. It must be 1 to MaxNestingCeiling; there is no default, so
	// a site that forgets its limit fails loudly instead of getting another
	// site's.
	Depth int
	// Size is the most bytes the document may hold, or 0 for no size check.
	Size int
}

// fixedStackLevels is how many levels Validate tracks without allocating.
const fixedStackLevels = 128

// Validate checks bytes from outside this process's own store and returns
// them as a Document. In one pass, without copying, it checks the lengths
// and terminators at every level, that every type byte is a BSON type, the
// value bytes of every element (a boolean is 0 or 1, a string ends in its
// terminator, and so on), and the nesting depth against limits.Depth. It
// does not check UTF-8.
//
// It accepts exactly the bytes that pass both today's server depth scan at
// limits.Depth and a decode into an ordered document (spec 6.2). A
// refusal is a *ValidationError matching ErrMalformedDocument,
// ErrNestingTooDeep or ErrDocumentTooLarge; Limits out of range give an
// error matching ErrInvalidLimits.
func Validate(raw []byte, limits Limits) (Document, error) {
	if limits.Depth < 1 || limits.Depth > MaxNestingCeiling || limits.Size < 0 {
		return Document{}, fmt.Errorf("%w: Depth is %d and must be 1 to %d; Size is %d and must be 0 (no check) or more",
			ErrInvalidLimits, limits.Depth, MaxNestingCeiling, limits.Size)
	}
	if limits.Size > 0 && len(raw) > limits.Size {
		return Document{}, &ValidationError{Err: ErrDocumentTooLarge, Offset: limits.Size,
			Reason: fmt.Sprintf("%d bytes is over the limit of %d", len(raw), limits.Size)}
	}
	if refusal := validateDocument(raw, limits.Depth); refusal != nil {
		return Document{}, refusal
	}
	return Document{b: raw}, nil
}

// malformedAt is a refusal for bytes that are not BSON.
func malformedAt(offset int, reason string) *ValidationError {
	return &ValidationError{Err: ErrMalformedDocument, Offset: offset, Reason: reason}
}

// validateDocument walks raw iteratively, holding the end offset of every
// open document, array and scope on an explicit stack. It reads elements
// in order, as a decoder does, and checks every length against the bytes
// it describes and every terminator at the offset its document's length
// names. Reads are bounded by the end of raw, not of the enclosing
// document; the terminator check at the declared end is what holds every
// level to its length, the same reading the server's scan and the decoder
// make, so all three see one tree.
func validateDocument(raw []byte, depthLimit int) *ValidationError {
	rootLength, ok := lengthAt(raw, 0)
	if !ok {
		return malformedAt(0, "too few bytes for a document's length prefix")
	}
	if rootLength != len(raw) {
		return malformedAt(0, "the document's length prefix does not equal the number of bytes")
	}
	var stackStorage [fixedStackLevels]int
	ends := append(stackStorage[:0], len(raw))
	position := 4
	for len(ends) > 0 {
		if position >= len(raw) {
			return malformedAt(position, "the bytes end inside a document")
		}
		elementType := raw[position]
		position++
		if elementType == 0 {
			if position != ends[len(ends)-1] {
				return malformedAt(position-1, "a document's terminator is not where its length prefix says")
			}
			ends = ends[:len(ends)-1]
			continue
		}
		keyLength := bytes.IndexByte(raw[position:], 0)
		if keyLength < 0 {
			return malformedAt(position, "an element name has no terminator")
		}
		valuePosition := position + keyLength + 1
		next, opened, reason := validateValue(raw, valuePosition, bsontype.Type(elementType))
		if reason != "" {
			return malformedAt(valuePosition, reason)
		}
		if opened > 0 {
			if len(ends) >= depthLimit {
				return &ValidationError{Err: ErrNestingTooDeep, Offset: valuePosition,
					Reason: fmt.Sprintf("a level past the limit of %d opens here", depthLimit)}
			}
			ends = append(ends, opened)
		}
		position = next
	}
	return nil
}

// validateValue checks the value of one element at position. It returns
// where the next element starts and, for a value that opens a document (an
// embedded document, an array, a code-with-scope's scope), that document's
// end offset; next is then the position of its first element. A non-empty
// reason is a refusal. Reasons are constants, so success allocates nothing.
func validateValue(raw []byte, position int, elementType bsontype.Type) (next, opened int, reason string) {
	switch elementType {
	case bsontype.Undefined, bsontype.Null, bsontype.MaxKey, bsontype.MinKey:
		return position, 0, ""
	case bsontype.Boolean:
		if position >= len(raw) {
			return 0, 0, "a boolean runs past the end of the bytes"
		}
		if raw[position] > 1 {
			return 0, 0, "a boolean's byte is neither 0 nor 1"
		}
		return position + 1, 0, ""
	case bsontype.Int32:
		return fixedSize(raw, position, 4)
	case bsontype.Double, bsontype.DateTime, bsontype.Timestamp, bsontype.Int64:
		return fixedSize(raw, position, 8)
	case bsontype.ObjectID:
		return fixedSize(raw, position, 12)
	case bsontype.Decimal128:
		return fixedSize(raw, position, 16)
	case bsontype.String, bsontype.JavaScript, bsontype.Symbol:
		return terminatedString(raw, position)
	case bsontype.DBPointer:
		end, _, reason := terminatedString(raw, position)
		if reason != "" {
			return 0, 0, reason
		}
		return fixedSize(raw, end, 12)
	case bsontype.Regex:
		end := position
		for range 2 {
			terminator := bytes.IndexByte(raw[end:], 0)
			if terminator < 0 {
				return 0, 0, "a regular expression has no terminator"
			}
			end += terminator + 1
		}
		return end, 0, ""
	case bsontype.Binary:
		return binaryValue(raw, position)
	case bsontype.EmbeddedDocument, bsontype.Array:
		return documentStart(raw, position)
	case bsontype.CodeWithScope:
		return scopeStart(raw, position)
	}
	return 0, 0, "the element's type byte is not a BSON type"
}

// lengthAt reads the int32 at position; ok is false when the four bytes are
// not there or the length is negative.
func lengthAt(raw []byte, position int) (length int, ok bool) {
	if len(raw)-position < 4 {
		return 0, false
	}
	length = int(int32(binary.LittleEndian.Uint32(raw[position:])))
	return length, length >= 0
}

// fixedSize is a value of size bytes.
func fixedSize(raw []byte, position, size int) (next, opened int, reason string) {
	if len(raw)-position < size {
		return 0, 0, "a fixed-size value runs past the end of the bytes"
	}
	return position + size, 0, ""
}

// terminatedString is a string, JavaScript code or symbol: a length that
// counts the terminator, so at least 1, the bytes, and a 0 last.
func terminatedString(raw []byte, position int) (next, opened int, reason string) {
	length, ok := lengthAt(raw, position)
	if !ok || length < 1 || len(raw)-position-4 < length {
		return 0, 0, "a string's length prefix does not fit the bytes"
	}
	end := position + 4 + length
	if raw[end-1] != 0 {
		return 0, 0, "a string does not end with its terminator"
	}
	return end, 0, ""
}

// binaryValue is a length, a subtype byte and that many bytes. The old
// binary subtype 0x02 holds a second length inside them, which a decoder
// reads in place of the first and a skipper ignores; the two must agree,
// or the readings would continue from different bytes. Because a decoder
// never reads an old binary's outer length, both of its refusals below
// (the outer length past the bytes, the two lengths apart) are the
// server scan's alone, and their reasons say "old binary" so the oracle
// can tell them from the decoder's.
func binaryValue(raw []byte, position int) (next, opened int, reason string) {
	length, ok := lengthAt(raw, position)
	if !ok {
		return 0, 0, "a binary value's length prefix does not fit the bytes"
	}
	if len(raw)-position-5 < length {
		if len(raw)-position > 4 && raw[position+4] == bsontype.BinaryBinaryOld && length > 4 {
			return 0, 0, "an old binary value's outer length does not fit the bytes"
		}
		return 0, 0, "a binary value's length prefix does not fit the bytes"
	}
	if raw[position+4] == bsontype.BinaryBinaryOld && length > 4 {
		if inner := int(int32(binary.LittleEndian.Uint32(raw[position+5:]))); inner != length-4 {
			return 0, 0, "an old binary value's two lengths disagree"
		}
	}
	return position + 5 + length, 0, ""
}

// documentStart opens an embedded document or an array: its first element
// follows the length prefix, and its terminator must be its last byte.
func documentStart(raw []byte, position int) (next, opened int, reason string) {
	length, ok := lengthAt(raw, position)
	if !ok || length < minDocumentLength || len(raw)-position < length {
		return 0, 0, "an embedded document's length prefix does not fit the bytes"
	}
	return position + 4, position + length, ""
}

// scopeStart opens the scope of a code-with-scope value: a total length,
// the code, then the scope document, whose length must account for the
// rest of the total. The code's last byte is not checked, because the
// decoder does not check it either.
func scopeStart(raw []byte, position int) (next, opened int, reason string) {
	total, ok := lengthAt(raw, position)
	if !ok || len(raw)-position < total {
		return 0, 0, "a code-with-scope value's length prefix does not fit the bytes"
	}
	codeLength, ok := lengthAt(raw, position+4)
	if !ok || codeLength < 1 || len(raw)-position-8 < codeLength {
		return 0, 0, "a code-with-scope value's code length does not fit the bytes"
	}
	scopePosition := position + 8 + codeLength
	scopeLength, ok := lengthAt(raw, scopePosition)
	if !ok || scopePosition+scopeLength != position+total {
		return 0, 0, "a code-with-scope value's lengths disagree"
	}
	return documentStart(raw, scopePosition)
}
