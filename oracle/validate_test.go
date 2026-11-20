// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/corpus"
)

// The library half of the validity oracle (spec 6.2, call C3): Validate
// accepts exactly when the server's depth scan accepts at the site's limit
// and the vendor decodes the bytes into bson.D. The depth scan is BSL code
// the library cannot import, so here the limit is the library ceiling and
// the inputs are shallower than it; the hoardDB half runs the full
// conjunction against the scan itself at every site limit.
//
// One rule of the scan is stricter than the D decode at any depth: the
// outer length of an old binary (subtype 0x02). The decoder reads only the
// inner length, and the scan holds the outer one to the bytes and to the
// inner (server/nesting_depth.go:binaryEnd). Where only that rule separates
// the two, the library's refusal is the scan's, and allowed here.

// vendorDecodeOutcome is the vendor's decode into bson.D: "ok", its error
// text, or "panic: ..." (a panic is a refusal, never a harness crash).
func vendorDecodeOutcome(documentBytes []byte) string {
	var orderedTarget vendorbson.D
	return vendorOutcome(func() error { return vendorbson.Unmarshal(documentBytes, &orderedTarget) })
}

// validateOutcome runs the library's Validate at the ceiling, turning a
// panic into a failure the caller reports: Validate never panics.
func validateOutcome(documentBytes []byte) (panicked string, validateErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panicked = fmt.Sprint(recovered)
		}
	}()
	_, validateErr = bson.Validate(documentBytes, bson.Limits{Depth: bson.MaxNestingCeiling})
	return "", validateErr
}

// validateDivergence compares Validate with the vendor's decode into
// bson.D, and describes the difference, or returns "" when they agree.
func validateDivergence(documentBytes []byte) string {
	panicked, validateErr := validateOutcome(documentBytes)
	if panicked != "" {
		return "Validate panicked: " + panicked
	}
	vendorOutcomeText := vendorDecodeOutcome(documentBytes)
	vendorAccepts := vendorOutcomeText == "ok"
	switch {
	case (validateErr == nil) == vendorAccepts:
		return ""
	case validateErr == nil:
		return "Validate accepted bytes the vendor's bson.D decode refuses (" + vendorOutcomeText + ")"
	case errors.Is(validateErr, bson.ErrNestingTooDeep):
		// Deeper than the ceiling: outside this half of the oracle.
		return ""
	case isOldBinaryDisagreement(validateErr):
		return ""
	}
	return fmt.Sprintf("Validate refused bytes the vendor's bson.D decode accepts: %v", validateErr)
}

// isOldBinaryDisagreement reports a refusal by the scan's old-binary rule.
func isOldBinaryDisagreement(validateErr error) bool {
	var validationError *bson.ValidationError
	return errors.As(validateErr, &validationError) && strings.Contains(validationError.Reason, "old binary")
}

// probeCase is one malformed input of a spec 6.2 probe class and a
// well-formed twin that differs from it only in the probed bytes.
type probeCase struct {
	name      string
	malformed []byte
	twin      []byte
}

// probeCases are the probe classes of spec 6.2 and the build doc's 2a table.
func probeCases() []probeCase {
	booleanTrue := documentOf(elementOf(0x08, "b", []byte{1}))
	text := documentOf(elementOf(0x02, "s", stringValueOf("hoard")))
	threeLevels := documentOf(elementOf(0x03, "d", documentOf(elementOf(0x04, "a", documentOf(elementOf(0x10, "0", int32Of(7)))))))
	// Offsets in threeLevels: root length 0, level 2's length 7, level 3's 14;
	// with L its length, level 2 holds L-8 bytes and level 3 L-16.
	scope := documentOf(elementOf(0x10, "x", int32Of(1)))
	codeWithScope := documentOf(elementOf(0x0F, "c", codeWithScopeOf("f()", scope)))
	pointer := documentOf(elementOf(0x0C, "p", append(stringValueOf("ns"), make([]byte, 12)...)))
	binaryValue := documentOf(elementOf(0x05, "b", binaryValueOf(0x00, []byte{1, 2, 3})))
	// The pointer with one object-id byte dropped and the root length fixed.
	shortObjectID := withInt32At(append(append([]byte(nil), pointer[:len(pointer)-2]...), 0), 0, int32(len(pointer)-1))
	oldBinary := documentOf(elementOf(0x05, "b", binaryValueOf(0x02, append(int32Of(3), 1, 2, 3))))
	return []probeCase{
		{"boolean byte 0x02", withByteAt(booleanTrue, 7, 2), booleanTrue},
		{"boolean byte 0xFF", withByteAt(booleanTrue, 7, 0xFF), booleanTrue},
		{"unterminated string", withByteAt(text, len(text)-2, 'x'), text},
		{"string length zero", withInt32At(text, 7, 0), text},
		{"string length negative", withInt32At(text, 7, -1), text},
		{"string length past the bytes", withInt32At(text, 7, 100), text},
		{"root length one short", withInt32At(threeLevels, 0, int32(len(threeLevels)-1)), threeLevels},
		{"root length one long", withInt32At(threeLevels, 0, int32(len(threeLevels)+1)), threeLevels},
		{"level 2 length one short", withInt32At(threeLevels, 7, int32(len(threeLevels)-9)), threeLevels},
		{"level 2 length one long", withInt32At(threeLevels, 7, int32(len(threeLevels)-7)), threeLevels},
		{"level 3 length one short", withInt32At(threeLevels, 14, int32(len(threeLevels)-17)), threeLevels},
		{"level 3 length one long", withInt32At(threeLevels, 14, int32(len(threeLevels)-15)), threeLevels},
		{"level 3 length negative", withInt32At(threeLevels, 14, -1), threeLevels},
		{"missing root terminator", withByteAt(threeLevels, len(threeLevels)-1, 1), threeLevels},
		{"missing level 3 terminator", withByteAt(threeLevels, len(threeLevels)-3, 1), threeLevels},
		{"invalid type byte 0x14", withByteAt(booleanTrue, 4, 0x14), booleanTrue},
		{"invalid type byte 0x20", withByteAt(booleanTrue, 4, 0x20), booleanTrue},
		{"invalid type byte 0x80", withByteAt(booleanTrue, 4, 0x80), booleanTrue},
		{"short binary", withInt32At(binaryValue, 7, 4), binaryValue},
		{"binary length negative", withInt32At(binaryValue, 7, -1), binaryValue},
		{"old binary inner length past the bytes", withInt32At(oldBinary, 12, 100), oldBinary},
		{"code with scope total too long", withInt32At(codeWithScope, 7, int32(len(codeWithScope)-7)), codeWithScope},
		{"code with scope total too short", withInt32At(codeWithScope, 7, int32(len(codeWithScope)-9)), codeWithScope},
		{"code with scope inner scope length", withInt32At(codeWithScope, 19, int32(len(scope)+1)), codeWithScope},
		{"code with scope code length zero", withInt32At(codeWithScope, 11, 0), codeWithScope},
		{"DBPointer unterminated namespace", withByteAt(pointer, 13, 'x'), pointer},
		{"DBPointer short object id", shortObjectID, pointer},
		{"DBPointer namespace length negative", withInt32At(pointer, 7, -1), pointer},
	}
}

func TestValidateRefusesEachProbeClass(t *testing.T) {
	for _, probe := range probeCases() {
		t.Run(probe.name, func(t *testing.T) {
			if vendorDecodeOutcome(probe.twin) != "ok" {
				t.Fatalf("the twin is not well-formed for the vendor: %s", vendorDecodeOutcome(probe.twin))
			}
			if vendorDecodeOutcome(probe.malformed) == "ok" {
				t.Fatal("the vendor accepts the probe, so it probes nothing")
			}
			for _, input := range [][]byte{probe.twin, probe.malformed} {
				if divergence := validateDivergence(input); divergence != "" {
					t.Errorf("% x: %s", input, divergence)
				}
			}
			if _, err := bson.Validate(probe.malformed, bson.Limits{Depth: 3}); !errors.Is(err, bson.ErrMalformedDocument) {
				t.Errorf("the probe gave %v, want an error matching ErrMalformedDocument", err)
			}
		})
	}
}

// TestValidateRefusesBadFraming: the root framings that panic the vendor's
// Raw.Validate are refused, with an error and no panic.
func TestValidateRefusesBadFraming(t *testing.T) {
	for _, framing := range framingSeeds() {
		panicked, validateErr := validateOutcome(framing)
		if panicked != "" {
			t.Errorf("% x: Validate panicked: %s", framing, panicked)
			continue
		}
		if !errors.Is(validateErr, bson.ErrMalformedDocument) {
			t.Errorf("% x: got %v, want an error matching ErrMalformedDocument", framing, validateErr)
		}
		if divergence := validateDivergence(framing); divergence != "" {
			t.Errorf("% x: %s", framing, divergence)
		}
	}
}

// TestValidateMatchesVendorDOnTheCorpus runs every corpus case and vector
// through both sides at the ceiling, far above any case's depth.
func TestValidateMatchesVendorDOnTheCorpus(t *testing.T) {
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
	accepted := 0
	for _, input := range inputs {
		if divergence := validateDivergence(input); divergence != "" {
			t.Errorf("% x: %s", input, divergence)
		}
		if _, err := bson.Validate(input, bson.Limits{Depth: bson.MaxNestingCeiling}); err == nil {
			accepted++
		}
	}
	t.Logf("%d inputs, %d accepted", len(inputs), accepted)
}

// TestValidateAcceptsInvalidUTF8 is ruling O10: no UTF-8 check, in keys or
// in strings, as the vendor.
func TestValidateAcceptsInvalidUTF8(t *testing.T) {
	for _, input := range [][]byte{
		documentOf(elementOf(0x02, "\xff\xfe", stringValueOf("ok"))),
		documentOf(elementOf(0x02, "s", stringValueOf("\xc3\x28"))),
		documentOf(elementOf(0x0E, "y", stringValueOf("\xed\xa0\x80"))),
		documentOf(elementOf(0x03, "\x80", documentOf(elementOf(0x02, "\xc0\xaf", stringValueOf("\xff"))))),
	} {
		if _, err := bson.Validate(input, bson.Limits{Depth: 2}); err != nil {
			t.Errorf("% x: %v", input, err)
		}
		if divergence := validateDivergence(input); divergence != "" {
			t.Errorf("% x: %s", input, divergence)
		}
	}
}

// TestValidateAcceptsLegacyTypes covers spec 6.2's legacy types, and every
// other element type beside them, against the vendor.
func TestValidateAcceptsLegacyTypes(t *testing.T) {
	objectID := make([]byte, 12)
	for _, element := range []struct {
		name    string
		element []byte
	}{
		{"code with scope", elementOf(0x0F, "c", codeWithScopeOf("f()", documentOf(elementOf(0x10, "x", int32Of(1)))))},
		{"code with scope, empty scope", elementOf(0x0F, "c", codeWithScopeOf("", documentOf()))},
		{"DBPointer", elementOf(0x0C, "p", append(stringValueOf("db.c"), objectID...))},
		{"symbol", elementOf(0x0E, "y", stringValueOf("sym"))},
		{"undefined", elementOf(0x06, "u", nil)},
		{"double", elementOf(0x01, "d", make([]byte, 8))},
		{"binary", elementOf(0x05, "b", binaryValueOf(0x04, make([]byte, 16)))},
		{"old binary", elementOf(0x05, "b", binaryValueOf(0x02, append(int32Of(2), 1, 2)))},
		{"old binary, short", elementOf(0x05, "b", binaryValueOf(0x02, []byte{1, 2, 3}))},
		{"object id", elementOf(0x07, "o", objectID)},
		{"boolean false", elementOf(0x08, "f", []byte{0})},
		{"datetime", elementOf(0x09, "t", make([]byte, 8))},
		{"null", elementOf(0x0A, "n", nil)},
		{"regex", elementOf(0x0B, "r", []byte("^a\x00i\x00"))},
		{"javascript", elementOf(0x0D, "j", stringValueOf("f()"))},
		{"int32", elementOf(0x10, "i", int32Of(-1))},
		{"timestamp", elementOf(0x11, "ts", make([]byte, 8))},
		{"int64", elementOf(0x12, "l", make([]byte, 8))},
		{"decimal128", elementOf(0x13, "m", make([]byte, 16))},
		{"min key", elementOf(0xFF, "min", nil)},
		{"max key", elementOf(0x7F, "max", nil)},
		{"empty array", elementOf(0x04, "a", documentOf())},
	} {
		input := documentOf(element.element)
		if _, err := bson.Validate(input, bson.Limits{Depth: 2}); err != nil {
			t.Errorf("%s: %v", element.name, err)
		}
		if vendorDecodeOutcome(input) != "ok" {
			t.Errorf("%s: the vendor refuses it: %s", element.name, vendorDecodeOutcome(input))
		}
	}
}

// TestValidateAllocatesNothing: one pass, a fixed stack, no copy.
func TestValidateAllocatesNothing(t *testing.T) {
	if raceEnabled {
		t.Skip("-race changes allocation counts; the gates run this test without it")
	}
	ref450, err := benchdata.Ref450Bytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		name          string
		documentBytes []byte
		limits        bson.Limits
	}{
		{"ref450", ref450, bson.Limits{Depth: 101}},
		{"100 deep", nestedDocumentOf(100), bson.Limits{Depth: 101}},
		{"128 deep at the ceiling", nestedDocumentOf(128), bson.Limits{Depth: bson.MaxNestingCeiling}},
	} {
		var validateErr error
		allocations := testing.AllocsPerRun(100, func() {
			_, validateErr = bson.Validate(input.documentBytes, input.limits)
		})
		if validateErr != nil {
			t.Fatalf("%s: %v", input.name, validateErr)
		}
		if allocations != 0 {
			t.Errorf("%s: Validate allocates %v times, want 0", input.name, allocations)
		}
	}
}

// TestValidateRefusesOldBinaryLengthDisagreement is the one place the
// oracle's two halves differ at any depth: the vendor's decoder never reads
// an old binary's (subtype 0x02) outer length, and the server's scan does
// (server/nesting_depth.go:binaryEnd). In the first input the outer length
// says 7 and the inner 0: the decoder reads 0 bytes, then a null element
// "n" from the rest. In the second, found by FuzzValidateDifferential, the
// outer length runs far past the bytes and the inner says 2. The decoder
// accepts both; the scan refuses both, so Validate does.
func TestValidateRefusesOldBinaryLengthDisagreement(t *testing.T) {
	for _, input := range []struct {
		name          string
		documentBytes []byte
	}{
		{"lengths disagree", documentOf(elementOf(0x05, "b", append(int32Of(7), 0x02, 0, 0, 0, 0, 0x0A, 'n', 0)))},
		{"outer length past the bytes", documentOf(elementOf(0x05, "0", append(int32Of(0x30303030), 0x02, 2, 0, 0, 0, '0', '0')))},
	} {
		if outcome := vendorDecodeOutcome(input.documentBytes); outcome != "ok" {
			t.Fatalf("%s: the vendor refuses the input (%s), so it does not show the difference", input.name, outcome)
		}
		_, err := bson.Validate(input.documentBytes, bson.Limits{Depth: 1})
		if !isOldBinaryDisagreement(err) {
			t.Errorf("%s: got %v, want an old-binary length refusal", input.name, err)
		}
	}
}
