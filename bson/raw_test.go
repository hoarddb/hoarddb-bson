// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"errors"
	"strings"
	"testing"
	"unsafe"

	"github.com/hoarddb/hoarddb-bson/bson"
)

// TestDocumentRawIsTheSameBytes: Document.Raw is the explicit conversion to
// the mirrored API, and copies nothing (spec 5.2). The Raw's capacity ends
// at the document, so appending to it cannot write past the document into
// the caller's buffer.
func TestDocumentRawIsTheSameBytes(t *testing.T) {
	buffer := append(documentOf(elementOf(0x02, "a", stringValueOf("x"))), 0xEE, 0xEE)
	documentBytes := buffer[:len(buffer)-2]
	document, err := bson.Validate(documentBytes, bson.Limits{Depth: 1})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	raw, err := document.Raw()
	if err != nil {
		t.Fatalf("Raw: %v", err)
	}
	if len(raw) != len(documentBytes) || unsafe.SliceData(raw) != unsafe.SliceData(documentBytes) {
		t.Fatal("Raw does not share the document's bytes")
	}
	if cap(raw) != len(raw) {
		t.Fatalf("Raw has capacity %d past its %d bytes", cap(raw), len(raw))
	}
	_ = append(raw, 1)
	if buffer[len(buffer)-2] != 0xEE {
		t.Fatal("appending to the Raw wrote into the bytes after the document")
	}
	if value := raw.Lookup("a"); value.StringValue() != "x" {
		t.Fatalf("Raw.Lookup(a) = %v", value)
	}
}

// TestZeroDocumentIsRefusedByRaw: the zero Document holds no document, and
// Raw refuses it with an error matching ErrZeroDocument instead of handing
// out a nil Raw that would read as malformed (core-api.md, step 2a's rule).
func TestZeroDocumentIsRefusedByRaw(t *testing.T) {
	var zero bson.Document
	raw, err := zero.Raw()
	if !errors.Is(err, bson.ErrZeroDocument) {
		t.Fatalf("Raw of the zero Document gave error %v, want ErrZeroDocument", err)
	}
	if raw != nil {
		t.Fatalf("Raw of the zero Document gave % x, want nil", raw)
	}
	empty, err := bson.Validate([]byte{5, 0, 0, 0, 0}, bson.Limits{Depth: 1})
	if err != nil {
		t.Fatalf("Validate of the empty document: %v", err)
	}
	if raw, err := empty.Raw(); err != nil || len(raw) != 5 {
		t.Fatalf("Raw of the empty document = % x, %v; want its five bytes", raw, err)
	}
}

// TestRawDoesNotConvertToADocument: Raw is a []byte, so a Document still
// comes only from Validate, TrustStored or the builder (call C1).
func TestRawDoesNotConvertToADocument(t *testing.T) {
	err := typeCheck(t, "var _ = bson.Document(bson.Raw{})")
	if err == nil || !strings.Contains(err.Error(), "cannot convert") {
		t.Fatalf("type-checking a Raw to Document conversion gave %v, want a conversion error", err)
	}
}
