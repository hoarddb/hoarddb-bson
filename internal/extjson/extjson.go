// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/bsonrw/copier.go (CopyValue read-then-write order), value_writer.go (zero-byte and size refusals and their text), mode.go (the root-array transition text) and extjson_writer.go (regex options sorted by rune): rewritten as one byte writer with heap frames instead of recursion; in step 5's Go-value half, the same writer copies one value (Copier.AppendValueBytes) or one document (CopyDocument) for the Go-value reader.

// Package extjson reads canonical and relaxed Extended JSON, with the
// vendor's acceptance, error text and quirks (spec 5.4, 9.1.5). Reader
// walks a text value by value; AppendDocument writes what it reads as BSON
// bytes, equal to the vendor's UnmarshalExtJSON into a bson.Raw.
package extjson

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/internal/wire"
)

// The byte writer's own refusals. The vendor's Go-value targets accept
// both inputs; only its bson.Raw target, which writes bytes, refuses them.
var (
	errKeyHoldsZero   = errors.New("BSON element key cannot contain null bytes")
	errRegexHoldsZero = errors.New("BSON regex values cannot contain null bytes")
)

// rootArrayError is the vendor's text for a root that is an array.
const rootArrayError = "ReadDocument can only read a Document while positioned on a TopLevel, Element, or Value but is positioned on a Array"

type frameKind byte

const (
	frameDocument frameKind = iota
	frameArray
	frameScope
)

// writeFrame is one open document, array or code-with-scope scope.
type writeFrame struct {
	kind         frameKind
	lengthOffset int
	// codeOffset is a scope's code-with-scope length, written when the
	// scope closes.
	codeOffset    int
	nextArraySlot int
}

// AppendDocument reads text as one Extended JSON document and appends its
// BSON bytes to dst. It reads only up to the document's closing brace:
// anything after it is never read, as in the vendor ("stops after the
// first JSON value"). A repeated key is written twice.
//
// On an error it returns dst with the bytes written before the error, as
// the vendor leaves them in a bson.Raw target. When the first value cannot
// be read at all, or the root is not a document, nothing is appended.
//
// It nests without recursion: the vendor's limit of 200 objects is the
// parser's, and arrays, which it does not count, cost one frame each.
func AppendDocument(dst, text []byte, canonical bool) ([]byte, error) {
	reader, rootType, err := NewReader(text, canonical)
	if err != nil {
		return dst, err
	}
	switch rootType {
	case bsontype.EmbeddedDocument:
	case bsontype.Array:
		return dst, errors.New(rootArrayError)
	default:
		return dst, fmt.Errorf("positioned on %s, but attempted to read embedded document", rootType)
	}
	return reader.appendDocument(dst)
}

// appendDocument appends the BSON of the document whose opening the reader
// has read: the vendor's CopyDocument into a value writer at its top level,
// which writes no header.
func (reader *Reader) appendDocument(dst []byte) ([]byte, error) {
	documentWriter := byteWriter{buffer: dst}
	documentWriter.frames = append(documentWriter.frames, writeFrame{kind: frameDocument, lengthOffset: documentWriter.reserveLength()})
	return documentWriter.copyAll(reader)
}

// appendValue appends the BSON of the value of valueType the reader is on,
// as an element with an empty key: the vendor's Copier.AppendValueBytes,
// which copies the value into a value writer positioned on an element.
func (reader *Reader) appendValue(dst []byte, valueType bsontype.Type) ([]byte, error) {
	documentWriter := byteWriter{buffer: dst}
	if err := documentWriter.copyValue(reader, valueType, "", false); err != nil {
		return documentWriter.buffer, err
	}
	return documentWriter.copyAll(reader)
}

// copyAll copies until every open container is closed.
func (documentWriter *byteWriter) copyAll(reader *Reader) ([]byte, error) {
	for len(documentWriter.frames) > 0 {
		if err := documentWriter.copyNext(reader); err != nil {
			return documentWriter.buffer, err
		}
	}
	return documentWriter.buffer, nil
}

// byteWriter writes the BSON of what a Reader reads, in the vendor's order:
// a scalar's header after its value is read, a container's header before
// its contents, so the bytes left by an error are the vendor's too.
type byteWriter struct {
	buffer []byte
	frames []writeFrame
}

// copyNext copies the next element of the innermost open container, or
// closes it at its end.
func (documentWriter *byteWriter) copyNext(reader *Reader) error {
	innermost := &documentWriter.frames[len(documentWriter.frames)-1]
	var key string
	var valueType bsontype.Type
	var err error
	if innermost.kind == frameArray {
		valueType, err = reader.ReadArrayValue()
		if errors.Is(err, ErrEndOfArray) {
			return documentWriter.closeFrame()
		}
		key = strconv.Itoa(innermost.nextArraySlot)
		innermost.nextArraySlot++
	} else {
		key, valueType, err = reader.ReadElement(innermost.kind == frameScope)
		if errors.Is(err, ErrEndOfDocument) {
			return documentWriter.closeFrame()
		}
	}
	if err != nil {
		return err
	}
	// Array positions are never checked for a zero byte; they hold none.
	return documentWriter.copyValue(reader, valueType, key, innermost.kind != frameArray)
}

// copyValue copies one value of valueType, read by reader, as the element
// key: a container is opened, a scalar written whole.
func (documentWriter *byteWriter) copyValue(reader *Reader, valueType bsontype.Type, key string, checkKey bool) error {
	switch valueType {
	case bsontype.EmbeddedDocument:
		return documentWriter.openFrame(frameDocument, valueType, key, checkKey)
	case bsontype.Array:
		return documentWriter.openFrame(frameArray, valueType, key, checkKey)
	case bsontype.CodeWithScope:
		return documentWriter.openScope(reader, key, checkKey)
	}
	read, err := reader.ReadScalar(valueType)
	if err != nil {
		return err
	}
	if valueType == bsontype.Regex && (strings.IndexByte(read.Text, 0) >= 0 || strings.IndexByte(read.Options, 0) >= 0) {
		return errRegexHoldsZero
	}
	if err := documentWriter.appendHeader(valueType, key, checkKey); err != nil {
		return err
	}
	documentWriter.appendScalar(read)
	return nil
}

func (documentWriter *byteWriter) openFrame(kind frameKind, valueType bsontype.Type, key string, checkKey bool) error {
	if err := documentWriter.appendHeader(valueType, key, checkKey); err != nil {
		return err
	}
	documentWriter.frames = append(documentWriter.frames, writeFrame{kind: kind, lengthOffset: documentWriter.reserveLength()})
	return nil
}

// openScope reads a code with scope's code, then writes its header, its
// code and the start of its scope.
func (documentWriter *byteWriter) openScope(reader *Reader, key string, checkKey bool) error {
	code, err := reader.ReadCodeWithScope()
	if err != nil {
		return err
	}
	if err := documentWriter.appendHeader(bsontype.CodeWithScope, key, checkKey); err != nil {
		return err
	}
	codeOffset := documentWriter.reserveLength()
	documentWriter.buffer = wire.AppendString(documentWriter.buffer, code)
	documentWriter.frames = append(documentWriter.frames, writeFrame{
		kind: frameScope, lengthOffset: documentWriter.reserveLength(), codeOffset: codeOffset,
	})
	return nil
}

func (documentWriter *byteWriter) appendHeader(valueType bsontype.Type, key string, checkKey bool) error {
	if checkKey && strings.IndexByte(key, 0) >= 0 {
		return errKeyHoldsZero
	}
	documentWriter.buffer = append(documentWriter.buffer, byte(valueType))
	documentWriter.buffer = append(documentWriter.buffer, key...)
	documentWriter.buffer = append(documentWriter.buffer, 0)
	return nil
}

func (documentWriter *byteWriter) reserveLength() int {
	lengthOffset := len(documentWriter.buffer)
	documentWriter.buffer = append(documentWriter.buffer, 0, 0, 0, 0)
	return lengthOffset
}

// closeFrame writes the innermost container's terminator and length, and a
// scope's code-with-scope length after it.
func (documentWriter *byteWriter) closeFrame() error {
	closing := documentWriter.frames[len(documentWriter.frames)-1]
	documentWriter.frames = documentWriter.frames[:len(documentWriter.frames)-1]
	documentWriter.buffer = append(documentWriter.buffer, 0)
	// The vendor checks the whole buffer, not the container, against the
	// int32 a length holds.
	if len(documentWriter.buffer) > math.MaxInt32 {
		return fmt.Errorf("document size (%d) is larger than the max int32", len(documentWriter.buffer))
	}
	wire.PutLength(documentWriter.buffer, closing.lengthOffset, int32(len(documentWriter.buffer)-closing.lengthOffset))
	if closing.kind == frameScope {
		wire.PutLength(documentWriter.buffer, closing.codeOffset, int32(len(documentWriter.buffer)-closing.codeOffset))
	}
	return nil
}

func (documentWriter *byteWriter) appendScalar(read Scalar) {
	buffer := documentWriter.buffer
	switch read.Type {
	case bsontype.Double:
		buffer = wire.AppendDouble(buffer, read.Double)
	case bsontype.String, bsontype.JavaScript, bsontype.Symbol:
		buffer = wire.AppendString(buffer, read.Text)
	case bsontype.Binary:
		buffer = wire.AppendBinary(buffer, read.Subtype, read.Binary)
	case bsontype.ObjectID:
		buffer = append(buffer, read.ObjectID[:]...)
	case bsontype.Boolean:
		if read.Boolean {
			buffer = append(buffer, 1)
		} else {
			buffer = append(buffer, 0)
		}
	case bsontype.DateTime, bsontype.Int64:
		buffer = wire.AppendInt64(buffer, read.Int64)
	case bsontype.Regex:
		buffer = append(buffer, read.Text...)
		buffer = append(buffer, 0)
		buffer = append(buffer, sortedOptions(read.Options)...)
		buffer = append(buffer, 0)
	case bsontype.DBPointer:
		buffer = wire.AppendString(buffer, read.Text)
		buffer = append(buffer, read.ObjectID[:]...)
	case bsontype.Int32:
		buffer = wire.AppendInt32(buffer, read.Int32)
	case bsontype.Timestamp:
		buffer = binary.LittleEndian.AppendUint32(buffer, read.TimestampOrdinal)
		buffer = binary.LittleEndian.AppendUint32(buffer, read.TimestampSeconds)
	case bsontype.Decimal128:
		high, low := read.Decimal128.GetBytes()
		buffer = binary.LittleEndian.AppendUint64(buffer, low)
		buffer = binary.LittleEndian.AppendUint64(buffer, high)
	}
	// Undefined, Null, MinKey and MaxKey have no value bytes.
	documentWriter.buffer = buffer
}

// sortedOptions sorts a regular expression's options by code point, as the
// vendor's byte writer does. Converting to runes first turns each invalid
// UTF-8 byte into U+FFFD, which the vendor does too.
func sortedOptions(options string) string {
	optionRunes := []rune(options)
	slices.Sort(optionRunes)
	return string(optionRunes)
}
