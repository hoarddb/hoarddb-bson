// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package corpus_test

import (
	"strings"
	"testing"

	"github.com/hoarddb/hoarddb-bson/oracle/internal/corpus"
)

// The corpus directory holds 32 entries: 31 JSON case files and bsonview, a
// Perl script the vendor ships beside them. The build doc and spec call this
// "32 files"; the loader reads the 31 JSON files and the test pins both counts.
func TestBSONCorpusLoadsAll32Files(t *testing.T) {
	corpusFiles, err := corpus.Load()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	if len(corpusFiles) != 31 {
		t.Errorf("parsed %d corpus files, want 31 JSON files", len(corpusFiles))
	}
	entryNames, err := corpus.EntryNames()
	if err != nil {
		t.Fatalf("listing the corpus directory: %v", err)
	}
	if len(entryNames) != 32 {
		t.Errorf("the corpus directory has %d entries, want 32 (31 JSON files and bsonview): %v", len(entryNames), entryNames)
	}
	for _, entryName := range entryNames {
		if !strings.HasSuffix(entryName, ".json") && entryName != "bsonview" {
			t.Errorf("unexpected corpus directory entry %q", entryName)
		}
	}

	var validCases, decodeErrorCases, parseErrorCases int
	for _, corpusFile := range corpusFiles {
		validCases += len(corpusFile.Valid)
		decodeErrorCases += len(corpusFile.DecodeErrors)
		parseErrorCases += len(corpusFile.ParseErrors)
	}
	t.Logf("corpus: %d files, %d valid cases, %d decode-error cases, %d parse-error cases", len(corpusFiles), validCases, decodeErrorCases, parseErrorCases)
	if validCases == 0 || decodeErrorCases == 0 || parseErrorCases == 0 {
		t.Errorf("a case list is empty: valid %d, decodeErrors %d, parseErrors %d", validCases, decodeErrorCases, parseErrorCases)
	}
}

func TestCorpusCasesCarryDecodedBytes(t *testing.T) {
	corpusFiles, err := corpus.Load()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	for _, corpusFile := range corpusFiles {
		for caseIndex, validCase := range corpusFile.Valid {
			if len(validCase.CanonicalBSON) < 5 {
				t.Errorf("%s valid[%d] %q: canonical_bson has %d bytes", corpusFile.Name, caseIndex, validCase.Description, len(validCase.CanonicalBSON))
			}
		}
		for caseIndex, decodeError := range corpusFile.DecodeErrors {
			if decodeError.BSON == nil {
				t.Errorf("%s decodeErrors[%d] %q: no bytes", corpusFile.Name, caseIndex, decodeError.Description)
			}
		}
	}
}
