// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package oracle

import (
	"testing"

	vendorbson "go.mongodb.org/mongo-driver/bson"

	"github.com/hoarddb/hoarddb-bson/bson"
)

// TestTypeConstantsMatchTheVendor holds every bson.Type* constant to the
// vendor's value of the same name. The type constants are the library's
// bsontype.Type values and the binary subtypes its byte values, so both
// sides are compared as the underlying byte.
func TestTypeConstantsMatchTheVendor(t *testing.T) {
	cases := []struct {
		name            string
		library, vendor uint8
	}{
		{"TypeDouble", uint8(bson.TypeDouble), uint8(vendorbson.TypeDouble)},
		{"TypeString", uint8(bson.TypeString), uint8(vendorbson.TypeString)},
		{"TypeEmbeddedDocument", uint8(bson.TypeEmbeddedDocument), uint8(vendorbson.TypeEmbeddedDocument)},
		{"TypeArray", uint8(bson.TypeArray), uint8(vendorbson.TypeArray)},
		{"TypeBinary", uint8(bson.TypeBinary), uint8(vendorbson.TypeBinary)},
		{"TypeUndefined", uint8(bson.TypeUndefined), uint8(vendorbson.TypeUndefined)},
		{"TypeObjectID", uint8(bson.TypeObjectID), uint8(vendorbson.TypeObjectID)},
		{"TypeBoolean", uint8(bson.TypeBoolean), uint8(vendorbson.TypeBoolean)},
		{"TypeDateTime", uint8(bson.TypeDateTime), uint8(vendorbson.TypeDateTime)},
		{"TypeNull", uint8(bson.TypeNull), uint8(vendorbson.TypeNull)},
		{"TypeRegex", uint8(bson.TypeRegex), uint8(vendorbson.TypeRegex)},
		{"TypeDBPointer", uint8(bson.TypeDBPointer), uint8(vendorbson.TypeDBPointer)},
		{"TypeJavaScript", uint8(bson.TypeJavaScript), uint8(vendorbson.TypeJavaScript)},
		{"TypeSymbol", uint8(bson.TypeSymbol), uint8(vendorbson.TypeSymbol)},
		{"TypeCodeWithScope", uint8(bson.TypeCodeWithScope), uint8(vendorbson.TypeCodeWithScope)},
		{"TypeInt32", uint8(bson.TypeInt32), uint8(vendorbson.TypeInt32)},
		{"TypeTimestamp", uint8(bson.TypeTimestamp), uint8(vendorbson.TypeTimestamp)},
		{"TypeInt64", uint8(bson.TypeInt64), uint8(vendorbson.TypeInt64)},
		{"TypeDecimal128", uint8(bson.TypeDecimal128), uint8(vendorbson.TypeDecimal128)},
		{"TypeMinKey", uint8(bson.TypeMinKey), uint8(vendorbson.TypeMinKey)},
		{"TypeMaxKey", uint8(bson.TypeMaxKey), uint8(vendorbson.TypeMaxKey)},
		{"TypeBinaryUUID", uint8(bson.TypeBinaryUUID), uint8(vendorbson.TypeBinaryUUID)},
		{"TypeBinaryGeneric", uint8(bson.TypeBinaryGeneric), uint8(vendorbson.TypeBinaryGeneric)},
		{"TypeBinaryFunction", uint8(bson.TypeBinaryFunction), uint8(vendorbson.TypeBinaryFunction)},
		{"TypeBinaryBinaryOld", uint8(bson.TypeBinaryBinaryOld), uint8(vendorbson.TypeBinaryBinaryOld)},
		{"TypeBinaryUUIDOld", uint8(bson.TypeBinaryUUIDOld), uint8(vendorbson.TypeBinaryUUIDOld)},
		{"TypeBinaryMD5", uint8(bson.TypeBinaryMD5), uint8(vendorbson.TypeBinaryMD5)},
		{"TypeBinaryEncrypted", uint8(bson.TypeBinaryEncrypted), uint8(vendorbson.TypeBinaryEncrypted)},
		{"TypeBinaryColumn", uint8(bson.TypeBinaryColumn), uint8(vendorbson.TypeBinaryColumn)},
		{"TypeBinarySensitive", uint8(bson.TypeBinarySensitive), uint8(vendorbson.TypeBinarySensitive)},
		{"TypeBinaryUserDefined", uint8(bson.TypeBinaryUserDefined), uint8(vendorbson.TypeBinaryUserDefined)},
	}
	for _, tc := range cases {
		if tc.library != tc.vendor {
			t.Errorf("bson.%s = %d, vendor = %d", tc.name, tc.library, tc.vendor)
		}
	}
}
