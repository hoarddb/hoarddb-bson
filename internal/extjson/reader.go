// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsonrw/extjson_reader.go: Reader keeps its root peek, the code-with-scope end peek and its error text, for a caller that reads every value in order; frames, pools, Skip and the per-type Read methods are replaced by ReadScalar.

package extjson

import (
	"errors"
	"fmt"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// Reader walks one Extended JSON text value by value, the way the vendor's
// ValueReader does for a caller that reads every value it is shown, in
// order. AppendDocument drives it to write BSON bytes. The decoder into Go
// values (the second half of step 5b) drives it too, so the values a map or
// a bson.D receives come from the same reads, before any byte is written
// (docs/core-api.md, "Step 5b core"). It reports what was read, as read:
// it does not sort regular expression options and does not refuse a key or
// a pattern holding a zero byte, because the vendor's Go-value targets
// accept them and only its byte writer refuses them.
//
// A Reader is not safe for concurrent use.
type Reader struct {
	parser *parser
}

// Scalar is one value that is not a document, an array or a code with
// scope. Type says which fields are set.
type Scalar struct {
	Type bsontype.Type
	// Text is a String, JavaScript or Symbol value, a Regex pattern, or a
	// DBPointer namespace.
	Text string
	// Options is a Regex's options, in input order.
	Options string
	Double  float64
	// Int64 is an Int64, or a DateTime in milliseconds since the epoch.
	Int64    int64
	Int32    int32
	Boolean  bool
	Binary   []byte
	Subtype  byte
	ObjectID primitive.ObjectID // an ObjectID, or a DBPointer's id
	// TimestampSeconds and TimestampOrdinal are a Timestamp's t and i.
	TimestampSeconds uint32
	TimestampOrdinal uint32
	Decimal128       primitive.Decimal128
}

// NewReader peeks the first value of text and returns a Reader positioned
// on it with its type: EmbeddedDocument for an object, Array, or a scalar
// type. A text whose first value cannot be read is ErrInvalidJSON, which is
// all the vendor says about it. The root's first key is never a wrapper:
// {"$numberInt": "1"} at the root is a document.
func NewReader(text []byte, canonical bool) (*Reader, bsontype.Type, error) {
	textParser := newParser(text, canonical)
	rootType, err := textParser.peekType()
	if err != nil {
		return nil, 0, ErrInvalidJSON
	}
	return &Reader{parser: textParser}, rootType, nil
}

// ReadElement reads the next key of the open document and returns it with
// its value's type. At the end of the document it returns ErrEndOfDocument.
// inScope says the document is the scope of a code with scope, whose end
// also reads the closing brace of the wrapper around it.
func (reader *Reader) ReadElement(inScope bool) (string, bsontype.Type, error) {
	key, valueType, err := reader.parser.readKey()
	if err != nil {
		if errors.Is(err, ErrEndOfDocument) && inScope {
			if _, closeErr := reader.parser.peekType(); closeErr != nil {
				return "", 0, closeErr
			}
		}
		return "", 0, err
	}
	return key, valueType, nil
}

// ReadArrayValue reads up to the open array's next value and returns its
// type. At the end of the array it returns ErrEndOfArray.
func (reader *Reader) ReadArrayValue() (bsontype.Type, error) {
	return reader.parser.peekType()
}

// ReadCodeWithScope reads the code of a code with scope and the opening of
// its scope, whose elements ReadElement then reads with inScope set.
func (reader *Reader) ReadCodeWithScope() (string, error) {
	code, err := reader.parser.readValue(bsontype.CodeWithScope)
	if err != nil {
		return "", err
	}
	return code.codeText()
}

// ReadScalar reads the value that ReadElement or ReadArrayValue announced
// as valueType, which is not EmbeddedDocument, Array or CodeWithScope.
func (reader *Reader) ReadScalar(valueType bsontype.Type) (Scalar, error) {
	wrapped, err := reader.parser.readValue(valueType)
	if err != nil {
		return Scalar{}, err
	}
	read := Scalar{Type: valueType}
	switch valueType {
	case bsontype.Double:
		read.Double, err = wrapped.doubleValue()
	case bsontype.String:
		read.Text, err = wrapped.stringText()
	case bsontype.Binary:
		read.Binary, read.Subtype, err = wrapped.binaryValue()
	case bsontype.Undefined:
		err = wrapped.checkUndefined()
	case bsontype.ObjectID:
		read.ObjectID, err = wrapped.objectIDValue()
	case bsontype.Boolean:
		read.Boolean, err = wrapped.booleanFlag()
	case bsontype.DateTime:
		read.Int64, err = wrapped.dateTimeValue()
	case bsontype.Null:
		err = wrapped.checkNull()
	case bsontype.Regex:
		read.Text, read.Options, err = wrapped.regexValue()
	case bsontype.DBPointer:
		read.Text, read.ObjectID, err = wrapped.dbPointerValue()
	case bsontype.JavaScript:
		read.Text, err = wrapped.codeText()
	case bsontype.Symbol:
		read.Text, err = wrapped.symbolText()
	case bsontype.Int32:
		read.Int32, err = wrapped.int32Value()
	case bsontype.Timestamp:
		read.TimestampSeconds, read.TimestampOrdinal, err = wrapped.timestampValue()
	case bsontype.Int64:
		read.Int64, err = wrapped.int64Value()
	case bsontype.Decimal128:
		read.Decimal128, err = wrapped.decimal128Value()
	case bsontype.MinKey:
		err = wrapped.checkKeyFlag("min")
	case bsontype.MaxKey:
		err = wrapped.checkKeyFlag("max")
	default:
		//nolint:staticcheck // ST1005: the vendor's capitalised text, kept because error text is the contract (spec 9.1.5).
		return Scalar{}, fmt.Errorf("Cannot copy unknown BSON type %s", valueType)
	}
	if err != nil {
		return Scalar{}, err
	}
	return read, nil
}

// The vendor's reader checks these kinds again after reading, with this text.

func (wrapped jsonValue) stringText() (string, error) {
	if wrapped.kind != bsontype.String {
		return "", fmt.Errorf("expected type string but got type %s", wrapped.kind)
	}
	return wrapped.text, nil
}

func (wrapped jsonValue) booleanFlag() (bool, error) {
	if wrapped.kind != bsontype.Boolean {
		return false, fmt.Errorf("expected type bool, but got type %s", wrapped.kind)
	}
	return wrapped.flag, nil
}

func (wrapped jsonValue) checkNull() error {
	if wrapped.kind != bsontype.Null {
		return fmt.Errorf("expected type null but got type %s", wrapped.kind)
	}
	return nil
}
