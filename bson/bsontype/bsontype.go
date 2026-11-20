// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package bsontype holds the BSON element types and binary subtypes of
// bsonspec.org, and the name of each type for messages.
package bsontype

// The BSON element types, by the type byte that precedes an element's name.
const (
	Double           Type = 0x01
	String           Type = 0x02
	EmbeddedDocument Type = 0x03
	Array            Type = 0x04
	Binary           Type = 0x05
	Undefined        Type = 0x06
	ObjectID         Type = 0x07
	Boolean          Type = 0x08
	DateTime         Type = 0x09
	Null             Type = 0x0A
	Regex            Type = 0x0B
	DBPointer        Type = 0x0C
	JavaScript       Type = 0x0D
	Symbol           Type = 0x0E
	CodeWithScope    Type = 0x0F
	Int32            Type = 0x10
	Timestamp        Type = 0x11
	Int64            Type = 0x12
	Decimal128       Type = 0x13
	MinKey           Type = 0xFF
	MaxKey           Type = 0x7F
)

// The BSON binary subtypes, by the byte after a binary value's length.
const (
	BinaryGeneric     byte = 0x00
	BinaryFunction    byte = 0x01
	BinaryBinaryOld   byte = 0x02
	BinaryUUIDOld     byte = 0x03
	BinaryUUID        byte = 0x04
	BinaryMD5         byte = 0x05
	BinaryEncrypted   byte = 0x06
	BinaryColumn      byte = 0x07
	BinarySensitive   byte = 0x08
	BinaryUserDefined byte = 0x80
)

// Type is a BSON element type byte.
type Type byte

// String names the type for messages, or returns "invalid" for a byte that
// is not a BSON type. The names are part of the mirrored contract: error
// texts built from them reach users unchanged.
func (elementType Type) String() string {
	switch elementType {
	case Double:
		return "double"
	case String:
		return "string"
	case EmbeddedDocument:
		return "embedded document"
	case Array:
		return "array"
	case Binary:
		return "binary"
	case Undefined:
		return "undefined"
	case ObjectID:
		return "objectID"
	case Boolean:
		return "boolean"
	case DateTime:
		return "UTC datetime"
	case Null:
		return "null"
	case Regex:
		return "regex"
	case DBPointer:
		return "dbPointer"
	case JavaScript:
		return "javascript"
	case Symbol:
		return "symbol"
	case CodeWithScope:
		return "code with scope"
	case Int32:
		return "32-bit integer"
	case Timestamp:
		return "timestamp"
	case Int64:
		return "64-bit integer"
	case Decimal128:
		return "128-bit decimal"
	case MinKey:
		return "min key"
	case MaxKey:
		return "max key"
	}
	return "invalid"
}

// IsValid reports whether the byte is one of the BSON element types.
func (elementType Type) IsValid() bool {
	switch elementType {
	case Double, String, EmbeddedDocument, Array, Binary, Undefined, ObjectID, Boolean, DateTime, Null, Regex,
		DBPointer, JavaScript, Symbol, CodeWithScope, Int32, Timestamp, Int64, Decimal128, MinKey, MaxKey:
		return true
	}
	return false
}
