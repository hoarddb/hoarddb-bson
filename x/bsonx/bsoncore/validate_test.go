// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bsoncore

import (
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// TestArrayAndDocumentValidateRefuseShortLengths: a declared length under the
// five-byte minimum is an error; it once indexed the slice at length-1.
func TestArrayAndDocumentValidateRefuseShortLengths(t *testing.T) {
	for _, declared := range []byte{0, 1, 2, 3, 4} {
		short := []byte{declared, 0, 0, 0, 0}
		if err := Array(short).Validate(); err == nil {
			t.Errorf("Array of declared length %d validated", declared)
		}
		if err := Document(short).Validate(); err == nil {
			t.Errorf("Document of declared length %d validated", declared)
		}
	}
}

// TestValueLengthDoesNotWrap: a declared length near the int32 maximum plus
// the header bytes must stay a large positive length, not wrap negative.
func TestValueLengthDoesNotWrap(t *testing.T) {
	src := []byte{0xff, 0xff, 0xff, 0x7f}
	for _, valueType := range []bsontype.Type{bsontype.Binary, bsontype.String, bsontype.DBPointer} {
		length, ok := valueLength(src, valueType)
		if !ok || length <= 0x7fffffff {
			t.Errorf("type 0x%02x: length %d ok=%v wrapped", byte(valueType), length, ok)
		}
	}
}

// TestBooleanElementRoundTrips: AppendBooleanElement writes type 0x08, the key
// and one byte, and ReadBoolean reads it back and leaves what follows.
func TestBooleanElementRoundTrips(t *testing.T) {
	for _, flag := range []bool{false, true} {
		element := AppendBooleanElement(nil, "ok", flag)
		want := []byte{0x08, 'o', 'k', 0, 0}
		if flag {
			want[4] = 1
		}
		if string(element) != string(want) {
			t.Fatalf("AppendBooleanElement(%t) = %x, want %x", flag, element, want)
		}
		got, rem, ok := ReadBoolean(append(want[4:], 0xAA))
		if !ok || got != flag || len(rem) != 1 || rem[0] != 0xAA {
			t.Fatalf("ReadBoolean = %t, %x, %t", got, rem, ok)
		}
	}
	if _, rem, ok := ReadBoolean(nil); ok || len(rem) != 0 {
		t.Fatal("ReadBoolean of no bytes reported ok")
	}
}
