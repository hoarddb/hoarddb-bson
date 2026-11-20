// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// mutationInput is one raw value or element handed to Validate.
type mutationInput struct {
	valueType bsontype.Type
	body      []byte
}

// validateWithoutPanic runs every Validate on the input and reports a panic
// as an error, so the test can print the bytes that caused it.
func validateWithoutPanic(input mutationInput) (panicked any, rawErr, elemErr error) {
	defer func() { panicked = recover() }()
	rawErr = bson.RawValue{Type: input.valueType, Value: input.body}.Validate()
	element := append([]byte{byte(input.valueType), 'k', 0}, input.body...)
	elemErr = bson.RawElement(element).Validate()
	// The whole-document and array forms see the same bytes as one element.
	document := make([]byte, 4, 4+len(element)+1)
	document = append(append(document, element...), 0)
	binary.LittleEndian.PutUint32(document, uint32(len(document)))
	_ = bson.Raw(document).Validate()
	_ = bson.RawValue{Type: input.valueType, Value: input.body}.String()
	return nil, rawErr, elemErr
}

func lengthPrefixed(payload []byte) []byte {
	out := make([]byte, 4, 4+len(payload))
	binary.LittleEndian.PutUint32(out, uint32(len(payload)))
	return append(out, payload...)
}

func mutationSeeds() []mutationInput {
	emptyDocument := []byte{5, 0, 0, 0, 0}
	nestedDocument := documentOf(elementOf(0x02, "a", stringValueOf("x")))
	binaryValue := []byte{3, 0, 0, 0, 0, 1, 2, 3}
	codeWithScope := lengthPrefixed(append(stringValueOf("c"), emptyDocument...))
	oldBinary := []byte{7, 0, 0, 0, 2, 3, 0, 0, 0, 9, 9, 9}
	return []mutationInput{
		{bsontype.String, stringValueOf("hello")},
		{bsontype.String, stringValueOf("")},
		{bsontype.JavaScript, stringValueOf("f()")},
		{bsontype.Symbol, stringValueOf("s")},
		{bsontype.Binary, binaryValue},
		{bsontype.Binary, oldBinary},
		{bsontype.EmbeddedDocument, emptyDocument},
		{bsontype.EmbeddedDocument, nestedDocument},
		{bsontype.Array, documentOf(elementOf(0x02, "0", stringValueOf("x")))},
		{bsontype.DBPointer, append(stringValueOf("ns"), make([]byte, 12)...)},
		{bsontype.CodeWithScope, codeWithScope},
		{bsontype.Regex, []byte("a\x00i\x00")},
		{bsontype.Int32, make([]byte, 4)},
		{bsontype.Int64, make([]byte, 8)},
		{bsontype.Double, make([]byte, 8)},
		{bsontype.Boolean, []byte{1}},
		{bsontype.ObjectID, make([]byte, 12)},
		{bsontype.Decimal128, make([]byte, 16)},
		{bsontype.Timestamp, make([]byte, 8)},
		{bsontype.Null, nil},
	}
}

func mutateBytes(random *rand.Rand, original []byte) []byte {
	out := append([]byte(nil), original...)
	for round := random.Intn(3) + 1; round > 0; round-- {
		switch random.Intn(6) {
		case 0:
			if len(out) > 0 {
				out[random.Intn(len(out))] = byte(random.Intn(256))
			}
		case 1:
			if len(out) > 0 {
				out = out[:random.Intn(len(out))]
			}
		case 2:
			out = append(out, byte(random.Intn(256)))
		case 3:
			if len(out) >= 4 {
				// Stamp an interesting length over the first four bytes.
				interesting := []uint32{0, 1, 2, 4, 5, 0x7fffffff, 0x7ffffffb, 0x7ffffffc, 0x80000000, 0xffffffff, uint32(len(out)), uint32(len(out)) + 1}
				binary.LittleEndian.PutUint32(out, interesting[random.Intn(len(interesting))])
			}
		case 4:
			if len(out) > 0 {
				out[random.Intn(len(out))] ^= 1 << uint(random.Intn(8))
			}
		default:
			out = append(out, out...)
		}
	}
	return out
}

// TestValidateNeverPanicsOnMutatedValues: Validate on a raw value, element
// or document is handed untrusted client bytes, so it returns an error and
// never panics. A value it accepts validates again, to the same answer.
func TestValidateNeverPanicsOnMutatedValues(t *testing.T) {
	random := rand.New(rand.NewSource(20261031))
	seeds := mutationSeeds()
	accepted := 0
	for iteration := 0; iteration < 100000; iteration++ {
		seed := seeds[random.Intn(len(seeds))]
		input := mutationInput{valueType: seed.valueType, body: mutateBytes(random, seed.body)}
		if random.Intn(20) == 0 {
			input.valueType = bsontype.Type(random.Intn(256))
		}
		panicked, rawErr, elemErr := validateWithoutPanic(input)
		if panicked != nil {
			t.Fatalf("Validate panicked on type 0x%02x bytes %x: %v", byte(input.valueType), input.body, panicked)
		}
		if rawErr != nil {
			continue
		}
		accepted++
		panickedAgain, again, againElem := validateWithoutPanic(input)
		if panickedAgain != nil || again != nil || fmt.Sprint(againElem) != fmt.Sprint(elemErr) {
			t.Fatalf("accepted value no longer validates: type 0x%02x bytes %x: %v / %v", byte(input.valueType), input.body, again, panickedAgain)
		}
	}
	if accepted == 0 {
		t.Fatal("no mutation was accepted, so the re-validation half of the test checked nothing")
	}
}

// TestValidateNeverPanicsOnMutatedDocuments: the same promise for a document
// or array whose own length prefix is mutated, and for a string, binary or
// code-with-scope whose declared length lies.
func TestValidateNeverPanicsOnMutatedDocuments(t *testing.T) {
	random := rand.New(rand.NewSource(20261032))
	seeds := [][]byte{
		documentOf(),
		documentOf(elementOf(0x02, "a", stringValueOf("x"))),
		documentOf(elementOf(0x05, "b", []byte{3, 0, 0, 0, 0, 1, 2, 3})),
		documentOf(elementOf(0x04, "c", documentOf(elementOf(0x10, "0", []byte{1, 0, 0, 0})))),
		documentOf(elementOf(0x03, "d", documentOf(elementOf(0x08, "e", []byte{1})))),
	}
	for iteration := 0; iteration < 100000; iteration++ {
		raw := mutateBytes(random, seeds[random.Intn(len(seeds))])
		func() {
			defer func() {
				if panicked := recover(); panicked != nil {
					t.Fatalf("Validate panicked on %x: %v", raw, panicked)
				}
			}()
			if err := bson.Raw(raw).Validate(); err != nil {
				return
			}
			if err := bson.Raw(raw).Validate(); err != nil {
				t.Fatalf("accepted document no longer validates: %x: %v", raw, err)
			}
			_ = bson.Raw(raw).String()
			_ = bson.RawValue{Type: bsontype.Array, Value: raw}.Validate()
			_, _ = bson.Raw(raw).Elements()
		}()
	}
}

// TestRawValueValidateRefusesLyingLengths: a declared length that disagrees
// with the bytes present is an error, not a panic and not a pass.
func TestRawValueValidateRefusesLyingLengths(t *testing.T) {
	cases := map[string]bson.RawValue{
		"string longer than the bytes":  {Type: bsontype.String, Value: []byte{9, 0, 0, 0, 'a', 0}},
		"string of length zero":         {Type: bsontype.String, Value: []byte{0, 0, 0, 0}},
		"string of negative length":     {Type: bsontype.String, Value: []byte{0xff, 0xff, 0xff, 0xff, 0}},
		"string of maximum length":      {Type: bsontype.String, Value: []byte{0xff, 0xff, 0xff, 0x7f, 0}},
		"string without its terminator": {Type: bsontype.String, Value: []byte{2, 0, 0, 0, 'a', 'b'}},
		"binary longer than the bytes":  {Type: bsontype.Binary, Value: []byte{9, 0, 0, 0, 0, 1}},
		"binary of negative length":     {Type: bsontype.Binary, Value: []byte{0xff, 0xff, 0xff, 0xff, 0, 1}},
		"binary of maximum length":      {Type: bsontype.Binary, Value: []byte{0xff, 0xff, 0xff, 0x7f, 0, 1}},
		"document of length zero":       {Type: bsontype.EmbeddedDocument, Value: []byte{0, 0, 0, 0, 0}},
		"document of length four":       {Type: bsontype.EmbeddedDocument, Value: []byte{4, 0, 0, 0, 0}},
		"document of negative length":   {Type: bsontype.EmbeddedDocument, Value: []byte{0xff, 0xff, 0xff, 0xff, 0}},
		"document without a terminator": {Type: bsontype.EmbeddedDocument, Value: []byte{5, 0, 0, 0, 1}},
		"array of length zero":          {Type: bsontype.Array, Value: []byte{0, 0, 0, 0, 0}},
		"code with scope too short":     {Type: bsontype.CodeWithScope, Value: []byte{4, 0, 0, 0}},
		"dbpointer with a short string": {Type: bsontype.DBPointer, Value: []byte{0, 0, 0, 0}},
		"javascript of length zero":     {Type: bsontype.JavaScript, Value: []byte{0, 0, 0, 0}},
		"symbol without its terminator": {Type: bsontype.Symbol, Value: []byte{1, 0, 0, 0, 'x'}},
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if panicked := recover(); panicked != nil {
					t.Fatalf("Validate panicked: %v", panicked)
				}
			}()
			if err := value.Validate(); err == nil {
				t.Fatalf("Validate accepted %x", value.Value)
			}
		})
	}
}
