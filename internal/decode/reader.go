// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsonrw/value_reader.go and mode.go: the frame stack, the read order and every error text kept in one concrete reader with no pool and no interface; a negative length or read position that would panic the vendor is an error; a negative skip that returns to an element read before is an error instead of the vendor's endless loop; a container pushed past 200 levels is refused (build step 4f); in step 5's Go-value half, every read can go to an Extended JSON value reader instead, so the decoders run unchanged over text.

package decode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/extjson"
	"github.com/hoarddb/hoarddb-bson/internal/nesting"
)

// errEndOfDocument and errEndOfArray end a document's or an array's
// elements, as the vendor's ErrEOD and ErrEOA do.
var (
	errEndOfArray    = errors.New("end of array")
	errEndOfDocument = errors.New("end of document")
)

// mode is what a reader frame is positioned on.
type mode int

const (
	_ mode = iota
	modeTopLevel
	modeDocument
	modeArray
	modeValue
	modeElement
	modeCodeWithScope
)

func (frameMode mode) typeString() string {
	switch frameMode {
	case modeTopLevel:
		return "TopLevel"
	case modeDocument:
		return "Document"
	case modeArray:
		return "Array"
	case modeValue:
		return "Value"
	case modeElement:
		return "Element"
	case modeCodeWithScope:
		return "CodeWithScope"
	}
	return "Unknown"
}

// transitionError is the vendor's TransitionError: a read the frame is not
// positioned for.
type transitionError struct {
	name        string
	parent      mode
	current     mode
	destination mode
	modes       []mode
}

func (refusal transitionError) Error() string {
	text := refusal.name + " can only read"
	if refusal.destination != mode(0) {
		text += " a " + refusal.destination.typeString()
	}
	text += " while positioned on a"
	for position, allowed := range refusal.modes {
		if position != 0 && len(refusal.modes) > 2 {
			text += ","
		}
		if position == len(refusal.modes)-1 && len(refusal.modes) > 1 {
			text += " or"
		}
		text += " " + allowed.typeString()
	}
	text += " but is positioned on a " + refusal.current.typeString()
	if refusal.parent != mode(0) {
		text += " with parent " + refusal.parent.typeString()
	}
	return text
}

type frame struct {
	mode      mode
	valueType bsontype.Type
	end       int64
}

// inlineFrames is how many frames a reader holds before it allocates: the
// root, and an element and a level for each of seven nested levels.
const inlineFrames = 16

// reader reads BSON values from input, one frame per open document, array,
// code-with-scope or element, as the vendor's valueReader does. The frames
// past the first inlineFrames go in spilled; a slice into inline would make
// the reader point into itself, and escape to the heap.
type reader struct {
	// text, when set, is the Extended JSON value reader every read goes to
	// instead (bson.UnmarshalExtJSON, text.go); input is then unused.
	text    *extjson.ValueReader
	input   []byte
	offset  int64
	top     int64
	inline  [inlineFrames]frame
	spilled []frame
}

// newDocumentReader positions a reader on a top-level document.
func newDocumentReader(documentBytes []byte, valueReader *reader) {
	valueReader.input = documentBytes
	valueReader.inline[0] = frame{mode: modeTopLevel}
}

// newValueReader positions a reader on one value of valueType.
func newValueReader(valueType bsontype.Type, valueBytes []byte, valueReader *reader) {
	valueReader.input = valueBytes
	valueReader.inline[0] = frame{mode: modeValue, valueType: valueType}
}

// openLevel refuses a container pushed past the ceiling (spec 5.2). Every
// container frame sits on the element frame it was read from. In a
// document reader frame 0 is the root, level 1, so a container's frame is
// even and its level top/2+1; in a value reader frame 0 is the value's
// element and its container, level 1, is frame 1, so a container's frame
// is odd and its level (top+1)/2, which is top/2+1 too. A container is
// pushed only to
// be decoded into, so this bounds the decoder's recursion; a skipped value
// or one copied as bytes opens no frame. It is checked once the
// container's header is read, so an error the vendor meets there is still
// the vendor's.
func (valueReader *reader) openLevel() error {
	if valueReader.top/2+1 > nesting.Ceiling {
		return nesting.ErrPastCeiling
	}
	return nil
}

func (valueReader *reader) frameAt(position int64) *frame {
	if position < inlineFrames {
		return &valueReader.inline[position]
	}
	return &valueReader.spilled[position-inlineFrames]
}

func (valueReader *reader) advanceFrame() {
	valueReader.top++
	if valueReader.top >= inlineFrames && valueReader.top-inlineFrames >= int64(len(valueReader.spilled)) {
		valueReader.spilled = append(valueReader.spilled, frame{})
	}
	*valueReader.current() = frame{}
}

func (valueReader *reader) current() *frame {
	return valueReader.frameAt(valueReader.top)
}

func (valueReader *reader) pushContainer(containerMode mode) error {
	valueReader.advanceFrame()
	valueReader.current().mode = containerMode
	size, err := valueReader.readLength()
	if err != nil {
		return err
	}
	valueReader.current().end = int64(size) + valueReader.offset - 4
	return valueReader.openLevel()
}

func (valueReader *reader) pushElement(elementMode mode, valueType bsontype.Type) {
	valueReader.advanceFrame()
	valueReader.current().mode = elementMode
	valueReader.current().valueType = valueType
}

func (valueReader *reader) pop() {
	switch valueReader.current().mode {
	case modeElement, modeValue:
		valueReader.top--
	case modeDocument, modeArray, modeCodeWithScope:
		// Twice, past the element frame the container was read from.
		valueReader.top -= 2
	}
}

func (valueReader *reader) invalidTransition(destination mode, name string, modes []mode) error {
	refusal := transitionError{name: name, current: valueReader.current().mode, destination: destination, modes: modes}
	if valueReader.top != 0 {
		refusal.parent = valueReader.frameAt(valueReader.top - 1).mode
	}
	return refusal
}

func (valueReader *reader) typeError(wanted bsontype.Type) error {
	return fmt.Errorf("positioned on %s, but attempted to read %s", valueReader.current().valueType, wanted)
}

func (valueReader *reader) invalidDocumentLength() error {
	return fmt.Errorf("document is invalid, end byte is at %d, but null byte found at %d", valueReader.current().end, valueReader.offset)
}

var elementModes = []mode{modeElement, modeValue}

func (valueReader *reader) ensureElementValue(wanted bsontype.Type, destination mode, name string) error {
	switch valueReader.current().mode {
	case modeElement, modeValue:
		if valueReader.current().valueType != wanted {
			return valueReader.typeError(wanted)
		}
	default:
		return valueReader.invalidTransition(destination, name, elementModes)
	}
	return nil
}

// Type is the BSON type of the value the reader is positioned on: 0 at the
// top level, which the decoders read as a document.
func (valueReader *reader) Type() bsontype.Type {
	if valueReader.text != nil {
		return valueReader.text.Type()
	}
	return valueReader.current().valueType
}

func (valueReader *reader) nextElementLength() (int32, error) {
	var length int32
	var err error
	switch valueReader.current().valueType {
	case bsontype.Array, bsontype.EmbeddedDocument, bsontype.CodeWithScope:
		length, err = valueReader.peekLength()
	case bsontype.Binary:
		length, err = valueReader.peekLength()
		length += 4 + 1
	case bsontype.Boolean:
		length = 1
	case bsontype.DBPointer:
		length, err = valueReader.peekLength()
		length += 4 + 12
	case bsontype.DateTime, bsontype.Double, bsontype.Int64, bsontype.Timestamp:
		length = 8
	case bsontype.Decimal128:
		length = 16
	case bsontype.Int32:
		length = 4
	case bsontype.JavaScript, bsontype.String, bsontype.Symbol:
		length, err = valueReader.peekLength()
		length += 4
	case bsontype.MaxKey, bsontype.MinKey, bsontype.Null, bsontype.Undefined:
		length = 0
	case bsontype.ObjectID:
		length = 12
	case bsontype.Regex:
		if err := valueReader.checkOffset(); err != nil {
			return 0, err
		}
		pattern := bytes.IndexByte(valueReader.input[valueReader.offset:], 0x00)
		if pattern < 0 {
			return 0, io.EOF
		}
		options := bytes.IndexByte(valueReader.input[valueReader.offset+int64(pattern)+1:], 0x00)
		if options < 0 {
			return 0, io.EOF
		}
		length = int32(int64(pattern) + 1 + int64(options) + 1)
	default:
		return 0, fmt.Errorf("attempted to read bytes of unknown BSON type %v", valueReader.current().valueType)
	}
	return length, err
}

// ReadValueBytes appends the bytes of the value the reader is positioned on
// to dst, uncopied from no other buffer: it is how the hooks, bson.Raw,
// bson.RawValue and the bsoncore types read a value (the vendor's
// Copier.AppendValueBytes, through its BytesReader).
func (valueReader *reader) ReadValueBytes(dst []byte) (bsontype.Type, []byte, error) {
	if valueReader.text != nil {
		return valueReader.text.AppendValueBytes(dst)
	}
	switch valueReader.current().mode {
	case modeTopLevel:
		length, err := valueReader.peekLength()
		if err != nil {
			return bsontype.Type(0), nil, err
		}
		dst, err = valueReader.appendBytes(dst, length)
		if err != nil {
			return bsontype.Type(0), nil, err
		}
		return bsontype.Type(0), dst, nil
	case modeElement, modeValue:
		length, err := valueReader.nextElementLength()
		if err != nil {
			return bsontype.Type(0), dst, err
		}
		dst, err = valueReader.appendBytes(dst, length)
		valueType := valueReader.current().valueType
		valueReader.pop()
		return valueType, dst, err
	default:
		return bsontype.Type(0), nil, valueReader.invalidTransition(0, "ReadValueBytes", elementModes)
	}
}

// Skip passes over the value the reader is positioned on without reading
// inside it (row N5). It returns whether the skip moved the read backwards,
// which only a negative length does.
func (valueReader *reader) Skip() (movedBack bool, err error) {
	if valueReader.text != nil {
		return false, valueReader.text.Skip()
	}
	switch valueReader.current().mode {
	case modeElement, modeValue:
	default:
		return false, valueReader.invalidTransition(0, "Skip", elementModes)
	}
	length, err := valueReader.nextElementLength()
	if err != nil {
		return false, err
	}
	err = valueReader.skipBytes(length)
	valueReader.pop()
	return length < 0, err
}

func (valueReader *reader) ReadArray() error {
	if valueReader.text != nil {
		return valueReader.text.ReadArray()
	}
	if err := valueReader.ensureElementValue(bsontype.Array, modeArray, "ReadArray"); err != nil {
		return err
	}
	return valueReader.pushContainer(modeArray)
}

func (valueReader *reader) ReadBinary() (payload []byte, subtype byte, err error) {
	if valueReader.text != nil {
		return valueReader.text.ReadBinary()
	}
	if err := valueReader.ensureElementValue(bsontype.Binary, 0, "ReadBinary"); err != nil {
		return nil, 0, err
	}
	length, err := valueReader.readLength()
	if err != nil {
		return nil, 0, err
	}
	subtype, err = valueReader.readByte()
	if err != nil {
		return nil, 0, err
	}
	if subtype == 0x02 && length > 4 {
		length, err = valueReader.readLength()
		if err != nil {
			return nil, 0, err
		}
	}
	inPlace, err := valueReader.readBytes(length)
	if err != nil {
		return nil, 0, err
	}
	// A copy, so the decoded value never aliases the input (row B1).
	copied := make([]byte, len(inPlace))
	copy(copied, inPlace)
	valueReader.pop()
	return copied, subtype, nil
}

func (valueReader *reader) ReadBoolean() (bool, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadBoolean()
	}
	if err := valueReader.ensureElementValue(bsontype.Boolean, 0, "ReadBoolean"); err != nil {
		return false, err
	}
	flag, err := valueReader.readByte()
	if err != nil {
		return false, err
	}
	if flag > 1 {
		return false, fmt.Errorf("invalid byte for boolean, %b", flag)
	}
	valueReader.pop()
	return flag == 1, nil
}

var documentReadModes = []mode{modeTopLevel, modeElement, modeValue}

// ReadDocument positions the reader inside the document it is on.
func (valueReader *reader) ReadDocument() error {
	if valueReader.text != nil {
		return valueReader.text.ReadDocument()
	}
	switch valueReader.current().mode {
	case modeTopLevel:
		size, err := valueReader.readLength()
		if err != nil {
			return err
		}
		if int(size) != len(valueReader.input) {
			return errors.New("invalid document length")
		}
		valueReader.current().end = int64(size) + valueReader.offset - 4
		return nil
	case modeElement, modeValue:
		if valueReader.current().valueType != bsontype.EmbeddedDocument {
			return valueReader.typeError(bsontype.EmbeddedDocument)
		}
	default:
		return valueReader.invalidTransition(modeDocument, "ReadDocument", documentReadModes)
	}
	return valueReader.pushContainer(modeDocument)
}

// ReadCodeWithScope reads the code and positions the reader inside the
// scope document.
func (valueReader *reader) ReadCodeWithScope() (string, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadCodeWithScope()
	}
	if err := valueReader.ensureElementValue(bsontype.CodeWithScope, 0, "ReadCodeWithScope"); err != nil {
		return "", err
	}
	totalLength, err := valueReader.readLength()
	if err != nil {
		return "", err
	}
	codeLength, err := valueReader.readLength()
	if err != nil {
		return "", err
	}
	if codeLength <= 0 {
		return "", fmt.Errorf("invalid string length: %d", codeLength)
	}
	codeBytes, err := valueReader.readBytes(codeLength)
	if err != nil {
		return "", err
	}
	code := string(codeBytes[:len(codeBytes)-1])
	valueReader.advanceFrame()
	valueReader.current().mode = modeCodeWithScope
	scopeSize, err := valueReader.readLength()
	if err != nil {
		return "", err
	}
	valueReader.current().end = int64(scopeSize) + valueReader.offset - 4
	componentsLength := int64(4+codeLength+4) + int64(scopeSize)
	if int64(totalLength) != componentsLength {
		return "", fmt.Errorf("length of CodeWithScope does not match lengths of components; total: %d; components: %d", totalLength, componentsLength)
	}
	if err := valueReader.openLevel(); err != nil {
		return "", err
	}
	return code, nil
}

func (valueReader *reader) ReadDBPointer() (namespace string, pointer primitive.ObjectID, err error) {
	if valueReader.text != nil {
		return valueReader.text.ReadDBPointer()
	}
	if err := valueReader.ensureElementValue(bsontype.DBPointer, 0, "ReadDBPointer"); err != nil {
		return "", pointer, err
	}
	namespace, err = valueReader.readString()
	if err != nil {
		return "", pointer, err
	}
	idBytes, err := valueReader.readBytes(12)
	if err != nil {
		return "", pointer, err
	}
	copy(pointer[:], idBytes)
	valueReader.pop()
	return namespace, pointer, nil
}

func (valueReader *reader) ReadDateTime() (int64, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadDateTime()
	}
	if err := valueReader.ensureElementValue(bsontype.DateTime, 0, "ReadDateTime"); err != nil {
		return 0, err
	}
	milliseconds, err := valueReader.readInt64()
	if err != nil {
		return 0, err
	}
	valueReader.pop()
	return milliseconds, nil
}

func (valueReader *reader) ReadDecimal128() (primitive.Decimal128, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadDecimal128()
	}
	if err := valueReader.ensureElementValue(bsontype.Decimal128, 0, "ReadDecimal128"); err != nil {
		return primitive.Decimal128{}, err
	}
	decimalBytes, err := valueReader.readBytes(16)
	if err != nil {
		return primitive.Decimal128{}, err
	}
	low := binary.LittleEndian.Uint64(decimalBytes[0:8])
	high := binary.LittleEndian.Uint64(decimalBytes[8:16])
	valueReader.pop()
	return primitive.NewDecimal128(high, low), nil
}

func (valueReader *reader) ReadDouble() (float64, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadDouble()
	}
	if err := valueReader.ensureElementValue(bsontype.Double, 0, "ReadDouble"); err != nil {
		return 0, err
	}
	bits, err := valueReader.readUint64()
	if err != nil {
		return 0, err
	}
	valueReader.pop()
	return math.Float64frombits(bits), nil
}

func (valueReader *reader) ReadInt32() (int32, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadInt32()
	}
	if err := valueReader.ensureElementValue(bsontype.Int32, 0, "ReadInt32"); err != nil {
		return 0, err
	}
	valueReader.pop()
	return valueReader.readInt32()
}

func (valueReader *reader) ReadInt64() (int64, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadInt64()
	}
	if err := valueReader.ensureElementValue(bsontype.Int64, 0, "ReadInt64"); err != nil {
		return 0, err
	}
	valueReader.pop()
	return valueReader.readInt64()
}

func (valueReader *reader) ReadJavascript() (string, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadJavascript()
	}
	if err := valueReader.ensureElementValue(bsontype.JavaScript, 0, "ReadJavascript"); err != nil {
		return "", err
	}
	valueReader.pop()
	return valueReader.readString()
}

// readEmpty reads a value with no bytes: MaxKey, MinKey, Null or Undefined.
func (valueReader *reader) readEmpty(wanted bsontype.Type, name string) error {
	if err := valueReader.ensureElementValue(wanted, 0, name); err != nil {
		return err
	}
	valueReader.pop()
	return nil
}

func (valueReader *reader) ReadMaxKey() error {
	if valueReader.text != nil {
		return valueReader.text.ReadMaxKey()
	}
	return valueReader.readEmpty(bsontype.MaxKey, "ReadMaxKey")
}

func (valueReader *reader) ReadMinKey() error {
	if valueReader.text != nil {
		return valueReader.text.ReadMinKey()
	}
	return valueReader.readEmpty(bsontype.MinKey, "ReadMinKey")
}

func (valueReader *reader) ReadNull() error {
	if valueReader.text != nil {
		return valueReader.text.ReadNull()
	}
	return valueReader.readEmpty(bsontype.Null, "ReadNull")
}

func (valueReader *reader) ReadUndefined() error {
	if valueReader.text != nil {
		return valueReader.text.ReadUndefined()
	}
	return valueReader.readEmpty(bsontype.Undefined, "ReadUndefined")
}

func (valueReader *reader) ReadObjectID() (primitive.ObjectID, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadObjectID()
	}
	if err := valueReader.ensureElementValue(bsontype.ObjectID, 0, "ReadObjectID"); err != nil {
		return primitive.ObjectID{}, err
	}
	idBytes, err := valueReader.readBytes(12)
	if err != nil {
		return primitive.ObjectID{}, err
	}
	var id primitive.ObjectID
	copy(id[:], idBytes)
	valueReader.pop()
	return id, nil
}

func (valueReader *reader) ReadRegex() (pattern, options string, err error) {
	if valueReader.text != nil {
		return valueReader.text.ReadRegex()
	}
	if err := valueReader.ensureElementValue(bsontype.Regex, 0, "ReadRegex"); err != nil {
		return "", "", err
	}
	pattern, err = valueReader.readCString()
	if err != nil {
		return "", "", err
	}
	options, err = valueReader.readCString()
	if err != nil {
		return "", "", err
	}
	valueReader.pop()
	return pattern, options, nil
}

func (valueReader *reader) ReadString() (string, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadString()
	}
	if err := valueReader.ensureElementValue(bsontype.String, 0, "ReadString"); err != nil {
		return "", err
	}
	valueReader.pop()
	return valueReader.readString()
}

func (valueReader *reader) ReadSymbol() (string, error) {
	if valueReader.text != nil {
		return valueReader.text.ReadSymbol()
	}
	if err := valueReader.ensureElementValue(bsontype.Symbol, 0, "ReadSymbol"); err != nil {
		return "", err
	}
	valueReader.pop()
	return valueReader.readString()
}

func (valueReader *reader) ReadTimestamp() (seconds, increment uint32, err error) {
	if valueReader.text != nil {
		return valueReader.text.ReadTimestamp()
	}
	if err := valueReader.ensureElementValue(bsontype.Timestamp, 0, "ReadTimestamp"); err != nil {
		return 0, 0, err
	}
	increment, err = valueReader.readUint32()
	if err != nil {
		return 0, 0, err
	}
	seconds, err = valueReader.readUint32()
	if err != nil {
		return 0, 0, err
	}
	valueReader.pop()
	return seconds, increment, nil
}

var elementReadModes = []mode{modeTopLevel, modeDocument, modeCodeWithScope}

// ReadElement reads the next element's key and positions the reader on its
// value, or returns errEndOfDocument at the document's terminator.
func (valueReader *reader) ReadElement() (string, error) {
	if valueReader.text != nil {
		key, err := valueReader.text.ReadElement()
		return key, textEnd(err)
	}
	keyBytes, err := valueReader.ReadElementKey()
	if err != nil {
		return "", err
	}
	return string(keyBytes), nil
}

// ReadElementKey is ReadElement with the key left in the document's bytes,
// for a caller that only looks it up.
func (valueReader *reader) ReadElementKey() ([]byte, error) {
	if valueReader.text != nil {
		key, err := valueReader.text.ReadElement()
		if err != nil {
			return nil, textEnd(err)
		}
		return []byte(key), nil
	}
	switch valueReader.current().mode {
	case modeTopLevel, modeDocument, modeCodeWithScope:
	default:
		return nil, valueReader.invalidTransition(modeElement, "ReadElement", elementReadModes)
	}
	typeByte, err := valueReader.readByte()
	if err != nil {
		return nil, err
	}
	if typeByte == 0 {
		if valueReader.offset != valueReader.current().end {
			return nil, valueReader.invalidDocumentLength()
		}
		valueReader.pop()
		return nil, errEndOfDocument
	}
	keyBytes, err := valueReader.readCStringBytes()
	if err != nil {
		return nil, err
	}
	valueReader.pushElement(modeElement, bsontype.Type(typeByte))
	return keyBytes, nil
}

var arrayReadModes = []mode{modeArray}

// ReadValue positions the reader on the next array value, skipping its key
// unread, or returns errEndOfArray at the array's terminator.
func (valueReader *reader) ReadValue() error {
	if valueReader.text != nil {
		return textEnd(valueReader.text.ReadValue())
	}
	if valueReader.current().mode != modeArray {
		return valueReader.invalidTransition(modeValue, "ReadValue", arrayReadModes)
	}
	typeByte, err := valueReader.readByte()
	if err != nil {
		return err
	}
	if typeByte == 0 {
		if valueReader.offset != valueReader.current().end {
			return valueReader.invalidDocumentLength()
		}
		valueReader.pop()
		return errEndOfArray
	}
	if err := valueReader.skipCString(); err != nil {
		return err
	}
	valueReader.pushElement(modeValue, bsontype.Type(typeByte))
	return nil
}

// checkOffset refuses a read position a negative skip moved before the
// start of the bytes, where the vendor's next read panics on its index.
func (valueReader *reader) checkOffset() error {
	if valueReader.offset < 0 {
		return fmt.Errorf("document is invalid: a value length moves the read to byte %d, before the start of the bytes", valueReader.offset)
	}
	return nil
}

func (valueReader *reader) readBytes(length int32) ([]byte, error) {
	if length < 0 {
		return nil, fmt.Errorf("invalid length: %d", length)
	}
	if valueReader.offset+int64(length) > int64(len(valueReader.input)) {
		return nil, io.EOF
	}
	if err := valueReader.checkOffset(); err != nil {
		return nil, err
	}
	start := valueReader.offset
	valueReader.offset += int64(length)
	return valueReader.input[start : start+int64(length)], nil
}

// appendBytes is readBytes appended to dst. The vendor does not check the
// length's sign here and panics slicing with a negative one; it is refused
// with readBytes's text.
func (valueReader *reader) appendBytes(dst []byte, length int32) ([]byte, error) {
	if valueReader.offset+int64(length) > int64(len(valueReader.input)) {
		return nil, io.EOF
	}
	if length < 0 {
		return nil, fmt.Errorf("invalid length: %d", length)
	}
	if err := valueReader.checkOffset(); err != nil {
		return nil, err
	}
	start := valueReader.offset
	valueReader.offset += int64(length)
	return append(dst, valueReader.input[start:start+int64(length)]...), nil
}

// skipBytes moves the read by length, backwards for a negative one as the
// vendor does; a read from before the start is refused by checkOffset.
func (valueReader *reader) skipBytes(length int32) error {
	if valueReader.offset+int64(length) > int64(len(valueReader.input)) {
		return io.EOF
	}
	valueReader.offset += int64(length)
	return nil
}

func (valueReader *reader) readByte() (byte, error) {
	if valueReader.offset+1 > int64(len(valueReader.input)) {
		return 0x0, io.EOF
	}
	if err := valueReader.checkOffset(); err != nil {
		return 0, err
	}
	valueReader.offset++
	return valueReader.input[valueReader.offset-1], nil
}

func (valueReader *reader) skipCString() error {
	if err := valueReader.checkOffset(); err != nil {
		return err
	}
	terminator := bytes.IndexByte(valueReader.input[valueReader.offset:], 0x00)
	if terminator < 0 {
		return io.EOF
	}
	valueReader.offset += int64(terminator) + 1
	return nil
}

func (valueReader *reader) readCString() (string, error) {
	text, err := valueReader.readCStringBytes()
	return string(text), err
}

// readCStringBytes is readCString uncopied, as a slice of the input.
func (valueReader *reader) readCStringBytes() ([]byte, error) {
	if err := valueReader.checkOffset(); err != nil {
		return nil, err
	}
	terminator := bytes.IndexByte(valueReader.input[valueReader.offset:], 0x00)
	if terminator < 0 {
		return nil, io.EOF
	}
	start := valueReader.offset
	valueReader.offset += int64(terminator) + 1
	return valueReader.input[start : start+int64(terminator)], nil
}

func (valueReader *reader) readString() (string, error) {
	length, err := valueReader.readLength()
	if err != nil {
		return "", err
	}
	if int64(length)+valueReader.offset > int64(len(valueReader.input)) {
		return "", io.EOF
	}
	if length <= 0 {
		return "", fmt.Errorf("invalid string length: %d", length)
	}
	if valueReader.input[valueReader.offset+int64(length)-1] != 0x00 {
		return "", fmt.Errorf("string does not end with null byte, but with %v", valueReader.input[valueReader.offset+int64(length)-1])
	}
	start := valueReader.offset
	valueReader.offset += int64(length)
	return string(valueReader.input[start : start+int64(length)-1]), nil
}

func (valueReader *reader) peekLength() (int32, error) {
	if valueReader.offset+4 > int64(len(valueReader.input)) {
		return 0, io.EOF
	}
	if err := valueReader.checkOffset(); err != nil {
		return 0, err
	}
	return int32(binary.LittleEndian.Uint32(valueReader.input[valueReader.offset:])), nil
}

func (valueReader *reader) readLength() (int32, error) { return valueReader.readInt32() }

func (valueReader *reader) readInt32() (int32, error) {
	number, err := valueReader.readUint32()
	return int32(number), err
}

func (valueReader *reader) readUint32() (uint32, error) {
	if valueReader.offset+4 > int64(len(valueReader.input)) {
		return 0, io.EOF
	}
	if err := valueReader.checkOffset(); err != nil {
		return 0, err
	}
	start := valueReader.offset
	valueReader.offset += 4
	return binary.LittleEndian.Uint32(valueReader.input[start:]), nil
}

func (valueReader *reader) readInt64() (int64, error) {
	number, err := valueReader.readUint64()
	return int64(number), err
}

func (valueReader *reader) readUint64() (uint64, error) {
	if valueReader.offset+8 > int64(len(valueReader.input)) {
		return 0, io.EOF
	}
	if err := valueReader.checkOffset(); err != nil {
		return 0, err
	}
	start := valueReader.offset
	valueReader.offset += 8
	return binary.LittleEndian.Uint64(valueReader.input[start:]), nil
}
