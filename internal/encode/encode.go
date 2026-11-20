// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: derived from bson/marshal.go and bson/encoder.go (Marshal's entry: the top-level MarshalBSON check, the pooled buffer copied out) and bson/bsonrw/value_writer.go with bson/bsonrw/mode.go (the element header, the zero-byte key refusal, the top-level transition errors and their text, the document size refusal); rewritten as one append-only writer whose position is passed down instead of a mode stack; MarshalValue's entry from bson/marshal.go (MarshalValueAppendWithContext: a value written at an element with an empty key) and a level past 200 refused where the vendor recurses without bound (build step 4f); in step 5's Go-value half, a text mode in which the same walk writes Extended JSON through extjson.TextWriter, with the vendor's Extended JSON writer's rules (no zero-byte or size refusal, the top-level refusals unchanged).

// Package encode is the reflection encoder: Go values to BSON bytes, with
// the bytes, the element order and the error text of the vendor's
// bson.Marshal (v1.17.10), except that a Go map's entries are written in
// ascending byte order of their keys (spec 6.1, O11). Every per-type
// decision comes from the plans of internal/plan; what is left here is
// what depends on the value: nil checks, dynamic types, addressability and
// the hooks' results.
//
// Package bson's Marshal and MarshalValue (step 4f) are thin entries over
// Encoder.Document and Encoder.Value.
package encode

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/internal/extjson"
	"github.com/hoarddb/hoarddb-bson/internal/nesting"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
	"github.com/hoarddb/hoarddb-bson/internal/wire"
)

// Encoder encodes Go values with the plans of one cache.
type Encoder struct {
	cache *plan.Cache
}

// New returns an Encoder that takes its plans from cache.
func New(cache *plan.Cache) *Encoder {
	return &Encoder{cache: cache}
}

// Cache returns the plan cache the Encoder takes its plans from, so that
// package bson can test that its encoder and decoder share one.
func (encoder *Encoder) Cache() *plan.Cache {
	return encoder.cache
}

// maxPooledBuffer is the largest buffer returned to the pool, as the
// vendor's Marshal keeps none of 16 MiB or more: one huge document must not
// pin its buffer for the life of the process.
const maxPooledBuffer = 16 * 1024 * 1024

// writerPool holds encode buffers between calls (spec 5.5). A buffer is
// never handed to a caller: Document copies its bytes out.
var writerPool = sync.Pool{New: func() any { return new(writer) }}

// Document encodes value as one BSON document, as the vendor's
// bson.Marshal does. A value whose type has MarshalBSON is written from
// that method's bytes, which must hold a document; anything else goes
// through its plan, and a value that is not a document at the top level is
// refused with the vendor's text. The result is a new slice the caller
// owns.
func (encoder *Encoder) Document(value any) ([]byte, error) {
	encoding := encoder.acquire()
	defer encoding.release()

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
		return encoding.output(), nil
	}
	if value == nil {
		return nil, noEncoderError{}
	}
	typePlan, err := encoder.cache.Lookup(reflect.TypeOf(value))
	if err != nil {
		return nil, err
	}
	if hint := int(typePlan.SizeHint.Load()); cap(encoding.buf) < hint {
		encoding.buf = make([]byte, 0, hint)
	}
	if err := encoding.encodeValue(typePlan, reflect.ValueOf(value), topLevel(), false); err != nil {
		return nil, err
	}
	encoded := encoding.output()
	typePlan.SizeHint.Store(int32(len(encoded)))
	return encoded, nil
}

// Value encodes value as one BSON value, as the vendor's bson.MarshalValue
// does: written as the value of an element, so a nil map, slice or pointer
// is a null and a scalar is allowed, and returned as its type and its
// bytes. A value whose type has MarshalBSON is that method's document. The
// bytes are a new slice the caller owns.
func (encoder *Encoder) Value(value any) (bsontype.Type, []byte, error) {
	encoding := encoder.acquire()
	defer encoding.release()

	// The vendor writes at an element with an empty key, so a value is its
	// type byte, the key's terminator, then the value's bytes.
	at := element("")
	if marshaler, ok := value.(plan.Marshaler); ok {
		if err := refuseNilValueReceiver(reflect.ValueOf(value), marshalerType); err != nil {
			return 0, nil, err
		}
		documentBytes, err := marshaler.MarshalBSON()
		if err != nil {
			return 0, nil, err
		}
		if err := encoding.copyDocument(at, documentBytes); err != nil {
			return 0, nil, err
		}
		return encoding.valueOutput()
	}
	if value == nil {
		return 0, nil, noEncoderError{}
	}
	typePlan, err := encoder.cache.Lookup(reflect.TypeOf(value))
	if err != nil {
		return 0, nil, err
	}
	if err := encoding.encodeValue(typePlan, reflect.ValueOf(value), at, false); err != nil {
		return 0, nil, err
	}
	return encoding.valueOutput()
}

// acquire takes a writer from the pool, emptied.
func (encoder *Encoder) acquire() *writer {
	encoding := writerPool.Get().(*writer)
	encoding.cache = encoder.cache
	encoding.buf = encoding.buf[:0]
	encoding.levels = 0
	encoding.textMode = false
	return encoding
}

// writer appends one encoding. It is pooled, so it holds nothing between
// calls that a caller could see.
type writer struct {
	cache *plan.Cache
	buf   []byte
	// levels is how many documents, arrays and scopes are open, to hold
	// the ceiling (spec 5.2).
	levels int
	// entries is scratch space for map entries being sorted. Nested maps
	// stack their entries after their parent's, so one slice serves any
	// depth.
	entries []mapEntry
	// textMode sends every write to text, the Extended JSON writer
	// (bson.MarshalExtJSON, text.go), instead of buf. The walk over the Go
	// value is the same; what each write puts down differs.
	textMode bool
	text     extjson.TextWriter
}

// output copies the encoding out of the pooled buffer. Returning the buffer
// itself would let the next encode overwrite a document the caller holds.
func (encoding *writer) output() []byte {
	return append([]byte(nil), encoding.buf...)
}

// valueOutput is output for a value written at element(""): its type byte,
// and a copy of what follows the empty key's terminator. Every successful
// write at an element slot starts with that two-byte header.
func (encoding *writer) valueOutput() (bsontype.Type, []byte, error) {
	return bsontype.Type(encoding.buf[0]), append([]byte(nil), encoding.buf[2:]...), nil
}

func (encoding *writer) release() {
	if cap(encoding.buf) > maxPooledBuffer {
		encoding.buf = nil
	}
	encoding.text.Release(maxPooledBuffer)
	// The scratch entries hold reflect.Values of the caller's data; clearing
	// them keeps the pool from keeping that data alive.
	clear(encoding.entries[:cap(encoding.entries)])
	encoding.entries = encoding.entries[:0]
	encoding.cache = nil
	writerPool.Put(encoding)
}

// slotKind is where the next value goes: the vendor's value writer mode.
type slotKind uint8

const (
	// topLevelSlot: no element is open, so only a document can be written.
	topLevelSlot slotKind = iota
	// elementSlot: a document element named by slot.key.
	elementSlot
	// arraySlot: an array element at slot.index.
	arraySlot
)

// slot is the position the next value is written at.
type slot struct {
	kind  slotKind
	index int
	key   string
}

func topLevel() slot              { return slot{kind: topLevelSlot} }
func element(key string) slot     { return slot{kind: elementSlot, key: key} }
func arrayElement(index int) slot { return slot{kind: arraySlot, index: index} }
func (at slot) isTopLevel() bool  { return at.kind == topLevelSlot }

// errNullByteKey is the vendor's refusal of a key that cannot be a cstring.
var errNullByteKey = errors.New("BSON element key cannot contain null bytes")

// transitionError is the vendor's bsonrw.TransitionError for a value
// written where only a document can go: the top level. Its text is the
// vendor's, which names the value writer method that refused.
type transitionError struct {
	method      string
	destination string
}

func (refusal transitionError) Error() string {
	text := refusal.method + " can only write"
	if refusal.destination != "" {
		text += " a " + refusal.destination
	}
	return text + " while positioned on a Element or Value but is positioned on a TopLevel"
}

// noEncoderError is the vendor's bsoncodec.ErrNoEncoder text.
type noEncoderError struct {
	goType reflect.Type
}

func (refusal noEncoderError) Error() string {
	if refusal.goType == nil {
		return "no encoder found for <nil>"
	}
	return "no encoder found for " + refusal.goType.String()
}

// documentSizeError is the vendor's refusal of an encoding past MaxInt32.
type documentSizeError struct {
	size int
}

func (refusal documentSizeError) Error() string {
	return "document size (" + strconv.Itoa(refusal.size) + ") is larger than the max int32"
}

// header writes the type byte and the name of the element at slot. method
// and destination name the vendor's writer call, for the text of the
// top-level refusal.
func (encoding *writer) header(at slot, elementType bsontype.Type, method, destination string) error {
	if encoding.textMode {
		return encoding.textHeader(at, method, destination)
	}
	switch at.kind {
	case elementSlot:
		if strings.IndexByte(at.key, 0) >= 0 {
			return errNullByteKey
		}
		encoding.buf = append(encoding.buf, byte(elementType))
		encoding.buf = append(encoding.buf, at.key...)
		encoding.buf = append(encoding.buf, 0)
	case arraySlot:
		encoding.buf = append(encoding.buf, byte(elementType))
		encoding.buf = strconv.AppendInt(encoding.buf, int64(at.index), 10)
		encoding.buf = append(encoding.buf, 0)
	default:
		return transitionError{method: method, destination: destination}
	}
	return nil
}

// scalarHeader is header for a value that is not a container.
func (encoding *writer) scalarHeader(at slot, elementType bsontype.Type, method string) error {
	return encoding.header(at, elementType, method, "")
}

// startDocument opens a document at slot and returns where its length goes.
// The top level is the one place a document needs no header.
func (encoding *writer) startDocument(at slot) (int, error) {
	if !at.isTopLevel() {
		if err := encoding.header(at, bsontype.EmbeddedDocument, "WriteDocument", "Document"); err != nil {
			return 0, err
		}
	}
	if encoding.textMode {
		return 0, encoding.text.OpenDocument()
	}
	return encoding.openLevel()
}

// startArray opens an array at slot, which the top level refuses.
func (encoding *writer) startArray(at slot) (int, error) {
	if err := encoding.header(at, bsontype.Array, "WriteArray", "Array"); err != nil {
		return 0, err
	}
	if encoding.textMode {
		return 0, encoding.text.OpenArray()
	}
	return encoding.openLevel()
}

// openLevel counts a document or array whose header is written, refusing
// one past the ceiling, and reserves its length. The vendor has no ceiling
// and recurses until the goroutine stack overflows on a value that
// contains itself; here the encoder stops at level 201 (spec 5.2, ruling
// O17). A header error comes first, as it would for the vendor.
func (encoding *writer) openLevel() (int, error) {
	encoding.levels++
	if encoding.levels > nesting.Ceiling {
		return 0, nesting.ErrPastCeiling
	}
	return encoding.reserveLength(), nil
}

func (encoding *writer) reserveLength() int {
	start := len(encoding.buf)
	encoding.buf = append(encoding.buf, 0, 0, 0, 0)
	return start
}

// end closes the document or array whose length is at start.
func (encoding *writer) end(start int) error {
	if encoding.textMode {
		encoding.text.Close()
		return nil
	}
	encoding.levels--
	encoding.buf = append(encoding.buf, 0)
	return encoding.putLength(start)
}

// putLength writes the length of everything from start on at start. The
// vendor measures the whole buffer against MaxInt32, not the one document.
func (encoding *writer) putLength(start int) error {
	if len(encoding.buf) > math.MaxInt32 {
		return documentSizeError{size: len(encoding.buf)}
	}
	wire.PutLength(encoding.buf, start, int32(len(encoding.buf)-start))
	return nil
}
