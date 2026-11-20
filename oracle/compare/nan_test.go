// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package compare_test

import (
	"math"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/hoarddb/hoarddb-bson/oracle/compare"
)

// nanHolder is a decode target with a typed float and an interface{} holder,
// the two places a double lands in a tagged struct.
type nanHolder struct {
	Typed  float64 `bson:"typed"`
	Holder any     `bson:"holder"`
}

// nanDocumentBytes is {"typed": NaN, "holder": NaN, "list": [NaN, 1.0],
// "inner": {"d": NaN}} with the quiet NaN bits the corpus's double.json uses.
func nanDocumentBytes(t *testing.T, nanBits uint64) []byte {
	t.Helper()
	nan := math.Float64frombits(nanBits)
	return vendorMarshal(t, vendorprimitive.D{
		{Key: "typed", Value: nan},
		{Key: "holder", Value: nan},
		{Key: "list", Value: vendorprimitive.A{nan, 1.0}},
		{Key: "inner", Value: vendorprimitive.D{{Key: "d", Value: nan}}},
	})
}

const quietNaNBits = 0x7FF8000000000000

// decodeTargets returns two independent decodes of the same bytes into every
// target type the oracle shim uses, so the pair holds identical values at
// different addresses.
func decodeTargets(t *testing.T, documentBytes []byte) map[string][2]any {
	t.Helper()
	decodes := map[string][2]any{}
	for _, name := range []string{"bson.D", "bson.M", "map[string]interface{}", "struct", "interface{}"} {
		var pair [2]any
		for side := range pair {
			var err error
			switch name {
			case "bson.D":
				var target vendorbson.D
				err = vendorbson.Unmarshal(documentBytes, &target)
				pair[side] = target
			case "bson.M":
				var target vendorbson.M
				err = vendorbson.Unmarshal(documentBytes, &target)
				pair[side] = target
			case "map[string]interface{}":
				var target map[string]any
				err = vendorbson.Unmarshal(documentBytes, &target)
				pair[side] = target
			case "struct":
				var target nanHolder
				err = vendorbson.Unmarshal(documentBytes, &target)
				pair[side] = target
			case "interface{}":
				var target any
				err = vendorbson.Unmarshal(documentBytes, &target)
				pair[side] = target
			}
			if err != nil {
				t.Fatalf("vendor Unmarshal into %s: %v", name, err)
			}
		}
		decodes[name] = pair
	}
	return decodes
}

func TestDecodedAcceptsIdenticalNaNDecodes(t *testing.T) {
	documentBytes := nanDocumentBytes(t, quietNaNBits)
	for targetName, pair := range decodeTargets(t, documentBytes) {
		t.Run(targetName, func(t *testing.T) {
			recorder := newFailureRecorder(t)
			compare.Decoded(recorder, pair[0], pair[1])
			requireSilent(t, recorder, "two identical NaN decodes into "+targetName)
		})
	}
}

func TestDecodedAcceptsIdenticalTopLevelNaN(t *testing.T) {
	nan := math.Float64frombits(quietNaNBits)
	recorder := newFailureRecorder(t)
	compare.Decoded(recorder, nan, math.Float64frombits(quietNaNBits))
	requireSilent(t, recorder, "two identical NaN float64 values")
	recorder = newFailureRecorder(t)
	compare.Decoded(recorder, float32(nan), float32(nan))
	requireSilent(t, recorder, "two identical NaN float32 values")
}

func TestDecodedStillRejectsNaNAgainstANumber(t *testing.T) {
	nanBytes := nanDocumentBytes(t, quietNaNBits)
	var nanDecoded, numberDecoded vendorbson.D
	if err := vendorbson.Unmarshal(nanBytes, &nanDecoded); err != nil {
		t.Fatalf("vendor Unmarshal: %v", err)
	}
	numberDecoded = append(vendorbson.D(nil), nanDecoded...)
	numberDecoded[0].Value = 1.0

	for _, swapped := range [][2]any{{nanDecoded, numberDecoded}, {numberDecoded, nanDecoded}} {
		recorder := newFailureRecorder(t)
		compare.Decoded(recorder, swapped[0], swapped[1])
		requireReported(t, recorder, "$[0]")
	}

	// A NaN nested in an array against a number must be located too.
	var nestedDecoded vendorbson.M
	if err := vendorbson.Unmarshal(nanBytes, &nestedDecoded); err != nil {
		t.Fatalf("vendor Unmarshal: %v", err)
	}
	var changedDecoded vendorbson.M
	if err := vendorbson.Unmarshal(nanBytes, &changedDecoded); err != nil {
		t.Fatalf("vendor Unmarshal: %v", err)
	}
	changedDecoded["list"].(vendorbson.A)[0] = 0.0
	recorder := newFailureRecorder(t)
	compare.Decoded(recorder, nestedDecoded, changedDecoded)
	requireReported(t, recorder, `["list"][0]`)
}

// A NaN with a different payload encodes to different bytes, so a library
// that rewrote the payload would write other bytes than the vendor.
func TestDecodedStillRejectsADifferentNaNPayload(t *testing.T) {
	var quietDecoded, payloadDecoded vendorbson.D
	if err := vendorbson.Unmarshal(nanDocumentBytes(t, quietNaNBits), &quietDecoded); err != nil {
		t.Fatalf("vendor Unmarshal: %v", err)
	}
	if err := vendorbson.Unmarshal(nanDocumentBytes(t, quietNaNBits|0x1234), &payloadDecoded); err != nil {
		t.Fatalf("vendor Unmarshal: %v", err)
	}
	recorder := newFailureRecorder(t)
	compare.Decoded(recorder, quietDecoded, payloadDecoded)
	requireReported(t, recorder, "$[0]")
}
