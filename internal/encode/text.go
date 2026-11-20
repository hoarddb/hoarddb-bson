// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package encode

import (
	"reflect"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
)

// The text mode: the Go value walked by the same plans and in the same
// order as for BSON, each write sent to extjson.TextWriter instead of the
// byte buffer. This is the vendor's MarshalExtJSON, which runs its encoders
// over its Extended JSON value writer rather than encoding BSON first, and
// the four places where the two answers differ follow from it
// (docs/core-api.md, "Step 5a core"):
//
//   - a zero byte in a key or a regex is not refused, and no size is
//     checked, because only the BSON writer makes those checks;
//   - bytes the vendor copies (bson.Raw, the bsoncore types, RawValue and
//     the hooks' results) are read from their own bytes, so their checks
//     and error offsets start there;
//   - the top level refuses every value but a document with the writer's
//     transition error, as header does in BSON mode, except that a hook's
//     or a RawValue's document is copied there (the text writer is no
//     BytesWriter);
//   - a code with scope's scope is still encoded to BSON first, as the
//     vendor encodes it, so its zero-byte refusals stay.

// Text encodes value as one Extended JSON document, as the vendor's
// bson.MarshalExtJSON does, canonical or relaxed, with '<', '>' and '&'
// escaped when escapeHTML is set. A map's entries are written in ascending
// byte order of their keys. A level past the ceiling of 200 (documents,
// arrays and scopes, the root as level 1, the levels of copied bytes
// included) is refused with an error matching the nesting sentinel. The
// result is a new slice the caller owns.
func (encoder *Encoder) Text(value any, canonical, escapeHTML bool) ([]byte, error) {
	encoding := encoder.acquire()
	defer encoding.release()
	encoding.textMode = true
	encoding.text.Reset(canonical, escapeHTML)

	if marshaler, ok := value.(plan.Marshaler); ok {
		if err := refuseNilValueReceiver(reflect.ValueOf(value), marshalerType); err != nil {
			return nil, err
		}
		documentBytes, err := marshaler.MarshalBSON()
		if err != nil {
			return nil, err
		}
		if err := encoding.copyDocument(topLevel(), documentBytes); err != nil {
			return nil, err
		}
		return encoding.textOutput(), nil
	}
	if value == nil {
		return nil, noEncoderError{}
	}
	typePlan, err := encoder.cache.Lookup(reflect.TypeOf(value))
	if err != nil {
		return nil, err
	}
	if err := encoding.encodeValue(typePlan, reflect.ValueOf(value), topLevel(), false); err != nil {
		return nil, err
	}
	return encoding.textOutput(), nil
}

// textOutput copies the text out of the pooled writer.
func (encoding *writer) textOutput() []byte {
	return append([]byte(nil), encoding.text.Bytes()...)
}

// textHeader is header in text mode: a document element's key, nothing for
// an array value, and at the top level the writer's transition error. The
// key is not checked for a zero byte; the text writer escapes it.
func (encoding *writer) textHeader(at slot, method, destination string) error {
	switch at.kind {
	case elementSlot:
		encoding.text.Key(at.key)
	case arraySlot:
	default:
		return transitionError{method: method, destination: destination}
	}
	return nil
}

// writeEmpty writes a value that has no bytes: a null, an undefined, a
// MinKey or a MaxKey.
func (encoding *writer) writeEmpty(at slot, valueType bsontype.Type, method string) error {
	if err := encoding.scalarHeader(at, valueType, method); err != nil {
		return err
	}
	if !encoding.textMode {
		return nil
	}
	switch valueType {
	case bsontype.Null:
		encoding.text.WriteNull()
	case bsontype.Undefined:
		encoding.text.WriteUndefined()
	case bsontype.MinKey:
		encoding.text.WriteMinKey()
	default:
		encoding.text.WriteMaxKey()
	}
	return nil
}

// writeTextString writes the three string-shaped types as text.
func (encoding *writer) writeTextString(stringType bsontype.Type, value string) {
	switch stringType {
	case bsontype.JavaScript:
		encoding.text.WriteJavaScript(value)
	case bsontype.Symbol:
		encoding.text.WriteSymbol(value)
	default:
		encoding.text.WriteString(value)
	}
}

// objectIDOf reads an ObjectID value, which need not be addressable.
func objectIDOf(val reflect.Value) primitive.ObjectID {
	var objectID primitive.ObjectID
	for position := range objectID {
		objectID[position] = byte(val.Index(position).Uint())
	}
	return objectID
}

// copyTextValue is writeValueBytes in text mode: the vendor's
// CopyValueFromBytes onto its Extended JSON writer, which reads the bytes
// with a fresh value reader. At the top level a document is copied and any
// other value refused after it is read.
func (encoding *writer) copyTextValue(at slot, valueType bsontype.Type, valueBytes []byte) error {
	if at.isTopLevel() {
		return encoding.text.CopyValue(valueType, valueBytes, true)
	}
	if err := encoding.textHeader(at, "WriteValueBytes", ""); err != nil {
		return err
	}
	return encoding.text.CopyValue(valueType, valueBytes, false)
}

// writeTextCodeWithScope is the vendor's CodeWithScopeEncodeValue on its
// Extended JSON writer, after the header: the code and the scope's opening
// are written, the scope is encoded to BSON at a fresh top level (a nil
// scope has no encoder), and its bytes are copied into the scope, which is
// then closed. The scope's own levels continue the count of the text's.
func (encoding *writer) writeTextCodeWithScope(val reflect.Value, minSize bool) error {
	if err := encoding.text.OpenScope(val.Field(0).String()); err != nil {
		return err
	}
	scope := val.Field(1)
	if scope.IsNil() {
		return noEncoderError{}
	}
	scopeEncoding := writerPool.Get().(*writer)
	scopeEncoding.cache = encoding.cache
	scopeEncoding.buf = scopeEncoding.buf[:0]
	scopeEncoding.textMode = false
	// The scope's root document is the level OpenScope opened.
	scopeEncoding.levels = encoding.text.Level() - 1
	defer scopeEncoding.release()
	if err := scopeEncoding.encodeDynamic(scope.Elem(), topLevel(), minSize); err != nil {
		return err
	}
	if err := encoding.text.CopyScopeElements(scopeEncoding.buf); err != nil {
		return err
	}
	encoding.text.Close()
	return nil
}
