// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

//go:build bsonoracle

package bson_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// tapRecording is a tap that keeps every event, before and after.
type tapRecording struct {
	mutex  sync.Mutex
	before []bson.OracleEvent
	after  []bson.OracleEvent
	// refuse, when set, is the error every call returns through the tap.
	refuse error
}

func (recording *tapRecording) tap(before bson.OracleEvent) func(bson.OracleEvent) error {
	recording.mutex.Lock()
	recording.before = append(recording.before, before)
	recording.mutex.Unlock()
	return func(after bson.OracleEvent) error {
		recording.mutex.Lock()
		defer recording.mutex.Unlock()
		recording.after = append(recording.after, after)
		return recording.refuse
	}
}

// installTap installs recording for the rest of the test.
func installTap(t *testing.T, recording *tapRecording) {
	t.Helper()
	bson.SetOracleTap(recording.tap)
	t.Cleanup(func() { bson.SetOracleTap(nil) })
}

// nestedMarshaler calls Marshal from inside MarshalBSON, so one call makes
// two.
type nestedMarshaler struct{}

func (nestedMarshaler) MarshalBSON() ([]byte, error) {
	return bson.Marshal(map[string]int32{"inner": 1})
}

// TestOracleTapSeesEveryCall: a recording tap sees one event per Marshal,
// MarshalValue, Unmarshal, RawValue.Unmarshal and Document.Decode call
// (build doc step 4f), with the call's inputs before it runs and its
// results after.
func TestOracleTapSeesEveryCall(t *testing.T) {
	recording := &tapRecording{}
	installTap(t, recording)

	encoded, err := bson.Marshal(map[string]int32{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	valueType, valueBytes, err := bson.MarshalValue(int32(5))
	if err != nil {
		t.Fatal(err)
	}
	target := map[string]any{"kept": true}
	if err := bson.Unmarshal(encoded, &target); err != nil {
		t.Fatal(err)
	}
	var number int64
	if err := (bson.RawValue{Type: valueType, Value: valueBytes}).Unmarshal(&number); err != nil {
		t.Fatal(err)
	}
	document, err := bson.TrustStored(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := document.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	_, refused := bson.Marshal("not a document")
	if refused == nil {
		t.Fatal("Marshal of a string succeeded")
	}
	if _, err := bson.Marshal(nestedMarshaler{}); err != nil {
		t.Fatal(err)
	}

	wantFunctions := []string{"Marshal", "MarshalValue", "Unmarshal", "RawValue.Unmarshal", "Document.Decode", "Marshal", "Marshal", "Marshal"}
	if len(recording.before) != len(wantFunctions) || len(recording.after) != len(wantFunctions) {
		t.Fatalf("the tap saw %d calls before and %d after, want %d each", len(recording.before), len(recording.after), len(wantFunctions))
	}
	for position, function := range wantFunctions {
		if recording.before[position].Function != function {
			t.Errorf("before event %d is %q, want %q", position, recording.before[position].Function, function)
		}
	}
	// The after events of the nested Marshal come in the order the calls
	// end: the inner Marshal before the outer.
	marshalAfter := recording.after[0]
	if !reflect.DeepEqual(marshalAfter.Bytes, encoded) || marshalAfter.Err != nil || !reflect.DeepEqual(marshalAfter.Value, map[string]int32{"a": 1}) {
		t.Errorf("Marshal's after event is %+v", marshalAfter)
	}
	valueAfter := recording.after[1]
	if valueAfter.Type != bsontype.Int32 || !reflect.DeepEqual(valueAfter.Bytes, valueBytes) || valueAfter.Value != int32(5) {
		t.Errorf("MarshalValue's after event is %+v", valueAfter)
	}
	unmarshalBefore, unmarshalAfter := recording.before[2], recording.after[2]
	if !reflect.DeepEqual(unmarshalBefore.Bytes, encoded) || unmarshalBefore.Value != any(&target) || unmarshalAfter.Value != any(&target) {
		t.Errorf("Unmarshal's events are %+v and %+v; both must carry the input and the target", unmarshalBefore, unmarshalAfter)
	}
	rawValueBefore := recording.before[3]
	if rawValueBefore.Type != bsontype.Int32 || !reflect.DeepEqual(rawValueBefore.Bytes, valueBytes) {
		t.Errorf("RawValue.Unmarshal's before event is %+v", rawValueBefore)
	}
	if refusedAfter := recording.after[5]; refusedAfter.Err == nil || refusedAfter.Err.Error() != refused.Error() {
		t.Errorf("the refused Marshal's after event carries %v, want %v", refusedAfter.Err, refused)
	}
	if inner, outer := recording.after[6], recording.after[7]; !reflect.DeepEqual(inner.Value, map[string]int32{"inner": 1}) || outer.Value != any(nestedMarshaler{}) {
		t.Errorf("the nested calls end as %+v then %+v, want the inner first", inner, outer)
	}
}

// TestOracleTapSeesTheTargetBeforeTheDecode: the before event comes before
// the decode writes the target, so a tap can copy a prefilled target and
// give the vendor the same one.
func TestOracleTapSeesTheTargetBeforeTheDecode(t *testing.T) {
	encoded, err := bson.Marshal(map[string]int32{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	var seenBefore []string
	bson.SetOracleTap(func(before bson.OracleEvent) func(bson.OracleEvent) error {
		for key := range *before.Value.(*map[string]any) {
			seenBefore = append(seenBefore, key)
		}
		return func(bson.OracleEvent) error { return nil }
	})
	t.Cleanup(func() { bson.SetOracleTap(nil) })
	target := map[string]any{"kept": true}
	if err := bson.Unmarshal(encoded, &target); err != nil {
		t.Fatal(err)
	}
	if len(seenBefore) != 1 || seenBefore[0] != "kept" {
		t.Errorf("the tap saw keys %v before the decode, want [kept]", seenBefore)
	}
}

// TestOracleTapErrorIsTheCallsError: a tap that reports a divergence makes
// the call return its error, which fails the test that made the call.
func TestOracleTapErrorIsTheCallsError(t *testing.T) {
	divergence := errors.New("the vendor encodes this differently")
	installTap(t, &tapRecording{refuse: divergence})
	encoded, err := bson.Marshal(map[string]int32{"a": 1})
	if !errors.Is(err, divergence) || encoded != nil {
		t.Errorf("Marshal returned (% x, %v), want (nil, the tap's error)", encoded, err)
	}
	if _, _, err := bson.MarshalValue(int32(1)); !errors.Is(err, divergence) {
		t.Errorf("MarshalValue returned %v, want the tap's error", err)
	}
	var target map[string]any
	if err := bson.Unmarshal([]byte{5, 0, 0, 0, 0}, &target); !errors.Is(err, divergence) {
		t.Errorf("Unmarshal returned %v, want the tap's error", err)
	}
	bson.SetOracleTap(nil)
	if _, err := bson.Marshal(map[string]int32{"a": 1}); err != nil {
		t.Errorf("Marshal after the tap was removed: %v", err)
	}
}

// TestOracleTapSeesExtendedJSONCalls: MarshalExtJSON and UnmarshalExtJSON
// are tapped too (spec 9.1.7: every Extended JSON call), with their mode
// arguments and the text as the event's bytes.
func TestOracleTapSeesExtendedJSONCalls(t *testing.T) {
	recording := &tapRecording{}
	installTap(t, recording)

	text, err := bson.MarshalExtJSON(map[string]int32{"a": 1}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	target := map[string]any{}
	if err := bson.UnmarshalExtJSON(text, false, &target); err != nil {
		t.Fatal(err)
	}
	wantFunctions := []string{"MarshalExtJSON", "UnmarshalExtJSON"}
	if len(recording.before) != len(wantFunctions) || len(recording.after) != len(wantFunctions) {
		t.Fatalf("the tap saw %d calls before and %d after, want %d each", len(recording.before), len(recording.after), len(wantFunctions))
	}
	marshalBefore, marshalAfter := recording.before[0], recording.after[0]
	if marshalBefore.Function != "MarshalExtJSON" || !marshalBefore.Canonical || !marshalBefore.EscapeHTML || marshalBefore.Bytes != nil {
		t.Errorf("MarshalExtJSON's before event is %+v", marshalBefore)
	}
	if !reflect.DeepEqual(marshalAfter.Bytes, text) || marshalAfter.Err != nil || !reflect.DeepEqual(marshalAfter.Value, map[string]int32{"a": 1}) {
		t.Errorf("MarshalExtJSON's after event is %+v", marshalAfter)
	}
	unmarshalBefore, unmarshalAfter := recording.before[1], recording.after[1]
	if unmarshalBefore.Function != "UnmarshalExtJSON" || unmarshalBefore.Canonical || !reflect.DeepEqual(unmarshalBefore.Bytes, text) || unmarshalBefore.Value != any(&target) {
		t.Errorf("UnmarshalExtJSON's before event is %+v", unmarshalBefore)
	}
	if unmarshalAfter.Value != any(&target) || unmarshalAfter.Err != nil {
		t.Errorf("UnmarshalExtJSON's after event is %+v", unmarshalAfter)
	}

	recording.refuse = errors.New("the tap refuses")
	if _, err := bson.MarshalExtJSON(map[string]int32{"a": 1}, false, false); !errors.Is(err, recording.refuse) {
		t.Errorf("MarshalExtJSON returned %v, want the tap's error", err)
	}
	if err := bson.UnmarshalExtJSON(text, false, &target); !errors.Is(err, recording.refuse) {
		t.Errorf("UnmarshalExtJSON returned %v, want the tap's error", err)
	}
}
