// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package vectors loads the synced test vectors in testdata/vectors: the
// codec's own vectors (bson-vectors/v1.json) and the driver wire vectors
// (driver-vectors/v1.json). The files are copies made by hoardDB's sync
// script; this package only reads them.
package vectors

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// HexBytes is a JSON string of hex digits, decoded.
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

// BSONVector is one case of bson-vectors/v1.json. Kind is encode, decode,
// reject, encode_refuse, datetime_floor or size; Hex is set for the kinds
// that carry bytes. Document keeps the vector's own typed-document form.
type BSONVector struct {
	Name     string          `json:"name"`
	Group    string          `json:"group"`
	Kind     string          `json:"kind"`
	Notes    string          `json:"notes"`
	Hex      HexBytes        `json:"hex"`
	Document json.RawMessage `json:"document"`
}

// DriverVector is one case of driver-vectors/v1.json. Kind names the exchange
// (frame, and others); the rest of the case stays raw, because the library
// only seeds from it.
type DriverVector struct {
	Name  string          `json:"name"`
	Group string          `json:"group"`
	Kind  string          `json:"kind"`
	Raw   json.RawMessage `json:"-"`
}

// PayloadBytes returns every payload_hex string anywhere in the vector, decoded.
// Exchange vectors carry the BSON payloads of their frames under that key.
func (driverVector DriverVector) PayloadBytes() ([][]byte, error) {
	var vectorTree any
	if err := json.Unmarshal(driverVector.Raw, &vectorTree); err != nil {
		return nil, fmt.Errorf("parsing driver vector %q: %w", driverVector.Name, err)
	}
	var payloads [][]byte
	var walkErr error
	var walk func(node any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if hexText, isText := typed[key].(string); isText && key == "payload_hex" {
					payload, err := hex.DecodeString(hexText)
					if err != nil && walkErr == nil {
						walkErr = fmt.Errorf("driver vector %q: payload_hex %q: %w", driverVector.Name, hexText, err)
					}
					payloads = append(payloads, payload)
					continue
				}
				walk(typed[key])
			}
		case []any:
			for _, element := range typed {
				walk(element)
			}
		}
	}
	walk(vectorTree)
	return payloads, walkErr
}

// Dir returns the repository's testdata/vectors directory, found by walking
// up from the working directory, which is the test package's directory.
func Dir() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("finding the working directory: %w", err)
	}
	for searchDir := workingDir; ; searchDir = filepath.Dir(searchDir) {
		candidate := filepath.Join(searchDir, "testdata", "vectors")
		if _, err := os.Stat(filepath.Join(candidate, "SHA256SUMS")); err == nil {
			return candidate, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("checking %s: %w", candidate, err)
		}
		if filepath.Dir(searchDir) == searchDir {
			return "", fmt.Errorf("no testdata/vectors/SHA256SUMS above %s", workingDir)
		}
	}
}

// LoadBSON reads bson-vectors/v1.json.
func LoadBSON() ([]BSONVector, error) {
	var bsonVectors []BSONVector
	if err := loadVectorList("bson-vectors/v1.json", &bsonVectors); err != nil {
		return nil, err
	}
	return bsonVectors, nil
}

// LoadDriver reads driver-vectors/v1.json.
func LoadDriver() ([]DriverVector, error) {
	var rawVectors []json.RawMessage
	if err := loadVectorList("driver-vectors/v1.json", &rawVectors); err != nil {
		return nil, err
	}
	driverVectors := make([]DriverVector, 0, len(rawVectors))
	for position, rawVector := range rawVectors {
		driverVector := DriverVector{Raw: rawVector}
		if err := json.Unmarshal(rawVector, &driverVector); err != nil {
			return nil, fmt.Errorf("parsing driver vector %d: %w", position, err)
		}
		driverVectors = append(driverVectors, driverVector)
	}
	return driverVectors, nil
}

// loadVectorList reads the "vectors" array of a vector file into target.
func loadVectorList(relativePath string, target any) error {
	vectorsDir, err := Dir()
	if err != nil {
		return err
	}
	fileBytes, err := os.ReadFile(filepath.Join(vectorsDir, filepath.FromSlash(relativePath)))
	if err != nil {
		return fmt.Errorf("reading %s: %w", relativePath, err)
	}
	var vectorFile struct {
		Vectors json.RawMessage `json:"vectors"`
	}
	if err := json.Unmarshal(fileBytes, &vectorFile); err != nil {
		return fmt.Errorf("parsing %s: %w", relativePath, err)
	}
	if err := json.Unmarshal(vectorFile.Vectors, target); err != nil {
		return fmt.Errorf("parsing the vectors of %s: %w", relativePath, err)
	}
	return nil
}
