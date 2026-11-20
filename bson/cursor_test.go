// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// int32Of is a little-endian int32 value.
func int32Of(number int32) []byte {
	return binary.LittleEndian.AppendUint32(nil, uint32(number))
}

// binaryValueOf is a binary value: payload length, subtype, payload.
func binaryValueOf(subtype byte, payload []byte) []byte {
	valueBytes := append(int32Of(int32(len(payload))), subtype)
	return append(valueBytes, payload...)
}

// everyTypeValues is one value of each of the 21 element types, legacy
// types included, with the exact bytes the cursor must report and skip.
func everyTypeValues() []struct {
	name       string
	valueType  bsontype.Type
	valueBytes []byte
} {
	objectID := bytes.Repeat([]byte{0xAB}, 12)
	return []struct {
		name       string
		valueType  bsontype.Type
		valueBytes []byte
	}{
		{"double", bsontype.Double, []byte{1, 2, 3, 4, 5, 6, 7, 8}},
		{"string", bsontype.String, stringValueOf("hoard")},
		{"empty string", bsontype.String, stringValueOf("")},
		{"embedded document", bsontype.EmbeddedDocument, documentOf(elementOf(0x10, "x", int32Of(1)))},
		{"empty document", bsontype.EmbeddedDocument, documentOf()},
		{"array", bsontype.Array, documentOf(elementOf(0x02, "0", stringValueOf("a")))},
		{"binary", bsontype.Binary, binaryValueOf(0x04, bytes.Repeat([]byte{7}, 16))},
		{"empty binary", bsontype.Binary, binaryValueOf(0x00, nil)},
		{"old binary", bsontype.Binary, binaryValueOf(0x02, append(int32Of(2), 1, 2))},
		{"undefined", bsontype.Undefined, []byte{}},
		{"object id", bsontype.ObjectID, objectID},
		{"boolean", bsontype.Boolean, []byte{1}},
		{"datetime", bsontype.DateTime, []byte{8, 7, 6, 5, 4, 3, 2, 1}},
		{"null", bsontype.Null, []byte{}},
		{"regex", bsontype.Regex, []byte("^a.*\x00imx\x00")},
		{"empty regex", bsontype.Regex, []byte{0, 0}},
		{"DBPointer", bsontype.DBPointer, append(stringValueOf("db.coll"), objectID...)},
		{"javascript", bsontype.JavaScript, stringValueOf("function(){}")},
		{"symbol", bsontype.Symbol, stringValueOf("sym")},
		{"code with scope", bsontype.CodeWithScope, codeWithScopeOf("f()", documentOf(elementOf(0x10, "x", int32Of(1))))},
		{"int32", bsontype.Int32, int32Of(-5)},
		{"timestamp", bsontype.Timestamp, []byte{1, 0, 0, 0, 2, 0, 0, 0}},
		{"int64", bsontype.Int64, []byte{9, 9, 9, 9, 9, 9, 9, 9}},
		{"decimal128", bsontype.Decimal128, bytes.Repeat([]byte{3}, 16)},
		{"min key", bsontype.MinKey, []byte{}},
		{"max key", bsontype.MaxKey, []byte{}},
	}
}

// TestCursorSkipsEveryTypeByLength: every element type, legacy ones
// included, is reported with its exact value bytes and skipped to the next
// element (spec 5.2).
func TestCursorSkipsEveryTypeByLength(t *testing.T) {
	for _, typed := range everyTypeValues() {
		t.Run(typed.name, func(t *testing.T) {
			documentBytes := documentOf(
				elementOf(byte(typed.valueType), "v", typed.valueBytes),
				elementOf(0x10, "next", int32Of(7)),
			)
			document, err := bson.Validate(documentBytes, bson.Limits{Depth: 3})
			if err != nil {
				t.Fatalf("the test document is not valid: %v", err)
			}
			cursor := document.Cursor()
			if !cursor.Next() {
				t.Fatalf("no first element: %v", cursor.Err())
			}
			if string(cursor.Key()) != "v" || cursor.Value().Type() != typed.valueType || !bytes.Equal(cursor.Value().Bytes(), typed.valueBytes) {
				t.Fatalf("first element is %q %v % x, want \"v\" %v % x",
					cursor.Key(), cursor.Value().Type(), cursor.Value().Bytes(), typed.valueType, typed.valueBytes)
			}
			if !cursor.Next() {
				t.Fatalf("the %s was not skipped to the next element: %v", typed.name, cursor.Err())
			}
			if string(cursor.Key()) != "next" || cursor.Value().Type() != bsontype.Int32 || !bytes.Equal(cursor.Value().Bytes(), int32Of(7)) {
				t.Fatalf("after the %s: %q %v % x, want \"next\" int32 07 00 00 00",
					typed.name, cursor.Key(), cursor.Value().Type(), cursor.Value().Bytes())
			}
			if cursor.Next() || cursor.Err() != nil {
				t.Fatalf("a third element, or an error at the end: %v", cursor.Err())
			}
		})
	}
}

// TestCursorStopsAtMalformedBytes: a stored document is framed but not
// walked (TrustStored), so the cursor meets malformed bytes inside it. It
// stops there, reports the elements before, and Err says where.
func TestCursorStopsAtMalformedBytes(t *testing.T) {
	good := elementOf(0x10, "a", int32Of(1))
	for _, malformed := range []struct {
		name string
		tail []byte
	}{
		{"invalid type byte", elementOf(0x14, "b", int32Of(1))},
		{"unterminated key", []byte{0x10, 'b', 'b'}},
		{"string length past the document", elementOf(0x02, "b", int32Of(100))},
		{"string length negative", elementOf(0x02, "b", int32Of(-3))},
		{"string length zero, without its terminator", elementOf(0x02, "b", int32Of(0))},
		{"code with scope shorter than an empty one", elementOf(0x0F, "b", append(int32Of(13), make([]byte, 9)...))},
		{"embedded length too short", elementOf(0x03, "b", int32Of(4))},
		{"binary length negative", elementOf(0x05, "b", append(int32Of(-1), 0))},
		{"fixed size past the document", elementOf(0x12, "b", []byte{1, 2, 3})},
		{"regex without its second terminator", elementOf(0x0B, "b", []byte("x\x00y"))},
	} {
		t.Run(malformed.name, func(t *testing.T) {
			document, err := bson.TrustStored(documentOf(good, malformed.tail))
			if err != nil {
				t.Fatalf("the framing is not good: %v", err)
			}
			cursor := document.Cursor()
			if !cursor.Next() || string(cursor.Key()) != "a" {
				t.Fatalf("the good element before the bad one was not reported: %v", cursor.Err())
			}
			if cursor.Next() {
				t.Fatalf("the malformed element was reported as %q %v", cursor.Key(), cursor.Value().Type())
			}
			var validationError *bson.ValidationError
			if !errors.As(cursor.Err(), &validationError) || !errors.Is(cursor.Err(), bson.ErrMalformedDocument) {
				t.Fatalf("Err is %v, want a *ValidationError matching ErrMalformedDocument", cursor.Err())
			}
			if validationError.Offset != 4+len(good) {
				t.Errorf("the error is at byte %d, want %d, where the bad element starts", validationError.Offset, 4+len(good))
			}
			if cursor.Next() {
				t.Error("Next after an error reported another element")
			}
		})
	}
}

// TestZeroDocumentIsRefusedByTheCursorAndLookup: the zero Document holds no
// bytes and is not a validated document (document.go:Document), so a method
// that reads bytes refuses it with ErrZeroDocument and never panics. The
// empty document, which a constructor made, is not refused.
func TestZeroDocumentIsRefusedByTheCursorAndLookup(t *testing.T) {
	var zeroDocument bson.Document
	cursor := zeroDocument.Cursor()
	if cursor.Next() {
		t.Fatal("the zero Document has an element")
	}
	if !errors.Is(cursor.Err(), bson.ErrZeroDocument) {
		t.Errorf("the zero Document's cursor Err is %v, want an error matching ErrZeroDocument", cursor.Err())
	}
	for _, path := range []bson.Path{{bson.Name("a")}, {bson.Position(0)}, {}} {
		view, found, err := zeroDocument.Lookup(path)
		if found || view.Bytes() != nil || !errors.Is(err, bson.ErrZeroDocument) {
			t.Errorf("Lookup(%v) on the zero Document gave (% x, %v, %v), want not found and an error matching ErrZeroDocument",
				path, view.Bytes(), found, err)
		}
	}
	emptyDocument, err := bson.Validate(documentOf(), bson.Limits{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	emptyCursor := emptyDocument.Cursor()
	if emptyCursor.Next() || emptyCursor.Err() != nil {
		t.Errorf("the empty document's cursor: an element, or error %v", emptyCursor.Err())
	}
	if _, found, err := emptyDocument.Lookup(bson.Path{bson.Name("a")}); found || err != nil {
		t.Errorf("Lookup on the empty document: found %v, err %v; want absent and no error", found, err)
	}
}

// TestCursorKeyCompareAllocatesNothing: comparing a key is a byte compare
// (spec 5.2, 9.2); walking a document and comparing every key costs nothing.
func TestCursorKeyCompareAllocatesNothing(t *testing.T) {
	if raceEnabled {
		t.Skip("-race changes allocation counts; the gates run this test without it")
	}
	document, err := bson.Validate(documentOf(
		elementOf(0x02, "street", stringValueOf("12 Sample Street")),
		elementOf(0x02, "city", stringValueOf("Exampleton")),
		elementOf(0x03, "zip", documentOf(elementOf(0x10, "code", int32Of(1)))),
	), bson.Limits{Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	matches := 0
	allocations := testing.AllocsPerRun(100, func() {
		cursor := document.Cursor()
		for cursor.Next() {
			if cursor.KeyEquals("zip") {
				matches++
			}
		}
	})
	if matches == 0 {
		t.Fatal("KeyEquals never matched, so the test compares nothing")
	}
	if allocations != 0 {
		t.Errorf("walking and comparing keys allocates %v times, want 0", allocations)
	}
}

// TestCursorKeyEqualsIsExact: a key equals a name only byte for byte, so a
// prefix, an extension or a name with a NUL in it never matches.
func TestCursorKeyEqualsIsExact(t *testing.T) {
	document, err := bson.Validate(documentOf(elementOf(0x0A, "name", nil)), bson.Limits{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	cursor := document.Cursor()
	if !cursor.Next() {
		t.Fatal(cursor.Err())
	}
	for name, want := range map[string]bool{"name": true, "nam": false, "names": false, "name\x00": false, "": false, "Name": false} {
		if got := cursor.KeyEquals(name); got != want {
			t.Errorf("KeyEquals(%q) = %v, want %v", name, got, want)
		}
	}
}
