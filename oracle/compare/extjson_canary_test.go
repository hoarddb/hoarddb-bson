// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package compare_test

import (
	"bytes"
	"strings"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"

	"github.com/hoarddb/hoarddb-bson/oracle/compare"
)

// The canaries of compare.ExtJSON, the text form of spec 6.1's rule
// (TestMarshalExtJSONMapsCompareByElementSet uses it): a reordered struct
// field or bson.D element must fail, a reordered map must pass, and a
// changed number form, escape or wrapper inside a map value must fail.

func vendorExtJSON(t *testing.T, goValue any, canonical bool) []byte {
	t.Helper()
	text, err := vendorbson.MarshalExtJSON(goValue, canonical, false)
	if err != nil {
		t.Fatalf("vendor MarshalExtJSON(%T): %v", goValue, err)
	}
	return text
}

func extJSONIdentityControl(t *testing.T, goValue any, vendorText []byte) {
	t.Helper()
	recorder := newFailureRecorder(t)
	compare.ExtJSON(recorder, goValue, vendorText, bytes.Clone(vendorText))
	requireSilent(t, recorder, "identical texts")
}

func TestExtJSONCompareFailsOnReorderedStructField(t *testing.T) {
	person := orderedPerson{Name: "Ada", Age: 36, Email: "ada@example.org"}
	vendorText := vendorExtJSON(t, person, false)
	extJSONIdentityControl(t, person, vendorText)

	recorder := newFailureRecorder(t)
	compare.ExtJSON(recorder, person, vendorText, []byte(`{"age":36,"name":"Ada","email":"ada@example.org"}`))
	requireReported(t, recorder, "$")
}

func TestExtJSONCompareFailsOnReorderedBsonD(t *testing.T) {
	ordered := vendorbson.D{{Key: "a", Value: int32(1)}, {Key: "b", Value: int32(2)}}
	vendorText := vendorExtJSON(t, ordered, true)
	extJSONIdentityControl(t, ordered, vendorText)

	recorder := newFailureRecorder(t)
	compare.ExtJSON(recorder, ordered, vendorText, []byte(`{"b":{"$numberInt":"2"},"a":{"$numberInt":"1"}}`))
	requireReported(t, recorder, "$")
}

func TestExtJSONComparePassesOnReorderedMapKeys(t *testing.T) {
	nested := map[string]any{
		"alpha": int32(1),
		"beta":  "b",
		"gamma": map[string]any{"x": int32(1), "y": int32(2), "z": int32(3)},
		"list":  []any{map[string]any{"p": true, "q": false}},
	}
	descending := []byte(`{"list":[{"q":false,"p":true}],"gamma":{"z":3,"y":2,"x":1},"beta":"b","alpha":1}`)
	var vendorText []byte
	for attempt := 0; attempt < 100; attempt++ {
		if vendorText = vendorExtJSON(t, nested, false); !bytes.Equal(vendorText, descending) {
			break
		}
	}
	if bytes.Equal(vendorText, descending) {
		t.Fatal("the vendor wrote the stand-in's order 100 times; the reordering is not exercised")
	}
	recorder := newFailureRecorder(t)
	compare.ExtJSON(recorder, nested, vendorText, descending)
	requireSilent(t, recorder, "map levels in a different order")
}

func TestExtJSONCompareFailsOnAChangedNumberInAMap(t *testing.T) {
	counters := map[string]any{"ratio": 1.0}
	vendorText := vendorExtJSON(t, counters, false)
	extJSONIdentityControl(t, counters, vendorText)

	recorder := newFailureRecorder(t)
	compare.ExtJSON(recorder, counters, vendorText, []byte(`{"ratio":1}`))
	requireReported(t, recorder, "$.ratio")
}

func TestExtJSONCompareFailsOnAChangedWrapperInAMap(t *testing.T) {
	counters := map[string]any{"count": int64(7)}
	vendorText := vendorExtJSON(t, counters, true)
	extJSONIdentityControl(t, counters, vendorText)

	recorder := newFailureRecorder(t)
	compare.ExtJSON(recorder, counters, vendorText, []byte(`{"count":{"$numberInt":"7"}}`))
	requireReported(t, recorder, "$.count")
}

func TestExtJSONCompareFailsOnAChangedEscape(t *testing.T) {
	tagged := map[string]any{"html": "<b>"}
	vendorText, err := vendorbson.MarshalExtJSON(tagged, false, true)
	if err != nil {
		t.Fatal(err)
	}
	extJSONIdentityControl(t, tagged, vendorText)

	recorder := newFailureRecorder(t)
	compare.ExtJSON(recorder, tagged, vendorText, []byte(`{"html":"<b>"}`))
	requireReported(t, recorder, "$.html")

	recorder = newFailureRecorder(t)
	compare.ExtJSON(recorder, map[string]any{"<": 1}, []byte(`{"\u003c":1}`), []byte(`{"<":1}`))
	requireReported(t, recorder, "$")
}

func TestExtJSONComparePassesOnReorderedMapKeysAroundAnUnescapedNamespace(t *testing.T) {
	// The vendor writes a DBPointer's namespace unescaped, so a quote in it
	// leaves text that is not JSON; the map level must still compare as a
	// set, and a changed namespace must still fail.
	pointer := `{"$dbPointer":{"$ref":"a"b","$id":{"$oid":"000000000000000000000000"}}}`
	value := map[string]any{"p": 1, "q": 2}
	recorder := newFailureRecorder(t)
	compare.ExtJSON(recorder, value, []byte(`{"q":`+pointer+`,"p":1}`), []byte(`{"p":1,"q":`+pointer+`}`))
	requireSilent(t, recorder, "a reordered map holding an unescaped namespace")
	recorder = newFailureRecorder(t)
	compare.ExtJSON(recorder, value, []byte(`{"q":`+pointer+`,"p":1}`), []byte(`{"p":1,"q":`+strings.Replace(pointer, `a"b`, `a"c`, 1)+`}`))
	requireReported(t, recorder, "$.q")
}

func TestExtJSONComparePassesOnKeysThatShareASpelling(t *testing.T) {
	// "\xe8" and "\xff" are both written "�"; the map level holds two
	// members with one spelling, in either order, and a changed value under
	// either must still fail.
	value := map[string]any{"\xe8": "000", "\xff": "001"}
	escape := "\\" + "ufffd"
	vendorText := []byte(`{"` + escape + `":"001","` + escape + `":"000"}`)
	libText := []byte(`{"` + escape + `":"000","` + escape + `":"001"}`)
	recorder := newFailureRecorder(t)
	compare.ExtJSON(recorder, value, vendorText, libText)
	requireSilent(t, recorder, "two keys spelled alike, in another order")
	recorder = newFailureRecorder(t)
	compare.ExtJSON(recorder, value, vendorText, []byte(`{"`+escape+`":"000","`+escape+`":"002"}`))
	requireReported(t, recorder, "$")
}
