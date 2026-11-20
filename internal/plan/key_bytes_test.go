// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package plan_test

import (
	"reflect"
	"testing"
)

type keyBytesProbe struct {
	Upper string `bson:"Upper"`
	Lower string `bson:"upper"`
	Name  string
	Mixed string `bson:"MiXed"`
}

// TestFieldByKeyBytesMatchesFieldByKey: the step 4e decoder looks fields up
// by the key's bytes, which must find what FieldByKey finds (row N4, which
// the oracle holds to the vendor through FieldByKey), and an exact hit
// allocates nothing.
func TestFieldByKeyBytesMatchesFieldByKey(t *testing.T) {
	typePlan, err := newTestCache().Lookup(reflect.TypeOf(keyBytesProbe{}))
	if err != nil {
		t.Fatal(err)
	}
	layout := typePlan.Struct
	for _, key := range []string{"Upper", "upper", "UPPER", "uPPER", "name", "Name", "NAME", "MiXed", "mixed", "MIXED", "unknown", ""} {
		byString, foundByString := layout.FieldByKey(key)
		byBytes, foundByBytes := layout.FieldByKeyBytes([]byte(key))
		if foundByString != foundByBytes || byString != byBytes {
			t.Errorf("key %q: FieldByKey gives %v %t, FieldByKeyBytes %v %t", key, byString, foundByString, byBytes, foundByBytes)
		}
	}
	if raceEnabled {
		return
	}
	exactKey := []byte("upper")
	if allocations := testing.AllocsPerRun(100, func() { layout.FieldByKeyBytes(exactKey) }); allocations != 0 {
		t.Errorf("an exact FieldByKeyBytes hit allocates %.0f times, want 0", allocations)
	}
}
