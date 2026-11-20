// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
)

// TestValidateDepthLadder pins the depth rule of spec 5.2 and trap 5 of the
// build doc: the root is level 1, and an embedded document, an array and a
// code-with-scope's scope each open one more. At Limits{Depth: n} depth n
// passes and n+1 is refused with ErrNestingTooDeep.
func TestValidateDepthLadder(t *testing.T) {
	for _, shape := range []nestingShape{nestedDocuments, nestedArrays, nestedScopes, nestedMixed} {
		for _, depthLimit := range []int{1, 2, 100, 101, 102, 103} {
			t.Run(fmt.Sprintf("%s/limit=%d", shape, depthLimit), func(t *testing.T) {
				limits := bson.Limits{Depth: depthLimit}
				if _, err := bson.Validate(nestedDocumentOf(depthLimit, shape), limits); err != nil {
					t.Fatalf("a document %d levels deep was refused at its own limit: %v", depthLimit, err)
				}
				_, err := bson.Validate(nestedDocumentOf(depthLimit+1, shape), limits)
				if !errors.Is(err, bson.ErrNestingTooDeep) {
					t.Fatalf("a document %d levels deep at limit %d gave %v, want an error matching ErrNestingTooDeep", depthLimit+1, depthLimit, err)
				}
			})
		}
	}
}

// TestValidateDepthAboveTheFixedStack crosses the 128 levels the scan holds
// without allocating, up to the library ceiling.
func TestValidateDepthAboveTheFixedStack(t *testing.T) {
	for _, depth := range []int{128, 129, bson.MaxNestingCeiling} {
		if _, err := bson.Validate(nestedDocumentOf(depth, nestedMixed), bson.Limits{Depth: bson.MaxNestingCeiling}); err != nil {
			t.Errorf("depth %d at the ceiling: %v", depth, err)
		}
	}
	_, err := bson.Validate(nestedDocumentOf(bson.MaxNestingCeiling+1, nestedMixed), bson.Limits{Depth: bson.MaxNestingCeiling})
	if !errors.Is(err, bson.ErrNestingTooDeep) {
		t.Fatalf("one level past the ceiling gave %v, want ErrNestingTooDeep", err)
	}
}

// TestValidateRefusesLimitsOutsideTheCeiling: a site that forgets its
// depth gets an error, not a silent default (trap 5: a limit made uniform).
func TestValidateRefusesLimitsOutsideTheCeiling(t *testing.T) {
	emptyDocument := documentOf()
	for _, limits := range []bson.Limits{{}, {Depth: -1}, {Depth: bson.MaxNestingCeiling + 1}, {Depth: 10, Size: -1}} {
		if _, err := bson.Validate(emptyDocument, limits); !errors.Is(err, bson.ErrInvalidLimits) {
			t.Errorf("Limits%+v gave %v, want ErrInvalidLimits", limits, err)
		}
	}
}

func TestValidateChecksSizeOnlyWhenSet(t *testing.T) {
	documentBytes := documentOf(elementOf(0x02, "name", stringValueOf("hoard")))
	exactLimits := bson.Limits{Depth: 1, Size: len(documentBytes)}
	if _, err := bson.Validate(documentBytes, exactLimits); err != nil {
		t.Fatalf("a document of exactly Size bytes was refused: %v", err)
	}
	_, err := bson.Validate(documentBytes, bson.Limits{Depth: 1, Size: len(documentBytes) - 1})
	if !errors.Is(err, bson.ErrDocumentTooLarge) {
		t.Fatalf("a document one byte over Size gave %v, want ErrDocumentTooLarge", err)
	}
	if _, err := bson.Validate(documentBytes, bson.Limits{Depth: 1}); err != nil {
		t.Fatalf("Size 0 must mean no size check: %v", err)
	}
}

// TestValidateDoesNotCopy: the Document is the caller's bytes (spec 5.2).
func TestValidateDoesNotCopy(t *testing.T) {
	documentBytes := documentOf(elementOf(0x10, "n", []byte{1, 0, 0, 0}))
	validated, err := bson.Validate(documentBytes, bson.Limits{Depth: 1})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if &validated.Bytes()[0] != &documentBytes[0] || len(validated.Bytes()) != len(documentBytes) {
		t.Fatal("Validate's Document does not hold the caller's bytes")
	}
}

// TestValidateErrorsCarryTheOffset: a refusal names where it stopped, for
// a message a developer can act on.
func TestValidateErrorsCarryTheOffset(t *testing.T) {
	// {"b": true} with the boolean byte at offset 8 set to 2.
	badBoolean := documentOf(elementOf(0x08, "b", []byte{2}))
	_, err := bson.Validate(badBoolean, bson.Limits{Depth: 1})
	var validationError *bson.ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("got %v (%T), want a *ValidationError", err, err)
	}
	if !errors.Is(err, bson.ErrMalformedDocument) {
		t.Fatalf("got %v, want an error matching ErrMalformedDocument", err)
	}
	if validationError.Offset != 7 {
		t.Fatalf("Offset is %d, want 7 (the boolean's value byte)", validationError.Offset)
	}
}
