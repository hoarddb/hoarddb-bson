// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/corpus"
)

// The resolver differential of spec 5.2 and 6.2. The library's
// Document.Lookup takes typed segments (C24); the vendor's LookupErr takes
// names, and walks an array by its keys. So each path is checked twice:
//
//  1. against a typed reference made of vendor calls only: a name is
//     bsoncore.Document.LookupErr on a document, a position is
//     bsoncore.Document.IndexErr on an array, and any other pairing is
//     absent. That is server/fieldpath.go:resolveRawPath's rule;
//  2. for a path of names only, against vendorbson.Raw.LookupErr itself,
//     type, bytes and found alike, except where the walk takes a name into
//     an array. There the vendor matches the array's keys and the typed rule
//     says absent, which is the rule the spec chose (5.2: "a decimal-key
//     string path would be a different rule").

// lookupStep is one segment of a test path, in a form both sides can read.
type lookupStep struct {
	name       string
	position   int
	isPosition bool
}

type lookupPath []lookupStep

func (path lookupPath) library() bson.Path {
	libraryPath := make(bson.Path, 0, len(path))
	for _, step := range path {
		if step.isPosition {
			libraryPath = append(libraryPath, bson.Position(step.position))
		} else {
			libraryPath = append(libraryPath, bson.Name(step.name))
		}
	}
	return libraryPath
}

// names is the path as vendor keys, when every step is a name.
func (path lookupPath) names() ([]string, bool) {
	keys := make([]string, 0, len(path))
	for _, step := range path {
		if step.isPosition {
			return nil, false
		}
		keys = append(keys, step.name)
	}
	return keys, true
}

func (path lookupPath) String() string {
	var text strings.Builder
	for _, step := range path {
		if step.isPosition {
			fmt.Fprintf(&text, "[%d]", step.position)
		} else {
			fmt.Fprintf(&text, ".%q", step.name)
		}
	}
	if text.Len() == 0 {
		return "(empty path)"
	}
	return text.String()
}

func (path lookupPath) with(step lookupStep) lookupPath {
	return append(append(lookupPath(nil), path...), step)
}

func nameStep(name string) lookupStep      { return lookupStep{name: name} }
func positionStep(position int) lookupStep { return lookupStep{position: position, isPosition: true} }

// lookupOutcome is what one side found: found, the type, the value bytes.
type lookupOutcome struct {
	found      bool
	valueType  byte
	valueBytes []byte
}

func (outcome lookupOutcome) String() string {
	if !outcome.found {
		return "not found"
	}
	return fmt.Sprintf("type 0x%02x % x", outcome.valueType, outcome.valueBytes)
}

func (outcome lookupOutcome) equal(other lookupOutcome) bool {
	return outcome.found == other.found && outcome.valueType == other.valueType && bytes.Equal(outcome.valueBytes, other.valueBytes)
}

// typedReference walks path with vendor calls under the typed rule. nameOnArray
// reports that a name step met an array, where the vendor's own name walk
// would go on by key.
func typedReference(documentBytes []byte, path lookupPath) (outcome lookupOutcome, nameOnArray bool) {
	if len(path) == 0 {
		return lookupOutcome{}, false
	}
	current := vendorbsoncore.Value{Type: vendorbson.TypeEmbeddedDocument, Data: documentBytes}
	for _, step := range path {
		switch {
		case step.isPosition && current.Type == vendorbson.TypeArray:
			if step.position < 0 {
				return lookupOutcome{}, nameOnArray
			}
			element, err := vendorbsoncore.Document(current.Data).IndexErr(uint(step.position))
			if err != nil {
				return lookupOutcome{}, nameOnArray
			}
			current = element.Value()
		case !step.isPosition && current.Type == vendorbson.TypeEmbeddedDocument:
			member, err := vendorbsoncore.Document(current.Data).LookupErr(step.name)
			if err != nil {
				return lookupOutcome{}, nameOnArray
			}
			current = member
		default:
			return lookupOutcome{}, nameOnArray || (!step.isPosition && current.Type == vendorbson.TypeArray)
		}
	}
	return lookupOutcome{found: true, valueType: byte(current.Type), valueBytes: current.Data}, nameOnArray
}

// vendorNameLookup is vendorbson.Raw.LookupErr over the path's names.
func vendorNameLookup(documentBytes []byte, keys []string) lookupOutcome {
	rawValue, err := vendorbson.Raw(documentBytes).LookupErr(keys...)
	if err != nil {
		return lookupOutcome{}
	}
	return lookupOutcome{found: true, valueType: byte(rawValue.Type), valueBytes: rawValue.Value}
}

// libraryLookup runs Document.Lookup, turning a panic or an error (a
// validated document never gives one) into a description.
func libraryLookup(document bson.Document, path lookupPath) (outcome lookupOutcome, failure string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			failure = "Lookup panicked: " + fmt.Sprint(recovered)
		}
	}()
	view, found, err := document.Lookup(path.library())
	if err != nil {
		return lookupOutcome{}, fmt.Sprintf("Lookup on a validated document gave an error: %v", err)
	}
	return lookupOutcome{found: found, valueType: byte(view.Type()), valueBytes: view.Bytes()}, ""
}

// lookupCounts tallies what a differential run exercised, so a test can
// show it compared something.
type lookupCounts struct {
	paths, found, nameOnArray, vendorNameCompared int
}

// lookupDivergence checks one path on a validated document both ways, and
// describes the first difference, or returns "".
func lookupDivergence(documentBytes []byte, document bson.Document, path lookupPath, counts *lookupCounts) string {
	library, failure := libraryLookup(document, path)
	if failure != "" {
		return fmt.Sprintf("%v: %s", path, failure)
	}
	reference, nameOnArray := typedReference(documentBytes, path)
	counts.paths++
	if library.found {
		counts.found++
	}
	if !library.equal(reference) {
		return fmt.Sprintf("%v: Lookup gave %v, the typed vendor reference %v", path, library, reference)
	}
	keys, namesOnly := path.names()
	if !namesOnly {
		return ""
	}
	if nameOnArray {
		counts.nameOnArray++
		return ""
	}
	counts.vendorNameCompared++
	if vendor := vendorNameLookup(documentBytes, keys); !library.equal(vendor) {
		return fmt.Sprintf("%v: Lookup gave %v, vendorbson.Raw.LookupErr %v", path, library, vendor)
	}
	return ""
}

// maxLookupDepth bounds how deep the path enumeration descends.
const maxLookupDepth = 12

// lookupPathsOf lists paths into documentBytes, found and missing alike: every
// member by name, every array element by position (and by its key, which
// the typed rule refuses), and around each container the probes a resolver
// can get wrong: a missing name, the empty name, a position past the end, a
// negative position, a position on a document, and a step through a scalar.
// It stops at maxPaths.
func lookupPathsOf(documentBytes []byte, maxPaths int) []lookupPath {
	paths := []lookupPath{nil, {nameStep("a"), nameStep("b")}}
	var visit func(prefix lookupPath, value vendorbsoncore.Value, depth int)
	visit = func(prefix lookupPath, value vendorbsoncore.Value, depth int) {
		if len(paths) >= maxPaths {
			return
		}
		isArray := value.Type == vendorbson.TypeArray
		if !isArray && value.Type != vendorbson.TypeEmbeddedDocument {
			paths = append(paths, prefix.with(nameStep("x")), prefix.with(positionStep(0)))
			return
		}
		elements, _ := vendorbsoncore.Document(value.Data).Elements()
		paths = append(paths,
			prefix.with(nameStep("absent")), prefix.with(nameStep("")),
			prefix.with(positionStep(len(elements))), prefix.with(positionStep(-1)), prefix.with(positionStep(0)))
		for position, element := range elements {
			if len(paths) >= maxPaths {
				return
			}
			byName := prefix.with(nameStep(string(element.KeyBytes())))
			paths = append(paths, byName)
			next := byName
			if isArray {
				next = prefix.with(positionStep(position))
				paths = append(paths, next)
			}
			if depth < maxLookupDepth {
				visit(next, element.Value(), depth+1)
			}
		}
	}
	visit(nil, vendorbsoncore.Value{Type: vendorbson.TypeEmbeddedDocument, Data: documentBytes}, 1)
	if len(paths) > maxPaths {
		paths = paths[:maxPaths]
	}
	return paths
}

// lookupDocumentDivergence runs every enumerated path of one validated document.
func lookupDocumentDivergence(documentBytes []byte, document bson.Document, maxPaths int, counts *lookupCounts) string {
	for _, path := range lookupPathsOf(documentBytes, maxPaths) {
		if divergence := lookupDivergence(documentBytes, document, path, counts); divergence != "" {
			return divergence
		}
	}
	return ""
}

// generatedLookupDocuments are hand-built shapes the corpus lacks (duplicate
// keys at every level, empty keys, a document key "0", arrays whose keys are
// out of sequence or repeated, a code-with-scope on the way) plus seeded
// random trees over a five-key alphabet, which repeats keys by design.
func generatedLookupDocuments() [][]byte {
	documents := [][]byte{
		documentOf(elementOf(0x10, "k", int32Of(1)), elementOf(0x10, "k", int32Of(2))),
		documentOf(
			elementOf(0x03, "d", documentOf(elementOf(0x10, "k", int32Of(1)), elementOf(0x02, "k", stringValueOf("second")))),
			elementOf(0x03, "d", documentOf(elementOf(0x10, "k", int32Of(3)))),
		),
		documentOf(elementOf(0x02, "", stringValueOf("empty")), elementOf(0x0A, "", nil)),
		documentOf(elementOf(0x02, "0", stringValueOf("key zero")), elementOf(0x04, "a", documentOf(elementOf(0x02, "0", stringValueOf("x"))))),
		documentOf(elementOf(0x04, "a", documentOf(
			elementOf(0x02, "3", stringValueOf("three")),
			elementOf(0x02, "3", stringValueOf("three again")),
			elementOf(0x02, "0", stringValueOf("zero")),
			elementOf(0x03, "x", documentOf(elementOf(0x10, "y", int32Of(5)))),
		))),
		documentOf(elementOf(0x0F, "c", codeWithScopeOf("f()", documentOf(elementOf(0x10, "x", int32Of(1)))))),
		documentOf(elementOf(0x04, "a", documentOf(elementOf(0x04, "0", documentOf(elementOf(0x03, "0", documentOf(elementOf(0x10, "b", int32Of(4))))))))),
		nestedDocumentOf(40),
	}
	random := rand.New(rand.NewPCG(2, 0xB50))
	for range 300 {
		documents = append(documents, randomLookupDocument(random, 1, false))
	}
	return documents
}

// randomLookupKeys repeat often, so duplicate keys and "0" keys are common.
var randomLookupKeys = []string{"a", "b", "0", "1", ""}

// randomLookupDocument is a random document (or, isArray, an array) of up
// to six elements, nesting at most five levels.
func randomLookupDocument(random *rand.Rand, depth int, isArray bool) []byte {
	var elements [][]byte
	for position := range random.IntN(7) {
		key := randomLookupKeys[random.IntN(len(randomLookupKeys))]
		if isArray && random.IntN(4) > 0 {
			key = strconv.Itoa(position)
		}
		switch kind := random.IntN(8); {
		case kind == 0 && depth < 5:
			elements = append(elements, elementOf(0x03, key, randomLookupDocument(random, depth+1, false)))
		case kind == 1 && depth < 5:
			elements = append(elements, elementOf(0x04, key, randomLookupDocument(random, depth+1, true)))
		case kind == 2:
			elements = append(elements, elementOf(0x02, key, stringValueOf(strconv.Itoa(random.IntN(1000)))))
		case kind == 3:
			elements = append(elements, elementOf(0x0A, key, nil))
		case kind == 4:
			elements = append(elements, elementOf(0x0F, key, codeWithScopeOf("f", documentOf(elementOf(0x10, "a", int32Of(1))))))
		default:
			elements = append(elements, elementOf(0x10, key, int32Of(random.Int32())))
		}
	}
	return documentOf(elements...)
}

// TestLookupMatchesVendorLookupErr is the library half of the resolver
// differential (build doc step 2b): over the corpus, the vectors and the
// generated documents, every enumerated path agrees with the typed vendor
// reference, and every name-only path that takes no name into an array
// agrees with vendorbson.Raw.LookupErr on found, type and bytes. That
// includes the first match on a duplicate key.
func TestLookupMatchesVendorLookupErr(t *testing.T) {
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
	inputs = append(append(inputs, ref450), generatedLookupDocuments()...)
	var counts lookupCounts
	documents := 0
	for _, input := range inputs {
		document, err := bson.Validate(input, bson.Limits{Depth: bson.MaxNestingCeiling})
		if err != nil {
			continue
		}
		documents++
		if divergence := lookupDocumentDivergence(input, document, 4096, &counts); divergence != "" {
			t.Errorf("% x: %s", input, divergence)
		}
	}
	t.Logf("%d documents, %d paths, %d found, %d compared with LookupErr itself, %d names into an array",
		documents, counts.paths, counts.found, counts.vendorNameCompared, counts.nameOnArray)
	if counts.found == 0 || counts.vendorNameCompared == 0 || counts.nameOnArray == 0 {
		t.Fatalf("the run exercised too little: %+v", counts)
	}
}

// TestLookupAllocatesNothing is spec 9.2's zero-allocation lookup, on Ref450:
// a top-level member, a nested one, an array position and a miss.
func TestLookupAllocatesNothing(t *testing.T) {
	if raceEnabled {
		t.Skip("-race changes allocation counts; the gates run this test without it")
	}
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		t.Fatal(err)
	}
	document, err := bson.Validate(ref450, bson.Limits{Depth: 3})
	if err != nil {
		t.Fatal(err)
	}
	// Each Path literal is written inside its measured closure, as a caller
	// writes it, so the count includes building the path.
	for _, lookup := range []struct {
		name  string
		found bool
		run   func() (bool, error)
	}{
		{"top", true, func() (bool, error) {
			_, found, err := document.Lookup(bson.Path{bson.Name("address")})
			return found, err
		}},
		{"nested", true, func() (bool, error) {
			_, found, err := document.Lookup(bson.Path{bson.Name("address"), bson.Name("country")})
			return found, err
		}},
		{"position", true, func() (bool, error) {
			_, found, err := document.Lookup(bson.Path{bson.Name("tags"), bson.Position(2)})
			return found, err
		}},
		{"miss", false, func() (bool, error) {
			_, found, err := document.Lookup(bson.Path{bson.Name("absent")})
			return found, err
		}},
	} {
		var found bool
		var lookupErr error
		allocations := testing.AllocsPerRun(100, func() { found, lookupErr = lookup.run() })
		if found != lookup.found || lookupErr != nil {
			t.Fatalf("%s: found is %v and err %v, want %v and nil", lookup.name, found, lookupErr, lookup.found)
		}
		if allocations != 0 {
			t.Errorf("%s: Lookup allocates %v times, want 0", lookup.name, allocations)
		}
	}
}
