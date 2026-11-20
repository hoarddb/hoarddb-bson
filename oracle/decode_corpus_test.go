// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"testing"
	"time"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/corpus"
)

// Spec 9.1 row 2: the corpus, the vectors, the v1.0-written corpus and
// generated documents decoded by both libraries into every target class the
// inventory lists, and malformed input per target.

// decCorpusInterfaces declares, as interface{} fields, the keys the corpus,
// the vectors and the probe classes use, so a struct target that declares
// the key reads the value instead of skipping it.
type decCorpusInterfaces struct {
	A, B, C, D, S, P, X, Z, V, T, K any
	Dup                             any
	ID                              any `bson:"_id"`
	Empty                           any `bson:""`
	Zero                            any `bson:"0"`
}

// decCorpusTyped declares the same keys with the types a typed site uses.
type decCorpusTyped struct {
	A   int64
	B   bool
	C   string
	D   map[string]any
	S   string
	P   *string
	X   float64
	Z   []any
	V   int32
	T   time.Time
	K   []byte
	Dup int32
}

// decUndeclared declares none of them: every element is skipped unread.
type decUndeclared struct {
	Unrelated int32
}

// decServerShapes are builtin-only copies of the shapes the tree decodes most
// (inventory section 3): an error response, a find response with a Raw
// field, a pop response with a Raw slice, an insert request with a map and
// json tags, and a catalog entry with a time and a named string type.
type decErrorResponse struct {
	Code           int    `bson:"code"`
	Message        string `bson:"message"`
	MaxResultBytes int64  `bson:"max_result_bytes,omitempty"`
	WriteOutcome   string `bson:"write_outcome,omitempty"`
}

type decFindResponse struct {
	Found   bool           `bson:"found"`
	Data    vendorbson.Raw `bson:"data,omitempty"`
	Message string         `bson:"message"`
}

type decLibFindResponse struct {
	Found   bool     `bson:"found"`
	Data    bson.Raw `bson:"data,omitempty"`
	Message string   `bson:"message"`
}

type decPopResponse struct {
	Found bool             `bson:"found"`
	Items []vendorbson.Raw `bson:"items,omitempty"`
	Dead  int64            `bson:"dead,omitempty"`
}

type decLibPopResponse struct {
	Found bool       `bson:"found"`
	Items []bson.Raw `bson:"items,omitempty"`
	Dead  int64      `bson:"dead,omitempty"`
}

type decInsertRequest struct {
	Database     string         `json:"database"`
	Key          string         `json:"key,omitempty"`
	Data         map[string]any `json:"data"`
	WriteConcern string         `json:"write_concern,omitempty"`
	Admitted     bool           `bson:"admitted,omitempty" json:"admitted,omitempty"`
}

type decBucketType string

type decCatalogEntry struct {
	ID        int64         `bson:"id"`
	Slot      int32         `bson:"slot"`
	Type      decBucketType `bson:"type"`
	Indices   []struct{ Name string }
	CreatedAt time.Time `bson:"created_at"`
	Epoch     uint64    `bson:"epoch,omitempty"`
}

type decObjectIDRecord struct {
	ID      vendorprimitive.ObjectID `bson:"_id"`
	Created vendorprimitive.DateTime `bson:"created"`
	Tags    []string                 `bson:"tags"`
}

type decLibObjectIDRecord struct {
	ID      primitive.ObjectID `bson:"_id"`
	Created primitive.DateTime `bson:"created"`
	Tags    []string           `bson:"tags"`
}

// decEveryTarget is every target a whole document goes into in this file.
func decEveryTarget() []decTarget {
	return append(decDocumentTargets(),
		decSame("*decCorpusInterfaces", decZero[decCorpusInterfaces]),
		decSame("*decCorpusTyped", decZero[decCorpusTyped]),
		decSame("*decUndeclared", decZero[decUndeclared]),
		decSame("*decErrorResponse", decZero[decErrorResponse]),
		decTarget{"*decFindResponse", func() any { return &decFindResponse{} }, func() any { return &decLibFindResponse{} }},
		decTarget{"*decPopResponse", func() any { return &decPopResponse{} }, func() any { return &decLibPopResponse{} }},
		decSame("*decInsertRequest", decZero[decInsertRequest]),
		decSame("*decCatalogEntry", decZero[decCatalogEntry]),
		decTarget{"*decObjectIDRecord", func() any { return &decObjectIDRecord{} }, func() any { return &decLibObjectIDRecord{} }},
		decTarget{"*benchdata.Record", func() any { return &benchdata.Record{} }, func() any { return &decBenchRecord{} }},
		decSame("*[]interface{}", decZero[[]any]),
		decTarget{"*[3]primitive.E", func() any { return &[3]vendorprimitive.E{} }, func() any { return &[3]primitive.E{} }},
		decTarget{"**bson.D", func() any { var target *vendorbson.D; return &target }, func() any { var target *bson.D; return &target }},
		decSame("*decCorpusInterfaces prefilled", func() decCorpusInterfaces { return decCorpusInterfaces{A: "old", Z: []any{1}} }),
		decSame("*map[string]interface{} prefilled", func() map[string]any { return map[string]any{"a": "old", "kept": true} }),
		decTarget{"*bson.D prefilled", func() any { return &vendorbson.D{{Key: "old", Value: 1}} }, func() any { return &bson.D{{Key: "old", Value: 1}} }},
	)
}

// decCorpusInputs are the corpus's valid, converted and decode-error cases,
// the vectors and driver payloads, the hand-stated documents and Ref450.
func decCorpusInputs(t *testing.T) [][]byte {
	inputs, err := allSeeds()
	if err != nil {
		t.Fatalf("loading the seeds: %v", err)
	}
	corpusFiles, err := corpus.Load()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	for _, corpusFile := range corpusFiles {
		for _, validCase := range corpusFile.Valid {
			if validCase.ConvertedBSON != nil {
				inputs = append(inputs, validCase.ConvertedBSON)
			}
		}
	}
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		t.Fatal(err)
	}
	inputs = append(inputs, ref450, decNested(t))
	// Deeper than the reader's 16 inline frames, as documents and as arrays,
	// with a malformed byte at the bottom.
	for _, levels := range []int{8, 9, 17, 60} {
		inputs = append(inputs, nestedDocumentOf(levels), decNestedArrays(levels))
		deep := nestedDocumentOf(levels)
		inputs = append(inputs, withByteAt(deep, len(deep)-levels, 7))
	}
	return append(inputs, baseDocuments()...)
}

// decNestedArrays is a document holding arrays nested levels deep, the
// innermost holding an embedded document.
func decNestedArrays(levels int) []byte {
	innermost := documentOf(elementOf(0x03, "0", documentOf(elementOf(0x10, "x", int32Of(1)))))
	for level := levels - 2; level >= 1; level-- {
		innermost = documentOf(elementOf(0x04, "0", innermost))
	}
	return documentOf(elementOf(0x04, "a", innermost))
}

func TestDecodeCorpusIntoEveryTarget(t *testing.T) {
	inputs := decCorpusInputs(t)
	targets := decEveryTarget()
	accepted := 0
	for _, input := range inputs {
		for _, target := range targets {
			if divergence := decDivergence(input, target); divergence != "" {
				t.Errorf("% x: %s", input, divergence)
			}
		}
		if vendorOutcome(func() error { return vendorbson.Unmarshal(input, &vendorbson.D{}) }) == "ok" {
			accepted++
		}
	}
	if accepted == 0 || accepted == len(inputs) {
		t.Fatalf("%d of %d inputs decode into bson.D: the set must hold both", accepted, len(inputs))
	}
	t.Logf("%d inputs (%d decode into bson.D) into %d targets; vendor panics refused by the library: %v", len(inputs), accepted, len(targets), decVendorPanics)
}

// decV1CorpusRecord is one record of hoardDB's v1.0-written corpus
// (hoardDB/internal/bsonoracle V1Record).
type decV1CorpusRecord struct {
	Class string `json:"class"`
	Value []byte `json:"value"`
}

// TestDecodeV1CorpusIntoEveryTarget reads hoardDB's v1.0-written corpus from
// the path in HOARDDB_V1CORPUS, read only. The corpus is not part of this
// repository, so a checkout without it skips the test.
func TestDecodeV1CorpusIntoEveryTarget(t *testing.T) {
	corpusPath := os.Getenv("HOARDDB_V1CORPUS")
	if corpusPath == "" {
		t.Skip("HOARDDB_V1CORPUS is not set: point it at hoardDB/internal/bsonoracle/testdata/v1corpus/records.jsonl.gz")
	}
	records := decLoadV1Corpus(t, corpusPath)
	classes := map[string]int{}
	targets := decEveryTarget()
	for _, record := range records {
		classes[record.Class]++
		for _, target := range targets {
			if divergence := decDivergence(record.Value, target); divergence != "" {
				t.Errorf("%s record % x: %s", record.Class, record.Value, divergence)
			}
		}
	}
	names := make([]string, 0, len(classes))
	for name := range classes {
		names = append(names, name)
	}
	sort.Strings(names)
	t.Logf("%d v1.0 records into %d targets; classes %v", len(records), len(targets), names)
}

func decLoadV1Corpus(t *testing.T, corpusPath string) []decV1CorpusRecord {
	t.Helper()
	opened, err := os.Open(corpusPath)
	if err != nil {
		t.Fatalf("opening the v1.0 corpus: %v", err)
	}
	defer func() {
		if closeErr := opened.Close(); closeErr != nil {
			t.Errorf("closing the v1.0 corpus: %v", closeErr)
		}
	}()
	decompressed, err := gzip.NewReader(bufio.NewReader(opened))
	if err != nil {
		t.Fatalf("reading the v1.0 corpus's gzip header: %v", err)
	}
	var records []decV1CorpusRecord
	decoder := json.NewDecoder(decompressed)
	for {
		var record decV1CorpusRecord
		if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
			return records
		} else if err != nil {
			t.Fatalf("decoding record %d of the v1.0 corpus: %v", len(records), err)
		}
		records = append(records, record)
	}
}

// decMalformedTargets are spec 9.1 row 2's targets for malformed input: a
// map, bson.D, bson.M, interface{}, a struct declaring the key and one not
// declaring it.
func decMalformedTargets() []decTarget {
	return []decTarget{
		decTargetMap, decTargetD, decTargetM, decTargetInterface,
		decSame("*decCorpusInterfaces", decZero[decCorpusInterfaces]),
		decSame("*decCorpusTyped", decZero[decCorpusTyped]),
		decSame("*decUndeclared", decZero[decUndeclared]),
	}
}

func TestDecodeMalformedInputPerTarget(t *testing.T) {
	targets := decMalformedTargets()
	refusedSomewhere, acceptedSomewhere := 0, 0
	var inputs [][]byte
	for _, probe := range probeCases() {
		inputs = append(inputs, probe.malformed, probe.twin)
	}
	inputs = append(inputs, framingSeeds()...)
	inputs = append(inputs, damagedDocuments(append(baseDocuments(), decNested(t)), 8)...)
	for _, input := range inputs {
		outcomes := map[bool]bool{}
		for _, target := range targets {
			outcome := vendorOutcome(func() error { return vendorbson.Unmarshal(input, target.vendor()) })
			outcomes[outcome == "ok"] = true
			if divergence := decDivergence(input, target); divergence != "" {
				t.Errorf("% x: %s", input, divergence)
			}
		}
		if outcomes[true] {
			acceptedSomewhere++
		}
		if outcomes[false] {
			refusedSomewhere++
		}
	}
	if refusedSomewhere == 0 || acceptedSomewhere == 0 {
		t.Fatalf("of %d inputs, %d are refused by some target and %d accepted by some: the set must hold both", len(inputs), refusedSomewhere, acceptedSomewhere)
	}
	t.Logf("%d malformed and twin inputs into %d targets; vendor panics refused by the library: %v", len(inputs), len(targets), decVendorPanics)
}
