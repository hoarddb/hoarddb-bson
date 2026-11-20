// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"fmt"
	"testing"
	"time"

	vendorbson "go.mongodb.org/mongo-driver/bson"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/corpus"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/vectors"
)

// The four differential fuzz targets of spec 9.1 row 3. Each is seeded from
// the vectors and the corpus (the tap records join in step 4f). Validate
// (2a) and Lookup (2b) have their library sides; the step that adds each
// other one is named in its body. A target whose library side is missing
// still runs its seeds in a plain go test, so a vendor panic on a seed shows
// up at once.

// fuzzTarget names a fuzz target and the seeds it starts from. A seed set
// that loads empty means a loader broke and the target fuzzes from nothing.
type fuzzTarget struct {
	name  string
	seeds func() ([][]byte, error)
}

var fuzzTargets = []fuzzTarget{
	{name: "FuzzEncodeDifferential", seeds: documentSeeds},
	{name: "FuzzDecodeDifferential", seeds: allSeeds},
	{name: "FuzzValidateDifferential", seeds: allSeeds},
	{name: "FuzzLookupDifferential", seeds: allSeeds},
}

// seedsOf returns the seeds of the named target, failing the fuzz run when
// they cannot load.
func seedsOf(f *testing.F, targetName string) [][]byte {
	f.Helper()
	for _, target := range fuzzTargets {
		if target.name == targetName {
			seedBytes, err := target.seeds()
			if err != nil {
				f.Fatalf("loading the seeds of %s: %v", targetName, err)
			}
			return seedBytes
		}
	}
	f.Fatalf("no fuzz target is declared as %s", targetName)
	return nil
}

// documentSeeds are the byte strings that are one valid document: the
// corpus's valid cases and the vectors the codec must encode or decode.
// An encode fuzz needs decodable input to get a Go value from.
func documentSeeds() ([][]byte, error) {
	corpusFiles, err := corpus.Load()
	if err != nil {
		return nil, fmt.Errorf("loading the corpus: %w", err)
	}
	var seedBytes [][]byte
	for _, corpusFile := range corpusFiles {
		for _, validCase := range corpusFile.Valid {
			seedBytes = append(seedBytes, validCase.CanonicalBSON)
			if validCase.DegenerateBSON != nil {
				seedBytes = append(seedBytes, validCase.DegenerateBSON)
			}
		}
	}
	bsonVectors, err := vectors.LoadBSON()
	if err != nil {
		return nil, fmt.Errorf("loading the bson vectors: %w", err)
	}
	for _, bsonVector := range bsonVectors {
		if bsonVector.Hex != nil && (bsonVector.Kind == "encode" || bsonVector.Kind == "decode") {
			seedBytes = append(seedBytes, bsonVector.Hex)
		}
	}
	return seedBytes, nil
}

// allSeeds adds what a decoder or validator must refuse: the corpus's decode
// errors, the vectors that must be rejected, and the driver vectors' payloads.
func allSeeds() ([][]byte, error) {
	seedBytes, err := documentSeeds()
	if err != nil {
		return nil, err
	}
	corpusFiles, err := corpus.Load()
	if err != nil {
		return nil, fmt.Errorf("loading the corpus: %w", err)
	}
	for _, corpusFile := range corpusFiles {
		for _, decodeError := range corpusFile.DecodeErrors {
			seedBytes = append(seedBytes, decodeError.BSON)
		}
	}
	bsonVectors, err := vectors.LoadBSON()
	if err != nil {
		return nil, fmt.Errorf("loading the bson vectors: %w", err)
	}
	for _, bsonVector := range bsonVectors {
		if bsonVector.Hex != nil && bsonVector.Kind == "reject" {
			seedBytes = append(seedBytes, bsonVector.Hex)
		}
	}
	driverVectors, err := vectors.LoadDriver()
	if err != nil {
		return nil, fmt.Errorf("loading the driver vectors: %w", err)
	}
	for _, driverVector := range driverVectors {
		payloads, err := driverVector.PayloadBytes()
		if err != nil {
			return nil, fmt.Errorf("reading driver vector payloads: %w", err)
		}
		seedBytes = append(seedBytes, payloads...)
	}
	return seedBytes, nil
}

// vendorOutcome runs one vendor call and reduces it to its outcome: "ok", the
// error text, or "panic: ..." when the vendor panics. The vendor's
// bson.Raw.Validate panics on a document whose declared length is zero or
// negative (bsoncore.Document.Validate indexes d[length-1]), so a differential
// must take a panic as an outcome to compare, not as a crash of the harness.
func vendorOutcome(vendorCall func() error) (outcome string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = fmt.Sprintf("panic: %v", recovered)
		}
	}()
	if err := vendorCall(); err != nil {
		return err.Error()
	}
	return "ok"
}

func addSeeds(f *testing.F, targetName string) {
	f.Helper()
	for _, seedBytes := range seedsOf(f, targetName) {
		f.Add(seedBytes)
	}
}

// FuzzEncodeDifferential decodes bytes to Go values with the vendor, then
// encodes each value with both libraries' public entry points: as bson.D
// (every BSON type, in order) with Marshal and with MarshalValue, as bson.M
// (map levels, which the library sorts), and as a struct of builtins (the
// struct codec's omitempty, minsize and widths). The outcomes must match:
// the same error text, or encodings compare.Encoded finds equal.
func FuzzEncodeDifferential(f *testing.F) {
	addSeeds(f, "FuzzEncodeDifferential")
	f.Fuzz(func(t *testing.T, documentBytes []byte) {
		var vendorDocument vendorbson.D
		if vendorOutcome(func() error { return vendorbson.Unmarshal(documentBytes, &vendorDocument) }) == "ok" {
			libraryDocument := libraryTwin(t, vendorDocument)
			requireSameEncoding(t, twinValues("bson.D", libraryDocument, vendorDocument))
			if len(vendorDocument) > 0 {
				requireSameMarshalValue(t, twinValues("MarshalValue of the first value", libraryDocument.(primitive.D)[0].Value, vendorDocument[0].Value))
			}
		}
		var vendorMap vendorbson.M
		if vendorOutcome(func() error { return vendorbson.Unmarshal(documentBytes, &vendorMap) }) == "ok" {
			requireSameEncoding(t, twinValues("bson.M", libraryTwin(t, vendorMap), vendorMap))
		}
		var shape fuzzEncodeShape
		if vendorOutcome(func() error { return vendorbson.Unmarshal(documentBytes, &shape) }) == "ok" {
			requireSameEncoding(t, sameValue("struct", shape))
			requireSameEncoding(t, sameValue("struct pointer", &shape))
		}
	})
}

// fuzzEncodeShape is a struct of builtins the vendor can decode fuzzed
// bytes into, so both libraries encode the same value.
type fuzzEncodeShape struct {
	Name     string            `bson:"name"`
	Count    int64             `bson:"count,omitempty"`
	Small    int32             `bson:"small"`
	Word     int               `bson:"word,omitempty"`
	Unsigned uint32            `bson:"unsigned,minsize"`
	Ratio    float64           `bson:"ratio,omitempty"`
	Flag     bool              `bson:"flag,omitempty"`
	Tags     []string          `bson:"tags"`
	Values   map[string]int64  `bson:"values,omitempty"`
	Payload  []byte            `bson:"payload"`
	When     time.Time         `bson:"when,omitempty"`
	Inner    *fuzzEncodeInner  `bson:"inner"`
	Matrix   [][]int32         `bson:"matrix,omitempty"`
	Extra    map[string]string `bson:",inline"`
}

type fuzzEncodeInner struct {
	Label string           `bson:"label"`
	Depth uint8            `bson:"depth"`
	Next  *fuzzEncodeInner `bson:"next,omitempty"`
}

// FuzzDecodeDifferential decodes bytes into each target type the tree uses,
// through bson.Unmarshal and RawValue.Unmarshal and the vendor's, and compares the
// outcome, the error text and the decoded value, Go types included
// (decode_harness_test.go:decDivergence). Where the vendor panics the
// library must return an error.
func FuzzDecodeDifferential(f *testing.F) {
	addSeeds(f, "FuzzDecodeDifferential")
	targets := decMalformedTargets()
	valueTargets := []decTarget{decTargetInterface, decTargetMap, decTargetD}
	f.Fuzz(func(t *testing.T, documentBytes []byte) {
		for _, target := range targets {
			if divergence := decDivergence(documentBytes, target); divergence != "" {
				t.Fatalf("% x: %s", documentBytes, divergence)
			}
		}
		// The same bytes as RawValue.Unmarshal's value: a document, and an
		// array.
		for _, valueType := range []byte{0x03, 0x04} {
			for _, target := range valueTargets {
				if divergence := decValueDivergence(valueType, documentBytes, target); divergence != "" {
					t.Fatalf("RawValue type %#x, % x: %s", valueType, documentBytes, divergence)
				}
			}
		}
	})
}

// FuzzValidateDifferential validates bytes as one document: the library's
// Validate accepts iff the vendor decodes them into bson.D, for inputs
// shallower than the ceiling (validate_test.go:validateDivergence). The
// root framings that panic the vendor's Raw.Validate join the seeds.
func FuzzValidateDifferential(f *testing.F) {
	addSeeds(f, "FuzzValidateDifferential")
	for _, framing := range framingSeeds() {
		f.Add(framing)
	}
	f.Fuzz(func(t *testing.T, documentBytes []byte) {
		if divergence := validateDivergence(documentBytes); divergence != "" {
			t.Fatalf("% x: %s", documentBytes, divergence)
		}
	})
}

// FuzzLookupDifferential looks paths up in fuzzed bytes. For bytes Validate
// accepts, every path lookupPathsOf enumerates must agree with the typed
// vendor reference and, for names only, with vendorbson.Raw.LookupErr
// (lookup_test.go:lookupDivergence). For bytes only TrustStored accepts,
// framed but malformed inside, Lookup and the cursor must not panic.
func FuzzLookupDifferential(f *testing.F) {
	addSeeds(f, "FuzzLookupDifferential")
	f.Fuzz(func(t *testing.T, documentBytes []byte) {
		var counts lookupCounts
		if document, err := bson.Validate(documentBytes, bson.Limits{Depth: bson.MaxNestingCeiling}); err == nil {
			if divergence := lookupDocumentDivergence(documentBytes, document, 512, &counts); divergence != "" {
				t.Fatalf("% x: %s", documentBytes, divergence)
			}
			return
		}
		document, err := bson.TrustStored(documentBytes)
		if err != nil {
			return
		}
		if panicked := walkTrustedWithoutPanic(document, lookupPathsOf(documentBytes, 512)); panicked != "" {
			t.Fatalf("% x: %s", documentBytes, panicked)
		}
	})
}

// walkTrustedWithoutPanic runs every path and a full cursor walk over a
// framed document whose inside may be malformed, and reports a panic.
func walkTrustedWithoutPanic(document bson.Document, paths []lookupPath) (panicked string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicked = fmt.Sprintf("panicked: %v", recovered)
		}
	}()
	for _, path := range paths {
		if _, _, err := document.Lookup(path.library()); err != nil {
			return fmt.Sprintf("Lookup on a framed document gave an error: %v", err)
		}
	}
	cursor := document.Cursor()
	for cursor.Next() {
		cursor.Value()
	}
	return ""
}
