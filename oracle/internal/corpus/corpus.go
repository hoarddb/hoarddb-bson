// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package corpus loads the BSON corpus that ships in the pinned vendor
// module, so the corpus is versioned with the oracle module's requirement
// (spec 9.1.4). The directory is found with go list, never copied.
package corpus

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const vendorModulePath = "go.mongodb.org/mongo-driver"

// HexBytes is a JSON string of hex digits, decoded. The corpus writes both
// cases of digit.
type HexBytes []byte

// UnmarshalJSON decodes the hex string.
func (hexBytes *HexBytes) UnmarshalJSON(jsonText []byte) error {
	var hexText string
	if err := json.Unmarshal(jsonText, &hexText); err != nil {
		return fmt.Errorf("reading a hex string: %w", err)
	}
	decoded, err := hex.DecodeString(hexText)
	if err != nil {
		return fmt.Errorf("decoding hex %q: %w", hexText, err)
	}
	*hexBytes = decoded
	return nil
}

// ValidCase is one entry of a file's "valid" list.
type ValidCase struct {
	Description       string   `json:"description"`
	CanonicalBSON     HexBytes `json:"canonical_bson"`
	CanonicalExtJSON  string   `json:"canonical_extjson"`
	RelaxedExtJSON    *string  `json:"relaxed_extjson"`
	DegenerateBSON    HexBytes `json:"degenerate_bson"`
	DegenerateExtJSON *string  `json:"degenerate_extjson"`
	ConvertedBSON     HexBytes `json:"converted_bson"`
	ConvertedExtJSON  *string  `json:"converted_extjson"`
	Lossy             bool     `json:"lossy"`
}

// DecodeErrorCase is one entry of "decodeErrors": bytes a decoder must refuse.
type DecodeErrorCase struct {
	Description string   `json:"description"`
	BSON        HexBytes `json:"bson"`
}

// ParseErrorCase is one entry of "parseErrors": text a parser must refuse.
type ParseErrorCase struct {
	Description string `json:"description"`
	String      string `json:"string"`
}

// File is one corpus file.
type File struct {
	Name         string            `json:"-"`
	Description  string            `json:"description"`
	BSONType     string            `json:"bson_type"`
	TestKey      *string           `json:"test_key"`
	Valid        []ValidCase       `json:"valid"`
	DecodeErrors []DecodeErrorCase `json:"decodeErrors"`
	ParseErrors  []ParseErrorCase  `json:"parseErrors"`
}

// Dir returns the corpus directory inside the vendor module the oracle
// module pins.
func Dir() (string, error) {
	listCommand := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", vendorModulePath)
	listCommand.Env = append(os.Environ(), "GOWORK=off")
	listOutput, err := listCommand.Output()
	if err != nil {
		return "", fmt.Errorf("finding the pinned %s module with go list: %w", vendorModulePath, err)
	}
	return filepath.Join(strings.TrimSpace(string(listOutput)), "testdata", "bson-corpus"), nil
}

// EntryNames lists every entry of the corpus directory, sorted. The vendor
// ships bsonview, a Perl script, beside the JSON files.
func EntryNames() ([]string, error) {
	corpusDir, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(corpusDir)
	if err != nil {
		return nil, fmt.Errorf("reading the corpus directory %s: %w", corpusDir, err)
	}
	entryNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		entryNames = append(entryNames, entry.Name())
	}
	sort.Strings(entryNames)
	return entryNames, nil
}

// Load parses every .json file of the corpus, sorted by file name.
func Load() ([]File, error) {
	corpusDir, err := Dir()
	if err != nil {
		return nil, err
	}
	entryNames, err := EntryNames()
	if err != nil {
		return nil, err
	}
	var corpusFiles []File
	for _, entryName := range entryNames {
		if !strings.HasSuffix(entryName, ".json") {
			continue
		}
		fileBytes, err := os.ReadFile(filepath.Join(corpusDir, entryName))
		if err != nil {
			return nil, fmt.Errorf("reading corpus file %s: %w", entryName, err)
		}
		corpusFile := File{Name: entryName}
		if err := json.Unmarshal(fileBytes, &corpusFile); err != nil {
			return nil, fmt.Errorf("parsing corpus file %s: %w", entryName, err)
		}
		corpusFiles = append(corpusFiles, corpusFile)
	}
	return corpusFiles, nil
}
