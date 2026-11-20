// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package decode

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/hoarddb/hoarddb-bson/internal/extjson"
	"github.com/hoarddb/hoarddb-bson/internal/plan"
)

// UnmarshalText decodes the Extended JSON text, canonical or relaxed, into
// target, as the vendor's bson.UnmarshalExtJSON does: the decoders of
// Unmarshal run over an Extended JSON value reader instead of BSON bytes,
// so a map, a bson.D or a struct receives what the text says (a zero byte
// in a key, regex options as typed) and each target's error text is the
// vendor's. The text is read first, so text whose first value cannot be
// read is refused before the target is looked at. Only what the root holds
// is read: anything after it is not.
func (decoder *Decoder) UnmarshalText(text []byte, canonical bool, target any) error {
	textReader, err := extjson.NewValueReader(text, canonical)
	if err != nil {
		return err
	}
	valueReader := reader{text: textReader}
	if hook, isHook := target.(plan.Unmarshaler); isHook {
		documentBytes, err := valueReader.ReadDocumentBytes(nil)
		if err != nil {
			return err
		}
		return hook.UnmarshalBSON(documentBytes)
	}
	targetValue := reflect.ValueOf(target)
	switch targetValue.Kind() {
	case reflect.Pointer:
		if targetValue.IsNil() {
			return errDecodeToNil
		}
		targetValue = targetValue.Elem()
	case reflect.Map:
		if targetValue.IsNil() {
			return errDecodeToNil
		}
	default:
		return fmt.Errorf("argument to Decode must be a pointer or a map, but got %v", targetValue)
	}
	return decoder.decodeRoot(&valueReader, targetValue)
}

// ReadDocumentBytes appends the bytes of the document the reader is on to
// dst: for BSON, the value's own bytes, whatever its type (the vendor's
// AppendDocumentBytes through its BytesReader); for text, the document
// copied into BSON (extjson.ValueReader.AppendDocumentBytes). It is how
// bson.Raw, bsoncore.Document and the top-level hook read their bytes.
func (valueReader *reader) ReadDocumentBytes(dst []byte) ([]byte, error) {
	if valueReader.text != nil {
		return valueReader.text.AppendDocumentBytes(dst)
	}
	_, documentBytes, err := valueReader.ReadValueBytes(dst)
	return documentBytes, err
}

// ReadArrayBytes is ReadDocumentBytes for bsoncore.Array.
func (valueReader *reader) ReadArrayBytes(dst []byte) ([]byte, error) {
	if valueReader.text != nil {
		return valueReader.text.AppendArrayBytes(dst)
	}
	_, arrayBytes, err := valueReader.ReadValueBytes(dst)
	return arrayBytes, err
}

// textEnd turns the text reader's end of a document or an array into the
// decoder's own sentinel, which its loops test for.
func textEnd(err error) error {
	switch {
	case errors.Is(err, extjson.ErrEndOfDocument):
		return errEndOfDocument
	case errors.Is(err, extjson.ErrEndOfArray):
		return errEndOfArray
	}
	return err
}
