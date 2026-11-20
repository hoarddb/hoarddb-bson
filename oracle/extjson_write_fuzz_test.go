// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import "testing"

// FuzzExtJSONWriterDifferential runs random bytes through the library's
// Extended JSON writer and the vendor's MarshalExtJSON of a bson.Raw, in
// the mode the fuzzer picks, and through RawElement.String as an element:
// the same text and the same error text, or the nesting refusal where the
// vendor's walk opens a level past the ceiling. It is seeded from every
// byte seed of the other targets, the reader probes and the escape
// documents.
func FuzzExtJSONWriterDifferential(f *testing.F) {
	seeds, err := allSeeds()
	if err != nil {
		f.Fatalf("loading the seeds: %v", err)
	}
	seeds = append(append(append(seeds, baseDocuments()...), readerProbes()...), escapeDocuments()...)
	if len(seeds) == 0 {
		f.Fatal("FuzzExtJSONWriterDifferential has no seeds")
	}
	for position, seed := range seeds {
		f.Add(seed, position%2 == 0, position%3 == 0)
	}
	f.Fuzz(func(t *testing.T, input []byte, canonical, escapeHTML bool) {
		if divergence := writeDivergence(input, canonical, escapeHTML); divergence != "" {
			t.Fatalf("document %x, %s: %s", input, modeLabel(canonical, escapeHTML), divergence)
		}
		if divergence := elementDivergence(input); divergence != "" {
			t.Fatalf("element %x: %s", input, divergence)
		}
	})
}
