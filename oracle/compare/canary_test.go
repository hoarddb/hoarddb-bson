// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package compare_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/hoarddb/hoarddb-bson/oracle/compare"
)

// Each canary hands a compare helper a deliberately wrong "library" stand-in
// and asserts that the helper reported it. The helper reports to a recorder,
// so "it reported a failure" is an assertion and never fails the real test by
// accident. Each canary also runs the helper on identical input first: a
// helper that reports everything would otherwise pass every failure canary.

// failureRecorder is a testing.TB that records failures instead of failing.
// Methods it does not override reach the real test, so a helper that called
// one of them would fail the canary loudly rather than be counted.
type failureRecorder struct {
	testing.TB
	failures []string
}

func newFailureRecorder(t *testing.T) *failureRecorder {
	return &failureRecorder{TB: t}
}

func (r *failureRecorder) Helper() {}

func (r *failureRecorder) Error(args ...any) { r.failures = append(r.failures, fmt.Sprint(args...)) }

func (r *failureRecorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *failureRecorder) Fatal(args ...any) { r.Error(args...) }

func (r *failureRecorder) Fatalf(format string, args ...any) { r.Errorf(format, args...) }

func (r *failureRecorder) Fail() { r.failures = append(r.failures, "Fail called") }

func (r *failureRecorder) FailNow() { r.Fail() }

func (r *failureRecorder) Failed() bool { return len(r.failures) > 0 }

func vendorMarshal(t *testing.T, goValue any) []byte {
	t.Helper()
	encoded, err := vendorbson.Marshal(goValue)
	if err != nil {
		t.Fatalf("vendor Marshal(%T): %v", goValue, err)
	}
	return encoded
}

// vendorMarshalUnlike marshals goValue until the vendor's random map order
// differs from standIn's, so that a "passes on reordered" canary has really
// been handed a reordering.
func vendorMarshalUnlike(t *testing.T, goValue any, standIn []byte) []byte {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		if vendorBytes := vendorMarshal(t, goValue); !bytes.Equal(vendorBytes, standIn) {
			return vendorBytes
		}
	}
	t.Fatalf("the vendor emitted the stand-in's order 100 times for %T; the reordering is not exercised", goValue)
	return nil
}

// requireReported fails t unless the recorder holds exactly one failure that
// names wantPath.
func requireReported(t *testing.T, recorder *failureRecorder, wantPath string) {
	t.Helper()
	if len(recorder.failures) != 1 {
		t.Fatalf("helper reported %d failures, want exactly 1: %q", len(recorder.failures), recorder.failures)
	}
	if !strings.Contains(recorder.failures[0], wantPath) {
		t.Fatalf("helper reported %q, want it to name the path %q", recorder.failures[0], wantPath)
	}
	t.Logf("reported as expected: %s", recorder.failures[0])
}

func requireSilent(t *testing.T, recorder *failureRecorder, situation string) {
	t.Helper()
	if len(recorder.failures) != 0 {
		t.Fatalf("helper reported a failure on %s: %q", situation, recorder.failures)
	}
}

// encodedIdentityControl runs compare.Encoded on the vendor's bytes against
// themselves, which must never report.
func encodedIdentityControl(t *testing.T, goValue any, vendorBytes []byte) {
	t.Helper()
	recorder := newFailureRecorder(t)
	compare.Encoded(recorder, goValue, vendorBytes, bytes.Clone(vendorBytes))
	requireSilent(t, recorder, "identical encodings")
}

type orderedPerson struct {
	Name  string `bson:"name"`
	Age   int32  `bson:"age"`
	Email string `bson:"email"`
}

func TestEncodedCompareFailsOnReorderedStructField(t *testing.T) {
	person := orderedPerson{Name: "Ada", Age: 36, Email: "ada@example.org"}
	vendorBytes := vendorMarshal(t, person)
	encodedIdentityControl(t, person, vendorBytes)

	// The stand-in library swaps the first two fields; every element is
	// otherwise byte-identical.
	swappedBytes := vendorMarshal(t, vendorprimitive.D{
		{Key: "age", Value: int32(36)},
		{Key: "name", Value: "Ada"},
		{Key: "email", Value: "ada@example.org"},
	})
	recorder := newFailureRecorder(t)
	compare.Encoded(recorder, person, vendorBytes, swappedBytes)
	requireReported(t, recorder, "$")
}

func TestEncodedCompareFailsOnChangedIntWidth(t *testing.T) {
	counters := map[string]any{"count": int32(7), "label": "x", "total": int32(9)}
	vendorBytes := vendorMarshal(t, counters)
	encodedIdentityControl(t, counters, vendorBytes)

	// The stand-in emits int64 where the vendor emits int32, inside a map
	// value; the map level itself may be in any order.
	widenedBytes := vendorMarshal(t, vendorprimitive.D{
		{Key: "total", Value: int32(9)},
		{Key: "label", Value: "x"},
		{Key: "count", Value: int64(7)},
	})
	recorder := newFailureRecorder(t)
	compare.Encoded(recorder, counters, vendorBytes, widenedBytes)
	requireReported(t, recorder, "$.count")
}

func TestEncodedComparePassesOnReorderedMapKeys(t *testing.T) {
	nested := map[string]any{
		"alpha": int32(1),
		"beta":  "b",
		"gamma": map[string]any{"x": int32(1), "y": int32(2), "z": int32(3)},
		"list":  []any{map[string]any{"p": true, "q": false}},
	}
	// The stand-in sorts every map level descending.
	descendingBytes := vendorMarshal(t, vendorprimitive.D{
		{Key: "list", Value: vendorprimitive.A{vendorprimitive.D{{Key: "q", Value: false}, {Key: "p", Value: true}}}},
		{Key: "gamma", Value: vendorprimitive.D{{Key: "z", Value: int32(3)}, {Key: "y", Value: int32(2)}, {Key: "x", Value: int32(1)}}},
		{Key: "beta", Value: "b"},
		{Key: "alpha", Value: int32(1)},
	})
	vendorBytes := vendorMarshalUnlike(t, nested, descendingBytes)
	recorder := newFailureRecorder(t)
	compare.Encoded(recorder, nested, vendorBytes, descendingBytes)
	requireSilent(t, recorder, "map levels in a different order")
}

func TestEncodedCompareFailsOnReorderedBsonD(t *testing.T) {
	ordered := vendorprimitive.D{{Key: "a", Value: int32(1)}, {Key: "b", Value: int32(2)}, {Key: "c", Value: "three"}}
	vendorBytes := vendorMarshal(t, ordered)
	encodedIdentityControl(t, ordered, vendorBytes)

	reorderedBytes := vendorMarshal(t, vendorprimitive.D{{Key: "b", Value: int32(2)}, {Key: "a", Value: int32(1)}, {Key: "c", Value: "three"}})
	recorder := newFailureRecorder(t)
	compare.Encoded(recorder, ordered, vendorBytes, reorderedBytes)
	requireReported(t, recorder, "$")
}

type taggedWithExtras struct {
	Name   string         `bson:"name"`
	Kind   string         `bson:"kind"`
	Extras map[string]any `bson:",inline"`
}

// An inline map's entries follow the struct's fields in the map's own random
// order: the fields compare in order, the inline entries as a set.
func TestEncodedComparePassesOnReorderedInlineMapKeys(t *testing.T) {
	tagged := taggedWithExtras{Name: "n", Kind: "k", Extras: map[string]any{"e1": int32(1), "e2": int32(2), "e3": int32(3)}}
	descendingExtras := vendorMarshal(t, vendorprimitive.D{
		{Key: "name", Value: "n"}, {Key: "kind", Value: "k"},
		{Key: "e3", Value: int32(3)}, {Key: "e2", Value: int32(2)}, {Key: "e1", Value: int32(1)},
	})
	vendorBytes := vendorMarshalUnlike(t, tagged, descendingExtras)
	encodedIdentityControl(t, tagged, vendorBytes)

	recorder := newFailureRecorder(t)
	compare.Encoded(recorder, tagged, vendorBytes, descendingExtras)
	requireSilent(t, recorder, "inline map entries in a different order")
}

func TestEncodedCompareFailsOnReorderedFieldsBeforeAnInlineMap(t *testing.T) {
	tagged := taggedWithExtras{Name: "n", Kind: "k", Extras: map[string]any{"e1": int32(1)}}
	vendorBytes := vendorMarshal(t, tagged)

	swappedFields := vendorMarshal(t, vendorprimitive.D{
		{Key: "kind", Value: "k"}, {Key: "name", Value: "n"}, {Key: "e1", Value: int32(1)},
	})
	recorder := newFailureRecorder(t)
	compare.Encoded(recorder, tagged, vendorBytes, swappedFields)
	requireReported(t, recorder, "$")
}

// The vendor writes every field before the first inline entry; an entry
// among the fields is a difference even though each part, taken alone, would
// compare equal.
func TestEncodedCompareFailsOnAnInlineEntryAmongTheFields(t *testing.T) {
	tagged := taggedWithExtras{Name: "n", Kind: "k", Extras: map[string]any{"e1": int32(1)}}
	vendorBytes := vendorMarshal(t, tagged)

	interleaved := vendorMarshal(t, vendorprimitive.D{
		{Key: "name", Value: "n"}, {Key: "e1", Value: int32(1)}, {Key: "kind", Value: "k"},
	})
	recorder := newFailureRecorder(t)
	compare.Encoded(recorder, tagged, vendorBytes, interleaved)
	requireReported(t, recorder, "$")
}

func TestEncodedCompareFailsOnMalformedLibraryBytes(t *testing.T) {
	person := orderedPerson{Name: "Ada"}
	vendorBytes := vendorMarshal(t, person)
	truncated := vendorBytes[:len(vendorBytes)-3]
	recorder := newFailureRecorder(t)
	compare.Encoded(recorder, person, vendorBytes, truncated)
	requireReported(t, recorder, "library")
}

// decodedIdentityControl runs compare.Decoded on two equal values built
// separately, which must never report.
func decodedIdentityControl(t *testing.T, build func() any) {
	t.Helper()
	recorder := newFailureRecorder(t)
	compare.Decoded(recorder, build(), build())
	requireSilent(t, recorder, "equal decoded values")
}

func TestDecodedCompareFailsOnMapVersusPrimitiveM(t *testing.T) {
	// D2/D3: an embedded document decodes to map[string]interface{} in one
	// target and to primitive.M in another. The two print the same.
	buildVendor := func() any {
		return map[string]any{"outer": map[string]any{"inner": int32(1)}}
	}
	decodedIdentityControl(t, buildVendor)

	library := map[string]any{"outer": vendorprimitive.M{"inner": int32(1)}}
	recorder := newFailureRecorder(t)
	compare.Decoded(recorder, buildVendor(), library)
	requireReported(t, recorder, `$["outer"]`)
}

func TestDecodedCompareFailsOnNilVersusEmptySlice(t *testing.T) {
	type holder struct{ Tags []any }
	buildVendor := func() any { return holder{Tags: nil} }
	decodedIdentityControl(t, buildVendor)

	recorder := newFailureRecorder(t)
	compare.Decoded(recorder, buildVendor(), holder{Tags: []any{}})
	requireReported(t, recorder, "$.Tags")
}
