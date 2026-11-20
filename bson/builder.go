// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/internal/wire"
)

// Builder builds one Document element by element, with no validation pass:
// every method writes well-framed bytes, so Document wraps the result as it
// is (spec section 5.2). Its bytes equal the vendor's bsoncore.BuildDocument
// of the same elements.
//
// The zero Builder is ready to use. The first problem (a key with a zero
// byte, a key inside an array, unbalanced levels) is kept, later calls do
// nothing, and Document returns it. Inside an array, pass "" as every key:
// the builder writes the positions "0", "1", ... itself.
//
// A Builder is not safe for concurrent use.
type Builder struct {
	buffer []byte
	levels []builderLevel
	err    error
}

// builderLevel is one open document or array. levels[0] is the root.
type builderLevel struct {
	lengthOffset int
	isArray      bool
	nextPosition int
}

// AppendDouble appends a double element.
func (builder *Builder) AppendDouble(key string, number float64) {
	if builder.appendHeader(bsontype.Double, key) {
		builder.buffer = wire.AppendDouble(builder.buffer, number)
	}
}

// AppendString appends a string element. The value is not checked: it may
// hold zero bytes and invalid UTF-8, as BSON allows.
func (builder *Builder) AppendString(key, text string) {
	if builder.appendHeader(bsontype.String, key) {
		builder.buffer = wire.AppendString(builder.buffer, text)
	}
}

// AppendDocument appends document as an embedded document, copying its
// bytes. A zero Document is refused with ErrMalformedDocument.
func (builder *Builder) AppendDocument(key string, document Document) {
	if builder.err == nil && len(document.b) < minDocumentLength {
		builder.fail(fmt.Errorf("appending %q: a zero Document has no bytes: %w", key, ErrMalformedDocument))
		return
	}
	if builder.appendHeader(bsontype.EmbeddedDocument, key) {
		builder.buffer = append(builder.buffer, document.b...)
	}
}

// AppendBinary appends a binary element. Subtype 0x02 is written with its
// deprecated inner length, as the vendor writes it.
func (builder *Builder) AppendBinary(key string, subtype byte, payload []byte) {
	if builder.appendHeader(bsontype.Binary, key) {
		builder.buffer = wire.AppendBinary(builder.buffer, subtype, payload)
	}
}

// AppendObjectID appends an ObjectID element.
func (builder *Builder) AppendObjectID(key string, id primitive.ObjectID) {
	if builder.appendHeader(bsontype.ObjectID, key) {
		builder.buffer = append(builder.buffer, id[:]...)
	}
}

// AppendBoolean appends a boolean element.
func (builder *Builder) AppendBoolean(key string, flag bool) {
	if !builder.appendHeader(bsontype.Boolean, key) {
		return
	}
	if flag {
		builder.buffer = append(builder.buffer, 1)
		return
	}
	builder.buffer = append(builder.buffer, 0)
}

// AppendDateTime appends a DateTime element: milliseconds since the Unix epoch.
func (builder *Builder) AppendDateTime(key string, milliseconds int64) {
	if builder.appendHeader(bsontype.DateTime, key) {
		builder.buffer = wire.AppendInt64(builder.buffer, milliseconds)
	}
}

// AppendNull appends a null element.
func (builder *Builder) AppendNull(key string) {
	builder.appendHeader(bsontype.Null, key)
}

// AppendInt32 appends an int32 element.
func (builder *Builder) AppendInt32(key string, number int32) {
	if builder.appendHeader(bsontype.Int32, key) {
		builder.buffer = wire.AppendInt32(builder.buffer, number)
	}
}

// AppendInt64 appends an int64 element.
func (builder *Builder) AppendInt64(key string, number int64) {
	if builder.appendHeader(bsontype.Int64, key) {
		builder.buffer = wire.AppendInt64(builder.buffer, number)
	}
}

// StartDocument opens an embedded document under key. Elements appended
// until the matching End go inside it.
func (builder *Builder) StartDocument(key string) {
	builder.openLevel(bsontype.EmbeddedDocument, key, false)
}

// StartArray opens an array under key. Elements appended until the
// matching End go inside it, each with "" as its key.
func (builder *Builder) StartArray(key string) {
	builder.openLevel(bsontype.Array, key, true)
}

// End closes the document or array the last StartDocument or StartArray
// opened. With none open, it is refused with ErrUnbalancedBuilder.
func (builder *Builder) End() {
	if builder.err != nil {
		return
	}
	if len(builder.levels) < 2 {
		builder.fail(fmt.Errorf("closing a level with no StartDocument or StartArray open: %w", ErrUnbalancedBuilder))
		return
	}
	builder.closeLevel()
}

// Document closes the root and returns the document, or the first error.
// Either way the Builder is reset and can build the next document. The
// returned bytes are never written again: the next document gets a new
// buffer.
func (builder *Builder) Document() (Document, error) {
	builder.startRoot()
	if builder.err == nil && len(builder.levels) != 1 {
		builder.fail(fmt.Errorf("finishing the document with %d StartDocument or StartArray levels still open: %w",
			len(builder.levels)-1, ErrUnbalancedBuilder))
	}
	if builder.err == nil {
		builder.closeLevel()
	}
	documentBytes, buildErr := builder.buffer, builder.err
	builder.buffer, builder.levels, builder.err = nil, builder.levels[:0], nil
	if buildErr != nil {
		return Document{}, buildErr
	}
	return Document{b: documentBytes}, nil
}

// startRoot opens the root level on first use, so the zero Builder works.
func (builder *Builder) startRoot() {
	if len(builder.levels) == 0 {
		builder.levels = append(builder.levels, builderLevel{lengthOffset: len(builder.buffer)})
		builder.buffer = append(builder.buffer, 0, 0, 0, 0)
	}
}

// appendHeader writes an element's type byte and key, or, inside an array,
// its position. It reports whether the value should follow.
func (builder *Builder) appendHeader(elementType bsontype.Type, key string) bool {
	if builder.err != nil {
		return false
	}
	builder.startRoot()
	level := &builder.levels[len(builder.levels)-1]
	if level.isArray && key != "" {
		builder.fail(fmt.Errorf("key %q inside an array, where the builder writes the position: pass \"\": %w",
			key, ErrInvalidKey))
		return false
	}
	if strings.IndexByte(key, 0) >= 0 {
		builder.fail(fmt.Errorf("key %q holds a zero byte, which ends a BSON key: %w", key, ErrInvalidKey))
		return false
	}
	builder.buffer = append(builder.buffer, byte(elementType))
	if level.isArray {
		builder.buffer = strconv.AppendInt(builder.buffer, int64(level.nextPosition), 10)
		level.nextPosition++
	} else {
		builder.buffer = append(builder.buffer, key...)
	}
	builder.buffer = append(builder.buffer, 0)
	return true
}

func (builder *Builder) openLevel(elementType bsontype.Type, key string, isArray bool) {
	if builder.appendHeader(elementType, key) {
		builder.levels = append(builder.levels, builderLevel{lengthOffset: len(builder.buffer), isArray: isArray})
		builder.buffer = append(builder.buffer, 0, 0, 0, 0)
	}
}

// closeLevel writes the innermost open level's terminator and length.
func (builder *Builder) closeLevel() {
	level := builder.levels[len(builder.levels)-1]
	builder.levels = builder.levels[:len(builder.levels)-1]
	builder.buffer = append(builder.buffer, 0)
	levelLength := int64(len(builder.buffer) - level.lengthOffset)
	if err := checkBuiltLength(levelLength); err != nil {
		builder.fail(err)
		return
	}
	wire.PutLength(builder.buffer, level.lengthOffset, int32(levelLength))
}

// checkBuiltLength refuses a document or array longer than the int32 a
// BSON length prefix holds.
func checkBuiltLength(length int64) error {
	if length > math.MaxInt32 {
		return fmt.Errorf("a built document or array of %d bytes is longer than a BSON length can hold: %w",
			length, ErrDocumentTooLarge)
	}
	return nil
}

func (builder *Builder) fail(err error) {
	if builder.err == nil {
		builder.err = err
	}
}
