// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson_test

import (
	"testing"

	"github.com/hoarddb/hoarddb-bson/bson"
	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
)

// TestBinarySubtypeConstantsMatchBsontype: every binary subtype the bsontype
// package defines is exported from bson under the TypeBinary prefix, so a
// caller needs one import for a binary element.
func TestBinarySubtypeConstantsMatchBsontype(t *testing.T) {
	cases := []struct {
		name string
		got  byte
		want byte
	}{
		{"TypeBinaryGeneric", bson.TypeBinaryGeneric, 0x00},
		{"TypeBinaryFunction", bson.TypeBinaryFunction, 0x01},
		{"TypeBinaryBinaryOld", bson.TypeBinaryBinaryOld, 0x02},
		{"TypeBinaryUUIDOld", bson.TypeBinaryUUIDOld, 0x03},
		{"TypeBinaryUUID", bson.TypeBinaryUUID, 0x04},
		{"TypeBinaryMD5", bson.TypeBinaryMD5, 0x05},
		{"TypeBinaryEncrypted", bson.TypeBinaryEncrypted, 0x06},
		{"TypeBinaryColumn", bson.TypeBinaryColumn, 0x07},
		{"TypeBinarySensitive", bson.TypeBinarySensitive, 0x08},
		{"TypeBinaryUserDefined", bson.TypeBinaryUserDefined, 0x80},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("bson.%s = 0x%02x, want 0x%02x", tc.name, tc.got, tc.want)
		}
	}
	if bson.TypeBinaryGeneric != bsontype.BinaryGeneric {
		t.Error("bson.TypeBinaryGeneric differs from bsontype.BinaryGeneric")
	}
}
