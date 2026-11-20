// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	libprimitive "github.com/hoarddb/hoarddb-bson/bson/primitive"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
)

// Decimal128 values are compared by their two 64-bit halves, never through
// compare.Decoded: the halves are the encoded bytes, and comparing them also
// keeps NaN cases exact (compare.Decoded treats identical NaNs as different
// until step 1d's fix lands).

// decimalCorpusFiles is the vendor corpus's Decimal128 file list at v1.17.10.
var decimalCorpusFiles = []string{
	"decimal128-1.json", "decimal128-2.json", "decimal128-3.json", "decimal128-4.json",
	"decimal128-5.json", "decimal128-6.json", "decimal128-7.json",
}

type decimalCorpusValid struct {
	Description       string `json:"description"`
	CanonicalBSON     string `json:"canonical_bson"`
	CanonicalExtJSON  string `json:"canonical_extjson"`
	DegenerateExtJSON string `json:"degenerate_extjson"`
	Lossy             bool   `json:"lossy"`
}

type decimalCorpusParseError struct {
	Description string `json:"description"`
	String      string `json:"string"`
}

type decimalCorpusFile struct {
	Valid       []decimalCorpusValid      `json:"valid"`
	ParseErrors []decimalCorpusParseError `json:"parseErrors"`
}

func loadDecimalCorpus(t *testing.T, fileName string) decimalCorpusFile {
	t.Helper()
	vendorDir := goListModule(t, "{{.Dir}}", vendorModulePath)
	corpusBytes, err := os.ReadFile(filepath.Join(vendorDir, "testdata", "bson-corpus", fileName))
	if err != nil {
		t.Fatalf("reading corpus file %s: %v", fileName, err)
	}
	var corpus decimalCorpusFile
	if err := json.Unmarshal(corpusBytes, &corpus); err != nil {
		t.Fatalf("decoding corpus file %s: %v", fileName, err)
	}
	return corpus
}

// corpusDecimalHalves returns the high and low halves of the Decimal128 in a
// corpus document {"d": <decimal128>}: int32 length, type 0x13, "d\x00", 16
// little-endian bytes, terminator.
func corpusDecimalHalves(t *testing.T, canonicalHex string) (uint64, uint64) {
	t.Helper()
	documentBytes, err := hex.DecodeString(canonicalHex)
	if err != nil {
		t.Fatalf("decoding canonical_bson %q: %v", canonicalHex, err)
	}
	if len(documentBytes) != 24 || documentBytes[4] != 0x13 || documentBytes[5] != 'd' || documentBytes[6] != 0 {
		t.Fatalf("canonical_bson %q is not a {d: decimal128} document", canonicalHex)
	}
	payload := documentBytes[7:23]
	return binary.LittleEndian.Uint64(payload[8:16]), binary.LittleEndian.Uint64(payload[0:8])
}

// corpusDecimalString returns the $numberDecimal string of a corpus
// Extended JSON document {"d": {"$numberDecimal": "..."}}.
func corpusDecimalString(t *testing.T, extJSON string) string {
	t.Helper()
	var wrapper map[string]map[string]string
	if err := json.Unmarshal([]byte(extJSON), &wrapper); err != nil {
		t.Fatalf("decoding extjson %q: %v", extJSON, err)
	}
	decimalText, ok := wrapper["d"]["$numberDecimal"]
	if !ok {
		t.Fatalf("extjson %q has no d.$numberDecimal", extJSON)
	}
	return decimalText
}

func decimalErrorText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// requireSameParse parses decimalText with both libraries and fails unless
// the halves and the error text are equal. It returns the vendor's halves.
func requireSameParse(t *testing.T, decimalText string) (uint64, uint64) {
	t.Helper()
	vendorDecimal, vendorErr := vendorprimitive.ParseDecimal128(decimalText)
	libDecimal, libErr := libprimitive.ParseDecimal128(decimalText)
	vendorHigh, vendorLow := vendorDecimal.GetBytes()
	libHigh, libLow := libDecimal.GetBytes()
	if vendorHigh != libHigh || vendorLow != libLow {
		t.Errorf("ParseDecimal128(%q) halves: library %016x %016x, vendor %016x %016x",
			decimalText, libHigh, libLow, vendorHigh, vendorLow)
	}
	if decimalErrorText(vendorErr) != decimalErrorText(libErr) {
		t.Errorf("ParseDecimal128(%q) error: library %q, vendor %q", decimalText, decimalErrorText(libErr), decimalErrorText(vendorErr))
	}
	return vendorHigh, vendorLow
}

// requireSameString renders the same halves with both libraries.
func requireSameString(t *testing.T, high, low uint64) string {
	t.Helper()
	vendorText := vendorprimitive.NewDecimal128(high, low).String()
	libText := libprimitive.NewDecimal128(high, low).String()
	if vendorText != libText {
		t.Errorf("String of %016x %016x: library %q, vendor %q", high, low, libText, vendorText)
	}
	return vendorText
}

func TestDecimal128StringMatchesVendorOnTheCorpus(t *testing.T) {
	validCount, parseErrorCount := 0, 0
	for _, fileName := range decimalCorpusFiles {
		corpus := loadDecimalCorpus(t, fileName)
		for _, validCase := range corpus.Valid {
			validCount++
			high, low := corpusDecimalHalves(t, validCase.CanonicalBSON)
			renderedText := requireSameString(t, high, low)
			requireSameParse(t, renderedText)
			canonicalText := corpusDecimalString(t, validCase.CanonicalExtJSON)
			if renderedText != canonicalText {
				t.Errorf("%s %q: vendor renders %q, corpus says %q", fileName, validCase.Description, renderedText, canonicalText)
			}
			parsedHigh, parsedLow := requireSameParse(t, canonicalText)
			// A lossy case's bytes do not survive the string (a negative or
			// signalling NaN parses back to the canonical NaN).
			if !validCase.Lossy && (parsedHigh != high || parsedLow != low) {
				t.Errorf("%s %q: vendor parses %q to %016x %016x, corpus bytes are %016x %016x",
					fileName, validCase.Description, canonicalText, parsedHigh, parsedLow, high, low)
			}
			if validCase.DegenerateExtJSON != "" {
				requireSameParse(t, corpusDecimalString(t, validCase.DegenerateExtJSON))
			}
			requireSameUnmarshalJSON(t, validCase.CanonicalExtJSON)
		}
		for _, parseErrorCase := range corpus.ParseErrors {
			parseErrorCount++
			requireSameParse(t, parseErrorCase.String)
		}
	}
	// 601 valid cases and 131 parse errors at v1.17.10: an empty or
	// truncated corpus read must not pass.
	if validCount != 601 || parseErrorCount != 131 {
		t.Fatalf("corpus cases read: %d valid, %d parse errors; want 601 and 131", validCount, parseErrorCount)
	}
}

// requireSameUnmarshalJSON unmarshals the {"$numberDecimal": ...} object of a
// corpus document with both libraries.
func requireSameUnmarshalJSON(t *testing.T, extJSON string) {
	t.Helper()
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal([]byte(extJSON), &wrapper); err != nil {
		t.Fatalf("decoding extjson %q: %v", extJSON, err)
	}
	requireSameUnmarshal(t, wrapper["d"])
}

func requireSameUnmarshal(t *testing.T, jsonInput []byte) {
	t.Helper()
	// Both start from the same non-zero value so "null leaves it unchanged"
	// is observable.
	vendorDecimal := vendorprimitive.NewDecimal128(1, 2)
	libDecimal := libprimitive.NewDecimal128(1, 2)
	vendorErr := vendorDecimal.UnmarshalJSON(jsonInput)
	libErr := libDecimal.UnmarshalJSON(jsonInput)
	vendorHigh, vendorLow := vendorDecimal.GetBytes()
	libHigh, libLow := libDecimal.GetBytes()
	if vendorHigh != libHigh || vendorLow != libLow || decimalErrorText(vendorErr) != decimalErrorText(libErr) {
		t.Errorf("UnmarshalJSON(%s): library %016x %016x %q, vendor %016x %016x %q",
			jsonInput, libHigh, libLow, decimalErrorText(libErr), vendorHigh, vendorLow, decimalErrorText(vendorErr))
	}
}

// decimalParseTable covers what the corpus leaves thin: spellings of NaN and
// infinity, signed zeros, unnormalised coefficients, exponent and precision
// limits, and invalid strings.
var decimalParseTable = []string{
	"", "NaN", "nan", "NAN", "-NaN", "+NaN", "-nan", "sNaN", "NaN1",
	"Inf", "inf", "INF", "Infinity", "infinity", "-Infinity", "+Infinity", "-inf", "+inf", "Infinit", "infinityy",
	"0", "-0", "+0", "0.0", "-0.0", "-0.000", "0E+3", "-0E-3", "0e6111", "0e6112", "-0e-6176", "0e-6177",
	"0e99999999", "-0e-99999999", "0e999999999999999999999",
	"1", "-1", "+1", "1.0", "1.00", "100", "1E+2", "1e2", "1.000E+3", "0.00123400", "-1.00E-6", "1.050E+3",
	"0001", "-000.0100", "00000000000000000000000000000000000000000000001",
	"1E6111", "1E6112", "1E6144", "1E6145", "1E-6176", "1E-6177", "10E-6177", "1E-6200",
	"9999999999999999999999999999999999", "99999999999999999999999999999999999",
	// A leading '+' is not trimmed before the 35-character limit, so the
	// zeros behind it count: 34 zeros reach the limit, 33 do not.
	"+00000000000000000000000000000000001", "+0000000000000000000000000000000001",
	"-9999999999999999999999999999999999", "1234567890123456789012345678901234",
	"12345678901234567890123456789012345", "1234567890123456789012345678901234.5",
	"1234567890123456789012345678901234E6111", "9999999999999999999999999999999999E6111",
	"99999999999999999999999999999999990", "999999999999999999999999999999999900000000E-8",
	"1e", "e1", ".", ".1", "1.", "-.1", "+.1", "1..1", "1e1.5", "1e+", "1e-", "--1", "+-1", "-+1",
	" 1", "1 ", "1,0", "0x10", "1_000", "١", "1e99999999999999999999", "∞", "-", "+",
}

func TestParseDecimal128MatchesVendor(t *testing.T) {
	for _, decimalText := range decimalParseTable {
		high, low := requireSameParse(t, decimalText)
		requireSameString(t, high, low)
		requireSameBigInt(t, high, low)
	}
}

func requireSameBigInt(t *testing.T, high, low uint64) {
	t.Helper()
	vendorSignificand, vendorExp, vendorErr := vendorprimitive.NewDecimal128(high, low).BigInt()
	libSignificand, libExp, libErr := libprimitive.NewDecimal128(high, low).BigInt()
	if decimalErrorText(vendorErr) != decimalErrorText(libErr) || vendorExp != libExp ||
		(vendorSignificand == nil) != (libSignificand == nil) ||
		(vendorSignificand != nil && vendorSignificand.Cmp(libSignificand) != 0) {
		t.Errorf("BigInt of %016x %016x: library %v %d %q, vendor %v %d %q", high, low,
			libSignificand, libExp, decimalErrorText(libErr), vendorSignificand, vendorExp, decimalErrorText(vendorErr))
		return
	}
	if vendorErr != nil {
		requireSameErrorIdentity(t, vendorErr, libErr)
		return
	}
	// Round trip through ParseDecimal128FromBigInt.
	vendorBack, vendorOK := vendorprimitive.ParseDecimal128FromBigInt(vendorSignificand, vendorExp)
	libBack, libOK := libprimitive.ParseDecimal128FromBigInt(libSignificand, libExp)
	vendorBackHigh, vendorBackLow := vendorBack.GetBytes()
	libBackHigh, libBackLow := libBack.GetBytes()
	if vendorOK != libOK || vendorBackHigh != libBackHigh || vendorBackLow != libBackLow {
		t.Errorf("ParseDecimal128FromBigInt(%v, %d): library %016x %016x %v, vendor %016x %016x %v",
			vendorSignificand, vendorExp, libBackHigh, libBackLow, libOK, vendorBackHigh, vendorBackLow, vendorOK)
	}
}

// requireSameErrorIdentity checks that a library error is the library's own
// exported sentinel in the same position as the vendor's.
func requireSameErrorIdentity(t *testing.T, vendorErr, libErr error) {
	t.Helper()
	sentinelPairs := []struct{ vendorSentinel, libSentinel error }{
		{vendorprimitive.ErrParseNaN, libprimitive.ErrParseNaN},
		{vendorprimitive.ErrParseInf, libprimitive.ErrParseInf},
		{vendorprimitive.ErrParseNegInf, libprimitive.ErrParseNegInf},
	}
	for _, pair := range sentinelPairs {
		if errors.Is(vendorErr, pair.vendorSentinel) != errors.Is(libErr, pair.libSentinel) {
			t.Errorf("BigInt error %q: library sentinel identity differs from the vendor's", vendorErr)
		}
	}
}

func TestDecimal128AccessorsMatchVendor(t *testing.T) {
	if libprimitive.MaxDecimal128Exp != vendorprimitive.MaxDecimal128Exp ||
		libprimitive.MinDecimal128Exp != vendorprimitive.MinDecimal128Exp {
		t.Errorf("exponent limits: library %d..%d, vendor %d..%d", libprimitive.MinDecimal128Exp,
			libprimitive.MaxDecimal128Exp, vendorprimitive.MinDecimal128Exp, vendorprimitive.MaxDecimal128Exp)
	}
	for _, sentinelPair := range [][2]error{
		{vendorprimitive.ErrParseNaN, libprimitive.ErrParseNaN},
		{vendorprimitive.ErrParseInf, libprimitive.ErrParseInf},
		{vendorprimitive.ErrParseNegInf, libprimitive.ErrParseNegInf},
	} {
		if sentinelPair[0].Error() != sentinelPair[1].Error() {
			t.Errorf("sentinel text: library %q, vendor %q", sentinelPair[1], sentinelPair[0])
		}
	}
	for _, halves := range accessorHalves() {
		high, low := halves[0], halves[1]
		vendorDecimal := vendorprimitive.NewDecimal128(high, low)
		libDecimal := libprimitive.NewDecimal128(high, low)
		gotHigh, gotLow := libDecimal.GetBytes()
		if gotHigh != high || gotLow != low {
			t.Errorf("GetBytes of NewDecimal128(%016x, %016x) = %016x %016x", high, low, gotHigh, gotLow)
		}
		if vendorDecimal.IsNaN() != libDecimal.IsNaN() || vendorDecimal.IsInf() != libDecimal.IsInf() ||
			vendorDecimal.IsZero() != libDecimal.IsZero() {
			t.Errorf("predicates of %016x %016x: library NaN=%v Inf=%d Zero=%v, vendor NaN=%v Inf=%d Zero=%v",
				high, low, libDecimal.IsNaN(), libDecimal.IsInf(), libDecimal.IsZero(),
				vendorDecimal.IsNaN(), vendorDecimal.IsInf(), vendorDecimal.IsZero())
		}
		requireSameString(t, high, low)
		requireSameBigInt(t, high, low)
		vendorJSON, vendorErr := vendorDecimal.MarshalJSON()
		libJSON, libErr := libDecimal.MarshalJSON()
		if string(vendorJSON) != string(libJSON) || decimalErrorText(vendorErr) != decimalErrorText(libErr) {
			t.Errorf("MarshalJSON of %016x %016x: library %s %q, vendor %s %q",
				high, low, libJSON, decimalErrorText(libErr), vendorJSON, decimalErrorText(vendorErr))
		}
		requireSameUnmarshal(t, vendorJSON)
	}
	for _, jsonInput := range []string{
		`null`, `"1.5"`, `"NaN"`, `"-Infinity"`, `"-0"`, `"bogus"`, `{"$numberDecimal": "2E+3"}`,
		`{"$numberDecimal": 5}`, `{"$numberDouble": "1"}`, `{}`, `[1]`, `5`, `true`, `{`, ``, `"`,
		`{"$numberDecimal": "1", "extra": 2}`,
	} {
		requireSameUnmarshal(t, []byte(jsonInput))
	}
	requireSameBigIntRoundTrips(t)
	requireSameMethodSet(t, reflect.TypeOf(vendorprimitive.Decimal128{}), reflect.TypeOf(libprimitive.Decimal128{}))
	requireSameMethodSet(t, reflect.TypeOf(&vendorprimitive.Decimal128{}), reflect.TypeOf(&libprimitive.Decimal128{}))
}

// accessorHalves are raw bit patterns: every special form (NaN and infinity
// with stray bits, the 11-prefix "out of range" form), signed zeros, and
// maximum precision.
func accessorHalves() [][2]uint64 {
	return [][2]uint64{
		{0, 0}, {1 << 63, 0}, {0x3040000000000000, 0}, {0xb040000000000000, 0},
		{0x3040000000000000, 1}, {0xb040000000000000, 1},
		{0x1F << 58, 0}, {0x3F << 58, 0}, {0x7E00000000000000, 0}, {0x7C00000000000000, 12},
		{0x1E << 58, 0}, {0x3E << 58, 0}, {0x7800000000000001, 5},
		{0x6000000000000000, 0}, {0x6FFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFF}, {0xEC00000000000000, 1},
		{0x3041ED09BEAD87C0, 0x378D8E63FFFFFFFF}, {0xB041ED09BEAD87C0, 0x378D8E63FFFFFFFF},
		{0x5FFFED09BEAD87C0, 0x378D8E63FFFFFFFF}, {0x0001ED09BEAD87C0, 0x378D8E63FFFFFFFF},
		{0x0001FFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFF}, {0x5FFE000000000000, 0}, {0, 0xFFFFFFFFFFFFFFFF},
		{0x3040000000000000, 1e9}, {0x303E000000000000, 1234500},
	}
}

func requireSameBigIntRoundTrips(t *testing.T) {
	t.Helper()
	tooWide := new(big.Int).Lsh(big.NewInt(1), 120)
	for _, significandCase := range []struct {
		significandText string
		exp             int
	}{
		{"0", 0}, {"0", 99999}, {"0", -99999}, {"-0", 3}, {"1", 0}, {"-1", 0}, {"10", 6111}, {"10", 6112},
		{"1", -6177}, {"10", -6177}, {"9999999999999999999999999999999999", 6111},
		{"99999999999999999999999999999999990", 0}, {"99999999999999999999999999999999991", 0},
		{tooWide.String(), 0}, {"-" + tooWide.String(), -3}, {"123456789", -6200},
	} {
		significand, ok := new(big.Int).SetString(significandCase.significandText, 10)
		if !ok {
			t.Fatalf("bad significand %q in the table", significandCase.significandText)
		}
		significandBefore := significand.String()
		vendorDecimal, vendorOK := vendorprimitive.ParseDecimal128FromBigInt(significand, significandCase.exp)
		libDecimal, libOK := libprimitive.ParseDecimal128FromBigInt(significand, significandCase.exp)
		vendorHigh, vendorLow := vendorDecimal.GetBytes()
		libHigh, libLow := libDecimal.GetBytes()
		if vendorOK != libOK || vendorHigh != libHigh || vendorLow != libLow {
			t.Errorf("ParseDecimal128FromBigInt(%s, %d): library %016x %016x %v, vendor %016x %016x %v",
				significandCase.significandText, significandCase.exp, libHigh, libLow, libOK, vendorHigh, vendorLow, vendorOK)
		}
		if significand.String() != significandBefore {
			t.Errorf("ParseDecimal128FromBigInt modified its argument: %s became %s", significandBefore, significand)
		}
	}
}

func decimalMethodSignatures(decimalType reflect.Type) []string {
	signatures := make([]string, 0, decimalType.NumMethod())
	for methodIndex := 0; methodIndex < decimalType.NumMethod(); methodIndex++ {
		method := decimalType.Method(methodIndex)
		signatures = append(signatures, method.Name+" "+method.Type.String())
	}
	sort.Strings(signatures)
	return signatures
}

func requireSameMethodSet(t *testing.T, vendorType, libType reflect.Type) {
	t.Helper()
	normalise := func(signature string) string {
		return strings.ReplaceAll(signature, vendorType.String(), "DECIMAL")
	}
	vendorSignatures := decimalMethodSignatures(vendorType)
	libSignatures := decimalMethodSignatures(libType)
	for signatureIndex := range vendorSignatures {
		vendorSignatures[signatureIndex] = normalise(vendorSignatures[signatureIndex])
	}
	for signatureIndex := range libSignatures {
		libSignatures[signatureIndex] = strings.ReplaceAll(libSignatures[signatureIndex], libType.String(), "DECIMAL")
	}
	if strings.Join(vendorSignatures, "\n") != strings.Join(libSignatures, "\n") {
		t.Errorf("method set of %s:\nlibrary %q\nvendor  %q", libType, libSignatures, vendorSignatures)
	}
}

// FuzzParseDecimal128MatchesVendor parses and renders any string with both
// libraries.
func FuzzParseDecimal128MatchesVendor(f *testing.F) {
	for _, decimalText := range decimalParseTable {
		f.Add(decimalText)
	}
	f.Fuzz(func(t *testing.T, decimalText string) {
		high, low := requireSameParse(t, decimalText)
		requireSameParse(t, requireSameString(t, high, low))
	})
}

// FuzzDecimal128BitsMatchVendor renders any bit pattern with both libraries,
// then parses the rendering and takes BigInt.
func FuzzDecimal128BitsMatchVendor(f *testing.F) {
	for _, halves := range accessorHalves() {
		f.Add(halves[0], halves[1])
	}
	f.Fuzz(func(t *testing.T, high, low uint64) {
		requireSameParse(t, requireSameString(t, high, low))
		requireSameBigInt(t, high, low)
	})
}
