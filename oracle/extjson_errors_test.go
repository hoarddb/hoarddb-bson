// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	vendorbson "go.mongodb.org/mongo-driver/bson"
)

// extJSONErrorTablePath is the vendor-generated error table (build doc
// step 5b, spec 9.1.5). It is the contract the library's error text is held
// to, and the step that adds the CLI-site test reads it from here.
const extJSONErrorTablePath = "testdata/extjson-error-table.json"

// updateExtJSONErrorTable rewrites the table from the vendor:
//
//	go test -run TestExtJSONErrorTextMatchesVendor -update-extjson-error-table
//
// A change to the table is a reviewed change: it means the input list or
// the pinned vendor changed.
var updateExtJSONErrorTable = flag.Bool("update-extjson-error-table", false,
	"rewrite "+extJSONErrorTablePath+" from the vendor")

// exactText is a string that survives JSON exactly. Valid UTF-8 is written
// as a JSON string. Anything else is written as {"hex": "..."}, because
// encoding/json replaces invalid UTF-8 with U+FFFD.
type exactText string

// MarshalJSON writes the text as a string, or as hex when it is not UTF-8.
func (text exactText) MarshalJSON() ([]byte, error) {
	if utf8.ValidString(string(text)) {
		return json.Marshal(string(text))
	}
	return json.Marshal(map[string]string{"hex": hex.EncodeToString([]byte(text))})
}

// UnmarshalJSON reads either form MarshalJSON writes.
func (text *exactText) UnmarshalJSON(jsonText []byte) error {
	var plain string
	if err := json.Unmarshal(jsonText, &plain); err == nil {
		*text = exactText(plain)
		return nil
	}
	var hexForm map[string]string
	if err := json.Unmarshal(jsonText, &hexForm); err != nil {
		return fmt.Errorf("reading an exact text: %w", err)
	}
	decoded, err := hex.DecodeString(hexForm["hex"])
	if err != nil {
		return fmt.Errorf("decoding an exact text's hex: %w", err)
	}
	*text = exactText(decoded)
	return nil
}

// targetOutcomes is the vendor's outcome per decode target the inventory
// lists for UnmarshalExtJSON: "ok", the error text, or "panic: ...".
type targetOutcomes struct {
	Raw        exactText `json:"raw"`
	Map        exactText `json:"map"`
	M          exactText `json:"M"`
	Ordered    exactText `json:"D"`
	SameAtEach bool      `json:"sameAtEveryTarget"`
}

// extJSONErrorRow is one row of the table.
type extJSONErrorRow struct {
	Name      string         `json:"name"`
	Input     exactText      `json:"input"`
	Canonical targetOutcomes `json:"canonical"`
	Relaxed   targetOutcomes `json:"relaxed"`
}

// vendorTargetOutcomes runs one input through the vendor into every target.
func vendorTargetOutcomes(text []byte, canonical bool) targetOutcomes {
	var mapTarget map[string]interface{}
	var unorderedTarget vendorbson.M
	var orderedTarget vendorbson.D
	outcomes := targetOutcomes{
		Raw:     exactText(vendorRawExtJSON(text, canonical).outcome),
		Map:     exactText(vendorOutcome(func() error { return vendorbson.UnmarshalExtJSON(text, canonical, &mapTarget) })),
		M:       exactText(vendorOutcome(func() error { return vendorbson.UnmarshalExtJSON(text, canonical, &unorderedTarget) })),
		Ordered: exactText(vendorOutcome(func() error { return vendorbson.UnmarshalExtJSON(text, canonical, &orderedTarget) })),
	}
	outcomes.SameAtEach = outcomes.Raw == outcomes.Map && outcomes.Map == outcomes.M && outcomes.M == outcomes.Ordered
	return outcomes
}

// generateExtJSONErrorTable is the table's generator: the input list run
// through the vendor.
func generateExtJSONErrorTable() []extJSONErrorRow {
	rows := make([]extJSONErrorRow, 0, len(extJSONErrorInputs))
	for _, input := range extJSONErrorInputs {
		rows = append(rows, extJSONErrorRow{
			Name:      input.name,
			Input:     exactText(input.text),
			Canonical: vendorTargetOutcomes([]byte(input.text), true),
			Relaxed:   vendorTargetOutcomes([]byte(input.text), false),
		})
	}
	return rows
}

func encodeExtJSONErrorTable(rows []extJSONErrorRow) ([]byte, error) {
	var tableBytes bytes.Buffer
	encoder := json.NewEncoder(&tableBytes)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(rows); err != nil {
		return nil, fmt.Errorf("encoding the Extended JSON error table: %w", err)
	}
	return tableBytes.Bytes(), nil
}

// loadExtJSONErrorTable reads the committed table, or rewrites it first
// under -update-extjson-error-table.
func loadExtJSONErrorTable(t *testing.T) []extJSONErrorRow {
	t.Helper()
	generatedBytes, err := encodeExtJSONErrorTable(generateExtJSONErrorTable())
	if err != nil {
		t.Fatal(err)
	}
	if *updateExtJSONErrorTable {
		if err := os.MkdirAll(filepath.Dir(extJSONErrorTablePath), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(extJSONErrorTablePath), err)
		}
		if err := os.WriteFile(extJSONErrorTablePath, generatedBytes, 0o644); err != nil {
			t.Fatalf("writing %s: %v", extJSONErrorTablePath, err)
		}
	}
	committedBytes, err := os.ReadFile(extJSONErrorTablePath)
	if err != nil {
		t.Fatalf("reading %s: %v. Generate it with: go test -run TestExtJSONErrorTextMatchesVendor -update-extjson-error-table",
			extJSONErrorTablePath, err)
	}
	if !bytes.Equal(committedBytes, generatedBytes) {
		t.Fatalf("%s is not what the vendor produces for the input list in extjson_error_inputs_test.go. "+
			"If the input list changed on purpose, regenerate it with: go test -run TestExtJSONErrorTextMatchesVendor -update-extjson-error-table",
			extJSONErrorTablePath)
	}
	var rows []extJSONErrorRow
	if err := json.Unmarshal(committedBytes, &rows); err != nil {
		t.Fatalf("parsing %s: %v", extJSONErrorTablePath, err)
	}
	return rows
}

// TestExtJSONErrorTextMatchesVendor holds the library's error text to the
// vendor-generated table, at the raw-document target, in both modes: the
// same acceptance and err.Error() exactly, and the same bytes (on a refusal,
// the bytes the vendor leaves in its bson.Raw). It first checks that the
// committed table is what the vendor generates now.
//
// The table records the vendor's outcome at every target the inventory
// lists. The rows where the targets disagree are the errors that come from
// decoding into a Go value or from writing bytes, not from parsing; they are
// logged for the step report.
func TestExtJSONErrorTextMatchesVendor(t *testing.T) {
	rows := loadExtJSONErrorTable(t)
	if len(rows) != len(extJSONErrorInputs) {
		t.Fatalf("the table has %d rows, the input list %d", len(rows), len(extJSONErrorInputs))
	}
	var refusedRows, targetDependent int
	var targetDependentLines []string
	for _, row := range rows {
		for _, mode := range []struct {
			canonical bool
			outcomes  targetOutcomes
		}{{true, row.Canonical}, {false, row.Relaxed}} {
			libraryResult := libraryRawExtJSON([]byte(row.Input), mode.canonical)
			if libraryResult.outcome != string(mode.outcomes.Raw) {
				t.Errorf("%s, canonical=%v: library %q, vendor table %q\n  input %q",
					row.Name, mode.canonical, libraryResult.outcome, mode.outcomes.Raw, row.Input)
				continue
			}
			if divergence := extJSONDivergence([]byte(row.Input), mode.canonical); divergence != "" {
				t.Errorf("%s: %s\n  input %q", row.Name, divergence, row.Input)
			}
			if mode.outcomes.Raw != "ok" {
				refusedRows++
			}
			if !mode.outcomes.SameAtEach {
				targetDependent++
				targetDependentLines = append(targetDependentLines, fmt.Sprintf("%s (canonical=%v): raw %q | map %q | M %q | D %q",
					row.Name, mode.canonical, mode.outcomes.Raw, mode.outcomes.Map, mode.outcomes.M, mode.outcomes.Ordered))
			}
		}
	}
	t.Logf("%d rows, %d refusals at the raw target over both modes; %d row-modes where the targets disagree:\n%s",
		len(rows), refusedRows, targetDependent, strings.Join(targetDependentLines, "\n"))
}
