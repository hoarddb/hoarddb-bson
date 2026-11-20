// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package extjson

import (
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// TextWriter is the Extended JSON writer the Go-value encoder drives
// (internal/encode, for bson.MarshalExtJSON): the vendor's
// extJSONValueWriter's operations, one call per value, over the same text
// writer AppendText uses. The encoder decides where a value goes and
// refuses a value at the top level with the vendor's text, as it does for
// BSON; the TextWriter writes the key, the value, and the bytes the vendor
// copies from their own bytes (bson.Raw, bsoncore types, RawValue, hooks).
//
// A level past MaxNestingDepth (documents, arrays and scopes, the root
// counted as 1) is refused with an error matching ErrNestingTooDeep, which
// is bson.ErrNestingTooDeep.
type TextWriter struct {
	writer textWriter
	// kinds holds, per open level, whether it is a document, an array or
	// a scope, so Close knows which closer to write, and the root document
	// is closed without the comma that follows a value.
	kinds []levelKind
}

type levelKind byte

const (
	documentLevel levelKind = iota
	arrayLevel
	scopeLevel
)

// Reset empties the writer and sets its mode, keeping its buffers.
func (text *TextWriter) Reset(canonical, escapeHTML bool) {
	text.writer = textWriter{buffer: text.writer.buffer[:0], canonical: canonical, escapeHTML: escapeHTML}
	text.kinds = text.kinds[:0]
}

// Bytes returns the text written, which the writer still owns.
func (text *TextWriter) Bytes() []byte { return text.writer.buffer }

// Release drops the buffers when they hold more than limit bytes, so a
// pooled writer does not keep one huge text alive.
func (text *TextWriter) Release(limit int) {
	if cap(text.writer.buffer) > limit {
		text.writer.buffer = nil
	}
}

// Level is how many documents, arrays and scopes are open.
func (text *TextWriter) Level() int { return text.writer.level }

// Key writes a document element's name.
func (text *TextWriter) Key(name string) { text.writer.key(name) }

// OpenDocument opens a document, after its key or at the top level.
func (text *TextWriter) OpenDocument() error {
	if err := text.writer.openDocument(); err != nil {
		return err
	}
	text.kinds = append(text.kinds, documentLevel)
	return nil
}

// OpenArray opens an array after its key.
func (text *TextWriter) OpenArray() error {
	if err := text.writer.openArray(); err != nil {
		return err
	}
	text.kinds = append(text.kinds, arrayLevel)
	return nil
}

// OpenScope writes a code with scope's code and opens its scope.
func (text *TextWriter) OpenScope(code string) error {
	if err := text.writer.openScope(code); err != nil {
		return err
	}
	text.kinds = append(text.kinds, scopeLevel)
	return nil
}

// Close closes the innermost open document, array or scope.
func (text *TextWriter) Close() {
	closing := text.kinds[len(text.kinds)-1]
	text.kinds = text.kinds[:len(text.kinds)-1]
	switch closing {
	case arrayLevel:
		text.writer.closeArray()
	case scopeLevel:
		text.writer.closeScope()
	default:
		text.writer.closeDocument(len(text.kinds) == 0)
	}
}

// CopyScopeElements copies the elements of a scope encoded to BSON (the
// vendor encodes a scope to bytes, then copies them, with the root checks
// of copyBytesToValueWriter) into the scope OpenScope opened.
func (text *TextWriter) CopyScopeElements(scopeDocument []byte) error {
	return copyElementsFromBytes(&text.writer, scopeDocument, false)
}

// CopyDocument writes a document from bytes the caller supplied, as the
// vendor's Copier.CopyDocumentFromBytes does: opened, with the root checks
// of the bytes, each value read from its own bytes, then closed.
func (text *TextWriter) CopyDocument(documentBytes []byte) error {
	if err := text.OpenDocument(); err != nil {
		return err
	}
	if err := copyElementsFromBytes(&text.writer, documentBytes, false); err != nil {
		return err
	}
	text.Close()
	return nil
}

// CopyArray writes an array from bsoncore.Array bytes, as
// Copier.CopyArrayFromBytes does: the keys are read and dropped.
func (text *TextWriter) CopyArray(arrayBytes []byte) error {
	if err := text.OpenArray(); err != nil {
		return err
	}
	if err := copyElementsFromBytes(&text.writer, arrayBytes, true); err != nil {
		return err
	}
	text.Close()
	return nil
}

// CopyValue writes one value of valueType from valueBytes, as the vendor's
// Copier.CopyValueFromBytes does onto its Extended JSON writer, which is
// not a BytesWriter: a fresh value reader reads the bytes, its offsets
// count from their start, and the value is written as it is read. At the
// top level only a document can be written: any other value is read first,
// as far as the vendor reads it before it writes, and then refused with the
// writer's transition error.
func (text *TextWriter) CopyValue(valueType bsontype.Type, valueBytes []byte, topLevel bool) error {
	reader := valueReader{source: valueBytes}
	if !topLevel {
		return reader.copyValue(&text.writer, valueType)
	}
	switch valueType {
	case bsontype.EmbeddedDocument:
		text.kinds = append(text.kinds, documentLevel)
		err := reader.copyDocument(&text.writer, true)
		text.kinds = text.kinds[:len(text.kinds)-1]
		return err
	case bsontype.Array:
		if _, err := reader.containerEnd(); err != nil {
			return err
		}
		return TopLevelWriteError("WriteArray", "Array")
	case bsontype.CodeWithScope:
		if _, _, err := reader.codeWithScopeHeader(); err != nil {
			return err
		}
		return TopLevelWriteError("WriteCodeWithScope", "CodeWithScope")
	}
	// The value is read into a writer of its own, which is dropped: only
	// the read's error, or the refusal, is the vendor's outcome.
	discard := textWriter{canonical: text.writer.canonical}
	if err := reader.copyScalar(&discard, valueType); err != nil {
		return err
	}
	return TopLevelWriteError(scalarWriteMethods[valueType], "")
}

// scalarWriteMethods names the vendor's writer method for each scalar type,
// which its top-level refusal names.
var scalarWriteMethods = map[bsontype.Type]string{
	bsontype.Double: "WriteDouble", bsontype.String: "WriteString", bsontype.Binary: "WriteBinaryWithSubtype",
	bsontype.Undefined: "WriteUndefined", bsontype.ObjectID: "WriteObjectID", bsontype.Boolean: "WriteBoolean",
	bsontype.DateTime: "WriteDateTime", bsontype.Null: "WriteNull", bsontype.Regex: "WriteRegex",
	bsontype.DBPointer: "WriteDBPointer", bsontype.JavaScript: "WriteJavascript", bsontype.Symbol: "WriteSymbol",
	bsontype.Int32: "WriteInt32", bsontype.Timestamp: "WriteTimestamp", bsontype.Int64: "WriteInt64",
	bsontype.Decimal128: "WriteDecimal128", bsontype.MinKey: "WriteMinKey", bsontype.MaxKey: "WriteMaxKey",
}

// topLevelWriteError is the vendor's TransitionError for a value written at
// the top level, where only a document can go.
type topLevelWriteError struct {
	method      string
	destination string
}

// TopLevelWriteError is that refusal for the writer method method, which
// would have opened a destination ("" for a scalar).
func TopLevelWriteError(method, destination string) error {
	return topLevelWriteError{method: method, destination: destination}
}

func (refusal topLevelWriteError) Error() string {
	text := refusal.method + " can only write"
	if refusal.destination != "" {
		text += " a " + refusal.destination
	}
	return text + " while positioned on a Element or Value but is positioned on a TopLevel"
}

// The scalar writes, each one value followed by its comma.

func (text *TextWriter) WriteDouble(number float64)  { text.writer.writeDouble(number) }
func (text *TextWriter) WriteString(value string)    { text.writer.writeString(value) }
func (text *TextWriter) WriteJavaScript(code string) { text.writer.writeJavaScript(code) }
func (text *TextWriter) WriteSymbol(symbol string)   { text.writer.writeSymbol(symbol) }
func (text *TextWriter) WriteBinary(subtype byte, payload []byte) {
	text.writer.writeBinary(subtype, payload)
}
func (text *TextWriter) WriteObjectID(objectID primitive.ObjectID) {
	text.writer.writeObjectID(objectID)
}
func (text *TextWriter) WriteBoolean(flag bool)             { text.writer.writeBoolean(flag) }
func (text *TextWriter) WriteDateTime(milliseconds int64)   { text.writer.writeDateTime(milliseconds) }
func (text *TextWriter) WriteRegex(pattern, options string) { text.writer.writeRegex(pattern, options) }
func (text *TextWriter) WriteDBPointer(namespace string, objectID primitive.ObjectID) {
	text.writer.writeDBPointer(namespace, objectID)
}
func (text *TextWriter) WriteInt32(number int32) { text.writer.writeInt32(number) }
func (text *TextWriter) WriteInt64(number int64) { text.writer.writeInt64(number) }
func (text *TextWriter) WriteTimestamp(seconds, increment uint32) {
	text.writer.writeTimestamp(seconds, increment)
}
func (text *TextWriter) WriteDecimal128(decimal primitive.Decimal128) {
	text.writer.writeDecimal128(decimal)
}
func (text *TextWriter) WriteUndefined() { text.writer.writeUndefined() }
func (text *TextWriter) WriteNull()      { text.writer.writeNull() }
func (text *TextWriter) WriteMinKey()    { text.writer.writeMinKey() }
func (text *TextWriter) WriteMaxKey()    { text.writer.writeMaxKey() }
