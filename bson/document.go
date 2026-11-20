// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

import "encoding/binary"

// Document is one BSON document accepted by Validate or TrustStored. It is
// a slice header and nothing more: making one copies nothing, and it
// cannot be converted to or from []byte, so a Document always comes from a
// constructor (spec section 5.2, goal 4).
//
// The zero value, as in var document Document, is the one Document no
// constructor made. It holds no bytes and is not a validated document,
// not even the empty one. Validate and TrustStored return it with every
// error and never with a nil error. Bytes returns nil for it. Every later
// method that reads the bytes refuses the zero value with an error and
// never panics on it.
type Document struct {
	b []byte
}

// Bytes returns the document's bytes, uncopied. The caller must not modify
// them: the Document was validated over exactly these bytes. For the zero
// Document it returns nil, and it is the one method that may take the zero
// value without an error, because it reads nothing.
func (document Document) Bytes() []byte {
	return document.b
}

// minDocumentLength is the empty document: a length prefix and a terminator.
const minDocumentLength = 5

// TrustStored wraps bytes this process read back from its own store, which
// were validated when they were written. It checks only the framing, in
// O(1): the length prefix equals len(raw), and the last byte is 0. It does
// not walk the document, so it must never see bytes from anywhere else (a
// client, a peer, a restore file, a migration stream, a WAL record): those
// go through Validate.
func TrustStored(raw []byte) (Document, error) {
	if len(raw) < minDocumentLength {
		return Document{}, &ValidationError{Err: ErrMalformedDocument, Offset: 0,
			Reason: "fewer bytes than the 5 of an empty document"}
	}
	if declaredLength := int32(binary.LittleEndian.Uint32(raw)); int(declaredLength) != len(raw) {
		return Document{}, &ValidationError{Err: ErrMalformedDocument, Offset: 0,
			Reason: "the length prefix does not equal the number of bytes"}
	}
	if raw[len(raw)-1] != 0 {
		return Document{}, &ValidationError{Err: ErrMalformedDocument, Offset: len(raw) - 1,
			Reason: "the last byte is not the document's terminator"}
	}
	return Document{b: raw}, nil
}

// Raw returns the document as a Raw, for the mirrored API. It copies
// nothing: the Raw is the document's own bytes, which the caller must not
// modify, and its capacity ends with them, so an append copies first. The
// zero Document is refused with an error matching ErrZeroDocument.
func (document Document) Raw() (Raw, error) {
	if document.b == nil {
		return nil, ErrZeroDocument
	}
	return Raw(document.b[:len(document.b):len(document.b)]), nil
}
