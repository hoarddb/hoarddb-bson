// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

import (
	"reflect"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/internal/decode"
	"github.com/hoarddb/hoarddb-bson/internal/encode"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
)

// planCache is the library's one plan cache (spec 5.3 and 5.5): every
// encode and every decode in the process takes its per-type plans from it.
// The encoder and the decoder share it, so a type is compiled once for
// both directions.
var planCache = plan.NewCache(plan.BSONTypes{
	Raw:      reflect.TypeOf(Raw(nil)),
	RawValue: reflect.TypeOf(RawValue{}),
})

var (
	libraryEncoder = encode.New(planCache)
	libraryDecoder = decode.New(planCache)
)

// Marshal returns the BSON document of val. val must be a document: a
// struct, a map with string keys, a D, a Raw, or a value whose MarshalBSON
// returns one; anything else is refused with an error, as is a nil value
// other than a nil map, which encodes as the empty document. A map's
// entries are written in ascending byte order of their keys. A value
// nested deeper than MaxNestingCeiling, or one that contains itself, is
// refused with an error matching ErrNestingTooDeep.
func Marshal(val interface{}) ([]byte, error) {
	if !oracleTapCompiled {
		return libraryEncoder.Document(val)
	}
	after := oracleBefore("Marshal", val, nil, 0)
	documentBytes, err := libraryEncoder.Document(val)
	if tapErr := after(val, documentBytes, 0, err); tapErr != nil {
		return nil, tapErr
	}
	return documentBytes, err
}

// MarshalValue returns the BSON type and the bytes of val as one value, as
// it would be written in a document's element: a scalar is allowed, and a
// nil map, slice or pointer is a null. The ceiling is Marshal's, with
// val's own document or array as level 1.
func MarshalValue(val interface{}) (bsontype.Type, []byte, error) {
	if !oracleTapCompiled {
		return libraryEncoder.Value(val)
	}
	after := oracleBefore("MarshalValue", val, nil, 0)
	valueType, valueBytes, err := libraryEncoder.Value(val)
	if tapErr := after(val, valueBytes, valueType, err); tapErr != nil {
		return 0, nil, tapErr
	}
	return valueType, valueBytes, err
}

// Unmarshal decodes the BSON document in data into val, which must be a
// non-nil pointer or a non-nil map. It does not validate data first, and
// refuses what the target's decoders refuse: a key the target does not
// declare is skipped unread. A document that opens a level past
// MaxNestingCeiling where it is decoded is refused with an error matching
// ErrNestingTooDeep. Bytes from outside this process's own store are
// checked with Validate before anything else reads them.
func Unmarshal(data []byte, val interface{}) error {
	if !oracleTapCompiled {
		return libraryDecoder.Unmarshal(data, val)
	}
	after := oracleBefore("Unmarshal", val, data, 0)
	err := libraryDecoder.Unmarshal(data, val)
	if tapErr := after(val, data, 0, err); tapErr != nil {
		return tapErr
	}
	return err
}

// Unmarshal decodes the value into val, which must be a non-nil pointer,
// with Unmarshal's rules. The value's own document or array is level 1 of
// the ceiling.
func (rv RawValue) Unmarshal(val interface{}) error {
	if !oracleTapCompiled {
		return libraryDecoder.UnmarshalValue(rv.Type, rv.Value, val)
	}
	after := oracleBefore("RawValue.Unmarshal", val, rv.Value, rv.Type)
	err := libraryDecoder.UnmarshalValue(rv.Type, rv.Value, val)
	if tapErr := after(val, rv.Value, rv.Type, err); tapErr != nil {
		return tapErr
	}
	return err
}

// Decode decodes the document into target, as Unmarshal decodes its bytes.
// It does not validate the bytes again: Validate checked them, or they are
// this process's own stored bytes (TrustStored), and the decode refuses
// only what Unmarshal refuses. The zero Document is refused with an error
// matching ErrZeroDocument, and target is left as it was.
func (document Document) Decode(target any) error {
	if document.b == nil {
		return ErrZeroDocument
	}
	if !oracleTapCompiled {
		return libraryDecoder.Unmarshal(document.b, target)
	}
	after := oracleBefore("Document.Decode", target, document.b, 0)
	err := libraryDecoder.Unmarshal(document.b, target)
	if tapErr := after(target, document.b, 0, err); tapErr != nil {
		return tapErr
	}
	return err
}

// oracleAfter receives an entry point's results in a build with the
// bsonoracle tag (tap.go). Its error, when not nil, is returned by the
// entry point in place of its own result. In a build without the tag,
// oracleTapCompiled is the constant false, so the compiler drops every
// branch that calls one (tap_off.go).
type oracleAfter func(value any, valueBytes []byte, valueType bsontype.Type, err error) error
