// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// The entry points' own rules, without the vendor: the oracle module holds
// the differentials (oracle/entrypoints_test.go).

// TestZeroDocumentIsRefusedByDecode is Decode's row of the zero Document's
// contract (document.go: Document, core-api.md step 2a): an error matching
// ErrZeroDocument, and the target untouched.
func TestZeroDocumentIsRefusedByDecode(t *testing.T) {
	var zeroDocument bson.Document
	target := map[string]any{"kept": int32(1)}
	if err := zeroDocument.Decode(&target); !errors.Is(err, bson.ErrZeroDocument) {
		t.Fatalf("Decode of the zero Document: %v, want an error matching ErrZeroDocument", err)
	}
	if len(target) != 1 || target["kept"] != int32(1) {
		t.Errorf("Decode of the zero Document changed the target to %v", target)
	}
	emptyDocument, err := bson.TrustStored(documentOf())
	if err != nil {
		t.Fatal(err)
	}
	if err := emptyDocument.Decode(&target); err != nil {
		t.Errorf("Decode of the empty document: %v", err)
	}
}

// TestEntryPointsHoldTheCeiling: 200 levels decode and encode, 201 are
// refused with an error matching ErrNestingTooDeep, for every shape of
// nesting.
func TestEntryPointsHoldTheCeiling(t *testing.T) {
	for _, shape := range []nestingShape{nestedDocuments, nestedArrays, nestedScopes, nestedMixed} {
		atCeiling := nestedDocumentOf(bson.MaxNestingCeiling, shape)
		pastCeiling := nestedDocumentOf(bson.MaxNestingCeiling+1, shape)
		var decoded any
		if err := bson.Unmarshal(atCeiling, &decoded); err != nil {
			t.Fatalf("%s: Unmarshal of 200 levels: %v", shape, err)
		}
		reencoded, err := bson.Marshal(decoded)
		if err != nil {
			t.Fatalf("%s: Marshal of 200 levels: %v", shape, err)
		}
		// The mixed shape puts keys other than "0" in its arrays, which an
		// encode renumbers, so only the others come back byte for byte.
		if shape != nestedMixed && !bytes.Equal(reencoded, atCeiling) {
			t.Errorf("%s: 200 levels did not round-trip", shape)
		}
		if err := bson.Unmarshal(pastCeiling, &decoded); !errors.Is(err, bson.ErrNestingTooDeep) {
			t.Errorf("%s: Unmarshal of 201 levels: %v, want ErrNestingTooDeep", shape, err)
		}
		if err := (bson.RawValue{Type: bsontype.EmbeddedDocument, Value: pastCeiling}).Unmarshal(&decoded); !errors.Is(err, bson.ErrNestingTooDeep) {
			t.Errorf("%s: RawValue.Unmarshal of 201 levels: %v, want ErrNestingTooDeep", shape, err)
		}
		// One more level on the Go side: the decoded 200 levels inside a
		// new root are 201.
		if _, err := bson.Marshal(map[string]any{"a": decoded}); !errors.Is(err, bson.ErrNestingTooDeep) {
			t.Errorf("%s: Marshal of 201 levels: %v, want ErrNestingTooDeep", shape, err)
		}
		if _, _, err := bson.MarshalValue(map[string]any{"a": decoded}); !errors.Is(err, bson.ErrNestingTooDeep) {
			t.Errorf("%s: MarshalValue of 201 levels: %v, want ErrNestingTooDeep", shape, err)
		}
		if _, _, err := bson.MarshalValue(decoded); err != nil {
			t.Errorf("%s: MarshalValue of 200 levels: %v", shape, err)
		}
	}
}

// TestEntryPointsShareOnePlanCache holds spec 5.3 and 5.5: the library has
// one plan cache, package bson's, and both its encoder and its decoder take
// their plans from it, so a type is compiled once for both directions.
func TestEntryPointsShareOnePlanCache(t *testing.T) {
	declared, encoderCache, decoderCache := bson.EntryPointPlanCaches()
	if declared == nil || encoderCache != declared || decoderCache != declared {
		t.Errorf("plan caches: package %p, encoder %p, decoder %p; want one cache for all three", declared, encoderCache, decoderCache)
	}
}
