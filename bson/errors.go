// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

import (
	"errors"
	"fmt"

	"github.com/hoarddb/hoarddb-bson/internal/nesting"
)

// ErrMalformedDocument is under every refusal of bytes that are not one
// well-formed BSON document.
var ErrMalformedDocument = errors.New("bson: malformed document")

// ErrNestingTooDeep is under every refusal of a document nested deeper than
// the limit it was checked at, or than MaxNestingCeiling: Validate's, and
// that of Marshal, MarshalValue, Unmarshal, RawValue.Unmarshal and
// Document.Decode at the ceiling.
var ErrNestingTooDeep = nesting.ErrTooDeep

// ErrDocumentTooLarge is under the refusal of a document longer than its
// Limits.Size.
var ErrDocumentTooLarge = errors.New("bson: document too large")

// ErrZeroDocument is under the refusal, by a method that reads a
// Document's bytes, of the zero Document, which no constructor made.
var ErrZeroDocument = errors.New("bson: the zero Document holds no document; " +
	"use the Document that Validate or TrustStored returned with a nil error")

// ErrInvalidLimits is returned for Limits outside their documented range.
// It is the caller's mistake, not the document's.
var ErrInvalidLimits = errors.New("bson: invalid limits")

// ErrInvalidKey is under a Builder's refusal of a key: one holding a zero
// byte, or any key but "" inside an array.
var ErrInvalidKey = errors.New("bson: invalid key")

// ErrUnbalancedBuilder is under a Builder's refusal of an End with no level
// open, or of a Document with a level still open.
var ErrUnbalancedBuilder = errors.New("bson: unbalanced builder levels")

// ValidationError is the error Validate and TrustStored return for bytes
// they refuse. It matches, through errors.Is, the sentinel in Err.
type ValidationError struct {
	// Err is ErrMalformedDocument, ErrNestingTooDeep or ErrDocumentTooLarge.
	Err error
	// Offset is the byte at which the check stopped.
	Offset int
	// Reason says what was wrong there.
	Reason string
}

// Error names the problem and where it is.
func (validationError *ValidationError) Error() string {
	return fmt.Sprintf("%v at byte %d: %s", validationError.Err, validationError.Offset, validationError.Reason)
}

// Unwrap returns the sentinel, for errors.Is.
func (validationError *ValidationError) Unwrap() error {
	return validationError.Err
}
