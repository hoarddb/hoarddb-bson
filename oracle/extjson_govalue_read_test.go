// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/benchdata"
	"github.com/hoarddb/hoarddb-bson/oracle/internal/corpus"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// The Go-value half of step 5b: bson.UnmarshalExtJSON into Go values,
// against the vendor's UnmarshalExtJSON into the same (or twin) targets:
// the same outcome, the same error text, and the same decoded value, which
// is compared after an error too (the partly filled target). The inputs are
// the corpus's Extended JSON texts, the error table's inputs and the texts
// below, in both modes; the targets are every target a decode site uses,
// the CLI's four (map, bson.M, bson.D, bson.Raw) among them.

// extJSONByteFields holds every byte-backed type as a field: the vendor
// copies each from the text through a BSON writer, so each has its own
// refusals (a zero byte in a key, a non-document into a bson.Raw).
type extJSONByteFields struct {
	Raw   vendorbson.Raw          `bson:"raw"`
	Value vendorbson.RawValue     `bson:"value"`
	Doc   vendorbsoncore.Document `bson:"doc"`
	Arr   vendorbsoncore.Array    `bson:"arr"`
	Any   any                     `bson:"any"`
}

type extJSONLibByteFields struct {
	Raw   bson.Raw          `bson:"raw"`
	Value bson.RawValue     `bson:"value"`
	Doc   bsoncore.Document `bson:"doc"`
	Arr   bsoncore.Array    `bson:"arr"`
	Any   any               `bson:"any"`
}

// extJSONTyped declares the keys the extra texts use, with typed fields.
type extJSONTyped struct {
	A     int64                 `bson:"a"`
	S     string                `bson:"s"`
	F     float64               `bson:"f"`
	R     vendorprimitive.Regex `bson:"r"`
	List  []int32               `bson:"list"`
	Inner *extJSONTypedInner    `bson:"inner"`
	Rest  map[string]any        `bson:",inline"`
}

type extJSONLibTyped struct {
	A     int64              `bson:"a"`
	S     string             `bson:"s"`
	F     float64            `bson:"f"`
	R     primitive.Regex    `bson:"r"`
	List  []int32            `bson:"list"`
	Inner *extJSONTypedInner `bson:"inner"`
	Rest  map[string]any     `bson:",inline"`
}

type extJSONTypedInner struct {
	N int32 `bson:"n"`
}

// extJSONDecodeTargets are every document target of the step 4e tests, the
// byte-backed fields, the hooks, typed structs, and the roots a text that is
// not a document can decode into.
func extJSONDecodeTargets() []decTarget {
	return append(decEveryTarget(),
		decTarget{"*extJSONByteFields", func() any { return &extJSONByteFields{} }, func() any { return &extJSONLibByteFields{} }},
		decTarget{"*extJSONTyped", func() any { return &extJSONTyped{} }, func() any { return &extJSONLibTyped{} }},
		decTarget{"*extJSONTyped prefilled", func() any { return &extJSONTyped{A: 9, S: "old", Rest: map[string]any{"kept": 1}} },
			func() any { return &extJSONLibTyped{A: 9, S: "old", Rest: map[string]any{"kept": 1}} }},
		decTarget{"*decHookFields", func() any { return &decHookFields{} }, func() any { return &decLibHookFields{} }},
		decSame("*decRecordedHook", decZero[decRecordedHook]),
		decSame("*map[string]int32", decZero[map[string]int32]),
		decSame("*map[string]string", decZero[map[string]string]),
		decSame("*int32", decZero[int32]),
		decSame("*int", decZero[int]),
		decSame("*string", decZero[string]),
		decSame("*float64", decZero[float64]),
		decSame("*bool", decZero[bool]),
		decSame("*[]int32", decZero[[]int32]),
		decSame("*[]string", decZero[[]string]),
		decSame("*map[string]interface{} with old keys", func() map[string]any { return map[string]any{"old": "kept", "a": "replaced"} }),
	)
}

// extJSONReadDivergence decodes text in mode canonical into fresh targets of
// both forms and describes the first difference, or returns "".
//
// The vendor's struct decode skips a key the struct does not declare with
// skipObject, which loops for ever when the skipped value's text ends or
// breaks before it closes. The library refuses those inputs with an error
// ending in skipNeverEnds, and the vendor is not called on them:
// the text must then fail to read into a map, which never skips, so the
// verdict cannot hide a text the vendor reads. Every other vendor call is
// bounded by vendorReturnsWithin, so a hang the library does not foresee
// fails the test instead of stopping it.
func extJSONReadDivergence(text []byte, canonical bool, target decTarget) string {
	libraryText, libraryErr := decLibraryOutcome(func() error { return bson.UnmarshalExtJSON(text, canonical, target.library()) })
	if strings.HasSuffix(libraryText, skipNeverEnds) {
		if vendorOutcome(func() error {
			var document map[string]any
			return vendorbson.UnmarshalExtJSON(text, canonical, &document)
		}) == "ok" {
			return fmt.Sprintf("%s: the library says a skipped value never ends (%v), but the text reads into a map", target.name, libraryErr)
		}
		decVendorPanics["the vendor never returns (a skipped value never ends)"]++
		return ""
	}
	return decDivergenceOf(target, func() int { return extJSONTextDepth(text) }, func(vendorTarget any) error {
		return vendorReturnsWithin(func() error { return vendorbson.UnmarshalExtJSON(text, canonical, vendorTarget) })
	}, func(libraryTarget any) error {
		return twinTypeNamed(bson.UnmarshalExtJSON(text, canonical, libraryTarget), target)
	})
}

// twinTypeNamed rewrites a library error that names the library's twin of
// the target's type ("cannot decode double into a oracle.extJSONLibTyped")
// with the vendor's type name, which the vendor's text names. A text root
// that is not a document names the target's type; a BSON root never does,
// which is why the step 4e harness has no such rewrite.
func twinTypeNamed(err error, target decTarget) error {
	if err == nil {
		return nil
	}
	vendorType, libraryType := reflect.TypeOf(target.vendor()), reflect.TypeOf(target.library())
	for vendorType.Kind() == reflect.Pointer && libraryType.Kind() == reflect.Pointer {
		vendorType, libraryType = vendorType.Elem(), libraryType.Elem()
	}
	if vendorType.String() == libraryType.String() || !strings.Contains(err.Error(), libraryType.String()) {
		return err
	}
	return renamedError{err: err, text: strings.ReplaceAll(err.Error(), libraryType.String(), vendorType.String())}
}

// renamedError is an error with its text rewritten and its chain kept.
type renamedError struct {
	err  error
	text string
}

func (renamed renamedError) Error() string { return renamed.text }
func (renamed renamedError) Unwrap() error { return renamed.err }

// skipNeverEnds ends the library's refusal of a skipped value whose text
// never closes (internal/extjson's ErrSkipNeverEnds).
const skipNeverEnds = "the value the decoder skips never ends"

// extJSONVendorHangs counts the vendor calls that did not return in time.
// One is enough to stop a test: each leaves a goroutine spinning.
var extJSONVendorHangs atomic.Int32

// vendorReturnsWithin runs a vendor call on its own goroutine and waits for
// it for a few seconds. A panic is raised again on the caller's goroutine,
// for vendorOutcome to report; a call that does not return is a failure.
func vendorReturnsWithin(vendorCall func() error) error {
	type callResult struct {
		err       error
		recovered any
	}
	returned := make(chan callResult, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				returned <- callResult{recovered: recovered}
			}
		}()
		returned <- callResult{err: vendorCall()}
	}()
	select {
	case result := <-returned:
		if result.recovered != nil {
			panic(result.recovered)
		}
		return result.err
	case <-time.After(5 * time.Second):
		extJSONVendorHangs.Add(1)
		return errors.New("the vendor did not return within 5 seconds")
	}
}

// jsonEscape is a backslash and a u, so a test text can hold a JSON \uXXXX
// escape without the Go source spelling one.
const jsonEscape = "\\" + "u"

// extJSONGoValueTexts are the texts beyond the corpus and the error table
// where decoding into Go values differs from writing bytes
// (docs/core-api.md, "The seam for the Go-value half"), and the values a
// decoder skips, copies or nests.
func extJSONGoValueTexts() []string {
	zeroKey := `{"a` + jsonEscape + `0000b": 1, "n": {"k` + jsonEscape + `0000": [1]}}`
	return []string{
		zeroKey,
		`{"r": {"$regularExpression": {"pattern": "a` + jsonEscape + `0000", "options": "xmi"}}}`,
		`{"r": {"$regex": "p` + jsonEscape + `0000", "$options": "si` + jsonEscape + `0000"}}`,
		`{"a": 1, "s": "x", "f": 2.5, "list": [1, 2], "inner": {"n": 3}, "other": {"$numberLong": "4"}}`,
		`{"a": {"$numberInt": "7"}, "s": {"$symbol": "sym"}, "f": {"$numberDecimal": "1"}}`,
		`{"a": 1.5, "list": [1.0, 2.5], "inner": null}`,
		`{"a": 9223372036854775807, "f": 1e400}`,
		`{"unknown": {"$numberInt": "x"}, "a": 1}`,
		`{"unknown": {"a": [1, {"b": {}}, []], "c": {"$code": "x", "$scope": {}}}, "a": 2}`,
		`{"unknown": [{"$date": 1}, {}, [[]]], "a": 3}`,
		`{"unknown": {}, "s": "after an empty object"}`,
		`{"unknown": "skipped", "unknown2": {"$numberLong": "x"}, "s": "y"}`,
		`{"raw": {"k": 1, "d": {"$date": "2026-01-01T00:00:00Z"}}, "value": [1, {"x": 2}], "doc": {}, "any": {"n": [1]}}`,
		`{"raw": [1, 2]}`,
		`{"raw": 5}`,
		`{"raw": {"z` + jsonEscape + `0000": 1}}`,
		`{"value": {"z` + jsonEscape + `0000": 1}}`,
		`{"value": {"$regularExpression": {"pattern": "p", "options": "xi"}}}`,
		`{"value": {"$numberInt": "x"}}`,
		`{"doc": [1]}`,
		`{"arr": [1, 2]}`,
		`{"arr": {"a": 1}}`,
		`{"raw": {"a": 1, "b": {"$numberInt": "x"}}}`,
		`{"any": {"$code": "f", "$scope": {"x": {"$numberInt": "1"}}}}`,
		`{"h": {"x": 1}, "p": null, "l": [{"y": 2}, "s"], "m": {"k": {"$numberLong": "3"}}, "r": {}, "value": "text", "vp": 2.5}`,
		`{"h": {"$numberInt": "7"}, "value": true}`,
		`{"p": {"$undefined": true}, "vp": {"$maxKey": 1}}`,
		`{"vp": {"z` + jsonEscape + `0000": 1}}`,
		`5`, `"text"`, `true`, `null`, `1.5`, `[1, 2, 3]`, `["a", "b"]`, `[[1], {"a": 1}]`, `{"$numberInt": "1"}`,
		`[{"$numberInt": "1"}, {"$numberLong": "2"}]`, `[]`, `{}`,
		`{"a": [[[[[[[[[[1]]]]]]]]]]}`,
		`{"x": {"$binary": {"base64": "AQID", "subType": "80"}}, "u": {"$uuid": "73ffd264-44b3-4c69-90e8-e7d1dfc035d4"}}`,
		`{"t": {"$timestamp": {"t": 1, "i": 2}}, "d": {"$date": {"$numberLong": "-1"}}, "m": {"$minKey": 1}}`,
		`{"p": {"$dbPointer": {"$ref": "db.c", "$id": {"$oid": "507f1f77bcf86cd799439011"}}}}`,
		`{"j": {"$code": "f()"}, "w": {"$code": "g()", "$scope": {"a": [1, {"b": 2}]}}}`,
		`{"a": 1}` + " trailing",
		`{"k1": 1, "k2": "two", "k3": {"$numberDouble": "nope"}}`,
		`{"old": "x", "a": {"$oid": "zz"}}`,
	}
}

// extJSONReadInputs is every text the read differentials run: the corpus,
// the error table's inputs and the texts above.
func extJSONReadInputs(t *testing.T) []corpusExtJSONText {
	t.Helper()
	corpusFiles, err := corpus.Load()
	if err != nil {
		t.Fatalf("loading the corpus: %v", err)
	}
	var inputs []corpusExtJSONText
	for _, corpusFile := range corpusFiles {
		for _, corpusText := range corpusExtJSONTexts(corpusFile) {
			inputs = append(inputs, corpusExtJSONText{label: corpusFile.Name + " " + corpusText.label, text: corpusText.text})
		}
	}
	for _, input := range extJSONErrorInputs {
		inputs = append(inputs, corpusExtJSONText{label: "error table: " + input.name, text: input.text})
	}
	for position, text := range extJSONGoValueTexts() {
		inputs = append(inputs, corpusExtJSONText{label: fmt.Sprintf("go-value text %d", position), text: text})
	}
	return inputs
}

// TestUnmarshalExtJSONIntoGoValuesMatchesVendor runs every input, in both
// modes, into every target: the outcome, the error text and the decoded
// value equal the vendor's. The tally of accepted and refused decodes is
// logged for the step report.
func TestUnmarshalExtJSONIntoGoValuesMatchesVendor(t *testing.T) {
	targets := extJSONDecodeTargets()
	var accepted, refused, failures int
	for _, input := range extJSONReadInputs(t) {
		for _, canonical := range []bool{true, false} {
			for _, target := range targets {
				if divergence := extJSONReadDivergence([]byte(input.text), canonical, target); divergence != "" {
					if extJSONVendorHangs.Load() > 0 {
						t.Fatalf("%s, canonical=%v: %s\n  input %q", input.label, canonical, divergence, clipped(input.text))
					}
					failures++
					if failures <= 60 {
						t.Errorf("%s, canonical=%v: %s\n  input %q", input.label, canonical, divergence, clipped(input.text))
					}
					continue
				}
				// The outcomes are the same, so the library's is counted: the
				// vendor may never return on this input.
				if outcome, _ := decLibraryOutcome(func() error { return bson.UnmarshalExtJSON([]byte(input.text), canonical, target.library()) }); outcome == "ok" {
					accepted++
				} else {
					refused++
				}
			}
		}
	}
	if failures > 60 {
		t.Errorf("%d divergences in all, the first 60 shown", failures)
	}
	t.Logf("%d targets; %d decodes the vendor accepts and %d it refuses, all matched", len(targets), accepted, refused)
	if len(decVendorPanics) > 0 {
		t.Logf("not compared: %v", decVendorPanics)
	}
}

// TestUnmarshalExtJSONGoValueErrorTextMatchesTheTable holds the library to
// the vendor-generated error table at its Go-value columns (map, bson.M,
// bson.D) in both modes: err.Error() exactly, or "ok".
func TestUnmarshalExtJSONGoValueErrorTextMatchesTheTable(t *testing.T) {
	rows := loadExtJSONErrorTable(t)
	for _, row := range rows {
		for _, mode := range []struct {
			canonical bool
			outcomes  targetOutcomes
		}{{true, row.Canonical}, {false, row.Relaxed}} {
			for _, column := range []struct {
				name   string
				want   exactText
				target func() any
			}{
				{"map", mode.outcomes.Map, func() any { var target map[string]any; return &target }},
				{"M", mode.outcomes.M, func() any { var target bson.M; return &target }},
				{"D", mode.outcomes.Ordered, func() any { var target bson.D; return &target }},
			} {
				got, _ := decLibraryOutcome(func() error { return bson.UnmarshalExtJSON([]byte(row.Input), mode.canonical, column.target()) })
				if got != string(column.want) {
					t.Errorf("%s, canonical=%v, %s: library %q, vendor table %q\n  input %q", row.Name, mode.canonical, column.name, got, column.want, clipped(string(row.Input)))
				}
			}
		}
	}
}

// TestUnmarshalExtJSONGoValuesRefusePastTheCeiling holds UnmarshalExtJSON
// into a Go value to the ceiling (spec 5.2): 200 levels decode as the
// vendor decodes them, and 201 levels are refused with an error matching
// ErrNestingTooDeep. The decoder counts every document, array and scope it
// descends into, as Unmarshal does, so arrays count here, where the
// parser's own limit of 200 counts only objects. A bson.Raw target, which
// the decoder copies without descending, keeps the parser's limit, and the
// parser's refusal matches ErrNestingTooDeep too.
func TestUnmarshalExtJSONGoValuesRefusePastTheCeiling(t *testing.T) {
	nestedText := func(levels int, through string) []byte {
		switch through {
		case "array":
			return []byte(`{"a":` + strings.Repeat("[", levels-1) + strings.Repeat("]", levels-1) + "}")
		case "mixed":
			pairs := (levels - 1) / 2
			return []byte(`{"a":` + strings.Repeat(`[{"b":`, pairs) + "1" + strings.Repeat("}]", pairs) + "}")
		case "scope":
			// The parser counts the wrapper of each scope as an object too.
			return []byte(strings.Repeat(`{"c":{"$code":"f","$scope":`, levels-1) + "{}" + strings.Repeat("}}", levels-1))
		}
		return nestedObjects(levels)
	}
	targets := []decTarget{decTargetMap, decTargetInterface, decTargetD, decTargetM, decTargetNilD}
	t.Run("scope", func(t *testing.T) {
		// A scope is two objects to the parser (the wrapper and the scope),
		// so the parser's limit of 200 objects refuses 100 scopes, at 101
		// levels, with the vendor's text, before the ceiling could: both
		// sides refuse, with the same text.
		for _, scopes := range []int{99, 100} {
			for _, canonical := range []bool{true, false} {
				for _, target := range targets {
					if divergence := extJSONReadDivergence(nestedText(scopes+1, "scope"), canonical, target); divergence != "" {
						t.Errorf("%d scopes, canonical=%v: %s", scopes, canonical, divergence)
					}
				}
			}
		}
	})
	for _, through := range []string{"document", "array", "mixed"} {
		t.Run(through, func(t *testing.T) {
			atCeiling, pastCeiling := nestedText(bson.MaxNestingCeiling, through), nestedText(bson.MaxNestingCeiling+1, through)
			if through == "mixed" {
				// An odd count of levels below the root.
				atCeiling, pastCeiling = nestedText(bson.MaxNestingCeiling-1, through), nestedText(bson.MaxNestingCeiling+1, through)
			}
			for _, canonical := range []bool{true, false} {
				for _, target := range targets {
					if divergence := extJSONReadDivergence(atCeiling, canonical, target); divergence != "" {
						t.Errorf("at the ceiling, canonical=%v: %s", canonical, divergence)
					}
					requireNestingRefusal(t, fmt.Sprintf("canonical=%v, 201 levels into %s", canonical, target.name),
						bson.UnmarshalExtJSON(pastCeiling, canonical, target.library()))
				}
			}
		})
	}
	t.Run("the parser's own limit", func(t *testing.T) {
		pastCeiling := nestedObjects(bson.MaxNestingCeiling + 1)
		var rawTarget bson.Raw
		err := bson.UnmarshalExtJSON(pastCeiling, false, &rawTarget)
		requireNestingRefusal(t, "201 objects into a bson.Raw", err)
		if want := libraryRawExtJSON(pastCeiling, false).outcome; err == nil || err.Error() != want {
			t.Errorf("201 objects into a bson.Raw: %v, want the vendor's text %q", err, want)
		}
		arrays := []byte(`{"a":` + strings.Repeat("[", 300) + strings.Repeat("]", 300) + "}")
		if divergence := extJSONReadDivergence(arrays, false, decTargetRaw); divergence != "" {
			t.Errorf("300 arrays into a bson.Raw: %s", divergence)
		}
	})
}

// TestUnmarshalExtJSONGoValuesKeepWhatBytesWouldLose pins the four places
// where decoding a Go value from bytes would give a different answer from
// the vendor's (docs/core-api.md, "Step 5b core"), each against the vendor.
func TestUnmarshalExtJSONGoValuesKeepWhatBytesWouldLose(t *testing.T) {
	zeroKey := []byte(`{"a` + jsonEscape + `0000b": 1}`)
	regex := []byte(`{"r": {"$regularExpression": {"pattern": "p` + jsonEscape + `0000", "options": "xmi"}}}`)
	mapPrefix := []byte(`{"a": {"b": [1, {"c": {"$numberInt": "x"}}]}}`)
	partial := []byte(`{"k1": 1, "k2": "two", "k3": {"$numberDouble": "nope"}}`)
	for _, check := range []struct {
		name   string
		text   []byte
		target decTarget
		want   string
	}{
		{"a zero byte in a key, into a map", zeroKey, decTargetMap, "ok"},
		{"a zero byte in a key, into a bson.D", zeroKey, decTargetD, "ok"},
		{"a zero byte in a key, into a bson.Raw", zeroKey, decTargetRaw, "BSON element key cannot contain null bytes"},
		{"regex options as typed and a zero byte, into a map", regex, decTargetMap, "ok"},
		{"regex into a bson.Raw", regex, decTargetRaw, "BSON regex values cannot contain null bytes"},
		{"an error at depth, into a map", mapPrefix, decTargetMap, `error decoding key a.b.1.c: strconv.ParseInt: parsing "x": invalid syntax`},
		{"an error at depth, into a bson.D: only the array index", mapPrefix, decTargetD, `error decoding key 1: strconv.ParseInt: parsing "x": invalid syntax`},
		{"a partly read map", partial, decTargetMap, `error decoding key k3: strconv.ParseFloat: parsing "nope": invalid syntax`},
		{"a partly read bson.D", partial, decTargetD, `strconv.ParseFloat: parsing "nope": invalid syntax`},
	} {
		t.Run(check.name, func(t *testing.T) {
			if got := vendorOutcome(func() error { return vendorbson.UnmarshalExtJSON(check.text, false, check.target.vendor()) }); got != check.want {
				t.Fatalf("the vendor's outcome is %q, the test expects %q", got, check.want)
			}
			if divergence := extJSONReadDivergence(check.text, false, check.target); divergence != "" {
				t.Error(divergence)
			}
		})
	}
	t.Run("the regex options a map receives are as typed", func(t *testing.T) {
		var document map[string]any
		if err := bson.UnmarshalExtJSON(regex, false, &document); err != nil {
			t.Fatal(err)
		}
		if got := document["r"].(primitive.Regex).Options; got != "xmi" {
			t.Errorf("options %q, want the input's %q", got, "xmi")
		}
	})
}

// TestUnmarshalExtJSONArgumentErrors holds the argument checks to the
// vendor's: a nil target, a non-pointer, a nil pointer, and text that is
// not JSON at all.
func TestUnmarshalExtJSONArgumentErrors(t *testing.T) {
	var nilMap map[string]any
	var nilPointer *map[string]any
	for _, check := range []struct {
		name   string
		text   string
		vendor any
		lib    any
	}{
		{"nil target", `{}`, nil, nil},
		{"non-pointer", `{}`, 5, 5},
		{"nil map", `{}`, nilMap, nilMap},
		{"nil pointer", `{}`, nilPointer, nilPointer},
		{"map by value", `{"a": 1}`, map[string]any{}, map[string]any{}},
		{"not JSON into a nil target", `@`, nil, nil},
		{"empty text", ``, &map[string]any{}, &map[string]any{}},
	} {
		vendorText := vendorOutcome(func() error { return vendorbson.UnmarshalExtJSON([]byte(check.text), true, check.vendor) })
		libText, _ := decLibraryOutcome(func() error { return bson.UnmarshalExtJSON([]byte(check.text), true, check.lib) })
		if vendorText != libText {
			t.Errorf("%s: vendor %q, library %q", check.name, vendorText, libText)
		}
	}
	if err := bson.UnmarshalExtJSON(nestedObjects(bson.MaxNestingCeiling+1), true, &map[string]any{}); !errors.Is(err, bson.ErrNestingTooDeep) {
		t.Errorf("201 objects into a map: %v, want an error matching ErrNestingTooDeep", err)
	}
}

// vendorSkipChild is the environment variable that makes
// TestUnmarshalExtJSONVendorSkipNeverReturns's child process run the
// vendor on one text.
const vendorSkipChild = "HOARDDB_BSON_VENDOR_SKIP_TEXT"

// TestUnmarshalExtJSONVendorSkipNeverReturns pins the vendor behaviour the
// library refuses with ErrSkipNeverEnds: a struct that skips an undeclared
// key whose value's text breaks or ends before it closes never returns from
// the vendor's UnmarshalExtJSON. Each text runs in a child process, which is
// killed once it has run for two seconds, so no spinning goroutine is left
// in this one; the library refuses each with the parser's error.
func TestUnmarshalExtJSONVendorSkipNeverReturns(t *testing.T) {
	if text, isChild := os.LookupEnv(vendorSkipChild); isChild {
		var target struct {
			A int32 `bson:"a"`
		}
		_ = vendorbson.UnmarshalExtJSON([]byte(text), true, &target)
		return
	}
	for _, check := range []struct{ text, parserError string }{
		{`{"unknown": {"a": @}}`, "invalid JSON input. Position: 18. Character: @"},
		{`{"unknown": {"a": 1`, "invalid JSON input; unexpected end of input at position 0"},
		{`{"unknown": [1, 2`, "invalid JSON input; unexpected end of input at position 0"},
		{`{"unknown": [{}, {"x": tru}]}`, "invalid JSON literal. Position: 23, literal: tru"},
	} {
		child := exec.Command(os.Args[0], "-test.run=^TestUnmarshalExtJSONVendorSkipNeverReturns$")
		child.Env = append(os.Environ(), vendorSkipChild+"="+check.text)
		if err := child.Start(); err != nil {
			t.Fatalf("starting the child: %v", err)
		}
		exited := make(chan error, 1)
		go func() { exited <- child.Wait() }()
		select {
		case err := <-exited:
			t.Errorf("%q: the vendor returned (child exit %v); the library's refusal is then not the vendor's behaviour", check.text, err)
		case <-time.After(2 * time.Second):
			if err := child.Process.Kill(); err != nil {
				t.Fatalf("killing the child: %v", err)
			}
			<-exited
		}
		var target struct {
			A int32 `bson:"a"`
		}
		err := bson.UnmarshalExtJSON([]byte(check.text), true, &target)
		if err == nil || !strings.HasSuffix(err.Error(), skipNeverEnds) || !strings.HasPrefix(err.Error(), check.parserError) {
			t.Errorf("%q: the library returned %v, want the parser's %q under ErrSkipNeverEnds", check.text, err, check.parserError)
		}
	}
}

// TestUnmarshalExtJSONRefusesWhereTheVendorPanics pins the one vendor panic
// a text root reaches that a BSON root cannot: a null root decoded into a
// map passed by value, which the vendor's map codec sets to nil. The
// library refuses it with an error that says what to pass instead.
func TestUnmarshalExtJSONRefusesWhereTheVendorPanics(t *testing.T) {
	vendorText := vendorOutcome(func() error { return vendorbson.UnmarshalExtJSON([]byte("null"), false, map[string]any{}) })
	if vendorText != "panic: reflect: reflect.Value.Set using unaddressable value" {
		t.Fatalf("the vendor's outcome is %q; the pinned panic is gone", vendorText)
	}
	libraryText, _ := decLibraryOutcome(func() error { return bson.UnmarshalExtJSON([]byte("null"), false, map[string]any{}) })
	if want := "cannot decode null into a map[string]interface {} passed by value: only a map behind a pointer can be set to nil; pass a pointer to the map"; libraryText != want {
		t.Errorf("the library's outcome is %q, want %q", libraryText, want)
	}
}

// TestExtJSONGoValuesRoundTrip writes Go values with each library and reads
// the text back with the same library: the library's round trip must give
// the vendor's, value for value, in both modes.
func TestExtJSONGoValuesRoundTrip(t *testing.T) {
	objectID := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	ordered := []decTarget{decTargetD, decTargetM, decTargetMap, decTargetInterface}
	// A map's text is in the vendor's random order, so only the unordered
	// targets read it back the same.
	unordered := []decTarget{decTargetM, decTargetMap}
	cases := []struct {
		encodeCase
		targets []decTarget
	}{
		{twinValues("every BSON type", extJSONEveryType(objectID), extJSONVendorEveryType(objectID)), ordered},
		{twinValues("Ref450 record", benchdata.Ref450LibRecord(), benchdata.Ref450Record()), ordered},
		{sameValue("map with a zero-byte key", map[string]any{"a\x00b": "c", "n": int32(1)}), unordered},
		{sameValue("doubles", map[string]float64{"nan": math.NaN(), "inf": math.Inf(1), "one": 1, "tiny": 5e-324}), unordered},
	}
	for _, encodeInput := range cases {
		for _, canonical := range []bool{true, false} {
			mode := extJSONMode{canonical: canonical}
			vendorText, vendorFailure := vendorMarshalExtJSON(encodeInput.vendor, mode)
			libText, libFailure, _ := libraryMarshalExtJSON(encodeInput.lib, mode)
			if vendorFailure != "" || libFailure != "" {
				t.Fatalf("%s, %s: vendor %s, library %s", encodeInput.name, mode, outcomeText(vendorFailure), outcomeText(libFailure))
			}
			for _, target := range encodeInput.targets {
				label := fmt.Sprintf("%s, %s, into %s", encodeInput.name, mode, target.name)
				vendorTarget, libTarget := target.vendor(), target.library()
				vendorOutcomeText := vendorOutcome(func() error { return vendorbson.UnmarshalExtJSON(vendorText, canonical, vendorTarget) })
				libOutcomeText, _ := decLibraryOutcome(func() error { return bson.UnmarshalExtJSON(libText, canonical, libTarget) })
				if vendorOutcomeText != libOutcomeText {
					t.Errorf("%s: read back %q (vendor) vs %q (library)", label, vendorOutcomeText, libOutcomeText)
					continue
				}
				if difference := decValueDifference(label, vendorTarget, libTarget); difference != "" {
					t.Error(difference)
				}
			}
		}
	}
}
