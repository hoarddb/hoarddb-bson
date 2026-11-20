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

// FuzzUnmarshalExtJSONGoValues runs random text through bson.UnmarshalExtJSON
// and the vendor's into the Go-value targets, in the mode the fuzzer picks:
// the same outcome, error text and decoded value, the partly filled target
// included. It is seeded from every corpus Extended JSON text, the error
// table's inputs and the Go-value texts.
func FuzzUnmarshalExtJSONGoValues(f *testing.F) {
	corpusFiles, err := corpus.Load()
	if err != nil {
		f.Fatalf("loading the corpus: %v", err)
	}
	var seeds []string
	for _, corpusFile := range corpusFiles {
		for _, corpusText := range corpusExtJSONTexts(corpusFile) {
			seeds = append(seeds, corpusText.text)
		}
	}
	for _, input := range extJSONErrorInputs {
		seeds = append(seeds, input.text)
	}
	seeds = append(seeds, extJSONGoValueTexts()...)
	if len(seeds) == 0 {
		f.Fatal("FuzzUnmarshalExtJSONGoValues has no seeds")
	}
	for position, seed := range seeds {
		f.Add([]byte(seed), position%2 == 0)
	}
	targets := []decTarget{
		decTargetMap, decTargetM, decTargetD, decTargetInterface, decTargetRawValue,
		decTarget{"*extJSONByteFields", func() any { return &extJSONByteFields{} }, func() any { return &extJSONLibByteFields{} }},
		decTarget{"*extJSONTyped", func() any { return &extJSONTyped{} }, func() any { return &extJSONLibTyped{} }},
		decTarget{"*decHookFields", func() any { return &decHookFields{} }, func() any { return &decLibHookFields{} }},
		decSame("*decCorpusInterfaces", decZero[decCorpusInterfaces]),
		decSame("*[]interface{}", decZero[[]any]),
	}
	f.Fuzz(func(t *testing.T, text []byte, canonical bool) {
		for _, target := range targets {
			if divergence := extJSONReadDivergence(text, canonical, target); divergence != "" {
				t.Fatalf("input %q, canonical=%v: %s", text, canonical, divergence)
			}
		}
	})
}
