// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsonrw/extjson_reader.go (the frame stack, its modes, the transition and type errors with their text, Skip and its skipObject, and the end-of-document peek of a code with scope) and the AppendDocumentBytes, AppendArrayBytes and AppendValueBytes of bson/bsonrw/copier.go: one ValueReader over the step 5b parser for the Go-value decoder, which reads no value bytes it does not decode; a skip the vendor never finishes is an error, and a level past the ceiling of 200 is refused.

package extjson

import (
	"errors"
	"fmt"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/nesting"
)

// ValueReader is the vendor's extJSONValueReader: the value reader the
// Go-value decoder (internal/decode, for bson.UnmarshalExtJSON) reads
// Extended JSON through. Its modes, its checks and their order, and every
// error text are the vendor's, because the decoder's error text and the
// partly filled target follow from them (spec 9.1.5). What it reads is
// reported as read: a key or a pattern holding a zero byte is not refused,
// and regular expression options are not sorted, except where the vendor
// copies a value into BSON bytes (AppendDocumentBytes, AppendArrayBytes,
// AppendValueBytes), whose byte writer refuses and sorts.
//
// Two departures, both for spec 5.2 and ruling O17: a document, array or
// scope pushed past MaxNestingDepth levels (the root as 1) is refused with
// nesting.ErrPastCeiling, so the decoder's recursion is bounded; and a
// skipped value whose text ends or breaks before it closes is an error,
// where the vendor's skipObject loops for ever.
type ValueReader struct {
	reader Reader
	frames []readFrame
	top    int
	// levels is how many documents, arrays and scopes are open, the root
	// document or array included.
	levels int
}

type readMode byte

// The vendor's modes, numbered as its mode type is, which only its
// TypeString names show.
const (
	readTopLevel readMode = iota + 1
	readDocument
	readArray
	readValue
	readElement
	readCodeWithScope
)

func (mode readMode) typeString() string {
	switch mode {
	case readTopLevel:
		return "TopLevel"
	case readDocument:
		return "Document"
	case readArray:
		return "Array"
	case readValue:
		return "Value"
	case readElement:
		return "Element"
	case readCodeWithScope:
		return "CodeWithScope"
	}
	return "Unknown"
}

type readFrame struct {
	mode      readMode
	valueType bsontype.Type
}

// NewValueReader peeks the first value of text, as the vendor's
// NewExtJSONValueReader does: a text whose first value cannot be read is
// ErrInvalidJSON. An object root is the top level, an array root an array,
// and any other root one value.
func NewValueReader(text []byte, canonical bool) (*ValueReader, error) {
	textParser := newParser(text, canonical)
	rootType, err := textParser.peekType()
	if err != nil {
		return nil, ErrInvalidJSON
	}
	valueReader := &ValueReader{reader: Reader{parser: textParser}, frames: make([]readFrame, 1, 8)}
	switch rootType {
	case bsontype.EmbeddedDocument:
		valueReader.frames[0] = readFrame{mode: readTopLevel, valueType: rootType}
		valueReader.levels = 1
	case bsontype.Array:
		valueReader.frames[0] = readFrame{mode: readArray, valueType: rootType}
		valueReader.levels = 1
	default:
		valueReader.frames[0] = readFrame{mode: readValue, valueType: rootType}
	}
	return valueReader, nil
}

func (valueReader *ValueReader) current() *readFrame { return &valueReader.frames[valueReader.top] }

func (valueReader *ValueReader) push(mode readMode, valueType bsontype.Type) {
	valueReader.top++
	if valueReader.top == len(valueReader.frames) {
		valueReader.frames = append(valueReader.frames, readFrame{})
	}
	valueReader.frames[valueReader.top] = readFrame{mode: mode, valueType: valueType}
}

// pushContainer pushes a document, array or scope frame, refusing one past
// the ceiling once it is pushed, as the BSON decoder refuses it once the
// container's header is read.
func (valueReader *ValueReader) pushContainer(mode readMode) error {
	valueReader.push(mode, 0)
	valueReader.levels++
	if valueReader.levels > MaxNestingDepth {
		return nesting.ErrPastCeiling
	}
	return nil
}

// pop is the vendor's pop: an element or value frame goes, and a container
// takes the element it was read from with it. The root frame is never
// popped below; the vendor's frame index goes negative there, after the
// last read.
func (valueReader *ValueReader) pop() {
	switch valueReader.current().mode {
	case readElement, readValue:
		valueReader.top--
	case readDocument, readArray, readCodeWithScope:
		valueReader.top -= 2
		valueReader.levels--
	}
	valueReader.top = max(valueReader.top, 0)
}

// transitionError is the vendor's TransitionError for a read, with its
// text.
type transitionError struct {
	name        string
	parent      readMode
	current     readMode
	destination readMode
	modes       []readMode
}

func (refusal transitionError) Error() string {
	text := refusal.name + " can only read"
	if refusal.destination != 0 {
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
	if refusal.parent != 0 {
		text += " with parent " + refusal.parent.typeString()
	}
	return text
}

func (valueReader *ValueReader) invalidTransition(destination readMode, name string, modes []readMode) error {
	refusal := transitionError{name: name, current: valueReader.current().mode, destination: destination, modes: modes}
	if valueReader.top != 0 {
		refusal.parent = valueReader.frames[valueReader.top-1].mode
	}
	return refusal
}

func (valueReader *ValueReader) typeError(wanted bsontype.Type) error {
	return fmt.Errorf("positioned on %s, but attempted to read %s", valueReader.current().valueType, wanted)
}

var elementOrValue = []readMode{readElement, readValue}

// ensureElementValue checks that the reader is on one value of wanted.
func (valueReader *ValueReader) ensureElementValue(wanted bsontype.Type, destination readMode, name string, extraModes ...readMode) error {
	switch valueReader.current().mode {
	case readElement, readValue:
		if valueReader.current().valueType != wanted {
			return valueReader.typeError(wanted)
		}
	default:
		modes := append(append([]readMode(nil), elementOrValue...), extraModes...)
		return valueReader.invalidTransition(destination, name, modes)
	}
	return nil
}

// Type is the type of the value the reader is on.
func (valueReader *ValueReader) Type() bsontype.Type { return valueReader.current().valueType }

// ErrSkipNeverEnds is under the refusal of a skipped value whose text ends,
// or stops parsing, before the value closes. The vendor's skipObject loops
// for ever on such a text; the refusal names the parser's error.
var ErrSkipNeverEnds = errors.New("the value the decoder skips never ends")

// Skip passes over the value the reader is on: a container token by token,
// without checking what is inside it, as the vendor's skipObject does, and
// a scalar by reading it.
func (valueReader *ValueReader) Skip() error {
	switch valueReader.current().mode {
	case readElement, readValue:
	default:
		return valueReader.invalidTransition(0, "Skip", elementOrValue)
	}
	defer valueReader.pop()
	switch valueType := valueReader.current().valueType; valueType {
	case bsontype.Array, bsontype.EmbeddedDocument, bsontype.CodeWithScope:
		return valueReader.skipObject()
	default:
		_, err := valueReader.reader.parser.readValue(valueType)
		return err
	}
}

// skipObject reads tokens until the depth the skip started at closes. At a
// state where the vendor's next advance does nothing (the parser has failed
// or the text has ended) and the depth cannot fall, the vendor loops for
// ever; this returns the parser's error instead.
func (valueReader *ValueReader) skipObject() error {
	textParser := valueReader.reader.parser
	depth := 1
	for depth > 0 {
		if !textParser.emptyObject && (textParser.state == stateInvalid || textParser.state == stateDone) {
			cause := textParser.err
			if cause == nil {
				cause = ErrInvalidJSON
			}
			return fmt.Errorf("%w: %w", cause, ErrSkipNeverEnds)
		}
		textParser.advance()
		// An empty object peekType read whole: the next token belongs to the
		// document around it.
		if textParser.emptyObject {
			if textParser.state == stateComma {
				textParser.emptyObject = false
				textParser.advance()
			}
			depth--
			continue
		}
		switch textParser.state {
		case stateBeginObject, stateBeginArray:
			depth++
		case stateEndObject, stateEndArray:
			depth--
		}
	}
	return nil
}

// ReadArray positions the reader inside the array it is on.
func (valueReader *ValueReader) ReadArray() error {
	switch valueReader.current().mode {
	case readTopLevel:
		// The vendor reads an array at the top level.
	case readArray:
		return nil
	default:
		if err := valueReader.ensureElementValue(bsontype.Array, readArray, "ReadArray", readTopLevel, readArray); err != nil {
			return err
		}
	}
	return valueReader.pushContainer(readArray)
}

// ReadDocument positions the reader inside the document it is on.
func (valueReader *ValueReader) ReadDocument() error {
	switch valueReader.current().mode {
	case readTopLevel:
		return nil
	case readElement, readValue:
		if valueReader.current().valueType != bsontype.EmbeddedDocument {
			return valueReader.typeError(bsontype.EmbeddedDocument)
		}
		return valueReader.pushContainer(readDocument)
	default:
		return valueReader.invalidTransition(readDocument, "ReadDocument", []readMode{readTopLevel, readElement, readValue})
	}
}

// ReadCodeWithScope reads the code and positions the reader inside the
// scope. The scope is pushed even when the code's check fails, as the
// vendor pushes it.
func (valueReader *ValueReader) ReadCodeWithScope() (string, error) {
	if err := valueReader.ensureElementValue(bsontype.CodeWithScope, 0, "ReadCodeWithScope"); err != nil {
		return "", err
	}
	code, err := valueReader.reader.ReadCodeWithScope()
	if pushErr := valueReader.pushContainer(readCodeWithScope); pushErr != nil && err == nil {
		err = pushErr
	}
	return code, err
}

// ReadElement reads the next key of the open document and positions the
// reader on its value. At the end of the document it returns
// ErrEndOfDocument; at the end of a scope it first reads the closing brace
// of the wrapper around it.
func (valueReader *ValueReader) ReadElement() (string, error) {
	switch valueReader.current().mode {
	case readTopLevel, readDocument, readCodeWithScope:
	default:
		return "", valueReader.invalidTransition(readElement, "ReadElement", []readMode{readTopLevel, readDocument, readCodeWithScope})
	}
	key, valueType, err := valueReader.reader.parser.readKey()
	if err != nil {
		if errors.Is(err, ErrEndOfDocument) {
			if valueReader.current().mode == readCodeWithScope {
				if _, peekErr := valueReader.reader.parser.peekType(); peekErr != nil {
					return "", peekErr
				}
			}
			valueReader.pop()
		}
		return "", err
	}
	valueReader.push(readElement, valueType)
	return key, nil
}

// ReadValue positions the reader on the next value of the open array, or
// returns ErrEndOfArray at its end.
func (valueReader *ValueReader) ReadValue() error {
	if valueReader.current().mode != readArray {
		return valueReader.invalidTransition(readValue, "ReadValue", []readMode{readArray})
	}
	valueType, err := valueReader.reader.parser.peekType()
	if err != nil {
		if errors.Is(err, ErrEndOfArray) {
			valueReader.pop()
		}
		return err
	}
	valueReader.push(readValue, valueType)
	return nil
}

// readScalar is the vendor's typed Read method for valueType, name: the
// position and type check, the read, then the pop. A value the parser
// cannot read leaves the frame, as the vendor's does.
func (valueReader *ValueReader) readScalar(valueType bsontype.Type, name string) (Scalar, error) {
	if err := valueReader.ensureElementValue(valueType, 0, name); err != nil {
		return Scalar{}, err
	}
	scalar, err := valueReader.reader.ReadScalar(valueType)
	if err != nil {
		return Scalar{}, err
	}
	valueReader.pop()
	return scalar, nil
}

// ReadBinary reads a binary value.
func (valueReader *ValueReader) ReadBinary() ([]byte, byte, error) {
	scalar, err := valueReader.readScalar(bsontype.Binary, "ReadBinary")
	return scalar.Binary, scalar.Subtype, err
}

// ReadBoolean reads a boolean.
func (valueReader *ValueReader) ReadBoolean() (bool, error) {
	scalar, err := valueReader.readScalar(bsontype.Boolean, "ReadBoolean")
	return scalar.Boolean, err
}

// ReadDBPointer reads a DBPointer.
func (valueReader *ValueReader) ReadDBPointer() (string, primitive.ObjectID, error) {
	scalar, err := valueReader.readScalar(bsontype.DBPointer, "ReadDBPointer")
	return scalar.Text, scalar.ObjectID, err
}

// ReadDateTime reads a DateTime in milliseconds since the epoch.
func (valueReader *ValueReader) ReadDateTime() (int64, error) {
	scalar, err := valueReader.readScalar(bsontype.DateTime, "ReadDateTime")
	return scalar.Int64, err
}

// ReadDecimal128 reads a Decimal128.
func (valueReader *ValueReader) ReadDecimal128() (primitive.Decimal128, error) {
	scalar, err := valueReader.readScalar(bsontype.Decimal128, "ReadDecimal128")
	return scalar.Decimal128, err
}

// ReadDouble reads a double.
func (valueReader *ValueReader) ReadDouble() (float64, error) {
	scalar, err := valueReader.readScalar(bsontype.Double, "ReadDouble")
	return scalar.Double, err
}

// ReadInt32 reads an int32.
func (valueReader *ValueReader) ReadInt32() (int32, error) {
	scalar, err := valueReader.readScalar(bsontype.Int32, "ReadInt32")
	return scalar.Int32, err
}

// ReadInt64 reads an int64.
func (valueReader *ValueReader) ReadInt64() (int64, error) {
	scalar, err := valueReader.readScalar(bsontype.Int64, "ReadInt64")
	return scalar.Int64, err
}

// ReadJavascript reads JavaScript code.
func (valueReader *ValueReader) ReadJavascript() (string, error) {
	scalar, err := valueReader.readScalar(bsontype.JavaScript, "ReadJavascript")
	return scalar.Text, err
}

// ReadMaxKey reads a MaxKey.
func (valueReader *ValueReader) ReadMaxKey() error {
	_, err := valueReader.readScalar(bsontype.MaxKey, "ReadMaxKey")
	return err
}

// ReadMinKey reads a MinKey.
func (valueReader *ValueReader) ReadMinKey() error {
	_, err := valueReader.readScalar(bsontype.MinKey, "ReadMinKey")
	return err
}

// ReadNull reads a null.
func (valueReader *ValueReader) ReadNull() error {
	_, err := valueReader.readScalar(bsontype.Null, "ReadNull")
	return err
}

// ReadUndefined reads an undefined.
func (valueReader *ValueReader) ReadUndefined() error {
	_, err := valueReader.readScalar(bsontype.Undefined, "ReadUndefined")
	return err
}

// ReadObjectID reads an ObjectID.
func (valueReader *ValueReader) ReadObjectID() (primitive.ObjectID, error) {
	scalar, err := valueReader.readScalar(bsontype.ObjectID, "ReadObjectID")
	return scalar.ObjectID, err
}

// ReadRegex reads a regular expression, its options as written.
func (valueReader *ValueReader) ReadRegex() (string, string, error) {
	scalar, err := valueReader.readScalar(bsontype.Regex, "ReadRegex")
	return scalar.Text, scalar.Options, err
}

// ReadString reads a string.
func (valueReader *ValueReader) ReadString() (string, error) {
	scalar, err := valueReader.readScalar(bsontype.String, "ReadString")
	return scalar.Text, err
}

// ReadSymbol reads a symbol.
func (valueReader *ValueReader) ReadSymbol() (string, error) {
	scalar, err := valueReader.readScalar(bsontype.Symbol, "ReadSymbol")
	return scalar.Text, err
}

// ReadTimestamp reads a timestamp's seconds and increment.
func (valueReader *ValueReader) ReadTimestamp() (uint32, uint32, error) {
	scalar, err := valueReader.readScalar(bsontype.Timestamp, "ReadTimestamp")
	return scalar.TimestampSeconds, scalar.TimestampOrdinal, err
}

// AppendDocumentBytes appends the BSON of the document the reader is on to
// dst, as the vendor's Copier.AppendDocumentBytes does through a value
// writer at its top level: the document is read as ReadDocument reads it
// (a value that is not a document is the same type error), then copied,
// with the byte writer's refusals. On an error the bytes written so far are
// returned with it. The copy descends without the decoder, so it opens no
// level of the ceiling.
func (valueReader *ValueReader) AppendDocumentBytes(dst []byte) ([]byte, error) {
	switch valueReader.current().mode {
	case readTopLevel:
		return valueReader.reader.appendDocument(dst)
	case readElement, readValue:
		if valueReader.current().valueType != bsontype.EmbeddedDocument {
			return dst, valueReader.typeError(bsontype.EmbeddedDocument)
		}
	default:
		return dst, valueReader.invalidTransition(readDocument, "ReadDocument", []readMode{readTopLevel, readElement, readValue})
	}
	// The document's frame and the element's go when the copy reads its
	// end, as the vendor's ReadElement pops them.
	valueReader.pop()
	return valueReader.reader.appendDocument(dst)
}

// AppendArrayBytes is the vendor's Copier.AppendArrayBytes: it checks the
// value as ReadArray does, then fails as its value writer, positioned at
// its top level, refuses to write an array there. It returns dst. The
// decode stops at the error, so the array's frame is not pushed.
func (valueReader *ValueReader) AppendArrayBytes(dst []byte) ([]byte, error) {
	switch valueReader.current().mode {
	case readTopLevel, readArray:
	default:
		if err := valueReader.ensureElementValue(bsontype.Array, readArray, "ReadArray", readTopLevel, readArray); err != nil {
			return dst, err
		}
	}
	return dst, TopLevelWriteError("WriteArray", "Array")
}

// AppendValueBytes appends the BSON of the value the reader is on and
// returns its type and its bytes, as the vendor's Copier.AppendValueBytes
// does through a value writer on an element: the value is read with the
// checks of the vendor's Read method for its type, and written with the
// byte writer's refusals and its sorting of regex options. On an error it
// returns type 0 and dst.
func (valueReader *ValueReader) AppendValueBytes(dst []byte) (bsontype.Type, []byte, error) {
	frame := *valueReader.current()
	switch frame.mode {
	case readTopLevel, readArray:
		// The root: the vendor reads it without a push, so there is no frame
		// to pop.
	case readElement, readValue:
		if err := valueReader.checkCopyable(frame.valueType); err != nil {
			return 0, dst, err
		}
		valueReader.pop()
	default:
		return 0, dst, valueReader.invalidTransition(0, valueReadName(frame.valueType), elementOrValue)
	}
	start := len(dst)
	written, err := valueReader.reader.appendValue(dst, frame.valueType)
	if err != nil {
		return 0, dst, err
	}
	return bsontype.Type(written[start]), written[start+2:], nil
}

// checkCopyable is the check the vendor's CopyValue makes before it reads
// a value of valueType: an unknown type cannot be copied.
func (valueReader *ValueReader) checkCopyable(valueType bsontype.Type) error {
	if valueReadName(valueType) == "" {
		//nolint:staticcheck // ST1005: the vendor's capitalised text is the contract.
		return fmt.Errorf("Cannot copy unknown BSON type %s", valueType)
	}
	return nil
}

// valueReadName names the vendor's Read method for valueType, "" for a
// type it has none for.
func valueReadName(valueType bsontype.Type) string {
	switch valueType {
	case bsontype.EmbeddedDocument:
		return "ReadDocument"
	case bsontype.Array:
		return "ReadArray"
	case bsontype.CodeWithScope:
		return "ReadCodeWithScope"
	}
	if name, known := scalarWriteMethods[valueType]; known {
		return "Read" + name[len("Write"):]
	}
	return ""
}
