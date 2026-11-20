// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"testing"

	"github.com/hoarddb/hoarddb-bson/oracle/internal/corpus"
)

// FuzzExtJSONDifferential runs random text through the library's Extended
// JSON core and the vendor's UnmarshalExtJSON into a bson.Raw, in the mode
// the fuzzer picks: the same acceptance, the same error text, the same
// bytes. It is seeded from every corpus Extended JSON text and the error
// table's inputs.
func FuzzExtJSONDifferential(f *testing.F) {
	corpusFiles, err := corpus.Load()
	if err != nil {
		f.Fatalf("loading the corpus: %v", err)
	}
	var seeds int
	for _, corpusFile := range corpusFiles {
		for _, corpusText := range corpusExtJSONTexts(corpusFile) {
			f.Add([]byte(corpusText.text), true)
			f.Add([]byte(corpusText.text), false)
			seeds += 2
		}
	}
	for _, input := range extJSONErrorInputs {
		f.Add([]byte(input.text), true)
		f.Add([]byte(input.text), false)
		seeds += 2
	}
	if seeds == 0 {
		f.Fatal("FuzzExtJSONDifferential has no seeds")
	}
	f.Fuzz(func(t *testing.T, text []byte, canonical bool) {
		if divergence := extJSONDivergence(text, canonical); divergence != "" {
			t.Fatalf("input %q: %s", text, divergence)
		}
	})
}
