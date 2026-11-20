// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"encoding/binary"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
)

// TestDocumentIsAnOpaqueStruct pins goal 4 (spec 5.2, call C1): a Document
// is made by a constructor only, never converted from a []byte.
func TestDocumentIsAnOpaqueStruct(t *testing.T) {
	documentType := reflect.TypeOf(bson.Document{})
	if documentType.Kind() != reflect.Struct || documentType.NumField() != 1 || documentType.Field(0).IsExported() {
		t.Fatalf("bson.Document is %s with %d fields, want a struct with one unexported field", documentType.Kind(), documentType.NumField())
	}
	for _, snippet := range []string{
		"var _ = bson.Document([]byte{})",
		"var _ = []byte(bson.Document{})",
	} {
		t.Run(snippet, func(t *testing.T) {
			err := typeCheck(t, snippet)
			if err == nil || !strings.Contains(err.Error(), "cannot convert") {
				t.Fatalf("type-checking %q gave %v, want a conversion error", snippet, err)
			}
		})
	}
	// The harness itself: a snippet that is legal must type-check, or the
	// conversion errors above could come from a broken importer.
	if err := typeCheck(t, "var _ = bson.Document{}.Bytes()"); err != nil {
		t.Fatalf("a legal snippet failed to type-check: %v", err)
	}
}

// typeCheck type-checks one declaration in a file that imports the library.
func typeCheck(t *testing.T, declaration string) error {
	t.Helper()
	t.Setenv("GOWORK", "off")
	fileSet := token.NewFileSet()
	sourceText := "package snippet\n\nimport \"github.com/hoarddb/hoarddb-bson/bson\"\n\n" + declaration + "\n"
	parsedFile, err := parser.ParseFile(fileSet, "snippet.go", sourceText, 0)
	if err != nil {
		t.Fatalf("parsing the snippet: %v", err)
	}
	config := types.Config{Importer: importer.ForCompiler(fileSet, "source", nil)}
	_, err = config.Check("snippet", fileSet, []*ast.File{parsedFile}, nil)
	return err
}

// TestTrustStoredChecksFramingOnly: TrustStored checks the length prefix
// and the last byte in O(1) and walks nothing (spec 5.2).
func TestTrustStoredChecksFramingOnly(t *testing.T) {
	wellFormed := documentOf(elementOf(0x02, "a", stringValueOf("x")))

	wrongPrefix := append([]byte(nil), wellFormed...)
	binary.LittleEndian.PutUint32(wrongPrefix, uint32(len(wrongPrefix)+1))
	noLastZero := append([]byte(nil), wellFormed...)
	noLastZero[len(noLastZero)-1] = 1
	trailingByte := append(append([]byte(nil), wellFormed...), 0)

	for _, refused := range []struct {
		name     string
		rawBytes []byte
	}{
		{"wrong length prefix", wrongPrefix},
		{"missing last zero", noLastZero},
		{"trailing byte", trailingByte},
		{"nil", nil},
		{"four bytes claiming four", []byte{4, 0, 0, 0}},
		{"negative prefix", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0}},
	} {
		if _, err := bson.TrustStored(refused.rawBytes); !errors.Is(err, bson.ErrMalformedDocument) {
			t.Errorf("%s: got %v, want an error matching ErrMalformedDocument", refused.name, err)
		}
	}

	if trusted, err := bson.TrustStored(wellFormed); err != nil || &trusted.Bytes()[0] != &wellFormed[0] {
		t.Fatalf("a well-formed document: err %v, or its bytes were copied", err)
	}
	if _, err := bson.TrustStored(documentOf()); err != nil {
		t.Fatalf("the empty document: %v", err)
	}

	// 500 levels deep, past every limit, with an interior length broken:
	// Validate refuses it, TrustStored accepts it because it does not walk.
	deepBroken := nestedDocumentOf(500, nestedDocuments)
	deepBroken[200] ^= 0x40
	if _, err := bson.Validate(deepBroken, bson.Limits{Depth: bson.MaxNestingCeiling}); err == nil {
		t.Fatal("the broken deep document passed Validate, so it does not prove TrustStored skips the walk")
	}
	if _, err := bson.TrustStored(deepBroken); err != nil {
		t.Fatalf("TrustStored walked the document: %v", err)
	}
}

// TestZeroDocumentHoldsNoBytes pins the zero value's contract (document.go:
// Document): it holds no bytes, is not a validated document, and Bytes
// returns nil for it without panicking. No constructor returns it with a
// nil error, so code that got a Document from Validate or TrustStored
// without an error never holds the zero value.
func TestZeroDocumentHoldsNoBytes(t *testing.T) {
	var zeroDocument bson.Document
	if zeroBytes := zeroDocument.Bytes(); zeroBytes != nil {
		t.Fatalf("the zero Document's Bytes is % x, want nil", zeroBytes)
	}
	refusedInputs := [][]byte{nil, {}, {4, 0, 0, 0}, {5, 0, 0, 0, 1}}
	for _, refusedInput := range refusedInputs {
		if document, err := bson.Validate(refusedInput, bson.Limits{Depth: 1}); err == nil || document.Bytes() != nil {
			t.Errorf("Validate(% x) gave (% x, %v), want the zero Document and an error", refusedInput, document.Bytes(), err)
		}
		if document, err := bson.TrustStored(refusedInput); err == nil || document.Bytes() != nil {
			t.Errorf("TrustStored(% x) gave (% x, %v), want the zero Document and an error", refusedInput, document.Bytes(), err)
		}
	}
	emptyDocument := documentOf()
	for _, construct := range []func([]byte) (bson.Document, error){
		func(raw []byte) (bson.Document, error) { return bson.Validate(raw, bson.Limits{Depth: 1}) },
		bson.TrustStored,
	} {
		if document, err := construct(emptyDocument); err != nil || document.Bytes() == nil {
			t.Errorf("an accepted empty document gave (% x, %v), want its bytes", document.Bytes(), err)
		}
	}
}
