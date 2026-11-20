// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsonrw/copier.go (CopyDocumentFromBytes, copyBytesToValueWriter, CopyValue, copyArray, copyDocumentCore), bson/bsonrw/value_reader.go (the reads below the root, their order and their error text) and the valueLength of x/bsonx/bsoncore/bsoncore.go: rewritten as one reader over a value's bytes that writes as it reads; a declared root length of 0 to 3, or a value length that overflows int32, is an error where the vendor's slice expressions panic; the root's element loop shared with the nested document, array and scope copies of the Go-value encoder (step 5's Go-value half).

package extjson

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// AppendText appends the Extended JSON text of one BSON document to dst,
// canonical or relaxed, with '<', '>' and '&' escaped when escapeHTML is
// set. The text and the error text are the vendor's MarshalExtJSON of a
// bson.Raw holding document, except that a document opening a level past
// MaxNestingDepth (documents, arrays and scopes counted, the root as level
// 1) is refused with an error matching ErrNestingTooDeep. On an error it
// returns dst as it was, as the vendor returns no text.
func AppendText(dst, document []byte, canonical, escapeHTML bool) ([]byte, error) {
	writer := textWriter{buffer: dst, canonical: canonical, escapeHTML: escapeHTML}
	if err := copyRoot(&writer, document); err != nil {
		return dst, err
	}
	return writer.buffer, nil
}

// AppendElementText appends the text of the document holding the one
// element, which is what the vendor's RawElement.String renders. The
// element's value is level 1, as RawElement.DebugString counts it.
func AppendElementText(dst, element []byte, canonical, escapeHTML bool) ([]byte, error) {
	document := make([]byte, 4, len(element)+5)
	document = append(append(document, element...), 0)
	binary.LittleEndian.PutUint32(document, uint32(len(document)))
	writer := textWriter{buffer: dst, canonical: canonical, escapeHTML: escapeHTML, level: -1}
	if err := copyRoot(&writer, document); err != nil {
		return dst, err
	}
	return writer.buffer, nil
}

// copyRoot reads the root the way the vendor's Copier does, with
// bsoncore's functions: the declared length, then each element's type, key
// and value bytes. Each value is then read by valueReader, as the Copier
// hands it to the vendor's value reader.
func copyRoot(writer *textWriter, document []byte) error {
	if err := writer.openDocument(); err != nil {
		return err
	}
	if err := copyElementsFromBytes(writer, document, false); err != nil {
		return err
	}
	writer.closeDocument(true)
	return nil
}

// copyElementsFromBytes is the vendor's copyBytesToValueWriter: the checks
// bsoncore makes of a document's own bytes (its declared length, each
// element's type, key and value length), each value read by a fresh
// valueReader, as CopyValueFromBytes reads it. An array's keys are read
// and dropped. The caller has opened the level and closes it.
func copyElementsFromBytes(writer *textWriter, document []byte, array bool) error {
	if len(document) < 4 || int32(binary.LittleEndian.Uint32(document)) < 0 {
		return fmt.Errorf("couldn't read length from src, not enough bytes. length=%d", len(document))
	}
	length := int32(binary.LittleEndian.Uint32(document))
	if len(document) < int(length) {
		// The vendor's text has its two numbers swapped.
		return fmt.Errorf("length read exceeds number of bytes available. length=%d bytes=%d", len(document), length)
	}
	if length < 4 {
		return fmt.Errorf("declared document length %d is shorter than its own length field", length)
	}
	remaining := document[4:length]
	for {
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
		keyEnd := bytes.IndexByte(remaining, 0)
		if keyEnd < 0 {
			return fmt.Errorf("invalid key found. remaining bytes=%v", remaining)
		}
		if !array {
			writer.key(string(remaining[:keyEnd]))
		}
		remaining = remaining[keyEnd+1:]
		valueBytes, rest, ok := splitValue(remaining, elementType)
		if !ok {
			return fmt.Errorf("not enough bytes available to read type. bytes=%d type=%s", len(remaining), elementType)
		}
		remaining = rest
		reader := valueReader{source: valueBytes}
		if err := reader.copyValue(writer, elementType); err != nil {
			return err
		}
	}
}

// splitValue is bsoncore's ReadValue: the next value's bytes, judged from
// its length prefix or its fixed size. A length that overflows int32 is not
// ok; the vendor slices with it and panics.
func splitValue(source []byte, elementType bsontype.Type) (valueBytes, rest []byte, ok bool) {
	length, ok := valueLength(source, elementType)
	if !ok || length < 0 || int(length) > len(source) {
		return nil, source, false
	}
	return source[:length], source[length:], true
}

// lengthPrefix is bsoncore's ReadLength: a negative length is not ok.
func lengthPrefix(source []byte) (int32, bool) {
	if len(source) < 4 {
		return 0, false
	}
	length := int32(binary.LittleEndian.Uint32(source))
	return length, length >= 0
}

// valueLength is bsoncore's valueLength. The additions wrap in int32 as
// the vendor's do.
func valueLength(source []byte, elementType bsontype.Type) (int32, bool) {
	switch elementType {
	case bsontype.Array, bsontype.EmbeddedDocument, bsontype.CodeWithScope:
		return lengthPrefix(source)
	case bsontype.Binary:
		length, ok := lengthPrefix(source)
		return length + 4 + 1, ok
	case bsontype.DBPointer:
		length, ok := lengthPrefix(source)
		return length + 4 + 12, ok
	case bsontype.JavaScript, bsontype.String, bsontype.Symbol:
		length, ok := lengthPrefix(source)
		return length + 4, ok
	case bsontype.Boolean:
		return 1, true
	case bsontype.DateTime, bsontype.Double, bsontype.Int64, bsontype.Timestamp:
		return 8, true
	case bsontype.Decimal128:
		return 16, true
	case bsontype.Int32:
		return 4, true
	case bsontype.MaxKey, bsontype.MinKey, bsontype.Null, bsontype.Undefined:
		return 0, true
	case bsontype.ObjectID:
		return 12, true
	case bsontype.Regex:
		patternEnd := bytes.IndexByte(source, 0)
		if patternEnd < 0 {
			return 0, false
		}
		optionsEnd := bytes.IndexByte(source[patternEnd+1:], 0)
		if optionsEnd < 0 {
			return 0, false
		}
		return int32(int64(patternEnd) + 1 + int64(optionsEnd) + 1), true
	}
	return 0, false
}

// valueReader reads one root element's value bytes as the vendor's value
// reader does: sequentially, with each container's declared end checked
// only at its terminator, so a container that declares a short length is
// read on into the bytes after it. Offsets in its errors count from the
// start of the root element's value.
type valueReader struct {
	source []byte
	offset int64
}

// copyValue reads one value of valueType and writes it, in the order of
// the vendor's Copier.CopyValue: a value is read whole before it is
// written, and a container's header is read before it is opened.
func (reader *valueReader) copyValue(writer *textWriter, valueType bsontype.Type) error {
	switch valueType {
	case bsontype.EmbeddedDocument:
		return reader.copyDocument(writer, false)
	case bsontype.Array:
		end, err := reader.containerEnd()
		if err != nil {
			return err
		}
		if err := writer.openArray(); err != nil {
			return err
		}
		return reader.copyArrayValues(writer, end)
	case bsontype.CodeWithScope:
		return reader.copyCodeWithScope(writer)
	}
	return reader.copyScalar(writer, valueType)
}

// copyDocument reads a document's header, opens it and copies its
// elements; a root document is closed without the comma that follows a
// value.
func (reader *valueReader) copyDocument(writer *textWriter, root bool) error {
	end, err := reader.containerEnd()
	if err != nil {
		return err
	}
	if err := writer.openDocument(); err != nil {
		return err
	}
	return reader.copyElements(writer, end, func(bool) { writer.closeDocument(root) })
}

// containerEnd reads a document's or an array's length and returns the
// offset its terminator must end at.
func (reader *valueReader) containerEnd() (int64, error) {
	length, err := reader.readInt32()
	if err != nil {
		return 0, err
	}
	return int64(length) + reader.offset - 4, nil
}

// endAt checks a terminator just read against the container's declared end.
func (reader *valueReader) endAt(end int64) error {
	if reader.offset != end {
		return fmt.Errorf("document is invalid, end byte is at %d, but null byte found at %d", end, reader.offset)
	}
	return nil
}

// copyElements copies a document's or a scope's elements up to its
// terminator, then closes it with closeLevel.
func (reader *valueReader) copyElements(writer *textWriter, end int64, closeLevel func(root bool)) error {
	for {
		elementType, err := reader.readByte()
		if err != nil {
			return err
		}
		if elementType == 0 {
			if err := reader.endAt(end); err != nil {
				return err
			}
			closeLevel(false)
			return nil
		}
		name, err := reader.readCString()
		if err != nil {
			return err
		}
		writer.key(name)
		if err := reader.copyValue(writer, bsontype.Type(elementType)); err != nil {
			return err
		}
	}
}

// copyArrayValues copies an array's values, skipping their keys.
func (reader *valueReader) copyArrayValues(writer *textWriter, end int64) error {
	for {
		elementType, err := reader.readByte()
		if err != nil {
			return err
		}
		if elementType == 0 {
			if err := reader.endAt(end); err != nil {
				return err
			}
			writer.closeArray()
			return nil
		}
		if err := reader.skipCString(); err != nil {
			return err
		}
		if err := reader.copyValue(writer, bsontype.Type(elementType)); err != nil {
			return err
		}
	}
}

// copyCodeWithScope reads the code with scope's header, then copies the
// scope.
func (reader *valueReader) copyCodeWithScope(writer *textWriter) error {
	code, end, err := reader.codeWithScopeHeader()
	if err != nil {
		return err
	}
	if err := writer.openScope(code); err != nil {
		return err
	}
	return reader.copyElements(writer, end, func(bool) { writer.closeScope() })
}

// codeWithScopeHeader reads the total length, the code and the scope's
// length, checks that they add up, and returns the code and the offset the
// scope's terminator must end at.
func (reader *valueReader) codeWithScopeHeader() (string, int64, error) {
	totalLength, err := reader.readInt32()
	if err != nil {
		return "", 0, err
	}
	codeLength, err := reader.readInt32()
	if err != nil {
		return "", 0, err
	}
	if codeLength <= 0 {
		return "", 0, fmt.Errorf("invalid string length: %d", codeLength)
	}
	codeBytes, err := reader.readBytes(codeLength)
	if err != nil {
		return "", 0, err
	}
	// The code's last byte is dropped unchecked, as the vendor does.
	code := string(codeBytes[:len(codeBytes)-1])
	scopeLength, err := reader.readInt32()
	if err != nil {
		return "", 0, err
	}
	end := int64(scopeLength) + reader.offset - 4
	// 4+codeLength+4 is int32 arithmetic in the vendor too.
	componentsLength := int64(4+codeLength+4) + int64(scopeLength)
	if int64(totalLength) != componentsLength {
		return "", 0, fmt.Errorf("length of CodeWithScope does not match lengths of components; total: %d; components: %d", totalLength, componentsLength)
	}
	return code, end, nil
}

// copyScalar reads and writes every value that is not a container.
func (reader *valueReader) copyScalar(writer *textWriter, valueType bsontype.Type) error {
	switch valueType {
	case bsontype.Double:
		bits, err := reader.readUint64()
		if err != nil {
			return err
		}
		writer.writeDouble(math.Float64frombits(bits))
	case bsontype.String, bsontype.JavaScript, bsontype.Symbol:
		text, err := reader.readString()
		if err != nil {
			return err
		}
		switch valueType {
		case bsontype.String:
			writer.writeString(text)
		case bsontype.JavaScript:
			writer.writeJavaScript(text)
		default:
			writer.writeSymbol(text)
		}
	case bsontype.Binary:
		return reader.copyBinary(writer)
	case bsontype.Undefined:
		writer.writeUndefined()
	case bsontype.ObjectID:
		objectID, err := reader.readObjectID()
		if err != nil {
			return err
		}
		writer.writeObjectID(objectID)
	case bsontype.Boolean:
		flag, err := reader.readByte()
		if err != nil {
			return err
		}
		if flag > 1 {
			return fmt.Errorf("invalid byte for boolean, %b", flag)
		}
		writer.writeBoolean(flag == 1)
	case bsontype.DateTime:
		bits, err := reader.readUint64()
		if err != nil {
			return err
		}
		writer.writeDateTime(int64(bits))
	case bsontype.Null:
		writer.writeNull()
	case bsontype.Regex:
		return reader.copyRegex(writer)
	case bsontype.DBPointer:
		namespace, err := reader.readString()
		if err != nil {
			return err
		}
		objectID, err := reader.readObjectID()
		if err != nil {
			return err
		}
		writer.writeDBPointer(namespace, objectID)
	case bsontype.Int32:
		number, err := reader.readInt32()
		if err != nil {
			return err
		}
		writer.writeInt32(number)
	case bsontype.Timestamp:
		increment, err := reader.readUint32()
		if err != nil {
			return err
		}
		seconds, err := reader.readUint32()
		if err != nil {
			return err
		}
		writer.writeTimestamp(seconds, increment)
	case bsontype.Int64:
		bits, err := reader.readUint64()
		if err != nil {
			return err
		}
		writer.writeInt64(int64(bits))
	case bsontype.Decimal128:
		decimalBytes, err := reader.readBytes(16)
		if err != nil {
			return err
		}
		writer.writeDecimal128(primitive.NewDecimal128(binary.LittleEndian.Uint64(decimalBytes[8:]), binary.LittleEndian.Uint64(decimalBytes[:8])))
	case bsontype.MinKey:
		writer.writeMinKey()
	case bsontype.MaxKey:
		writer.writeMaxKey()
	default:
		//nolint:staticcheck // ST1005: the vendor's capitalised text is the contract.
		return fmt.Errorf("Cannot copy unknown BSON type %s", valueType)
	}
	return nil
}

// copyBinary reads a binary: its length, its subtype, and for the old
// subtype 0x02 with a length past 4, a second length inside.
func (reader *valueReader) copyBinary(writer *textWriter) error {
	length, err := reader.readInt32()
	if err != nil {
		return err
	}
	subtype, err := reader.readByte()
	if err != nil {
		return err
	}
	if subtype == 0x02 && length > 4 {
		if length, err = reader.readInt32(); err != nil {
			return err
		}
	}
	payload, err := reader.readBytes(length)
	if err != nil {
		return err
	}
	writer.writeBinary(subtype, payload)
	return nil
}

func (reader *valueReader) copyRegex(writer *textWriter) error {
	pattern, err := reader.readCString()
	if err != nil {
		return err
	}
	options, err := reader.readCString()
	if err != nil {
		return err
	}
	writer.writeRegex(pattern, options)
	return nil
}

func (reader *valueReader) readBytes(length int32) ([]byte, error) {
	if length < 0 {
		return nil, fmt.Errorf("invalid length: %d", length)
	}
	if reader.offset+int64(length) > int64(len(reader.source)) {
		return nil, io.EOF
	}
	start := reader.offset
	reader.offset += int64(length)
	return reader.source[start:reader.offset], nil
}

func (reader *valueReader) readByte() (byte, error) {
	octets, err := reader.readBytes(1)
	if err != nil {
		return 0, err
	}
	return octets[0], nil
}

func (reader *valueReader) readInt32() (int32, error) {
	number, err := reader.readUint32()
	return int32(number), err
}

func (reader *valueReader) readUint32() (uint32, error) {
	octets, err := reader.readBytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(octets), nil
}

func (reader *valueReader) readUint64() (uint64, error) {
	octets, err := reader.readBytes(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(octets), nil
}

func (reader *valueReader) readObjectID() (primitive.ObjectID, error) {
	var objectID primitive.ObjectID
	octets, err := reader.readBytes(int32(len(objectID)))
	if err != nil {
		return objectID, err
	}
	copy(objectID[:], octets)
	return objectID, nil
}

// readCString reads up to the next zero byte, which it consumes. It
// searches to the end of the value's bytes, past any container's end.
func (reader *valueReader) readCString() (string, error) {
	terminator := bytes.IndexByte(reader.source[reader.offset:], 0)
	if terminator < 0 {
		return "", io.EOF
	}
	start := reader.offset
	reader.offset += int64(terminator) + 1
	return string(reader.source[start : reader.offset-1]), nil
}

func (reader *valueReader) skipCString() error {
	terminator := bytes.IndexByte(reader.source[reader.offset:], 0)
	if terminator < 0 {
		return io.EOF
	}
	reader.offset += int64(terminator) + 1
	return nil
}

// readString reads a BSON string in the vendor's order of checks: the
// bytes are there, the length is positive, the last byte is zero.
func (reader *valueReader) readString() (string, error) {
	length, err := reader.readInt32()
	if err != nil {
		return "", err
	}
	if int64(length)+reader.offset > int64(len(reader.source)) {
		return "", io.EOF
	}
	if length <= 0 {
		return "", fmt.Errorf("invalid string length: %d", length)
	}
	if last := reader.source[reader.offset+int64(length)-1]; last != 0 {
		return "", fmt.Errorf("string does not end with null byte, but with %v", last)
	}
	start := reader.offset
	reader.offset += int64(length)
	return string(reader.source[start : reader.offset-1]), nil
}
