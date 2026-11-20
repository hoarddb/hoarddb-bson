// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsonrw/copier.go (CopyDocumentFromBytes, CopyArrayFromBytes, copyBytesToValueWriter) and the valueLength and readValue of x/bsonx/bsoncore/bsoncore.go; rewritten to append each element's bytes directly, with the vendor's checks and error text; a declared length below 4, or a value length past MaxInt32, is an error where the vendor's slice expression panics; in text mode (step 5's Go-value half) the bytes go to extjson.TextWriter's copies instead.

package encode

import (
	"encoding/binary"
	"fmt"
	"io"
	"strconv"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// copyDocument writes a document from bytes the caller supplied (bson.Raw,
// bsoncore.Document, the top-level MarshalBSON). Like the vendor's copier it
// checks only the top level: the declared length, and that each element
// has a type, a key and enough bytes for its value. A value's own bytes,
// a nested document's included, are copied as they are.
func (encoding *writer) copyDocument(at slot, documentBytes []byte) error {
	if encoding.textMode {
		if !at.isTopLevel() {
			if err := encoding.header(at, bsontype.EmbeddedDocument, "WriteDocument", "Document"); err != nil {
				return err
			}
		}
		return encoding.text.CopyDocument(documentBytes)
	}
	start, err := encoding.startDocument(at)
	if err != nil {
		return err
	}
	if err := encoding.copyElements(documentBytes, false); err != nil {
		return err
	}
	return encoding.end(start)
}

// copyArray writes an array from bsoncore.Array bytes. The elements are
// renumbered from 0, whatever keys the bytes held.
func (encoding *writer) copyArray(at slot, arrayBytes []byte) error {
	if encoding.textMode {
		if err := encoding.header(at, bsontype.Array, "WriteArray", "Array"); err != nil {
			return err
		}
		return encoding.text.CopyArray(arrayBytes)
	}
	start, err := encoding.startArray(at)
	if err != nil {
		return err
	}
	if err := encoding.copyElements(arrayBytes, true); err != nil {
		return err
	}
	return encoding.end(start)
}

func (encoding *writer) copyElements(source []byte, renumber bool) error {
	if len(source) < 4 || int32(binary.LittleEndian.Uint32(source)) < 0 {
		return fmt.Errorf("couldn't read length from src, not enough bytes. length=%d", len(source))
	}
	length := int32(binary.LittleEndian.Uint32(source))
	if len(source) < int(length) {
		return fmt.Errorf("length read exceeds number of bytes available. length=%d bytes=%d", len(source), length)
	}
	if length < 4 {
		// The vendor slices to length-4 and panics.
		return fmt.Errorf("declared document length %d is shorter than its own length field", length)
	}
	remaining := source[4:length]
	for index := 0; ; index++ {
		if len(remaining) == 0 {
			return io.EOF
		}
		elementType := bsontype.Type(remaining[0])
		remaining = remaining[1:]
		if elementType == 0 {
			if len(remaining) != 0 {
				return fmt.Errorf("document end byte found before end of document. remaining bytes=%v", remaining)
			}
			return nil
		}
		keyEnd := indexOfZero(remaining)
		if keyEnd < 0 {
			return fmt.Errorf("invalid key found. remaining bytes=%v", remaining)
		}
		key := remaining[:keyEnd]
		remaining = remaining[keyEnd+1:]
		valueBytes, rest, ok := readValue(remaining, elementType)
		if !ok {
			return fmt.Errorf("not enough bytes available to read type. bytes=%d type=%s", len(remaining), elementType)
		}
		remaining = rest
		encoding.buf = append(encoding.buf, byte(elementType))
		if renumber {
			encoding.buf = strconv.AppendInt(encoding.buf, int64(index), 10)
		} else {
			encoding.buf = append(encoding.buf, key...)
		}
		encoding.buf = append(encoding.buf, 0)
		encoding.buf = append(encoding.buf, valueBytes...)
	}
}

func indexOfZero(source []byte) int {
	for position, octet := range source {
		if octet == 0 {
			return position
		}
	}
	return -1
}

// readValue splits off the next value of elementType, as bsoncore.ReadValue
// does.
func readValue(source []byte, elementType bsontype.Type) (valueBytes, rest []byte, ok bool) {
	length, ok := valueLength(source, elementType)
	if !ok || length < 0 || int(length) > len(source) {
		return nil, source, false
	}
	return source[:length], source[length:], true
}

// readLength reads a length prefix; a negative one is not a length.
func readLength(source []byte) (int32, bool) {
	if len(source) < 4 {
		return 0, false
	}
	length := int32(binary.LittleEndian.Uint32(source))
	return length, length >= 0
}

// valueLength is bsoncore's valueLength: how many bytes the next value of
// elementType takes, judged from its length prefix or its fixed size.
func valueLength(source []byte, elementType bsontype.Type) (int32, bool) {
	switch elementType {
	case bsontype.Array, bsontype.EmbeddedDocument, bsontype.CodeWithScope:
		return readLength(source)
	case bsontype.Binary:
		length, ok := readLength(source)
		return length + 4 + 1, ok
	case bsontype.Boolean:
		return 1, true
	case bsontype.DBPointer:
		length, ok := readLength(source)
		return length + 4 + 12, ok
	case bsontype.DateTime, bsontype.Double, bsontype.Int64, bsontype.Timestamp:
		return 8, true
	case bsontype.Decimal128:
		return 16, true
	case bsontype.Int32:
		return 4, true
	case bsontype.JavaScript, bsontype.String, bsontype.Symbol:
		length, ok := readLength(source)
		return length + 4, ok
	case bsontype.MaxKey, bsontype.MinKey, bsontype.Null, bsontype.Undefined:
		return 0, true
	case bsontype.ObjectID:
		return 12, true
	case bsontype.Regex:
		patternEnd := indexOfZero(source)
		if patternEnd < 0 {
			return 0, false
		}
		optionsEnd := indexOfZero(source[patternEnd+1:])
		if optionsEnd < 0 {
			return 0, false
		}
		return int32(int64(patternEnd) + 1 + int64(optionsEnd) + 1), true
	}
	return 0, false
}
