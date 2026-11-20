// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"

	"github.com/hoarddb/hoarddb-bson/oracle/internal/corpus"
)

const divergencesPath = "corpus-divergences.md"

var updateDivergences = flag.Bool("update-divergences", false, "rewrite corpus-divergences.md from the vendor's outcomes")

// divergence is one corpus case where the vendor's outcome is not what the
// corpus expects. Spec 9.1.4 judges each case against the vendor's outcome,
// so these are the cases a library must match the vendor on rather than the
// corpus on, each one reviewed.
type divergence struct {
	fileName    string
	section     string
	position    int
	description string
	input       []byte
	inputText   string
	expected    string
	vendorDoes  string
}

// truncatedHex shows the first 48 bytes of input, enough to identify a case
// without bloating the file.
func truncatedHex(input []byte) string {
	const shownBytes = 48
	if len(input) <= shownBytes {
		return hex.EncodeToString(input)
	}
	return fmt.Sprintf("%s...(%d bytes)", hex.EncodeToString(input[:shownBytes]), len(input))
}

// vendorDecodeAndEncode decodes documentBytes into a bson.D and encodes it
// back, returning the decode outcome and the re-encoded bytes. Outcome is
// "ok" when decoding succeeded.
func vendorDecodeAndEncode(documentBytes []byte) (outcome string, reencoded []byte) {
	var decoded vendorbson.D
	outcome = vendorOutcome(func() error { return vendorbson.Unmarshal(documentBytes, &decoded) })
	if outcome != "ok" {
		return outcome, nil
	}
	reencodeOutcome := vendorOutcome(func() error {
		var err error
		reencoded, err = vendorbson.Marshal(decoded)
		return err
	})
	if reencodeOutcome != "ok" {
		return "re-encode: " + reencodeOutcome, nil
	}
	return "ok", reencoded
}

// judgeValidBytes checks that documentBytes decodes and re-encodes to the
// canonical bytes, the corpus's expectation of both canonical and degenerate
// input.
func judgeValidBytes(documentBytes, canonicalBytes []byte) string {
	outcome, reencoded := vendorDecodeAndEncode(documentBytes)
	switch {
	case outcome != "ok":
		return "refuses: " + outcome
	case !bytes.Equal(reencoded, canonicalBytes):
		return "re-encodes to " + truncatedHex(reencoded)
	}
	return ""
}

// judgeParseError reports how the vendor handles text the corpus says no
// parser may accept. A refusal at Unmarshal or at the Marshal of the result
// counts: the vendor checks some rules (a NUL in a key) only on encode.
func judgeParseError(parseText string, bsonType string) string {
	if bsonType == "0x13" {
		parseText = fmt.Sprintf(`{"decimal128": {"$numberDecimal": "%s"}}`, parseText)
	}
	var decoded vendorbson.D
	if outcome := vendorOutcome(func() error {
		return vendorbson.UnmarshalExtJSON([]byte(parseText), true, &decoded)
	}); outcome != "ok" {
		return ""
	}
	if outcome := vendorOutcome(func() error {
		_, err := vendorbson.Marshal(decoded)
		return err
	}); outcome != "ok" {
		return ""
	}
	return "accepts it"
}

func findDivergences(t *testing.T) (divergences []divergence, judged [3]int) {
	t.Helper()
	corpusFiles, err := corpus.Load()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	for _, corpusFile := range corpusFiles {
		for position, validCase := range corpusFile.Valid {
			judged[0]++
			if verdict := judgeValidBytes(validCase.CanonicalBSON, validCase.CanonicalBSON); verdict != "" {
				divergences = append(divergences, divergence{corpusFile.Name, "valid", position, validCase.Description, validCase.CanonicalBSON, "", "decodes and re-encodes to the canonical bytes", verdict})
			}
			if validCase.DegenerateBSON != nil {
				if verdict := judgeValidBytes(validCase.DegenerateBSON, validCase.CanonicalBSON); verdict != "" {
					divergences = append(divergences, divergence{corpusFile.Name, "valid (degenerate_bson)", position, validCase.Description, validCase.DegenerateBSON, "", "decodes and re-encodes to the canonical bytes", verdict})
				}
			}
		}
		for position, decodeError := range corpusFile.DecodeErrors {
			judged[1]++
			var decoded vendorbson.D
			outcome := vendorOutcome(func() error { return vendorbson.Unmarshal(decodeError.BSON, &decoded) })
			switch {
			case outcome == "ok":
				divergences = append(divergences, divergence{corpusFile.Name, "decodeErrors", position, decodeError.Description, decodeError.BSON, "", "returns an error", "accepts it"})
			case strings.HasPrefix(outcome, "panic: "):
				divergences = append(divergences, divergence{corpusFile.Name, "decodeErrors", position, decodeError.Description, decodeError.BSON, "", "returns an error", "panics"})
			}
		}
		for position, parseError := range corpusFile.ParseErrors {
			judged[2]++
			if verdict := judgeParseError(parseError.String, corpusFile.BSONType); verdict != "" {
				divergences = append(divergences, divergence{corpusFile.Name, "parseErrors", position, parseError.Description, nil, parseError.String, "returns an error", verdict})
			}
		}
	}
	return divergences, judged
}

// tableCell makes text safe inside a markdown table cell on one line.
func tableCell(text string) string {
	text = strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(text)
	return "`" + strings.ReplaceAll(text, "`", "'") + "`"
}

func renderDivergences(divergences []divergence, judged [3]int, vendorVersion string) string {
	var rendered strings.Builder
	rendered.WriteString("# Corpus divergences\n\n")
	rendered.WriteString("Generated by `go test -run TestCorpusDivergencesFileIsCurrent -update-divergences` in `oracle/`.\n")
	rendered.WriteString("Do not edit by hand: `TestCorpusDivergencesFileIsCurrent` regenerates it and compares byte for byte.\n\n")
	rendered.WriteString("Spec 9.1.4 judges each corpus case against the vendor's outcome, not the corpus's\n")
	rendered.WriteString("expectation. A case is listed here when the two differ. The library must match the\n")
	rendered.WriteString("vendor on these cases. A change to this file is a reviewed change and is named in a\n")
	rendered.WriteString("commit's subject.\n\n")
	fmt.Fprintf(&rendered, "Vendor: go.mongodb.org/mongo-driver %s.\n\n", vendorVersion)
	rendered.WriteString("What is judged:\n\n")
	rendered.WriteString("- `valid` and degenerate_bson: the vendor decodes the bytes into a bson.D and encodes it back to the canonical bytes.\n")
	rendered.WriteString("- `decodeErrors`: the vendor's Unmarshal into a bson.D returns an error (a panic is a divergence too).\n")
	rendered.WriteString("- `parseErrors`: the vendor's UnmarshalExtJSON (canonical) into a bson.D, or the Marshal of its result, returns an error. A decimal128 file's strings are wrapped in a numberDecimal field first.\n")
	rendered.WriteString("- Extended JSON output (canonical_extjson and the like) is not judged here: step 5 owns it.\n\n")
	fmt.Fprintf(&rendered, "Judged: %d valid cases, %d decodeErrors cases, %d parseErrors cases. Divergences: %d.\n", judged[0], judged[1], judged[2], len(divergences))
	currentFile := ""
	for _, found := range divergences {
		if found.fileName != currentFile {
			currentFile = found.fileName
			fmt.Fprintf(&rendered, "\n## %s\n\n| Case | Description | Input | Corpus expects | Vendor does |\n| --- | --- | --- | --- | --- |\n", currentFile)
		}
		inputText := found.inputText
		if found.input != nil {
			inputText = truncatedHex(found.input)
		}
		fmt.Fprintf(&rendered, "| %s[%d] | %s | %s | %s | %s |\n", found.section, found.position, tableCell(found.description), tableCell(inputText), found.expected, found.vendorDoes)
	}
	return rendered.String()
}

func TestCorpusDivergencesFileIsCurrent(t *testing.T) {
	divergences, judged := findDivergences(t)
	rendered := renderDivergences(divergences, judged, goListModule(t, "{{.Version}}", vendorModulePath))
	if *updateDivergences {
		if err := os.WriteFile(divergencesPath, []byte(rendered), 0o644); err != nil {
			t.Fatalf("writing %s: %v", divergencesPath, err)
		}
		t.Logf("wrote %s: %d divergences", divergencesPath, len(divergences))
		return
	}
	committed, err := os.ReadFile(divergencesPath)
	if err != nil {
		t.Fatalf("reading %s: %v. To create it, run 'go test -run TestCorpusDivergencesFileIsCurrent -update-divergences' in oracle/ and review the file", divergencesPath, err)
	}
	if string(committed) != rendered {
		t.Errorf("%s is not what the vendor's outcomes give today. If the vendor pin changed on purpose, regenerate it with 'go test -run TestCorpusDivergencesFileIsCurrent -update-divergences' in oracle/, review the diff, and name the change in the commit's subject", divergencesPath)
	}
}
