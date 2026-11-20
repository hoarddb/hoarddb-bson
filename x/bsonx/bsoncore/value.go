// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
// Modified by hoardDB, 2026-10-24: Value's accessors ported from value.go; String, DebugString and the string escaping moved to render.go (escaping rewritten without the encoding/json tables); panics go through newElementTypeError; the read helpers are unexported (read.go)

package bsoncore

import (
	"bytes"
	"time"

	"github.com/hoarddb/hoarddb-bson/bson/bsontype"
	"github.com/hoarddb/hoarddb-bson/bson/primitive"
)

// elementTypeError is the panic value of a typed accessor called on a value of another type.
type elementTypeError struct {
	Method string
	Type   bsontype.Type
}

func newElementTypeError(method string, t bsontype.Type) elementTypeError {
	return elementTypeError{Method: method, Type: t}
}

// Error implements the error interface.
func (ete elementTypeError) Error() string {
	return "Call of " + ete.Method + " on " + ete.Type.String() + " type"
}

// Value represents a BSON value with a type and raw bytes.
type Value struct {
	Type bsontype.Type
	Data []byte
}

// Validate ensures the value is a valid BSON value.
//
// It checks the value's own framing and never descends into a nested
// document: a string needs its length and its terminating zero, a binary its
// subtype byte, and a
// document, array or code-with-scope its minimum length and terminator. The
// input is untrusted, so every refusal is an error and none is a panic.
func (v Value) Validate() error {
	body, _, valid := readValue(v.Data, v.Type)
	if !valid || !framingHolds(v.Type, body) {
		return newInsufficientBytesError(v.Data, v.Data)
	}
	return nil
}

// framingHolds checks the length-prefixed layouts of body, which readValue has
// already cut to the declared length.
func framingHolds(t bsontype.Type, body []byte) bool {
	switch t {
	case bsontype.String, bsontype.JavaScript, bsontype.Symbol:
		return stringFramed(body)
	case bsontype.DBPointer:
		return len(body) >= 4+1+12 && stringFramed(body[:len(body)-12])
	case bsontype.EmbeddedDocument, bsontype.Array:
		return len(body) >= 5 && body[len(body)-1] == 0
	case bsontype.CodeWithScope:
		// length, string, document: 4 + (4 + 1) + 5 bytes at least.
		return len(body) >= 14 && body[len(body)-1] == 0
	case bsontype.Binary:
		// A subtype 0x02 inner length is ReadBinary's to check: it refuses an
		// overrun, so nothing here panics on one.
		return len(body) >= 5
	default:
		return true
	}
}

// stringFramed reports whether body is a length prefix, that many bytes and a
// closing zero among them.
func stringFramed(body []byte) bool {
	declared, rem, ok := ReadLength(body)
	return ok && declared >= 1 && int64(declared) == int64(len(rem)) && rem[declared-1] == 0
}

// IsNumber returns true if the type of v is a numeric BSON type.
func (v Value) IsNumber() bool {
	switch v.Type {
	case bsontype.Double, bsontype.Int32, bsontype.Int64, bsontype.Decimal128:
		return true
	default:
		return false
	}
}

// AsInt32 returns a BSON number as an int32. If the BSON type is not a numeric one, this method
// will panic.
func (v Value) AsInt32() int32 {
	if !v.IsNumber() {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; AsInt32OK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.AsInt32", v.Type))
	}
	var i32 int32
	switch v.Type {
	case bsontype.Double:
		f64, _, ok := readDouble(v.Data)
		if !ok {
			// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; AsInt32OK is the form that does not.
			panic(newInsufficientBytesError(v.Data, v.Data))
		}
		i32 = int32(f64)
	case bsontype.Int32:
		var ok bool
		i32, _, ok = ReadInt32(v.Data)
		if !ok {
			// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; AsInt32OK is the form that does not.
			panic(newInsufficientBytesError(v.Data, v.Data))
		}
	case bsontype.Int64:
		i64, _, ok := ReadInt64(v.Data)
		if !ok {
			// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; AsInt32OK is the form that does not.
			panic(newInsufficientBytesError(v.Data, v.Data))
		}
		i32 = int32(i64)
	case bsontype.Decimal128:
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; AsInt32OK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.AsInt32", v.Type))
	}
	return i32
}

// AsInt32OK functions the same as AsInt32 but returns a boolean instead of panicking. False
// indicates an error.
func (v Value) AsInt32OK() (int32, bool) {
	if !v.IsNumber() {
		return 0, false
	}
	var i32 int32
	switch v.Type {
	case bsontype.Double:
		f64, _, ok := readDouble(v.Data)
		if !ok {
			return 0, false
		}
		i32 = int32(f64)
	case bsontype.Int32:
		var ok bool
		i32, _, ok = ReadInt32(v.Data)
		if !ok {
			return 0, false
		}
	case bsontype.Int64:
		i64, _, ok := ReadInt64(v.Data)
		if !ok {
			return 0, false
		}
		i32 = int32(i64)
	case bsontype.Decimal128:
		return 0, false
	}
	return i32, true
}

// AsInt64 returns a BSON number as an int64. If the BSON type is not a numeric one, this method
// will panic.
func (v Value) AsInt64() int64 {
	if !v.IsNumber() {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; AsInt64OK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.AsInt64", v.Type))
	}
	var i64 int64
	switch v.Type {
	case bsontype.Double:
		f64, _, ok := readDouble(v.Data)
		if !ok {
			// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; AsInt64OK is the form that does not.
			panic(newInsufficientBytesError(v.Data, v.Data))
		}
		i64 = int64(f64)
	case bsontype.Int32:
		var ok bool
		i32, _, ok := ReadInt32(v.Data)
		if !ok {
			// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; AsInt64OK is the form that does not.
			panic(newInsufficientBytesError(v.Data, v.Data))
		}
		i64 = int64(i32)
	case bsontype.Int64:
		var ok bool
		i64, _, ok = ReadInt64(v.Data)
		if !ok {
			// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; AsInt64OK is the form that does not.
			panic(newInsufficientBytesError(v.Data, v.Data))
		}
	case bsontype.Decimal128:
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; AsInt64OK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.AsInt64", v.Type))
	}
	return i64
}

// AsInt64OK functions the same as AsInt64 but returns a boolean instead of panicking. False
// indicates an error.
func (v Value) AsInt64OK() (int64, bool) {
	if !v.IsNumber() {
		return 0, false
	}
	var i64 int64
	switch v.Type {
	case bsontype.Double:
		f64, _, ok := readDouble(v.Data)
		if !ok {
			return 0, false
		}
		i64 = int64(f64)
	case bsontype.Int32:
		var ok bool
		i32, _, ok := ReadInt32(v.Data)
		if !ok {
			return 0, false
		}
		i64 = int64(i32)
	case bsontype.Int64:
		var ok bool
		i64, _, ok = ReadInt64(v.Data)
		if !ok {
			return 0, false
		}
	case bsontype.Decimal128:
		return 0, false
	}
	return i64, true
}

// AsFloat64 returns a BSON number as an float64. If the BSON type is not a numeric one, this method
// will panic.
//
// TODO(GODRIVER-2751): Implement AsFloat64.
// func (v Value) AsFloat64() float64

// AsFloat64OK functions the same as AsFloat64 but returns a boolean instead of panicking. False
// indicates an error.
//
// TODO(GODRIVER-2751): Implement AsFloat64OK.
// func (v Value) AsFloat64OK() (float64, bool)

// Equal compaes v to v2 and returns true if they are equal.
func (v Value) Equal(v2 Value) bool {
	if v.Type != v2.Type {
		return false
	}

	return bytes.Equal(v.Data, v2.Data)
}

// Double returns the float64 value for this element.
// It panics if e's BSON type is not bsontype.Double.
func (v Value) Double() float64 {
	if v.Type != bsontype.Double {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; DoubleOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Double", v.Type))
	}
	f64, _, ok := readDouble(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; DoubleOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return f64
}

// DoubleOK is the same as Double, but returns a boolean instead of panicking.
func (v Value) DoubleOK() (float64, bool) {
	if v.Type != bsontype.Double {
		return 0, false
	}
	f64, _, ok := readDouble(v.Data)
	if !ok {
		return 0, false
	}
	return f64, true
}

// StringValue returns the string balue for this element.
// It panics if e's BSON type is not bsontype.String.
//
// NOTE: This method is called StringValue to avoid a collision with the String method which
// implements the fmt.Stringer interface.
func (v Value) StringValue() string {
	if v.Type != bsontype.String {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; StringValueOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.StringValue", v.Type))
	}
	str, _, ok := readString(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; StringValueOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return str
}

// StringValueOK is the same as StringValue, but returns a boolean instead of
// panicking.
func (v Value) StringValueOK() (string, bool) {
	if v.Type != bsontype.String {
		return "", false
	}
	str, _, ok := readString(v.Data)
	if !ok {
		return "", false
	}
	return str, true
}

// Document returns the BSON document the Value represents as a Document. It panics if the
// value is a BSON type other than document.
func (v Value) Document() Document {
	if v.Type != bsontype.EmbeddedDocument {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; DocumentOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Document", v.Type))
	}
	doc, _, ok := ReadDocument(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; DocumentOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return doc
}

// DocumentOK is the same as Document, except it returns a boolean
// instead of panicking.
func (v Value) DocumentOK() (Document, bool) {
	if v.Type != bsontype.EmbeddedDocument {
		return nil, false
	}
	doc, _, ok := ReadDocument(v.Data)
	if !ok {
		return nil, false
	}
	return doc, true
}

// Array returns the BSON array the Value represents as an Array. It panics if the
// value is a BSON type other than array.
func (v Value) Array() Array {
	if v.Type != bsontype.Array {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; ArrayOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Array", v.Type))
	}
	arr, _, ok := readArray(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; ArrayOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return arr
}

// ArrayOK is the same as Array, except it returns a boolean instead
// of panicking.
func (v Value) ArrayOK() (Array, bool) {
	if v.Type != bsontype.Array {
		return nil, false
	}
	arr, _, ok := readArray(v.Data)
	if !ok {
		return nil, false
	}
	return arr, true
}

// Binary returns the BSON binary value the Value represents. It panics if the value is a BSON type
// other than binary.
func (v Value) Binary() (subtype byte, data []byte) {
	if v.Type != bsontype.Binary {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; BinaryOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Binary", v.Type))
	}
	subtype, data, _, ok := ReadBinary(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; BinaryOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return subtype, data
}

// BinaryOK is the same as Binary, except it returns a boolean instead of
// panicking.
func (v Value) BinaryOK() (subtype byte, data []byte, ok bool) {
	if v.Type != bsontype.Binary {
		return 0x00, nil, false
	}
	subtype, data, _, ok = ReadBinary(v.Data)
	if !ok {
		return 0x00, nil, false
	}
	return subtype, data, true
}

// ObjectID returns the BSON objectid value the Value represents. It panics if the value is a BSON
// type other than objectid.
func (v Value) ObjectID() primitive.ObjectID {
	if v.Type != bsontype.ObjectID {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; ObjectIDOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.ObjectID", v.Type))
	}
	oid, _, ok := readObjectID(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; ObjectIDOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return oid
}

// ObjectIDOK is the same as ObjectID, except it returns a boolean instead of
// panicking.
func (v Value) ObjectIDOK() (primitive.ObjectID, bool) {
	if v.Type != bsontype.ObjectID {
		return primitive.ObjectID{}, false
	}
	oid, _, ok := readObjectID(v.Data)
	if !ok {
		return primitive.ObjectID{}, false
	}
	return oid, true
}

// Boolean returns the boolean value the Value represents. It panics if the
// value is a BSON type other than boolean.
func (v Value) Boolean() bool {
	if v.Type != bsontype.Boolean {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; BooleanOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Boolean", v.Type))
	}
	b, _, ok := readBoolean(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; BooleanOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return b
}

// BooleanOK is the same as Boolean, except it returns a boolean instead of
// panicking.
func (v Value) BooleanOK() (bool, bool) {
	if v.Type != bsontype.Boolean {
		return false, false
	}
	b, _, ok := readBoolean(v.Data)
	if !ok {
		return false, false
	}
	return b, true
}

// DateTime returns the BSON datetime value the Value represents as a
// unix timestamp. It panics if the value is a BSON type other than datetime.
func (v Value) DateTime() int64 {
	if v.Type != bsontype.DateTime {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; DateTimeOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.DateTime", v.Type))
	}
	dt, _, ok := readDateTime(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; DateTimeOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return dt
}

// DateTimeOK is the same as DateTime, except it returns a boolean instead of
// panicking.
func (v Value) DateTimeOK() (int64, bool) {
	if v.Type != bsontype.DateTime {
		return 0, false
	}
	dt, _, ok := readDateTime(v.Data)
	if !ok {
		return 0, false
	}
	return dt, true
}

// Time returns the BSON datetime value the Value represents. It panics if the value is a BSON
// type other than datetime.
func (v Value) Time() time.Time {
	if v.Type != bsontype.DateTime {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; TimeOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Time", v.Type))
	}
	dt, _, ok := readDateTime(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; TimeOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return time.Unix(dt/1000, dt%1000*1000000)
}

// TimeOK is the same as Time, except it returns a boolean instead of
// panicking.
func (v Value) TimeOK() (time.Time, bool) {
	if v.Type != bsontype.DateTime {
		return time.Time{}, false
	}
	dt, _, ok := readDateTime(v.Data)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(dt/1000, dt%1000*1000000), true
}

// Regex returns the BSON regex value the Value represents. It panics if the value is a BSON
// type other than regex.
func (v Value) Regex() (pattern, options string) {
	if v.Type != bsontype.Regex {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; RegexOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Regex", v.Type))
	}
	pattern, options, _, ok := readRegex(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; RegexOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return pattern, options
}

// RegexOK is the same as Regex, except it returns a boolean instead of
// panicking.
func (v Value) RegexOK() (pattern, options string, ok bool) {
	if v.Type != bsontype.Regex {
		return "", "", false
	}
	pattern, options, _, ok = readRegex(v.Data)
	if !ok {
		return "", "", false
	}
	return pattern, options, true
}

// DBPointer returns the BSON dbpointer value the Value represents. It panics if the value is a BSON
// type other than DBPointer.
func (v Value) DBPointer() (string, primitive.ObjectID) {
	if v.Type != bsontype.DBPointer {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; DBPointerOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.DBPointer", v.Type))
	}
	ns, pointer, _, ok := readDBPointer(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; DBPointerOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return ns, pointer
}

// DBPointerOK is the same as DBPoitner, except that it returns a boolean
// instead of panicking.
func (v Value) DBPointerOK() (string, primitive.ObjectID, bool) {
	if v.Type != bsontype.DBPointer {
		return "", primitive.ObjectID{}, false
	}
	ns, pointer, _, ok := readDBPointer(v.Data)
	if !ok {
		return "", primitive.ObjectID{}, false
	}
	return ns, pointer, true
}

// JavaScript returns the BSON JavaScript code value the Value represents. It panics if the value is
// a BSON type other than JavaScript code.
func (v Value) JavaScript() string {
	if v.Type != bsontype.JavaScript {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; JavaScriptOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.JavaScript", v.Type))
	}
	js, _, ok := readJavaScript(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; JavaScriptOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return js
}

// JavaScriptOK is the same as Javascript, excepti that it returns a boolean
// instead of panicking.
func (v Value) JavaScriptOK() (string, bool) {
	if v.Type != bsontype.JavaScript {
		return "", false
	}
	js, _, ok := readJavaScript(v.Data)
	if !ok {
		return "", false
	}
	return js, true
}

// Symbol returns the BSON symbol value the Value represents. It panics if the value is a BSON
// type other than symbol.
func (v Value) Symbol() string {
	if v.Type != bsontype.Symbol {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; SymbolOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Symbol", v.Type))
	}
	symbol, _, ok := readSymbol(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; SymbolOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return symbol
}

// SymbolOK is the same as Symbol, excepti that it returns a boolean
// instead of panicking.
func (v Value) SymbolOK() (string, bool) {
	if v.Type != bsontype.Symbol {
		return "", false
	}
	symbol, _, ok := readSymbol(v.Data)
	if !ok {
		return "", false
	}
	return symbol, true
}

// CodeWithScope returns the BSON JavaScript code with scope the Value represents.
// It panics if the value is a BSON type other than JavaScript code with scope.
func (v Value) CodeWithScope() (string, Document) {
	if v.Type != bsontype.CodeWithScope {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; CodeWithScopeOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.CodeWithScope", v.Type))
	}
	code, scope, _, ok := readCodeWithScope(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; CodeWithScopeOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return code, scope
}

// CodeWithScopeOK is the same as CodeWithScope, except that it returns a boolean instead of
// panicking.
func (v Value) CodeWithScopeOK() (string, Document, bool) {
	if v.Type != bsontype.CodeWithScope {
		return "", nil, false
	}
	code, scope, _, ok := readCodeWithScope(v.Data)
	if !ok {
		return "", nil, false
	}
	return code, scope, true
}

// Int32 returns the int32 the Value represents. It panics if the value is a BSON type other than
// int32.
func (v Value) Int32() int32 {
	if v.Type != bsontype.Int32 {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; Int32OK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Int32", v.Type))
	}
	i32, _, ok := ReadInt32(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; Int32OK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return i32
}

// Int32OK is the same as Int32, except that it returns a boolean instead of
// panicking.
func (v Value) Int32OK() (int32, bool) {
	if v.Type != bsontype.Int32 {
		return 0, false
	}
	i32, _, ok := ReadInt32(v.Data)
	if !ok {
		return 0, false
	}
	return i32, true
}

// Timestamp returns the BSON timestamp value the Value represents. It panics if the value is a
// BSON type other than timestamp.
func (v Value) Timestamp() (t, i uint32) {
	if v.Type != bsontype.Timestamp {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; TimestampOK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Timestamp", v.Type))
	}
	t, i, _, ok := readTimestamp(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; TimestampOK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return t, i
}

// TimestampOK is the same as Timestamp, except that it returns a boolean
// instead of panicking.
func (v Value) TimestampOK() (t, i uint32, ok bool) {
	if v.Type != bsontype.Timestamp {
		return 0, 0, false
	}
	t, i, _, ok = readTimestamp(v.Data)
	if !ok {
		return 0, 0, false
	}
	return t, i, true
}

// Int64 returns the int64 the Value represents. It panics if the value is a BSON type other than
// int64.
func (v Value) Int64() int64 {
	if v.Type != bsontype.Int64 {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; Int64OK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Int64", v.Type))
	}
	i64, _, ok := ReadInt64(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; Int64OK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return i64
}

// Int64OK is the same as Int64, except that it returns a boolean instead of
// panicking.
func (v Value) Int64OK() (int64, bool) {
	if v.Type != bsontype.Int64 {
		return 0, false
	}
	i64, _, ok := ReadInt64(v.Data)
	if !ok {
		return 0, false
	}
	return i64, true
}

// Decimal128 returns the decimal the Value represents. It panics if the value is a BSON type other than
// decimal.
func (v Value) Decimal128() primitive.Decimal128 {
	if v.Type != bsontype.Decimal128 {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; Decimal128OK is the form that does not.
		panic(newElementTypeError("bsoncore.Value.Decimal128", v.Type))
	}
	d128, _, ok := readDecimal128(v.Data)
	if !ok {
		// Mirrored contract (spec 5.1, S-4): this accessor panics where the vendor's does; Decimal128OK is the form that does not.
		panic(newInsufficientBytesError(v.Data, v.Data))
	}
	return d128
}

// Decimal128OK is the same as Decimal128, except that it returns a boolean
// instead of panicking.
func (v Value) Decimal128OK() (primitive.Decimal128, bool) {
	if v.Type != bsontype.Decimal128 {
		return primitive.Decimal128{}, false
	}
	d128, _, ok := readDecimal128(v.Data)
	if !ok {
		return primitive.Decimal128{}, false
	}
	return d128, true
}
