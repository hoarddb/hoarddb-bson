// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"bytes"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// typedSegmentDocument holds every case the typed-segment rule decides: a
// document key "0", an array whose keys are out of sequence, a duplicate key,
// an empty key, and scalars to walk through.
func typedSegmentDocument() []byte {
	return documentOf(
		elementOf(0x03, "a", documentOf(elementOf(0x10, "b", int32Of(1)))),
		elementOf(0x04, "list", documentOf(
			elementOf(0x02, "0", stringValueOf("first")),
			elementOf(0x03, "1", documentOf(elementOf(0x02, "c", stringValueOf("inner")))),
		)),
		elementOf(0x04, "shuffled", documentOf(
			elementOf(0x02, "5", stringValueOf("keyed five")),
			elementOf(0x02, "0", stringValueOf("keyed zero")),
		)),
		elementOf(0x02, "0", stringValueOf("document key zero")),
		elementOf(0x10, "dup", int32Of(1)),
		elementOf(0x10, "dup", int32Of(2)),
		elementOf(0x02, "", stringValueOf("empty key")),
		elementOf(0x02, "s", stringValueOf("text")),
		elementOf(0x0F, "code", codeWithScopeOf("f()", documentOf(elementOf(0x10, "x", int32Of(9))))),
	)
}

// TestLookupFollowsTypedSegments is the rule of spec 5.2 (C24), the one
// server/fieldpath.go:resolveRawPath applies: a name needs a document, a
// position needs an array and counts its elements, a duplicate key gives
// the first match, and anything else on the way is absent.
func TestLookupFollowsTypedSegments(t *testing.T) {
	document, err := bson.Validate(typedSegmentDocument(), bson.Limits{Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []struct {
		name      string
		path      bson.Path
		found     bool
		valueType bsontype.Type
		value     []byte
	}{
		{"top-level name", bson.Path{bson.Name("s")}, true, bsontype.String, stringValueOf("text")},
		{"nested name", bson.Path{bson.Name("a"), bson.Name("b")}, true, bsontype.Int32, int32Of(1)},
		{"array position", bson.Path{bson.Name("list"), bson.Position(0)}, true, bsontype.String, stringValueOf("first")},
		{"name inside a position", bson.Path{bson.Name("list"), bson.Position(1), bson.Name("c")}, true, bsontype.String, stringValueOf("inner")},
		{"positions count, keys do not", bson.Path{bson.Name("shuffled"), bson.Position(0)}, true, bsontype.String, stringValueOf("keyed five")},
		{"second position of an out-of-sequence array", bson.Path{bson.Name("shuffled"), bson.Position(1)}, true, bsontype.String, stringValueOf("keyed zero")},
		{"document key 0 by name", bson.Path{bson.Name("0")}, true, bsontype.String, stringValueOf("document key zero")},
		{"duplicate key gives the first", bson.Path{bson.Name("dup")}, true, bsontype.Int32, int32Of(1)},
		{"empty key", bson.Path{bson.Name("")}, true, bsontype.String, stringValueOf("empty key")},
		{"a whole embedded document", bson.Path{bson.Name("a")}, true, bsontype.EmbeddedDocument, documentOf(elementOf(0x10, "b", int32Of(1)))},
		{"a position is not a document key", bson.Path{bson.Position(0)}, false, 0, nil},
		{"a name is not an array position", bson.Path{bson.Name("list"), bson.Name("0")}, false, 0, nil},
		{"position past the end", bson.Path{bson.Name("list"), bson.Position(2)}, false, 0, nil},
		{"negative position", bson.Path{bson.Name("list"), bson.Position(-1)}, false, 0, nil},
		{"missing name", bson.Path{bson.Name("absent")}, false, 0, nil},
		{"name through a string", bson.Path{bson.Name("s"), bson.Name("x")}, false, 0, nil},
		{"position through a string", bson.Path{bson.Name("s"), bson.Position(0)}, false, 0, nil},
		{"name through an int32", bson.Path{bson.Name("a"), bson.Name("b"), bson.Name("c")}, false, 0, nil},
		{"a code-with-scope's scope is not a document", bson.Path{bson.Name("code"), bson.Name("x")}, false, 0, nil},
		{"the empty path", bson.Path{}, false, 0, nil},
		{"a nil path", nil, false, 0, nil},
	} {
		t.Run(lookup.name, func(t *testing.T) {
			view, found, err := document.Lookup(lookup.path)
			if err != nil {
				t.Fatalf("a validated document gave an error: %v", err)
			}
			if found != lookup.found {
				t.Fatalf("found is %v, want %v", found, lookup.found)
			}
			if !found {
				if view.Type() != 0 || view.Bytes() != nil {
					t.Fatalf("a miss returned a view %v % x, want the zero View", view.Type(), view.Bytes())
				}
				return
			}
			if view.Type() != lookup.valueType || !bytes.Equal(view.Bytes(), lookup.value) {
				t.Fatalf("got %v % x, want %v % x", view.Type(), view.Bytes(), lookup.valueType, lookup.value)
			}
		})
	}
}

// TestLookupDoesNotCopy: the view's bytes are the document's own (spec 5.2).
func TestLookupDoesNotCopy(t *testing.T) {
	documentBytes := typedSegmentDocument()
	document, err := bson.Validate(documentBytes, bson.Limits{Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	view, found, err := document.Lookup(bson.Path{bson.Name("list"), bson.Position(1), bson.Name("c")})
	if !found || err != nil {
		t.Fatal("not found")
	}
	// The value is the string "inner"; write over its first letter in the
	// document's own bytes, and the view must see the change.
	letterOffset := bytes.Index(documentBytes, []byte("inner"))
	documentBytes[letterOffset] = 'I'
	if !bytes.Equal(view.Bytes(), stringValueOf("Inner")) {
		t.Fatalf("the view holds % x after the document changed: its bytes were copied", view.Bytes())
	}
}

// TestLookupStopsQuietlyAtMalformedBytes: inside a stored document that
// TrustStored framed but did not walk, a member before the malformed bytes
// is found and one after them is absent, as resolveRawPath's walk stops.
func TestLookupStopsQuietlyAtMalformedBytes(t *testing.T) {
	document, err := bson.TrustStored(documentOf(
		elementOf(0x10, "before", int32Of(1)),
		elementOf(0x14, "bad", int32Of(1)),
		elementOf(0x10, "after", int32Of(2)),
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := document.Lookup(bson.Path{bson.Name("before")}); !found || err != nil {
		t.Error("the member before the malformed bytes was not found")
	}
	if _, found, err := document.Lookup(bson.Path{bson.Name("after")}); found || err != nil {
		t.Errorf("a member after the malformed bytes: found %v, err %v; want absent and no error", found, err)
	}
}
