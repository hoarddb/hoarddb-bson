// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"

	"github.com/hoarddb/hoarddb-bson/internal/extjson"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/corpus"
)

// The Extended JSON reader's core (build doc step 5b, the parser half) turns
// text into BSON bytes. Its oracle is the vendor's UnmarshalExtJSON into a
// bson.Raw, the raw-document target the inventory lists
// (driver:internal/codec/extjson.go:FromExtendedJSON). The other targets
// (*map[string]interface{}, *bson.M, *bson.D) are added to these tests by
// the step that adds the Go-value half (docs/core-api.md, "Step 5b core").

// extJSONOutcome is what one call produced: the bytes it left in the target
// and "ok", the error text, or "panic: ..." when it panicked. The vendor's
// raw target keeps the bytes written before an error, so they are compared
// on refusals too.
type extJSONOutcome struct {
	documentBytes []byte
	outcome       string
}

// vendorRawExtJSON runs the vendor's UnmarshalExtJSON into a bson.Raw.
func vendorRawExtJSON(text []byte, canonical bool) extJSONOutcome {
	var rawTarget vendorbson.Raw
	outcome := vendorOutcome(func() error { return vendorbson.UnmarshalExtJSON(text, canonical, &rawTarget) })
	return extJSONOutcome{documentBytes: rawTarget, outcome: outcome}
}

// libraryRawExtJSON runs the library's core. A panic is reported as an
// outcome, which no vendor outcome equals unless the vendor panicked too.
func libraryRawExtJSON(text []byte, canonical bool) (result extJSONOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result.outcome = fmt.Sprintf("panic: %v", recovered)
		}
	}()
	documentBytes, err := extjson.AppendDocument(nil, text, canonical)
	result.documentBytes = documentBytes
	if err != nil {
		result.outcome = err.Error()
		return result
	}
	result.outcome = "ok"
	return result
}

// extJSONDivergence compares the library with the vendor on one input and
// mode, and describes the first difference, or returns "".
func extJSONDivergence(text []byte, canonical bool) string {
	vendorResult := vendorRawExtJSON(text, canonical)
	libraryResult := libraryRawExtJSON(text, canonical)
	if vendorResult.outcome != libraryResult.outcome {
		return fmt.Sprintf("canonical=%v: vendor %q, library %q", canonical, vendorResult.outcome, libraryResult.outcome)
	}
	if !bytes.Equal(vendorResult.documentBytes, libraryResult.documentBytes) {
		return fmt.Sprintf("canonical=%v, both %q: vendor bytes % x, library bytes % x",
			canonical, vendorResult.outcome, vendorResult.documentBytes, libraryResult.documentBytes)
	}
	return ""
}

// corpusExtJSONText is one Extended JSON text of a corpus case.
type corpusExtJSONText struct {
	label string
	text  string
}

// decimal128CorpusType is the bson_type of the Decimal128 corpus files.
const decimal128CorpusType = "0x13"

// corpusExtJSONTexts lists every Extended JSON text a corpus file holds:
// each valid case's canonical, relaxed, degenerate and converted forms, and
// each parse-error string.
func corpusExtJSONTexts(corpusFile corpus.File) []corpusExtJSONText {
	var texts []corpusExtJSONText
	for caseIndex, validCase := range corpusFile.Valid {
		prefix := fmt.Sprintf("valid[%d] %s", caseIndex, validCase.Description)
		texts = append(texts, corpusExtJSONText{label: prefix + " canonical_extjson", text: validCase.CanonicalExtJSON})
		optional := []struct {
			name string
			text *string
		}{
			{"relaxed_extjson", validCase.RelaxedExtJSON},
			{"degenerate_extjson", validCase.DegenerateExtJSON},
			{"converted_extjson", validCase.ConvertedExtJSON},
		}
		for _, form := range optional {
			if form.text != nil {
				texts = append(texts, corpusExtJSONText{label: prefix + " " + form.name, text: *form.text})
			}
		}
	}
	for caseIndex, parseError := range corpusFile.ParseErrors {
		label := fmt.Sprintf("parseErrors[%d] %s", caseIndex, parseError.Description)
		texts = append(texts, corpusExtJSONText{label: label, text: parseError.String})
		// A Decimal128 file's parse errors are decimal strings, not JSON, so
		// they are also read the way the corpus means them: inside a
		// $numberDecimal wrapper.
		if corpusFile.BSONType == decimal128CorpusType {
			quoted, err := json.Marshal(parseError.String)
			if err == nil {
				texts = append(texts, corpusExtJSONText{
					label: label + " in $numberDecimal",
					text:  `{"d": {"$numberDecimal": ` + string(quoted) + `}}`,
				})
			}
		}
	}
	return texts
}

// TestUnmarshalExtJSONMatchesVendorOnTheCorpus runs every Extended JSON text
// of the corpus, in canonical and relaxed mode, through the library and the
// vendor: the same acceptance, the same error text, the same bytes. Each
// case is judged against the vendor's outcome, not the corpus's expectation
// (spec 9.1.4). The per-file tally is logged for the step report.
func TestUnmarshalExtJSONMatchesVendorOnTheCorpus(t *testing.T) {
	corpusFiles, err := corpus.Load()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	var tally []string
	var totalRuns, totalMatches int
	for _, corpusFile := range corpusFiles {
		texts := corpusExtJSONTexts(corpusFile)
		var runs, matches, accepted int
		for _, corpusText := range texts {
			for _, canonical := range []bool{true, false} {
				runs++
				if vendorRawExtJSON([]byte(corpusText.text), canonical).outcome == "ok" {
					accepted++
				}
				if divergence := extJSONDivergence([]byte(corpusText.text), canonical); divergence != "" {
					t.Errorf("%s %s: %s\n  input %q", corpusFile.Name, corpusText.label, divergence, corpusText.text)
					continue
				}
				matches++
			}
		}
		tally = append(tally, fmt.Sprintf("%-28s texts %3d  runs %3d  vendor-accepted %3d  matches %3d",
			corpusFile.Name, len(texts), runs, accepted, matches))
		totalRuns += runs
		totalMatches += matches
	}
	sort.Strings(tally)
	t.Logf("corpus Extended JSON, both modes:\n%s\ntotal runs %d, matches %d", strings.Join(tally, "\n"), totalRuns, totalMatches)
	if totalRuns == 0 {
		t.Fatal("the corpus produced no Extended JSON text")
	}
}

// nestedObjects is depth levels of {"a": ...} around an empty document.
func nestedObjects(depth int) []byte {
	return []byte(strings.Repeat(`{"a":`, depth-1) + "{}" + strings.Repeat("}", depth-1))
}

// TestUnmarshalExtJSONRefusesPastTheCeiling pins the vendor's ceiling of 200
// nested objects (the root is level 1, bson.MaxNestingCeiling) and its
// error text. Arrays do not count toward it in the vendor's parser, and they
// do not here: 300 nested arrays inside one object are read.
func TestUnmarshalExtJSONRefusesPastTheCeiling(t *testing.T) {
	cases := []struct {
		name   string
		text   []byte
		accept bool
	}{
		{"200 objects", nestedObjects(200), true},
		{"201 objects", nestedObjects(201), false},
		{"300 objects", nestedObjects(300), false},
		{"199 objects and a wrapper", []byte(strings.Repeat(`{"a":`, 199) + `{"$numberInt":"1"}` + strings.Repeat("}", 199)), true},
		{"200 objects and a wrapper", []byte(strings.Repeat(`{"a":`, 200) + `{"$numberInt":"1"}` + strings.Repeat("}", 200)), false},
		{"300 arrays", []byte(`{"a":` + strings.Repeat("[", 300) + strings.Repeat("]", 300) + "}"), true},
		{"objects inside arrays", []byte(`{"a":` + strings.Repeat(`[{"b":`, 200) + "1" + strings.Repeat("}]", 200) + "}"), false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			for _, canonical := range []bool{true, false} {
				if divergence := extJSONDivergence(testCase.text, canonical); divergence != "" {
					t.Fatal(divergence)
				}
				libraryResult := libraryRawExtJSON(testCase.text, canonical)
				if accepted := libraryResult.outcome == "ok"; accepted != testCase.accept {
					t.Fatalf("canonical=%v: accepted %v, want %v: %s", canonical, accepted, testCase.accept, libraryResult.outcome)
				}
				if !testCase.accept && !strings.Contains(libraryResult.outcome, "nesting too deep (201 levels)") {
					t.Fatalf("canonical=%v: refused with %q, want the nesting-depth error at level 201", canonical, libraryResult.outcome)
				}
			}
		})
	}
}
