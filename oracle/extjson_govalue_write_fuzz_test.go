// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
)

// FuzzMarshalExtJSONGoValues decodes random bytes to Go values with the
// vendor (bson.D, bson.M and a struct of builtins), then writes each with
// bson.MarshalExtJSON and the vendor's, in the mode the fuzzer picks: the
// same error text, or texts compare.ExtJSON finds equal. It is seeded from
// the encode fuzz target's seeds, the reader probes and the escape
// documents.
func FuzzMarshalExtJSONGoValues(f *testing.F) {
	seeds := append(append(append(seedsOf(f, "FuzzEncodeDifferential"), baseDocuments()...), readerProbes()...), escapeDocuments()...)
	if len(seeds) == 0 {
		f.Fatal("FuzzMarshalExtJSONGoValues has no seeds")
	}
	for position, seed := range seeds {
		f.Add(seed, position%2 == 0, position%3 == 0)
	}
	f.Fuzz(func(t *testing.T, documentBytes []byte, canonical, escapeHTML bool) {
		mode := extJSONMode{canonical: canonical, escapeHTML: escapeHTML}
		var cases []encodeCase
		var vendorDocument vendorbson.D
		if vendorOutcome(func() error { return vendorbson.Unmarshal(documentBytes, &vendorDocument) }) == "ok" {
			cases = append(cases, twinValues("bson.D", libraryTwin(t, vendorDocument), vendorDocument))
		}
		var vendorMap vendorbson.M
		if vendorOutcome(func() error { return vendorbson.Unmarshal(documentBytes, &vendorMap) }) == "ok" {
			cases = append(cases, twinValues("bson.M", libraryTwin(t, vendorMap), vendorMap))
		}
		var shape fuzzEncodeShape
		if vendorOutcome(func() error { return vendorbson.Unmarshal(documentBytes, &shape) }) == "ok" {
			cases = append(cases, sameValue("struct", shape), sameValue("struct pointer", &shape))
		}
		cases = append(cases, twinValues("bson.Raw", libraryTwin(t, vendorbson.Raw(documentBytes)), vendorbson.Raw(documentBytes)),
			twinValues("bson.Raw in a map", map[string]any{"raw": libraryTwin(t, vendorbson.Raw(documentBytes))}, map[string]any{"raw": vendorbson.Raw(documentBytes)}))
		for _, encodeInput := range cases {
			if divergence := extJSONWriteDivergence(encodeInput, mode); divergence != "" {
				t.Fatalf("document %x as %s, %s: %s", documentBytes, encodeInput.name, mode, divergence)
			}
		}
	})
}
