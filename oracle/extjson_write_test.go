// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsonrw "go.mongodb.org/mongo-driver/bson/bsonrw"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/internal/extjson"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/corpus"
)

// The Extended JSON writer's core (build doc step 5a) turns BSON bytes into
// canonical or relaxed Extended JSON text. Its oracle is the vendor's
// MarshalExtJSON of a bson.Raw, the path the hoardDB sites that pass a
// bson.Raw take (cli/ingest_id.go, cmd/gen-testvectors/capture.go), and the
// vendor's RawElement.String, which renders through it. Go values reach
// MarshalExtJSON through the encoder; the step after 4f adds them to these
// tests (docs/core-api.md, "Step 5a core").
//
// The vendor has no depth limit on this path; the library refuses a level
// past the 200 ceiling (spec 5.2). Where that applies is settled by the
// vendor's own reading: the vendor's Copier is run into levelCounter, a
// ValueWriter that writes nothing and fails when a level past the ceiling
// opens. Below the ceiling the text and the error text must be the vendor's.

// errPastCeiling is levelCounter's refusal.
var errPastCeiling = errors.New("opens a level past the ceiling")

// levelCounter is a vendor ValueWriter, DocumentWriter and ArrayWriter that
// counts open levels. The Copier calls WriteDocument, WriteArray and
// WriteCodeWithScope after it has read the container's header, so a
// refusal here lands where the library's writer refuses.
type levelCounter struct{ level int }

func (counter *levelCounter) open() error {
	counter.level++
	if counter.level > bson.MaxNestingCeiling {
		return errPastCeiling
	}
	return nil
}

func (counter *levelCounter) WriteDocument() (vendorbsonrw.DocumentWriter, error) {
	return counter, counter.open()
}

func (counter *levelCounter) WriteArray() (vendorbsonrw.ArrayWriter, error) {
	return counter, counter.open()
}

func (counter *levelCounter) WriteCodeWithScope(string) (vendorbsonrw.DocumentWriter, error) {
	return counter, counter.open()
}

func (counter *levelCounter) WriteDocumentEnd() error { counter.level--; return nil }
func (counter *levelCounter) WriteArrayEnd() error    { counter.level--; return nil }
func (counter *levelCounter) WriteDocumentElement(string) (vendorbsonrw.ValueWriter, error) {
	return counter, nil
}
func (counter *levelCounter) WriteArrayElement() (vendorbsonrw.ValueWriter, error) {
	return counter, nil
}
func (*levelCounter) WriteBinary([]byte) error                              { return nil }
func (*levelCounter) WriteBinaryWithSubtype([]byte, byte) error             { return nil }
func (*levelCounter) WriteBoolean(bool) error                               { return nil }
func (*levelCounter) WriteDBPointer(string, vendorprimitive.ObjectID) error { return nil }
func (*levelCounter) WriteDateTime(int64) error                             { return nil }
func (*levelCounter) WriteDecimal128(vendorprimitive.Decimal128) error      { return nil }
func (*levelCounter) WriteDouble(float64) error                             { return nil }
func (*levelCounter) WriteInt32(int32) error                                { return nil }
func (*levelCounter) WriteInt64(int64) error                                { return nil }
func (*levelCounter) WriteJavascript(string) error                          { return nil }
func (*levelCounter) WriteMaxKey() error                                    { return nil }
func (*levelCounter) WriteMinKey() error                                    { return nil }
func (*levelCounter) WriteNull() error                                      { return nil }
func (*levelCounter) WriteObjectID(vendorprimitive.ObjectID) error          { return nil }
func (*levelCounter) WriteRegex(string, string) error                       { return nil }
func (*levelCounter) WriteString(string) error                              { return nil }
func (*levelCounter) WriteSymbol(string) error                              { return nil }
func (*levelCounter) WriteTimestamp(uint32, uint32) error                   { return nil }
func (*levelCounter) WriteUndefined() error                                 { return nil }

// vendorOpensPastCeiling reports whether the vendor's walk of document
// opens a level past the ceiling before it stops. rootLevel is the level of
// the document's own parent: 0 for a document, -1 for the document
// RawElement.String wraps around an element, whose value is level 1 as in
// RawElement.DebugString.
func vendorOpensPastCeiling(document []byte, rootLevel int) (opens bool) {
	defer func() {
		if recover() != nil {
			opens = false
		}
	}()
	// The Copier is deprecated, and it is the code MarshalExtJSON runs for a
	// bson.Raw, so it is the walk to count.
	err := vendorbsonrw.Copier{}.CopyDocumentFromBytes(&levelCounter{level: rootLevel}, document) //nolint:staticcheck // SA1019, see above.
	return errors.Is(err, errPastCeiling)
}

// writeOutcome is one side's result: the text and "ok", the error text, or
// "panic: ..." when it panicked.
type writeOutcome struct {
	text    string
	outcome string
	err     error
}

func vendorWrite(document []byte, canonical, escapeHTML bool) (result writeOutcome) {
	result.outcome = vendorOutcome(func() error {
		text, err := vendorbson.MarshalExtJSON(vendorbson.Raw(document), canonical, escapeHTML)
		result.text = string(text)
		return err
	})
	return result
}

func libraryWrite(document []byte, canonical, escapeHTML bool) (result writeOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result.outcome = fmt.Sprintf("panic: %v", recovered)
		}
	}()
	text, err := extjson.AppendText(nil, document, canonical, escapeHTML)
	result.text, result.err = string(text), err
	if err != nil {
		result.outcome = err.Error()
		return result
	}
	result.outcome = "ok"
	return result
}

// nestingRefusalText is the library writer's error past the ceiling.
var nestingRefusalText = fmt.Sprintf("%v: more than %d levels", extjson.ErrNestingTooDeep, bson.MaxNestingCeiling)

// writeDivergence compares the library's writer with the vendor's
// MarshalExtJSON on one document in one mode, and says how they differ, or
// returns "". The vendor panics on some framings (a declared length of 0 to
// 3, a value length that overflows int32); there the library must return an
// error instead.
func writeDivergence(document []byte, canonical, escapeHTML bool) string {
	library := libraryWrite(slices.Clone(document), canonical, escapeHTML)
	if strings.HasPrefix(library.outcome, "panic: ") {
		return "library " + library.outcome
	}
	if vendorOpensPastCeiling(slices.Clone(document), 0) {
		if library.outcome != nestingRefusalText || !errors.Is(library.err, extjson.ErrNestingTooDeep) {
			return fmt.Sprintf("the vendor's walk opens a level past %d; library gave %q, want %q", bson.MaxNestingCeiling, clipped(library.outcome), nestingRefusalText)
		}
		return ""
	}
	vendor := vendorWrite(slices.Clone(document), canonical, escapeHTML)
	if strings.HasPrefix(vendor.outcome, "panic: ") {
		if library.err == nil || errors.Is(library.err, extjson.ErrNestingTooDeep) {
			return fmt.Sprintf("vendor %s; library gave %q, want a refusal", vendor.outcome, clipped(library.outcome))
		}
		return ""
	}
	if library.outcome != vendor.outcome {
		return fmt.Sprintf("library %q, vendor %q", clipped(library.outcome), clipped(vendor.outcome))
	}
	if library.text != vendor.text {
		return fmt.Sprintf("text:\n library %s\n  vendor %s", clipped(library.text), clipped(vendor.text))
	}
	return ""
}

// writeModes are the four (canonical, escapeHTML) settings.
var writeModes = [][2]bool{{true, false}, {false, false}, {true, true}, {false, true}}

func modeLabel(canonical, escapeHTML bool) string {
	return fmt.Sprintf("canonical=%t escapeHTML=%t", canonical, escapeHTML)
}

// corpusWriteInputs are a corpus file's byte inputs: every valid case's
// canonical, degenerate and converted BSON, and the decode errors.
func corpusWriteInputs(corpusFile corpus.File) (labels []string, documents [][]byte) {
	for caseIndex, validCase := range corpusFile.Valid {
		prefix := fmt.Sprintf("valid[%d] %s", caseIndex, validCase.Description)
		labels, documents = append(labels, prefix+" canonical_bson"), append(documents, validCase.CanonicalBSON)
		if validCase.DegenerateBSON != nil {
			labels, documents = append(labels, prefix+" degenerate_bson"), append(documents, validCase.DegenerateBSON)
		}
		if validCase.ConvertedBSON != nil {
			labels, documents = append(labels, prefix+" converted_bson"), append(documents, validCase.ConvertedBSON)
		}
	}
	for caseIndex, decodeError := range corpusFile.DecodeErrors {
		labels = append(labels, fmt.Sprintf("decodeErrors[%d] %s", caseIndex, decodeError.Description))
		documents = append(documents, decodeError.BSON)
	}
	return labels, documents
}

// compactJSON is text with insignificant space removed, or text as it is
// when it is not JSON. The corpus writes its expectations with spaces.
func compactJSON(text string) string {
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, []byte(text)); err != nil {
		return text
	}
	return compacted.String()
}

// corpusExpectation is the corpus's own text for a valid case in a mode:
// canonical_extjson, or relaxed_extjson where the case has one.
func corpusExpectation(validCase corpus.ValidCase, canonical bool) string {
	if !canonical && validCase.RelaxedExtJSON != nil {
		return *validCase.RelaxedExtJSON
	}
	return validCase.CanonicalExtJSON
}

// runCorpusWrite is the corpus differential in one mode (both escapeHTML
// settings), with a table per file: inputs, runs, vendor-accepted, matches,
// and how often the vendor's canonical_bson text equals the corpus's own
// expectation, which is reported, not required (spec 9.1.4: the vendor's
// outcome is the oracle).
func runCorpusWrite(t *testing.T, canonical bool) {
	t.Helper()
	corpusFiles, err := corpus.Load()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	var table []string
	totalRuns, totalMatches := 0, 0
	for _, corpusFile := range corpusFiles {
		labels, documents := corpusWriteInputs(corpusFile)
		runs, matches, accepted, agrees := 0, 0, 0, 0
		for position, document := range documents {
			for _, escapeHTML := range []bool{false, true} {
				runs++
				if vendorWrite(document, canonical, escapeHTML).outcome == "ok" {
					accepted++
				}
				if divergence := writeDivergence(document, canonical, escapeHTML); divergence != "" {
					t.Errorf("%s %s, %s: %s", corpusFile.Name, labels[position], modeLabel(canonical, escapeHTML), divergence)
					continue
				}
				matches++
			}
		}
		for _, validCase := range corpusFile.Valid {
			if vendorWrite(validCase.CanonicalBSON, canonical, false).text == compactJSON(corpusExpectation(validCase, canonical)) {
				agrees++
			}
		}
		totalRuns, totalMatches = totalRuns+runs, totalMatches+matches
		table = append(table, fmt.Sprintf("%-28s inputs %4d  runs %4d  vendor-accepted %4d  matches %4d  vendor-equals-corpus %3d/%d",
			corpusFile.Name, len(documents), runs, accepted, matches, agrees, len(corpusFile.Valid)))
	}
	if totalRuns == 0 {
		t.Fatal("the corpus gave no inputs, so nothing was compared")
	}
	t.Logf("canonical=%t, per file:\n%s\ntotal runs %d, matches %d", canonical, strings.Join(table, "\n"), totalRuns, totalMatches)
}

// TestMarshalExtJSONCanonicalMatchesVendorOnTheCorpus holds the writer to
// the vendor's canonical text over every corpus byte input.
func TestMarshalExtJSONCanonicalMatchesVendorOnTheCorpus(t *testing.T) {
	runCorpusWrite(t, true)
}

// TestMarshalExtJSONRelaxedMatchesVendorOnTheCorpus is the relaxed mode.
func TestMarshalExtJSONRelaxedMatchesVendorOnTheCorpus(t *testing.T) {
	runCorpusWrite(t, false)
}

func doubleDocument(number float64) []byte {
	return documentOf(elementOf(0x01, "d", doubleBytes(number)))
}

// TestExtJSONDoubleFormatting runs the double formatting's edges in both
// modes: integral values (1.0 against 1), the exponent form at both ends,
// -0, NaN with several payloads, the infinities, and the largest and
// smallest magnitudes. A few renderings are also stated, so that a change
// in the vendor shows here as well as in the differential.
func TestExtJSONDoubleFormatting(t *testing.T) {
	numbers := []float64{
		0, math.Copysign(0, -1), 1, -1, 1.5, 10, 100000, 999999, 1e6, 1234567, 1e15, 1e20, 1e21, 1e22,
		123456789012345678, 0.1, 0.0001, 0.00001, 1e-7, 1.2345e-300, 5e-324, math.SmallestNonzeroFloat64,
		math.MaxFloat64, -math.MaxFloat64, 9007199254740993, 4.35, 2.5e-5, -1e100,
		math.Inf(1), math.Inf(-1), math.NaN(),
		math.Float64frombits(0x7ff0000000000001), math.Float64frombits(0xfff8000000000000), math.Float64frombits(0x7fffffffffffffff),
	}
	for _, number := range numbers {
		for _, mode := range writeModes {
			if divergence := writeDivergence(doubleDocument(number), mode[0], mode[1]); divergence != "" {
				t.Errorf("double %v (bits %016x), %s: %s", number, math.Float64bits(number), modeLabel(mode[0], mode[1]), divergence)
			}
		}
	}
	stated := []struct {
		number    float64
		canonical bool
		text      string
	}{
		{1, false, `{"d":1.0}`},
		{1, true, `{"d":{"$numberDouble":"1.0"}}`},
		{1e21, false, `{"d":1E+21}`},
		{1e-7, false, `{"d":1E-07}`},
		{math.Copysign(0, -1), true, `{"d":{"$numberDouble":"-0.0"}}`},
		{math.NaN(), false, `{"d":{"$numberDouble":"NaN"}}`},
		{math.Inf(-1), false, `{"d":{"$numberDouble":"-Infinity"}}`},
	}
	for _, statedCase := range stated {
		if library := libraryWrite(doubleDocument(statedCase.number), statedCase.canonical, false); library.text != statedCase.text {
			t.Errorf("double %v canonical=%t: %q (%s), want %q", statedCase.number, statedCase.canonical, library.text, library.outcome, statedCase.text)
		}
	}
}

// escapeProbes are the strings the escaping is tried with: every ASCII
// byte alone, the HTML-sensitive bytes, U+2028 and U+2029, the replacement
// character written out, and broken UTF-8 of each kind.
func escapeProbes() []string {
	probes := append([]string{}, stringProbes...)
	for octet := range 0x80 {
		probes = append(probes, string([]byte{byte(octet)}), "a"+string([]byte{byte(octet)})+"z")
	}
	return append(probes,
		"<>&", "a<b>c&d", "\u2028", "\u2029", "x\u2028y\u2029z", "\u2027\u202a", "\ufffd", "\xef\xbf\xbd",
		"\x80", "\xbf", "\xc0\xaf", "\xe2\x80", "\xe2\x80\xa8\xe2", "\xf4\x90\x80\x80", "\xed\xbf\xbf", "a\xffb\xfec",
		"\u007f\u0080\u00ff", "\U0010ffff", strings.Repeat("<\u2028\x00", 50),
	)
}

// escapeDocuments put each probe everywhere the writer escapes text (a
// key, a string, code, a symbol, a regex pattern and its options, a scope's
// code, a key inside a scope) and where it does not (a DBPointer's
// namespace, which the vendor writes as it is).
func escapeDocuments() [][]byte {
	objectID := []byte{0x50, 0x7f, 0x1f, 0x77, 0xbc, 0xf8, 0x6c, 0xd7, 0x99, 0x43, 0x90, 0x11}
	var documents [][]byte
	for _, probe := range escapeProbes() {
		cstring := strings.ReplaceAll(probe, "\x00", "")
		documents = append(documents,
			documentOf(elementOf(0x02, "s", stringValueOf(probe)), elementOf(0x0D, "j", stringValueOf(probe)), elementOf(0x0E, "y", stringValueOf(probe))),
			documentOf(elementOf(0x10, cstring, int32Of(1))),
			documentOf(elementOf(0x0B, "r", []byte(cstring+"\x00"+cstring+"\x00"))),
			documentOf(elementOf(0x0F, "w", codeWithScopeOf(probe, documentOf(elementOf(0x02, cstring, stringValueOf(probe)))))),
			documentOf(elementOf(0x0C, "p", append(stringValueOf(probe), objectID...))),
			documentOf(elementOf(0x04, "a", documentOf(elementOf(0x02, cstring, stringValueOf(probe))))),
		)
	}
	return documents
}

// TestMarshalExtJSONEscapeHTML holds the escaping to the vendor's under
// both values of the escapeHTML argument, in both modes, and checks that
// the argument is honoured.
func TestMarshalExtJSONEscapeHTML(t *testing.T) {
	compared := 0
	for _, document := range escapeDocuments() {
		for _, mode := range writeModes {
			compared++
			if divergence := writeDivergence(document, mode[0], mode[1]); divergence != "" {
				t.Errorf("%s, %s: %s", truncatedHex(document), modeLabel(mode[0], mode[1]), divergence)
			}
		}
	}
	html := documentOf(elementOf(0x02, "s", stringValueOf("<&>")))
	escaped, plain := libraryWrite(html, true, true).text, libraryWrite(html, true, false).text
	if escaped != `{"s":"\u003c\u0026\u003e"}` || plain != `{"s":"<&>"}` {
		t.Errorf("escapeHTML=true gave %q and false gave %q", escaped, plain)
	}
	t.Logf("%d documents and modes compared", compared)
}

// codeWithScopeNest is a document levels deep whose innermost level holds a
// code with scope: the scope is level levels+1.
func codeWithScopeNest(levels int) []byte {
	current := documentOf(elementOf(0x0F, "w", codeWithScopeOf("f()", documentOf(elementOf(0x10, "x", int32Of(1))))))
	for range levels - 1 {
		current = documentOf(elementOf(0x03, "a", current))
	}
	return current
}

// TestMarshalExtJSONRefusesPastTheCeiling holds the writer to the 200
// ceiling (spec 5.2): to the vendor's text up to 200 levels, documents,
// arrays and scopes counted, and to the ErrNestingTooDeep refusal past it.
func TestMarshalExtJSONRefusesPastTheCeiling(t *testing.T) {
	cases := map[string]struct {
		document []byte
		tooDeep  bool
	}{
		"201 levels, then an unreadable element": {deepThenMalformed(), true},
		"a shallow element, then 201 levels":     {shallowThenDeep(), true},
		"100,000 levels":                         {linearlyNestedDocument(100_000), true},
		"a scope at level 200":                   {codeWithScopeNest(199), false},
		"a scope at level 201":                   {codeWithScopeNest(200), true},
		"an unreadable element, then 201 levels": {documentOf(elementOf(0x02, "bad", int32Of(1<<20)), elementOf(0x03, "deep", nestedDocument(200, false))), false},
	}
	for _, useArrays := range []bool{false, true} {
		for _, levels := range []int{1, 2, 199, 200, 201, 202, 1000} {
			cases[fmt.Sprintf("%d levels, arrays=%t", levels, useArrays)] = struct {
				document []byte
				tooDeep  bool
			}{nestedDocument(levels, useArrays), levels > bson.MaxNestingCeiling}
		}
	}
	names := make([]string, 0, len(cases))
	for name := range cases {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		ceilingCase := cases[name]
		if opens := vendorOpensPastCeiling(ceilingCase.document, 0); opens != ceilingCase.tooDeep {
			t.Errorf("%s: the vendor's walk opens past the ceiling: %t, the case says %t", name, opens, ceilingCase.tooDeep)
		}
		for _, mode := range writeModes {
			library := libraryWrite(ceilingCase.document, mode[0], mode[1])
			if ceilingCase.tooDeep && !errors.Is(library.err, extjson.ErrNestingTooDeep) {
				t.Errorf("%s, %s: library gave %q, want the nesting refusal", name, modeLabel(mode[0], mode[1]), clipped(library.outcome))
			}
			if !ceilingCase.tooDeep && vendorWrite(ceilingCase.document, mode[0], mode[1]).outcome != "ok" && library.err == nil {
				t.Errorf("%s, %s: the vendor refuses and the library does not", name, modeLabel(mode[0], mode[1]))
			}
			if divergence := writeDivergence(ceilingCase.document, mode[0], mode[1]); divergence != "" {
				t.Errorf("%s, %s: %s", name, modeLabel(mode[0], mode[1]), divergence)
			}
		}
	}
}

// readerProbes are documents whose nested values exercise the vendor's
// value reader, which reads every value below the root (the root's own
// loop uses bsoncore's functions): containers that declare a length too
// short or too long, so the reader runs past their end; each scalar's
// length and terminator checks; code with scope's component lengths; the
// old binary subtype's inner length; an unnamed type below the root; and
// every value type inside an array and inside a scope.
func readerProbes() [][]byte {
	inner := documentOf(elementOf(0x10, "x", int32Of(1)), elementOf(0x02, "s", stringValueOf("v")))
	nestTwice := func(value []byte, typeByte byte) []byte {
		return documentOf(elementOf(0x03, "o", documentOf(elementOf(typeByte, "a", value), elementOf(0x10, "after", int32Of(9)))))
	}
	var probes [][]byte
	for _, delta := range []int32{-12, -6, -5, -1, 1, 3, 100, -100} {
		edited := withInt32At(inner, 0, int32(len(inner))+delta)
		probes = append(probes, nestTwice(edited, 0x03), nestTwice(edited, 0x04), documentOf(elementOf(0x03, "a", edited)))
		probes = append(probes, nestTwice(codeWithScopeOf("f", edited), 0x0F))
	}
	for _, stringBytes := range [][]byte{
		int32Of(0), int32Of(-1), int32Of(-0x80000000), int32Of(1), append(int32Of(1), 0), append(int32Of(2), 'a', 'b'),
		append(int32Of(40), 'a', 0), append(int32Of(3), 'a', 'b', 0), append(int32Of(1), 7),
	} {
		probes = append(probes, nestTwice(stringBytes, 0x02), nestTwice(stringBytes, 0x0D), nestTwice(stringBytes, 0x0E),
			nestTwice(append(slices.Clone(stringBytes), make([]byte, 12)...), 0x0C))
	}
	for _, booleanByte := range []byte{0, 1, 2, 0x80, 0xff} {
		probes = append(probes, nestTwice([]byte{booleanByte}, 0x08))
	}
	for _, binaryBytes := range [][]byte{
		binaryValueOf(0x00, []byte{1, 2}), withInt32At(binaryValueOf(0x00, []byte{1, 2}), 0, -1), withInt32At(binaryValueOf(0x00, []byte{1, 2}), 0, 9),
		binaryValueOf(0x02, append(int32Of(2), 1, 2)), binaryValueOf(0x02, append(int32Of(-3), 1, 2)), binaryValueOf(0x02, append(int32Of(7), 1, 2)),
		binaryValueOf(0x02, []byte{1, 2, 3, 4}), binaryValueOf(0x02, append(int32Of(0), 9, 9)), binaryValueOf(0x80, nil),
	} {
		probes = append(probes, nestTwice(binaryBytes, 0x05), documentOf(elementOf(0x05, "b", binaryBytes)))
	}
	codeBytes := stringValueOf("f()")
	for _, scope := range [][]byte{
		append(append(int32Of(int32(4+len(codeBytes)+5)), codeBytes...), documentOf()...),
		append(append(int32Of(int32(4+len(codeBytes)+6)), codeBytes...), documentOf()...),
		append(append(int32Of(int32(4+len(codeBytes)+5)), withInt32At(codeBytes, 0, 0)...), documentOf()...),
		append(append(int32Of(int32(4+len(codeBytes)+5)), withInt32At(codeBytes, 0, -2)...), documentOf()...),
		append(append(int32Of(int32(4+len(codeBytes)+5)), withInt32At(codeBytes, 0, 2)...), documentOf()...),
		append(append(int32Of(int32(4+len(codeBytes)+5)), withByteAt(codeBytes, len(codeBytes)-1, 'x')...), documentOf()...),
		append(int32Of(9), codeBytes...),
	} {
		probes = append(probes, nestTwice(scope, 0x0F), documentOf(elementOf(0x0F, "w", scope)))
	}
	for _, unnamed := range []byte{0x00, 0x14, 0x80, 0xFE} {
		probes = append(probes, nestTwice([]byte{1, 2, 3, 4}, unnamed), documentOf(elementOf(0x04, "a", documentOf(elementOf(unnamed, "0", []byte{1, 2, 3, 4})))))
	}
	probes = append(probes,
		nestTwice([]byte("pattern"), 0x0B), nestTwice([]byte("pattern\x00opts"), 0x0B),
		documentOf(elementOf(0x03, "o", []byte{20, 0, 0, 0, 0x10, 'a'})),
		documentOf(elementOf(0x04, "a", documentOf(elementOf(0x10, "0", int32Of(1)))[:9])),
		append(documentOf(elementOf(0x10, "x", int32Of(1))), 0xAB, 0xCD),
		withInt32At(binaryValueOf(0x00, nil), 0, 0x7FFFFFFF),
		documentOf(elementOf(0x05, "b", withInt32At(binaryValueOf(0x00, nil), 0, 0x7FFFFFFF))),
		documentOf(elementOf(0x0C, "p", int32Of(0x7FFFFFF0))),
	)
	for _, scalar := range scalarElements() {
		probes = append(probes,
			documentOf(elementOf(0x04, "a", documentOf(append([]byte{scalar[0], '0', 0}, scalar[bytes.IndexByte(scalar[1:], 0)+2:]...)))),
			documentOf(elementOf(0x0F, "w", codeWithScopeOf("f()", documentOf(scalar)))),
			documentOf(elementOf(0x03, "o", documentOf(scalar))))
	}
	return append(probes, framingSeeds()...)
}

// generatedWriteDocuments are the shared bsoncore inputs (hand-stated
// documents, corpus valid and decode-error bytes, vectors, driver payloads,
// damaged copies), the Raw differential's documents with its depth edges,
// the reader probes and the escape documents.
func generatedWriteDocuments(t *testing.T) [][]byte {
	t.Helper()
	documents := append([][]byte{}, rawDocuments(t)...)
	documents = append(documents, readerProbes()...)
	return append(documents, escapeDocuments()...)
}

// TestExtJSONWriterMatchesVendorOnGeneratedDocuments runs every generated
// document in all four modes.
func TestExtJSONWriterMatchesVendorOnGeneratedDocuments(t *testing.T) {
	documents := generatedWriteDocuments(t)
	mismatches, vendorAccepted, refusedPastCeiling := 0, 0, 0
	for _, document := range documents {
		for _, mode := range writeModes {
			if divergence := writeDivergence(document, mode[0], mode[1]); divergence != "" {
				mismatches++
				if mismatches <= mismatchLimit {
					t.Errorf("%s, %s: %s", truncatedHex(document), modeLabel(mode[0], mode[1]), divergence)
				}
			}
		}
		if vendorWrite(document, true, false).outcome == "ok" {
			vendorAccepted++
		}
		if vendorOpensPastCeiling(document, 0) {
			refusedPastCeiling++
		}
	}
	if mismatches > mismatchLimit {
		t.Errorf("%d mismatches in all", mismatches)
	}
	t.Logf("%d documents, %d runs, %d accepted by the vendor, %d past the ceiling", len(documents), 4*len(documents), vendorAccepted, refusedPastCeiling)
}

// elementDivergence compares RawElement.String with the vendor's on one
// element. Where the vendor's walk opens a level past the ceiling, the
// element's value counted as level 1, the library renders the nesting
// refusal. Where the vendor panics, the library renders "<malformed>".
func elementDivergence(element []byte) string {
	libraryText := invoke(reflect.ValueOf(bson.RawElement(slices.Clone(element))), "String", nil)
	vendorDocument := vendorbsoncore.BuildDocument(nil, slices.Clone(element))
	if vendorOpensPastCeiling(vendorDocument, -1) {
		if libraryText != renderedRefusal {
			return fmt.Sprintf("past the ceiling: library %s, want %s", clipped(libraryText), renderedRefusal)
		}
		return ""
	}
	vendorText := invoke(reflect.ValueOf(vendorbson.RawElement(slices.Clone(element))), "String", nil)
	if strings.HasPrefix(vendorText, "panic(") {
		if libraryText != `string("<malformed>")` {
			return fmt.Sprintf("vendor %s; library %s, want <malformed>", clipped(vendorText), clipped(libraryText))
		}
		return ""
	}
	if libraryText != vendorText {
		return fmt.Sprintf("library %s, vendor %s", clipped(libraryText), clipped(vendorText))
	}
	return ""
}

// TestRawElementStringMatchesVendor runs RawElement.String over the Raw
// differential's elements, every element of the generated documents, and
// their truncations.
func TestRawElementStringMatchesVendor(t *testing.T) {
	documents := generatedWriteDocuments(t)
	elements := rawElements(t, rawDocuments(t))
	for _, document := range documents {
		if len(document) < 5 {
			continue
		}
		rest := document[4:]
		for range 64 {
			element, remaining, ok := vendorbsoncore.ReadElement(rest)
			if !ok {
				break
			}
			elements = append(elements, slices.Clone(element), slices.Clone(element[:len(element)/2]))
			rest = remaining
		}
	}
	mismatches := 0
	for _, element := range elements {
		if divergence := elementDivergence(element); divergence != "" {
			mismatches++
			if mismatches <= mismatchLimit {
				t.Errorf("%s: %s", truncatedHex(element), divergence)
			}
		}
	}
	if mismatches > mismatchLimit {
		t.Errorf("%d mismatches in all", mismatches)
	}
	t.Logf("%d elements compared", len(elements))
}

// TestExtJSONWriterRoundTripsThroughTheReader reads the text each library
// writes back with its own reader: the library's AppendDocument of the
// library's text equals the vendor's UnmarshalExtJSON of the vendor's text,
// bytes and outcome, for every corpus valid case in both modes.
func TestExtJSONWriterRoundTripsThroughTheReader(t *testing.T) {
	corpusFiles, err := corpus.Load()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	trips, sameAsInput := 0, 0
	for _, corpusFile := range corpusFiles {
		for caseIndex, validCase := range corpusFile.Valid {
			for _, canonical := range []bool{true, false} {
				library, vendor := libraryWrite(validCase.CanonicalBSON, canonical, false), vendorWrite(validCase.CanonicalBSON, canonical, false)
				if library.err != nil || vendor.outcome != "ok" {
					continue
				}
				trips++
				libraryBack, vendorBack := libraryRawExtJSON([]byte(library.text), canonical), vendorRawExtJSON([]byte(vendor.text), canonical)
				if libraryBack.outcome != vendorBack.outcome || !bytes.Equal(libraryBack.documentBytes, vendorBack.documentBytes) {
					t.Errorf("%s valid[%d] %s canonical=%t: read back, library %q %x, vendor %q %x", corpusFile.Name, caseIndex, validCase.Description,
						canonical, libraryBack.outcome, libraryBack.documentBytes, vendorBack.outcome, vendorBack.documentBytes)
				}
				if bytes.Equal(libraryBack.documentBytes, validCase.CanonicalBSON) {
					sameAsInput++
				}
			}
		}
	}
	if trips == 0 {
		t.Fatal("no corpus case was written by both libraries")
	}
	t.Logf("%d round trips, %d back to the input bytes", trips, sameAsInput)
}
