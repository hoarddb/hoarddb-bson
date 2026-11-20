// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	vendorprimitive "go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// errorText renders an error so that two sides can be compared, including
// the case where neither failed.
func errorText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// describeType renders everything about a type that a call site can observe
// without calling a method: its kind, its element types and every field with
// its tag, exported or not.
func describeType(goType reflect.Type) string {
	var description strings.Builder
	fmt.Fprintf(&description, "name=%s kind=%s", goType.Name(), goType.Kind())
	switch goType.Kind() {
	case reflect.Array:
		fmt.Fprintf(&description, " len=%d elem=%s", goType.Len(), goType.Elem())
	case reflect.Slice, reflect.Pointer:
		fmt.Fprintf(&description, " elem=%s", goType.Elem())
	case reflect.Struct:
		for fieldIndex := range goType.NumField() {
			field := goType.Field(fieldIndex)
			fmt.Fprintf(&description, " field(%s %s %q exported=%t)", field.Name, field.Type, field.Tag, field.IsExported())
		}
	}
	return description.String()
}

// methodSignatures maps every exported method of a type to its signature,
// receiver included. Both packages are named primitive, so the signature
// strings are comparable as they are.
func methodSignatures(goType reflect.Type) map[string]string {
	signatures := map[string]string{}
	for methodIndex := range goType.NumMethod() {
		method := goType.Method(methodIndex)
		signatures[method.Name] = method.Type.String()
	}
	return signatures
}

func TestScalarMethodSetsMatchVendor(t *testing.T) {
	typePairs := []struct {
		name       string
		vendorType reflect.Type
		libType    reflect.Type
	}{
		{"ObjectID", reflect.TypeOf(vendorprimitive.ObjectID{}), reflect.TypeOf(primitive.ObjectID{})},
		{"DateTime", reflect.TypeOf(vendorprimitive.DateTime(0)), reflect.TypeOf(primitive.DateTime(0))},
		{"Binary", reflect.TypeOf(vendorprimitive.Binary{}), reflect.TypeOf(primitive.Binary{})},
		{"Timestamp", reflect.TypeOf(vendorprimitive.Timestamp{}), reflect.TypeOf(primitive.Timestamp{})},
		{"Regex", reflect.TypeOf(vendorprimitive.Regex{}), reflect.TypeOf(primitive.Regex{})},
		{"DBPointer", reflect.TypeOf(vendorprimitive.DBPointer{}), reflect.TypeOf(primitive.DBPointer{})},
		{"CodeWithScope", reflect.TypeOf(vendorprimitive.CodeWithScope{}), reflect.TypeOf(primitive.CodeWithScope{})},
		{"Symbol", reflect.TypeOf(vendorprimitive.Symbol("")), reflect.TypeOf(primitive.Symbol(""))},
		{"JavaScript", reflect.TypeOf(vendorprimitive.JavaScript("")), reflect.TypeOf(primitive.JavaScript(""))},
		{"Undefined", reflect.TypeOf(vendorprimitive.Undefined{}), reflect.TypeOf(primitive.Undefined{})},
		{"Null", reflect.TypeOf(vendorprimitive.Null{}), reflect.TypeOf(primitive.Null{})},
		{"MinKey", reflect.TypeOf(vendorprimitive.MinKey{}), reflect.TypeOf(primitive.MinKey{})},
		{"MaxKey", reflect.TypeOf(vendorprimitive.MaxKey{}), reflect.TypeOf(primitive.MaxKey{})},
	}
	for _, pair := range typePairs {
		t.Run(pair.name, func(t *testing.T) {
			if got, want := describeType(pair.libType), describeType(pair.vendorType); got != want {
				t.Errorf("type shape = %s, want %s", got, want)
			}
			for _, receiver := range []struct {
				label      string
				vendorType reflect.Type
				libType    reflect.Type
			}{
				{"value receiver set", pair.vendorType, pair.libType},
				{"pointer receiver set", reflect.PointerTo(pair.vendorType), reflect.PointerTo(pair.libType)},
			} {
				want := methodSignatures(receiver.vendorType)
				got := methodSignatures(receiver.libType)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s of %s: library has %v, vendor has %v", receiver.label, pair.name, got, want)
				}
			}
		})
	}
}

// TestScalarPackageLevelNamesMatchVendor covers the functions and variables
// of the scalar types. The names beyond the inventory's reference list are
// the rest of the vendor's exported set for these types: a type is mirrored
// whole (spec 5.1 item 2).
func TestScalarPackageLevelNamesMatchVendor(t *testing.T) {
	functionPairs := []struct {
		name       string
		vendorFunc any
		libFunc    any
	}{
		{"NewObjectID", vendorprimitive.NewObjectID, primitive.NewObjectID},
		{"NewObjectIDFromTimestamp", vendorprimitive.NewObjectIDFromTimestamp, primitive.NewObjectIDFromTimestamp},
		{"ObjectIDFromHex", vendorprimitive.ObjectIDFromHex, primitive.ObjectIDFromHex},
		{"IsValidObjectID", vendorprimitive.IsValidObjectID, primitive.IsValidObjectID}, //nolint:staticcheck // mirrored deprecated name
		{"NewDateTimeFromTime", vendorprimitive.NewDateTimeFromTime, primitive.NewDateTimeFromTime},
		{"CompareTimestamp", vendorprimitive.CompareTimestamp, primitive.CompareTimestamp}, //nolint:staticcheck // mirrored deprecated name
	}
	for _, pair := range functionPairs {
		if got, want := reflect.TypeOf(pair.libFunc).String(), reflect.TypeOf(pair.vendorFunc).String(); got != want {
			t.Errorf("%s: library signature %s, vendor signature %s", pair.name, got, want)
		}
	}
	if got, want := reflect.TypeOf(primitive.NilObjectID).String(), reflect.TypeOf(vendorprimitive.NilObjectID).String(); got != want {
		t.Errorf("NilObjectID type = %s, want %s", got, want)
	}
	if got, want := errorText(primitive.ErrInvalidHex), errorText(vendorprimitive.ErrInvalidHex); got != want {
		t.Errorf("ErrInvalidHex = %q, want %q", got, want)
	}
}

var objectIDSamples = map[string]primitive.ObjectID{
	"zero":      {},
	"all ones":  {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	"counting":  {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
	"epoch end": {0x7f, 0xff, 0xff, 0xff, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x00, 0x00, 0x01},
	"high bit":  {0x80, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8},
	"fresh":     primitive.NewObjectID(),
}

func TestObjectIDRenderingsMatchVendor(t *testing.T) {
	for name, libID := range objectIDSamples {
		t.Run(name, func(t *testing.T) {
			vendorID := vendorprimitive.ObjectID(libID)
			if got, want := libID.Hex(), vendorID.Hex(); got != want {
				t.Errorf("Hex = %q, want %q", got, want)
			}
			if got, want := libID.String(), vendorID.String(); got != want {
				t.Errorf("String = %q, want %q", got, want)
			}
			if got, want := fmt.Sprintf("%v|%s|%q|%x", libID, libID, libID, libID), fmt.Sprintf("%v|%s|%q|%x", vendorID, vendorID, vendorID, vendorID); got != want {
				t.Errorf("fmt verbs = %q, want %q", got, want)
			}
			if got, want := libID.IsZero(), vendorID.IsZero(); got != want {
				t.Errorf("IsZero = %t, want %t", got, want)
			}
			libTime, vendorTime := libID.Timestamp(), vendorID.Timestamp()
			if !libTime.Equal(vendorTime) || libTime.Location() != vendorTime.Location() {
				t.Errorf("Timestamp = %v (%v), want %v (%v)", libTime, libTime.Location(), vendorTime, vendorTime.Location())
			}
			libJSON, libErr := libID.MarshalJSON()
			vendorJSON, vendorErr := vendorID.MarshalJSON()
			if string(libJSON) != string(vendorJSON) || errorText(libErr) != errorText(vendorErr) {
				t.Errorf("MarshalJSON = %s, %v; want %s, %v", libJSON, libErr, vendorJSON, vendorErr)
			}
			libText, libErr := libID.MarshalText()
			vendorText, vendorErr := vendorID.MarshalText()
			if string(libText) != string(vendorText) || errorText(libErr) != errorText(vendorErr) {
				t.Errorf("MarshalText = %s, %v; want %s, %v", libText, libErr, vendorText, vendorErr)
			}
		})
	}
}

func TestObjectIDInsideEncodingJSONMatchesVendor(t *testing.T) {
	type libHolder struct {
		ID  primitive.ObjectID
		Ptr *primitive.ObjectID
		Map map[primitive.ObjectID]int
	}
	type vendorHolder struct {
		ID  vendorprimitive.ObjectID
		Ptr *vendorprimitive.ObjectID
		Map map[vendorprimitive.ObjectID]int
	}
	libID := objectIDSamples["counting"]
	vendorID := vendorprimitive.ObjectID(libID)
	libBytes, libErr := json.Marshal(libHolder{ID: libID, Ptr: &libID, Map: map[primitive.ObjectID]int{libID: 1}})
	vendorBytes, vendorErr := json.Marshal(vendorHolder{ID: vendorID, Ptr: &vendorID, Map: map[vendorprimitive.ObjectID]int{vendorID: 1}})
	if string(libBytes) != string(vendorBytes) || errorText(libErr) != errorText(vendorErr) {
		t.Fatalf("json.Marshal = %s, %v; want %s, %v", libBytes, libErr, vendorBytes, vendorErr)
	}
	var libBack libHolder
	var vendorBack vendorHolder
	libErr = json.Unmarshal(vendorBytes, &libBack)
	vendorErr = json.Unmarshal(vendorBytes, &vendorBack)
	if errorText(libErr) != errorText(vendorErr) || vendorprimitive.ObjectID(libBack.ID) != vendorBack.ID ||
		len(libBack.Map) != len(vendorBack.Map) {
		t.Fatalf("json.Unmarshal = %+v, %v; want %+v, %v", libBack, libErr, vendorBack, vendorErr)
	}
}

func TestObjectIDParsingMatchesVendor(t *testing.T) {
	validHex := "000102030405060708090a0b"
	inputs := []string{
		"", validHex, strings.ToUpper("ffeeddccbbaa99887766554433221100"[:24]), "000102030405060708090a0", validHex + "0",
		"zz0102030405060708090a0b", "000102030405060708090a0z", "00010203040506070809 a0b", "0x0102030405060708090a0b",
		"000102030405060708090a0é", "\x00",
	}
	for _, input := range inputs {
		libID, libErr := primitive.ObjectIDFromHex(input)
		vendorID, vendorErr := vendorprimitive.ObjectIDFromHex(input)
		if vendorprimitive.ObjectID(libID) != vendorID || errorText(libErr) != errorText(vendorErr) {
			t.Errorf("ObjectIDFromHex(%q) = %v, %v; want %v, %v", input, libID, libErr, vendorID, vendorErr)
		}
		if errors.Is(libErr, primitive.ErrInvalidHex) != errors.Is(vendorErr, vendorprimitive.ErrInvalidHex) {
			t.Errorf("ObjectIDFromHex(%q): errors.Is(ErrInvalidHex) differs: library %v, vendor %v", input, libErr, vendorErr)
		}
		if got, want := primitive.IsValidObjectID(input), vendorprimitive.IsValidObjectID(input); got != want { //nolint:staticcheck // mirrored deprecated name
			t.Errorf("IsValidObjectID(%q) = %t, want %t", input, got, want)
		}

		// A failed decode may leave a prefix written into the target. The
		// prefilled target makes that observable.
		prefilled := primitive.ObjectID{0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee}
		libTarget, vendorTarget := prefilled, vendorprimitive.ObjectID(prefilled)
		libErr = libTarget.UnmarshalText([]byte(input))
		vendorErr = vendorTarget.UnmarshalText([]byte(input))
		if vendorprimitive.ObjectID(libTarget) != vendorTarget || errorText(libErr) != errorText(vendorErr) {
			t.Errorf("UnmarshalText(%q) left %v, %v; want %v, %v", input, libTarget, libErr, vendorTarget, vendorErr)
		}
	}
}

func TestObjectIDUnmarshalJSONMatchesVendor(t *testing.T) {
	inputs := []string{
		`null`, `""`, `"000102030405060708090a0b"`, `"000102030405060708090A0B"`, `{"$oid":"000102030405060708090a0b"}`,
		`{"$oid":""}`, `{"$oid":1}`, `{"$oid":"abc"}`, `{"other":"000102030405060708090a0b"}`, `{}`, `[]`, `1`, `true`,
		`"abc"`, `"zz0102030405060708090a0b"`, `"000102030405060708090a0z"`, `"000102030405060708090a0b0"`,
		`abcdefghijkl`, `"abcdefghij"`, `{"a":1,"b":2}`, `not json`, ``, `"`, `"0"`, `{"$oid":"000102030405060708090a0b"} `,
		`{"$oid":"000102030405060708090a0b","x":1}`, `"000102030405060708090a0b" x`,
	}
	prefilled := primitive.ObjectID{0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee}
	for _, input := range inputs {
		libTarget, vendorTarget := prefilled, vendorprimitive.ObjectID(prefilled)
		libErr := libTarget.UnmarshalJSON([]byte(input))
		vendorErr := vendorTarget.UnmarshalJSON([]byte(input))
		if vendorprimitive.ObjectID(libTarget) != vendorTarget || errorText(libErr) != errorText(vendorErr) {
			t.Errorf("UnmarshalJSON(%q) left %v, %v; want %v, %v", input, libTarget, libErr, vendorTarget, vendorErr)
		}
	}
}

func TestNewObjectIDLayout(t *testing.T) {
	before := time.Now().Unix()
	first, second := primitive.NewObjectID(), primitive.NewObjectID()
	after := time.Now().Unix()
	if stamp := first.Timestamp().Unix(); stamp < before || stamp > after {
		t.Errorf("timestamp = %d, want within [%d, %d]", stamp, before, after)
	}
	if !reflect.DeepEqual(first[4:9], second[4:9]) {
		t.Errorf("process-unique bytes differ within one process: %x vs %x", first[4:9], second[4:9])
	}
	counter := func(id primitive.ObjectID) uint32 { return uint32(id[9])<<16 | uint32(id[10])<<8 | uint32(id[11]) }
	if got, want := counter(second), (counter(first)+1)&0xffffff; got != want {
		t.Errorf("counter after one more ID = %d, want %d", got, want)
	}

	fixedTime := time.Date(2001, 9, 9, 1, 46, 40, 0, time.UTC)
	fromTimestamp := primitive.NewObjectIDFromTimestamp(fixedTime)
	vendorFromTimestamp := vendorprimitive.NewObjectIDFromTimestamp(fixedTime)
	if !reflect.DeepEqual(fromTimestamp[0:4], vendorFromTimestamp[0:4]) {
		t.Errorf("NewObjectIDFromTimestamp time bytes = %x, want %x", fromTimestamp[0:4], vendorFromTimestamp[0:4])
	}
	if got := fromTimestamp.Timestamp(); !got.Equal(fixedTime) {
		t.Errorf("NewObjectIDFromTimestamp round trip = %v, want %v", got, fixedTime)
	}
}

func TestNewObjectIDIsUniqueUnderConcurrency(t *testing.T) {
	const goroutineCount, idsPerGoroutine = 64, 10_000
	generated := make([][]primitive.ObjectID, goroutineCount)
	var group sync.WaitGroup
	for goroutineIndex := range goroutineCount {
		group.Add(1)
		go func() {
			defer group.Done()
			own := make([]primitive.ObjectID, idsPerGoroutine)
			for idIndex := range own {
				own[idIndex] = primitive.NewObjectID()
			}
			generated[goroutineIndex] = own
		}()
	}
	group.Wait()

	seen := make(map[primitive.ObjectID]struct{}, goroutineCount*idsPerGoroutine)
	for _, own := range generated {
		for _, id := range own {
			if _, duplicate := seen[id]; duplicate {
				t.Fatalf("NewObjectID returned %s twice among %d IDs", id.Hex(), goroutineCount*idsPerGoroutine)
			}
			seen[id] = struct{}{}
		}
	}
}

func TestDateTimeRenderingsMatchVendor(t *testing.T) {
	// A UTC runner would hide a MarshalJSON that forgot .UTC(): both sides would
	// print Z. A fixed non-UTC local zone makes the offset visible.
	originalLocal := time.Local
	time.Local = time.FixedZone("UTC+5:30", 5*3600+30*60)
	t.Cleanup(func() { time.Local = originalLocal })
	dateTimeValues := []int64{
		0, 1, -1, 999, 1000, 1001, -999, -1000, -1001, -1500, 1_600_000_000_123, -1_600_000_000_123,
		253_402_300_799_999,  // 9999-12-31T23:59:59.999Z, the last year JSON accepts
		253_402_300_800_000,  // year 10000
		-62_135_596_800_000,  // 0001-01-01
		-62_167_219_200_000,  // year 0
		-62_167_219_200_001,  // year -1
		math.MaxInt64 / 1000, // large but without overflow in the seconds multiply
		math.MaxInt64, math.MinInt64,
	}
	for _, millis := range dateTimeValues {
		t.Run(fmt.Sprint(millis), func(t *testing.T) {
			libValue, vendorValue := primitive.DateTime(millis), vendorprimitive.DateTime(millis)
			libTime, vendorTime := libValue.Time(), vendorValue.Time()
			if !libTime.Equal(vendorTime) || libTime.Location() != vendorTime.Location() || libTime.String() != vendorTime.String() {
				t.Errorf("Time = %v, want %v", libTime, vendorTime)
			}
			libJSON, libErr := libValue.MarshalJSON()
			vendorJSON, vendorErr := vendorValue.MarshalJSON()
			if string(libJSON) != string(vendorJSON) || errorText(libErr) != errorText(vendorErr) {
				t.Errorf("MarshalJSON = %s, %v; want %s, %v", libJSON, libErr, vendorJSON, vendorErr)
			}
			// The vendor type has no String method, so fmt prints the integer.
			if got, want := fmt.Sprintf("%v|%d|%+v", libValue, libValue, libValue), fmt.Sprintf("%v|%d|%+v", vendorValue, vendorValue, vendorValue); got != want {
				t.Errorf("fmt verbs = %q, want %q", got, want)
			}
		})
	}
}

func TestDateTimeFromTimeMatchesVendor(t *testing.T) {
	tokyo := time.FixedZone("UTC+9", 9*3600)
	times := []time.Time{
		{},
		time.Unix(0, 0),
		time.Unix(0, 999_999),
		time.Unix(0, 1_000_000),
		time.Unix(-1, 999_999_999), // 1969-12-31T23:59:59.999999999Z, floors to -1 ms
		time.Unix(-1, 999_000_000), // exactly -1 ms
		time.Unix(-1, 1),           // just after -1000 ms
		time.Unix(-2, 500_000_000), // -1500 ms
		time.Date(2026, 10, 24, 12, 30, 45, 123_456_789, tokyo),
		time.Date(9999, 12, 31, 23, 59, 59, 999_999_999, time.UTC),
		time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(-100, 1, 1, 0, 0, 0, 1, time.UTC),
	}
	for _, instant := range times {
		libValue, vendorValue := primitive.NewDateTimeFromTime(instant), vendorprimitive.NewDateTimeFromTime(instant)
		if int64(libValue) != int64(vendorValue) {
			t.Errorf("NewDateTimeFromTime(%v) = %d, want %d", instant, libValue, vendorValue)
		}
	}
}

func TestDateTimeUnmarshalJSONMatchesVendor(t *testing.T) {
	inputs := []string{
		`null`, `"2020-01-01T00:00:00Z"`, `"2020-01-01T00:00:00.123Z"`, `"2020-01-01T00:00:00.123456789Z"`,
		`"2020-01-01T00:00:00.9999999Z"`, `"1969-12-31T23:59:59.9995Z"`, `"1969-12-31T23:59:59.999Z"`, `"1970-01-01T00:00:00+09:00"`,
		`"0001-01-01T00:00:00Z"`, `"9999-12-31T23:59:59.999Z"`, `"2020-01-01"`, `"yesterday"`, `""`, `1`, `1577836800000`,
		`{"$date":"2020-01-01T00:00:00Z"}`, `true`, `[]`, `not json`, ``, `"2020-01-01T00:00:00Z" x`,
	}
	const sentinel = 4242
	for _, input := range inputs {
		libTarget, vendorTarget := primitive.DateTime(sentinel), vendorprimitive.DateTime(sentinel)
		libErr := libTarget.UnmarshalJSON([]byte(input))
		vendorErr := vendorTarget.UnmarshalJSON([]byte(input))
		if int64(libTarget) != int64(vendorTarget) || errorText(libErr) != errorText(vendorErr) {
			t.Errorf("UnmarshalJSON(%q) left %d, %v; want %d, %v", input, libTarget, libErr, vendorTarget, vendorErr)
		}
	}

	type libHolder struct{ When primitive.DateTime }
	type vendorHolder struct{ When vendorprimitive.DateTime }
	libBytes, _ := json.Marshal(libHolder{When: 1_600_000_000_123})
	vendorBytes, _ := json.Marshal(vendorHolder{When: 1_600_000_000_123})
	if string(libBytes) != string(vendorBytes) {
		t.Errorf("json.Marshal of a DateTime field = %s, want %s", libBytes, vendorBytes)
	}
}

func TestTimestampCompareMatchesVendor(t *testing.T) {
	components := []uint32{0, 1, 2, math.MaxUint32 - 1, math.MaxUint32}
	var libValues []primitive.Timestamp
	for _, seconds := range components {
		for _, increment := range components {
			libValues = append(libValues, primitive.Timestamp{T: seconds, I: increment})
		}
	}
	for _, left := range libValues {
		vendorLeft := vendorprimitive.Timestamp{T: left.T, I: left.I}
		if got, want := left.IsZero(), vendorLeft.IsZero(); got != want {
			t.Errorf("%v.IsZero = %t, want %t", left, got, want)
		}
		for _, right := range libValues {
			vendorRight := vendorprimitive.Timestamp{T: right.T, I: right.I}
			if got, want := left.Compare(right), vendorLeft.Compare(vendorRight); got != want {
				t.Errorf("%v.Compare(%v) = %d, want %d", left, right, got, want)
			}
			if got, want := left.After(right), vendorLeft.After(vendorRight); got != want {
				t.Errorf("%v.After(%v) = %t, want %t", left, right, got, want)
			}
			if got, want := left.Before(right), vendorLeft.Before(vendorRight); got != want {
				t.Errorf("%v.Before(%v) = %t, want %t", left, right, got, want)
			}
			if got, want := left.Equal(right), vendorLeft.Equal(vendorRight); got != want {
				t.Errorf("%v.Equal(%v) = %t, want %t", left, right, got, want)
			}
			if got, want := primitive.CompareTimestamp(left, right), vendorprimitive.CompareTimestamp(vendorLeft, vendorRight); got != want { //nolint:staticcheck // mirrored deprecated name
				t.Errorf("CompareTimestamp(%v, %v) = %d, want %d", left, right, got, want)
			}
		}
	}
	if got, want := fmt.Sprintf("%v|%+v", primitive.Timestamp{T: 7, I: 9}, primitive.Timestamp{T: 7, I: 9}),
		fmt.Sprintf("%v|%+v", vendorprimitive.Timestamp{T: 7, I: 9}, vendorprimitive.Timestamp{T: 7, I: 9}); got != want {
		t.Errorf("fmt verbs = %q, want %q", got, want)
	}
}

func TestBinaryAndRegexMatchVendor(t *testing.T) {
	binaries := []struct {
		subtype byte
		data    []byte
	}{
		{0, nil}, {0, []byte{}}, {0, []byte{1}}, {1, nil}, {0x80, []byte{1, 2, 3}}, {4, []byte{1, 2, 3}}, {0xff, []byte("abc")},
	}
	for _, left := range binaries {
		libLeft := primitive.Binary{Subtype: left.subtype, Data: left.data}
		vendorLeft := vendorprimitive.Binary{Subtype: left.subtype, Data: left.data}
		if got, want := libLeft.IsZero(), vendorLeft.IsZero(); got != want {
			t.Errorf("Binary %v IsZero = %t, want %t", left, got, want)
		}
		for _, right := range binaries {
			libRight := primitive.Binary{Subtype: right.subtype, Data: right.data}
			vendorRight := vendorprimitive.Binary{Subtype: right.subtype, Data: right.data}
			if got, want := libLeft.Equal(libRight), vendorLeft.Equal(vendorRight); got != want {
				t.Errorf("Binary %v Equal %v = %t, want %t", left, right, got, want)
			}
		}
	}

	regexes := []struct{ pattern, options string }{
		{"", ""}, {"a", ""}, {"", "i"}, {"^a.*b$", "im"}, {`quote " and \ backslash`, "x"}, {"nul\x00byte", "s"}, {"é", "u"},
	}
	for _, left := range regexes {
		libLeft := primitive.Regex{Pattern: left.pattern, Options: left.options}
		vendorLeft := vendorprimitive.Regex{Pattern: left.pattern, Options: left.options}
		if got, want := libLeft.String(), vendorLeft.String(); got != want {
			t.Errorf("Regex %v String = %q, want %q", left, got, want)
		}
		if got, want := fmt.Sprintf("%v|%s|%+v", libLeft, libLeft, libLeft), fmt.Sprintf("%v|%s|%+v", vendorLeft, vendorLeft, vendorLeft); got != want {
			t.Errorf("Regex %v fmt verbs = %q, want %q", left, got, want)
		}
		if got, want := libLeft.IsZero(), vendorLeft.IsZero(); got != want {
			t.Errorf("Regex %v IsZero = %t, want %t", left, got, want)
		}
		for _, right := range regexes {
			libRight := primitive.Regex{Pattern: right.pattern, Options: right.options}
			vendorRight := vendorprimitive.Regex{Pattern: right.pattern, Options: right.options}
			if got, want := libLeft.Equal(libRight), vendorLeft.Equal(vendorRight); got != want {
				t.Errorf("Regex %v Equal %v = %t, want %t", left, right, got, want)
			}
		}
	}
}

func TestLegacyTypesMatchVendor(t *testing.T) {
	pointers := []struct {
		database string
		pointer  primitive.ObjectID
	}{
		{"", primitive.ObjectID{}}, {"app", primitive.ObjectID{}}, {"", objectIDSamples["counting"]},
		{"app", objectIDSamples["counting"]}, {`quote " db`, objectIDSamples["all ones"]},
	}
	for _, left := range pointers {
		libLeft := primitive.DBPointer{DB: left.database, Pointer: left.pointer}
		vendorLeft := vendorprimitive.DBPointer{DB: left.database, Pointer: vendorprimitive.ObjectID(left.pointer)}
		if got, want := libLeft.String(), vendorLeft.String(); got != want {
			t.Errorf("DBPointer %v String = %q, want %q", left, got, want)
		}
		if got, want := fmt.Sprintf("%v|%s", libLeft, libLeft), fmt.Sprintf("%v|%s", vendorLeft, vendorLeft); got != want {
			t.Errorf("DBPointer %v fmt verbs = %q, want %q", left, got, want)
		}
		if got, want := libLeft.IsZero(), vendorLeft.IsZero(); got != want {
			t.Errorf("DBPointer %v IsZero = %t, want %t", left, got, want)
		}
		for _, right := range pointers {
			libRight := primitive.DBPointer{DB: right.database, Pointer: right.pointer}
			vendorRight := vendorprimitive.DBPointer{DB: right.database, Pointer: vendorprimitive.ObjectID(right.pointer)}
			if got, want := libLeft.Equal(libRight), vendorLeft.Equal(vendorRight); got != want {
				t.Errorf("DBPointer %v Equal %v = %t, want %t", left, right, got, want)
			}
		}
	}

	scopes := []any{nil, "scope", 42, int64(-7), 1.5, true, []int{1, 2}, map[string]int{"a": 1}, struct{ X, Y int }{1, 2}, []byte("raw")}
	for _, scope := range scopes {
		libValue := primitive.CodeWithScope{Code: "return 1", Scope: scope}
		vendorValue := vendorprimitive.CodeWithScope{Code: "return 1", Scope: scope}
		if got, want := libValue.String(), vendorValue.String(); got != want {
			t.Errorf("CodeWithScope with scope %#v String = %q, want %q", scope, got, want)
		}
		if got, want := fmt.Sprintf("%v|%s", libValue, libValue), fmt.Sprintf("%v|%s", vendorValue, vendorValue); got != want {
			t.Errorf("CodeWithScope with scope %#v fmt verbs = %q, want %q", scope, got, want)
		}
	}
	if got, want := (primitive.CodeWithScope{Code: `a"b`}).String(), (vendorprimitive.CodeWithScope{Code: `a"b`}).String(); got != want {
		t.Errorf("CodeWithScope quoting = %q, want %q", got, want)
	}

	// Symbol, JavaScript, Undefined, Null, MinKey and MaxKey carry no methods;
	// the method-set test pins that. Their formatting is what remains.
	if got, want := fmt.Sprintf("%v|%+v|%#v", primitive.Symbol("s"), primitive.JavaScript("j"), primitive.Undefined{}),
		fmt.Sprintf("%v|%+v|%#v", vendorprimitive.Symbol("s"), vendorprimitive.JavaScript("j"), vendorprimitive.Undefined{}); strings.ReplaceAll(got, "primitive.", "") != strings.ReplaceAll(want, "primitive.", "") {
		t.Errorf("fmt of the string-backed and empty types = %q, want %q", got, want)
	}
}
