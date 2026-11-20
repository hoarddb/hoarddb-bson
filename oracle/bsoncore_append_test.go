// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"bytes"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
	vendorbsontype "go.mongodb.org/mongo-driver/bson/bsontype"
	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"
	vendorbsoncore "go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// inventoryBsoncoreRow matches a referenced-name row of docs/api-inventory.md.
var inventoryBsoncoreRow = regexp.MustCompile("^\\| `bsoncore\\.([A-Za-z0-9]+)` \\| (func|type|method|field) \\|")

// inventoriedBsoncoreNames reads the bsoncore functions and types the
// inventory lists, so the tests below follow the inventory, not a copy of it.
func inventoriedBsoncoreNames(t *testing.T) (functions, types []string) {
	t.Helper()
	inventoryText, err := os.ReadFile("../docs/api-inventory.md")
	if err != nil {
		t.Fatalf("reading the inventory: %v", err)
	}
	for _, line := range strings.Split(string(inventoryText), "\n") {
		match := inventoryBsoncoreRow.FindStringSubmatch(line)
		switch {
		case match == nil:
		case match[2] == "func":
			functions = append(functions, match[1])
		case match[2] == "type":
			types = append(types, match[1])
		}
	}
	if len(functions) == 0 {
		t.Fatal("the inventory lists no bsoncore function; the row pattern no longer matches it")
	}
	// Names the server tree did not use when the inventory was generated, added
	// later and mirrored from the vendor all the same (v0.7.3).
	functions = append(functions, "AppendBooleanElement", "ReadBoolean")
	return functions, types
}

// callOutcome is everything a mirrored call can show: its bytes, its error
// text and, if it panicked, the panic's text.
type callOutcome struct {
	output    []byte
	isNil     bool
	errorText string
	panicText string
}

func (outcome callOutcome) String() string {
	return fmt.Sprintf("bytes=% x nil=%t err=%q panic=%q", truncatedHex(outcome.output), outcome.isNil,
		outcome.errorText, outcome.panicText)
}

func (outcome callOutcome) equal(other callOutcome) bool {
	return bytes.Equal(outcome.output, other.output) && outcome.isNil == other.isNil &&
		outcome.errorText == other.errorText && outcome.panicText == other.panicText
}

func runAppend(call func() ([]byte, error)) (outcome callOutcome) {
	defer func() {
		if recovered := recover(); recovered != nil {
			outcome = callOutcome{panicText: fmt.Sprint(recovered)}
		}
	}()
	output, err := call()
	outcome = callOutcome{output: output, isNil: output == nil}
	if err != nil {
		outcome.errorText = err.Error()
	}
	return outcome
}

// appendRow is one input to one mirrored function, called through both
// libraries. Each closure builds its own inputs, so neither side can see
// bytes the other appended into a shared backing array.
type appendRow struct {
	function string
	row      string
	library  func() ([]byte, error)
	vendor   func() ([]byte, error)
}

func noError(output []byte) ([]byte, error) { return output, nil }

// keyCases are the keys every element function is called with. The zero
// byte makes both libraries panic (AppendHeader's contract).
var keyCases = []struct{ label, key string }{
	{"empty key", ""},
	{"typical key", "name"},
	{"maximum-length key", strings.Repeat("k", 1<<16)},
	{"invalid UTF-8 key", "\xff\xfe"},
	{"key with a zero byte", "a\x00b"},
}

// destinationCases are the dst buffers: nil, and a prefix the output must
// keep. Each call gets a fresh copy.
var destinationCases = []struct {
	label  string
	prefix []byte
}{
	{"nil dst", nil},
	{"prefixed dst", []byte{0xAA, 0xBB, 0xCC}},
}

func freshDestination(prefix []byte) []byte {
	if prefix == nil {
		return nil
	}
	return append(make([]byte, 0, len(prefix)), prefix...)
}

var doubleCases = []float64{
	0, math.Copysign(0, -1), 1.5, -2.25, math.MaxFloat64, -math.MaxFloat64, math.SmallestNonzeroFloat64,
	math.Inf(1), math.Inf(-1), math.NaN(), math.Float64frombits(0x7ff0000000000001),
	math.Float64frombits(0xfff8000000000000),
}

var int32Cases = []int32{0, 1, -1, math.MaxInt32, math.MinInt32}

var int64Cases = []int64{0, 1, -1, math.MaxInt64, math.MinInt64}

var stringCases = []string{"", "hello", "with\x00zero", "\xff invalid", strings.Repeat("x", 1<<20)}

var binarySubtypes = []byte{0x00, 0x02, 0x04, 0x80, 0xFF}

var binaryPayloads = [][]byte{nil, {}, {0x01}, bytes.Repeat([]byte{0x5A}, 1024)}

var objectIDCases = [][12]byte{
	{},
	{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
	{0x65, 0x0f, 0x1a, 0x2b, 0x3c, 0x4d, 0x5e, 0x6f, 0x70, 0x81, 0x92, 0xa3},
}

// elementRows calls one element function with every key and destination,
// for each value label the caller gives.
func elementRows(function string, valueLabel string,
	library func(dst []byte, key string) []byte, vendor func(dst []byte, key string) []byte) []appendRow {
	var rows []appendRow
	for _, keyCase := range keyCases {
		for _, destinationCase := range destinationCases {
			key, prefix := keyCase.key, destinationCase.prefix
			rows = append(rows, appendRow{
				function: function,
				row:      fmt.Sprintf("%s, %s, %s", valueLabel, keyCase.label, destinationCase.label),
				library:  func() ([]byte, error) { return noError(library(freshDestination(prefix), key)) },
				vendor:   func() ([]byte, error) { return noError(vendor(freshDestination(prefix), key)) },
			})
		}
	}
	return rows
}

// nestedLibraryDocument and nestedVendorDocument build the same three-level
// document, each with its own library's functions only.
func nestedLibraryDocument() []byte {
	innermost := bsoncore.BuildDocument(nil, bsoncore.AppendInt32Element(nil, "level", 3))
	array := bsoncore.BuildArray(nil, bsoncore.Value{Type: bsontype.EmbeddedDocument, Data: innermost},
		bsoncore.Value{Type: bsontype.String, Data: stringValueOf("two")})
	middle := bsoncore.BuildDocument(nil, bsoncore.AppendArrayElement(nil, "items", array),
		bsoncore.AppendDocumentElement(nil, "inner", innermost))
	return bsoncore.BuildDocument(nil, bsoncore.AppendStringElement(nil, "name", "outer"),
		bsoncore.AppendDocumentElement(nil, "middle", middle))
}

func nestedVendorDocument() []byte {
	innermost := vendorbsoncore.BuildDocument(nil, vendorbsoncore.AppendInt32Element(nil, "level", 3))
	array := vendorbsoncore.BuildArray(nil, vendorbsoncore.Value{Type: vendorbson.TypeEmbeddedDocument, Data: innermost},
		vendorbsoncore.Value{Type: vendorbson.TypeString, Data: stringValueOf("two")})
	middle := vendorbsoncore.BuildDocument(nil, vendorbsoncore.AppendArrayElement(nil, "items", array),
		vendorbsoncore.AppendDocumentElement(nil, "inner", innermost))
	return vendorbsoncore.BuildDocument(nil, vendorbsoncore.AppendStringElement(nil, "name", "outer"),
		vendorbsoncore.AppendDocumentElement(nil, "middle", middle))
}

// embeddedDocumentCases are the doc and arr arguments of
// AppendDocumentElement and AppendArrayElement. Neither function looks at
// them, so malformed bytes are appended verbatim by both.
var embeddedDocumentCases = []struct {
	label string
	bytes func() []byte
}{
	{"nil document", func() []byte { return nil }},
	{"zero-length document", func() []byte { return []byte{} }},
	{"empty document", func() []byte { return []byte{5, 0, 0, 0, 0} }},
	{"typical document", func() []byte { return documentOf(elementOf(0x02, "a", stringValueOf("b"))) }},
	{"nested document", nestedVendorDocument},
	{"malformed bytes", func() []byte { return []byte{0x01, 0x02} }},
}

func headerRows() []appendRow {
	var rows []appendRow
	for typeByte := range 256 {
		rows = append(rows, elementRows("AppendHeader", fmt.Sprintf("type 0x%02x", typeByte),
			func(dst []byte, key string) []byte { return bsoncore.AppendHeader(dst, bsontype.Type(typeByte), key) },
			func(dst []byte, key string) []byte {
				return vendorbsoncore.AppendHeader(dst, vendorbsontype.Type(typeByte), key)
			})...)
	}
	return rows
}

func scalarElementRows() []appendRow {
	var rows []appendRow
	for _, number := range doubleCases {
		rows = append(rows, elementRows("AppendDoubleElement", fmt.Sprintf("double %v bits %016x", number, math.Float64bits(number)),
			func(dst []byte, key string) []byte { return bsoncore.AppendDoubleElement(dst, key, number) },
			func(dst []byte, key string) []byte { return vendorbsoncore.AppendDoubleElement(dst, key, number) })...)
	}
	for _, flag := range []bool{false, true} {
		rows = append(rows, elementRows("AppendBooleanElement", fmt.Sprintf("boolean %t", flag),
			func(dst []byte, key string) []byte { return bsoncore.AppendBooleanElement(dst, key, flag) },
			func(dst []byte, key string) []byte { return vendorbsoncore.AppendBooleanElement(dst, key, flag) })...)
	}
	for _, number := range int32Cases {
		rows = append(rows, elementRows("AppendInt32Element", fmt.Sprintf("int32 %d", number),
			func(dst []byte, key string) []byte { return bsoncore.AppendInt32Element(dst, key, number) },
			func(dst []byte, key string) []byte { return vendorbsoncore.AppendInt32Element(dst, key, number) })...)
	}
	for _, number := range int64Cases {
		rows = append(rows, elementRows("AppendInt64Element", fmt.Sprintf("int64 %d", number),
			func(dst []byte, key string) []byte { return bsoncore.AppendInt64Element(dst, key, number) },
			func(dst []byte, key string) []byte { return vendorbsoncore.AppendInt64Element(dst, key, number) })...)
		rows = append(rows, elementRows("AppendDateTimeElement", fmt.Sprintf("datetime %d", number),
			func(dst []byte, key string) []byte { return bsoncore.AppendDateTimeElement(dst, key, number) },
			func(dst []byte, key string) []byte { return vendorbsoncore.AppendDateTimeElement(dst, key, number) })...)
	}
	for _, text := range stringCases {
		rows = append(rows, elementRows("AppendStringElement", fmt.Sprintf("string of %d bytes %q", len(text), truncatedText(text)),
			func(dst []byte, key string) []byte { return bsoncore.AppendStringElement(dst, key, text) },
			func(dst []byte, key string) []byte { return vendorbsoncore.AppendStringElement(dst, key, text) })...)
	}
	for _, subtype := range binarySubtypes {
		for _, payload := range binaryPayloads {
			rows = append(rows, elementRows("AppendBinaryElement",
				fmt.Sprintf("binary subtype 0x%02x, %d bytes, nil=%t", subtype, len(payload), payload == nil),
				func(dst []byte, key string) []byte { return bsoncore.AppendBinaryElement(dst, key, subtype, payload) },
				func(dst []byte, key string) []byte {
					return vendorbsoncore.AppendBinaryElement(dst, key, subtype, payload)
				})...)
		}
	}
	for _, idBytes := range objectIDCases {
		rows = append(rows, elementRows("AppendObjectIDElement", fmt.Sprintf("objectid %x", idBytes),
			func(dst []byte, key string) []byte {
				return bsoncore.AppendObjectIDElement(dst, key, primitive.ObjectID(idBytes))
			},
			func(dst []byte, key string) []byte {
				return vendorbsoncore.AppendObjectIDElement(dst, key, vendorprimitive.ObjectID(idBytes))
			})...)
	}
	return rows
}

func embeddedElementRows() []appendRow {
	var rows []appendRow
	for _, documentCase := range embeddedDocumentCases {
		makeBytes := documentCase.bytes
		rows = append(rows, elementRows("AppendDocumentElement", documentCase.label,
			func(dst []byte, key string) []byte { return bsoncore.AppendDocumentElement(dst, key, makeBytes()) },
			func(dst []byte, key string) []byte {
				return vendorbsoncore.AppendDocumentElement(dst, key, makeBytes())
			})...)
		rows = append(rows, elementRows("AppendArrayElement", documentCase.label,
			func(dst []byte, key string) []byte { return bsoncore.AppendArrayElement(dst, key, makeBytes()) },
			func(dst []byte, key string) []byte { return vendorbsoncore.AppendArrayElement(dst, key, makeBytes()) })...)
	}
	rows = append(rows, elementRows("AppendDocumentElement", "nested document built by each library",
		func(dst []byte, key string) []byte {
			return bsoncore.AppendDocumentElement(dst, key, nestedLibraryDocument())
		},
		func(dst []byte, key string) []byte {
			return vendorbsoncore.AppendDocumentElement(dst, key, nestedVendorDocument())
		})...)
	return rows
}

// startEndRows covers AppendDocumentStart/End and AppendArrayStart/End:
// starting on each buffer, and ending at every index that is in range, at
// the edge, past it and negative. The vendor returns an error past the edge
// and panics on a negative index, and so must the library.
func startEndRows() []appendRow {
	largePrefix := bytes.Repeat([]byte{0x11}, 1<<20)
	starts := []struct {
		label  string
		prefix []byte
	}{
		{"nil dst", nil}, {"prefixed dst", []byte{0xAA, 0xBB, 0xCC}}, {"1 MiB prefix", largePrefix},
		{"nested: after an element header", append([]byte{5, 0, 0, 0, 0x03}, "key\x00"...)},
	}
	var rows []appendRow
	for _, startCase := range starts {
		prefix := startCase.prefix
		rows = append(rows,
			appendRow{"AppendDocumentStart", startCase.label,
				func() ([]byte, error) {
					index, output := bsoncore.AppendDocumentStart(freshDestination(prefix))
					return indexed(index, output)
				},
				func() ([]byte, error) {
					index, output := vendorbsoncore.AppendDocumentStart(freshDestination(prefix))
					return indexed(index, output)
				}},
			appendRow{"AppendArrayStart", startCase.label,
				func() ([]byte, error) {
					index, output := bsoncore.AppendArrayStart(freshDestination(prefix))
					return indexed(index, output)
				},
				func() ([]byte, error) {
					index, output := vendorbsoncore.AppendArrayStart(freshDestination(prefix))
					return indexed(index, output)
				}})
	}
	body := append([]byte{0xAA, 0xBB, 0, 0, 0, 0}, elementOf(0x10, "0", int32Of(7))...)
	ends := []struct {
		label string
		dst   []byte
		index int32
	}{
		{"typical: index of the reserved length", body, 2},
		{"empty document from index 0", []byte{0, 0, 0, 0}, 0},
		{"index exactly len-4", body, int32(len(body) - 4)},
		{"index len-3: no room for the length", body, int32(len(body) - 3)},
		{"index past the end", body, int32(len(body) + 10)},
		{"empty dst", nil, 0},
		{"negative index", body, -1},
		{"most negative index", body, math.MinInt32},
		{"largest index", body, math.MaxInt32},
	}
	for _, endCase := range ends {
		dstBytes, index := endCase.dst, endCase.index
		rows = append(rows,
			appendRow{"AppendDocumentEnd", endCase.label,
				func() ([]byte, error) { return bsoncore.AppendDocumentEnd(freshDestination(dstBytes), index) },
				func() ([]byte, error) { return vendorbsoncore.AppendDocumentEnd(freshDestination(dstBytes), index) }},
			appendRow{"AppendArrayEnd", endCase.label,
				func() ([]byte, error) { return bsoncore.AppendArrayEnd(freshDestination(dstBytes), index) },
				func() ([]byte, error) { return vendorbsoncore.AppendArrayEnd(freshDestination(dstBytes), index) }})
	}
	rows = append(rows, appendRow{"AppendDocumentEnd", "nested: start, element, start, element, end, end",
		func() ([]byte, error) {
			outer, output := bsoncore.AppendDocumentStart(nil)
			output = bsoncore.AppendInt32Element(output, "a", 1)
			inner, output := bsoncore.AppendDocumentStart(bsoncore.AppendHeader(output, bsontype.EmbeddedDocument, "b"))
			output = bsoncore.AppendStringElement(output, "c", "d")
			output, err := bsoncore.AppendDocumentEnd(output, inner)
			if err != nil {
				return output, err
			}
			return bsoncore.AppendDocumentEnd(output, outer)
		},
		func() ([]byte, error) {
			outer, output := vendorbsoncore.AppendDocumentStart(nil)
			output = vendorbsoncore.AppendInt32Element(output, "a", 1)
			inner, output := vendorbsoncore.AppendDocumentStart(vendorbsoncore.AppendHeader(output, vendorbson.TypeEmbeddedDocument, "b"))
			output = vendorbsoncore.AppendStringElement(output, "c", "d")
			output, err := vendorbsoncore.AppendDocumentEnd(output, inner)
			if err != nil {
				return output, err
			}
			return vendorbsoncore.AppendDocumentEnd(output, outer)
		}})
	return rows
}

// indexed folds a start function's index into the compared bytes.
func indexed(index int32, output []byte) ([]byte, error) {
	return append(int32Of(index), output...), nil
}

func buildDocumentRows() []appendRow {
	manyElements := func(appendInt32 func([]byte, string, int32) []byte) [][]byte {
		elements := make([][]byte, 1000)
		for position := range elements {
			elements[position] = appendInt32(nil, fmt.Sprintf("k%d", position), int32(position))
		}
		return elements
	}
	cases := []struct {
		label   string
		prefix  []byte
		library func() [][]byte
		vendor  func() [][]byte
	}{
		{"no elements", nil, func() [][]byte { return nil }, func() [][]byte { return nil }},
		{"one nil element", nil, func() [][]byte { return [][]byte{nil} }, func() [][]byte { return [][]byte{nil} }},
		{"typical", nil,
			func() [][]byte {
				return [][]byte{bsoncore.AppendStringElement(nil, "name", "ada"), bsoncore.AppendInt32Element(nil, "age", 36)}
			},
			func() [][]byte {
				return [][]byte{vendorbsoncore.AppendStringElement(nil, "name", "ada"), vendorbsoncore.AppendInt32Element(nil, "age", 36)}
			}},
		{"typical, prefixed dst", []byte{0xAA, 0xBB},
			func() [][]byte { return [][]byte{bsoncore.AppendDoubleElement(nil, "x", 1)} },
			func() [][]byte { return [][]byte{vendorbsoncore.AppendDoubleElement(nil, "x", 1)} }},
		{"maximum-length key", nil,
			func() [][]byte { return [][]byte{bsoncore.AppendInt64Element(nil, keyCases[2].key, -1)} },
			func() [][]byte { return [][]byte{vendorbsoncore.AppendInt64Element(nil, keyCases[2].key, -1)} }},
		{"nested", nil,
			func() [][]byte { return [][]byte{bsoncore.AppendDocumentElement(nil, "n", nestedLibraryDocument())} },
			func() [][]byte {
				return [][]byte{vendorbsoncore.AppendDocumentElement(nil, "n", nestedVendorDocument())}
			}},
		{"1000 elements", nil,
			func() [][]byte { return manyElements(bsoncore.AppendInt32Element) },
			func() [][]byte { return manyElements(vendorbsoncore.AppendInt32Element) }},
	}
	var rows []appendRow
	for _, buildCase := range cases {
		buildCase := buildCase
		rows = append(rows, appendRow{"BuildDocument", buildCase.label,
			func() ([]byte, error) {
				return noError(bsoncore.BuildDocument(freshDestination(buildCase.prefix), buildCase.library()...))
			},
			func() ([]byte, error) {
				return noError(vendorbsoncore.BuildDocument(freshDestination(buildCase.prefix), buildCase.vendor()...))
			}})
	}
	return rows
}

// arrayValue is one BuildArray argument, as a type byte and value bytes, so
// both libraries get the same Value.
type arrayValue struct {
	typeByte byte
	data     []byte
}

func buildArrayRows() []appendRow {
	typical := []arrayValue{
		{0x01, binaryLittleEndian64(math.Float64bits(2.5))},
		{0x02, stringValueOf("text")},
		{0x08, []byte{1}},
		{0x0A, nil},
		{0x10, int32Of(math.MinInt32)},
	}
	eleven := make([]arrayValue, 11)
	for position := range eleven {
		eleven[position] = arrayValue{0x10, int32Of(int32(position))}
	}
	cases := []struct {
		label  string
		prefix []byte
		values []arrayValue
	}{
		{"no values", nil, nil},
		{"zero Value", nil, []arrayValue{{0, nil}}},
		{"typical", nil, typical},
		{"typical, prefixed dst", []byte{0xAA}, typical},
		{"eleven values: a two-digit key", nil, eleven},
		{"nested document and array", nil, []arrayValue{{0x03, nestedVendorDocument()}, {0x04, documentOf(elementOf(0x10, "0", int32Of(1)))}}},
		{"invalid type byte", nil, []arrayValue{{0xEE, []byte{1, 2, 3}}}},
	}
	var rows []appendRow
	for _, arrayCase := range cases {
		arrayCase := arrayCase
		rows = append(rows, appendRow{"BuildArray", arrayCase.label,
			func() ([]byte, error) {
				values := make([]bsoncore.Value, len(arrayCase.values))
				for position, value := range arrayCase.values {
					values[position] = bsoncore.Value{Type: bsontype.Type(value.typeByte), Data: value.data}
				}
				return noError(bsoncore.BuildArray(freshDestination(arrayCase.prefix), values...))
			},
			func() ([]byte, error) {
				values := make([]vendorbsoncore.Value, len(arrayCase.values))
				for position, value := range arrayCase.values {
					values[position] = vendorbsoncore.Value{Type: vendorbsontype.Type(value.typeByte), Data: value.data}
				}
				return noError(vendorbsoncore.BuildArray(freshDestination(arrayCase.prefix), values...))
			}})
	}
	return rows
}

func binaryLittleEndian64(bits uint64) []byte {
	encoded := make([]byte, 8)
	for position := range encoded {
		encoded[position] = byte(bits >> (8 * position))
	}
	return encoded
}

func truncatedText(text string) string {
	if len(text) > 16 {
		return text[:16] + "..."
	}
	return text
}

func allAppendRows() []appendRow {
	var rows []appendRow
	rows = append(rows, headerRows()...)
	rows = append(rows, scalarElementRows()...)
	rows = append(rows, embeddedElementRows()...)
	rows = append(rows, startEndRows()...)
	rows = append(rows, buildDocumentRows()...)
	rows = append(rows, buildArrayRows()...)
	return rows
}

func TestEveryInventoriedAppendFunctionMatchesVendor(t *testing.T) {
	rows := allAppendRows()
	rowsPerFunction := map[string]int{}
	for _, row := range rows {
		rowsPerFunction[row.function]++
		libraryOutcome, vendorOutcome := runAppend(row.library), runAppend(row.vendor)
		if !libraryOutcome.equal(vendorOutcome) {
			t.Errorf("%s (%s):\n library %v\n  vendor %v", row.function, row.row, libraryOutcome, vendorOutcome)
		}
	}
	inventoriedFunctions, _ := inventoriedBsoncoreNames(t)
	var appendBuildNames []string
	for _, name := range inventoriedFunctions {
		if strings.HasPrefix(name, "Append") || strings.HasPrefix(name, "Build") {
			appendBuildNames = append(appendBuildNames, name)
			if rowsPerFunction[name] < 4 {
				t.Errorf("%s is in the inventory and has %d rows, want at least 4", name, rowsPerFunction[name])
			}
		}
	}
	for name := range rowsPerFunction {
		if !containsName(appendBuildNames, name) {
			t.Errorf("%s has rows but is not an inventoried Append*/Build* name", name)
		}
	}
	t.Logf("%d rows over %d inventoried Append*/Build* functions", len(rows), len(appendBuildNames))
}

func containsName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

// readOutcome is every result a Read* function returns, with each returned
// slice's offset into the input, so a rem that does not alias the input at
// the vendor's position fails too.
type readOutcome struct {
	numbers   []int64
	slices    [][]byte
	nilSlices []bool
	offsets   []int
	ok        bool
}

func (outcome readOutcome) String() string {
	var sliceTexts []string
	for _, returned := range outcome.slices {
		sliceTexts = append(sliceTexts, truncatedHex(returned))
	}
	return fmt.Sprintf("numbers=%v slices=%v nil=%v offsets=%v ok=%t", outcome.numbers, sliceTexts,
		outcome.nilSlices, outcome.offsets, outcome.ok)
}

func (outcome readOutcome) equal(other readOutcome) bool {
	if fmt.Sprint(outcome.numbers, outcome.nilSlices, outcome.offsets, outcome.ok) !=
		fmt.Sprint(other.numbers, other.nilSlices, other.offsets, other.ok) || len(outcome.slices) != len(other.slices) {
		return false
	}
	for position := range outcome.slices {
		if !bytes.Equal(outcome.slices[position], other.slices[position]) {
			return false
		}
	}
	return true
}

// offsetIn is a returned slice's start within src, from their capacities,
// or -1 for a nil slice. The input is given full capacity, so a subslice's
// offset is exact.
func offsetIn(src, returned []byte) int {
	if returned == nil {
		return -1
	}
	return cap(src) - cap(returned)
}

func readsOf(src []byte, numbers []int64, ok bool, slices ...[]byte) readOutcome {
	outcome := readOutcome{numbers: numbers, slices: slices, ok: ok}
	for _, returned := range slices {
		outcome.nilSlices = append(outcome.nilSlices, returned == nil)
		outcome.offsets = append(outcome.offsets, offsetIn(src, returned))
	}
	return outcome
}

type readFunction struct {
	name    string
	library func(src []byte) readOutcome
	vendor  func(src []byte) readOutcome
}

func boolInt(flag bool) int64 {
	if flag {
		return 1
	}
	return 0
}

var readFunctions = []readFunction{
	{"ReadLength",
		func(src []byte) readOutcome {
			length, rem, ok := bsoncore.ReadLength(src)
			return readsOf(src, []int64{int64(length)}, ok, rem)
		},
		func(src []byte) readOutcome {
			length, rem, ok := vendorbsoncore.ReadLength(src)
			return readsOf(src, []int64{int64(length)}, ok, rem)
		}},
	{"ReadBoolean",
		func(src []byte) readOutcome {
			flag, rem, ok := bsoncore.ReadBoolean(src)
			return readsOf(src, []int64{boolInt(flag)}, ok, rem)
		},
		func(src []byte) readOutcome {
			flag, rem, ok := vendorbsoncore.ReadBoolean(src)
			return readsOf(src, []int64{boolInt(flag)}, ok, rem)
		}},
	{"ReadInt32",
		func(src []byte) readOutcome {
			number, rem, ok := bsoncore.ReadInt32(src)
			return readsOf(src, []int64{int64(number)}, ok, rem)
		},
		func(src []byte) readOutcome {
			number, rem, ok := vendorbsoncore.ReadInt32(src)
			return readsOf(src, []int64{int64(number)}, ok, rem)
		}},
	{"ReadInt64",
		func(src []byte) readOutcome {
			number, rem, ok := bsoncore.ReadInt64(src)
			return readsOf(src, []int64{number}, ok, rem)
		},
		func(src []byte) readOutcome {
			number, rem, ok := vendorbsoncore.ReadInt64(src)
			return readsOf(src, []int64{number}, ok, rem)
		}},
	{"ReadBinary",
		func(src []byte) readOutcome {
			subtype, payload, rem, ok := bsoncore.ReadBinary(src)
			return readsOf(src, []int64{int64(subtype)}, ok, payload, rem)
		},
		func(src []byte) readOutcome {
			subtype, payload, rem, ok := vendorbsoncore.ReadBinary(src)
			return readsOf(src, []int64{int64(subtype)}, ok, payload, rem)
		}},
	{"ReadDocument",
		func(src []byte) readOutcome {
			document, rem, ok := bsoncore.ReadDocument(src)
			return readsOf(src, nil, ok, document, rem)
		},
		func(src []byte) readOutcome {
			document, rem, ok := vendorbsoncore.ReadDocument(src)
			return readsOf(src, nil, ok, document, rem)
		}},
	{"ReadElement",
		func(src []byte) readOutcome {
			element, rem, ok := bsoncore.ReadElement(src)
			return readsOf(src, nil, ok, element, rem)
		},
		func(src []byte) readOutcome {
			element, rem, ok := vendorbsoncore.ReadElement(src)
			return readsOf(src, nil, ok, element, rem)
		}},
}

// readInputs are fixed edge inputs plus seeded random ones shaped like
// binaries: an outer length near the bytes, a subtype, an inner length.
func readInputs() [][]byte {
	inputs := [][]byte{
		nil, {}, {0x01}, {0x01, 0x02, 0x03},
		int32Of(0), int32Of(-1), int32Of(math.MinInt32), int32Of(math.MaxInt32), int32Of(5),
		append(int32Of(4), 0xEE), binaryLittleEndian64(math.MaxUint64), binaryLittleEndian64(1),
		append(binaryLittleEndian64(0x0102030405060708), 0xFF, 0xFE),
		append(append(int32Of(3), 0x00), 1, 2, 3, 9),
		append(append(int32Of(3), 0x00), 1, 2),
		append(int32Of(0), 0x00),
		int32Of(0)[:3],
		append(append(int32Of(7), 0x02), append(int32Of(3), 1, 2, 3)...),
		append(append(int32Of(7), 0x02), append(int32Of(4), 1, 2, 3)...),
		append(append(int32Of(7), 0x02), append(int32Of(-1), 1, 2, 3)...),
		append(append(int32Of(2), 0x02), append(int32Of(1), 1)...),
		append(append(int32Of(7), 0x02), 1, 2),
		append(int32Of(0), 0x02),
		append(append(int32Of(4), 0x02), int32Of(0)...),
		// Subtype 0xff is a valid user-defined subtype (the step 2c
		// verifier asked for the row): payload, empty payload, trailing bytes.
		append(append(int32Of(3), 0xFF), 1, 2, 3),
		append(append(int32Of(3), 0xFF), 1, 2, 3, 9, 9),
		append(int32Of(0), 0xFF),
		append(append(int32Of(3), 0xFF), 1, 2),
		append(append(int32Of(1), 0xFF), 0xFF),
	}
	generator := rand.New(rand.NewPCG(2026, 1024))
	for range 5000 {
		payload := make([]byte, generator.IntN(12))
		for position := range payload {
			payload[position] = byte(generator.IntN(256))
		}
		subtype := []byte{0x00, 0x02, 0x80}[generator.IntN(3)]
		innerLength := int32(generator.IntN(len(payload)+3) - 1)
		body := payload
		if subtype == 0x02 {
			body = append(int32Of(innerLength), payload...)
		}
		outerLength := int32(len(body) + generator.IntN(5) - 2)
		input := append(append(int32Of(outerLength), subtype), body...)
		inputs = append(inputs, input[:generator.IntN(len(input)+1)])
	}
	return inputs
}

func TestReadFunctionsMatchVendor(t *testing.T) {
	inputs := readInputs()
	// ReadDocument and ReadElement read documents and elements, not
	// binaries: the type corpus feeds them (and every other Read function,
	// which must agree on those bytes as well).
	typeInputs := sharedBsoncoreInputs(t)
	inputs = append(inputs, typeInputs.documents...)
	inputs = append(inputs, typeInputs.elements...)
	for _, function := range readFunctions {
		for _, input := range inputs {
			libraryInput := append(make([]byte, 0, len(input)), input...)
			vendorInput := append(make([]byte, 0, len(input)), input...)
			if input == nil {
				libraryInput, vendorInput = nil, nil
			}
			libraryOutcome, vendorOutcome := function.library(libraryInput), function.vendor(vendorInput)
			if !libraryOutcome.equal(vendorOutcome) {
				t.Errorf("%s(% x):\n library %v\n  vendor %v", function.name, input, libraryOutcome, vendorOutcome)
			}
		}
	}
	inventoriedFunctions, _ := inventoriedBsoncoreNames(t)
	for _, name := range inventoriedFunctions {
		if !strings.HasPrefix(name, "Read") {
			continue
		}
		tested := false
		for _, function := range readFunctions {
			tested = tested || function.name == name
		}
		if !tested {
			t.Errorf("%s is inventoried and has no row in readFunctions", name)
		}
	}
}

// TestBsoncoreExportsOnlyInventoriedNames holds the library's bsoncore to
// the inventory (spec 5.1: a name not on it is not implemented) and to
// this step's share of it.
func TestBsoncoreExportsOnlyInventoriedNames(t *testing.T) {
	inventoriedFunctions, inventoriedTypes := inventoriedBsoncoreNames(t)
	inventoried := append(append([]string{}, inventoriedFunctions...), inventoriedTypes...)
	libraryNames := exportedNamesOf(t, "github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore")
	for _, name := range libraryNames {
		_, reachable := signatureReachableTypes[name]
		if !containsName(inventoried, name) && !reachable {
			t.Errorf("bsoncore exports %s, which the inventory does not list", name)
		}
	}
	var missing []string
	for _, name := range append(append([]string{}, inventoriedFunctions...), inventoriedTypes...) {
		if !containsName(libraryNames, name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("inventoried bsoncore names not built: %v", missing)
	}
	for typeName, reason := range signatureReachableTypes {
		if !containsName(libraryNames, typeName) {
			t.Errorf("%s is allowed as %s but the package does not export it; delete the allowance", typeName, reason)
		}
	}
}

// signatureReachableTypes are exported types the inventory does not list by
// name, because the server never names them, but which an inventoried
// method returns, so a listed type cannot be mirrored whole without them
// (spec 5.1 item 2). Value.Array and Value.ArrayOK return Array.
var signatureReachableTypes = map[string]string{
	"Array": "the result type of Value.Array and Value.ArrayOK",
}

// inventoryMethodRow matches a method line of a type section of the inventory.
var inventoryMethodRow = regexp.MustCompile("^- `func \\(bsoncore\\.([A-Za-z0-9]+)\\) ([A-Za-z0-9]+)\\(")

// TestBsoncoreMethodSetsAreTheInventoried holds each inventoried type's
// exported methods to the inventory's list, both ways: a method the
// inventory lists and the library lacks, and a method the library has and
// the inventory does not list, both fail.
func TestBsoncoreMethodSetsAreTheInventoried(t *testing.T) {
	inventoryText, err := os.ReadFile("../docs/api-inventory.md")
	if err != nil {
		t.Fatalf("reading the inventory: %v", err)
	}
	inventoried := map[string][]string{}
	for _, line := range strings.Split(string(inventoryText), "\n") {
		if match := inventoryMethodRow.FindStringSubmatch(line); match != nil {
			inventoried[match[1]] = append(inventoried[match[1]], match[2])
		}
	}
	libraryTypes := map[string]reflect.Type{
		"Value": reflect.TypeOf(bsoncore.Value{}), "Document": reflect.TypeOf(bsoncore.Document{}), "Element": reflect.TypeOf(bsoncore.Element{}),
	}
	for typeName, libraryType := range libraryTypes {
		var libraryMethods []string
		for methodIndex := range libraryType.NumMethod() {
			libraryMethods = append(libraryMethods, libraryType.Method(methodIndex).Name)
		}
		listed := append([]string{}, inventoried[typeName]...)
		sort.Strings(listed)
		sort.Strings(libraryMethods)
		if len(listed) == 0 {
			t.Fatalf("the inventory lists no methods of bsoncore.%s; the row pattern no longer matches it", typeName)
		}
		if strings.Join(listed, " ") != strings.Join(libraryMethods, " ") {
			t.Errorf("bsoncore.%s methods:\n library %v\n inventory %v", typeName, libraryMethods, listed)
		}
	}
}
