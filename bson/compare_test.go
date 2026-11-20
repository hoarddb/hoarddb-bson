// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// viewOf is the View of one value with the given type and value bytes, read
// back through Validate and the cursor as every View is made.
func viewOf(t testing.TB, valueType bsontype.Type, valueBytes []byte) bson.View {
	t.Helper()
	view, ok := tryViewOf(valueType, valueBytes)
	if !ok {
		t.Fatalf("no View of type %v from % x: Validate refused it", valueType, valueBytes)
	}
	return view
}

// tryViewOf is viewOf for inputs that may not be BSON, as the fuzz target's.
func tryViewOf(valueType bsontype.Type, valueBytes []byte) (bson.View, bool) {
	document, err := bson.Validate(documentOf(elementOf(byte(valueType), "v", valueBytes)), bson.Limits{Depth: bson.MaxNestingCeiling})
	if err != nil {
		return bson.View{}, false
	}
	cursor := document.Cursor()
	if !cursor.Next() {
		return bson.View{}, false
	}
	return cursor.Value(), true
}

func int64Bytes(number int64) []byte {
	return binary.LittleEndian.AppendUint64(nil, uint64(number))
}

func doubleBytes(number float64) []byte {
	return binary.LittleEndian.AppendUint64(nil, math.Float64bits(number))
}

func int32View(t testing.TB, number int32) bson.View {
	return viewOf(t, bsontype.Int32, int32Of(number))
}

func int64View(t testing.TB, number int64) bson.View {
	return viewOf(t, bsontype.Int64, int64Bytes(number))
}

func doubleView(t testing.TB, number float64) bson.View {
	return viewOf(t, bsontype.Double, doubleBytes(number))
}

func stringView(t testing.TB, text string) bson.View {
	return viewOf(t, bsontype.String, stringValueOf(text))
}

func dateView(t testing.TB, milliseconds int64) bson.View {
	return viewOf(t, bsontype.DateTime, int64Bytes(milliseconds))
}

func booleanView(t testing.TB, flag bool) bson.View {
	if flag {
		return viewOf(t, bsontype.Boolean, []byte{1})
	}
	return viewOf(t, bsontype.Boolean, []byte{0})
}

// rankSevenValues is one value of every type in the "other" class, in
// ascending order of type byte: the order the class sub-ranks them in.
func rankSevenValues(t testing.TB) []bson.View {
	objectID := bytes.Repeat([]byte{0xAB}, 12)
	return []bson.View{
		viewOf(t, bsontype.Binary, binaryValueOf(0x00, []byte{1, 2})),
		viewOf(t, bsontype.Undefined, nil),
		viewOf(t, bsontype.ObjectID, objectID),
		viewOf(t, bsontype.Regex, []byte("^a\x00i\x00")),
		viewOf(t, bsontype.DBPointer, append(stringValueOf("db.c"), objectID...)),
		viewOf(t, bsontype.JavaScript, stringValueOf("f()")),
		viewOf(t, bsontype.Symbol, stringValueOf("s")),
		viewOf(t, bsontype.CodeWithScope, codeWithScopeOf("f()", documentOf())),
		viewOf(t, bsontype.Timestamp, []byte{1, 0, 0, 0, 2, 0, 0, 0}),
		viewOf(t, bsontype.Decimal128, decimalBytes(0x3040000000000000, 1)),
		viewOf(t, bsontype.MaxKey, nil),
		viewOf(t, bsontype.MinKey, nil),
	}
}

// decimalBytes is a Decimal128 value from its high and low words.
func decimalBytes(high, low uint64) []byte {
	return binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint64(nil, low), high)
}

func sign(number int) int {
	switch {
	case number < 0:
		return -1
	case number > 0:
		return 1
	}
	return 0
}

// expectOrder checks that Compare puts every earlier group strictly before
// every later one, and ties every pair within a group, in both argument
// orders.
func expectOrder(t *testing.T, groups [][]bson.View) {
	t.Helper()
	for leftGroup := range groups {
		for rightGroup := range groups {
			want := sign(leftGroup - rightGroup)
			for leftIndex, left := range groups[leftGroup] {
				for rightIndex, right := range groups[rightGroup] {
					if got := bson.Compare(left, right); got != want {
						t.Errorf("Compare(group %d #%d (%v % x), group %d #%d (%v % x)) = %d, want %d",
							leftGroup, leftIndex, left.Type(), left.Bytes(),
							rightGroup, rightIndex, right.Type(), right.Bytes(), got, want)
					}
				}
			}
		}
	}
}

// TestCompareRankTable: the pinned class order of the unindexed sort (spec
// 6.3): number 1, string 2, date 3, boolean 4, document 5, array 6, other 7
// sub-ranked by type byte, missing or null 8. Each group is one class, or
// one type inside the other class, and every member ties within it.
func TestCompareRankTable(t *testing.T) {
	groups := [][]bson.View{
		{int32View(t, 7), int64View(t, 7), doubleView(t, 7)},
		{stringView(t, "seven")},
		{dateView(t, 7)},
		{booleanView(t, true)},
		{viewOf(t, bsontype.EmbeddedDocument, documentOf(elementOf(0x10, "x", int32Of(1))))},
		{viewOf(t, bsontype.Array, documentOf(elementOf(0x10, "0", int32Of(1))))},
	}
	for _, otherView := range rankSevenValues(t) {
		groups = append(groups, []bson.View{otherView})
	}
	groups = append(groups, []bson.View{viewOf(t, bsontype.Null, nil), {}})
	expectOrder(t, groups)
}

// TestCompareRanksExtremesByClass: the class decides before the value, so
// the largest value of a class still sorts before the smallest of the next.
func TestCompareRanksExtremesByClass(t *testing.T) {
	expectOrder(t, [][]bson.View{
		{doubleView(t, math.Inf(1))},
		{stringView(t, "")},
		{stringView(t, "\xff\xff\xff")},
		{dateView(t, math.MinInt64)},
		{dateView(t, math.MaxInt64)},
		{booleanView(t, false)},
		{booleanView(t, true)},
		{viewOf(t, bsontype.EmbeddedDocument, documentOf())},
		{viewOf(t, bsontype.Array, documentOf())},
		{viewOf(t, bsontype.Binary, binaryValueOf(0x00, nil))},
		{viewOf(t, bsontype.MinKey, nil)},
		{viewOf(t, bsontype.Null, nil)},
	})
}

// TestCompareNumbersAcrossTypes: int32, int64 and double compare by value,
// exactly, whatever their types (spec 5.2, 6.3): NaN below every number and
// tied with every NaN, -0 tied with 0, and integers beyond 2^53 never
// rounded to a double.
func TestCompareNumbersAcrossTypes(t *testing.T) {
	const twoTo53 = 1 << 53
	quietNaN := math.NaN()
	payloadNaN := math.Float64frombits(0x7FF0000000000001)
	negativeNaN := math.Float64frombits(0xFFF8000000000000)
	expectOrder(t, [][]bson.View{
		{doubleView(t, quietNaN), doubleView(t, payloadNaN), doubleView(t, negativeNaN)},
		{doubleView(t, math.Inf(-1))},
		{doubleView(t, -math.MaxFloat64)},
		{int64View(t, math.MinInt64), doubleView(t, -0x1p63)},
		{int64View(t, math.MinInt64+1)},
		{doubleView(t, -twoTo53-2), int64View(t, -twoTo53-2)},
		{int64View(t, -twoTo53-1)},
		{doubleView(t, -twoTo53), int64View(t, -twoTo53)},
		{int32View(t, math.MinInt32), int64View(t, math.MinInt32), doubleView(t, math.MinInt32)},
		{doubleView(t, -2)},
		{doubleView(t, -1.5)},
		{int32View(t, -1), int64View(t, -1), doubleView(t, -1)},
		{doubleView(t, -0.5)},
		{doubleView(t, -math.SmallestNonzeroFloat64)},
		{int32View(t, 0), int64View(t, 0), doubleView(t, 0), doubleView(t, math.Copysign(0, -1))},
		{doubleView(t, math.SmallestNonzeroFloat64)},
		{doubleView(t, 0.5)},
		{int32View(t, 1), int64View(t, 1), doubleView(t, 1)},
		{doubleView(t, 1.5)},
		{int32View(t, math.MaxInt32), int64View(t, math.MaxInt32), doubleView(t, math.MaxInt32)},
		{doubleView(t, twoTo53), int64View(t, twoTo53)},
		{int64View(t, twoTo53+1)},
		{doubleView(t, twoTo53+2), int64View(t, twoTo53+2)},
		{int64View(t, math.MaxInt64-1)},
		{int64View(t, math.MaxInt64)},
		{doubleView(t, 0x1p63)},
		{doubleView(t, math.MaxFloat64)},
		{doubleView(t, math.Inf(1))},
	})
}

// TestCompareTiesEveryDecimal128: every Decimal128 ties with every other,
// numeric order or not, and sits in the other class at type byte 0x13
// (ruling O9): after every number, after a timestamp, before MaxKey.
func TestCompareTiesEveryDecimal128(t *testing.T) {
	decimals := []bson.View{
		viewOf(t, bsontype.Decimal128, decimalBytes(0x3040000000000000, 1)),       // 1
		viewOf(t, bsontype.Decimal128, decimalBytes(0x3040000000000000, 2)),       // 2
		viewOf(t, bsontype.Decimal128, decimalBytes(0xB040000000000000, 1000)),    // -1000
		viewOf(t, bsontype.Decimal128, decimalBytes(0x7C00000000000000, 0)),       // NaN
		viewOf(t, bsontype.Decimal128, decimalBytes(0xF800000000000000, 0)),       // -Infinity
		viewOf(t, bsontype.Decimal128, decimalBytes(0x7800000000000000, 0)),       // Infinity
		viewOf(t, bsontype.Decimal128, decimalBytes(0x3040000000000000, 0)),       // 0
		viewOf(t, bsontype.Decimal128, decimalBytes(0x3040FFFFFFFFFFFF, 1<<63-1)), // large
	}
	expectOrder(t, [][]bson.View{
		{int64View(t, math.MaxInt64)},
		{doubleView(t, math.Inf(1))},
		{viewOf(t, bsontype.Timestamp, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})},
		decimals,
		{viewOf(t, bsontype.MaxKey, nil)},
	})
}

// TestCompareStringsByByteOrder: strings compare byte by byte, shorter
// first on a common prefix, with no collation and no UTF-8 decoding (build
// doc step 3c, "string byte order").
func TestCompareStringsByByteOrder(t *testing.T) {
	expectOrder(t, [][]bson.View{
		{stringView(t, "")},
		{stringView(t, "\x00")},
		{stringView(t, "B")},
		{stringView(t, "a")},
		{stringView(t, "a\x00")},
		{stringView(t, "ab")},
		{stringView(t, "b")},
		{stringView(t, "z")},
		{stringView(t, "\xc3\xa9")}, // é
		{stringView(t, "\xff")},
	})
}

// TestCompareWithinTheScalarClasses: dates by their milliseconds, false
// before true, and documents, arrays, null and missing tied whatever they
// hold.
func TestCompareWithinTheScalarClasses(t *testing.T) {
	t.Run("dates", func(t *testing.T) {
		expectOrder(t, [][]bson.View{
			{dateView(t, math.MinInt64)},
			{dateView(t, -1)},
			{dateView(t, 0)},
			{dateView(t, 1)},
			{dateView(t, 1<<40)},
			{dateView(t, math.MaxInt64)},
		})
	})
	t.Run("booleans", func(t *testing.T) {
		expectOrder(t, [][]bson.View{{booleanView(t, false)}, {booleanView(t, true)}})
	})
	t.Run("documents tie", func(t *testing.T) {
		expectOrder(t, [][]bson.View{{
			viewOf(t, bsontype.EmbeddedDocument, documentOf()),
			viewOf(t, bsontype.EmbeddedDocument, documentOf(elementOf(0x10, "a", int32Of(1)))),
			viewOf(t, bsontype.EmbeddedDocument, documentOf(elementOf(0x10, "a", int32Of(2)))),
			viewOf(t, bsontype.EmbeddedDocument, documentOf(elementOf(0x02, "z", stringValueOf("zzz")))),
		}})
	})
	t.Run("arrays tie", func(t *testing.T) {
		expectOrder(t, [][]bson.View{{
			viewOf(t, bsontype.Array, documentOf()),
			viewOf(t, bsontype.Array, documentOf(elementOf(0x10, "0", int32Of(1)))),
			viewOf(t, bsontype.Array, documentOf(elementOf(0x10, "0", int32Of(-9)), elementOf(0x10, "1", int32Of(3)))),
		}})
	})
	t.Run("null and missing tie", func(t *testing.T) {
		expectOrder(t, [][]bson.View{{viewOf(t, bsontype.Null, nil), {}}})
	})
}

// TestCompareTiesEachOtherTypeWithItself: inside the other class a type is
// compared only by its type byte, so two values of one type tie whatever
// their bytes.
func TestCompareTiesEachOtherTypeWithItself(t *testing.T) {
	pairs := map[string][2]bson.View{
		"binary":    {viewOf(t, bsontype.Binary, binaryValueOf(0x00, nil)), viewOf(t, bsontype.Binary, binaryValueOf(0x04, bytes.Repeat([]byte{9}, 16)))},
		"object id": {viewOf(t, bsontype.ObjectID, make([]byte, 12)), viewOf(t, bsontype.ObjectID, bytes.Repeat([]byte{0xFF}, 12))},
		"regex":     {viewOf(t, bsontype.Regex, []byte("a\x00\x00")), viewOf(t, bsontype.Regex, []byte("z\x00imx\x00"))},
		"timestamp": {viewOf(t, bsontype.Timestamp, make([]byte, 8)), viewOf(t, bsontype.Timestamp, bytes.Repeat([]byte{0xFF}, 8))},
		"symbol":    {viewOf(t, bsontype.Symbol, stringValueOf("a")), viewOf(t, bsontype.Symbol, stringValueOf("b"))},
		"code":      {viewOf(t, bsontype.JavaScript, stringValueOf("a")), viewOf(t, bsontype.JavaScript, stringValueOf("b"))},
	}
	for name, pair := range pairs {
		t.Run(name, func(t *testing.T) {
			expectOrder(t, [][]bson.View{{pair[0], pair[1]}})
		})
	}
}

// literalCase is one literal beside the View of the same value.
type literalCase struct {
	literal bson.Literal
	view    bson.View
}

func literalCases(t testing.TB) []literalCase {
	return []literalCase{
		{bson.Int32Literal(-7), int32View(t, -7)},
		{bson.Int64Literal(1<<53 + 1), int64View(t, 1<<53+1)},
		{bson.Int64Literal(math.MinInt64), int64View(t, math.MinInt64)},
		{bson.DoubleLiteral(math.NaN()), doubleView(t, math.NaN())},
		{bson.DoubleLiteral(math.Copysign(0, -1)), doubleView(t, math.Copysign(0, -1))},
		{bson.DoubleLiteral(1<<53 + 2), doubleView(t, 1<<53+2)},
		{bson.DoubleLiteral(2.5), doubleView(t, 2.5)},
		{bson.DoubleLiteral(math.Inf(-1)), doubleView(t, math.Inf(-1))},
		{bson.StringLiteral(""), stringView(t, "")},
		{bson.StringLiteral("ab"), stringView(t, "ab")},
		{bson.StringLiteral("\xff"), stringView(t, "\xff")},
		{bson.DateTimeLiteral(-1), dateView(t, -1)},
		{bson.DateTimeLiteral(1 << 40), dateView(t, 1<<40)},
		{bson.BooleanLiteral(false), booleanView(t, false)},
		{bson.BooleanLiteral(true), booleanView(t, true)},
		{bson.NullLiteral(), viewOf(t, bsontype.Null, nil)},
		// The zero Literal is null by contract (docs/core-api.md), so it ties
		// with a null view; without CompareLiteral's zero-class branch it would
		// sort below every number.
		{bson.Literal{}, viewOf(t, bsontype.Null, nil)},
	}
}

// TestCompareLiteralMatchesCompare: a view against a literal orders exactly
// as the view against the View of the literal's value (spec 5.2's two
// forms), for every literal against every value of the table.
func TestCompareLiteralMatchesCompare(t *testing.T) {
	cases := literalCases(t)
	views := append(generatedViews(t, 200), rankSevenValues(t)...)
	for _, literalRow := range cases {
		views = append(views, literalRow.view)
	}
	for literalIndex, literalRow := range cases {
		for _, view := range views {
			want := bson.Compare(view, literalRow.view)
			if got := bson.CompareLiteral(view, literalRow.literal); got != want {
				t.Errorf("CompareLiteral(%v % x, literal #%d) = %d, Compare against its View = %d",
					view.Type(), view.Bytes(), literalIndex, got, want)
			}
		}
	}
}

// generatedViews is count Views of every type, weighted to the values that
// stress the order: equal numbers of different types, NaN, signed zero,
// infinities and integers past 2^53.
func generatedViews(t testing.TB, count int) []bson.View {
	random := rand.New(rand.NewSource(20261024))
	interesting := []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, math.Copysign(0, -1), 1, -1, 0.5,
		1 << 53, 1<<53 + 2, -(1 << 53), 0x1p63, -0x1p63, math.MaxFloat64, math.SmallestNonzeroFloat64}
	interestingIntegers := []int64{0, 1, -1, 1 << 53, 1<<53 + 1, -(1<<53 + 1), math.MaxInt64, math.MinInt64, math.MaxInt32, math.MinInt32}
	others := rankSevenValues(t)
	views := make([]bson.View, 0, count)
	for len(views) < count {
		switch random.Intn(12) {
		case 0:
			views = append(views, int32View(t, int32(interestingIntegers[random.Intn(len(interestingIntegers))])))
		case 1:
			views = append(views, int64View(t, interestingIntegers[random.Intn(len(interestingIntegers))]))
		case 2:
			views = append(views, doubleView(t, interesting[random.Intn(len(interesting))]))
		case 3:
			views = append(views, int32View(t, int32(random.Intn(7)-3)), doubleView(t, float64(random.Intn(13)-6)/2))
		case 4:
			text := make([]byte, random.Intn(4))
			for index := range text {
				text[index] = "aB\x00\xff"[random.Intn(4)]
			}
			views = append(views, stringView(t, string(text)))
		case 5:
			views = append(views, dateView(t, interestingIntegers[random.Intn(len(interestingIntegers))]))
		case 6:
			views = append(views, booleanView(t, random.Intn(2) == 1))
		case 7:
			views = append(views, viewOf(t, bsontype.EmbeddedDocument, documentOf(elementOf(0x10, "x", int32Of(random.Int31())))))
		case 8:
			views = append(views, viewOf(t, bsontype.Array, documentOf(elementOf(0x10, "0", int32Of(random.Int31())))))
		case 9:
			views = append(views, others[random.Intn(len(others))])
		case 10:
			views = append(views, viewOf(t, bsontype.Decimal128, decimalBytes(random.Uint64(), random.Uint64())))
		default:
			views = append(views, viewOf(t, bsontype.Null, nil), bson.View{})
		}
	}
	return views
}

// checkOrderProperties is the total preorder: every value ties with
// itself, swapping the arguments flips the sign, and order and ties are
// transitive. It returns the first broken property.
func checkOrderProperties(left, middle, right bson.View) error {
	for _, view := range []bson.View{left, middle, right} {
		if got := bson.Compare(view, view); got != 0 {
			return fmt.Errorf("Compare(x, x) = %d for %v % x", got, view.Type(), view.Bytes())
		}
	}
	pairs := [][2]bson.View{{left, middle}, {middle, right}, {left, right}}
	for _, pair := range pairs {
		forward, backward := bson.Compare(pair[0], pair[1]), bson.Compare(pair[1], pair[0])
		if forward < -1 || forward > 1 || forward != -backward {
			return fmt.Errorf("Compare(a, b) = %d and Compare(b, a) = %d for %v % x and %v % x",
				forward, backward, pair[0].Type(), pair[0].Bytes(), pair[1].Type(), pair[1].Bytes())
		}
	}
	leftMiddle, middleRight, leftRight := bson.Compare(left, middle), bson.Compare(middle, right), bson.Compare(left, right)
	if leftMiddle <= 0 && middleRight <= 0 && leftRight > 0 {
		return fmt.Errorf("a <= b and b <= c but a > c: %v % x, %v % x, %v % x",
			left.Type(), left.Bytes(), middle.Type(), middle.Bytes(), right.Type(), right.Bytes())
	}
	if leftMiddle == 0 && leftRight != middleRight {
		return fmt.Errorf("a ties b but Compare(a, c) = %d and Compare(b, c) = %d: %v % x, %v % x, %v % x",
			leftRight, middleRight, left.Type(), left.Bytes(), middle.Type(), middle.Bytes(), right.Type(), right.Bytes())
	}
	return nil
}

// TestCompareIsATotalPreorder: over generated values of every class, every
// triple keeps the order properties, and values with the same bytes tie.
func TestCompareIsATotalPreorder(t *testing.T) {
	views := generatedViews(t, 120)
	for _, left := range views {
		for _, middle := range views {
			for _, right := range views {
				if err := checkOrderProperties(left, middle, right); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

// fuzzTypes is the element type a fuzz input's type selector picks, every
// type and the missing value (0).
var fuzzTypes = []bsontype.Type{0, bsontype.Double, bsontype.String, bsontype.EmbeddedDocument, bsontype.Array,
	bsontype.Binary, bsontype.Undefined, bsontype.ObjectID, bsontype.Boolean, bsontype.DateTime, bsontype.Null,
	bsontype.Regex, bsontype.DBPointer, bsontype.JavaScript, bsontype.Symbol, bsontype.CodeWithScope,
	bsontype.Int32, bsontype.Timestamp, bsontype.Int64, bsontype.Decimal128, bsontype.MinKey, bsontype.MaxKey}

// fuzzView shapes payload into a value of the type the selector picks: a
// fixed-size value takes the payload's first bytes, zero-padded; a string
// type wraps it; a container holds it as one string member. It reports
// false where the shaped bytes are still not BSON (a regex with a NUL).
func fuzzView(selector byte, payload []byte) (bson.View, bool) {
	valueType := fuzzTypes[int(selector)%len(fuzzTypes)]
	fixed := func(size int) []byte {
		valueBytes := make([]byte, size)
		copy(valueBytes, payload)
		return valueBytes
	}
	switch valueType {
	case 0:
		return bson.View{}, true
	case bsontype.Boolean:
		return tryViewOf(valueType, fixed(1))
	case bsontype.Int32:
		return tryViewOf(valueType, fixed(4))
	case bsontype.Double, bsontype.DateTime, bsontype.Timestamp, bsontype.Int64:
		return tryViewOf(valueType, fixed(8))
	case bsontype.ObjectID:
		return tryViewOf(valueType, fixed(12))
	case bsontype.Decimal128:
		return tryViewOf(valueType, fixed(16))
	case bsontype.Undefined, bsontype.Null, bsontype.MinKey, bsontype.MaxKey:
		return tryViewOf(valueType, nil)
	case bsontype.String, bsontype.JavaScript, bsontype.Symbol:
		return tryViewOf(valueType, stringValueOf(string(payload)))
	case bsontype.DBPointer:
		return tryViewOf(valueType, append(stringValueOf(string(payload)), make([]byte, 12)...))
	case bsontype.Binary:
		return tryViewOf(valueType, binaryValueOf(0x00, payload))
	case bsontype.Regex:
		return tryViewOf(valueType, append(append([]byte(nil), payload...), 0, 0))
	case bsontype.CodeWithScope:
		return tryViewOf(valueType, codeWithScopeOf(string(payload), documentOf()))
	}
	return tryViewOf(valueType, documentOf(elementOf(0x02, "s", stringValueOf(string(payload)))))
}

// FuzzCompareOrderProperties: for any three values the fuzzer builds, the
// order properties of TestCompareIsATotalPreorder hold, and the literal
// form agrees with the view form wherever the value has a literal.
func FuzzCompareOrderProperties(f *testing.F) {
	nan := doubleBytes(math.NaN())
	f.Add(byte(1), nan, byte(16), int32Of(0), byte(18), int64Bytes(1<<53+1))
	f.Add(byte(1), doubleBytes(1<<53), byte(18), int64Bytes(1<<53+1), byte(1), doubleBytes(1<<53+2))
	f.Add(byte(18), int64Bytes(1<<53+1), byte(1), doubleBytes(1<<53), byte(18), int64Bytes(1<<53))
	f.Add(byte(2), []byte("a"), byte(14), []byte("a"), byte(13), []byte("b"))
	f.Add(byte(19), []byte{1}, byte(19), []byte{2}, byte(10), []byte{})
	f.Add(byte(0), []byte{}, byte(10), []byte{}, byte(20), []byte{})
	f.Fuzz(func(t *testing.T, leftType byte, leftPayload []byte, middleType byte, middlePayload []byte, rightType byte, rightPayload []byte) {
		left, leftOK := fuzzView(leftType, leftPayload)
		middle, middleOK := fuzzView(middleType, middlePayload)
		right, rightOK := fuzzView(rightType, rightPayload)
		if !leftOK || !middleOK || !rightOK {
			t.Skip("an input shaped into bytes Validate refuses")
		}
		if err := checkOrderProperties(left, middle, right); err != nil {
			t.Fatal(err)
		}
		if literal, ok := literalOf(left); ok {
			for _, view := range []bson.View{middle, right} {
				if got, want := bson.CompareLiteral(view, literal), bson.Compare(view, left); got != want {
					t.Fatalf("CompareLiteral = %d, Compare = %d for %v % x against %v % x", got, want, view.Type(), view.Bytes(), left.Type(), left.Bytes())
				}
			}
		}
	})
}

// literalOf is the literal of a View's value, for the types that have one,
// read with encoding/binary rather than the comparator's own reading.
func literalOf(view bson.View) (bson.Literal, bool) {
	valueBytes := view.Bytes()
	switch view.Type() {
	case bsontype.Int32:
		return bson.Int32Literal(int32(binary.LittleEndian.Uint32(valueBytes))), true
	case bsontype.Int64:
		return bson.Int64Literal(int64(binary.LittleEndian.Uint64(valueBytes))), true
	case bsontype.Double:
		return bson.DoubleLiteral(math.Float64frombits(binary.LittleEndian.Uint64(valueBytes))), true
	case bsontype.String:
		return bson.StringLiteral(string(valueBytes[4 : len(valueBytes)-1])), true
	case bsontype.DateTime:
		return bson.DateTimeLiteral(int64(binary.LittleEndian.Uint64(valueBytes))), true
	case bsontype.Boolean:
		return bson.BooleanLiteral(valueBytes[0] == 1), true
	case bsontype.Null:
		return bson.NullLiteral(), true
	}
	return bson.Literal{}, false
}

// TestCompareAllocatesNothing: both forms compare on the bytes in place
// (spec 5.2, 9.2), so a comparison of any two values allocates nothing.
func TestCompareAllocatesNothing(t *testing.T) {
	if raceEnabled {
		t.Skip("-race changes allocation counts; the gates run this test without it")
	}
	views := append(generatedViews(t, 60), rankSevenValues(t)...)
	cases := literalCases(t)
	total := 0
	allocations := testing.AllocsPerRun(20, func() {
		for _, left := range views {
			for _, right := range views {
				total += bson.Compare(left, right)
			}
			for _, literalRow := range cases {
				total += bson.CompareLiteral(left, literalRow.literal)
			}
		}
	})
	if allocations != 0 {
		t.Errorf("comparing allocates %v times per run, want 0 (sum %d)", allocations, total)
	}
}
