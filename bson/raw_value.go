// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: RawValue's methods on the library's bsoncore; String and DebugString render the ErrNestingTooDeep refusal past MaxNestingCeiling; Unmarshal is declared in entrypoints.go over the library's decoder (build step 4f); the unexported registry field, UnmarshalWithRegistry, UnmarshalWithContext, ErrNilContext and ErrNilRegistry left out, because they need bsoncodec's registry, which spec call C7 does not mirror; the typed accessors' panics carry the mirrored-contract note

package bson

import (
	"bytes"
	"time"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
	"github.com/hoarddb/hoarddb-bson/x/bsonx/bsoncore"
)

// RawValue is a raw encoded BSON value. It can be used to delay BSON value decoding or precompute
// BSON encoded value. Type is the BSON type of the value and Value is the raw encoded BSON value.
//
// A RawValue must be an individual BSON value. Use the Raw type for full BSON documents.
type RawValue struct {
	Type  bsontype.Type
	Value []byte
}

// IsZero reports whether the RawValue is zero, i.e. no data is present on
// the RawValue. It returns true if Type is 0 and Value is empty or nil.
func (rv RawValue) IsZero() bool {
	return rv.Type == 0x00 && len(rv.Value) == 0
}

// Equal compares rv and rv2 and returns true if they are equal.
func (rv RawValue) Equal(rv2 RawValue) bool {
	if rv.Type != rv2.Type {
		return false
	}

	if !bytes.Equal(rv.Value, rv2.Value) {
		return false
	}

	return true
}

func convertFromCoreValue(v bsoncore.Value) RawValue { return RawValue{Type: v.Type, Value: v.Data} }
func convertToCoreValue(v RawValue) bsoncore.Value {
	return bsoncore.Value{Type: v.Type, Data: v.Value}
}

// Validate ensures the value is a valid BSON value.
func (rv RawValue) Validate() error { return convertToCoreValue(rv).Validate() }

// IsNumber returns true if the type of v is a numeric BSON type.
func (rv RawValue) IsNumber() bool { return convertToCoreValue(rv).IsNumber() }

// String implements the fmt.String interface. This method will return values in extended JSON
// format. If the value is not valid, this returns an empty string. A value nested deeper than
// MaxNestingCeiling renders as a refusal naming ErrNestingTooDeep.
func (rv RawValue) String() string {
	coreValue := convertToCoreValue(rv)
	return refuseValuePastCeiling(coreValue.String(), "", coreValue)
}

// DebugString outputs a human readable version of Document. It will attempt to stringify the
// valid components of the document even if the entire document is not valid. A value nested
// deeper than MaxNestingCeiling renders as a refusal naming ErrNestingTooDeep.
func (rv RawValue) DebugString() string {
	coreValue := convertToCoreValue(rv)
	return refuseValuePastCeiling(coreValue.DebugString(), "<malformed>", coreValue)
}

// Double returns the float64 value for this element.
// It panics if e's BSON type is not bsontype.Double.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Double panics where the
// vendor's does, with the same error. DoubleOK is the form that does not panic.
func (rv RawValue) Double() float64 { return convertToCoreValue(rv).Double() }

// DoubleOK is the same as Double, but returns a boolean instead of panicking.
func (rv RawValue) DoubleOK() (float64, bool) { return convertToCoreValue(rv).DoubleOK() }

// StringValue returns the string value for this element.
// It panics if e's BSON type is not bsontype.String.
//
// NOTE: This method is called StringValue to avoid a collision with the String method which
// implements the fmt.Stringer interface.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.StringValue panics where the
// vendor's does, with the same error. StringValueOK is the form that does not panic.
func (rv RawValue) StringValue() string { return convertToCoreValue(rv).StringValue() }

// StringValueOK is the same as StringValue, but returns a boolean instead of
// panicking.
func (rv RawValue) StringValueOK() (string, bool) { return convertToCoreValue(rv).StringValueOK() }

// Document returns the BSON document the Value represents as a Document. It panics if the
// value is a BSON type other than document.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Document panics where the
// vendor's does, with the same error. DocumentOK is the form that does not panic.
func (rv RawValue) Document() Raw { return Raw(convertToCoreValue(rv).Document()) }

// DocumentOK is the same as Document, except it returns a boolean
// instead of panicking.
func (rv RawValue) DocumentOK() (Raw, bool) {
	doc, ok := convertToCoreValue(rv).DocumentOK()
	return Raw(doc), ok
}

// Array returns the BSON array the Value represents as an Array. It panics if the
// value is a BSON type other than array.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Array panics where the
// vendor's does, with the same error. ArrayOK is the form that does not panic.
func (rv RawValue) Array() Raw { return Raw(convertToCoreValue(rv).Array()) }

// ArrayOK is the same as Array, except it returns a boolean instead
// of panicking.
func (rv RawValue) ArrayOK() (Raw, bool) {
	doc, ok := convertToCoreValue(rv).ArrayOK()
	return Raw(doc), ok
}

// Binary returns the BSON binary value the Value represents. It panics if the value is a BSON type
// other than binary.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Binary panics where the
// vendor's does, with the same error. BinaryOK is the form that does not panic.
func (rv RawValue) Binary() (subtype byte, data []byte) { return convertToCoreValue(rv).Binary() }

// BinaryOK is the same as Binary, except it returns a boolean instead of
// panicking.
func (rv RawValue) BinaryOK() (subtype byte, data []byte, ok bool) {
	return convertToCoreValue(rv).BinaryOK()
}

// ObjectID returns the BSON objectid value the Value represents. It panics if the value is a BSON
// type other than objectid.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.ObjectID panics where the
// vendor's does, with the same error. ObjectIDOK is the form that does not panic.
func (rv RawValue) ObjectID() primitive.ObjectID { return convertToCoreValue(rv).ObjectID() }

// ObjectIDOK is the same as ObjectID, except it returns a boolean instead of
// panicking.
func (rv RawValue) ObjectIDOK() (primitive.ObjectID, bool) {
	return convertToCoreValue(rv).ObjectIDOK()
}

// Boolean returns the boolean value the Value represents. It panics if the
// value is a BSON type other than boolean.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Boolean panics where the
// vendor's does, with the same error. BooleanOK is the form that does not panic.
func (rv RawValue) Boolean() bool { return convertToCoreValue(rv).Boolean() }

// BooleanOK is the same as Boolean, except it returns a boolean instead of
// panicking.
func (rv RawValue) BooleanOK() (bool, bool) { return convertToCoreValue(rv).BooleanOK() }

// DateTime returns the BSON datetime value the Value represents as a
// unix timestamp. It panics if the value is a BSON type other than datetime.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.DateTime panics where the
// vendor's does, with the same error. DateTimeOK is the form that does not panic.
func (rv RawValue) DateTime() int64 { return convertToCoreValue(rv).DateTime() }

// DateTimeOK is the same as DateTime, except it returns a boolean instead of
// panicking.
func (rv RawValue) DateTimeOK() (int64, bool) { return convertToCoreValue(rv).DateTimeOK() }

// Time returns the BSON datetime value the Value represents. It panics if the value is a BSON
// type other than datetime.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Time panics where the
// vendor's does, with the same error. TimeOK is the form that does not panic.
func (rv RawValue) Time() time.Time { return convertToCoreValue(rv).Time() }

// TimeOK is the same as Time, except it returns a boolean instead of
// panicking.
func (rv RawValue) TimeOK() (time.Time, bool) { return convertToCoreValue(rv).TimeOK() }

// Regex returns the BSON regex value the Value represents. It panics if the value is a BSON
// type other than regex.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Regex panics where the
// vendor's does, with the same error. RegexOK is the form that does not panic.
func (rv RawValue) Regex() (pattern, options string) { return convertToCoreValue(rv).Regex() }

// RegexOK is the same as Regex, except it returns a boolean instead of
// panicking.
func (rv RawValue) RegexOK() (pattern, options string, ok bool) {
	return convertToCoreValue(rv).RegexOK()
}

// DBPointer returns the BSON dbpointer value the Value represents. It panics if the value is a BSON
// type other than DBPointer.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.DBPointer panics where the
// vendor's does, with the same error. DBPointerOK is the form that does not panic.
func (rv RawValue) DBPointer() (string, primitive.ObjectID) {
	return convertToCoreValue(rv).DBPointer()
}

// DBPointerOK is the same as DBPoitner, except that it returns a boolean
// instead of panicking.
func (rv RawValue) DBPointerOK() (string, primitive.ObjectID, bool) {
	return convertToCoreValue(rv).DBPointerOK()
}

// JavaScript returns the BSON JavaScript code value the Value represents. It panics if the value is
// a BSON type other than JavaScript code.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.JavaScript panics where the
// vendor's does, with the same error. JavaScriptOK is the form that does not panic.
func (rv RawValue) JavaScript() string { return convertToCoreValue(rv).JavaScript() }

// JavaScriptOK is the same as Javascript, excepti that it returns a boolean
// instead of panicking.
func (rv RawValue) JavaScriptOK() (string, bool) { return convertToCoreValue(rv).JavaScriptOK() }

// Symbol returns the BSON symbol value the Value represents. It panics if the value is a BSON
// type other than symbol.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Symbol panics where the
// vendor's does, with the same error. SymbolOK is the form that does not panic.
func (rv RawValue) Symbol() string { return convertToCoreValue(rv).Symbol() }

// SymbolOK is the same as Symbol, excepti that it returns a boolean
// instead of panicking.
func (rv RawValue) SymbolOK() (string, bool) { return convertToCoreValue(rv).SymbolOK() }

// CodeWithScope returns the BSON JavaScript code with scope the Value represents.
// It panics if the value is a BSON type other than JavaScript code with scope.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.CodeWithScope panics where the
// vendor's does, with the same error. CodeWithScopeOK is the form that does not panic.
func (rv RawValue) CodeWithScope() (string, Raw) {
	code, scope := convertToCoreValue(rv).CodeWithScope()
	return code, Raw(scope)
}

// CodeWithScopeOK is the same as CodeWithScope, except that it returns a boolean instead of
// panicking.
func (rv RawValue) CodeWithScopeOK() (string, Raw, bool) {
	code, scope, ok := convertToCoreValue(rv).CodeWithScopeOK()
	return code, Raw(scope), ok
}

// Int32 returns the int32 the Value represents. It panics if the value is a BSON type other than
// int32.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Int32 panics where the
// vendor's does, with the same error. Int32OK is the form that does not panic.
func (rv RawValue) Int32() int32 { return convertToCoreValue(rv).Int32() }

// Int32OK is the same as Int32, except that it returns a boolean instead of
// panicking.
func (rv RawValue) Int32OK() (int32, bool) { return convertToCoreValue(rv).Int32OK() }

// AsInt32 returns a BSON number as an int32. If the BSON type is not a numeric one, this method
// will panic.
//
// Deprecated: Use AsInt64 instead. If an int32 is required, convert the returned value to an int32
// and perform any required overflow/underflow checking.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.AsInt32 panics where the
// vendor's does, with the same error. AsInt32OK is the form that does not panic.
func (rv RawValue) AsInt32() int32 { return convertToCoreValue(rv).AsInt32() }

// AsInt32OK is the same as AsInt32, except that it returns a boolean instead of
// panicking.
//
// Deprecated: Use AsInt64OK instead. If an int32 is required, convert the returned value to an
// int32 and perform any required overflow/underflow checking.
func (rv RawValue) AsInt32OK() (int32, bool) { return convertToCoreValue(rv).AsInt32OK() }

// Timestamp returns the BSON timestamp value the Value represents. It panics if the value is a
// BSON type other than timestamp.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Timestamp panics where the
// vendor's does, with the same error. TimestampOK is the form that does not panic.
func (rv RawValue) Timestamp() (t, i uint32) { return convertToCoreValue(rv).Timestamp() }

// TimestampOK is the same as Timestamp, except that it returns a boolean
// instead of panicking.
func (rv RawValue) TimestampOK() (t, i uint32, ok bool) { return convertToCoreValue(rv).TimestampOK() }

// Int64 returns the int64 the Value represents. It panics if the value is a BSON type other than
// int64.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Int64 panics where the
// vendor's does, with the same error. Int64OK is the form that does not panic.
func (rv RawValue) Int64() int64 { return convertToCoreValue(rv).Int64() }

// Int64OK is the same as Int64, except that it returns a boolean instead of
// panicking.
func (rv RawValue) Int64OK() (int64, bool) { return convertToCoreValue(rv).Int64OK() }

// AsInt64 returns a BSON number as an int64. If the BSON type is not a numeric one, this method
// will panic.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.AsInt64 panics where the
// vendor's does, with the same error. AsInt64OK is the form that does not panic.
func (rv RawValue) AsInt64() int64 { return convertToCoreValue(rv).AsInt64() }

// AsInt64OK is the same as AsInt64, except that it returns a boolean instead of
// panicking.
func (rv RawValue) AsInt64OK() (int64, bool) { return convertToCoreValue(rv).AsInt64OK() }

// Decimal128 returns the decimal the Value represents. It panics if the value is a BSON type other than
// decimal.
//
// The panic is the mirrored contract (spec 5.1, S-4): bsoncore.Value.Decimal128 panics where the
// vendor's does, with the same error. Decimal128OK is the form that does not panic.
func (rv RawValue) Decimal128() primitive.Decimal128 { return convertToCoreValue(rv).Decimal128() }

// Decimal128OK is the same as Decimal128, except that it returns a boolean
// instead of panicking.
func (rv RawValue) Decimal128OK() (primitive.Decimal128, bool) {
	return convertToCoreValue(rv).Decimal128OK()
}
