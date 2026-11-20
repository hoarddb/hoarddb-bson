// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package benchdata

import (
	"bytes"
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"
)

func TestRef450IsItsSize(t *testing.T) {
	structBytes, err := Ref450Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(structBytes) != Ref450Size {
		t.Fatalf("Ref450 encodes to %d bytes, want %d", len(structBytes), Ref450Size)
	}
	orderedBytes, err := vendorbson.Marshal(Ref450D())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(structBytes, orderedBytes) {
		t.Fatal("the struct and bson.D forms of Ref450 encode differently")
	}
}
