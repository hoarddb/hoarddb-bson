// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package vectors_test

import (
	"testing"

	"github.com/hoarddb/hoarddb-bson/oracle/internal/vectors"
)

func TestDriverAndV1VectorsLoad(t *testing.T) {
	bsonVectors, err := vectors.LoadBSON()
	if err != nil {
		t.Fatalf("loading bson-vectors/v1.json: %v", err)
	}
	driverVectors, err := vectors.LoadDriver()
	if err != nil {
		t.Fatalf("loading driver-vectors/v1.json: %v", err)
	}
	bsonKinds := map[string]int{}
	for _, bsonVector := range bsonVectors {
		bsonKinds[bsonVector.Kind]++
	}
	driverKinds := map[string]int{}
	for _, driverVector := range driverVectors {
		driverKinds[driverVector.Kind]++
	}
	t.Logf("bson-vectors/v1.json: %d cases by kind %v", len(bsonVectors), bsonKinds)
	t.Logf("driver-vectors/v1.json: %d cases by kind %v", len(driverVectors), driverKinds)
	if len(bsonVectors) == 0 || len(driverVectors) == 0 {
		t.Fatalf("a vector set is empty: bson %d, driver %d", len(bsonVectors), len(driverVectors))
	}
	for _, bsonVector := range bsonVectors {
		if bsonVector.Name == "" || bsonVector.Kind == "" {
			t.Errorf("a bson vector has no name or kind: %+v", bsonVector)
		}
	}
}

func TestBSONVectorHexDecodes(t *testing.T) {
	bsonVectors, err := vectors.LoadBSON()
	if err != nil {
		t.Fatalf("loading bson-vectors/v1.json: %v", err)
	}
	var withBytes int
	for _, bsonVector := range bsonVectors {
		if bsonVector.Hex == nil {
			continue
		}
		withBytes++
		if bsonVector.Kind == "encode" && len(bsonVector.Hex) < 5 {
			t.Errorf("%s: encode vector has %d bytes", bsonVector.Name, len(bsonVector.Hex))
		}
	}
	if withBytes == 0 {
		t.Fatal("no bson vector carries bytes")
	}
	t.Logf("%d of %d bson vectors carry bytes", withBytes, len(bsonVectors))
}

func TestDriverVectorsCarryPayloads(t *testing.T) {
	driverVectors, err := vectors.LoadDriver()
	if err != nil {
		t.Fatalf("loading driver-vectors/v1.json: %v", err)
	}
	var payloadCount int
	for _, driverVector := range driverVectors {
		payloads, err := driverVector.PayloadBytes()
		if err != nil {
			t.Fatalf("reading the payloads of %q: %v", driverVector.Name, err)
		}
		payloadCount += len(payloads)
	}
	t.Logf("%d payload_hex strings in %d driver vectors", payloadCount, len(driverVectors))
	if payloadCount == 0 {
		t.Fatal("no driver vector carries a payload")
	}
}
